package eventbus

import (
	"context"
	"errors"
	"strings"
	"testing"
	"uuid"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

func newOfflinePublisher() *Publisher {
	return NewPublisher("driver-api", newFakeDB(), nil, discardLog)
}

func TestEnqueueWritesTheEventToTheOutboxInTheCallersTransaction(t *testing.T) {
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	ctx := platform.WithTraceID(t.Context(), traceID)
	tx := &fakeTx{db: newFakeDB()}

	err := newOfflinePublisher().Enqueue(ctx, tx, Event{
		Type: contracts.TypeStopArrived, Key: "rt-1", AggregateVersion: 4, CausationID: "cause-1",
		Data: contracts.StopArrived{PlanID: "plan-1", RouteID: "rt-1", StopID: "stop-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tx.execs) != 1 || !strings.Contains(tx.execs[0].sql, "INSERT INTO outbox") {
		t.Fatalf("want exactly one outbox insert in the transaction, got %+v", tx.execs)
	}
	args := tx.execs[0].args
	eventID, topic, key, eventType, traceparent, payload := args[0].(string), args[1].(string), args[2].(string), args[3].(string), args[4].(string), args[5].(string)

	if topic != contracts.TopicExecution || key != "rt-1" || eventType != contracts.TypeStopArrived {
		t.Errorf("row = topic %q key %q type %q", topic, key, eventType)
	}
	env, err := contracts.Decode([]byte(payload))
	if err != nil {
		t.Fatalf("stored payload is not a valid envelope: %v", err)
	}
	if env.ID != eventID || env.Source != "driver-api" || env.AggregateVersion != 4 || env.CausationID != "cause-1" || env.TraceID != traceID {
		t.Errorf("envelope = %+v", env)
	}
	if id, err := uuid.Parse(eventID); err != nil || id.String()[14] != '7' {
		t.Errorf("event id %q is not a UUIDv7", eventID)
	}
	if want := env.Traceparent(); traceparent == "" || traceparent != want {
		t.Errorf("traceparent = %q, want %q", traceparent, want)
	}
	if got, err := env.Payload[contracts.StopArrived](); err != nil || got.StopID != "stop-1" {
		t.Errorf("payload = %+v, %v", got, err)
	}
}

func TestEnqueueStartsATraceWhenTheContextHasNone(t *testing.T) {
	tx := &fakeTx{db: newFakeDB()}
	if err := newOfflinePublisher().Enqueue(t.Context(), tx, Event{
		Type: contracts.TypeDriverOffline, Key: "drv-1", Data: contracts.DriverOffline{DriverID: "drv-1"},
	}); err != nil {
		t.Fatal(err)
	}
	env, err := contracts.Decode([]byte(tx.execs[0].args[5].(string)))
	if err != nil {
		t.Fatal(err)
	}
	if !contracts.ValidTraceID(env.TraceID) {
		t.Errorf("trace id %q is not valid", env.TraceID)
	}
}

func TestEnqueueRejectsEventsThatCouldNeverBeDelivered(t *testing.T) {
	huge := contracts.StopCompleted{Proof: &contracts.DeliveryProof{Kind: contracts.ProofKindPhoto, Photo: strings.Repeat("a", contracts.MaxEnvelopeBytes)}}
	tests := []struct {
		name string
		ev   Event
		want error
	}{
		{"unknown type", Event{Type: "plan.exploded", Key: "k", Data: struct{}{}}, nil},
		{"empty key", Event{Type: contracts.TypeDriverOffline, Data: contracts.DriverOffline{}}, contracts.ErrInvalidEnvelope},
		{"negative version", Event{Type: contracts.TypeDriverOffline, Key: "k", AggregateVersion: -1, Data: contracts.DriverOffline{}}, contracts.ErrInvalidEnvelope},
		{"too large", Event{Type: contracts.TypeStopCompleted, Key: "rt-1", Data: huge}, contracts.ErrTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := &fakeTx{db: newFakeDB()}
			err := newOfflinePublisher().Enqueue(t.Context(), tx, tt.ev)
			if err == nil {
				t.Fatal("want an error")
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Errorf("error = %v, want %v", err, tt.want)
			}
			if len(tx.execs) != 0 {
				t.Error("a rejected event must not touch the database")
			}
		})
	}
}

func TestOutboxRowBecomesAKafkaRecord(t *testing.T) {
	tp := "00-4bf92f3577b34da6a3ce929d0e0e4736-9c3b1d2e3f4a5b0a-01"
	rec := outboxRow{topic: contracts.TopicPlan, key: "plan-1", eventType: contracts.TypePlanCancelled, traceparent: &tp, payload: `{"x":1}`}.record()
	if rec.Topic != contracts.TopicPlan || string(rec.Key) != "plan-1" || string(rec.Value) != `{"x":1}` {
		t.Errorf("record = %+v", rec)
	}
	if headerValue(rec, contracts.HeaderType) != contracts.TypePlanCancelled || headerValue(rec, contracts.HeaderTraceparent) != tp {
		t.Errorf("headers = %+v", rec.Headers)
	}
	bare := outboxRow{topic: "t", key: "k", eventType: "e"}.record()
	if len(bare.Headers) != 1 {
		t.Errorf("without a traceparent only the type header is sent, got %+v", bare.Headers)
	}
}

// flakyProducer sends the first sendFirst records of its first call for real and fails everything else, as a
// broker that dies in the middle of a batch would.
type flakyProducer struct {
	real      producer
	sendFirst int
	calls     int
}

var errBrokerDown = errors.New("broker down")

func (f *flakyProducer) ProduceSync(ctx context.Context, rs ...*kgo.Record) kgo.ProduceResults {
	f.calls++
	sent := 0
	if f.calls == 1 {
		sent = min(f.sendFirst, len(rs))
	}
	var results kgo.ProduceResults
	if sent > 0 {
		results = f.real.ProduceSync(ctx, rs[:sent]...)
	}
	for _, r := range rs[sent:] {
		results = append(results, kgo.ProduceResult{Record: r, Err: errBrokerDown})
	}
	return results
}
