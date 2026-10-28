package quote

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/optimizerapi"
)

// The parity fixtures pair a quote request with the option set this engine
// computes for it. The frontend's test suite reads the same files: it checks
// the option set against the rules the wizard relies on, and that the engine is
// never beaten by the mock it replaces. Regenerate them after an intended
// change with UPDATE_PARITY=1 go test ./services/optimizer/internal/quote -run Parity.

func parityDir() string { return filepath.Join("..", "..", "..", "..", "testdata", "parity") }

func scenarios() map[string]Request {
	rushNow := time.Date(2026, 10, 21, 0, 50, 0, 0, time.UTC) // 07:50 WIB
	withDeadline := request(contracts.PresetCheapest, parcel("s1", kemang, senayan, 2), parcel("s2", senopati, monas, 2))
	withDeadline.Shipments[1].Deadline = contracts.At(now.Add(40 * time.Minute))
	rush := request(contracts.PresetFastest, parcel("s1", kemang, senayan, 3), parcel("s2", blokM, pondok, 4))
	rush.Now = rushNow
	return map[string]Request{
		"single":   request(contracts.PresetCheapest, parcel("s1", kemang, senayan, 3)),
		"batching": request(contracts.PresetBalanced, parcel("s1", kemang, senayan, 3), parcel("s2", kemang, blokM, 4), parcel("s3", kemang, senopati, 2)),
		"spread": request(contracts.PresetBalanced,
			parcel("s1", kemang, senayan, 3), parcel("s2", blokM, pondok, 4), parcel("s3", senopati, monas, 2), parcel("s4", senayan, pondok, 5)),
		"heavy":    request(contracts.PresetCheapest, parcel("s1", kemang, senayan, 80), parcel("s2", kemang, blokM, 2), parcel("s3", blokM, senopati, 3)),
		"deadline": withDeadline,
		"rush":     rush,
	}
}

func TestWriteParityFixtures(t *testing.T) {
	if os.Getenv("UPDATE_PARITY") == "" {
		t.Skip("set UPDATE_PARITY=1 to regenerate the fixtures")
	}
	if err := os.MkdirAll(parityDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, r := range scenarios() {
		res := compute(t, r)
		req := optimizerapi.QuoteRequest{
			PlanID: r.PlanID, Shipments: r.Shipments, Schedule: r.Schedule, Priority: r.Priority, Vehicles: r.Vehicles, Now: contracts.At(r.Now),
		}
		write(t, name+".request.json", req)
		write(t, name+".optionset.json", res.Set)
	}
}

func write(t *testing.T, file string, v any) {
	t.Helper()
	data, err := json.Marshal(v, json.Deterministic(true), jsonIndent())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parityDir(), file), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The committed fixtures must be what the engine computes today, so the
// frontend never checks against a stale file.
func TestParityFixturesAreCurrent(t *testing.T) {
	if os.Getenv("UPDATE_PARITY") != "" {
		t.Skip("regenerating")
	}
	entries, err := filepath.Glob(filepath.Join(parityDir(), "*.request.json"))
	if err != nil || len(entries) == 0 {
		t.Skip("no parity fixtures yet (run with UPDATE_PARITY=1)")
	}
	for _, path := range entries {
		name := filepath.Base(path)
		name = name[:len(name)-len(".request.json")]
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var req optimizerapi.QuoteRequest
		if err := json.Unmarshal(data, &req); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		res, err := planner().Compute(context.Background(), Request{
			PlanID: req.PlanID, Shipments: req.Shipments, Schedule: req.Schedule, Priority: req.Priority, Vehicles: req.Vehicles, Now: req.Now.Time,
		}, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want, _ := json.Marshal(res.Set, json.Deterministic(true), jsonIndent())
		got, err := os.ReadFile(filepath.Join(parityDir(), name+".optionset.json"))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want)+"\n" {
			t.Errorf("%s: the committed option set is stale; run UPDATE_PARITY=1 go test ./services/optimizer/internal/quote -run Parity", name)
		}
	}
}

func jsonIndent() json.Options { return jsontext.WithIndent("  ") }
