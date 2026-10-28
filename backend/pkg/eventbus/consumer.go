package eventbus

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

// Handler applies one event. It runs inside the transaction tx, which also holds the inbox row for the event,
// so the handler's writes and the "seen" mark commit together or not at all. Do not commit or roll back tx.
// Return an error to retry the event; wrap it with Permanent to dead-letter it without retries.
type Handler func(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error

// Dead-letter record headers, added to the original record's own.
const (
	HeaderDLQError     = "dlq-error"
	HeaderDLQAttempts  = "dlq-attempts"
	HeaderDLQTopic     = "dlq-source-topic"
	HeaderDLQPartition = "dlq-source-partition"
	HeaderDLQOffset    = "dlq-source-offset"
	HeaderDLQGroup     = "dlq-consumer-group"
	HeaderDLQFailedAt  = "dlq-failed-at"
)

// ConsumerConfig configures a Consumer.
type ConsumerConfig struct {
	Brokers []string
	// Group is the Kafka consumer group and the inbox's `consumer` key: one logical consumer, whatever the
	// number of replicas.
	Group  string
	Topics []string
	// DB is the service's own database (inbox table plus whatever the handlers write).
	DB  platform.TxBeginner
	Log *slog.Logger
	// ClientOpts are extra franz-go options, e.g. TLS and SASL.
	ClientOpts []kgo.Opt

	// Zero values take the defaults in parentheses.
	MaxPollRecords int           // records per poll (100)
	MaxAttempts    int           // tries per record before dead-lettering (5)
	BackoffBase    time.Duration // wait before the first retry, doubling each time (200ms)
	BackoffMax     time.Duration // cap on that wait (5s)
}

// Consumer reads topics as one member of a consumer group and applies each event exactly once in effect.
type Consumer struct {
	cfg      ConsumerConfig
	client   *kgo.Client
	handlers map[string]Handler
}

// NewConsumer connects a group member. It reads from the earliest offset when the group is new, and commits
// offsets only after the events they cover are applied (never automatically).
func NewConsumer(cfg ConsumerConfig) (*Consumer, error) {
	if cfg.Group == "" || len(cfg.Topics) == 0 || cfg.DB == nil || cfg.Log == nil {
		return nil, errors.New("eventbus: consumer needs Group, Topics, DB and Log")
	}
	cfg.MaxPollRecords = cmp.Or(cfg.MaxPollRecords, 100)
	cfg.MaxAttempts = cmp.Or(cfg.MaxAttempts, 5)
	cfg.BackoffBase = cmp.Or(cfg.BackoffBase, 200*time.Millisecond)
	cfg.BackoffMax = cmp.Or(cfg.BackoffMax, 5*time.Second)

	opts := []kgo.Opt{
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ClientID(cfg.Group),
		kgo.ConsumerGroup(cfg.Group),
		kgo.ConsumeTopics(cfg.Topics...),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.DisableAutoCommit(),
		// Partitions are not taken away while a polled batch is being handled; AllowRebalance after each batch.
		kgo.BlockRebalanceOnPoll(),
		kgo.RequiredAcks(kgo.AllISRAcks()), // for dead-letter writes
	}
	client, err := kgo.NewClient(append(opts, cfg.ClientOpts...)...)
	if err != nil {
		return nil, fmt.Errorf("eventbus: consumer client: %w", err)
	}
	return &Consumer{cfg: cfg, client: client, handlers: map[string]Handler{}}, nil
}

// Handle registers the handler for an event type. Types without a handler are skipped (their offsets still
// advance), which is what lets a producer add event types to a topic without breaking its consumers. Call it
// before Run.
func (c *Consumer) Handle(eventType string, h Handler) {
	c.handlers[eventType] = h
}

// Run consumes until ctx is cancelled, then leaves the group and returns nil. Per batch it handles the records
// in order, commits the offsets of those fully handled, and allows rebalances. A record that fails
// MaxAttempts times, or fails permanently, is copied to `<topic>.dlq` and the consumer moves on; if the
// dead-letter write itself cannot succeed the consumer keeps retrying it rather than skipping the record.
func (c *Consumer) Run(ctx context.Context) error {
	defer c.client.CloseAllowingRebalance()
	for ctx.Err() == nil {
		fetches := c.client.PollRecords(ctx, c.cfg.MaxPollRecords)
		if fetches.IsClientClosed() {
			return nil
		}
		fetches.EachError(func(topic string, partition int32, err error) {
			if ctx.Err() == nil {
				c.cfg.Log.WarnContext(ctx, "fetch error", "topic", topic, "partition", partition, "error", err)
			}
		})

		records := fetches.Records()
		done := 0
		for _, rec := range records {
			if err := c.process(ctx, rec); err != nil {
				break // only when ctx is cancelled: the rest is redelivered after restart
			}
			done++
		}
		c.commit(records[:done])
		c.client.AllowRebalance()
	}
	return nil
}

// commit records progress for fully handled records. It ignores cancellation of the run's context so that a
// shutdown still saves what was done.
func (c *Consumer) commit(done []*kgo.Record) {
	if len(done) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.client.CommitRecords(ctx, done...); err != nil {
		// Not fatal: the records are redelivered and the inbox makes that harmless.
		c.cfg.Log.Error("offset commit failed", "error", err)
	}
}

// process handles one record to completion: applied, skipped, or dead-lettered. It returns an error only when
// ctx is cancelled before that, in which case the record is left for the next run.
func (c *Consumer) process(ctx context.Context, rec *kgo.Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	env, err := contracts.Decode(rec.Value)
	if err != nil {
		return c.deadLetter(ctx, rec, fmt.Errorf("undecodable record: %w", err), 0)
	}
	handler, ok := c.handlers[env.Type]
	if !ok {
		return nil
	}

	var lastErr error
	for attempt := 1; attempt <= c.cfg.MaxAttempts; attempt++ {
		if lastErr = c.apply(ctx, env, handler); lastErr == nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if isPermanent(lastErr) || attempt == c.cfg.MaxAttempts {
			return c.deadLetter(ctx, rec, lastErr, attempt)
		}
		wait := backoff(c.cfg.BackoffBase, c.cfg.BackoffMax, attempt)
		c.cfg.Log.WarnContext(platform.WithTraceID(ctx, env.TraceID), "event handler failed, retrying",
			"type", env.Type, "eventId", env.ID, "attempt", attempt, "retryIn", wait, "error", lastErr)
		sleep(ctx, wait)
	}
	return nil // unreachable: MaxAttempts >= 1 always ends in a return above
}

// apply runs the handler for env in one transaction together with the inbox insert. A duplicate (the inbox
// already has the event) is skipped without calling the handler.
func (c *Consumer) apply(ctx context.Context, env contracts.Envelope, handler Handler) (err error) {
	ctx = platform.WithTraceID(ctx, env.TraceID)
	tx, err := c.cfg.DB.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	tag, err := tx.Exec(ctx, `INSERT INTO inbox (consumer, event_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, c.cfg.Group, env.ID)
	if err != nil {
		return fmt.Errorf("inbox: %w", err)
	}
	if tag.RowsAffected() == 0 {
		c.cfg.Log.DebugContext(ctx, "duplicate event skipped", "type", env.Type, "eventId", env.ID)
		return nil
	}

	if err := callHandler(ctx, handler, tx, env); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// callHandler turns a handler panic into an error so one bad event cannot kill the consumer.
func callHandler(ctx context.Context, h Handler, tx pgx.Tx, env contracts.Envelope) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("handler panic: %v", rec)
		}
	}()
	return h(ctx, tx, env)
}

// deadLetter copies rec to its dead-letter topic with headers saying why, and returns once that write is
// durable. It retries the write until it succeeds, and gives up only when ctx is cancelled.
func (c *Consumer) deadLetter(ctx context.Context, rec *kgo.Record, cause error, attempts int) error {
	headers := append([]kgo.RecordHeader(nil), rec.Headers...)
	headers = append(headers,
		kgo.RecordHeader{Key: HeaderDLQError, Value: []byte(cause.Error())},
		kgo.RecordHeader{Key: HeaderDLQAttempts, Value: []byte(strconv.Itoa(attempts))},
		kgo.RecordHeader{Key: HeaderDLQTopic, Value: []byte(rec.Topic)},
		kgo.RecordHeader{Key: HeaderDLQPartition, Value: []byte(strconv.Itoa(int(rec.Partition)))},
		kgo.RecordHeader{Key: HeaderDLQOffset, Value: []byte(strconv.FormatInt(rec.Offset, 10))},
		kgo.RecordHeader{Key: HeaderDLQGroup, Value: []byte(c.cfg.Group)},
		kgo.RecordHeader{Key: HeaderDLQFailedAt, Value: []byte(time.Now().UTC().Format(time.RFC3339))},
	)
	c.cfg.Log.ErrorContext(ctx, "record dead-lettered",
		"topic", rec.Topic, "partition", rec.Partition, "offset", rec.Offset, "attempts", attempts, "error", cause)

	for attempt := 1; ; attempt++ {
		dlq := &kgo.Record{Topic: contracts.DLQTopic(rec.Topic), Key: rec.Key, Value: rec.Value, Headers: headers}
		err := c.client.ProduceSync(ctx, dlq).FirstErr()
		if err == nil {
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		wait := backoff(c.cfg.BackoffBase, c.cfg.BackoffMax, attempt)
		c.cfg.Log.ErrorContext(ctx, "dead-letter write failed, retrying", "topic", dlq.Topic, "retryIn", wait, "error", err)
		sleep(ctx, wait)
	}
}
