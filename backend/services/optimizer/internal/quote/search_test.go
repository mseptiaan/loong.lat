package quote

import (
	"context"
	"encoding/json/v2"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi/aiapitest"
	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/orchestrate/orchestratetest"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/roads"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/runner"
)

func trio(effort contracts.Effort) Request {
	r := request(contracts.PresetFastest, parcel("s1", kemang, senayan, 3), parcel("s2", blokM, pondok, 4), parcel("s3", senopati, monas, 2))
	r.Effort = effort
	return r
}

// searcher plans with a runner that answers a Search the way a searching solver does, on top of the real plain solve.
func searcher() *Planner {
	// A short search keeps the tests quick; what is asserted here is the orchestration, not the quality of the routes.
	return &Planner{Roads: roads.Estimate{}, Runner: orchestratetest.Searching(runner.Local{}), Limits: job.Limits{Iterations: 40}}
}

type seen struct {
	stages []Stage
	steps  []string
	// order is every update as "stage" or "stage:key", to check where the steps fall.
	order []string
}

func (s *seen) hear(u Update) {
	if u.Msg == nil {
		s.stages = append(s.stages, u.Stage)
		s.order = append(s.order, string(rune('0'+int(u.Stage))))
		return
	}
	key := strings.TrimPrefix(u.Msg.Key, "srv.engine.search.")
	s.steps = append(s.steps, key)
	s.order = append(s.order, string(rune('0'+int(u.Stage)))+":"+key)
}

func TestAFastQuoteSaysHowItWasFound(t *testing.T) {
	for _, effort := range []contracts.Effort{"", contracts.EffortFast} {
		res, err := searcher().Compute(context.Background(), trio(effort), nil)
		if err != nil {
			t.Fatal(err)
		}
		m := res.Set.Method
		if m == nil {
			t.Fatal("no method")
		}
		want := contracts.Method{Effort: contracts.EffortFast, Trials: len(job.Presets), Proven: true, RoadSource: "estimate"}
		if *m != want {
			t.Errorf("effort %q: method = %+v, want %+v", effort, *m, want)
		}
		if m.Proven != res.Proven || m.RoadSource != res.RoadSource {
			t.Errorf("the method and the result disagree: %+v vs proven %v roads %q", *m, res.Proven, res.RoadSource)
		}
	}
}

func TestAFastQuoteIsOneJobWithoutASearch(t *testing.T) {
	var jobs []*job.Job
	p := planner()
	p.Runner = runner.Func(func(ctx context.Context, j *job.Job, _ func(job.Progress)) (*job.Result, error) {
		jobs = append(jobs, j)
		return runner.Local{}.Run(ctx, j, nil)
	})
	if _, err := p.Compute(context.Background(), trio(contracts.EffortFast), nil); err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Search != nil {
		t.Fatalf("%d jobs, search %+v: a fast quote is one plain job", len(jobs), jobs[0].Search)
	}
}

func TestAThoroughQuoteReportsItsStepsInsideTheCombineStage(t *testing.T) {
	var heard seen
	res, err := searcher().Compute(context.Background(), trio(contracts.EffortThorough), heard.hear)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(heard.stages, []Stage{StageReadPoints, StageMeasure, StageCombine, StageCompare}) {
		t.Errorf("stages = %v", heard.stages)
	}
	// The stub's baseline wins every preset, so job 2 re-runs the one winner: one experiment, then the polish.
	if want := []string{"experiments", "experiments", "polish"}; !slices.Equal(heard.steps, want) {
		t.Errorf("steps = %v, want %v", heard.steps, want)
	}
	if want := []string{"1", "2", "3", "3:experiments", "3:experiments", "3:polish", "4"}; !slices.Equal(heard.order, want) {
		t.Errorf("order = %v, want %v: the steps belong inside stage 3", heard.order, want)
	}

	m := res.Set.Method
	if m == nil || m.Effort != contracts.EffortThorough || !m.Polished || m.Trials == 0 || m.Rounds != 0 || m.Reviewer != "" || m.RoadSource != "estimate" {
		t.Errorf("method = %+v", m)
	}
	checkOptionSet(t, res.Set, trio(contracts.EffortThorough))
}

func TestADeepQuoteNeverLosesToAFastOne(t *testing.T) {
	// The stub ties every experiment with the plain solve, and a tie keeps the earliest job's routes: the plain solve.
	// So the options of a deep quote are exactly those of a fast one.
	options := func(effort contracts.Effort) string {
		res, err := searcher().Compute(context.Background(), trio(effort), nil)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(res.Set.Options, json.Deterministic(true))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	fast := options(contracts.EffortFast)
	for _, effort := range []contracts.Effort{contracts.EffortThorough, contracts.EffortReviewed} {
		if got := options(effort); got != fast {
			t.Errorf("%s: the options differ from the fast quote's although nothing found better", effort)
		}
	}
}

func TestEffortIsPartOfWhatAQuoteDependsOn(t *testing.T) {
	hash := func(effort contracts.Effort) string {
		res, err := searcher().Compute(context.Background(), trio(effort), nil)
		if err != nil {
			t.Fatal(err)
		}
		return res.Set.InputHash
	}
	fast, thorough, reviewed := hash(contracts.EffortFast), hash(contracts.EffortThorough), hash(contracts.EffortReviewed)
	if fast != hash("") {
		t.Error("an empty effort is fast, so the hash must be the same")
	}
	if fast == thorough || thorough == reviewed || fast == reviewed {
		t.Errorf("hashes must differ by effort: fast %s, thorough %s, reviewed %s", fast, thorough, reviewed)
	}
}

func TestAnUnknownEffortIsAnInputError(t *testing.T) {
	_, err := searcher().Compute(context.Background(), trio("extreme"), nil)
	var qe *Error
	if !errors.As(err, &qe) || qe.Code != CodeInvalid || qe.Field != "effort" {
		t.Errorf("err = %v, want an invalid-input error on effort", err)
	}
}

func says(answer string) aiapitest.Step { return aiapitest.Say(answer) }

const (
	askMore   = `{"satisfied":false,"experiments":[{"algorithm":"kmedoids","k":2,"effort":"normal"}],"note":"try two"}`
	satisfied = `{"satisfied":true,"experiments":[],"note":"enough"}`
)

func TestAReviewedQuoteIsReviewedByTheModelWhenAllowedAndByRulesOtherwise(t *testing.T) {
	run := func(allow bool, model *aiapitest.Fake) (*Result, []string) {
		t.Helper()
		p := searcher()
		if model != nil {
			p.AI = model
		}
		r := trio(contracts.EffortReviewed)
		r.AIReview = allow
		var heard seen
		res, err := p.Compute(context.Background(), r, heard.hear)
		if err != nil {
			t.Fatal(err)
		}
		return res, heard.steps
	}

	t.Run("the model", func(t *testing.T) {
		model := aiapitest.New(says(askMore), says(satisfied))
		res, steps := run(true, model)
		m := res.Set.Method
		if m.Reviewer != contracts.ReviewerAI || m.Rounds != 1 || !m.Polished || m.Effort != contracts.EffortReviewed {
			t.Errorf("method = %+v", m)
		}
		want := []string{"experiments", "round", "reviewAI", "more", "round", "reviewAI", "satisfied", "polish"}
		if !slices.Equal(steps, want) {
			t.Errorf("steps = %v, want %v", steps, want)
		}
		if model.Calls() != 2 {
			t.Errorf("%d model calls", model.Calls())
		}
	})
	t.Run("the caller did not allow the model", func(t *testing.T) {
		model := aiapitest.New()
		res, steps := run(false, model)
		if res.Set.Method.Reviewer != contracts.ReviewerRules || model.Calls() != 0 || slices.Contains(steps, "reviewAI") {
			t.Errorf("method %+v, %d model calls, steps %v", res.Set.Method, model.Calls(), steps)
		}
	})
	t.Run("there is no gateway", func(t *testing.T) {
		res, steps := run(true, nil)
		if res.Set.Method.Reviewer != contracts.ReviewerRules || slices.Contains(steps, "reviewAI") {
			t.Errorf("method %+v, steps %v", res.Set.Method, steps)
		}
	})
	t.Run("the model fails", func(t *testing.T) {
		res, steps := run(true, aiapitest.New(aiapitest.Fail(errors.New("gateway down"))))
		if res.Set.Method.Reviewer != contracts.ReviewerRules {
			t.Errorf("method = %+v", res.Set.Method)
		}
		if want := []string{"experiments", "round", "reviewAI", "reviewRules", "satisfied", "polish"}; !slices.Equal(steps, want) {
			t.Errorf("steps = %v, want %v", steps, want)
		}
	})
}

func TestAFailureAfterTheFirstJobStillGivesAQuote(t *testing.T) {
	calls := 0
	inner := orchestratetest.Searching(runner.Local{})
	p := searcher()
	p.Runner = runner.Func(func(ctx context.Context, j *job.Job, pr func(job.Progress)) (*job.Result, error) {
		calls++
		if calls > 1 {
			return nil, errors.New("sandbox lost")
		}
		return inner.Run(ctx, j, pr)
	})
	res, err := p.Compute(context.Background(), trio(contracts.EffortThorough), nil)
	if err != nil {
		t.Fatalf("the first job's answers are a quote: %v", err)
	}
	if !res.Truncated {
		t.Error("a search that was cut short must not look canonical")
	}
	if m := res.Set.Method; m.Polished || m.Trials != 4*4 {
		t.Errorf("method = %+v, want only what ran (the default experiments over four presets)", m)
	}
	checkOptionSet(t, res.Set, trio(contracts.EffortThorough))
}

func TestAFailureOfTheFirstJobOfADeepQuoteIsAServiceError(t *testing.T) {
	p := searcher()
	p.Runner = runner.Func(func(context.Context, *job.Job, func(job.Progress)) (*job.Result, error) {
		return nil, errors.New("sandbox exploded")
	})
	for _, effort := range []contracts.Effort{contracts.EffortThorough, contracts.EffortReviewed} {
		_, err := p.Compute(context.Background(), trio(effort), nil)
		var qe *Error
		if !errors.As(err, &qe) || qe.Code != CodeService {
			t.Errorf("%s: err = %v, want a service error", effort, err)
		}
	}
}

func TestATruncatedJobMarksTheQuote(t *testing.T) {
	inner := orchestratetest.Searching(runner.Local{})
	calls := 0
	p := searcher()
	p.Runner = runner.Func(func(ctx context.Context, j *job.Job, pr func(job.Progress)) (*job.Result, error) {
		res, err := inner.Run(ctx, j, pr)
		if calls++; err == nil && calls == 2 {
			res.Candidates[0].Truncated = true
		}
		return res, err
	})
	res, err := p.Compute(context.Background(), trio(contracts.EffortThorough), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated {
		t.Error("a job whose search was cut by the time limit makes the quote truncated, even if another job's answer won")
	}
	if fast, _ := searcher().Compute(context.Background(), trio(contracts.EffortFast), nil); fast.Truncated {
		t.Error("a quote nobody cut short is not truncated")
	}
}

func TestTheRoundsAreCappedByThePlanner(t *testing.T) {
	jobs := 0
	inner := orchestratetest.Searching(runner.Local{})
	p := searcher()
	p.Rounds = 1
	p.Runner = runner.Func(func(ctx context.Context, j *job.Job, pr func(job.Progress)) (*job.Result, error) {
		jobs++
		return inner.Run(ctx, j, pr)
	})
	// A model that always wants more: without the cap it would keep proposing new experiments.
	p.AI = aiapitest.New(says(askMore), says(`{"satisfied":false,"experiments":[{"algorithm":"kmeans","k":2,"effort":"normal"}],"note":""}`), says(satisfied))
	r := trio(contracts.EffortReviewed)
	r.AIReview = true
	res, err := p.Compute(context.Background(), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if jobs != 3 || res.Set.Method.Rounds != 1 {
		t.Errorf("%d jobs and %d rounds, want the default job, one round and the polish", jobs, res.Set.Method.Rounds)
	}
}

// With the real solver in process: a thorough and a reviewed quote complete, say how they were found, are never dearer
// than the fast one, and serve every shipment. Skipped while the solver does not honour Job.Search.
func TestDeepQuotesOnTheRealSolver(t *testing.T) {
	var first *job.Result
	local := runner.Local{}
	p := &Planner{Roads: roads.Estimate{}, Limits: job.Limits{Iterations: 60}, Runner: runner.Func(func(ctx context.Context, j *job.Job, pr func(job.Progress)) (*job.Result, error) {
		res, err := local.Run(ctx, j, pr)
		if first == nil && res != nil && j.Search != nil {
			first = res
		}
		return res, err
	})}
	r := trio(contracts.EffortThorough)
	thorough, err := p.Compute(context.Background(), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first != nil && (len(first.Candidates) == 0 || len(first.Candidates[0].Trials) == 0) {
		t.Skip("solve.Run does not yet honour Job.Search (a search job came back without trials); skipping the quote tests against the real solver")
	}
	r.Effort = contracts.EffortFast
	fast, err := p.Compute(context.Background(), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Effort = contracts.EffortReviewed
	reviewed, err := p.Compute(context.Background(), r, nil)
	if err != nil {
		t.Fatal(err)
	}

	if m := thorough.Set.Method; m.Effort != contracts.EffortThorough || !m.Polished || m.Trials <= fast.Set.Method.Trials {
		t.Errorf("thorough method = %+v against fast %+v", m, fast.Set.Method)
	}
	if m := reviewed.Set.Method; m.Effort != contracts.EffortReviewed || m.Reviewer != contracts.ReviewerRules || !m.Polished {
		t.Errorf("reviewed method = %+v", m)
	}
	cost := func(set contracts.OptionSet, preset contracts.Preset) int64 {
		for _, o := range set.Options {
			if o.Preset == preset {
				return o.Totals.CostRp
			}
		}
		return -1
	}
	for _, res := range []*Result{thorough, reviewed} {
		checkOptionSet(t, res.Set, trio(res.Set.Method.Effort))
		if res.Truncated {
			t.Errorf("%s was truncated", res.Set.Method.Effort)
		}
		if got, base := cost(res.Set, contracts.PresetCheapest), cost(fast.Set, contracts.PresetCheapest); base >= 0 && got > base {
			t.Errorf("%s: the cheapest option costs %d, the fast quote's costs %d", res.Set.Method.Effort, got, base)
		}
	}
}
