package orchestrate

import (
	"slices"

	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
)

// merger folds the results of several jobs into one: per preset the best candidate seen, compared by Score (the
// engine's own objective vector, so no engine is needed here), plus every trial of every job.
type merger struct {
	jobs     int
	version  int
	engine   string
	order    []string
	best     map[string]*entry
	unserved []job.Unserved
	stats    job.Stats
}

// entry is what is known about one preset.
type entry struct {
	cand   job.Candidate
	trials []job.Trial
	// winFrom:winTo are the trials of the job the best candidate came from.
	winFrom, winTo int
	truncated      bool
}

// valid says a candidate may stand as an answer. A candidate without an audit (a plain job reports none) is taken
// at its word: the solver validates what it returns.
func valid(c job.Candidate) bool { return c.Facts == nil || c.Facts.Valid }

// better says c should replace than. Only valid candidates compete, and only by Score, lower being better; a tie or
// a candidate without a Score keeps what is already held, so the earliest job (the plain solve) wins ties.
func better(c, than job.Candidate) bool {
	switch {
	case !valid(c):
		return false
	case !valid(than):
		return true
	case len(c.Score) == 0 || len(than.Score) == 0:
		return false
	}
	return slices.Compare(c.Score, than.Score) < 0
}

// add folds in the result of one job.
func (m *merger) add(res *job.Result) {
	if m.jobs == 0 {
		m.version, m.engine = res.Version, res.Engine
		m.best = map[string]*entry{}
		m.stats.Shipments, m.stats.Vehicles = res.Stats.Shipments, res.Stats.Vehicles
	}
	m.jobs++

	m.stats.Iterations += res.Stats.Iterations
	m.stats.ElapsedMs += res.Stats.ElapsedMs
	m.stats.ExactRan = m.stats.ExactRan || res.Stats.ExactRan
	m.stats.Trials += trialsOf(res)
	m.stats.Polished = m.stats.Polished || polishedBy(res)

	for _, u := range res.Unserved {
		if !slices.Contains(m.unserved, u) {
			m.unserved = append(m.unserved, u)
		}
	}
	for _, c := range res.Candidates {
		e := m.best[c.Preset]
		if e == nil {
			m.order = append(m.order, c.Preset)
			m.best[c.Preset] = &entry{cand: c, trials: slices.Clone(c.Trials), winTo: len(c.Trials), truncated: c.Truncated}
			continue
		}
		e.truncated = e.truncated || c.Truncated
		from := len(e.trials)
		e.trials = append(e.trials, c.Trials...)
		if better(c, e.cand) {
			e.cand, e.winFrom, e.winTo = c, from, len(e.trials)
		}
	}
}

// trialsOf counts the solves of a job. The solver says so itself; a job that does not (a plain one) solved each
// preset once.
func trialsOf(res *job.Result) int {
	if res.Stats.Trials > 0 {
		return res.Stats.Trials
	}
	n := 0
	for _, c := range res.Candidates {
		for _, t := range c.Trials {
			if t.Kind != job.TrialIncumbent {
				n++
			}
		}
	}
	if n > 0 {
		return n
	}
	return len(res.Candidates)
}

// polishedBy says a job refined its answers from where they stood.
func polishedBy(res *job.Result) bool {
	if res.Stats.Polished {
		return true
	}
	return slices.ContainsFunc(res.Candidates, func(c job.Candidate) bool {
		return slices.ContainsFunc(c.Trials, func(t job.Trial) bool { return t.Kind == job.TrialPolish })
	})
}

// truncated reports that any job's search was cut short by a time limit.
func (m *merger) truncated() bool {
	for _, e := range m.best {
		if e.truncated {
			return true
		}
	}
	return false
}

// from hands the best routes of each preset to the next job as its incumbents. A preset without a valid candidate
// has none worth refining.
func (m *merger) from() map[string][]job.Route {
	out := map[string][]job.Route{}
	for _, p := range m.order {
		if e := m.best[p]; valid(e.cand) && len(e.cand.Routes) > 0 {
			out[p] = e.cand.Routes
		}
	}
	return out
}

// result is the merged outcome in the shape of a job's own: the best candidate per preset, with every trial that was
// run for it (Won only where the winning job said so) and Truncated if any job's search was cut short.
func (m *merger) result() *job.Result {
	res := &job.Result{Version: m.version, Engine: m.engine, Unserved: slices.Clone(m.unserved), Stats: m.stats}
	for _, p := range m.order {
		e := m.best[p]
		c := e.cand
		c.Truncated = e.truncated
		c.Trials = slices.Clone(e.trials)
		for i := range c.Trials {
			if i < e.winFrom || i >= e.winTo {
				c.Trials[i].Won = false
			}
		}
		res.Candidates = append(res.Candidates, c)
	}
	return res
}

// winners are the experiments that won a preset in a result, each re-run at the given effort: distinct (algorithm, k)
// in preset order. The winner is the trial the engine marked Won; without one (an engine that does not mark) the
// best trial by Score. A winner that cannot be reproduced from its trial alone (dbscan, whose neighbourhood a trial
// does not record) is left out.
func winners(res *job.Result, effort string) []job.Experiment {
	var out []job.Experiment
	seen := map[Attempt]bool{}
	for _, c := range res.Candidates {
		i := slices.IndexFunc(c.Trials, func(t job.Trial) bool { return t.Won && t.Kind == job.TrialExperiment })
		var t job.Trial
		ok := i >= 0
		if ok {
			t = c.Trials[i]
		} else {
			t, ok = leader(c.Trials)
		}
		if !ok || t.Algorithm == job.AlgDBSCAN {
			continue
		}
		a := attemptOf(job.Experiment{Algorithm: t.Algorithm, K: t.K, Effort: effort})
		if !seen[a] {
			seen[a] = true
			out = append(out, a.experiment())
		}
	}
	return out
}
