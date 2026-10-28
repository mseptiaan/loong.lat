// Package orchestratetest is a stand-in for a solver that honours Job.Search, for tests of the code above the
// solver (the planner, the HTTP handler) that need a searching runner without running a search: it solves the plain
// job and then reports, for every candidate, what a searching solver reports (Score, Facts, Trials, Stats).
package orchestratetest

import (
	"context"

	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/runner"
)

// Searching wraps a runner that solves plain jobs. A job with a Search is solved plainly, and its candidates then
// carry the vector Score {lateness, cost}, a valid audit, and one Trial per experiment (the first one Won, all
// with the candidate's own metrics) plus an incumbent trial for routes handed in and a polish trial when asked.
// Every experiment therefore ties, which is the honest outcome of a small plan.
func Searching(plain runner.Runner) runner.Runner {
	return runner.Func(func(ctx context.Context, j *job.Job, onProgress func(job.Progress)) (*job.Result, error) {
		if j.Search == nil {
			return plain.Run(ctx, j, onProgress)
		}
		p := *j
		p.Search = nil
		res, err := plain.Run(ctx, &p, onProgress)
		if err != nil {
			return nil, err
		}
		sr := j.Search
		trials := 0
		for i := range res.Candidates {
			c := &res.Candidates[i]
			c.Score = []int64{c.Metrics.LateSec, c.Metrics.CostMilli}
			c.Facts = &job.Facts{Valid: true}
			trial := func(kind string) job.Trial {
				return job.Trial{Kind: kind, Valid: true, Metrics: c.Metrics, Score: c.Score}
			}
			for k, e := range sr.Experiments {
				t := trial(job.TrialExperiment)
				t.Algorithm, t.K, t.Effort, t.Won = e.Algorithm, e.K, e.Effort, k == 0
				if t.K == 0 && e.Algorithm != job.AlgNone {
					t.K = 3
				}
				if t.Effort == "" {
					t.Effort = job.EffortNormal
				}
				c.Trials = append(c.Trials, t)
				trials++
			}
			if len(sr.From[c.Preset]) > 0 {
				c.Trials = append(c.Trials, trial(job.TrialIncumbent))
			}
			if sr.Polish != "" {
				t := trial(job.TrialPolish)
				t.Effort = sr.Polish
				c.Trials = append(c.Trials, t)
				trials++
			}
		}
		res.Stats.Trials = trials
		res.Stats.Polished = sr.Polish != ""
		return res, nil
	})
}
