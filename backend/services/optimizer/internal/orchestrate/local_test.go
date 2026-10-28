package orchestrate

import (
	"context"
	"fmt"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/runner"
)

// The tests in this file run the orchestrator against the real solver in process (runner.Local). They need a solver
// that honours Job.Search, which a probe job checks first; without one they skip instead of failing.

// syntheticJob is n shipments scattered around Jakarta, picked up and dropped at distinct places, with a motorbike
// and a van to serve them, deterministic from n.
func syntheticJob(n int) *job.Job {
	seed := uint64(12345 + n)
	next := func() float64 { // a small deterministic generator in [0, 1)
		seed = seed*6364136223846793005 + 1442695040888963407
		return float64(seed>>11) / (1 << 53)
	}
	j := &job.Job{
		Version: job.Version, ID: fmt.Sprintf("synthetic-%d", n), DepartAt: time.Date(2026, 10, 21, 3, 0, 0, 0, time.UTC),
		Presets: job.Presets, Dwell: job.Dwell{Pickup: 120, Drop: 90},
		Limits: job.Limits{Iterations: 150},
		Fleet: []job.FleetType{
			{Type: "motor", Count: n, SpeedKmh: 28, Start: 0, Capacity: job.Capacity{WeightKg: 20, VolumeM3: 0.12, MaxDimensionCm: 55},
				Fare: job.Fare{Base: 6000, PerKm: 1800, PerMin: 150, PerExtraStop: 2500, SurchargeAboveKg: 10, SurchargePerKg: 800}},
			{Type: "blindvan", Count: n, SpeedKmh: 24, Start: 0, Capacity: job.Capacity{WeightKg: 500, VolumeM3: 3.2, MaxDimensionCm: 190},
				Fare: job.Fare{Base: 45000, PerKm: 4500, PerMin: 500, PerExtraStop: 8000, SurchargeAboveKg: 300, SurchargePerKg: 150}},
		},
	}
	place := func() int {
		j.Points = append(j.Points, job.Point{Lat: -6.35 + next()*0.2, Lng: 106.72 + next()*0.2})
		return len(j.Points) - 1
	}
	place() // the depot
	for i := range n {
		j.Shipments = append(j.Shipments, job.Shipment{
			ID: fmt.Sprintf("s%02d", i+1), Pickup: place(), Dropoff: place(),
			WeightKg: 1 + next()*5, VolumeM3: 0.002 + next()*0.01, MaxDimensionCm: 30,
		})
	}
	m := len(j.Points)
	j.DistM = make([]int32, m*m)
	for a := range m {
		for b := range m {
			if a != b {
				j.DistM[a*m+b] = int32(1.3 * haversineM(j.Points[a], j.Points[b]))
			}
		}
	}
	return j
}

func haversineM(a, b job.Point) float64 {
	const r = 6371000
	rad := math.Pi / 180
	dLat, dLng := (b.Lat-a.Lat)*rad, (b.Lng-a.Lng)*rad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(a.Lat*rad)*math.Cos(b.Lat*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * r * math.Asin(math.Sqrt(h))
}

// honoursSearch probes the real solver with a tiny search job.
func honoursSearch(t *testing.T) {
	t.Helper()
	probe := syntheticJob(3)
	probe.Search = &job.Search{Experiments: []job.Experiment{{Algorithm: job.AlgNone, Effort: job.EffortNormal}}}
	res, err := runner.Local{}.Run(context.Background(), probe, nil)
	if err != nil {
		t.Skipf("solve.Run does not yet honour Job.Search (the probe job failed: %v); skipping the tests against the real solver", err)
	}
	if len(res.Candidates) == 0 || len(res.Candidates[0].Trials) == 0 || res.Candidates[0].Facts == nil {
		t.Skip("solve.Run does not yet honour Job.Search (the probe job came back without trials or facts); skipping the tests against the real solver")
	}
}

// served checks every shipment is picked up and dropped exactly once, pickup before drop.
func served(t *testing.T, j *job.Job, c job.Candidate) {
	t.Helper()
	for _, s := range j.Shipments {
		pick, drop := 0, 0
		for _, r := range c.Routes {
			for i, st := range r.Stops {
				if st.Shipment != s.ID {
					continue
				}
				if st.Kind == job.Pickup {
					pick++
				} else {
					drop++
					if !slices.ContainsFunc(r.Stops[:i], func(p job.Stop) bool { return p.Shipment == s.ID && p.Kind == job.Pickup }) {
						t.Errorf("%s: %s is dropped before it is picked up", c.Preset, s.ID)
					}
				}
			}
		}
		if pick != 1 || drop != 1 {
			t.Errorf("%s: %s picked up %d times and dropped %d times", c.Preset, s.ID, pick, drop)
		}
	}
}

// askFirst asks for two experiments in round 1, whatever it sees, so that a round runs on the real solver, and from
// then on the rules decide on the real facts.
type askFirst struct{}

func (askFirst) Name() string { return contracts.ReviewerRules }

func (askFirst) Review(ctx context.Context, b Brief) (Decision, error) {
	if b.Round == 1 {
		return Decision{By: contracts.ReviewerRules, Note: "forced", Experiments: []job.Experiment{
			{Algorithm: job.AlgKMedoids, K: 2, Effort: job.EffortNormal}, {Algorithm: job.AlgKMeans, K: 4, Effort: job.EffortNormal},
		}}, nil
	}
	return RulesReviewer{}.Review(ctx, b)
}

func scoreOf(res *job.Result) map[string][]int64 {
	out := map[string][]int64{}
	for _, c := range res.Candidates {
		out[c.Preset] = c.Score
	}
	return out
}

func TestTheRealSolverNeverDoesWorseWithADeeperSearch(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the real solver through every effort; skipped with -short")
	}
	honoursSearch(t)
	base := syntheticJob(6)
	// A deadline far away gives every job a generous MaxSeconds, so a slow machine (the race detector, a busy CI box)
	// does not make the solver cut a search short and the comparison below unfair.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	fast, err := (&Orchestrator{Runner: runner.Local{}}).Run(ctx, contracts.EffortFast, base)
	if err != nil {
		t.Fatal(err)
	}
	var jobs []*job.Job
	spy := runner.Func(func(ctx context.Context, j *job.Job, p func(job.Progress)) (*job.Result, error) {
		jobs = append(jobs, j)
		return runner.Local{}.Run(ctx, j, p)
	})

	run := func(effort contracts.Effort) *Outcome {
		t.Helper()
		jobs = nil
		out, err := (&Orchestrator{Runner: spy, Reviewer: askFirst{}}).Run(ctx, effort, base)
		if err != nil {
			t.Fatalf("%s: %v", effort, err)
		}
		if out.Truncated || out.Err != nil {
			t.Fatalf("%s: truncated %v, err %v", effort, out.Truncated, out.Err)
		}
		fastScores := scoreOf(fast.Result)
		for _, c := range out.Result.Candidates {
			served(t, base, c)
			if c.Facts == nil || !c.Facts.Valid {
				t.Errorf("%s/%s: facts = %+v", effort, c.Preset, c.Facts)
			}
			if slices.Compare(c.Score, fastScores[c.Preset]) > 0 {
				t.Errorf("%s/%s: score %v is worse than the quick quote's %v", effort, c.Preset, c.Score, fastScores[c.Preset])
			}
		}
		return out
	}

	thorough := run(contracts.EffortThorough)
	if len(jobs) != 2 {
		t.Fatalf("thorough ran %d jobs, want 2", len(jobs))
	}
	if m := thorough.Method; m.Effort != contracts.EffortThorough || !m.Polished || m.Trials < 4*len(job.Presets) {
		t.Errorf("thorough method = %+v", m)
	}
	if got := jobs[0].Search.Experiments[0]; got != (job.Experiment{Algorithm: job.AlgNone, Effort: job.EffortNormal}) {
		t.Errorf("job 1 starts with %+v, want the baseline", got)
	}
	for _, e := range jobs[1].Search.Experiments {
		if e.Effort != job.EffortThorough {
			t.Errorf("job 2 re-runs %+v, want thorough", e)
		}
	}
	reviewed := run(contracts.EffortReviewed)
	if m := reviewed.Method; m.Effort != contracts.EffortReviewed || m.Reviewer != contracts.ReviewerRules || !m.Polished || m.Rounds > DefaultRounds {
		t.Errorf("reviewed method = %+v", m)
	}
	if len(jobs) < 3 || len(jobs) > DefaultRounds+2 || reviewed.Method.Rounds < 1 {
		t.Errorf("reviewed ran %d jobs and %d rounds, want the default job, a forced round and more", len(jobs), reviewed.Method.Rounds)
	}
	if round := jobs[1].Search; len(round.From) != len(job.Presets) {
		t.Errorf("round 1 starts from %d presets' routes, want %d", len(round.From), len(job.Presets))
	}
	// No experiment is ever run twice over the whole search.
	seen := map[Attempt]bool{}
	for _, j := range jobs {
		for _, e := range j.Search.Experiments {
			if a := attemptOf(e); seen[a] {
				t.Errorf("experiment %+v was run twice", a)
			} else {
				seen[a] = true
			}
		}
	}
	for _, r := range reviewed.Reviews {
		t.Logf("round %d by %s: satisfied %v, %d more (%s)", r.Round, r.By, r.Satisfied, len(r.Experiments), r.Note)
	}
	t.Logf("reviewed: %d jobs, %d rounds, %d trials; thorough: %d trials", len(jobs), reviewed.Method.Rounds, reviewed.Method.Trials, thorough.Method.Trials)
}
