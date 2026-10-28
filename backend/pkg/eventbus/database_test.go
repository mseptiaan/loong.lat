package eventbus

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus/eventbustest"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform/platformtest"
)

// These tests need a real Postgres (TEST_DATABASE_URL, see scripts/dev-up.sh) and skip without one. Kafka is
// still the in-process fake.

// newDB returns a pool on a fresh schema with the eventbus tables and an `effects` table that stands in for
// a service's own data.
func newDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := platformtest.Pool(t)
	migrations := append(Migrations(), platform.Migration{
		ID:  "test/effects",
		SQL: `CREATE TABLE effects (id bigserial PRIMARY KEY, event_id text NOT NULL, note text NOT NULL DEFAULT '')`,
	})
	if _, err := platform.Migrate(t.Context(), pool, migrations...); err != nil {
		t.Fatal(err)
	}
	return pool
}

func count(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// enqueue commits a transaction that enqueues one stop.arrived per call; version n of route key.
func enqueue(t *testing.T, ctx context.Context, pool *pgxpool.Pool, pub *Publisher, key string, version int64) {
	t.Helper()
	err := platform.WithTx(ctx, pool, func(tx pgx.Tx) error {
		return pub.Enqueue(ctx, tx, Event{
			Type: contracts.TypeStopArrived, Key: key, AggregateVersion: version,
			Data: contracts.StopArrived{PlanID: "plan-1", RouteID: key, StopID: "stop-" + strconv.FormatInt(version, 10)},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// runRelay runs pub.Run until the returned stop is called.
func runRelay(t *testing.T, pub *Publisher) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- pub.Run(ctx) }()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("relay returned %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Error("relay did not stop")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

// perKeyVersionsAscend checks that the events of each key arrive in version order and that every event id is
// distinct (a duplicate would show as a repeated id).
func perKeyVersionsAscend(t *testing.T, records []*kgo.Record) {
	t.Helper()
	last := map[string]int64{}
	ids := map[string]bool{}
	for _, rec := range records { // records of one partition are in order; a key lives in one partition
		env, err := contracts.Decode(rec.Value)
		if err != nil {
			t.Fatal(err)
		}
		if ids[env.ID] {
			t.Errorf("event %s was published twice", env.ID)
		}
		ids[env.ID] = true
		if env.AggregateVersion <= last[env.Key] {
			t.Errorf("%s: version %d arrived after %d", env.Key, env.AggregateVersion, last[env.Key])
		}
		last[env.Key] = env.AggregateVersion
	}
}

func TestMigrationsApplyOnceAndAreIdempotent(t *testing.T) {
	pool := platformtest.Pool(t)
	applied, err := platform.Migrate(t.Context(), pool, Migrations()...)
	if err != nil || len(applied) != 1 {
		t.Fatalf("first run applied %v, %v", applied, err)
	}
	again, err := platform.Migrate(t.Context(), pool, Migrations()...)
	if err != nil || len(again) != 0 {
		t.Errorf("second run applied %v, %v; want nothing", again, err)
	}
}

func TestRelayPublishesCommittedEventsAndMarksThemPublished(t *testing.T) {
	pool := newDB(t)
	brokers := eventbustest.NewCluster(t, contracts.TopicExecution)
	client, err := NewProducerClient(brokers, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	pub := NewPublisher("driver-api", pool, client, discardLog, WithPollInterval(5*time.Millisecond))

	for v := int64(1); v <= 5; v++ {
		for _, key := range []string{"rt-1", "rt-2", "rt-3"} {
			enqueue(t, t.Context(), pool, pub, key, v)
		}
	}
	// A rolled-back transaction must leave nothing to publish.
	rollback := errors.New("business rule failed")
	err = platform.WithTx(t.Context(), pool, func(tx pgx.Tx) error {
		if err := pub.Enqueue(t.Context(), tx, Event{Type: contracts.TypeStopArrived, Key: "rt-ghost", Data: contracts.StopArrived{}}); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM outbox`); n != 15 {
		t.Fatalf("outbox has %d rows, want 15", n)
	}

	runRelay(t, pub)
	records := eventbustest.ReadTopic(t, brokers, contracts.TopicExecution, 15)
	if len(records) != 15 {
		t.Fatalf("topic has %d records, want 15", len(records))
	}
	perKeyVersionsAscend(t, records)
	for _, rec := range records {
		if string(rec.Key) == "rt-ghost" {
			t.Error("an event from a rolled-back transaction was published")
		}
		if headerValue(rec, contracts.HeaderType) != contracts.TypeStopArrived || headerValue(rec, contracts.HeaderTraceparent) == "" {
			t.Errorf("headers = %+v", rec.Headers)
		}
	}
	eventually(t, 15*time.Second, "all rows marked published", func() bool {
		return count(t, pool, `SELECT count(*) FROM outbox WHERE published_at IS NULL`) == 0
	})
}

func TestEndToEndEnqueueRelayConsumeKeepsTheTrace(t *testing.T) {
	pool := newDB(t)
	brokers := eventbustest.NewCluster(t, contracts.TopicExecution)
	client, err := NewProducerClient(brokers, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	pub := NewPublisher("driver-api", pool, client, discardLog, WithPollInterval(5*time.Millisecond))

	traceID := platform.NewTraceID()
	ctx := platform.WithTraceID(t.Context(), traceID)
	enqueue(t, ctx, pool, pub, "rt-1", 1)
	runRelay(t, pub)

	consumer, err := NewConsumer(ConsumerConfig{
		Brokers: brokers, Group: "console-api", Topics: []string{contracts.TopicExecution}, DB: pool, Log: discardLog,
	})
	if err != nil {
		t.Fatal(err)
	}
	var gotTrace string
	consumer.Handle(contracts.TypeStopArrived, func(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
		gotTrace = platform.TraceID(ctx)
		_, err := tx.Exec(ctx, `INSERT INTO effects (event_id) VALUES ($1)`, env.ID)
		return err
	})
	runConsumer(t, consumer)

	eventually(t, 15*time.Second, "the consumer to apply the event", func() bool { return count(t, pool, `SELECT count(*) FROM effects`) == 1 })
	if gotTrace != traceID {
		t.Errorf("the handler saw trace %q, want %q: the trace must survive Kafka", gotTrace, traceID)
	}
}

func runConsumer(t *testing.T, c *Consumer) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); _ = c.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("consumer did not stop")
		}
	})
}

func TestConsumerAppliesAnEventDeliveredTwiceOnce(t *testing.T) {
	pool := newDB(t)
	brokers := eventbustest.NewCluster(t, contracts.TopicExecution)

	rec := eventbustest.Record(t, "driver-api", contracts.TypeStopArrived, "rt-1", 1, contracts.StopArrived{RouteID: "rt-1", StopID: "stop-1"})
	again := &kgo.Record{Topic: rec.Topic, Key: rec.Key, Value: rec.Value, Headers: rec.Headers}
	other := eventbustest.Record(t, "driver-api", contracts.TypeStopArrived, "rt-1", 2, contracts.StopArrived{RouteID: "rt-1", StopID: "stop-2"})
	eventbustest.Produce(t, brokers, rec, again, other)

	consumer, err := NewConsumer(ConsumerConfig{
		Brokers: brokers, Group: "console-api", Topics: []string{contracts.TopicExecution}, DB: pool, Log: discardLog,
	})
	if err != nil {
		t.Fatal(err)
	}
	consumer.Handle(contracts.TypeStopArrived, func(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
		_, err := tx.Exec(ctx, `INSERT INTO effects (event_id) VALUES ($1)`, env.ID)
		return err
	})
	runConsumer(t, consumer)

	eventually(t, 15*time.Second, "both distinct events applied", func() bool { return count(t, pool, `SELECT count(*) FROM effects`) >= 2 })
	time.Sleep(300 * time.Millisecond) // a wrongly applied duplicate would land by now
	if n := count(t, pool, `SELECT count(*) FROM effects`); n != 2 {
		t.Errorf("%d effects for 3 deliveries of 2 events, want 2", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM inbox WHERE consumer = 'console-api'`); n != 2 {
		t.Errorf("inbox has %d rows, want 2", n)
	}
}

func TestAFailedHandlerLeavesNeitherEffectNorInboxRow(t *testing.T) {
	pool := newDB(t)
	brokers := eventbustest.NewCluster(t, contracts.TopicExecution)
	eventbustest.Produce(t, brokers, eventbustest.Record(t, "driver-api", contracts.TypeStopArrived, "rt-1", 1, contracts.StopArrived{RouteID: "rt-1"}))

	consumer, err := NewConsumer(ConsumerConfig{
		Brokers: brokers, Group: "console-api", Topics: []string{contracts.TopicExecution}, DB: pool, Log: discardLog,
		MaxAttempts: 3, BackoffBase: time.Millisecond, BackoffMax: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	attempts := 0
	consumer.Handle(contracts.TypeStopArrived, func(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
		attempts++
		if _, err := tx.Exec(ctx, `INSERT INTO effects (event_id, note) VALUES ($1, $2)`, env.ID, "attempt "+strconv.Itoa(attempts)); err != nil {
			return err
		}
		if attempts < 3 {
			return errors.New("route not projected yet")
		}
		return nil
	})
	runConsumer(t, consumer)

	eventually(t, 15*time.Second, "the third attempt to commit", func() bool { return count(t, pool, `SELECT count(*) FROM effects`) >= 1 })
	time.Sleep(100 * time.Millisecond)
	var notes []string
	rows, err := pool.Query(t.Context(), `SELECT note FROM effects ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var note string
		if err := rows.Scan(&note); err != nil {
			t.Fatal(err)
		}
		notes = append(notes, note)
	}
	if !slices.Equal(notes, []string{"attempt 3"}) {
		t.Errorf("effects %v: the failed attempts must have rolled back", notes)
	}
	if n := count(t, pool, `SELECT count(*) FROM inbox`); n != 1 {
		t.Errorf("inbox has %d rows, want 1", n)
	}
}

// The relay is killed in the middle of a batch (the broker accepts four events, then dies): nothing may be
// lost or duplicated once a fresh relay takes over, and per-key order must hold.
func TestRelayRestartedMidBatchLosesNothing(t *testing.T) {
	pool := newDB(t)
	brokers := eventbustest.NewCluster(t, contracts.TopicExecution)
	client, err := NewProducerClient(brokers, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	fast := []PublisherOption{WithPollInterval(5 * time.Millisecond), WithRetryBackoff(5*time.Millisecond, 20*time.Millisecond), WithBatchSize(10)}

	writer := NewPublisher("driver-api", pool, client, discardLog, fast...)
	const total = 10
	for v := int64(1); v <= total/2; v++ {
		enqueue(t, t.Context(), pool, writer, "rt-1", v)
		enqueue(t, t.Context(), pool, writer, "rt-2", v)
	}

	first := NewPublisher("driver-api", pool, client, discardLog, fast...)
	first.producer = &flakyProducer{real: client, sendFirst: 4}
	stop := runRelay(t, first)
	eventually(t, 15*time.Second, "the relay to fail after four events", func() bool {
		return count(t, pool, `SELECT count(*) FROM outbox WHERE published_at IS NOT NULL`) == 4 &&
			count(t, pool, `SELECT count(*) FROM outbox WHERE last_error IS NOT NULL`) >= 1
	})
	stop() // the relay process dies with six events unsent

	if n := count(t, pool, `SELECT count(*) FROM outbox WHERE published_at IS NULL`); n != total-4 {
		t.Fatalf("%d events left unpublished, want %d", n, total-4)
	}

	second := NewPublisher("driver-api", pool, client, discardLog, fast...)
	runRelay(t, second)
	eventually(t, 15*time.Second, "the new relay to drain the outbox", func() bool {
		return count(t, pool, `SELECT count(*) FROM outbox WHERE published_at IS NULL`) == 0
	})

	records := eventbustest.ReadTopic(t, brokers, contracts.TopicExecution, total)
	if len(records) != total {
		t.Fatalf("topic has %d events, want exactly %d (none lost, none repeated)", len(records), total)
	}
	perKeyVersionsAscend(t, records)
}

// Two relays may run at once (replicas): rows another relay holds locked must be skipped, not waited for.
func TestRelaySkipsRowsLockedByAnotherRelay(t *testing.T) {
	pool := newDB(t)
	brokers := eventbustest.NewCluster(t, contracts.TopicExecution)
	client, err := NewProducerClient(brokers, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	pub := NewPublisher("driver-api", pool, client, discardLog, WithPollInterval(5*time.Millisecond))
	for i := range 6 {
		enqueue(t, t.Context(), pool, pub, "rt-"+strconv.Itoa(i), 1)
	}

	// Another relay is in the middle of a batch: it holds the two oldest rows.
	other, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Rollback(context.WithoutCancel(t.Context()))
	rows, err := other.Query(t.Context(), `SELECT seq FROM outbox ORDER BY seq LIMIT 2 FOR UPDATE`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
	}
	rows.Close()

	runRelay(t, pub)
	eventually(t, 15*time.Second, "the unlocked rows to be published without waiting for the locked ones", func() bool {
		return count(t, pool, `SELECT count(*) FROM outbox WHERE published_at IS NOT NULL`) == 4
	})

	if err := other.Rollback(t.Context()); err != nil { // that relay died; its rows are free again
		t.Fatal(err)
	}
	eventually(t, 15*time.Second, "the released rows to be published", func() bool {
		return count(t, pool, `SELECT count(*) FROM outbox WHERE published_at IS NULL`) == 0
	})
	if records := eventbustest.ReadTopic(t, brokers, contracts.TopicExecution, 6); len(records) != 6 {
		t.Errorf("topic has %d events, want 6", len(records))
	}
}
