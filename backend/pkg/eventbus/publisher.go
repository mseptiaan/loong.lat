package eventbus

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

// Event is a fact a service publishes.
type Event struct {
	// Type is one of the contracts.Type* constants; it decides the topic.
	Type string
	// Key is the aggregate id: it is the Kafka key, so events with one key stay ordered.
	Key string
	// AggregateVersion increases by one per change of the aggregate (see contracts.Envelope).
	AggregateVersion int64
	// CausationID is the id of the event being handled when this one is a reaction to it; empty otherwise.
	CausationID string
	// Data is the payload struct from contracts.
	Data any
}

// producer is the part of *kgo.Client the relay needs; tests substitute one that fails on demand.
type producer interface {
	ProduceSync(ctx context.Context, rs ...*kgo.Record) kgo.ProduceResults
}

// Publisher writes events to the outbox in the caller's transaction (Enqueue) and relays them to Kafka (Run).
type Publisher struct {
	source   string
	db       platform.TxBeginner
	producer producer
	log      *slog.Logger

	batchSize      int
	pollInterval   time.Duration
	produceTimeout time.Duration
	retryBase      time.Duration
	retryMax       time.Duration
}

// PublisherOption tunes a Publisher.
type PublisherOption func(*Publisher)

// WithBatchSize sets how many outbox rows the relay publishes per round (default 100).
func WithBatchSize(n int) PublisherOption { return func(p *Publisher) { p.batchSize = max(1, n) } }

// WithPollInterval sets how often the relay looks for new rows when idle (default 100ms).
func WithPollInterval(d time.Duration) PublisherOption {
	return func(p *Publisher) { p.pollInterval = d }
}

// WithRetryBackoff sets the bounds of the exponential wait after a failed round (default 200ms to 10s).
func WithRetryBackoff(base, maxWait time.Duration) PublisherOption {
	return func(p *Publisher) { p.retryBase, p.retryMax = base, maxWait }
}

// NewPublisher returns a Publisher for the service named source (the envelope's `source`, e.g. "console-api").
// db is the service's own database, which holds the outbox table (see Migrations); client sends to Kafka and
// comes from NewProducerClient.
func NewPublisher(source string, db platform.TxBeginner, client *kgo.Client, log *slog.Logger, opts ...PublisherOption) *Publisher {
	p := &Publisher{
		source: source, db: db, producer: client, log: log,
		batchSize: 100, pollInterval: 100 * time.Millisecond, produceTimeout: 30 * time.Second,
		retryBase: 200 * time.Millisecond, retryMax: 10 * time.Second,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Enqueue records ev in the outbox as part of tx, so it exists if and only if the surrounding change commits.
// The trace id comes from ctx (a new one if there is none). Nothing is sent to Kafka here; Run does that.
func (p *Publisher) Enqueue(ctx context.Context, tx pgx.Tx, ev Event) error {
	topic, ok := contracts.TopicFor(ev.Type)
	if !ok {
		return fmt.Errorf("eventbus: unknown event type %q", ev.Type)
	}
	env, err := contracts.NewEnvelope(p.source, ev.Type, ev.Key, ev.AggregateVersion, ev.Data)
	if err != nil {
		return fmt.Errorf("eventbus: enqueue %s: %w", ev.Type, err)
	}
	env.CausationID = ev.CausationID
	env.TraceID = platform.TraceID(ctx)
	if !contracts.ValidTraceID(env.TraceID) {
		env.TraceID = platform.NewTraceID()
	}
	payload, err := contracts.Encode(env)
	if err != nil {
		return fmt.Errorf("eventbus: enqueue %s: %w", ev.Type, err)
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO outbox (event_id, topic, key, event_type, traceparent, payload) VALUES ($1, $2, $3, $4, $5, $6)`,
		env.ID, topic, env.Key, env.Type, env.Traceparent(), string(payload))
	if err != nil {
		return fmt.Errorf("eventbus: enqueue %s: %w", ev.Type, err)
	}
	return nil
}

// Run relays outbox rows to Kafka until ctx is cancelled. Each round it locks a batch of unpublished rows
// (FOR UPDATE SKIP LOCKED), sends them with ProduceSync (idempotent, acks=all) and marks the sent ones
// published; failures back off exponentially and are retried forever, so an outage delays events but loses
// none. Stopping is graceful: a round already under way finishes and is recorded before Run returns.
//
// Rows are sent in seq order, which keeps the events of one key ordered when a single relay runs per database.
// With several relays, SKIP LOCKED lets them take different rows concurrently; consumers then rely on
// AggregateVersion to discard anything that arrives out of order. A crash between sending and marking
// re-sends a row; the consumer's inbox absorbs the duplicate.
func (p *Publisher) Run(ctx context.Context) error {
	failures := 0
	for ctx.Err() == nil {
		n, err := p.relayBatch(ctx)
		var wait time.Duration
		switch {
		case err != nil:
			failures++
			wait = backoff(p.retryBase, p.retryMax, failures)
			p.log.ErrorContext(ctx, "outbox relay round failed", "error", err, "retryIn", wait)
		case n == p.batchSize:
			failures = 0
			continue // a full batch suggests more waiting
		default:
			failures = 0
			wait = p.pollInterval
		}
		sleep(ctx, wait)
	}
	return nil
}

type outboxRow struct {
	seq         int64
	eventID     string
	topic       string
	key         string
	eventType   string
	traceparent *string
	payload     string
}

func (r outboxRow) record() *kgo.Record {
	headers := []kgo.RecordHeader{{Key: contracts.HeaderType, Value: []byte(r.eventType)}}
	if r.traceparent != nil && *r.traceparent != "" {
		headers = append(headers, kgo.RecordHeader{Key: contracts.HeaderTraceparent, Value: []byte(*r.traceparent)})
	}
	return &kgo.Record{Topic: r.topic, Key: []byte(r.key), Value: []byte(r.payload), Headers: headers}
}

// relayBatch publishes one batch and returns how many rows it handled (all published when err is nil).
func (p *Publisher) relayBatch(ctx context.Context) (int, error) {
	// The round in flight finishes even if ctx is cancelled meanwhile: its rows are locked and sending them
	// without marking them would only produce duplicates.
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), p.produceTimeout)
	defer cancel()

	tx, err := p.db.Begin(work)
	if err != nil {
		return 0, fmt.Errorf("eventbus: relay begin: %w", err)
	}
	defer func() { _ = tx.Rollback(work) }()

	batch, err := claimBatch(work, tx, p.batchSize)
	if err != nil || len(batch) == 0 {
		return 0, err
	}

	records := make([]*kgo.Record, len(batch))
	for i, row := range batch {
		records[i] = row.record()
	}
	failed := map[*kgo.Record]error{}
	for _, res := range p.producer.ProduceSync(work, records...) {
		if res.Err != nil {
			failed[res.Record] = res.Err
		}
	}

	// ProduceSync reports in completion order. Mark the rows before the first failure: sending later rows of
	// the same key ahead of a failed one would reorder them on retry.
	published := 0
	var produceErr error
	for i, rec := range records {
		if err := failed[rec]; err != nil {
			produceErr = err
			break
		}
		published = i + 1
	}
	if published > 0 {
		seqs := make([]int64, published)
		for i := range published {
			seqs[i] = batch[i].seq
		}
		if _, err := tx.Exec(work, `UPDATE outbox SET published_at = now(), attempts = attempts + 1 WHERE seq = ANY($1)`, seqs); err != nil {
			return 0, fmt.Errorf("eventbus: mark published: %w", err)
		}
	}
	if produceErr != nil {
		if _, err := tx.Exec(work, `UPDATE outbox SET attempts = attempts + 1, last_error = $2 WHERE seq = $1`, batch[published].seq, produceErr.Error()); err != nil {
			return 0, fmt.Errorf("eventbus: record failure: %w", err)
		}
	}
	if err := tx.Commit(work); err != nil {
		return 0, fmt.Errorf("eventbus: relay commit: %w", err)
	}
	if produceErr != nil {
		return published, fmt.Errorf("eventbus: produce %s: %w", batch[published].eventType, produceErr)
	}
	return len(batch), nil
}

func claimBatch(ctx context.Context, tx pgx.Tx, limit int) ([]outboxRow, error) {
	rows, err := tx.Query(ctx,
		`SELECT seq, event_id, topic, key, event_type, traceparent, payload
		   FROM outbox
		  WHERE published_at IS NULL
		  ORDER BY seq
		  LIMIT $1
		    FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return nil, fmt.Errorf("eventbus: claim outbox rows: %w", err)
	}
	defer rows.Close()
	var batch []outboxRow
	for rows.Next() {
		var r outboxRow
		if err := rows.Scan(&r.seq, &r.eventID, &r.topic, &r.key, &r.eventType, &r.traceparent, &r.payload); err != nil {
			return nil, fmt.Errorf("eventbus: scan outbox row: %w", err)
		}
		batch = append(batch, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("eventbus: claim outbox rows: %w", err)
	}
	return batch, nil
}

// backoff is the wait before retry number attempt (1-based): base doubling up to maxWait, with jitter so
// replicas do not retry in lockstep.
func backoff(base, maxWait time.Duration, attempt int) time.Duration {
	d := base
	for i := 1; i < attempt && d < maxWait; i++ {
		d *= 2
	}
	d = min(d, maxWait)
	return d/2 + rand.N(d/2+1)
}

// sleep waits for d or until ctx is done.
func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
