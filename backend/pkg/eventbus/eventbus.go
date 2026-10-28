// Package eventbus moves events between services over Kafka (franz-go) with delivery that is at-least-once on
// the wire and exactly-once in effect: a transactional outbox on the producing side, an idempotent inbox on
// the consuming side, and a dead-letter topic so one poison record never stops a consumer.
//
//	producer:  tx: change data + Publisher.Enqueue  ->  Publisher.Run (relay)  ->  Kafka
//	consumer:  Kafka  ->  tx: inbox insert + handler  ->  commit offset  (failures: retry, then <topic>.dlq)
package eventbus

import (
	_ "embed"
	"errors"
	"fmt"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

//go:embed schema.sql
var schemaSQL string

// Migrations returns the outbox and inbox DDL for a service's migrator; each service applies it to its own
// database: platform.Migrate(ctx, pool, slices.Concat(platform.Migrations(), eventbus.Migrations(), own)...).
func Migrations() []platform.Migration {
	return []platform.Migration{{ID: "eventbus/0001_outbox_inbox", SQL: schemaSQL}}
}

// NewProducerClient returns a Kafka client for a Publisher: idempotent writes (franz-go's default) with
// acks=all. Extra options carry TLS and SASL.
func NewProducerClient(brokers []string, clientID string, opts ...kgo.Opt) (*kgo.Client, error) {
	base := []kgo.Opt{
		kgo.SeedBrokers(brokers...),
		kgo.ClientID(clientID),
		kgo.RequiredAcks(kgo.AllISRAcks()),
	}
	client, err := kgo.NewClient(append(base, opts...)...)
	if err != nil {
		return nil, fmt.Errorf("eventbus: producer client: %w", err)
	}
	return client, nil
}

// permanentError marks a handler failure that retrying cannot fix.
type permanentError struct{ err error }

func (e permanentError) Error() string { return e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }

// Permanent wraps err so the consumer sends the record to the dead-letter topic at once instead of retrying.
// Use it for input that will never become valid; leave transient conditions (a row that has not arrived yet
// because another topic is behind) as plain errors so they are retried.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err}
}

func isPermanent(err error) bool {
	_, ok := errors.AsType[permanentError](err)
	return ok
}
