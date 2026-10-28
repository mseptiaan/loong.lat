package orchestrate

import (
	"context"
	"slices"

	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
)

// MaxExperiments is the most a review round may ask for.
const MaxExperiments = 3

// Bounds of k, the number of clusters of an experiment. It never goes past 8 and a single cluster is no clustering.
const (
	minK = 2
	maxK = 8
)

// Reviewer audits what the search has found and decides what to try next.
type Reviewer interface {
	// Name is contracts.ReviewerAI or contracts.ReviewerRules: who is deciding.
	Name() string
	// Review judges the brief. An error means the reviewer could not decide (the network, an unusable answer); the
	// orchestrator then asks the rules instead, so a reviewer that is deterministic and offline never fails.
	Review(ctx context.Context, b Brief) (Decision, error)
}

// Decision is a reviewer's verdict on a brief.
type Decision struct {
	// Satisfied ends the review: what has been found is good enough.
	Satisfied bool
	// Experiments are what to try next, at most MaxExperiments, none of them a repeat. Empty when Satisfied.
	Experiments []job.Experiment
	// Note is the reviewer's own words for the logs. It is diagnostics only: it is never shown to a customer or
	// believed as a fact, because a model wrote it.
	Note string
	// By is the reviewer that decided: contracts.ReviewerAI or contracts.ReviewerRules.
	By string
}

// Brief is everything a reviewer is told about the search so far. Its strings come from the engine and its numbers
// are final; a reviewer treats them as data.
type Brief struct {
	// Round is the review round about to run, from 1, and MaxRounds the most there will be.
	Round, MaxRounds int
	// Shipments is the size of the plan, which bounds how many clusters make sense.
	Shipments int
	// Presets holds the best candidate of each preset, in display order.
	Presets []PresetFacts
	// Tried lists every experiment run in any job, as requested and as actually used (an experiment that asked for
	// k = 0 also appears with the k the silhouette chose), so that nothing is run twice.
	Tried []Attempt
	// History is the best Score of each preset after the first pass (History[0]) and after each round that ran.
	History []Snapshot
}

// PresetFacts is the audit of one preset's best candidate.
type PresetFacts struct {
	Preset string
	// Valid says the independent validator accepted the candidate; false also when no valid candidate was found.
	Valid bool
	Proof string
	// Score is the candidate's objective vector, lower is better.
	Score []int64
	// Overlap and Purity are the secondary diagnostics of the clustering; Purity is 0 when none was used.
	Overlap, Purity float64
	Unserved        []job.Unserved
	Hints           []string
	// Trials is everything tried for the preset in any job, in order.
	Trials []job.Trial
}

// Snapshot is the best Score of each preset at one point of the search, in the order of Brief.Presets.
type Snapshot [][]int64

// Attempt identifies an experiment: what it clusters with, into how many, and how hard it solves.
type Attempt struct {
	Algorithm string
	K         int
	Effort    string
}

// attemptOf normalizes an experiment: an empty effort is normal, and k means nothing without clusters to count.
func attemptOf(e job.Experiment) Attempt {
	a := Attempt{Algorithm: e.Algorithm, K: e.K, Effort: e.Effort}
	if a.Effort == "" {
		a.Effort = job.EffortNormal
	}
	if a.Algorithm == job.AlgNone || a.Algorithm == job.AlgDBSCAN {
		a.K = 0
	}
	return a
}

func (a Attempt) experiment() job.Experiment {
	return job.Experiment{Algorithm: a.Algorithm, K: a.K, Effort: a.Effort}
}

// admit keeps the experiments a round may run: known algorithm, k within bounds (and no more clusters than the plan
// has shipments, which the solver would silently skip), known effort, not run before and not asked twice, at most
// MaxExperiments. Whatever proposes experiments goes through it, so a reviewer that misbehaves cannot make the
// search repeat itself or run without bound. The experiments come back normalized: an explicit effort, no k where
// there are no clusters to count.
func admit(asked []job.Experiment, tried []Attempt, shipments int) []job.Experiment {
	seen := make(map[Attempt]bool, len(tried)+len(asked))
	for _, a := range tried {
		seen[a] = true
	}
	var out []job.Experiment
	for _, e := range asked {
		if len(out) == MaxExperiments {
			break
		}
		if !slices.Contains(job.Algorithms, e.Algorithm) || e.K < 0 || e.K > maxK || (shipments > 0 && e.K > shipments) {
			continue
		}
		if e.Effort != "" && !slices.Contains(efforts, e.Effort) {
			continue
		}
		a := attemptOf(e)
		if seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a.experiment())
	}
	return out
}

var efforts = []string{job.EffortQuick, job.EffortNormal, job.EffortThorough}

// relGap is how much worse score is than best, as a share of best at the first entry where they differ: the entries
// are ordered by importance, so the first one that differs decides which is better and by how much. A count-like entry
// (shipments left out, seconds late) has a floor of 1 as its base, so one unserved against none is a gap of 100
// percent, while a cost-like entry (thousandths of a rupiah) is measured against its own size. Zero when they are
// equal or score is the better one.
func relGap(score, best []int64) float64 {
	return share(score, best, best)
}

// gain is how much cur improves on prev, as a share of prev, measured at the first entry where they differ in the
// same way as relGap. Zero when they are equal or cur is not better.
func gain(prev, cur []int64) float64 {
	return share(prev, cur, prev)
}

// share is the difference at the first differing entry of hi and lo, over the entry of base (floored at 1); zero
// when hi is the smaller there, or they are equal.
func share(hi, lo, base []int64) float64 {
	for i := range min(len(hi), len(lo)) {
		if hi[i] == lo[i] {
			continue
		}
		if hi[i] < lo[i] {
			return 0
		}
		return float64(hi[i]-lo[i]) / float64(max(base[i], 1))
	}
	return 0
}

// Gains is how much each round improved the best objective values: for every round that ran, the largest relative
// improvement over any preset (see gain). Gains()[i] belongs to round i+1.
func (b Brief) Gains() []float64 {
	var out []float64
	for i := 1; i < len(b.History); i++ {
		var best float64
		prev, cur := b.History[i-1], b.History[i]
		for p := range min(len(prev), len(cur)) {
			best = max(best, gain(prev[p], cur[p]))
		}
		out = append(out, best)
	}
	return out
}

// stagnantGain is the share under which two rounds in a row are not worth a third: "less than 1%".
const stagnantGain = 0.01

// Stagnant reports that the last two rounds each improved the best objective values by less than one percent.
func (b Brief) Stagnant() bool {
	g := b.Gains()
	return len(g) >= 2 && g[len(g)-1] < stagnantGain && g[len(g)-2] < stagnantGain
}

// leader is the experiment trial that holds a preset's best result: the lowest Score among the valid ones, and among
// equals the one the engine marked Won, then the earliest. Trials without a Score rank behind those with one.
func leader(trials []job.Trial) (job.Trial, bool) {
	var best job.Trial
	found := false
	for _, t := range trials {
		if t.Kind != job.TrialExperiment || !t.Valid {
			continue
		}
		if !found || trialBefore(t, best) {
			best, found = t, true
		}
	}
	return best, found
}

func trialBefore(a, b job.Trial) bool {
	switch {
	case len(a.Score) > 0 && len(b.Score) == 0:
		return true
	case len(a.Score) == 0 && len(b.Score) > 0:
		return false
	}
	if c := slices.Compare(a.Score, b.Score); c != 0 {
		return c < 0
	}
	return a.Won && !b.Won
}
