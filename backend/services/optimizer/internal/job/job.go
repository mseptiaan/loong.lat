// Package job defines the wire contract between the optimizer service and the
// solver that runs in a sandbox: a Job goes in on stdin, newline-delimited
// Events come out on stdout. Both sides are versioned together; a solver
// rejects a job whose Version or Engine it does not know.
//
// A job carries coordinates, weights and ids only. It never holds names,
// phone numbers or addresses, so the sandbox sees nothing personal.
package job

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"time"
)

// Version is the wire version of Job and Result.
const Version = 1

// Preset names, as the frontend knows them.
const (
	Cheapest = "termurah"
	Shortest = "terpendek"
	Fastest  = "tercepat"
	Balanced = "seimbang"
)

// Presets lists every preset in display order.
var Presets = []string{Cheapest, Shortest, Fastest, Balanced}

// Point is a WGS84 coordinate.
type Point struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// Job is one optimization request.
type Job struct {
	Version int    `json:"version"`
	ID      string `json:"jobId"`
	// Engine, when set, is the build the caller expects to run the job.
	Engine   string    `json:"engineVersion,omitempty"`
	DepartAt time.Time `json:"departAt"`
	// Points are the distinct places of the job; every other field refers to them by index.
	Points []Point `json:"points"`
	// DistM is the road distance in meters from point i to point j at
	// DistM[i*len(Points)+j]. Travel times are derived from it and the speed of each fleet type.
	DistM     []int32     `json:"distM"`
	Fleet     []FleetType `json:"fleet"`
	Shipments []Shipment  `json:"shipments"`
	Presets   []string    `json:"presets"`
	Dwell     Dwell       `json:"dwellSec"`
	Limits    Limits      `json:"limits"`
	// Search tunes how hard the job looks; nil is one plain solve per preset, the quick quote.
	Search *Search `json:"search,omitempty"`
}

// FleetType is a kind of vehicle the job may use, in as many copies as Count.
type FleetType struct {
	Type     string  `json:"type"`
	Count    int     `json:"count"`
	SpeedKmh float64 `json:"speedKmh"`
	// Start is the point where a vehicle of this type begins its route.
	Start    int      `json:"start"`
	Capacity Capacity `json:"capacity"`
	Fare     Fare     `json:"fare"`
}

// Capacity is what a vehicle can carry.
type Capacity struct {
	WeightKg       float64 `json:"weightKg"`
	VolumeM3       float64 `json:"volumeM3"`
	MaxDimensionCm float64 `json:"maxDimensionCm"`
}

// Fare is the tariff the solver minimizes: base + per km + per minute + per
// extra stop (beyond the first pickup and drop) + a surcharge on the weight
// above a threshold. The approach to the first stop is never billed.
type Fare struct {
	Base             float64 `json:"base"`
	PerKm            float64 `json:"perKm"`
	PerMin           float64 `json:"perMin"`
	PerExtraStop     float64 `json:"perExtraStop"`
	SurchargeAboveKg float64 `json:"surchargeAboveKg"`
	SurchargePerKg   float64 `json:"surchargePerKg"`
}

// Shipment is one parcel group to move from a pickup to a drop.
type Shipment struct {
	ID             string     `json:"id"`
	Pickup         int        `json:"pickup"`
	Dropoff        int        `json:"dropoff"`
	WeightKg       float64    `json:"weightKg"`
	VolumeM3       float64    `json:"volumeM3"`
	MaxDimensionCm float64    `json:"maxDimensionCm"`
	Deadline       *time.Time `json:"deadline,omitempty"`
}

// Dwell is the time spent at each kind of stop.
type Dwell struct {
	Pickup int `json:"pickup"`
	Drop   int `json:"drop"`
}

// Limits bound the work of a job.
type Limits struct {
	// Iterations per preset, before scaling by problem size; zero means the engine default.
	Iterations int `json:"iterations,omitempty"`
	// MaxSeconds is a safety valve: a search cut short by it is flagged Truncated.
	MaxSeconds int `json:"maxSeconds,omitempty"`
	// ExactMaxOrders is the largest job that is also solved exhaustively, which proves the result optimal.
	ExactMaxOrders int `json:"exactMaxOrders,omitempty"`
	// ExactBudget caps the routes the exhaustive search may evaluate.
	ExactBudget int64 `json:"exactBudget,omitempty"`
}

// Clustering algorithms an Experiment may name; "none" solves without clustering.
const (
	AlgKMedoids      = "kmedoids"
	AlgKMeans        = "kmeans"
	AlgAgglomerative = "agglomerative"
	AlgDBSCAN        = "dbscan"
	AlgNone          = "none"
)

// Algorithms lists every algorithm.
var Algorithms = []string{AlgKMedoids, AlgKMeans, AlgAgglomerative, AlgDBSCAN, AlgNone}

// Effort levels of an Experiment or a polish: iterations per worker are 400, 1500 and 5000 (times max(1, n/25)).
// EffortNormal is exactly the plain solve of a job without a Search, so it is the baseline every search contains.
const (
	EffortQuick    = "quick"
	EffortNormal   = "normal"
	EffortThorough = "thorough"
)

// Search is a cluster-first search with several attempts per preset.
// Everything it does is deterministic: seeds follow the order of the experiments.
type Search struct {
	// Experiments are tried for every preset: each clusters the shipments and solves from that clustering.
	// The best valid result per preset wins. Empty means no experiments (only From and Polish act).
	Experiments []Experiment `json:"experiments,omitempty"`
	// From gives, per preset, routes found by an earlier job. They compete as an incumbent, so the result is
	// never worse than they were, and Polish refines them.
	From map[string][]Route `json:"from,omitempty"`
	// Polish, an effort level, continues the search from the best of each preset (warm start) once the experiments
	// are done. Empty skips it.
	Polish string `json:"polish,omitempty"`
}

// Experiment is one clustering and the solve that follows it.
type Experiment struct {
	// Algorithm is one of Algorithms.
	Algorithm string `json:"algorithm"`
	// K is the number of clusters; 0 lets the silhouette choose.
	K int `json:"k,omitempty"`
	// EpsMinutes is the neighbourhood of dbscan, in minutes of travel.
	EpsMinutes float64 `json:"epsMinutes,omitempty"`
	// Effort is the search effort of the solve; empty is EffortNormal.
	Effort string `json:"effort,omitempty"`
}

// Kinds of stop in a Result.
const (
	Pickup = "pickup"
	Drop   = "dropoff"
)

// Proof says how much is known about a candidate's quality.
const (
	ProofOptimal   = "optimal"   // an exhaustive search confirmed it
	ProofHeuristic = "heuristic" // validated and feasible, optimality unproven
)

// Result is the outcome of a Job.
type Result struct {
	Version    int         `json:"version"`
	Engine     string      `json:"engineVersion"`
	Candidates []Candidate `json:"candidates"`
	// Unserved lists the shipments that no vehicle of the fleet can carry.
	Unserved []Unserved `json:"unserved,omitempty"`
	Stats    Stats      `json:"stats"`
}

// Candidate is the best plan the solver found for one preset.
type Candidate struct {
	Preset  string  `json:"preset"`
	Routes  []Route `json:"routes"`
	Proof   string  `json:"proof"`
	Metrics Metrics `json:"metrics"`
	// Truncated means a time limit or cancellation cut the search short.
	Truncated bool `json:"truncated,omitempty"`
	// Score is the preset's objective vector, most important first (serving everything, then deadlines, then the
	// preset's own order; the balanced preset's blend is one entry). Candidates of one preset compare
	// lexicographically, lower is better, so a caller can merge the results of several jobs without the engine.
	Score []int64 `json:"score,omitempty"`
	// Trials lists what was tried for this preset (only for a job with a Search).
	Trials []Trial `json:"trials,omitempty"`
	// Facts audits the candidate for a reviewer (only for a job with a Search).
	Facts *Facts `json:"facts,omitempty"`
}

// Kinds of Trial.
const (
	TrialExperiment = "experiment" // a clustering and a solve
	TrialIncumbent  = "incumbent"  // routes handed in through Search.From
	TrialPolish     = "polish"     // a warm-start refinement
)

// Trial is one attempt at a preset.
type Trial struct {
	Kind      string `json:"kind"`
	Algorithm string `json:"algorithm,omitempty"`
	// K is the number of clusters actually used (the silhouette's choice when the experiment asked for 0).
	K          int     `json:"k,omitempty"`
	Effort     string  `json:"effort,omitempty"`
	Silhouette float64 `json:"silhouette,omitempty"`
	Valid      bool    `json:"valid"`
	Unserved   int     `json:"unserved,omitempty"`
	Iterations int     `json:"iterations,omitempty"`
	Metrics    Metrics `json:"metrics"`
	Score      []int64 `json:"score,omitempty"`
	// Won marks the trial whose routes became the candidate.
	Won bool `json:"won,omitempty"`
}

// Facts are what an independent audit found about a candidate: the independent validator's verdict and the
// diagnostics a reviewer weighs.
type Facts struct {
	Valid bool `json:"valid"`
	// MaxOverlap is the largest share of one vehicle's territory that another vehicle's also covers, 0 to 1.
	MaxOverlap float64 `json:"maxOverlap,omitempty"`
	// ClusterPurity is the share of orders that stayed in the cluster they were given, 0 to 1; 0 when no clustering was used.
	ClusterPurity float64 `json:"clusterPurity,omitempty"`
	// Hints are the engine's rule-based remarks.
	Hints []string `json:"hints,omitempty"`
	// Unserved lists shipments the candidate leaves out with the engine's cause.
	Unserved []Unserved `json:"unserved,omitempty"`
}

// Route is the ordered stops of one vehicle.
type Route struct {
	Type  string `json:"type"`
	Stops []Stop `json:"stops"`
}

// Stop is a visit to the pickup or the drop of a shipment.
type Stop struct {
	Shipment string `json:"shipment"`
	Kind     string `json:"kind"`
}

// Metrics are the solver's own totals for a candidate. The caller recomputes
// the user-facing numbers from the routes; these let it cross-check them.
type Metrics struct {
	CostMilli     int64 `json:"costMilli"`
	DistM         int64 `json:"distM"`
	SumArrivalSec int64 `json:"sumArrivalSec"`
	MakespanSec   int64 `json:"makespanSec"`
	LateSec       int64 `json:"lateSec"`
}

// Unserved explains a shipment that has no route.
type Unserved struct {
	Shipment string `json:"shipment"`
	// Cause is a stable engine code (no_vehicle_skill, too_heavy, ...).
	Cause string `json:"cause"`
}

// Stats describe the run.
type Stats struct {
	Shipments  int   `json:"shipments"`
	Vehicles   int   `json:"vehicles"`
	Iterations int   `json:"iterations"`
	ElapsedMs  int64 `json:"elapsedMs"`
	ExactRan   bool  `json:"exactRan,omitempty"`
	// Trials counts the solves run over all presets, polishes included.
	Trials   int  `json:"trials,omitempty"`
	Polished bool `json:"polished,omitempty"`
}

// Event types of the stdout stream.
const (
	EventProgress = "progress"
	EventResult   = "result"
	EventError    = "error"
)

// Event is one line of the solver's output.
type Event struct {
	Type     string    `json:"type"`
	Progress *Progress `json:"progress,omitempty"`
	Result   *Result   `json:"result,omitempty"`
	Error    *Error    `json:"error,omitempty"`
}

// Progress reports how many presets are finished.
type Progress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// Error codes of the stream.
const (
	CodeInvalid  = "invalid_job"
	CodeVersion  = "version_mismatch"
	CodeEngine   = "engine_mismatch"
	CodeInternal = "internal"
)

// Error is a failed job.
type Error struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Msg }

// Fail builds an *Error.
func Fail(code, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// maxPoints bounds the matrix a job may carry; beyond it the payload and the search stop being quote-sized.
const maxPoints = 120

// Validate checks that the job is internally consistent before any work is done.
func (j *Job) Validate() error {
	if j.Version != Version {
		return Fail(CodeVersion, "job version %d, solver speaks %d", j.Version, Version)
	}
	n := len(j.Points)
	switch {
	case n == 0 || n > maxPoints:
		return Fail(CodeInvalid, "%d points, need 1 to %d", n, maxPoints)
	case len(j.DistM) != n*n:
		return Fail(CodeInvalid, "distance matrix has %d entries for %d points", len(j.DistM), n)
	case len(j.Shipments) == 0:
		return Fail(CodeInvalid, "no shipments")
	case len(j.Fleet) == 0:
		return Fail(CodeInvalid, "no fleet")
	case len(j.Presets) == 0:
		return Fail(CodeInvalid, "no presets")
	case j.DepartAt.IsZero():
		return Fail(CodeInvalid, "no departure time")
	}
	in := func(i int) bool { return i >= 0 && i < n }
	seen := map[string]bool{}
	for _, s := range j.Shipments {
		switch {
		case s.ID == "" || seen[s.ID]:
			return Fail(CodeInvalid, "shipment id %q is empty or repeated", s.ID)
		case !in(s.Pickup) || !in(s.Dropoff):
			return Fail(CodeInvalid, "shipment %s refers to a point out of range", s.ID)
		case s.WeightKg < 0 || s.VolumeM3 < 0 || s.MaxDimensionCm < 0:
			return Fail(CodeInvalid, "shipment %s has a negative size", s.ID)
		}
		seen[s.ID] = true
	}
	types := map[string]bool{}
	for _, f := range j.Fleet {
		switch {
		case f.Type == "" || types[f.Type]:
			return Fail(CodeInvalid, "fleet type %q is empty or repeated", f.Type)
		case f.Count < 1 || f.SpeedKmh <= 0 || !in(f.Start):
			return Fail(CodeInvalid, "fleet type %s needs a positive count and speed and a start point", f.Type)
		}
		types[f.Type] = true
	}
	for _, p := range j.Presets {
		if p != Cheapest && p != Shortest && p != Fastest && p != Balanced {
			return Fail(CodeInvalid, "unknown preset %q", p)
		}
	}
	return j.Search.validate()
}

// validEffort accepts the effort levels and the empty string, which means EffortNormal.
func validEffort(e string) bool {
	return e == "" || e == EffortQuick || e == EffortNormal || e == EffortThorough
}

// validate rejects what no solver could ever run, so a typo fails the job loudly instead of silently
// dropping an experiment. A k beyond what the shipments allow is not an error: the solver skips or clamps it.
func (s *Search) validate() error {
	if s == nil {
		return nil
	}
	for i, e := range s.Experiments {
		switch {
		case !slices.Contains(Algorithms, e.Algorithm):
			return Fail(CodeInvalid, "experiment %d: unknown algorithm %q", i, e.Algorithm)
		case !validEffort(e.Effort):
			return Fail(CodeInvalid, "experiment %d: unknown effort %q", i, e.Effort)
		case e.K < 0 || e.EpsMinutes < 0:
			return Fail(CodeInvalid, "experiment %d: k and epsMinutes must not be negative", i)
		}
	}
	if !validEffort(s.Polish) {
		return Fail(CodeInvalid, "unknown polish effort %q", s.Polish)
	}
	return nil
}

// Encode renders the job as one JSON document.
func (j *Job) Encode() ([]byte, error) { return json.Marshal(j) }

// Decode parses a job and validates it.
func Decode(data []byte) (*Job, error) {
	var j Job
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, Fail(CodeInvalid, "decode job: %v", err)
	}
	return &j, j.Validate()
}

// ErrNoResult reports a stream that ended without a result or an error.
var ErrNoResult = errors.New("solver stream ended without a result")
