package contracts

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// goldens are the wire samples frozen in testdata/*.golden.json. A diff there is a contract change: review it
// as one (additive only) before running `go test ./pkg/contracts -update`.
var goldens = []struct {
	name string
	env  Envelope
	// decode re-encodes the payload through its Go type; any field the type lacks disappears and fails the test.
	decode func(Envelope) ([]byte, error)
}{
	{"plan.confirmed", sampleEnvelope(TypePlanConfirmed, "console-api", "plan-1", 1, "0198a5d2-7c10-7e5a-9c3b-1d2e3f4a5b01"), reencode[PlanConfirmed]},
	{"route.assigned", sampleEnvelope(TypeRouteAssigned, "backoffice-api", "rt-1", 1, "0198a5d2-7c10-7e5a-9c3b-1d2e3f4a5b02"), reencode[RouteAssigned]},
	{"stop.completed", sampleEnvelope(TypeStopCompleted, "driver-api", "rt-1", 4, "0198a5d2-7c10-7e5a-9c3b-1d2e3f4a5b03"), reencode[StopCompleted]},
	{"driver.position", sampleEnvelope(TypeDriverPosition, "driver-api", "drv-1", 128, "0198a5d2-7c10-7e5a-9c3b-1d2e3f4a5b04"), reencode[DriverPosition]},
}

func reencode[T any](env Envelope) ([]byte, error) {
	v, err := env.Payload[T]()
	if err != nil {
		return nil, err
	}
	return json.Marshal(v, json.Deterministic(true))
}

func goldenPath(name string) string { return filepath.Join("testdata", name+".golden.json") }

func indented(t *testing.T, env Envelope) []byte {
	t.Helper()
	b, err := json.Marshal(env, json.Deterministic(true), jsontext.WithIndent("  "))
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}

func compact(t *testing.T, raw []byte) string {
	t.Helper()
	v := jsontext.Value(bytes.Clone(raw))
	if err := v.Compact(); err != nil {
		t.Fatalf("compact: %v", err)
	}
	return string(v)
}

func TestGolden(t *testing.T) {
	for _, g := range goldens {
		t.Run(g.name, func(t *testing.T) {
			got := indented(t, g.env)
			if *update {
				if err := os.WriteFile(goldenPath(g.name), got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(goldenPath(g.name))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Errorf("%s drifted from its golden file; if the change is additive run `go test ./pkg/contracts -update`\n got: %s\nwant: %s", g.name, got, want)
			}
		})
	}
}

func TestGoldenRoundTrip(t *testing.T) {
	for _, g := range goldens {
		t.Run(g.name, func(t *testing.T) {
			raw, err := os.ReadFile(goldenPath(g.name))
			if err != nil {
				t.Fatal(err)
			}
			env, err := Decode(raw)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if env.Type != g.env.Type || env.ID != g.env.ID || env.Key != g.env.Key || env.AggregateVersion != g.env.AggregateVersion {
				t.Errorf("envelope header changed: %+v", env)
			}
			back, err := g.decode(env)
			if err != nil {
				t.Fatal(err)
			}
			if compact(t, back) != compact(t, env.Data) {
				t.Errorf("payload does not survive decode+encode\n got: %s\nwant: %s", compact(t, back), compact(t, env.Data))
			}
			encoded, err := Encode(env)
			if err != nil {
				t.Fatal(err)
			}
			if compact(t, encoded) != compact(t, raw) {
				t.Errorf("envelope does not survive decode+encode")
			}
		})
	}
}

// addUnknown puts an extra field into every JSON object of the tree, as a newer producer might. Msg params
// are open data, not a struct, so they are left alone.
func addUnknown(v any) {
	switch v := v.(type) {
	case map[string]any:
		for key, child := range v {
			if key != "params" {
				addUnknown(child)
			}
		}
		v["futureField"] = map[string]any{"nested": []any{1, "two"}}
	case []any:
		for _, child := range v {
			addUnknown(child)
		}
	}
}

func TestUnknownFieldsAreTolerated(t *testing.T) {
	for _, g := range goldens {
		t.Run(g.name, func(t *testing.T) {
			raw, err := os.ReadFile(goldenPath(g.name))
			if err != nil {
				t.Fatal(err)
			}
			var tree map[string]any
			if err := json.Unmarshal(raw, &tree); err != nil {
				t.Fatal(err)
			}
			addUnknown(tree)
			extended, err := json.Marshal(tree)
			if err != nil {
				t.Fatal(err)
			}

			env, err := Decode(extended)
			if err != nil {
				t.Fatalf("Decode with unknown fields: %v", err)
			}
			if _, err := g.decode(env); err != nil {
				t.Fatalf("payload with unknown fields: %v", err)
			}
			// the known part is unchanged
			original, err := Decode(raw)
			if err != nil {
				t.Fatal(err)
			}
			if env.ID != original.ID || env.Type != original.Type || env.Key != original.Key {
				t.Errorf("known fields changed: %+v", env)
			}
			want, _ := g.decode(original)
			got, _ := g.decode(env)
			if compact(t, got) != compact(t, want) {
				t.Errorf("unknown fields changed the decoded payload")
			}
		})
	}
}

func TestEverySampleRoundTrips(t *testing.T) {
	samples := samplePayloads()
	for eventType, sample := range samples {
		t.Run(eventType, func(t *testing.T) {
			topic, ok := TopicFor(eventType)
			if !ok {
				t.Fatalf("%s has no topic", eventType)
			}
			if !strings.HasPrefix(topic, "loong.") {
				t.Errorf("topic %q lacks the loong. prefix", topic)
			}
			env, err := NewEnvelope("test", eventType, "k-1", 1, sample)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := Encode(env)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := Decode(encoded)
			if err != nil {
				t.Fatal(err)
			}
			ptr := reflect.New(reflect.TypeOf(sample))
			if err := json.Unmarshal(decoded.Data, ptr.Interface()); err != nil {
				t.Fatal(err)
			}
			back, err := json.Marshal(ptr.Elem().Interface(), json.Deterministic(true))
			if err != nil {
				t.Fatal(err)
			}
			if string(back) != string(env.Data) {
				t.Errorf("payload changed\n got: %s\nwant: %s", back, env.Data)
			}
		})
	}
}

func TestSamplesCoverEveryEventType(t *testing.T) {
	samples := samplePayloads()
	for eventType := range eventTopics {
		if _, ok := samples[eventType]; !ok {
			t.Errorf("event type %s has no sample payload", eventType)
		}
	}
	for eventType := range samples {
		if _, ok := eventTopics[eventType]; !ok {
			t.Errorf("sample %s is not a catalogued event type", eventType)
		}
	}
	used := map[string]bool{}
	for _, topic := range eventTopics {
		used[topic] = true
	}
	for _, spec := range Topics() {
		if !used[spec.Name] {
			t.Errorf("topic %s carries no event type", spec.Name)
		}
	}
}

// Passwords and PINs never travel through Kafka: only the argon2id hash of a driver PIN does.
func TestPayloadsCarryNoCredentials(t *testing.T) {
	forbidden := []string{"password", "pin", "secret", "token", "passwd"}
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch v := v.(type) {
		case map[string]any:
			for key, child := range v {
				if slices.Contains(forbidden, strings.ToLower(key)) {
					t.Errorf("credential-like field %q at %s", key, path)
				}
				walk(path+"."+key, child)
			}
		case []any:
			for _, child := range v {
				walk(path+"[]", child)
			}
		}
	}
	for eventType, sample := range samplePayloads() {
		b, err := json.Marshal(sample)
		if err != nil {
			t.Fatal(err)
		}
		var tree any
		if err := json.Unmarshal(b, &tree); err != nil {
			t.Fatal(err)
		}
		walk(eventType, tree)
	}
	b, _ := json.Marshal(samplePayloads()[TypeDriverProvisioned])
	if !strings.Contains(string(b), `"pinHash":"$argon2id$`) {
		t.Errorf("driver.provisioned must carry the PIN as an argon2id hash: %s", b)
	}
}

// The frontend iterates these arrays without null checks.
func TestRequiredCollectionsEncodeAsArrays(t *testing.T) {
	for name, v := range map[string]any{"Plan": Plan{}, "Route": Route{}, "OptionSet": OptionSet{}, "PlanOption": PlanOption{}, "Shipment": Shipment{}} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "null") {
			t.Errorf("%s encodes a null: %s", name, b)
		}
	}
}

func TestOptionalFieldsAreOmitted(t *testing.T) {
	b, err := json.Marshal(RouteStop{ID: "s", ItemsSummary: Msg{Key: "k"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"arrivedAt", "completedAt", "issue", "items", "deliveryCode", "proof"} {
		if strings.Contains(string(b), `"`+field+`"`) {
			t.Errorf("%s should be omitted when empty: %s", field, b)
		}
	}
}

func TestTime(t *testing.T) {
	tests := []struct {
		name, in, out string
	}{
		{"utc", `"2026-10-21T03:15:00Z"`, `"2026-10-21T03:15:00.000Z"`},
		{"millis", `"2026-10-21T03:15:00.123456Z"`, `"2026-10-21T03:15:00.123Z"`},
		{"offset converts to utc", `"2026-10-21T10:15:00+07:00"`, `"2026-10-21T03:15:00.000Z"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Time
			if err := json.Unmarshal([]byte(tt.in), &got); err != nil {
				t.Fatal(err)
			}
			b, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != tt.out {
				t.Errorf("got %s, want %s", b, tt.out)
			}
		})
	}

	for _, bad := range []string{`"yesterday"`, `12`, `true`} {
		var got Time
		if err := json.Unmarshal([]byte(bad), &got); err == nil {
			t.Errorf("%s should not decode", bad)
		}
	}

	var zero struct {
		At Time `json:"at,omitzero"`
	}
	if b, _ := json.Marshal(zero); string(b) != `{}` {
		t.Errorf("zero Time should be omitted, got %s", b)
	}
	if At(time.Date(2026, 10, 21, 10, 0, 0, 0, time.FixedZone("WIB", 7*3600))).Hour() != 3 {
		t.Error("At must convert to UTC")
	}
}

func TestEnvelopeValidation(t *testing.T) {
	good := sampleEnvelope(TypeDriverOffline, "driver-api", "drv-1", 1, "0198a5d2-7c10-7e5a-9c3b-1d2e3f4a5b09")
	tests := []struct {
		name   string
		mutate func(*Envelope)
	}{
		{"empty id", func(e *Envelope) { e.ID = "" }},
		{"id not a uuid", func(e *Envelope) { e.ID = "event-1" }},
		{"empty type", func(e *Envelope) { e.Type = "" }},
		{"version zero", func(e *Envelope) { e.Version = 0 }},
		{"future version", func(e *Envelope) { e.Version = CurrentVersion + 1 }},
		{"empty source", func(e *Envelope) { e.Source = "" }},
		{"empty key", func(e *Envelope) { e.Key = "" }},
		{"negative aggregate version", func(e *Envelope) { e.AggregateVersion = -1 }},
		{"no data", func(e *Envelope) { e.Data = nil }},
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("good envelope rejected: %v", err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := good
			tt.mutate(&env)
			if err := env.Validate(); !errors.Is(err, ErrInvalidEnvelope) {
				t.Errorf("Validate = %v, want ErrInvalidEnvelope", err)
			}
			if _, err := Encode(env); !errors.Is(err, ErrInvalidEnvelope) {
				t.Errorf("Encode = %v, want ErrInvalidEnvelope", err)
			}
		})
	}
	for _, bad := range []string{``, `not json`, `[]`, `{"id":"x"}`} {
		if _, err := Decode([]byte(bad)); !errors.Is(err, ErrInvalidEnvelope) {
			t.Errorf("Decode(%q) = %v, want ErrInvalidEnvelope", bad, err)
		}
	}
}

func TestNewEnvelope(t *testing.T) {
	env, err := NewEnvelope("console-api", TypePlanCancelled, "plan-1", 7, PlanCancelled{PlanID: "plan-1"})
	if err != nil {
		t.Fatal(err)
	}
	if env.Version != CurrentVersion || env.Source != "console-api" || env.Key != "plan-1" || env.AggregateVersion != 7 {
		t.Errorf("unexpected envelope %+v", env)
	}
	if env.ID[14] != '7' {
		t.Errorf("id %s is not a UUIDv7", env.ID)
	}
	if time.Since(env.OccurredAt.Time) > time.Minute || env.OccurredAt.Location() != time.UTC {
		t.Errorf("occurredAt %v should be now, in UTC", env.OccurredAt)
	}
	other, _ := NewEnvelope("console-api", TypePlanCancelled, "plan-1", 8, PlanCancelled{PlanID: "plan-1"})
	if other.ID == env.ID || other.ID < env.ID {
		t.Errorf("ids must be unique and ascending: %s then %s", env.ID, other.ID)
	}
	if _, err := NewEnvelope("", TypePlanCancelled, "plan-1", 1, PlanCancelled{}); !errors.Is(err, ErrInvalidEnvelope) {
		t.Errorf("empty source: %v", err)
	}
	if got, err := env.Payload[PlanCancelled](); err != nil || got.PlanID != "plan-1" {
		t.Errorf("Payload = %+v, %v", got, err)
	}
	broken := Envelope{Type: TypePlanCancelled, Data: jsontext.Value(`[1]`)}
	if _, err := broken.Payload[PlanCancelled](); err == nil {
		t.Error("a payload of the wrong shape should fail to decode")
	}
}

func TestEncodeRejectsOversizedEvents(t *testing.T) {
	proof := DeliveryProof{Kind: ProofKindPhoto, At: sampleTime, Photo: strings.Repeat("a", MaxEnvelopeBytes)}
	env, err := NewEnvelope("driver-api", TypeStopCompleted, "rt-1", 1, StopCompleted{Proof: &proof})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Encode(env); !errors.Is(err, ErrTooLarge) {
		t.Errorf("Encode = %v, want ErrTooLarge", err)
	}
}

func TestHeaders(t *testing.T) {
	env := sampleEnvelope(TypeStopArrived, "driver-api", "rt-1", 2, "0198a5d2-7c10-7e5a-9c3b-1d2e3f4a5b0a")
	headers := map[string]string{}
	for _, h := range env.Headers() {
		headers[h.Key] = string(h.Value)
	}
	if headers[HeaderType] != TypeStopArrived {
		t.Errorf("type header = %q", headers[HeaderType])
	}
	if want := "00-4bf92f3577b34da6a3ce929d0e0e4736-9c3b1d2e3f4a5b0a-01"; headers[HeaderTraceparent] != want {
		t.Errorf("traceparent = %q, want %q", headers[HeaderTraceparent], want)
	}

	for _, bad := range []string{"", "short", strings.Repeat("0", 32), strings.ToUpper(env.TraceID), "zz" + env.TraceID[2:]} {
		env.TraceID = bad
		if tp := env.Traceparent(); tp != "" {
			t.Errorf("TraceID %q gave traceparent %q", bad, tp)
		}
		if len(env.Headers()) != 1 {
			t.Errorf("TraceID %q should leave only the type header", bad)
		}
	}
}

func TestTopics(t *testing.T) {
	specs := map[string]TopicSpec{}
	for _, s := range Topics() {
		if _, dup := specs[s.Name]; dup {
			t.Fatalf("duplicate topic %s", s.Name)
		}
		specs[s.Name] = s
		if s.Partitions != 3 {
			t.Errorf("%s: partitions = %d, want 3", s.Name, s.Partitions)
		}
		if !strings.HasPrefix(s.Name, "loong.") || !strings.HasSuffix(s.Name, ".v1") {
			t.Errorf("%s does not follow loong.<name>.v1", s.Name)
		}
		if s.Cleanup == CleanupCompact && s.Retention != 0 {
			t.Errorf("%s: compacted topics take no retention", s.Name)
		}
	}
	if len(specs) != 9 {
		t.Errorf("got %d topics, want 9", len(specs))
	}

	compacted := []string{TopicFleet, TopicSettings}
	for name, s := range specs {
		if want := slices.Contains(compacted, name); (s.Cleanup == CleanupCompact) != want {
			t.Errorf("%s: cleanup = %s", name, s.Cleanup)
		}
	}
	if specs[TopicPlan].Retention != 7*24*time.Hour {
		t.Errorf("plan retention = %v", specs[TopicPlan].Retention)
	}
	if specs[TopicDriverPosition].Retention != time.Hour {
		t.Errorf("position retention = %v", specs[TopicDriverPosition].Retention)
	}

	if got := specs[TopicPlan].Configs(); got["cleanup.policy"] != "delete" || got["retention.ms"] != "604800000" {
		t.Errorf("plan configs = %v", got)
	}
	if got := specs[TopicFleet].Configs(); got["cleanup.policy"] != "compact" || len(got) != 1 {
		t.Errorf("fleet configs = %v", got)
	}

	if DLQTopic(TopicPlan) != "loong.plan.v1.dlq" {
		t.Errorf("DLQTopic = %s", DLQTopic(TopicPlan))
	}
	dlq := specs[TopicFleet].DLQ()
	if dlq.Name != "loong.fleet.v1.dlq" || dlq.Cleanup != CleanupDelete || dlq.Retention != DLQRetention || dlq.Partitions != 3 {
		t.Errorf("DLQ spec = %+v", dlq)
	}
	if all := AllTopics(); len(all) != 18 {
		t.Errorf("AllTopics = %d, want 18", len(all))
	}
}
