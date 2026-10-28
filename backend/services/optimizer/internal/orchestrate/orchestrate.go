// Package orchestrate runs a quote's search over several jobs. A sandbox is stateless, so whatever links one job to
// the next (the best routes found so far, what was already tried, how the reviewer decided) is kept here, in the
// optimizer service, and handed to each job as its Search.
//
// The three efforts of contracts.Effort become sequences of jobs:
//
//	fast      one job without a Search: the plain solve of every preset, exactly the quick quote.
//	thorough  job 1 tries the default experiments; job 2 re-runs the winners at effort
//	          thorough and then polishes the best from where it stands.
//	reviewed  job 1 as above, then up to Rounds rounds in which a Reviewer audits the best candidates and either is
//	          satisfied (the search stops) or asks for up to three more experiments; a last job polishes the best.
//
// The results are merged by Score (the engine's own objective vector), so a deeper search is never worse than the
// quick one: its first experiment is the plain solve.
package orchestrate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/runner"
)

// DefaultRounds is how many review rounds a reviewed quote may take.
const DefaultRounds = 3

// Time budget of a job. Without a deadline in the context a job gets defaultJobSeconds; with one, what is left is
// shared by the jobs still to come, and a job that would get less than minJobTime is not started.
const (
	defaultJobSeconds = 40
	minJobTime        = 3 * time.Second
)

// Catalog keys of the steps a search reports, with their params (all integers). They are shown to the sender inside
// the "combining routes" stage of the wizard.
const (
	MsgExperiments = "srv.engine.search.experiments" // {n}: n experiments are about to be tried
	MsgPolish      = "srv.engine.search.polish"      // the best answers are about to be polished
	MsgRound       = "srv.engine.search.round"       // {round}: a review round begins
	MsgReviewAI    = "srv.engine.search.reviewAI"    // the AI reviewer is being asked
	MsgReviewRules = "srv.engine.search.reviewRules" // the rules reviewer is being asked
	MsgSatisfied   = "srv.engine.search.satisfied"   // the reviewer is satisfied: the search stops
	MsgMore        = "srv.engine.search.more"        // {n}: the reviewer asked for n more experiments
)

// DefaultExperiments is the default strategy at effort normal: every clustering algorithm with the
// silhouette's k, plus the no-clustering baseline. The baseline comes first, and a solver reproduces the plain
// solve with it, which is what makes a search never worse than a quick quote.
func DefaultExperiments() []job.Experiment {
	return []job.Experiment{
		{Algorithm: job.AlgNone, Effort: job.EffortNormal},
		{Algorithm: job.AlgKMedoids, Effort: job.EffortNormal},
		{Algorithm: job.AlgKMeans, Effort: job.EffortNormal},
		{Algorithm: job.AlgAgglomerative, Effort: job.EffortNormal},
	}
}

// Orchestrator drives a Runner through the jobs of one quote. Make one per quote: it carries the reviewer and the
// listener of that quote.
type Orchestrator struct {
	Runner runner.Runner
	// Rounds caps the review rounds of a reviewed quote; zero takes DefaultRounds.
	Rounds int
	// Reviewer decides the rounds of a reviewed quote; nil takes the rules. A reviewer that fails a round is replaced
	// by the rules for that round.
	Reviewer Reviewer
	// Notify, which may be nil, hears each step the search is about to take (see the Msg constants).
	Notify func(contracts.Msg)
	// Reserve is time kept back from the context's deadline for what follows a thorough or reviewed search (drawing
	// the routes), so a search that uses all its time still leaves the quote time to be finished.
	Reserve time.Duration
	Log     *slog.Logger
}

// Outcome is the result of a quote's search.
type Outcome struct {
	// Result is what Planner.assemble consumes: the best candidate per preset over all the jobs. For a fast quote it
	// is the one job's result, untouched.
	Result *job.Result
	// Method says how the result was found: Effort, Trials, Rounds, Polished and Reviewer. The caller adds Proven
	// and RoadSource, which it knows better.
	Method contracts.Method
	// Truncated means the search did not run to its end: a time limit cut a job short, the budget ran out before a
	// round or the polish, or a job after the first failed. Such a quote is the best found, not a canonical one.
	Truncated bool
	// Err is the failure that cut the search short, nil when it ran on.
	Err error
	// Reviews are the decisions of the rounds, for logs.
	Reviews []Review
}

// Review is one decision of a review round.
type Review struct {
	Round       int
	By          string
	Satisfied   bool
	Experiments []job.Experiment
	// Note is the reviewer's own words. Diagnostics only: a model may have written it.
	Note string
	// Fallback says the AI reviewer failed this round and the rules decided.
	Fallback bool
}

func (o *Orchestrator) log() *slog.Logger {
	if o.Log != nil {
		return o.Log
	}
	return slog.Default()
}

func (o *Orchestrator) note(key string, params map[string]any) {
	if o.Notify != nil {
		o.Notify(contracts.Msg{Key: key, Params: params})
	}
}

// Run searches for base, a job without a Search, at the given effort; empty means fast. A failure of the first job
// is returned as it came (a *job.Error stays one). A later failure only ends the search: the best found so far is
// returned, Truncated, with a Method that says only what ran.
func (o *Orchestrator) Run(ctx context.Context, effort contracts.Effort, base *job.Job) (*Outcome, error) {
	// A fast quote is the one plain job and keeps the caller's deadline as it is.
	if deep := effort != "" && effort != contracts.EffortFast; deep && o.Reserve > 0 {
		if dl, ok := ctx.Deadline(); ok && time.Until(dl) > o.Reserve {
			var cancel context.CancelFunc
			ctx, cancel = context.WithDeadline(ctx, dl.Add(-o.Reserve))
			defer cancel()
		}
	}
	switch effort {
	case "", contracts.EffortFast:
		return o.fast(ctx, base)
	case contracts.EffortThorough:
		return o.thorough(ctx, base)
	case contracts.EffortReviewed:
		return o.reviewed(ctx, base)
	}
	return nil, fmt.Errorf("orchestrate: unknown effort %q", effort)
}

// fast is the plain job: no search, nothing merged.
func (o *Orchestrator) fast(ctx context.Context, base *job.Job) (*Outcome, error) {
	res, err := o.Runner.Run(ctx, base, nil)
	if err != nil {
		return nil, err
	}
	out := &Outcome{Result: res, Method: contracts.Method{Effort: contracts.EffortFast, Trials: trialsOf(res)}}
	out.Truncated = slices.ContainsFunc(res.Candidates, func(c job.Candidate) bool { return c.Truncated })
	return out, nil
}

func (o *Orchestrator) thorough(ctx context.Context, base *job.Job) (*Outcome, error) {
	s := &search{o: o, base: base}
	o.note(MsgExperiments, map[string]any{"n": len(DefaultExperiments())})
	first, err := s.exec(ctx, &job.Search{Experiments: DefaultExperiments()}, 2)
	if err != nil {
		return nil, err
	}

	// A thorough pass on the winner, then the finisher: the winning clusterings again with more effort, and a
	// warm start from the best answers.
	again := winners(first, job.EffortThorough)
	if len(again) > 0 {
		o.note(MsgExperiments, map[string]any{"n": len(again)})
	}
	o.note(MsgPolish, nil)
	s.next(ctx, &job.Search{Experiments: again, From: s.m.from(), Polish: job.EffortThorough}, 1)
	return s.outcome(contracts.EffortThorough), nil
}

func (o *Orchestrator) reviewed(ctx context.Context, base *job.Job) (*Outcome, error) {
	s := &search{o: o, base: base}
	rounds := o.Rounds
	if rounds <= 0 {
		rounds = DefaultRounds
	}
	o.note(MsgExperiments, map[string]any{"n": len(DefaultExperiments())})
	if _, err := s.exec(ctx, &job.Search{Experiments: DefaultExperiments()}, rounds+2); err != nil {
		return nil, err
	}
	s.history = append(s.history, s.snapshot())

	for round := 1; round <= rounds; round++ {
		if !s.affordable(ctx) {
			s.cut(errBudget)
			return s.outcome(contracts.EffortReviewed), nil
		}
		o.note(MsgRound, map[string]any{"round": round})
		d := s.review(ctx, round, rounds)
		if d.Satisfied {
			o.note(MsgSatisfied, nil)
			break
		}
		o.note(MsgMore, map[string]any{"n": len(d.Experiments)})
		if !s.next(ctx, &job.Search{Experiments: d.Experiments, From: s.m.from()}, rounds-round+2) {
			return s.outcome(contracts.EffortReviewed), nil
		}
		s.rounds++
		s.history = append(s.history, s.snapshot())
	}

	o.note(MsgPolish, nil)
	s.next(ctx, &job.Search{From: s.m.from(), Polish: job.EffortThorough}, 1)
	return s.outcome(contracts.EffortReviewed), nil
}

// search is the state of one quote's search between its jobs.
type search struct {
	o    *Orchestrator
	base *job.Job
	m    merger

	tried   []Attempt
	history []Snapshot
	// rounds counts the review rounds that ran a job.
	rounds    int
	decisions []Review
	// cutShort: the search did not run to its end, and err is why.
	cutShort bool
	err      error
}

// seconds is the budget of a job, with `left` jobs (this one included) still to run: what remains of the deadline
// shared among them, within the caller's own MaxSeconds if it set one. False when too little time is left to start.
func (s *search) seconds(ctx context.Context, left int) (int, bool) {
	secs := defaultJobSeconds
	if dl, ok := ctx.Deadline(); ok {
		remaining := time.Until(dl)
		if remaining < minJobTime {
			return 0, false
		}
		secs = int(remaining.Seconds()) / max(left, 1)
	}
	if cap := s.base.Limits.MaxSeconds; cap > 0 {
		secs = min(secs, cap)
	}
	return max(secs, 1), true
}

// affordable says there is time to begin another step.
func (s *search) affordable(ctx context.Context) bool {
	_, ok := s.seconds(ctx, 1)
	return ok && ctx.Err() == nil
}

// errBudget is a search that ran out of time before it could begin a step.
var errBudget = errors.New("the time budget of the search is used up")

// exec runs one job of the search: the base with a Search and a budget of its own. The failure of job 1 is the
// quote's (there is nothing to return without it); a later job is run through next.
func (s *search) exec(ctx context.Context, sr *job.Search, left int) (*job.Result, error) {
	secs, ok := s.seconds(ctx, left)
	if !ok {
		return nil, errBudget
	}
	j := *s.base
	j.Search = sr
	j.ID = fmt.Sprintf("%s.%d", s.base.ID, s.m.jobs+1)
	j.Limits.MaxSeconds = secs
	res, err := s.o.Runner.Run(ctx, &j, nil)
	if err != nil {
		return nil, err
	}
	s.m.add(res)
	s.record(sr, res)
	return res, nil
}

// next runs a later job. Its failure ends the search with what is already held: false.
func (s *search) next(ctx context.Context, sr *job.Search, left int) bool {
	if _, err := s.exec(ctx, sr, left); err != nil {
		s.cut(err)
		return false
	}
	return true
}

func (s *search) cut(err error) {
	s.cutShort = true
	s.err = errors.Join(s.err, err)
	s.o.log().Warn("search cut short, using the best found so far", "jobs", s.m.jobs, "err", err)
}

// record remembers what a job tried, as asked and as the solver says it did (k = 0 becomes the k that was chosen).
func (s *search) record(sr *job.Search, res *job.Result) {
	add := func(a Attempt) {
		if !slices.Contains(s.tried, a) {
			s.tried = append(s.tried, a)
		}
	}
	for _, e := range sr.Experiments {
		add(attemptOf(e))
	}
	for _, c := range res.Candidates {
		for _, t := range c.Trials {
			if t.Kind == job.TrialExperiment {
				add(attemptOf(job.Experiment{Algorithm: t.Algorithm, K: t.K, Effort: t.Effort}))
			}
		}
	}
}

// snapshot is the best Score of each preset right now.
func (s *search) snapshot() Snapshot {
	snap := make(Snapshot, 0, len(s.m.order))
	for _, p := range s.m.order {
		snap = append(snap, slices.Clone(s.m.best[p].cand.Score))
	}
	return snap
}

// brief is what the reviewer is told before a round.
func (s *search) brief(round, rounds int) Brief {
	b := Brief{
		Round: round, MaxRounds: rounds, Shipments: len(s.base.Shipments),
		Tried: slices.Clone(s.tried), History: slices.Clone(s.history),
	}
	for _, p := range s.m.order {
		e := s.m.best[p]
		f := PresetFacts{Preset: p, Valid: valid(e.cand), Proof: e.cand.Proof, Score: e.cand.Score, Trials: slices.Clone(e.trials)}
		if fa := e.cand.Facts; fa != nil {
			f.Overlap, f.Purity = fa.MaxOverlap, fa.ClusterPurity
			f.Hints, f.Unserved = fa.Hints, fa.Unserved
		}
		b.Presets = append(b.Presets, f)
	}
	return b
}

// review asks the reviewer about the search so far. When the AI cannot answer, the rules do, and the decision says
// so. Whatever the reviewer proposes goes through admit again, and a reviewer that wants more but has nothing
// admissible to ask for is satisfied.
func (s *search) review(ctx context.Context, round, rounds int) Decision {
	b := s.brief(round, rounds)
	rv := s.o.Reviewer
	if rv == nil {
		rv = RulesReviewer{}
	}

	if rv.Name() == contracts.ReviewerAI {
		s.o.note(MsgReviewAI, nil)
	} else {
		s.o.note(MsgReviewRules, nil)
	}
	fallback := false
	d, err := rv.Review(ctx, b)
	if err != nil {
		s.o.log().Warn("reviewer failed, the rules decide this round", "round", round, "reviewer", rv.Name(), "err", err)
		if rv.Name() == contracts.ReviewerAI {
			s.o.note(MsgReviewRules, nil)
		}
		d, _ = RulesReviewer{}.Review(ctx, b)
		d.By, fallback = contracts.ReviewerRules, true
	}
	if d.By == "" {
		d.By = rv.Name()
	}
	d.Experiments = admit(d.Experiments, s.tried, len(s.base.Shipments))
	if d.Satisfied {
		d.Experiments = nil
	} else if len(d.Experiments) == 0 {
		d.Satisfied = true
	}
	s.decisions = append(s.decisions, Review{
		Round: round, By: d.By, Satisfied: d.Satisfied, Experiments: d.Experiments, Note: d.Note, Fallback: fallback,
	})
	return d
}

// reviewer is who decided the search, for Method: the AI only if every decision was the model's, the rules if any was
// not. A search that asked nobody (the budget ran out first) reports who would have been asked.
func (s *search) reviewer() string {
	if len(s.decisions) == 0 {
		if s.o.Reviewer != nil {
			return s.o.Reviewer.Name()
		}
		return contracts.ReviewerRules
	}
	if slices.ContainsFunc(s.decisions, func(r Review) bool { return r.By != contracts.ReviewerAI }) {
		return contracts.ReviewerRules
	}
	return contracts.ReviewerAI
}

func (s *search) outcome(effort contracts.Effort) *Outcome {
	out := &Outcome{
		Result:    s.m.result(),
		Method:    contracts.Method{Effort: effort, Trials: s.m.stats.Trials, Rounds: s.rounds, Polished: s.m.stats.Polished},
		Truncated: s.cutShort || s.m.truncated(),
		Err:       s.err,
		Reviews:   s.decisions,
	}
	if effort == contracts.EffortReviewed {
		out.Method.Reviewer = s.reviewer()
	}
	return out
}
