// Package eventbustest gives tests a Kafka that needs no Docker: franz-go's in-process fake cluster (kfake),
// plus helpers to put events on it and read them back.
package eventbustest

import (
	"context"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

// NewCluster starts a single-broker fake cluster with the given topics (3 partitions each, like dev) and
// the matching dead-letter topics, and returns its broker addresses. It is closed when the test ends.
func NewCluster(t testing.TB, topics ...string) []string {
	t.Helper()
	all := append([]string(nil), topics...)
	for _, topic := range topics {
		all = append(all, contracts.DLQTopic(topic))
	}
	cluster, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(3, all...))
	if err != nil {
		t.Fatalf("eventbustest: start fake cluster: %v", err)
	}
	t.Cleanup(cluster.Close)
	return cluster.ListenAddrs()
}

// NewClient returns a plain client on the cluster, closed when the test ends.
func NewClient(t testing.TB, brokers []string, opts ...kgo.Opt) *kgo.Client {
	t.Helper()
	client, err := kgo.NewClient(append([]kgo.Opt{kgo.SeedBrokers(brokers...)}, opts...)...)
	if err != nil {
		t.Fatalf("eventbustest: client: %v", err)
	}
	t.Cleanup(client.Close)
	return client
}

// Record builds the Kafka record a Publisher would send for an event: key, envelope JSON and headers.
func Record(t testing.TB, source, eventType string, key string, aggregateVersion int64, data any) *kgo.Record {
	t.Helper()
	topic, ok := contracts.TopicFor(eventType)
	if !ok {
		t.Fatalf("eventbustest: unknown event type %q", eventType)
	}
	env, err := contracts.NewEnvelope(source, eventType, key, aggregateVersion, data)
	if err != nil {
		t.Fatalf("eventbustest: envelope: %v", err)
	}
	return EnvelopeRecord(t, topic, env)
}

// EnvelopeRecord builds the Kafka record for an existing envelope (to replay one, for instance).
func EnvelopeRecord(t testing.TB, topic string, env contracts.Envelope) *kgo.Record {
	t.Helper()
	value, err := contracts.Encode(env)
	if err != nil {
		t.Fatalf("eventbustest: encode: %v", err)
	}
	rec := &kgo.Record{Topic: topic, Key: []byte(env.Key), Value: value}
	for _, h := range env.Headers() {
		rec.Headers = append(rec.Headers, kgo.RecordHeader{Key: h.Key, Value: h.Value})
	}
	return rec
}

// Produce writes records and waits until they are stored.
func Produce(t testing.TB, brokers []string, records ...*kgo.Record) {
	t.Helper()
	client := NewClient(t, brokers)
	if err := client.ProduceSync(t.Context(), records...).FirstErr(); err != nil {
		t.Fatalf("eventbustest: produce: %v", err)
	}
}

// ReadTopic reads a topic from the start without joining a group. It waits up to five seconds for at least
// want records, then briefly for stragglers, so a test can assert both "arrived" and "nothing extra". Records
// of one partition are in order; across partitions there is no order.
func ReadTopic(t testing.TB, brokers []string, topic string, want int) []*kgo.Record {
	t.Helper()
	client := NewClient(t, brokers,
		kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))

	var got []*kgo.Record
	deadline := time.Now().Add(5 * time.Second)
	for len(got) < want && time.Now().Before(deadline) {
		got = append(got, poll(t.Context(), client, 200*time.Millisecond)...)
	}
	return append(got, poll(t.Context(), client, 300*time.Millisecond)...)
}

func poll(ctx context.Context, client *kgo.Client, wait time.Duration) []*kgo.Record {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	return client.PollFetches(ctx).Records()
}
