package eventbus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus/eventbustest"
)

var discardLog = slog.New(slog.DiscardHandler)

// newTestConsumer builds a consumer with short backoffs and three attempts, closed when the test ends.
func newTestConsumer(t *testing.T, brokers []string, db *fakeDB, group string, topics ...string) *Consumer {
	t.Helper()
	c, err := NewConsumer(ConsumerConfig{
		Brokers: brokers, Group: group, Topics: topics, DB: db, Log: discardLog,
		MaxAttempts: 3, BackoffBase: time.Millisecond, BackoffMax: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.client.Close)
	return c
}

// start runs the consumer and returns a function that stops it and waits for Run to return.
func start(t *testing.T, c *Consumer) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("Run returned %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Error("consumer did not stop")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

type seen struct {
	mu   sync.Mutex
	list []string
}

func (s *seen) add(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.list = append(s.list, v)
}

func (s *seen) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.list)
}

func (s *seen) count() int { return len(s.all()) }

func offline(t *testing.T, driverID string, version int64) *kgo.Record {
	t.Helper()
	return eventbustest.Record(t, "driver-api", contracts.TypeDriverOffline, driverID, version, contracts.DriverOffline{DriverID: driverID})
}

func TestConsumerKeepsEventsOfOneKeyOrdered(t *testing.T) {
	brokers := eventbustest.NewCluster(t, contracts.TopicDriver)
	db := newFakeDB()
	c := newTestConsumer(t, brokers, db, "ordered", contracts.TopicDriver)

	var mu sync.Mutex
	versions := map[string][]int64{}
	c.Handle(contracts.TypeDriverOffline, func(_ context.Context, _ pgx.Tx, env contracts.Envelope) error {
		mu.Lock()
		defer mu.Unlock()
		versions[env.Key] = append(versions[env.Key], env.AggregateVersion)
		return nil
	})

	const keys, perKey = 5, 20
	var records []*kgo.Record
	for v := int64(1); v <= perKey; v++ { // interleave keys so ordering is not an accident of batching
		for k := range keys {
			records = append(records, offline(t, "drv-"+strconv.Itoa(k), v))
		}
	}
	eventbustest.Produce(t, brokers, records...)
	start(t, c)

	eventually(t, 10*time.Second, "all events handled", func() bool {
		mu.Lock()
		defer mu.Unlock()
		total := 0
		for _, v := range versions {
			total += len(v)
		}
		return total == keys*perKey
	})
	mu.Lock()
	defer mu.Unlock()
	for key, got := range versions {
		if !slices.IsSorted(got) || len(got) != perKey {
			t.Errorf("%s: versions %v are not 1..%d in order", key, got, perKey)
		}
	}
}

func TestConsumerSendsPoisonRecordsToDLQAndKeepsGoing(t *testing.T) {
	brokers := eventbustest.NewCluster(t, contracts.TopicDriver)
	db := newFakeDB()
	c := newTestConsumer(t, brokers, db, "poison", contracts.TopicDriver)

	var failing atomic.Int32
	var handled seen
	c.Handle(contracts.TypeDriverOffline, func(_ context.Context, _ pgx.Tx, env contracts.Envelope) error {
		handled.add(env.Key)
		return nil
	})
	c.Handle(contracts.TypeDriverOnline, func(context.Context, pgx.Tx, contracts.Envelope) error {
		failing.Add(1)
		return errors.New("driver row not there yet")
	})

	garbage := &kgo.Record{Topic: contracts.TopicDriver, Key: []byte("junk"), Value: []byte("not an envelope")}
	failingEvent := eventbustest.Record(t, "driver-api", contracts.TypeDriverOnline, "drv-bad", 1, contracts.DriverOnline{DriverID: "drv-bad"})
	eventbustest.Produce(t, brokers, offline(t, "before", 1), garbage, failingEvent, offline(t, "after", 1))
	start(t, c)

	dlq := eventbustest.ReadTopic(t, brokers, contracts.DLQTopic(contracts.TopicDriver), 2)
	if len(dlq) != 2 {
		t.Fatalf("DLQ has %d records, want 2", len(dlq))
	}
	byKey := map[string]*kgo.Record{}
	for _, rec := range dlq {
		byKey[string(rec.Key)] = rec
	}

	bad := byKey["drv-bad"]
	if bad == nil {
		t.Fatal("the failing event is not in the DLQ")
	}
	if !bytes.Equal(bad.Value, failingEvent.Value) {
		t.Error("DLQ record must keep the original value")
	}
	for header, want := range map[string]string{
		HeaderDLQAttempts: "3", HeaderDLQTopic: contracts.TopicDriver, HeaderDLQGroup: "poison",
		HeaderDLQError: "driver row not there yet", contracts.HeaderType: contracts.TypeDriverOnline,
	} {
		if got := headerValue(bad, header); got != want {
			t.Errorf("DLQ header %s = %q, want %q", header, got, want)
		}
	}
	for _, header := range []string{HeaderDLQPartition, HeaderDLQOffset, HeaderDLQFailedAt} {
		if headerValue(bad, header) == "" {
			t.Errorf("DLQ header %s is missing", header)
		}
	}
	if got := headerValue(byKey["junk"], HeaderDLQAttempts); got != "0" {
		t.Errorf("an undecodable record is not retried, attempts = %q", got)
	}
	if got := failing.Load(); got != 3 {
		t.Errorf("failing handler ran %d times, want MaxAttempts = 3", got)
	}

	eventually(t, 10*time.Second, "records around the poison pill handled", func() bool { return handled.count() == 2 })
	if got := handled.all(); !slices.Equal(got, []string{"before", "after"}) {
		t.Errorf("handled %v, want [before after]", got)
	}
}

func headerValue(rec *kgo.Record, key string) string {
	if rec == nil {
		return ""
	}
	for _, h := range rec.Headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

func TestConsumerRetriesTransientErrorsThenSucceeds(t *testing.T) {
	brokers := eventbustest.NewCluster(t, contracts.TopicDriver)
	db := newFakeDB()
	c := newTestConsumer(t, brokers, db, "transient", contracts.TopicDriver)

	var calls atomic.Int32
	c.Handle(contracts.TypeDriverOffline, func(_ context.Context, tx pgx.Tx, env contracts.Envelope) error {
		tx.(*fakeTx).addEffect(env.Key)
		if calls.Add(1) < 3 {
			return errors.New("not yet")
		}
		return nil
	})
	eventbustest.Produce(t, brokers, offline(t, "drv-1", 1))
	start(t, c)

	eventually(t, 10*time.Second, "the third attempt to succeed", func() bool { return len(db.effectsSnapshot()) == 1 })
	if got := db.effectsSnapshot(); !slices.Equal(got, []string{"drv-1"}) {
		t.Errorf("effects %v: the failed attempts must not leave effects behind", got)
	}
	if calls.Load() != 3 {
		t.Errorf("handler ran %d times, want 3", calls.Load())
	}
	if dlq := eventbustest.ReadTopic(t, brokers, contracts.DLQTopic(contracts.TopicDriver), 0); len(dlq) != 0 {
		t.Errorf("a recovered event must not be dead-lettered, got %d DLQ records", len(dlq))
	}
}

func TestConsumerDoesNotRetryPermanentErrorsOrPanics(t *testing.T) {
	brokers := eventbustest.NewCluster(t, contracts.TopicDriver)
	db := newFakeDB()
	c := newTestConsumer(t, brokers, db, "permanent", contracts.TopicDriver)

	var permanent, panics atomic.Int32
	c.Handle(contracts.TypeDriverOffline, func(context.Context, pgx.Tx, contracts.Envelope) error {
		permanent.Add(1)
		return Permanent(errors.New("invalid forever"))
	})
	c.Handle(contracts.TypeDriverOnline, func(context.Context, pgx.Tx, contracts.Envelope) error {
		panics.Add(1)
		panic("boom")
	})
	online := eventbustest.Record(t, "driver-api", contracts.TypeDriverOnline, "drv-2", 1, contracts.DriverOnline{DriverID: "drv-2"})
	eventbustest.Produce(t, brokers, offline(t, "drv-1", 1), online)
	start(t, c)

	dlq := eventbustest.ReadTopic(t, brokers, contracts.DLQTopic(contracts.TopicDriver), 2)
	if len(dlq) != 2 {
		t.Fatalf("DLQ has %d records, want 2", len(dlq))
	}
	if permanent.Load() != 1 {
		t.Errorf("a Permanent error ran the handler %d times, want 1", permanent.Load())
	}
	if panics.Load() != 3 {
		t.Errorf("a panic is a retryable error: ran %d times, want 3", panics.Load())
	}
}

func TestConsumerAppliesDuplicateDeliveriesOnce(t *testing.T) {
	brokers := eventbustest.NewCluster(t, contracts.TopicDriver)
	db := newFakeDB()
	c := newTestConsumer(t, brokers, db, "dedupe", contracts.TopicDriver)

	var handled seen
	c.Handle(contracts.TypeDriverOffline, func(_ context.Context, tx pgx.Tx, env contracts.Envelope) error {
		handled.add(env.ID)
		tx.(*fakeTx).addEffect(env.ID)
		return nil
	})

	dup := offline(t, "drv-1", 1)
	same := &kgo.Record{Topic: dup.Topic, Key: dup.Key, Value: dup.Value, Headers: dup.Headers} // the relay re-sent it
	eventbustest.Produce(t, brokers, dup, same, offline(t, "drv-2", 1))
	start(t, c)

	eventually(t, 10*time.Second, "both distinct events handled", func() bool { return handled.count() >= 2 })
	time.Sleep(200 * time.Millisecond) // give a wrongly handled duplicate time to show up
	if got := handled.count(); got != 2 {
		t.Errorf("handler ran %d times for 2 distinct events (3 deliveries)", got)
	}
	if got := len(db.effectsSnapshot()); got != 2 {
		t.Errorf("%d effects committed, want 2", got)
	}
}

func TestConsumerResumesFromTheLastCommittedOffset(t *testing.T) {
	brokers := eventbustest.NewCluster(t, contracts.TopicDriver)
	db := newFakeDB()

	// All records share a key, so they sit in one partition and "first three" is well defined.
	var records []*kgo.Record
	for v := int64(1); v <= 6; v++ {
		records = append(records, eventbustest.Record(t, "driver-api", contracts.TypeDriverOffline, "drv-1", v, contracts.DriverOffline{DriverID: "drv-1"}))
	}
	eventbustest.Produce(t, brokers, records...)

	var first seen
	c1 := newTestConsumer(t, brokers, db, "resume", contracts.TopicDriver)
	ctx, cancel := context.WithCancel(t.Context())
	c1.Handle(contracts.TypeDriverOffline, func(_ context.Context, _ pgx.Tx, env contracts.Envelope) error {
		first.add(strconv.FormatInt(env.AggregateVersion, 10))
		if env.AggregateVersion == 3 {
			cancel() // the process is told to stop in the middle of the batch
		}
		return nil
	})
	done := make(chan error, 1)
	go func() { done <- c1.Run(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("first run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("first consumer did not stop")
	}
	if got := first.all(); !slices.Equal(got, []string{"1", "2", "3"}) {
		t.Fatalf("first consumer handled %v, want [1 2 3]", got)
	}

	// A fresh database: with the old inbox, replayed records would be skipped as duplicates and hide a missing
	// offset commit. Resuming must come from the committed offset alone.
	var second seen
	c2 := newTestConsumer(t, brokers, newFakeDB(), "resume", contracts.TopicDriver)
	c2.Handle(contracts.TypeDriverOffline, func(_ context.Context, _ pgx.Tx, env contracts.Envelope) error {
		second.add(strconv.FormatInt(env.AggregateVersion, 10))
		return nil
	})
	start(t, c2)

	eventually(t, 10*time.Second, "the second consumer to catch up", func() bool { return second.count() >= 3 })
	time.Sleep(200 * time.Millisecond)
	if got := second.all(); !slices.Equal(got, []string{"4", "5", "6"}) {
		t.Errorf("restarted consumer handled %v, want [4 5 6]: it must resume, not replay or skip", got)
	}
}

func TestConsumerSkipsEventTypesItHasNoHandlerFor(t *testing.T) {
	brokers := eventbustest.NewCluster(t, contracts.TopicDriver)
	db := newFakeDB()
	c := newTestConsumer(t, brokers, db, "partial", contracts.TopicDriver)

	var handled seen
	c.Handle(contracts.TypeDriverOffline, func(_ context.Context, _ pgx.Tx, env contracts.Envelope) error {
		handled.add(env.Key)
		return nil
	})
	online := eventbustest.Record(t, "driver-api", contracts.TypeDriverOnline, "drv-1", 1, contracts.DriverOnline{DriverID: "drv-1"})
	eventbustest.Produce(t, brokers, online, offline(t, "drv-2", 1))
	start(t, c)

	eventually(t, 10*time.Second, "the handled type to arrive", func() bool { return handled.count() == 1 })
	if dlq := eventbustest.ReadTopic(t, brokers, contracts.DLQTopic(contracts.TopicDriver), 0); len(dlq) != 0 {
		t.Errorf("an event type without a handler is skipped, not dead-lettered (%d DLQ records)", len(dlq))
	}
}

func TestConsumerStopsPromptlyWhenIdle(t *testing.T) {
	brokers := eventbustest.NewCluster(t, contracts.TopicDriver)
	c := newTestConsumer(t, brokers, newFakeDB(), "idle", contracts.TopicDriver)
	stop := start(t, c)
	time.Sleep(100 * time.Millisecond)
	began := time.Now()
	stop()
	if took := time.Since(began); took > 5*time.Second {
		t.Errorf("shutdown took %v", took)
	}
}

func TestNewConsumerValidatesConfig(t *testing.T) {
	tests := map[string]ConsumerConfig{
		"no group":  {Topics: []string{"t"}, DB: newFakeDB(), Log: discardLog},
		"no topics": {Group: "g", DB: newFakeDB(), Log: discardLog},
		"no db":     {Group: "g", Topics: []string{"t"}, Log: discardLog},
		"no log":    {Group: "g", Topics: []string{"t"}, DB: newFakeDB()},
	}
	for name, cfg := range tests {
		if _, err := NewConsumer(cfg); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestPermanent(t *testing.T) {
	base := errors.New("bad input")
	if !isPermanent(Permanent(base)) || !isPermanent(fmt.Errorf("wrapped: %w", Permanent(base))) {
		t.Error("Permanent must survive wrapping")
	}
	if isPermanent(base) || isPermanent(nil) {
		t.Error("plain errors are retryable")
	}
	if !errors.Is(Permanent(base), base) {
		t.Error("Permanent must keep the cause")
	}
	if Permanent(nil) != nil {
		t.Error("Permanent(nil) must be nil")
	}
}

func TestBackoffGrowsAndIsCapped(t *testing.T) {
	for attempt, want := range []time.Duration{100, 200, 400, 800, 1000, 1000, 1000} {
		want *= time.Millisecond
		for range 50 {
			got := backoff(100*time.Millisecond, time.Second, attempt+1)
			if got < want/2 || got > want {
				t.Fatalf("attempt %d: %v is outside [%v, %v]", attempt+1, got, want/2, want)
			}
		}
	}
	if backoff(0, 0, 3) != 0 {
		t.Error("zero bounds must not panic or wait")
	}
}

// The delivery guarantees rest on a handful of client options; pin them so a refactor cannot drop one silently.
func TestClientsAreConfiguredForSafeDelivery(t *testing.T) {
	c, err := NewConsumer(ConsumerConfig{Brokers: []string{"127.0.0.1:1"}, Group: "g", Topics: []string{"loong.plan.v1"}, DB: newFakeDB(), Log: discardLog})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.client.Close)
	for name, opt := range map[string]any{"DisableAutoCommit": kgo.DisableAutoCommit, "BlockRebalanceOnPoll": kgo.BlockRebalanceOnPoll} {
		if on, _ := c.client.OptValue(opt).(bool); !on {
			t.Errorf("consumer client must set %s", name)
		}
	}
	if got := c.client.OptValue(kgo.ConsumerGroup); got != "g" {
		t.Errorf("consumer group = %v", got)
	}
	if got := c.client.OptValue(kgo.RequiredAcks); fmt.Sprint(got) != fmt.Sprint(kgo.AllISRAcks()) {
		t.Errorf("dead-letter writes need acks=all, got %v", got)
	}

	p, err := NewProducerClient([]string{"127.0.0.1:1"}, "console-api")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	if off, _ := p.OptValue(kgo.DisableIdempotentWrite).(bool); off {
		t.Error("the producer must stay idempotent")
	}
	if got := p.OptValue(kgo.RequiredAcks); fmt.Sprint(got) != fmt.Sprint(kgo.AllISRAcks()) {
		t.Errorf("the producer must use acks=all, got %v", got)
	}
}
