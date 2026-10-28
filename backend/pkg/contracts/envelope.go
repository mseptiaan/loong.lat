// Package contracts is the only thing services share: the wire format of events (envelope, topics, payloads)
// and the domain types they carry, mirrored from the frontend's TypeScript. Nothing here has behaviour that
// depends on a service; importing it never couples one service's code to another's.
package contracts

import (
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"uuid"
)

// CurrentVersion is the envelope version. It only changes with a new topic generation (v2).
const CurrentVersion = 1

// MaxEnvelopeBytes bounds an encoded envelope below Kafka's default 1 MiB message limit. An event over it can
// never be delivered, so producers fail fast instead of wedging the outbox on it.
const MaxEnvelopeBytes = 900 << 10

// Kafka record header names. `type` lets a consumer route without parsing the value; `traceparent` is the
// W3C trace context so broker-side tools and tracers can follow an event.
const (
	HeaderType        = "type"
	HeaderTraceparent = "traceparent"
)

// Envelope wraps every event on every topic. Kafka key = Key, value = the JSON of the envelope.
type Envelope struct {
	// ID is a UUIDv7: unique per event, the inbox's idempotency key, time-ordered.
	ID      string `json:"id"`
	Type    string `json:"type"`
	Version int    `json:"version"`
	// Source is the producing service, e.g. "console-api".
	Source string `json:"source"`
	// Key is the aggregate id (also the Kafka key): events with one key stay ordered.
	Key string `json:"key"`
	// AggregateVersion increases by one with every change of the aggregate, so consumers drop late or duplicate
	// events by comparing it with what they already applied.
	AggregateVersion int64 `json:"aggregateVersion"`
	OccurredAt       Time  `json:"occurredAt"`
	// TraceID is a W3C trace id (32 hex chars) shared by everything one user action caused.
	TraceID string `json:"traceId"`
	// CausationID is the ID of the event whose handling produced this one, when there is one.
	CausationID string         `json:"causationId,omitempty"`
	Data        jsontext.Value `json:"data"`
}

// Sentinel errors; use errors.Is.
var (
	ErrInvalidEnvelope = errors.New("contracts: invalid envelope")
	ErrTooLarge        = errors.New("contracts: envelope exceeds size limit")
)

// NewEnvelope builds an envelope for data with a fresh ID and the current time. TraceID and CausationID are
// left to the caller (the eventbus publisher fills them from the request context).
func NewEnvelope(source, eventType, key string, aggregateVersion int64, data any) (Envelope, error) {
	payload, err := json.Marshal(data, json.Deterministic(true))
	if err != nil {
		return Envelope{}, fmt.Errorf("contracts: marshal %s payload: %w", eventType, err)
	}
	env := Envelope{
		ID:               uuid.NewV7().String(),
		Type:             eventType,
		Version:          CurrentVersion,
		Source:           source,
		Key:              key,
		AggregateVersion: aggregateVersion,
		OccurredAt:       Now(),
		Data:             payload,
	}
	return env, env.Validate()
}

// Validate checks the fields a consumer relies on.
func (e Envelope) Validate() error {
	var problem string
	switch {
	case e.ID == "":
		problem = "id is empty"
	case e.Type == "":
		problem = "type is empty"
	case e.Version < 1 || e.Version > CurrentVersion:
		problem = fmt.Sprintf("unsupported version %d", e.Version)
	case e.Source == "":
		problem = "source is empty"
	case e.Key == "":
		problem = "key is empty"
	case e.AggregateVersion < 0:
		problem = "aggregateVersion is negative"
	case len(e.Data) == 0:
		problem = "data is empty"
	default:
		if _, err := uuid.Parse(e.ID); err != nil {
			problem = "id is not a UUID"
		}
	}
	if problem != "" {
		return fmt.Errorf("%w: %s", ErrInvalidEnvelope, problem)
	}
	return nil
}

// Encode validates env and returns its JSON.
func Encode(env Envelope) ([]byte, error) {
	if err := env.Validate(); err != nil {
		return nil, err
	}
	b, err := json.Marshal(env, json.Deterministic(true))
	if err != nil {
		return nil, fmt.Errorf("contracts: encode envelope: %w", err)
	}
	if len(b) > MaxEnvelopeBytes {
		return nil, fmt.Errorf("%w: %s is %d bytes, limit %d", ErrTooLarge, env.Type, len(b), MaxEnvelopeBytes)
	}
	return b, nil
}

// Decode parses and validates an envelope. Unknown fields are ignored: that is how the format grows.
func Decode(b []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return Envelope{}, fmt.Errorf("%w: %w", ErrInvalidEnvelope, err)
	}
	return env, env.Validate()
}

// Payload decodes the envelope's data into T, e.g. env.Payload[PlanConfirmed]().
func (e Envelope) Payload[T any]() (T, error) {
	var v T
	if err := json.Unmarshal(e.Data, &v); err != nil {
		return v, fmt.Errorf("contracts: decode %s payload: %w", e.Type, err)
	}
	return v, nil
}

// Header is a Kafka record header; contracts stays free of any Kafka client.
type Header struct {
	Key   string
	Value []byte
}

// Headers returns the Kafka headers for the envelope: its type and, when it has a valid trace id, traceparent.
func (e Envelope) Headers() []Header {
	headers := []Header{{Key: HeaderType, Value: []byte(e.Type)}}
	if tp := e.Traceparent(); tp != "" {
		headers = append(headers, Header{Key: HeaderTraceparent, Value: []byte(tp)})
	}
	return headers
}

// Traceparent returns the W3C traceparent for the event ("00-<trace>-<span>-01"), or "" if TraceID is not a
// valid trace id. The span id is the low 64 bits of the event id, which is unique per event.
func (e Envelope) Traceparent() string {
	if !ValidTraceID(e.TraceID) {
		return ""
	}
	id := strings.ReplaceAll(e.ID, "-", "")
	if len(id) != 32 {
		return ""
	}
	span := id[16:]
	if span == "0000000000000000" {
		return ""
	}
	return "00-" + e.TraceID + "-" + span + "-01"
}

// ValidTraceID reports whether s is a W3C trace id: 32 lowercase hex characters, not all zero.
func ValidTraceID(s string) bool {
	if len(s) != 32 || s == strings.Repeat("0", 32) {
		return false
	}
	if _, err := hex.DecodeString(s); err != nil {
		return false
	}
	return s == strings.ToLower(s)
}
