package proofs

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform/platformtest"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/ai"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/plans"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/testkit"
)

var t0 = time.Date(2026, 10, 21, 3, 0, 0, 0, time.UTC)

// dataURL is a photo as the driver app sends it.
func dataURL(mime, payload string) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString([]byte(payload))
}

// stubAnalyzer is the model: it records what it was asked and answers with fn, or with a fixed analysis.
type stubAnalyzer struct {
	mu     sync.Mutex
	inputs []ai.ProofInput
	fn     func(ctx context.Context, in ai.ProofInput) (contracts.ProofAnalysis, error)
}

func (s *stubAnalyzer) AnalyzeProof(ctx context.Context, in ai.ProofInput) (contracts.ProofAnalysis, error) {
	s.mu.Lock()
	s.inputs = append(s.inputs, in)
	fn := s.fn
	s.mu.Unlock()
	if fn != nil {
		return fn(ctx, in)
	}
	return stubAnalysis, nil
}

func (s *stubAnalyzer) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.inputs)
}

func (s *stubAnalyzer) setFn(fn func(ctx context.Context, in ai.ProofInput) (contracts.ProofAnalysis, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fn = fn
}

var stubAnalysis = contracts.ProofAnalysis{
	Summary: contracts.Msg{Key: "srv.ai.text", Params: map[string]any{"text": `{"en":"A box at a gate.","id":"Kotak di gerbang."}`}},
	Checks: []contracts.ProofCheck{
		{Key: contracts.ProofCheckRecipient, Result: contracts.ProofResultOK, Note: contracts.Msg{Key: "srv.ai.proof.noNote"}},
		{Key: contracts.ProofCheckParcel, Result: contracts.ProofResultUnclear, Note: contracts.Msg{Key: "srv.ai.proof.noNote"}},
		{Key: contracts.ProofCheckQuality, Result: contracts.ProofResultMissing, Note: contracts.Msg{Key: "srv.ai.proof.noNote"}},
	},
	Confidence: 0.8, Model: "stub-model", AnalyzedAt: contracts.At(t0.Add(time.Hour)),
}

// syncBuffer is a log sink that the worker's goroutine and the test can share.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// rig is a worker on a fresh database with a customer, a model that can be scripted and the admin's vision switch.
type rig struct {
	t        *testing.T
	pool     *pgxpool.Pool
	customer string
	model    *stubAnalyzer
	vision   *atomic.Bool
	logs     *syncBuffer
	worker   *Worker
	seq      int
}

func newRig(t *testing.T) *rig {
	t.Helper()
	pool := testkit.DB(t)
	r := &rig{t: t, pool: pool, customer: testkit.Customer(t, pool, "Dina"), model: &stubAnalyzer{}, vision: &atomic.Bool{}, logs: &syncBuffer{}}
	r.vision.Store(true)
	r.worker = r.newWorker()
	return r
}

// newWorker builds another worker on the same database, model and switch.
func (r *rig) newWorker() *Worker {
	return New(Options{
		Pool: r.pool, Analyzer: r.model, Log: slog.New(slog.NewJSONHandler(r.logs, nil)),
		Flags: func(context.Context) (contracts.AIFeatures, error) {
			return contracts.AIFeatures{Vision: r.vision.Load(), Copilot: true}, nil
		},
	})
}

// target is where a job points.
type target struct{ plan, route, stop string }

// plan stores a running plan of the customer with one route: a pickup and then the given drop-offs, each with the
// proof it was closed with. It returns where each drop-off is.
func (r *rig) plan(proofs ...*contracts.DeliveryProof) []target {
	r.t.Helper()
	r.seq++
	id := "pln-" + string(rune('a'+r.seq))
	route := contracts.Route{ID: id + ":r1", PlanID: id, Status: contracts.RouteStatusRunning, StartAt: contracts.At(t0), CostRp: 25_000}
	route.Stops = append(route.Stops, contracts.RouteStop{
		ID: route.ID + ":s0", Seq: 1, Kind: contracts.StopKindPickup, ShipmentID: "s1", Status: contracts.StopStatusDone, CompletedAt: contracts.At(t0),
	})
	var targets []target
	for i, proof := range proofs {
		stop := contracts.RouteStop{
			ID: route.ID + ":s" + string(rune('1'+i)), Seq: i + 2, Kind: contracts.StopKindDropoff, ShipmentID: "s1", Status: contracts.StopStatusDone,
			CompletedAt: contracts.At(t0.Add(time.Duration(i+1) * time.Minute)), Proof: proof,
			Items: []contracts.StopItem{{Name: "Box", Qty: 2}, {Name: "  ", Qty: 1}, {Name: "Lamp", Qty: 0}},
		}
		route.Stops = append(route.Stops, stop)
		targets = append(targets, target{id, route.ID, stop.ID})
	}
	plan := contracts.Plan{
		ID: id, Code: "PL-" + id, UserID: r.customer, Status: contracts.PlanStatusRunning, Routes: []contracts.Route{route},
		CreatedAt: contracts.At(t0), UpdatedAt: contracts.At(t0), ConfirmedAt: contracts.At(t0),
	}
	plans.AppendEvent(&plan, t0, contracts.Msg{Key: "srv.api.event.confirmed"})
	rec := plans.Record{Plan: plan, Version: 7, RouteVersions: map[string]plans.RouteVersions{route.ID: {Assign: 2, Exec: 5}}}
	if err := plans.Insert(r.t.Context(), r.pool, rec); err != nil {
		r.t.Fatal(err)
	}
	return targets
}

// photoProof is a drop-off proved with a photo.
func photoProof(photo string) *contracts.DeliveryProof {
	return &contracts.DeliveryProof{Kind: contracts.ProofKindPhoto, At: contracts.At(t0), Photo: photo}
}

// enqueue adds the job of a target.
func (r *rig) enqueue(tg target) {
	r.t.Helper()
	if err := Enqueue(r.t.Context(), r.pool, tg.plan, tg.route, tg.stop); err != nil {
		r.t.Fatal(err)
	}
}

// job is a row of proof_jobs.
type jobRow struct {
	state, lastError string
	attempts         int
	// dueIn is how far ahead next_at is, in seconds (negative: due).
	dueIn float64
}

func (r *rig) job(tg target) jobRow {
	r.t.Helper()
	var j jobRow
	err := r.pool.QueryRow(r.t.Context(),
		`SELECT state, attempts, last_error, extract(epoch FROM next_at - now())::float8 FROM proof_jobs WHERE plan_id = $1 AND stop_id = $2`,
		tg.plan, tg.stop).Scan(&j.state, &j.attempts, &j.lastError, &j.dueIn)
	if err != nil {
		r.t.Fatalf("job of %s: %v", tg.stop, err)
	}
	return j
}

// makeDue moves a pending job's due time into the past, as the passing of its lease or backoff would.
func (r *rig) makeDue(tg target) {
	r.t.Helper()
	if _, err := r.pool.Exec(r.t.Context(), `UPDATE proof_jobs SET next_at = now() - interval '1 second' WHERE plan_id = $1 AND stop_id = $2`, tg.plan, tg.stop); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) record(tg target) plans.Record {
	r.t.Helper()
	rec, err := plans.Load(r.t.Context(), r.pool, tg.plan)
	if err != nil {
		r.t.Fatal(err)
	}
	return rec
}

// stop reads the stop of a target as stored.
func (r *rig) stop(tg target) contracts.RouteStop {
	r.t.Helper()
	rec := r.record(tg)
	if s := locate(&rec.Plan, tg.route, tg.stop); s != nil {
		return *s
	}
	r.t.Fatalf("stop %s not found", tg.stop)
	return contracts.RouteStop{}
}

// step works one job and insists there was one.
func (r *rig) step(w *Worker) {
	r.t.Helper()
	worked, err := w.Step(r.t.Context())
	if err != nil || !worked {
		r.t.Fatalf("Step = %v, %v: want a job worked", worked, err)
	}
}

// idle insists that nothing is due.
func (r *rig) idle(w *Worker) {
	r.t.Helper()
	worked, err := w.Step(r.t.Context())
	if err != nil || worked {
		r.t.Fatalf("Step = %v, %v: want nothing due", worked, err)
	}
}

// rewrite changes the stored plan in a transaction, as another writer would.
func (r *rig) rewrite(tg target, change func(s *contracts.RouteStop)) {
	r.t.Helper()
	err := platform.WithTx(r.t.Context(), r.pool, func(tx pgx.Tx) error {
		rec, err := plans.Lock(r.t.Context(), tx, tg.plan)
		if err != nil {
			return err
		}
		change(locate(&rec.Plan, tg.route, tg.stop))
		return plans.Save(r.t.Context(), tx, rec)
	})
	if err != nil {
		r.t.Fatal(err)
	}
}

// listenForChanges subscribes to what notify announces and returns a function that yields the next announcement that
// mentions the customer (the channel belongs to the whole test database).
func listenForChanges(t *testing.T, customer string) func() string {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), os.Getenv(platformtest.EnvDatabaseURL))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(context.WithoutCancel(t.Context())) })
	if _, err := conn.Exec(t.Context(), `LISTEN console_events`); err != nil {
		t.Fatal(err)
	}
	return func() string {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		for {
			n, err := conn.WaitForNotification(ctx)
			if err != nil {
				t.Fatalf("no announcement: %v", err)
			}
			if strings.Contains(n.Payload, customer) {
				return n.Payload
			}
		}
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
