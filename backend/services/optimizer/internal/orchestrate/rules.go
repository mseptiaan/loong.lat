package orchestrate

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
)

// Thresholds of the reviewer: territory overlap at most 0.15 and cluster purity at least 0.85.
const (
	maxOverlap = 0.15
	minPurity  = 0.85
)

// planCauses are the reasons a shipment is left unserved that belong to the plan itself: no experiment can serve
// such an order, so none is asked for because of it. Any other cause (not_placed, schedule_conflict,
// driver_time_full, vehicle_full) is a matter of how the search went and may be fixed by trying again.
var planCauses = []string{
	"no_vehicle_skill", "too_heavy", "window_in_break", "window_unreachable", "shift_too_short", "max_working_duration",
}

// fixable says an experiment may serve a shipment left unserved for this cause.
func fixable(cause string) bool { return !slices.Contains(planCauses, cause) }

// RulesReviewer is the reviewer written as rules: deterministic, offline, and the fallback of the AI reviewer.
// It audits the best candidate of every preset:
//
//   - A candidate that is not valid, or that leaves a shipment unserved for a cause an experiment could fix, asks for
//     more experiments.
//   - A valid one whose territories overlap by more than 0.15 or whose clusters were split (purity under 0.85) asks
//     for more too, unless several different clusterings already give the very same objective values.
//   - Satisfied when every candidate is valid and none of that applies, or when the last two rounds each improved
//     the best objective values by less than one percent, or when there is nothing left to try.
//
// More experiments means at most three that were never run before (in any job, at any k or effort): kmedoids with k
// one below and one above the winner's, agglomerative and kmeans with another k, then the winning clustering again at
// effort thorough.
type RulesReviewer struct{}

// Name implements Reviewer.
func (RulesReviewer) Name() string { return contracts.ReviewerRules }

// Review implements Reviewer. It never fails.
func (RulesReviewer) Review(_ context.Context, b Brief) (Decision, error) {
	d := Decision{By: contracts.ReviewerRules}
	var needy []PresetFacts
	var why []string
	allValid := true
	for _, p := range b.Presets {
		allValid = allValid && p.Valid
		if reason := audit(p); reason != "" {
			needy = append(needy, p)
			why = append(why, p.Preset+": "+reason)
		}
	}

	switch {
	case len(needy) == 0:
		d.Satisfied, d.Note = true, "every candidate is valid, nothing fixable is unserved and the territories are tidy"
	case allValid && b.Stagnant():
		d.Satisfied, d.Note = true, "the last two rounds each gained under 1 percent"
	default:
		d.Experiments = proposals(b, needy)
		d.Note = strings.Join(why, "; ")
		if len(d.Experiments) == 0 {
			d.Satisfied, d.Note = true, "nothing left to try; "+d.Note
		}
	}
	return d, nil
}

// audit is why a preset's best candidate wants more experiments, or "" when it does not.
func audit(p PresetFacts) string {
	if !p.Valid {
		return "no valid candidate"
	}
	for _, u := range p.Unserved {
		if fixable(u.Cause) {
			return "shipments unserved for a cause an experiment may fix (" + u.Cause + ")"
		}
	}
	if (p.Overlap > maxOverlap || (p.Purity > 0 && p.Purity < minPurity)) && !tied(p) {
		return fmt.Sprintf("territories overlap %.2f, purity %.2f", p.Overlap, p.Purity)
	}
	return ""
}

// tied says several different clusterings already give the very same objective values as the best: then a tidier map
// is not worth another round. Solving without clustering is no clustering, so it does not count.
func tied(p PresetFacts) bool {
	if len(p.Score) == 0 {
		return false
	}
	var same []Attempt
	for _, t := range p.Trials {
		if t.Kind != job.TrialExperiment || !t.Valid || t.Algorithm == job.AlgNone || slices.Compare(t.Score, p.Score) != 0 {
			continue
		}
		a := attemptOf(job.Experiment{Algorithm: t.Algorithm, K: t.K})
		if !slices.Contains(same, a) {
			same = append(same, a)
		}
	}
	return len(same) >= 2
}

// defaultPivot is the k to explore around when no clustering has been tried yet.
const defaultPivot = 3

// proposals are the experiments to ask for, from the first of the presets that need more.
func proposals(b Brief, needy []PresetFacts) []job.Experiment {
	kMax := maxK
	if b.Shipments > 0 {
		kMax = min(kMax, b.Shipments)
	}
	inBounds := func(k int) bool { return k >= minK && k <= kMax }

	var win job.Trial
	var haveWin bool
	pivot := defaultPivot
	for _, p := range needy {
		if t, ok := leader(p.Trials); ok {
			win, haveWin = t, true
			pivot = pivotOf(p.Trials, t)
			break
		}
	}

	var want []job.Experiment
	for _, delta := range []int{-1, +1} {
		if k := pivot + delta; inBounds(k) {
			want = append(want, job.Experiment{Algorithm: job.AlgKMedoids, K: k, Effort: job.EffortNormal})
		}
	}
	// Another k for the other algorithms: the nearest one they have not run.
	tried := map[Attempt]bool{}
	for _, a := range b.Tried {
		tried[a] = true
	}
	for _, alg := range []string{job.AlgAgglomerative, job.AlgKMeans} {
		for _, k := range []int{pivot, pivot + 1, pivot - 1} {
			if e := (job.Experiment{Algorithm: alg, K: k, Effort: job.EffortNormal}); inBounds(k) && !tried[attemptOf(e)] {
				want = append(want, e)
				break
			}
		}
	}
	// A trial does not record dbscan's neighbourhood, so a dbscan winner cannot be asked for again.
	if haveWin && win.Algorithm != job.AlgDBSCAN {
		want = append(want, job.Experiment{Algorithm: win.Algorithm, K: win.K, Effort: job.EffortThorough})
	}
	return admit(want, b.Tried, b.Shipments)
}

// pivotOf is the k to explore around: the winner's, or when the winner is no clustering the best clustering's.
func pivotOf(trials []job.Trial, win job.Trial) int {
	if win.Algorithm != job.AlgNone && win.K > 0 {
		return win.K
	}
	var best job.Trial
	found := false
	for _, t := range trials {
		if t.Kind == job.TrialExperiment && t.Valid && t.Algorithm != job.AlgNone && t.K > 0 && (!found || trialBefore(t, best)) {
			best, found = t, true
		}
	}
	if found {
		return best.K
	}
	return defaultPivot
}
