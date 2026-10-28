package orchestrate

import (
	"slices"
	"testing"

	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
)

func invalidCand(preset, tag string, score ...int64) job.Candidate {
	c := cand(preset, tag, score)
	c.Facts = &job.Facts{Valid: false}
	return c
}

func TestBetterComparesScoresLexicographicallyAndOnlyAmongValidCandidates(t *testing.T) {
	score := func(s ...int64) job.Candidate { return cand(job.Cheapest, "x", s) }
	noScore := cand(job.Cheapest, "x", nil)
	unaudited := job.Candidate{Preset: job.Cheapest, Score: []int64{0, 5}} // a plain job reports no audit

	for name, tc := range map[string]struct {
		c, than job.Candidate
		want    bool
	}{
		"lower cost wins":                       {score(0, 90), score(0, 100), true},
		"higher cost loses":                     {score(0, 110), score(0, 100), false},
		"a tie keeps what is held":              {score(0, 100), score(0, 100), false},
		"an earlier entry outweighs a later":    {score(1, 1), score(0, 1000), false},
		"serving more beats a cheaper plan":     {score(0, 1000), score(1, 1), true},
		"an invalid candidate never wins":       {invalidCand(job.Cheapest, "x", 0, 1), score(0, 100), false},
		"a valid one replaces an invalid one":   {score(9, 9), invalidCand(job.Cheapest, "x", 0, 1), true},
		"two invalid ones keep the first":       {invalidCand(job.Cheapest, "x", 0, 1), invalidCand(job.Cheapest, "x", 0, 2), false},
		"no score cannot beat a score":          {noScore, score(0, 100), false},
		"nothing to beat means no replacement":  {score(0, 1), noScore, false},
		"a candidate without an audit is valid": {unaudited, score(0, 6), true},
	} {
		if got := better(tc.c, tc.than); got != tc.want {
			t.Errorf("%s: better = %v, want %v", name, got, tc.want)
		}
	}
}

func TestMergeKeepsTheBestValidCandidatePerPresetAndEveryTrial(t *testing.T) {
	var m merger
	m.add(&job.Result{Version: 1, Engine: "e1", Stats: job.Stats{Shipments: 3, Vehicles: 2, Iterations: 10, ElapsedMs: 5, Trials: 4},
		Unserved: []job.Unserved{{Shipment: "s9", Cause: "too_heavy"}},
		Candidates: []job.Candidate{
			cand(job.Cheapest, "a1", []int64{0, 100}, exp("none", 0, "normal", true, 0, 100)),
			cand(job.Shortest, "b1", []int64{0, 100}, exp("none", 0, "normal", true, 0, 100)),
		}})
	m.add(&job.Result{Version: 1, Engine: "e2", Stats: job.Stats{Iterations: 20, ElapsedMs: 7, ExactRan: true, Polished: true, Trials: 3},
		Unserved: []job.Unserved{{Shipment: "s9", Cause: "too_heavy"}, {Shipment: "s8", Cause: "too_heavy"}},
		Candidates: []job.Candidate{
			cand(job.Cheapest, "a2", []int64{0, 90}, exp("kmeans", 2, "normal", true, 0, 90)),
			func() job.Candidate { // lower score but not valid: ignored
				c := invalidCand(job.Shortest, "b2", 0, 1)
				c.Trials = []job.Trial{exp("kmeans", 2, "normal", false, 0, 1)}
				return c
			}(),
		}})

	res := m.result()
	if res.Engine != "e1" || res.Version != 1 {
		t.Errorf("engine %q version %d: the first job's", res.Engine, res.Version)
	}
	if got := []string{tagOf(res.Candidates[0].Routes), tagOf(res.Candidates[1].Routes)}; !slices.Equal(got, []string{"a2", "b1"}) {
		t.Errorf("winners = %v, want a2 (cheaper) and b1 (the other is invalid)", got)
	}
	if want := (job.Stats{Shipments: 3, Vehicles: 2, Iterations: 30, ElapsedMs: 12, ExactRan: true, Polished: true, Trials: 7}); res.Stats != want {
		t.Errorf("stats = %+v, want %+v", res.Stats, want)
	}
	if len(res.Unserved) != 2 {
		t.Errorf("unserved = %+v, want the two distinct shipments", res.Unserved)
	}

	// Cheapest: both jobs' trials, and only the trial of the winning job still says Won.
	cheapest := res.Candidates[0].Trials
	if len(cheapest) != 2 || cheapest[0].Won || !cheapest[1].Won || cheapest[1].Algorithm != "kmeans" {
		t.Errorf("cheapest trials = %+v", cheapest)
	}
	// Shortest: job 1 still won, so its Won mark stays and job 2's invalid trial is listed.
	shortest := res.Candidates[1].Trials
	if len(shortest) != 2 || !shortest[0].Won || shortest[1].Won {
		t.Errorf("shortest trials = %+v", shortest)
	}
}

func TestMergeFromHandsOnlyValidRoutes(t *testing.T) {
	var m merger
	m.add(&job.Result{Candidates: []job.Candidate{invalidCand(job.Cheapest, "bad", 0, 1), cand(job.Shortest, "good", []int64{0, 5})}})
	from := m.from()
	if _, ok := from[job.Cheapest]; ok {
		t.Error("an invalid candidate is not an incumbent")
	}
	if tagOf(from[job.Shortest]) != "good" {
		t.Errorf("from = %+v", from)
	}
}

func TestTrialsAreCountedFromTheSolverOrElseFromWhatWasTried(t *testing.T) {
	withTrials := &job.Result{Candidates: []job.Candidate{{Trials: []job.Trial{
		{Kind: job.TrialExperiment}, {Kind: job.TrialIncumbent}, {Kind: job.TrialPolish},
	}}}}
	for name, tc := range map[string]struct {
		res  *job.Result
		want int
	}{
		"the solver says":        {&job.Result{Stats: job.Stats{Trials: 9}}, 9},
		"counted, no incumbents": {withTrials, 2},
		"one per preset":         {&job.Result{Candidates: make([]job.Candidate, 4)}, 4},
	} {
		if got := trialsOf(tc.res); got != tc.want {
			t.Errorf("%s: %d, want %d", name, got, tc.want)
		}
	}
}

func TestPolishedIsWhatTheSolverReports(t *testing.T) {
	if polishedBy(&job.Result{}) {
		t.Error("nothing was polished")
	}
	if !polishedBy(&job.Result{Stats: job.Stats{Polished: true}}) {
		t.Error("stats say polished")
	}
	if !polishedBy(&job.Result{Candidates: []job.Candidate{{Trials: []job.Trial{{Kind: job.TrialPolish}}}}}) {
		t.Error("a polish trial says polished")
	}
}

func TestAdmitKeepsOnlyWhatMayRun(t *testing.T) {
	tried := []Attempt{{job.AlgKMedoids, 3, job.EffortNormal}, {job.AlgNone, 0, job.EffortNormal}}
	asked := []job.Experiment{
		{Algorithm: job.AlgKMedoids, K: 3},                             // tried (empty effort is normal)
		{Algorithm: job.AlgNone, K: 5, Effort: job.EffortNormal},       // none has no k: the same as tried
		{Algorithm: "spectral", K: 3},                                  // unknown algorithm
		{Algorithm: job.AlgKMeans, K: 9},                               // k out of range
		{Algorithm: job.AlgKMeans, K: -1},                              // k out of range
		{Algorithm: job.AlgKMeans, K: 3, Effort: "extreme"},            // unknown effort
		{Algorithm: job.AlgKMedoids, K: 3, Effort: job.EffortThorough}, // new: another effort
		{Algorithm: job.AlgKMedoids, K: 3, Effort: job.EffortThorough}, // asked twice
		{Algorithm: job.AlgAgglomerative, K: 0},
		{Algorithm: job.AlgKMeans, K: 2},
		{Algorithm: job.AlgKMeans, K: 4}, // over the cap of three
	}
	got := admit(asked, tried, 0)
	want := []job.Experiment{
		{Algorithm: job.AlgKMedoids, K: 3, Effort: job.EffortThorough},
		{Algorithm: job.AlgAgglomerative, Effort: job.EffortNormal},
		{Algorithm: job.AlgKMeans, K: 2, Effort: job.EffortNormal},
	}
	if !slices.Equal(got, want) {
		t.Errorf("admit = %+v, want %+v", got, want)
	}
}

func TestRelativeGapMeasuresTheFirstEntryThatDiffers(t *testing.T) {
	for name, tc := range map[string]struct {
		score, best []int64
		want        float64
	}{
		"equal":                         {[]int64{0, 100}, []int64{0, 100}, 0},
		"worse in cost":                 {[]int64{0, 101}, []int64{0, 100}, 0.01},
		"better is no gap":              {[]int64{0, 90}, []int64{0, 100}, 0},
		"an earlier entry decides":      {[]int64{1, 50}, []int64{0, 100}, 1},
		"a worse later entry is moot":   {[]int64{0, 200, 5}, []int64{0, 100, 9}, 1},
		"count entries floor at one":    {[]int64{3}, []int64{2}, 0.5},
		"a count that went from 1 to 0": {[]int64{1}, []int64{0}, 1},
		"empty":                         {nil, nil, 0},
	} {
		if got := relGap(tc.score, tc.best); got != tc.want {
			t.Errorf("%s: relGap = %v, want %v", name, got, tc.want)
		}
	}
}

func TestStagnationNeedsTwoConsecutiveSmallRounds(t *testing.T) {
	snap := func(costs ...int64) Snapshot {
		var s Snapshot
		for _, c := range costs {
			s = append(s, []int64{0, c})
		}
		return s
	}
	for name, tc := range map[string]struct {
		history []Snapshot
		want    bool
	}{
		"no rounds yet":                    {[]Snapshot{snap(1000)}, false},
		"one small round":                  {[]Snapshot{snap(1000), snap(995)}, false},
		"two small rounds":                 {[]Snapshot{snap(1000), snap(995), snap(992)}, true},
		"two with no gain at all":          {[]Snapshot{snap(1000), snap(1000), snap(1000)}, true},
		"exactly one percent is not small": {[]Snapshot{snap(1000), snap(1000), snap(990)}, false},
		"a big round then a small":         {[]Snapshot{snap(1000), snap(900), snap(899)}, false},
		"small then big":                   {[]Snapshot{snap(1000), snap(999), snap(900)}, false},
		"big, small, small":                {[]Snapshot{snap(1000), snap(900), snap(899), snap(898)}, true},
		"one preset still moving":          {[]Snapshot{snap(1000, 1000), snap(1000, 990), snap(1000, 980)}, false},
	} {
		b := Brief{History: tc.history}
		if got := b.Stagnant(); got != tc.want {
			t.Errorf("%s: Stagnant = %v (gains %v), want %v", name, got, b.Gains(), tc.want)
		}
	}
}
