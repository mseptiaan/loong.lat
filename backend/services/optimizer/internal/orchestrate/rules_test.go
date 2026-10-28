package orchestrate

import (
	"context"
	"slices"
	"testing"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
)

// tidy is a preset audit that passes every rule: valid, nothing unserved, no overlap, no clustering penalty.
func tidy(preset string) PresetFacts {
	return PresetFacts{Preset: preset, Valid: true, Score: []int64{0, 100}, Trials: []job.Trial{
		exp("none", 0, "normal", false, 0, 100),
		exp("kmedoids", 3, "normal", true, 0, 100),
	}}
}

// triedAfterDefaults is what the orchestrator knows after the default experiments, which chose k = 3 everywhere.
func triedAfterDefaults() []Attempt {
	var out []Attempt
	for _, e := range DefaultExperiments() {
		out = append(out, attemptOf(e))
	}
	for _, alg := range []string{job.AlgKMedoids, job.AlgKMeans, job.AlgAgglomerative} {
		out = append(out, Attempt{alg, 3, job.EffortNormal})
	}
	return out
}

func review(t *testing.T, b Brief) Decision {
	t.Helper()
	d, err := RulesReviewer{}.Review(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	if d.By != contracts.ReviewerRules || (RulesReviewer{}).Name() != contracts.ReviewerRules {
		t.Errorf("by = %q", d.By)
	}
	if d.Note == "" {
		t.Error("a decision explains itself")
	}
	if d.Satisfied && len(d.Experiments) != 0 {
		t.Errorf("a satisfied reviewer asks for nothing: %+v", d.Experiments)
	}
	if len(d.Experiments) > MaxExperiments {
		t.Errorf("asked for %d experiments", len(d.Experiments))
	}
	return d
}

func briefOf(presets ...PresetFacts) Brief {
	return Brief{Round: 1, MaxRounds: 3, Shipments: 12, Presets: presets, Tried: triedAfterDefaults()}
}

func TestRulesAreSatisfiedByAValidTidyCandidate(t *testing.T) {
	d := review(t, briefOf(tidy(job.Cheapest), tidy(job.Shortest)))
	if !d.Satisfied {
		t.Errorf("not satisfied: %+v", d)
	}

	// No clustering was used: purity is reported as 0 and must not count against it.
	p := tidy(job.Cheapest)
	p.Purity, p.Overlap = 0, 0.1
	if d := review(t, briefOf(p)); !d.Satisfied {
		t.Errorf("a candidate without clustering is judged on its overlap alone: %+v", d)
	}
}

func TestRulesAskForExperimentsWhenACandidateIsNotValid(t *testing.T) {
	bad := tidy(job.Shortest)
	bad.Valid = false
	d := review(t, briefOf(tidy(job.Cheapest), bad))
	if d.Satisfied || len(d.Experiments) == 0 {
		t.Fatalf("want concrete experiments, got %+v", d)
	}
}

func TestUnservedCausesThatArePropertiesOfThePlanNeverTriggerExperiments(t *testing.T) {
	for _, cause := range planCauses {
		p := tidy(job.Cheapest)
		p.Unserved = []job.Unserved{{Shipment: "s1", Cause: cause}}
		if d := review(t, briefOf(p)); !d.Satisfied {
			t.Errorf("%s is a property of the plan, yet the reviewer wants more: %+v", cause, d)
		}
	}
	for _, cause := range []string{"not_placed", "schedule_conflict", "driver_time_full", "vehicle_full"} {
		p := tidy(job.Cheapest)
		p.Unserved = []job.Unserved{{Shipment: "s1", Cause: cause}}
		d := review(t, briefOf(p))
		if d.Satisfied || len(d.Experiments) == 0 {
			t.Errorf("%s can be fixed by another experiment, yet: %+v", cause, d)
		}
	}
	// One fixable shipment among plan-made ones is enough.
	p := tidy(job.Cheapest)
	p.Unserved = []job.Unserved{{Shipment: "s1", Cause: "too_heavy"}, {Shipment: "s2", Cause: "vehicle_full"}}
	if d := review(t, briefOf(p)); d.Satisfied {
		t.Errorf("a fixable cause among others must count: %+v", d)
	}
	// An unknown cause is not a known property of the plan, so it may be worth another try.
	p.Unserved = []job.Unserved{{Shipment: "s1", Cause: "something_new"}}
	if d := review(t, briefOf(p)); d.Satisfied {
		t.Errorf("an unknown cause must not be assumed to be the plan's: %+v", d)
	}
}

func TestOverlapAndPurityThresholds(t *testing.T) {
	for name, tc := range map[string]struct {
		overlap, purity float64
		satisfied       bool
	}{
		"overlap at the limit":      {0.15, 0.9, true},
		"overlap over the limit":    {0.16, 0.9, false},
		"purity at the limit":       {0.1, 0.85, true},
		"purity under the limit":    {0.1, 0.84, false},
		"both bad":                  {0.5, 0.5, false},
		"no clustering, no overlap": {0, 0, true},
	} {
		p := tidy(job.Cheapest)
		p.Overlap, p.Purity = tc.overlap, tc.purity
		if d := review(t, briefOf(p)); d.Satisfied != tc.satisfied {
			t.Errorf("%s: satisfied = %v, want %v (%+v)", name, d.Satisfied, tc.satisfied, d)
		}
	}
}

func TestSeveralClusteringsWithTheSameObjectiveExcuseOverlap(t *testing.T) {
	messy := func(trials ...job.Trial) PresetFacts {
		p := tidy(job.Cheapest)
		p.Overlap, p.Purity = 0.4, 0.6
		p.Trials = trials
		return p
	}
	same := func(alg string, k int) job.Trial { return exp(alg, k, "normal", false, 0, 100) }

	for name, tc := range map[string]struct {
		p         PresetFacts
		satisfied bool
	}{
		"two clusterings tie":             {messy(same("kmedoids", 3), same("kmeans", 3)), true},
		"the same algorithm at two ks":    {messy(same("kmedoids", 3), same("kmedoids", 4)), true},
		"one clustering and the baseline": {messy(same("none", 0), same("kmedoids", 3)), false},
		"one clustering alone":            {messy(same("kmedoids", 3)), false},
		"the same clustering twice":       {messy(same("kmedoids", 3), same("kmedoids", 3)), false},
		"a second clustering is worse":    {messy(same("kmedoids", 3), exp("kmeans", 3, "normal", false, 0, 120)), false},
		"a second clustering is invalid":  {messy(same("kmedoids", 3), func() job.Trial { t := same("kmeans", 3); t.Valid = false; return t }()), false},
		"ties in a polish do not count":   {messy(same("kmedoids", 3), job.Trial{Kind: job.TrialPolish, Valid: true, Score: []int64{0, 100}}), false},
	} {
		d := review(t, briefOf(tc.p))
		if d.Satisfied != tc.satisfied {
			t.Errorf("%s: satisfied = %v, want %v", name, d.Satisfied, tc.satisfied)
		}
	}

	// A tie excuses overlap only: an invalid candidate is not excused by one.
	p := messy(same("kmedoids", 3), same("kmeans", 3))
	p.Valid = false
	if d := review(t, briefOf(p)); d.Satisfied {
		t.Errorf("a tie must not excuse an invalid candidate: %+v", d)
	}
}

func TestTwoRoundsUnderOnePercentSatisfyTheReviewer(t *testing.T) {
	messy := tidy(job.Cheapest)
	messy.Overlap = 0.5
	snap := func(c int64) Snapshot { return Snapshot{{0, c}} }

	b := briefOf(messy)
	b.History = []Snapshot{snap(1000)}
	if d := review(t, b); d.Satisfied {
		t.Fatalf("no rounds yet, overlap is high: want more experiments, got %+v", d)
	}
	b.History = []Snapshot{snap(1000), snap(995)}
	if d := review(t, b); d.Satisfied {
		t.Errorf("one small round is not enough: %+v", d)
	}
	b.History = []Snapshot{snap(1000), snap(995), snap(992)}
	if d := review(t, b); !d.Satisfied {
		t.Errorf("two rounds under 1 percent: want satisfied, got %+v", d)
	}
	b.History = []Snapshot{snap(1000), snap(995), snap(900)}
	if d := review(t, b); d.Satisfied {
		t.Errorf("the last round gained 10 percent: %+v", d)
	}

	// Stagnation does not make an invalid candidate acceptable.
	invalid := messy
	invalid.Valid = false
	b = briefOf(invalid)
	b.History = []Snapshot{snap(1000), snap(1000), snap(1000)}
	if d := review(t, b); d.Satisfied {
		t.Errorf("stagnation must not satisfy the reviewer while a candidate is invalid: %+v", d)
	}
}

func TestProposalsAreKMedoidsAroundTheWinnerThenOtherAlgorithmsThenThorough(t *testing.T) {
	messy := tidy(job.Cheapest)
	messy.Overlap = 0.5
	messy.Trials = []job.Trial{
		exp("none", 0, "normal", false, 0, 120),
		exp("kmedoids", 4, "normal", true, 0, 100),
		exp("kmeans", 3, "normal", false, 0, 110),
		exp("agglomerative", 4, "normal", false, 0, 105),
	}
	// The default experiments asked for k = 0 and the silhouette chose 4, 3 and 4.
	var tried []Attempt
	for _, e := range DefaultExperiments() {
		tried = append(tried, attemptOf(e))
	}
	tried = append(tried, Attempt{job.AlgKMedoids, 4, job.EffortNormal}, Attempt{job.AlgKMeans, 3, job.EffortNormal}, Attempt{job.AlgAgglomerative, 4, job.EffortNormal})
	b := briefOf(messy)
	b.Tried = tried

	d := review(t, b)
	want := []job.Experiment{km(3), km(5), {Algorithm: job.AlgAgglomerative, K: 5, Effort: job.EffortNormal}}
	if !slices.Equal(d.Experiments, want) {
		t.Fatalf("round 1 asks %+v, want kmedoids 3, kmedoids 5, agglomerative 5", d.Experiments)
	}

	// Each round moves on to what has not been run yet, and the thorough pass on the winner comes once the
	// alternatives are used up. It ends, never repeating anything.
	seen := map[Attempt]bool{}
	for _, a := range tried {
		seen[a] = true
	}
	var order []Attempt
	for round := 1; ; round++ {
		if round > 10 {
			t.Fatal("the reviewer never ran out of experiments")
		}
		d := review(t, b)
		if d.Satisfied {
			break
		}
		for _, e := range d.Experiments {
			a := attemptOf(e)
			if seen[a] {
				t.Fatalf("round %d repeats %+v", round, a)
			}
			if e.Algorithm == job.AlgDBSCAN || e.K != 0 && (e.K < minK || e.K > maxK) {
				t.Fatalf("round %d asks for %+v", round, e)
			}
			seen[a] = true
			order = append(order, a)
			b.Tried = append(b.Tried, a)
		}
	}
	thorough := Attempt{job.AlgKMedoids, 4, job.EffortThorough}
	if !slices.Contains(order, thorough) {
		t.Errorf("the winning clustering was never re-run at thorough: %v", order)
	}
	if idx := slices.Index(order, thorough); idx < 3 {
		t.Errorf("the thorough pass came at position %d, before the alternatives were tried: %v", idx, order)
	}
}

func TestProposalsWithoutAClusteringWinnerPivotOnTheBestClustering(t *testing.T) {
	p := tidy(job.Cheapest)
	p.Overlap = 0.5
	p.Trials = []job.Trial{
		exp("none", 0, "normal", true, 0, 100),
		exp("kmedoids", 5, "normal", false, 0, 110),
		exp("kmeans", 2, "normal", false, 0, 130),
	}
	b := briefOf(p)
	d := review(t, b)
	if len(d.Experiments) < 2 || d.Experiments[0] != km(4) || d.Experiments[1] != km(6) {
		t.Errorf("asked %+v, want kmedoids around the best clustering's k=5", d.Experiments)
	}

	// With no clustering tried at all it explores around a default.
	p.Trials = []job.Trial{exp("none", 0, "normal", true, 0, 100)}
	d = review(t, briefOf(p))
	if len(d.Experiments) < 2 || d.Experiments[0] != km(2) || d.Experiments[1] != km(4) {
		t.Errorf("asked %+v, want kmedoids 2 and 4 around k=3", d.Experiments)
	}
}

func TestProposalsStayWithinThePlansSize(t *testing.T) {
	p := tidy(job.Cheapest)
	p.Overlap = 0.5
	p.Trials = []job.Trial{exp("kmedoids", 3, "normal", true, 0, 100)}
	b := briefOf(p)
	b.Shipments = 3 // at most 3 clusters
	// kmedoids 4 and the nearest untried k of the others (4) are out of bounds, so the next nearest, 2, is asked for.
	want := []job.Experiment{km(2), {Algorithm: job.AlgAgglomerative, K: 2, Effort: job.EffortNormal}, {Algorithm: job.AlgKMeans, K: 2, Effort: job.EffortNormal}}
	if got := review(t, b).Experiments; !slices.Equal(got, want) {
		t.Errorf("asked %+v, want %+v", got, want)
	}

	b.Shipments = 8
	p.Trials = []job.Trial{exp("kmedoids", 8, "normal", true, 0, 100)}
	b.Presets = []PresetFacts{p}
	for _, e := range review(t, b).Experiments {
		if e.K > maxK {
			t.Errorf("%+v is beyond the largest k", e)
		}
	}
}

func TestADBSCANWinnerIsNeverAskedForAgain(t *testing.T) {
	p := tidy(job.Cheapest)
	p.Overlap = 0.5
	p.Trials = []job.Trial{exp("dbscan", 0, "normal", true, 0, 100)}
	for _, e := range review(t, briefOf(p)).Experiments {
		if e.Algorithm == job.AlgDBSCAN {
			t.Errorf("asked for %+v: a trial does not record dbscan's neighbourhood", e)
		}
	}
}

func TestWithNothingLeftToTryTheReviewerIsSatisfied(t *testing.T) {
	p := tidy(job.Cheapest)
	p.Overlap = 0.5
	p.Trials = []job.Trial{exp("kmedoids", 3, "normal", true, 0, 100)}
	b := briefOf(p)
	b.Shipments = 3
	// Exhaust everything the rules could propose for 3 shipments.
	for k := minK; k <= 3; k++ {
		for _, alg := range []string{job.AlgKMedoids, job.AlgKMeans, job.AlgAgglomerative} {
			b.Tried = append(b.Tried, Attempt{alg, k, job.EffortNormal})
		}
	}
	b.Tried = append(b.Tried, Attempt{job.AlgKMedoids, 3, job.EffortThorough})
	if d := review(t, b); !d.Satisfied {
		t.Errorf("nothing left, yet %+v", d)
	}
}
