package orchestrate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/runner"
)

/* ---------------- helpers: a scripted runner and the results it returns ---------------- */

func baseJob() *job.Job {
	j := &job.Job{
		Version: job.Version, ID: "PL-1", DepartAt: time.Date(2026, 10, 21, 3, 0, 0, 0, time.UTC),
		Points: []job.Point{{Lat: -6.2, Lng: 106.8}, {Lat: -6.3, Lng: 106.9}}, DistM: []int32{0, 1000, 1000, 0},
		Fleet:   []job.FleetType{{Type: "motor", Count: 2, SpeedKmh: 30}},
		Presets: job.Presets,
	}
	for i := range 12 { // enough shipments for any k a test asks for
		j.Shipments = append(j.Shipments, job.Shipment{ID: fmt.Sprintf("s%d", i+1), Pickup: 0, Dropoff: 1})
	}
	return j
}

// route is a route that carries a tag, so that a test can tell which job's routes ended up where.
func route(tag string) []job.Route {
	return []job.Route{{Type: "motor", Stops: []job.Stop{{Shipment: tag, Kind: job.Pickup}}}}
}

func tagOf(routes []job.Route) string {
	if len(routes) == 0 || len(routes[0].Stops) == 0 {
		return ""
	}
	return routes[0].Stops[0].Shipment
}

// exp is a trial of an experiment.
func exp(alg string, k int, effort string, won bool, score ...int64) job.Trial {
	return job.Trial{Kind: job.TrialExperiment, Algorithm: alg, K: k, Effort: effort, Valid: true, Won: won, Score: score}
}

// cand is a valid candidate whose routes carry the tag.
func cand(preset, tag string, score []int64, trials ...job.Trial) job.Candidate {
	return job.Candidate{
		Preset: preset, Routes: route(tag), Proof: job.ProofHeuristic, Score: score, Trials: trials,
		Facts: &job.Facts{Valid: true},
	}
}

func result(stats job.Stats, cands ...job.Candidate) *job.Result {
	return &job.Result{Version: job.Version, Engine: "test", Candidates: cands, Stats: stats}
}

// allPresets gives every preset the same candidate shape.
func allPresets(tag string, score []int64, trials ...job.Trial) []job.Candidate {
	var out []job.Candidate
	for _, p := range job.Presets {
		out = append(out, cand(p, tag, slices.Clone(score), slices.Clone(trials)...))
	}
	return out
}

// rec runs jobs from a script and records them.
type rec struct {
	jobs  []*job.Job
	steps []func(n int, j *job.Job) (*job.Result, error)
}

func (r *rec) Run(_ context.Context, j *job.Job, _ func(job.Progress)) (*job.Result, error) {
	c := *j
	r.jobs = append(r.jobs, &c)
	n := len(r.jobs) - 1
	if n >= len(r.steps) {
		return nil, fmt.Errorf("the script has no step %d", n)
	}
	return r.steps[n](n, j)
}

func (r *rec) runner() runner.Runner { return r }

// give is a step that returns a fixed result.
func give(res *job.Result) func(int, *job.Job) (*job.Result, error) {
	return func(int, *job.Job) (*job.Result, error) { return res, nil }
}

func fail(err error) func(int, *job.Job) (*job.Result, error) {
	return func(int, *job.Job) (*job.Result, error) { return nil, err }
}

// notes collects what the orchestrator tells the listener.
type notes []string

func (n *notes) add(m contracts.Msg) {
	s := m.Key
	for _, k := range []string{"n", "round"} {
		if v, ok := m.Params[k]; ok {
			s += fmt.Sprintf(" %s=%v", k, v)
		}
	}
	*n = append(*n, strings.TrimPrefix(s, "srv.engine.search."))
}

// reviewer is a scripted reviewer.
type reviewer struct {
	name   string
	steps  []step
	briefs []Brief
}

type step struct {
	d   Decision
	err error
}

func (r *reviewer) Name() string { return r.name }

func (r *reviewer) Review(_ context.Context, b Brief) (Decision, error) {
	r.briefs = append(r.briefs, b)
	n := len(r.briefs) - 1
	if n >= len(r.steps) {
		return Decision{}, fmt.Errorf("the script has no review %d", n+1)
	}
	return r.steps[n].d, r.steps[n].err
}

func more(by string, e ...job.Experiment) step {
	return step{d: Decision{By: by, Experiments: e, Note: "more"}}
}
func satisfied(by string) step { return step{d: Decision{By: by, Satisfied: true, Note: "enough"}} }

func km(k int) job.Experiment {
	return job.Experiment{Algorithm: job.AlgKMedoids, K: k, Effort: job.EffortNormal}
}

/* ---------------- fast ---------------- */

func TestFastIsOneUntouchedJob(t *testing.T) {
	for _, effort := range []contracts.Effort{"", contracts.EffortFast} {
		want := result(job.Stats{Shipments: 12, Trials: 4}, allPresets("plain", []int64{0, 100})...)
		r := &rec{steps: []func(int, *job.Job) (*job.Result, error){give(want)}}
		var heard notes
		o := &Orchestrator{Runner: r, Notify: heard.add}
		base := baseJob()
		out, err := o.Run(context.Background(), effort, base)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.jobs) != 1 || r.jobs[0].Search != nil || r.jobs[0].ID != "PL-1" {
			t.Fatalf("effort %q: want one plain job with the id it was given, got %d jobs, search %+v, id %q", effort, len(r.jobs), r.jobs[0].Search, r.jobs[0].ID)
		}
		if out.Result != want {
			t.Errorf("effort %q: a fast quote must return the job's result untouched", effort)
		}
		if out.Method != (contracts.Method{Effort: contracts.EffortFast, Trials: 4}) {
			t.Errorf("effort %q: method = %+v", effort, out.Method)
		}
		if out.Truncated || out.Err != nil || len(heard) != 0 {
			t.Errorf("effort %q: truncated %v, err %v, heard %v", effort, out.Truncated, out.Err, heard)
		}
		if base.Search != nil {
			t.Error("the base job was changed")
		}
	}
}

func TestFastCountsOneSolvePerPresetWhenTheSolverDoesNotSay(t *testing.T) {
	r := &rec{steps: []func(int, *job.Job) (*job.Result, error){give(result(job.Stats{}, allPresets("plain", []int64{0, 100})...))}}
	out, err := (&Orchestrator{Runner: r}).Run(context.Background(), contracts.EffortFast, baseJob())
	if err != nil || out.Method.Trials != len(job.Presets) {
		t.Fatalf("trials = %d (%v), want %d", out.Method.Trials, err, len(job.Presets))
	}
}

func TestFastFailureAndTruncationAreReportedAsTheyCome(t *testing.T) {
	boom := errors.New("sandbox exploded")
	_, err := (&Orchestrator{Runner: &rec{steps: []func(int, *job.Job) (*job.Result, error){fail(boom)}}}).Run(context.Background(), contracts.EffortFast, baseJob())
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want the runner's own", err)
	}

	cands := allPresets("plain", []int64{0, 100})
	cands[2].Truncated = true
	out, err := (&Orchestrator{Runner: &rec{steps: []func(int, *job.Job) (*job.Result, error){give(result(job.Stats{}, cands...))}}}).Run(context.Background(), "", baseJob())
	if err != nil || !out.Truncated {
		t.Errorf("truncated = %v (%v), want true", out.Truncated, err)
	}
}

func TestAnUnknownEffortIsRefused(t *testing.T) {
	_, err := (&Orchestrator{Runner: &rec{}}).Run(context.Background(), "extreme", baseJob())
	if err == nil {
		t.Fatal("want an error")
	}
}

/* ---------------- thorough ---------------- */

func TestThoroughRunsTheDefaultsThenTheWinnersAgainAndPolishes(t *testing.T) {
	// Presets are won by kmedoids 3 (twice), none, and kmeans 2: three distinct winners, in preset order.
	first := result(job.Stats{Shipments: 2, Trials: 16, Iterations: 100, ElapsedMs: 50, ExactRan: true},
		cand(job.Cheapest, "c1", []int64{0, 100}, exp("none", 0, "normal", false, 0, 110), exp("kmedoids", 3, "normal", true, 0, 100)),
		cand(job.Shortest, "s1", []int64{0, 50}, exp("kmedoids", 3, "normal", true, 0, 50), exp("kmeans", 2, "normal", false, 0, 60)),
		cand(job.Fastest, "f1", []int64{0, 70}, exp("none", 0, "normal", true, 0, 70), exp("kmeans", 2, "normal", false, 0, 80)),
		cand(job.Balanced, "b1", []int64{0, 80}, exp("none", 0, "normal", false, 0, 85), exp("kmeans", 2, "normal", true, 0, 80)),
	)
	second := result(job.Stats{Trials: 7, Iterations: 300, ElapsedMs: 200, Polished: true},
		cand(job.Cheapest, "c2", []int64{0, 95}), cand(job.Shortest, "s2", []int64{0, 50}),
		cand(job.Fastest, "f2", []int64{0, 72}), cand(job.Balanced, "b2", []int64{0, 79}),
	)
	r := &rec{steps: []func(int, *job.Job) (*job.Result, error){give(first), give(second)}}
	var heard notes
	o := &Orchestrator{Runner: r, Notify: heard.add}
	out, err := o.Run(context.Background(), contracts.EffortThorough, baseJob())
	if err != nil {
		t.Fatal(err)
	}

	if len(r.jobs) != 2 {
		t.Fatalf("%d jobs, want 2", len(r.jobs))
	}
	j1, j2 := r.jobs[0], r.jobs[1]
	if !slices.Equal(j1.Search.Experiments, DefaultExperiments()) || j1.Search.Polish != "" || j1.Search.From != nil {
		t.Errorf("job 1 search = %+v, want the default experiments only", j1.Search)
	}
	if j1.Search.Experiments[0] != (job.Experiment{Algorithm: job.AlgNone, Effort: job.EffortNormal}) {
		t.Errorf("the baseline must come first, got %+v", j1.Search.Experiments[0])
	}
	wantAgain := []job.Experiment{
		{Algorithm: job.AlgKMedoids, K: 3, Effort: job.EffortThorough},
		{Algorithm: job.AlgNone, Effort: job.EffortThorough},
		{Algorithm: job.AlgKMeans, K: 2, Effort: job.EffortThorough},
	}
	if !slices.Equal(j2.Search.Experiments, wantAgain) {
		t.Errorf("job 2 experiments = %+v, want %+v", j2.Search.Experiments, wantAgain)
	}
	if j2.Search.Polish != job.EffortThorough {
		t.Errorf("job 2 polish = %q", j2.Search.Polish)
	}
	for p, tag := range map[string]string{job.Cheapest: "c1", job.Shortest: "s1", job.Fastest: "f1", job.Balanced: "b1"} {
		if got := tagOf(j2.Search.From[p]); got != tag {
			t.Errorf("job 2 starts %s from %q, want the best of job 1 (%q)", p, got, tag)
		}
	}
	if j1.ID == j2.ID || j1.ID == "PL-1" {
		t.Errorf("job ids %q and %q must differ", j1.ID, j2.ID)
	}

	// The better of the two jobs wins each preset; shortest tied, so job 1 keeps it.
	got := map[string]string{}
	for _, c := range out.Result.Candidates {
		got[c.Preset] = tagOf(c.Routes)
	}
	want := map[string]string{job.Cheapest: "c2", job.Shortest: "s1", job.Fastest: "f1", job.Balanced: "b2"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("merged = %v, want %v", got, want)
	}
	wantMethod := contracts.Method{Effort: contracts.EffortThorough, Trials: 23, Polished: true}
	if out.Method != wantMethod {
		t.Errorf("method = %+v, want %+v", out.Method, wantMethod)
	}
	if s := out.Result.Stats; s.Iterations != 400 || s.ElapsedMs != 250 || !s.ExactRan || s.Shipments != 2 {
		t.Errorf("stats = %+v", s)
	}
	if want := (notes{"experiments n=4", "experiments n=3", "polish"}); !slices.Equal(heard, want) {
		t.Errorf("heard %v, want %v", heard, want)
	}
	if out.Truncated || out.Err != nil {
		t.Errorf("truncated %v err %v", out.Truncated, out.Err)
	}
}

func TestThoroughSkipsExperimentsItCannotReproduce(t *testing.T) {
	first := result(job.Stats{}, cand(job.Cheapest, "c1", []int64{0, 1}, exp("dbscan", 0, "normal", true, 0, 1)))
	second := result(job.Stats{Polished: true}, cand(job.Cheapest, "c2", []int64{0, 1}))
	r := &rec{steps: []func(int, *job.Job) (*job.Result, error){give(first), give(second)}}
	if _, err := (&Orchestrator{Runner: r}).Run(context.Background(), contracts.EffortThorough, baseJob()); err != nil {
		t.Fatal(err)
	}
	if got := r.jobs[1].Search.Experiments; len(got) != 0 {
		t.Errorf("a dbscan winner has no recorded neighbourhood and cannot be re-run, got %+v", got)
	}
}

func TestThoroughWithoutAWonTrialFallsBackToTheBestScore(t *testing.T) {
	first := result(job.Stats{}, cand(job.Cheapest, "c1", []int64{0, 80},
		exp("none", 0, "normal", false, 0, 90), exp("kmeans", 4, "normal", false, 0, 80), exp("kmedoids", 3, "normal", false, 0, 85)))
	r := &rec{steps: []func(int, *job.Job) (*job.Result, error){give(first), give(first)}}
	if _, err := (&Orchestrator{Runner: r}).Run(context.Background(), contracts.EffortThorough, baseJob()); err != nil {
		t.Fatal(err)
	}
	want := []job.Experiment{{Algorithm: job.AlgKMeans, K: 4, Effort: job.EffortThorough}}
	if got := r.jobs[1].Search.Experiments; !slices.Equal(got, want) {
		t.Errorf("job 2 experiments = %+v, want %+v", got, want)
	}
}

/* ---------------- reviewed ---------------- */

func plainFirst() *job.Result {
	return result(job.Stats{Shipments: 2, Trials: 8}, allPresets("r0", []int64{0, 100},
		exp("none", 0, "normal", false, 0, 100), exp("kmedoids", 3, "normal", true, 0, 100))...)
}

func TestReviewedRunsRoundsUntilTheReviewerIsSatisfiedThenPolishes(t *testing.T) {
	r1 := result(job.Stats{Trials: 8}, allPresets("r1", []int64{0, 90})...)
	polished := result(job.Stats{Trials: 4, Polished: true}, allPresets("r2", []int64{0, 88})...)
	r := &rec{steps: []func(int, *job.Job) (*job.Result, error){give(plainFirst()), give(r1), give(polished)}}
	rv := &reviewer{name: contracts.ReviewerRules, steps: []step{more(contracts.ReviewerRules, km(2), km(4)), satisfied(contracts.ReviewerRules)}}
	var heard notes
	o := &Orchestrator{Runner: r, Reviewer: rv, Notify: heard.add}
	out, err := o.Run(context.Background(), contracts.EffortReviewed, baseJob())
	if err != nil {
		t.Fatal(err)
	}

	if len(r.jobs) != 3 {
		t.Fatalf("%d jobs, want default + one round + polish", len(r.jobs))
	}
	round := r.jobs[1].Search
	if !slices.Equal(round.Experiments, []job.Experiment{km(2), km(4)}) || round.Polish != "" {
		t.Errorf("round job = %+v", round)
	}
	if tagOf(round.From[job.Cheapest]) != "r0" {
		t.Errorf("the round must start from the best so far, got %q", tagOf(round.From[job.Cheapest]))
	}
	last := r.jobs[2].Search
	if len(last.Experiments) != 0 || last.Polish != job.EffortThorough || tagOf(last.From[job.Cheapest]) != "r1" {
		t.Errorf("last job = %+v, want a polish from round 1's best", last)
	}
	want := contracts.Method{Effort: contracts.EffortReviewed, Trials: 20, Rounds: 1, Polished: true, Reviewer: contracts.ReviewerRules}
	if out.Method != want {
		t.Errorf("method = %+v, want %+v", out.Method, want)
	}
	wantHeard := notes{"experiments n=4", "round round=1", "reviewRules", "more n=2", "round round=2", "reviewRules", "satisfied", "polish"}
	if !slices.Equal(heard, wantHeard) {
		t.Errorf("heard %v, want %v", heard, wantHeard)
	}
	if len(out.Reviews) != 2 || out.Reviews[0].Satisfied || !out.Reviews[1].Satisfied || out.Reviews[0].Round != 1 {
		t.Errorf("reviews = %+v", out.Reviews)
	}

	// The reviewer was briefed with what had been found and tried.
	b := rv.briefs[1]
	if b.Round != 2 || b.MaxRounds != DefaultRounds || b.Shipments != 12 || len(b.Presets) != 4 {
		t.Errorf("brief = round %d/%d, %d shipments, %d presets", b.Round, b.MaxRounds, b.Shipments, len(b.Presets))
	}
	if len(b.History) != 2 || b.History[0][0][1] != 100 || b.History[1][0][1] != 90 {
		t.Errorf("history = %v", b.History)
	}
	for _, a := range []Attempt{{job.AlgKMedoids, 2, job.EffortNormal}, {job.AlgKMedoids, 3, job.EffortNormal}, {job.AlgNone, 0, job.EffortNormal}} {
		if !slices.Contains(b.Tried, a) {
			t.Errorf("tried = %v lacks %+v", b.Tried, a)
		}
	}
}

func TestReviewedStopsEarlyWhenTheFirstReviewIsSatisfied(t *testing.T) {
	polished := result(job.Stats{Trials: 4, Polished: true}, allPresets("r1", []int64{0, 99})...)
	r := &rec{steps: []func(int, *job.Job) (*job.Result, error){give(plainFirst()), give(polished)}}
	rv := &reviewer{name: contracts.ReviewerAI, steps: []step{satisfied(contracts.ReviewerAI)}}
	var heard notes
	out, err := (&Orchestrator{Runner: r, Reviewer: rv, Notify: heard.add}).Run(context.Background(), contracts.EffortReviewed, baseJob())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.jobs) != 2 || len(r.jobs[1].Search.Experiments) != 0 || r.jobs[1].Search.Polish != job.EffortThorough {
		t.Fatalf("jobs = %d, want the default job then the polish", len(r.jobs))
	}
	// Zero rounds ran, but the method still says who was asked.
	if out.Method.Rounds != 0 || out.Method.Reviewer != contracts.ReviewerAI {
		t.Errorf("method = %+v", out.Method)
	}
	if want := (notes{"experiments n=4", "round round=1", "reviewAI", "satisfied", "polish"}); !slices.Equal(heard, want) {
		t.Errorf("heard %v, want %v", heard, want)
	}
}

func TestReviewedStopsAtTheRoundCap(t *testing.T) {
	for _, tc := range []struct{ rounds, want int }{{0, DefaultRounds}, {1, 1}, {2, 2}, {5, 5}} {
		steps := []func(int, *job.Job) (*job.Result, error){give(plainFirst())}
		var rsteps []step
		for i := 0; i < tc.want; i++ {
			steps = append(steps, give(result(job.Stats{Trials: 1}, allPresets(fmt.Sprintf("r%d", i+1), []int64{0, int64(100 - i)})...)))
			rsteps = append(rsteps, more(contracts.ReviewerRules, km(4+i)))
		}
		steps = append(steps, give(result(job.Stats{Polished: true}, allPresets("end", []int64{0, 1})...)))
		r := &rec{steps: steps}
		rv := &reviewer{name: contracts.ReviewerRules, steps: rsteps}
		out, err := (&Orchestrator{Runner: r, Reviewer: rv, Rounds: tc.rounds}).Run(context.Background(), contracts.EffortReviewed, baseJob())
		if err != nil {
			t.Fatal(err)
		}
		if len(r.jobs) != tc.want+2 || len(rv.briefs) != tc.want {
			t.Errorf("rounds %d: %d jobs and %d reviews, want %d and %d", tc.rounds, len(r.jobs), len(rv.briefs), tc.want+2, tc.want)
		}
		if out.Method.Rounds != tc.want {
			t.Errorf("rounds %d: method rounds = %d", tc.rounds, out.Method.Rounds)
		}
		if last := r.jobs[len(r.jobs)-1].Search; last.Polish == "" || len(last.Experiments) != 0 {
			t.Errorf("rounds %d: the last job must be the polish, got %+v", tc.rounds, last)
		}
	}
}

func TestReviewedWithoutAReviewerUsesTheRules(t *testing.T) {
	// A plain candidate set the rules find satisfactory: valid, nothing unserved, no overlap data.
	r := &rec{steps: []func(int, *job.Job) (*job.Result, error){give(plainFirst()), give(result(job.Stats{Polished: true}, allPresets("end", []int64{0, 99})...))}}
	var heard notes
	out, err := (&Orchestrator{Runner: r, Notify: heard.add}).Run(context.Background(), contracts.EffortReviewed, baseJob())
	if err != nil {
		t.Fatal(err)
	}
	if out.Method.Reviewer != contracts.ReviewerRules || len(r.jobs) != 2 {
		t.Errorf("reviewer %q, %d jobs", out.Method.Reviewer, len(r.jobs))
	}
	if !slices.Contains(heard, "reviewRules") || slices.Contains(heard, "reviewAI") {
		t.Errorf("heard %v", heard)
	}
}

func TestFromCarriesTheBestOfAllJobsNotTheLatest(t *testing.T) {
	good := result(job.Stats{Trials: 4}, allPresets("good", []int64{0, 90})...)
	worse := result(job.Stats{Trials: 4}, allPresets("worse", []int64{0, 95})...)
	end := result(job.Stats{Polished: true}, allPresets("end", []int64{0, 95})...)
	r := &rec{steps: []func(int, *job.Job) (*job.Result, error){give(plainFirst()), give(good), give(worse), give(end)}}
	rv := &reviewer{name: contracts.ReviewerRules, steps: []step{more(contracts.ReviewerRules, km(2)), more(contracts.ReviewerRules, km(4))}}
	out, err := (&Orchestrator{Runner: r, Reviewer: rv, Rounds: 2}).Run(context.Background(), contracts.EffortReviewed, baseJob())
	if err != nil {
		t.Fatal(err)
	}
	if got := tagOf(r.jobs[2].Search.From[job.Cheapest]); got != "good" {
		t.Errorf("round 2 starts from %q, want the best so far (good)", got)
	}
	if got := tagOf(r.jobs[3].Search.From[job.Cheapest]); got != "good" {
		t.Errorf("the polish starts from %q, want good: a worse later job must not displace it", got)
	}
	for _, c := range out.Result.Candidates {
		if tagOf(c.Routes) != "good" {
			t.Errorf("%s ends with %q", c.Preset, tagOf(c.Routes))
		}
	}
}

func TestAnInvalidCandidateNeverStartsAJobEvenWithAGoodScore(t *testing.T) {
	cheat := cand(job.Cheapest, "cheat", []int64{0, 1})
	cheat.Facts = &job.Facts{Valid: false}
	r1 := result(job.Stats{}, cheat, cand(job.Shortest, "ok", []int64{0, 90}))
	end := result(job.Stats{}, cand(job.Cheapest, "end", []int64{0, 95}), cand(job.Shortest, "end", []int64{0, 95}))
	first := result(job.Stats{}, cand(job.Cheapest, "r0", []int64{0, 100}), cand(job.Shortest, "r0", []int64{0, 100}))
	r := &rec{steps: []func(int, *job.Job) (*job.Result, error){give(first), give(r1), give(end)}}
	rv := &reviewer{name: contracts.ReviewerRules, steps: []step{more(contracts.ReviewerRules, km(2)), satisfied(contracts.ReviewerRules)}}
	if _, err := (&Orchestrator{Runner: r, Reviewer: rv}).Run(context.Background(), contracts.EffortReviewed, baseJob()); err != nil {
		t.Fatal(err)
	}
	if got := tagOf(r.jobs[2].Search.From[job.Cheapest]); got != "r0" {
		t.Errorf("polish starts cheapest from %q: the invalid candidate with the lower score must be ignored", got)
	}
	if got := tagOf(r.jobs[2].Search.From[job.Shortest]); got != "ok" {
		t.Errorf("polish starts shortest from %q, want the better valid one", got)
	}
}

func TestTheOrchestratorHoldsReviewersToTheRules(t *testing.T) {
	t.Run("repeats and surplus are dropped", func(t *testing.T) {
		r := &rec{steps: []func(int, *job.Job) (*job.Result, error){
			give(plainFirst()),
			give(result(job.Stats{}, allPresets("r1", []int64{0, 99})...)),
			give(result(job.Stats{}, allPresets("r2", []int64{0, 98})...)),
		}}
		ask := []job.Experiment{
			{Algorithm: job.AlgKMedoids, Effort: job.EffortNormal}, // sent in job 1
			{Algorithm: job.AlgKMedoids, K: 3},                     // the k the silhouette chose, effort empty means normal
			km(2), km(2), km(4), km(5), km(6),
		}
		rv := &reviewer{name: contracts.ReviewerRules, steps: []step{more(contracts.ReviewerRules, ask...), satisfied(contracts.ReviewerRules)}}
		if _, err := (&Orchestrator{Runner: r, Reviewer: rv}).Run(context.Background(), contracts.EffortReviewed, baseJob()); err != nil {
			t.Fatal(err)
		}
		if got := r.jobs[1].Search.Experiments; !slices.Equal(got, []job.Experiment{km(2), km(4), km(5)}) {
			t.Errorf("round job asked for %+v, want km(2), km(4), km(5)", got)
		}
	})
	t.Run("nothing admissible means satisfied", func(t *testing.T) {
		r := &rec{steps: []func(int, *job.Job) (*job.Result, error){give(plainFirst()), give(result(job.Stats{Polished: true}, allPresets("end", []int64{0, 99})...))}}
		rv := &reviewer{name: contracts.ReviewerRules, steps: []step{more(contracts.ReviewerRules, job.Experiment{Algorithm: "magic", K: 2}, job.Experiment{Algorithm: job.AlgKMeans, K: 99})}}
		var heard notes
		out, err := (&Orchestrator{Runner: r, Reviewer: rv, Notify: heard.add}).Run(context.Background(), contracts.EffortReviewed, baseJob())
		if err != nil {
			t.Fatal(err)
		}
		if len(r.jobs) != 2 || !slices.Contains(heard, "satisfied") || slices.Contains(heard, "more n=0") {
			t.Errorf("%d jobs, heard %v", len(r.jobs), heard)
		}
		if !out.Reviews[0].Satisfied {
			t.Errorf("reviews = %+v", out.Reviews)
		}
	})
}

/* ---------------- who reviewed ---------------- */

func TestTheAIReviewerFailingHandsTheRoundToTheRules(t *testing.T) {
	steps := []func(int, *job.Job) (*job.Result, error){give(plainFirst()),
		give(result(job.Stats{}, allPresets("r1", []int64{0, 90})...)),
		give(result(job.Stats{}, allPresets("r2", []int64{0, 80})...)),
		give(result(job.Stats{Polished: true}, allPresets("end", []int64{0, 79})...))}
	r := &rec{steps: steps}
	// The model fails in round 1. The rules take that round: they find this candidate set fine and are satisfied at
	// once, so the model is never asked again.
	ai := &reviewer{name: contracts.ReviewerAI, steps: []step{{err: errors.New("gateway down")}}}
	var heard notes
	out, err := (&Orchestrator{Runner: r, Reviewer: ai, Notify: heard.add}).Run(context.Background(), contracts.EffortReviewed, baseJob())
	if err != nil {
		t.Fatal(err)
	}
	if out.Method.Reviewer != contracts.ReviewerRules {
		t.Errorf("reviewer = %q, want rules: the model did not decide the round", out.Method.Reviewer)
	}
	if len(out.Reviews) != 1 || out.Reviews[0].By != contracts.ReviewerRules || !out.Reviews[0].Fallback {
		t.Errorf("reviews = %+v", out.Reviews)
	}
	if want := (notes{"experiments n=4", "round round=1", "reviewAI", "reviewRules", "satisfied", "polish"}); !slices.Equal(heard, want) {
		t.Errorf("heard %v, want %v", heard, want)
	}
}

func TestMethodSaysAIOnlyWhenEveryRoundWasTheModels(t *testing.T) {
	run := func(rv *reviewer) contracts.Method {
		t.Helper()
		steps := []func(int, *job.Job) (*job.Result, error){give(plainFirst())}
		for i := 0; i < 3; i++ {
			steps = append(steps, give(result(job.Stats{}, allPresets(fmt.Sprintf("r%d", i), []int64{0, int64(90 - i)})...)))
		}
		out, err := (&Orchestrator{Runner: &rec{steps: steps}, Reviewer: rv, Rounds: 2}).Run(context.Background(), contracts.EffortReviewed, baseJob())
		if err != nil {
			t.Fatal(err)
		}
		return out.Method
	}
	allAI := &reviewer{name: contracts.ReviewerAI, steps: []step{more(contracts.ReviewerAI, km(2)), satisfied(contracts.ReviewerAI)}}
	if m := run(allAI); m.Reviewer != contracts.ReviewerAI || m.Rounds != 1 {
		t.Errorf("all rounds by the model: %+v", m)
	}
	mixed := &reviewer{name: contracts.ReviewerAI, steps: []step{more(contracts.ReviewerAI, km(2)), {err: errors.New("timeout")}}}
	// Round 2 falls back to the rules, which find the plain candidates satisfactory.
	if m := run(mixed); m.Reviewer != contracts.ReviewerRules {
		t.Errorf("one round by the rules: %+v", m)
	}
}

/* ---------------- failures and the clock ---------------- */

func TestFailureOfTheFirstJobIsTheQuotesFailure(t *testing.T) {
	invalid := job.Fail(job.CodeInvalid, "too many points")
	for _, effort := range []contracts.Effort{contracts.EffortThorough, contracts.EffortReviewed} {
		r := &rec{steps: []func(int, *job.Job) (*job.Result, error){fail(invalid)}}
		out, err := (&Orchestrator{Runner: r}).Run(context.Background(), effort, baseJob())
		var rejected *job.Error
		if out != nil || !errors.As(err, &rejected) || rejected.Code != job.CodeInvalid {
			t.Errorf("%s: out %v, err %v, want the solver's own *job.Error", effort, out, err)
		}
	}
}

func TestFailureAfterTheFirstJobReturnsTheBestSoFar(t *testing.T) {
	boom := errors.New("sandbox lost")
	t.Run("thorough", func(t *testing.T) {
		r := &rec{steps: []func(int, *job.Job) (*job.Result, error){give(plainFirst()), fail(boom)}}
		out, err := (&Orchestrator{Runner: r}).Run(context.Background(), contracts.EffortThorough, baseJob())
		if err != nil {
			t.Fatalf("a failure after job 1 must not fail the quote: %v", err)
		}
		if !out.Truncated || !errors.Is(out.Err, boom) {
			t.Errorf("truncated %v, err %v", out.Truncated, out.Err)
		}
		if out.Method.Polished || out.Method.Trials != 8 || out.Method.Effort != contracts.EffortThorough {
			t.Errorf("method = %+v must say only what ran", out.Method)
		}
		if got := tagOf(out.Result.Candidates[0].Routes); got != "r0" {
			t.Errorf("result = %q, want job 1's", got)
		}
	})
	t.Run("a round", func(t *testing.T) {
		r := &rec{steps: []func(int, *job.Job) (*job.Result, error){give(plainFirst()), fail(boom)}}
		rv := &reviewer{name: contracts.ReviewerAI, steps: []step{more(contracts.ReviewerAI, km(2))}}
		out, err := (&Orchestrator{Runner: r, Reviewer: rv}).Run(context.Background(), contracts.EffortReviewed, baseJob())
		if err != nil {
			t.Fatal(err)
		}
		if len(r.jobs) != 2 {
			t.Errorf("%d jobs: after a failure nothing more is attempted, not even the polish", len(r.jobs))
		}
		if !out.Truncated || out.Method.Rounds != 0 || out.Method.Polished || out.Method.Reviewer != contracts.ReviewerAI {
			t.Errorf("truncated %v, method %+v", out.Truncated, out.Method)
		}
	})
	t.Run("the polish", func(t *testing.T) {
		r := &rec{steps: []func(int, *job.Job) (*job.Result, error){give(plainFirst()), give(result(job.Stats{}, allPresets("r1", []int64{0, 90})...)), fail(boom)}}
		rv := &reviewer{name: contracts.ReviewerRules, steps: []step{more(contracts.ReviewerRules, km(2)), satisfied(contracts.ReviewerRules)}}
		out, err := (&Orchestrator{Runner: r, Reviewer: rv}).Run(context.Background(), contracts.EffortReviewed, baseJob())
		if err != nil {
			t.Fatal(err)
		}
		if !out.Truncated || out.Method.Rounds != 1 || out.Method.Polished {
			t.Errorf("truncated %v, method %+v", out.Truncated, out.Method)
		}
		if got := tagOf(out.Result.Candidates[0].Routes); got != "r1" {
			t.Errorf("result = %q, want round 1's", got)
		}
	})
}

func TestTruncationOfAnyJobMarksTheQuote(t *testing.T) {
	cut := allPresets("cut", []int64{0, 120})
	cut[1].Truncated = true // a worse candidate of a later job: it loses, but its search was cut short
	r := &rec{steps: []func(int, *job.Job) (*job.Result, error){give(plainFirst()), give(result(job.Stats{Polished: true}, cut...))}}
	out, err := (&Orchestrator{Runner: r}).Run(context.Background(), contracts.EffortThorough, baseJob())
	if err != nil {
		t.Fatal(err)
	}
	if !out.Truncated {
		t.Error("a truncated job must mark the quote truncated, whichever candidate won")
	}
	if got := tagOf(out.Result.Candidates[1].Routes); got != "r0" || !out.Result.Candidates[1].Truncated {
		t.Errorf("candidate = %q truncated %v: the winner stays, the flag follows the preset", got, out.Result.Candidates[1].Truncated)
	}
	if out.Result.Candidates[0].Truncated {
		t.Error("only the preset whose search was cut short carries the flag")
	}
}

func TestTruncationStaysEvenWhenALaterJobIsClean(t *testing.T) {
	cutFirst := plainFirst()
	cutFirst.Candidates[0].Truncated = true
	r := &rec{steps: []func(int, *job.Job) (*job.Result, error){give(cutFirst), give(result(job.Stats{Polished: true}, allPresets("end", []int64{0, 99})...))}}
	out, err := (&Orchestrator{Runner: r}).Run(context.Background(), contracts.EffortThorough, baseJob())
	if err != nil {
		t.Fatal(err)
	}
	if !out.Truncated || !out.Result.Candidates[0].Truncated {
		t.Errorf("job 1 was cut short and job 2 was not: the quote and the preset stay truncated (%v, %v)", out.Truncated, out.Result.Candidates[0].Truncated)
	}
}

func TestEachDeepJobGetsAShareOfTheTimeLeft(t *testing.T) {
	r := &rec{steps: []func(int, *job.Job) (*job.Result, error){give(plainFirst()), give(plainFirst())}}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	if _, err := (&Orchestrator{Runner: r}).Run(ctx, contracts.EffortThorough, baseJob()); err != nil {
		t.Fatal(err)
	}
	first, second := r.jobs[0].Limits.MaxSeconds, r.jobs[1].Limits.MaxSeconds
	if first < 45 || first > 50 {
		t.Errorf("job 1 of 2 got %ds of 100s, want about half", first)
	}
	if second < 95 || second > 100 {
		t.Errorf("the last job gets what is left, got %ds", second)
	}

	// A cap the caller set wins, and without a deadline a job gets the default.
	capped := baseJob()
	capped.Limits.MaxSeconds = 7
	r = &rec{steps: []func(int, *job.Job) (*job.Result, error){give(plainFirst()), give(plainFirst())}}
	if _, err := (&Orchestrator{Runner: r}).Run(context.Background(), contracts.EffortThorough, capped); err != nil {
		t.Fatal(err)
	}
	if r.jobs[0].Limits.MaxSeconds != 7 || r.jobs[1].Limits.MaxSeconds != 7 {
		t.Errorf("limits = %d, %d, want the caller's 7", r.jobs[0].Limits.MaxSeconds, r.jobs[1].Limits.MaxSeconds)
	}
	r = &rec{steps: []func(int, *job.Job) (*job.Result, error){give(plainFirst()), give(plainFirst())}}
	if _, err := (&Orchestrator{Runner: r}).Run(context.Background(), contracts.EffortThorough, baseJob()); err != nil {
		t.Fatal(err)
	}
	if r.jobs[0].Limits.MaxSeconds != defaultJobSeconds {
		t.Errorf("limit without a deadline = %d, want %d", r.jobs[0].Limits.MaxSeconds, defaultJobSeconds)
	}
}

func TestTheReserveIsKeptBackForWhatFollowsADeepSearch(t *testing.T) {
	var deadlines []time.Time
	probe := runner.Func(func(c context.Context, _ *job.Job, _ func(job.Progress)) (*job.Result, error) {
		d, _ := c.Deadline()
		deadlines = append(deadlines, d)
		return plainFirst(), nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	outer, _ := ctx.Deadline()
	o := &Orchestrator{Runner: probe, Reserve: 10 * time.Second}

	if _, err := o.Run(ctx, contracts.EffortThorough, baseJob()); err != nil {
		t.Fatal(err)
	}
	for i, d := range deadlines {
		if got := outer.Sub(d); got < 9*time.Second || got > 11*time.Second {
			t.Errorf("job %d: the search deadline is %v before the caller's, want the 10s reserve", i+1, got)
		}
	}

	// A fast quote is one plain job and keeps the caller's deadline.
	deadlines = nil
	if _, err := o.Run(ctx, contracts.EffortFast, baseJob()); err != nil {
		t.Fatal(err)
	}
	if len(deadlines) != 1 || !deadlines[0].Equal(outer) {
		t.Errorf("a fast quote's deadline = %v, want the caller's %v", deadlines, outer)
	}
}

func TestWhenTheTimeIsGoneTheSearchStopsAndSaysSo(t *testing.T) {
	// Less than the minimum job time at the start: job 1 is refused, which is the quote's failure.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r := &rec{steps: []func(int, *job.Job) (*job.Result, error){give(plainFirst())}}
	if _, err := (&Orchestrator{Runner: r}).Run(ctx, contracts.EffortReviewed, baseJob()); !errors.Is(err, errBudget) || len(r.jobs) != 0 {
		t.Fatalf("err = %v with %d jobs run, want the budget error and none", err, len(r.jobs))
	}

	// Time runs out while job 1 runs: the rounds and the polish are skipped, the best found stays, the quote is
	// truncated, and nobody was asked to review.
	ctx, cancel = context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	ran := 0
	slow := runner.Func(func(context.Context, *job.Job, func(job.Progress)) (*job.Result, error) {
		ran++
		time.Sleep(1300 * time.Millisecond)
		return plainFirst(), nil
	})
	rv := &reviewer{name: contracts.ReviewerAI}
	out, err := (&Orchestrator{Runner: slow, Reviewer: rv}).Run(ctx, contracts.EffortReviewed, baseJob())
	if err != nil {
		t.Fatal(err)
	}
	if !out.Truncated || !errors.Is(out.Err, errBudget) || ran != 1 || len(rv.briefs) != 0 {
		t.Errorf("truncated %v, err %v, %d jobs, %d reviews", out.Truncated, out.Err, ran, len(rv.briefs))
	}
	if out.Method.Reviewer != contracts.ReviewerAI || out.Method.Rounds != 0 || out.Method.Polished {
		t.Errorf("method = %+v, want the reviewer that would have been asked", out.Method)
	}
}

func TestTheBaseJobIsNeverChanged(t *testing.T) {
	r := &rec{steps: []func(int, *job.Job) (*job.Result, error){give(plainFirst()), give(plainFirst())}}
	base := baseJob()
	if _, err := (&Orchestrator{Runner: r}).Run(context.Background(), contracts.EffortThorough, base); err != nil {
		t.Fatal(err)
	}
	if base.Search != nil || base.ID != "PL-1" || base.Limits.MaxSeconds != 0 {
		t.Errorf("base = %+v", base)
	}
}
