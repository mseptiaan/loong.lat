package solve

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/engine/geo"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/model"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/solver"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/validate"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
)

// defaultSearch is what a thorough quote runs: the default experiments, then a thorough polish.
func defaultSearch() *job.Search {
	return &job.Search{
		Experiments: []job.Experiment{
			{Algorithm: job.AlgKMedoids}, {Algorithm: job.AlgKMeans}, {Algorithm: job.AlgAgglomerative}, {Algorithm: job.AlgNone},
		},
		Polish: job.EffortThorough,
	}
}

// synthJob is a job of n shipments over a synthetic South Jakarta: a depot, merchants to pick up from and
// customers to drop at, with a motorbike and a van fleet and a few heavy parcels and deadlines. A job carries
// at most 120 points, so at 120 shipments the places are used about once each.
func synthJob(n int, seed uint64) *job.Job {
	rng := rand.New(rand.NewPCG(seed, 99))
	hubs := max(2, n/6)
	drops := min(n, 119-hubs)
	pts := make([]job.Point, 0, 1+hubs+drops)
	pts = append(pts, job.Point{Lat: -6.2608, Lng: 106.8133}) // the depot, in Kemang
	for range hubs + drops {
		pts = append(pts, job.Point{Lat: -6.26 + (rng.Float64()-0.5)*0.16, Lng: 106.82 + (rng.Float64()-0.5)*0.16})
	}
	np := len(pts)
	dist := make([]int32, np*np)
	for a := range np {
		for b := range np {
			if a != b {
				la, lb := geo.LatLon{Lat: pts[a].Lat, Lon: pts[a].Lng}, geo.LatLon{Lat: pts[b].Lat, Lon: pts[b].Lng}
				dist[a*np+b] = int32(math.Round(geo.Haversine(la, lb) * 1.28))
			}
		}
	}
	shipments := make([]job.Shipment, n)
	for i := range shipments {
		s := job.Shipment{
			ID: fmt.Sprintf("s%03d", i), Pickup: 1 + rng.IntN(hubs), Dropoff: 1 + hubs + i%drops,
			WeightKg: 1 + rng.Float64()*11, VolumeM3: 0.01, MaxDimensionCm: 30,
		}
		switch r := rng.Float64(); {
		case r < 0.08:
			s.WeightKg = 25 + rng.Float64()*90 // beyond a motorbike
		case r < 0.12:
			s.MaxDimensionCm, s.VolumeM3 = 100, 0.08
		}
		if rng.Float64() < 0.2 {
			due := depart.Add(time.Duration(40+rng.IntN(140)) * time.Minute)
			s.Deadline = &due
		}
		shipments[i] = s
	}
	return &job.Job{
		Version: job.Version, ID: fmt.Sprintf("synth-%d-%d", n, seed), DepartAt: depart, Points: pts, DistM: dist,
		Fleet: []job.FleetType{
			{Type: "motor", Count: max(3, n/8), SpeedKmh: 28, Start: 0, Capacity: job.Capacity{WeightKg: 20, VolumeM3: 0.12, MaxDimensionCm: 55}, Fare: motorFare},
			{Type: "blindvan", Count: max(2, n/20), SpeedKmh: 22, Start: 0, Capacity: job.Capacity{WeightKg: 500, VolumeM3: 3.2, MaxDimensionCm: 190}, Fare: vanFare},
		},
		Shipments: shipments, Presets: job.Presets, Dwell: job.Dwell{Pickup: 240, Drop: 180},
	}
}

// lite caps the base iterations so a test of many searches stays quick; the search properties do not depend
// on the base, which is what Limits.Iterations is for.
func lite(j *job.Job) *job.Job {
	j.Limits.Iterations = 120
	return j
}

// compiled builds j and the problem of a preset, the way Run does.
func compiled(t *testing.T, j *job.Job, preset string) (*built, *model.Problem) {
	t.Helper()
	b, err := build(j)
	if err != nil {
		t.Fatal(err)
	}
	return b, b.problem(preset, b.baseline())
}

func byPreset(res *job.Result) map[string]job.Candidate {
	out := map[string]job.Candidate{}
	for _, c := range res.Candidates {
		out[c.Preset] = c
	}
	return out
}

// jsonOf renders a result without the wall-clock field, for comparing two runs.
func jsonOf(t *testing.T, res *job.Result) string {
	t.Helper()
	c := *res
	c.Stats.ElapsedMs = 0
	data, err := json.Marshal(&c)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRoutesBecomeASolutionOrAreRefused(t *testing.T) {
	a, b2 := parcel("a", 0, 1, 3), parcel("b", 2, 3, 4)
	j := testJob(a, b2)
	b, q := compiled(t, j, job.Cheapest)
	stop := func(id, kind string) job.Stop { return job.Stop{Shipment: id, Kind: kind} }
	pa, da, pb, db := stop("a", job.Pickup), stop("a", job.Drop), stop("b", job.Pickup), stop("b", job.Drop)

	for _, tc := range []struct {
		name     string
		routes   []job.Route
		refused  string // a fragment of the error, "" when the routes map
		invalid  bool   // they map, but the validator would not pass them
		wantLost int    // shipments left without a route
	}{
		{name: "one route each", routes: []job.Route{{Type: "motor", Stops: []job.Stop{pa, da}}, {Type: "motor", Stops: []job.Stop{pb, db}}}},
		{name: "both on one vehicle", routes: []job.Route{{Type: "motor", Stops: []job.Stop{pa, pb, da, db}}}},
		{name: "a shipment may be left out", routes: []job.Route{{Type: "motor", Stops: []job.Stop{pa, da}}}, wantLost: 1},
		{name: "an empty route is no route", routes: []job.Route{{Type: "motor"}, {Type: "motor", Stops: []job.Stop{pa, da, pb, db}}}},
		{name: "no routes at all", routes: nil, wantLost: 2},
		{name: "wrong fleet type", routes: []job.Route{{Type: "bicycle", Stops: []job.Stop{pa, da}}}, refused: `no free vehicle of type "bicycle"`},
		{name: "unknown shipment", routes: []job.Route{{Type: "motor", Stops: []job.Stop{pa, stop("zzz", job.Drop)}}}, refused: `unknown shipment "zzz"`},
		{name: "unknown stop kind", routes: []job.Route{{Type: "motor", Stops: []job.Stop{pa, stop("a", "teleport")}}}, refused: "unknown stop kind"},
		{name: "more routes than vehicles of the type", routes: []job.Route{
			{Type: "motor", Stops: []job.Stop{pa, da}}, {Type: "motor", Stops: []job.Stop{pb, db}}, {Type: "motor", Stops: []job.Stop{stop("a", job.Pickup)}},
		}, refused: "no free vehicle"},
		{name: "a stop twice", routes: []job.Route{{Type: "motor", Stops: []job.Stop{pa, pa, da}}}, refused: "twice"},
		{name: "a shipment with one stop only", routes: []job.Route{{Type: "motor", Stops: []job.Stop{pa}}}, refused: "only some of its stops"},
		{name: "drop before pickup", routes: []job.Route{{Type: "motor", Stops: []job.Stop{da, pa}}}, refused: "not feasible"},
		{name: "a shipment split over two vehicles", routes: []job.Route{{Type: "motor", Stops: []job.Stop{pa}}, {Type: "motor", Stops: []job.Stop{da, pb, db}}}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sol, err := b.solution(q, tc.routes)
			if tc.refused != "" {
				if err == nil || !strings.Contains(err.Error(), tc.refused) {
					t.Fatalf("err = %v, want one containing %q", err, tc.refused)
				}
				return
			}
			if err != nil {
				if tc.invalid {
					return // refused earlier than the validator, which is as good
				}
				t.Fatal(err)
			}
			rep := validate.Check(q, sol)
			if rep.Valid == tc.invalid {
				t.Fatalf("validator verdict valid=%v (%v), want valid=%v", rep.Valid, rep.Violations, !tc.invalid)
			}
			if !tc.invalid && len(sol.Unassigned) != tc.wantLost {
				t.Errorf("%d shipments without a route, want %d", len(sol.Unassigned), tc.wantLost)
			}
		})
	}
}

// What a solve reports must map back to the very solution it came from: the metrics and the score
// of the routes, re-evaluated, are the candidate's own.
func TestRoutesOfASolveRoundTrip(t *testing.T) {
	for seed := range uint64(3) {
		j := lite(synthJob(8, seed))
		res := run(t, j)
		for _, c := range res.Candidates {
			b, q := compiled(t, j, c.Preset)
			sol, err := b.solution(q, c.Routes)
			if err != nil {
				t.Fatalf("seed %d %s: %v", seed, c.Preset, err)
			}
			if rep := validate.Check(q, sol); !rep.Valid {
				t.Fatalf("seed %d %s: %v", seed, c.Preset, rep.Violations)
			}
			if got := metricsOf(&sol.M); got != c.Metrics {
				t.Errorf("seed %d %s: re-evaluated metrics %+v, candidate says %+v", seed, c.Preset, got, c.Metrics)
			}
			if got := score(q, &sol.M); !slices.Equal(got, c.Score) {
				t.Errorf("seed %d %s: re-evaluated score %v, candidate says %v", seed, c.Preset, got, c.Score)
			}
			if again := b.routes(sol); !jsonEqual(t, again, c.Routes) {
				t.Errorf("seed %d %s: routes changed in the round trip:\n%+v\n%+v", seed, c.Preset, again, c.Routes)
			}
		}
	}
}

func jsonEqual(t *testing.T, a, b any) bool {
	t.Helper()
	x, err1 := json.Marshal(a)
	y, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	return string(x) == string(y)
}

func TestScoreOrderIsTheEnginesOrder(t *testing.T) {
	j := lite(synthJob(7, 5))
	for _, preset := range job.Presets {
		_, q := compiled(t, j, preset)
		// Solutions of very different quality: a different seed, effort and clustering each.
		var sols []*model.Solution
		for seed := uint64(1); seed <= 3; seed++ {
			sol, _ := solver.Solve(q, nil, solver.Options{Seed: seed, Iterations: int(seed) * 20, Workers: 1})
			sols = append(sols, sol)
		}
		if got, want := len(score(q, &sols[0].M)), len(engineObjectives(preset)); got != want {
			t.Fatalf("%s: score has %d entries, the preset ranks %d objectives", preset, got, want)
		}
		for x := range sols {
			for y := range sols {
				got := slices.Compare(score(q, &sols[x].M), score(q, &sols[y].M))
				if want := q.Compare(&sols[x].M, &sols[y].M); got != want {
					t.Errorf("%s: score order %d, Problem.Compare %d for solutions %d and %d", preset, got, want, x, y)
				}
			}
		}
	}
}

func TestScoreHeadsWithServingThenDeadlinesThenThePresetsOwnOrder(t *testing.T) {
	j := lite(synthJob(8, 2))
	want := map[string]int{job.Cheapest: 5, job.Shortest: 5, job.Fastest: 5, job.Balanced: 3}
	for _, c := range run(t, j).Candidates {
		if len(c.Score) != want[c.Preset] {
			t.Errorf("%s: score %v has %d entries, want %d", c.Preset, c.Score, len(c.Score), want[c.Preset])
		}
		if c.Score[0] != 0 || c.Score[1] != c.Metrics.LateSec {
			t.Errorf("%s: score %v should start with 0 unserved stops and the lateness %d", c.Preset, c.Score, c.Metrics.LateSec)
		}
		switch c.Preset {
		case job.Cheapest:
			if c.Score[2] != c.Metrics.CostMilli || c.Score[3] != c.Metrics.DistM {
				t.Errorf("cheapest: score %v does not follow cost, then distance", c.Score)
			}
		case job.Shortest:
			if c.Score[2] != c.Metrics.DistM || c.Score[3] != c.Metrics.CostMilli {
				t.Errorf("shortest: score %v does not follow distance, then cost", c.Score)
			}
		case job.Fastest:
			if c.Score[2] != c.Metrics.MakespanSec || c.Score[3] != c.Metrics.SumArrivalSec {
				t.Errorf("fastest: score %v does not follow the longest route, then the arrivals", c.Score)
			}
		case job.Balanced:
			if c.Score[2] <= 0 {
				t.Errorf("balanced: the blend entry of %v must be positive", c.Score)
			}
		}
	}
}

func TestSearchIsNeverWorseThanThePlainSolve(t *testing.T) {
	jobs := 12
	if raceEnabled {
		jobs = 4
	}
	rng := rand.New(rand.NewPCG(7, 7))
	for i := range jobs {
		n := 8 + rng.IntN(5) // beyond the exact solver, which would make both optimal
		j := synthJob(n, uint64(100+i))
		// Few iterations keep the test short and make the trials differ from one another, which is what gives
		// "the best of them wins" something to prove; with many, small jobs reach the same plan from anywhere.
		j.Limits.Iterations = 15
		// One preset per job; rotating them covers all four across the dozen.
		j.Presets = []string{job.Presets[i%4]}
		plain := byPreset(run(t, j))
		j.Search = defaultSearch()
		res := run(t, j)
		for _, c := range res.Candidates {
			checkRoutes(t, j, c)
			p := plain[c.Preset]
			if slices.Compare(c.Score, p.Score) > 0 {
				t.Errorf("job %d (%d shipments) %s: the search found %v, the plain solve %v", i, n, c.Preset, c.Score, p.Score)
			}
			if c.Facts == nil || !c.Facts.Valid || len(c.Trials) == 0 {
				t.Errorf("job %d %s: a searched candidate needs its facts and trials", i, c.Preset)
				continue
			}
			// The winner is the best of what was tried, not merely no worse than the baseline.
			best := slices.MinFunc(c.Trials, func(a, b job.Trial) int { return slices.Compare(a.Score, b.Score) })
			if slices.Compare(best.Score, c.Score) != 0 {
				t.Errorf("job %d %s: the best trial scores %v but the candidate %v", i, c.Preset, best.Score, c.Score)
			}
		}
	}
}

// The baseline experiment is the plain solve whatever else runs and wherever it is listed.
func TestTheBaselineExperimentIsThePlainSolveWhereverItIsListed(t *testing.T) {
	base := job.Experiment{Algorithm: job.AlgNone, Effort: job.EffortNormal}
	others := []job.Experiment{{Algorithm: job.AlgKMeans, K: 3, Effort: job.EffortQuick}, {Algorithm: job.AlgKMedoids}}
	for name, tc := range map[string]struct {
		exps       []job.Experiment
		iterations int // 0 is the engine's default, which only the smallest job can afford
		shipments  int
		seed       uint64 // of the job; small jobs usually reach the same plan from any solver seed, this one does not
	}{
		"middle":               {[]job.Experiment{others[0], base, others[1]}, 100, 9, 4},
		"last":                 {[]job.Experiment{others[0], others[1], base}, 100, 9, 4},
		"alone at the default": {[]job.Experiment{base}, 0, 6, 3},
	} {
		j := synthJob(tc.shipments, tc.seed)
		j.Limits.Iterations = tc.iterations
		j.Presets = []string{job.Cheapest}
		plain := byPreset(run(t, j))
		j.Search = &job.Search{Experiments: tc.exps}
		for _, c := range run(t, j).Candidates {
			var trial *job.Trial
			for k := range c.Trials {
				if tr := &c.Trials[k]; tr.Algorithm == job.AlgNone && tr.Effort == job.EffortNormal {
					trial = tr
				}
			}
			p := plain[c.Preset]
			if trial == nil {
				t.Fatalf("%s %s: the baseline trial is missing from %+v", name, c.Preset, c.Trials)
			}
			if !slices.Equal(trial.Score, p.Score) || trial.Metrics != p.Metrics {
				t.Errorf("%s %s: the baseline trial found %v, the plain solve %v", name, c.Preset, trial.Score, p.Score)
			}
			if slices.Compare(c.Score, p.Score) > 0 {
				t.Errorf("%s %s: the search is worse than the plain solve", name, c.Preset)
			}
		}
	}
}

// A job small enough for the exact solver is still proven optimal after a search, and the proof lands on the
// very plan the quick quote gives: the thorough tiers agree with the fast one where nothing can be gained.
func TestTheExactStepStillRunsAfterASearch(t *testing.T) {
	j := lite(synthJob(4, 9))
	plain := run(t, j)
	j.Search = defaultSearch()
	res := run(t, j)
	if !res.Stats.ExactRan {
		t.Fatal("the exact solver must still run after the search")
	}
	p := byPreset(plain)
	for _, c := range res.Candidates {
		if c.Proof != job.ProofOptimal || p[c.Preset].Proof != job.ProofOptimal {
			t.Errorf("%s: proof %q after the search, %q without", c.Preset, c.Proof, p[c.Preset].Proof)
		}
		if !slices.Equal(c.Score, p[c.Preset].Score) || !jsonEqual(t, c.Routes, p[c.Preset].Routes) {
			t.Errorf("%s: the proven plan differs from the quick quote's:\n%+v\n%+v", c.Preset, c.Routes, p[c.Preset].Routes)
		}
		wins := 0
		for _, tr := range c.Trials {
			if tr.Won {
				wins++
			}
		}
		if wins != 1 || !c.Facts.Valid {
			t.Errorf("%s: want one winning trial and valid facts, got %d and %+v", c.Preset, wins, c.Facts)
		}
	}
}

func TestSearchIsDeterministic(t *testing.T) {
	j := lite(synthJob(7, 11))
	j.Search = defaultSearch()
	all := run(t, j)
	if again := run(t, j); jsonOf(t, all) != jsonOf(t, again) {
		t.Errorf("the same job gave different results:\n%s\n%s", jsonOf(t, all), jsonOf(t, again))
	}

	// The presets run side by side and share their clusterings, whichever of them gets there first; what one
	// finds must not depend on which others are being solved with it. Seeds follow the experiments' places
	// in the list, never the presets' or the clock.
	j.Presets = []string{job.Balanced, job.Cheapest}
	some := byPreset(run(t, j))
	for _, c := range all.Candidates {
		if s, ok := some[c.Preset]; ok && !jsonEqual(t, s, c) {
			t.Errorf("%s: solved with another set of presets it found something else:\n%+v\n%+v", c.Preset, s, c)
		}
	}
}

func TestAnIncumbentNeverRegresses(t *testing.T) {
	j := lite(synthJob(7, 21))
	j.Presets = []string{job.Cheapest, job.Fastest}
	thorough := *j
	thorough.Search = defaultSearch()
	strong := byPreset(run(t, &thorough))

	from := map[string][]job.Route{}
	for p, c := range strong {
		from[p] = c.Routes
	}
	// Against a thorough incumbent, every kind of weaker work: nothing at all, a quick experiment, a cheap polish.
	for name, sch := range map[string]*job.Search{
		"incumbent only":   {From: from},
		"quick experiment": {From: from, Experiments: []job.Experiment{{Algorithm: job.AlgKMeans, K: 2, Effort: job.EffortQuick}}},
		"quick polish":     {From: from, Polish: job.EffortQuick},
		"only the baseline": {From: from, Experiments: []job.Experiment{
			{Algorithm: job.AlgNone, Effort: job.EffortNormal}, {Algorithm: job.AlgDBSCAN, EpsMinutes: 6, Effort: job.EffortQuick},
		}},
	} {
		j2 := *j
		j2.Search = sch
		res := run(t, &j2)
		for _, c := range res.Candidates {
			checkRoutes(t, &j2, c)
			if slices.Compare(c.Score, strong[c.Preset].Score) > 0 {
				t.Errorf("%s/%s: result %v is worse than the incumbent %v", name, c.Preset, c.Score, strong[c.Preset].Score)
			}
			if c.Trials[0].Kind != job.TrialIncumbent || !c.Trials[0].Valid {
				t.Errorf("%s/%s: the first trial must be the valid incumbent, got %+v", name, c.Preset, c.Trials[0])
			}
		}
	}

	// A weak incumbent (one route per fleet type, shipments in the order of the job, no thought given to
	// geography) is beaten by even a quick solve.
	weak := map[string][]job.Route{}
	for _, preset := range j.Presets {
		routes := map[string]*job.Route{}
		for _, s := range j.Shipments {
			typ := "motor"
			if s.WeightKg > 20 || s.MaxDimensionCm > 55 {
				typ = "blindvan"
			}
			if routes[typ] == nil {
				routes[typ] = &job.Route{Type: typ}
			}
			routes[typ].Stops = append(routes[typ].Stops, job.Stop{Shipment: s.ID, Kind: job.Pickup}, job.Stop{Shipment: s.ID, Kind: job.Drop})
		}
		for _, typ := range []string{"motor", "blindvan"} {
			if routes[typ] != nil {
				weak[preset] = append(weak[preset], *routes[typ])
			}
		}
	}
	j3 := *j
	j3.Search = &job.Search{From: weak, Experiments: []job.Experiment{{Algorithm: job.AlgNone, Effort: job.EffortQuick}}}
	for _, c := range run(t, &j3).Candidates {
		if c.Trials[0].Kind != job.TrialIncumbent || !c.Trials[0].Valid {
			t.Fatalf("%s: the weak incumbent should be valid, got %+v", c.Preset, c.Trials[0])
		}
		if slices.Compare(c.Score, c.Trials[0].Score) >= 0 || c.Trials[0].Won {
			t.Errorf("%s: a quick solve should beat a plan nobody optimized: %v vs %v", c.Preset, c.Score, c.Trials[0].Score)
		}
	}
}

func TestAnIncumbentThatDoesNotMapIsIgnoredButRecorded(t *testing.T) {
	j := lite(synthJob(8, 4))
	bogus := []job.Route{{Type: "hovercraft", Stops: []job.Stop{{Shipment: "s000", Kind: job.Pickup}}}}
	j.Search = &job.Search{From: map[string][]job.Route{job.Cheapest: bogus}, Experiments: []job.Experiment{{Algorithm: job.AlgNone}}}
	j.Presets = []string{job.Cheapest}
	c := run(t, j).Candidates[0]
	checkRoutes(t, j, c)
	if len(c.Trials) != 2 || c.Trials[0].Kind != job.TrialIncumbent || c.Trials[0].Valid || c.Trials[0].Won {
		t.Fatalf("trials = %+v, want a rejected incumbent first and then the experiment", c.Trials)
	}
	if !c.Trials[1].Won {
		t.Errorf("the experiment must win: %+v", c.Trials)
	}
}

func TestTrialsRecordEveryAttempt(t *testing.T) {
	j := lite(synthJob(10, 8))
	j.Presets = []string{job.Cheapest, job.Fastest}
	j.Search = &job.Search{
		Experiments: []job.Experiment{
			{Algorithm: job.AlgKMedoids}, {Algorithm: job.AlgKMeans, K: 3, Effort: job.EffortQuick}, {Algorithm: job.AlgNone},
			{Algorithm: job.AlgKMeans, K: 3, Effort: job.EffortQuick}, // a repeat: run once
			{Algorithm: job.AlgKMeans, K: 99},                         // more clusters than shipments: skipped
		},
		Polish: job.EffortNormal,
	}
	res := run(t, j)
	if want := len(j.Presets) * (3 + 1); res.Stats.Trials != want || !res.Stats.Polished {
		t.Errorf("stats = %+v, want %d trials and a polish", res.Stats, want)
	}
	iterations := 0
	for _, c := range res.Candidates {
		var kinds []string
		wins := 0
		for _, tr := range c.Trials {
			kinds = append(kinds, tr.Kind+":"+tr.Algorithm+":"+tr.Effort)
			iterations += tr.Iterations
			if tr.Won {
				wins++
			}
			if !tr.Valid || len(tr.Score) != len(c.Score) || tr.Iterations <= 0 {
				t.Errorf("%s: incomplete trial %+v", c.Preset, tr)
			}
		}
		// The baseline runs first whatever its place in the list, then the others in list order, then the polish.
		if want := []string{"experiment:none:normal", "experiment:kmedoids:normal", "experiment:kmeans:quick", "polish::normal"}; !slices.Equal(kinds, want) {
			t.Errorf("%s: trials %v, want %v", c.Preset, kinds, want)
		}
		if wins != 1 {
			t.Errorf("%s: %d trials won, want exactly one", c.Preset, wins)
		}
		k := c.Trials[1].K
		if k < 2 || k > maxClusters || c.Trials[1].Silhouette == 0 {
			t.Errorf("%s: the silhouette should have chosen a k in 2..%d with a silhouette, got k=%d s=%v", c.Preset, maxClusters, k, c.Trials[1].Silhouette)
		}
		if c.Trials[2].K != 3 || c.Trials[0].K != 0 {
			t.Errorf("%s: k = %d and %d, want 3 for kmeans and none for the baseline", c.Preset, c.Trials[2].K, c.Trials[0].K)
		}
		best := slices.MinFunc(c.Trials, func(a, b job.Trial) int { return slices.Compare(a.Score, b.Score) })
		if slices.Compare(best.Score, c.Score) != 0 {
			t.Errorf("%s: the best trial scores %v but the candidate %v", c.Preset, best.Score, c.Score)
		}
	}
	if res.Stats.Iterations != iterations {
		t.Errorf("stats count %d iterations, the trials %d", res.Stats.Iterations, iterations)
	}
}

func TestFactsLeaveOutTheRemarkAboutIdleVehicles(t *testing.T) {
	// The fleet is a pool with a virtual vehicle per shipment, so most of it is idle by design.
	j := lite(synthJob(10, 13))
	j.Presets = []string{job.Cheapest}
	j.Search = &job.Search{Experiments: []job.Experiment{{Algorithm: job.AlgNone}}}
	c := run(t, j).Candidates[0]
	if len(c.Routes) >= len(j.Shipments) {
		t.Fatalf("%d routes for %d shipments: the premise (idle vehicles) does not hold", len(c.Routes), len(j.Shipments))
	}
	for _, h := range c.Facts.Hints {
		if strings.Contains(h, " vehicles are used") {
			t.Errorf("hint %q should have been dropped", h)
		}
	}
}

func TestRelevantHintsKeepsTheOthers(t *testing.T) {
	in := []string{"Only 2 of 10 vehicles are used (idle: a, b).", "1 order is unserved.", "Only the first stop is early."}
	got := relevantHints(in)
	want := []string{"1 order is unserved.", "Only the first stop is early."}
	if !slices.Equal(got, want) {
		t.Errorf("relevantHints = %q, want %q", got, want)
	}
	if len(in) != 3 {
		t.Errorf("the input slice was modified: %q", in)
	}
}

func TestFactsAuditTheCandidate(t *testing.T) {
	j := lite(synthJob(10, 13))
	j.Shipments = append(j.Shipments, job.Shipment{ID: "huge", Pickup: 1, Dropoff: 2, WeightKg: 900, VolumeM3: 1, MaxDimensionCm: 30})
	j.Presets = []string{job.Cheapest}

	// Only a clustered experiment, so the facts have a clustering to speak about.
	j.Search = &job.Search{Experiments: []job.Experiment{{Algorithm: job.AlgKMedoids, K: 3}}}
	c := run(t, j).Candidates[0]
	f := c.Facts
	if f == nil || !f.Valid {
		t.Fatalf("facts = %+v", f)
	}
	if f.ClusterPurity <= 0 || f.ClusterPurity > 1 || f.MaxOverlap < 0 || f.MaxOverlap > 1 {
		t.Errorf("purity %v, overlap %v: both belong in [0, 1], and the purity is positive after clustering", f.ClusterPurity, f.MaxOverlap)
	}
	if len(f.Unserved) != 1 || f.Unserved[0].Shipment != "huge" || f.Unserved[0].Cause != "too_heavy" {
		t.Errorf("unserved = %+v, want huge with the cause too_heavy", f.Unserved)
	}
	if !slices.ContainsFunc(f.Hints, func(h string) bool { return strings.Contains(h, "unserved") }) {
		t.Errorf("hints = %q, want one about the unserved shipment", f.Hints)
	}
	if c.Trials[0].Unserved != 1 {
		t.Errorf("the trial counts %d unserved shipments, want 1", c.Trials[0].Unserved)
	}

	// Without clustering there is no purity to report.
	j.Search = &job.Search{Experiments: []job.Experiment{{Algorithm: job.AlgNone}}}
	if f := run(t, j).Candidates[0].Facts; f == nil || f.ClusterPurity != 0 || !f.Valid {
		t.Errorf("an unclustered candidate has no purity: %+v", f)
	}

	// A plain job carries no search record, but still its score.
	j.Search = nil
	if c := run(t, j).Candidates[0]; c.Facts != nil || c.Trials != nil || len(c.Score) == 0 {
		t.Errorf("a plain job must give a score and nothing else: facts %+v trials %+v score %v", c.Facts, c.Trials, c.Score)
	}
}

func TestACancelledSearchStillReturnsAValidTruncatedResult(t *testing.T) {
	j := synthJob(24, 17)
	j.Search = defaultSearch()
	j.Limits.Iterations = 50_000_000

	t.Run("already cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		res, err := Run(ctx, j, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range res.Candidates {
			checkRoutes(t, j, c)
			if !c.Truncated || c.Facts == nil || !c.Facts.Valid {
				t.Errorf("%s: want a valid truncated candidate, got truncated=%v facts=%+v", c.Preset, c.Truncated, c.Facts)
			}
			// Nothing is started once the context is done, except the plain solve a result cannot do without.
			if len(c.Trials) != 1 || c.Trials[0].Algorithm != job.AlgNone {
				t.Errorf("%s: trials %+v, want only the baseline", c.Preset, c.Trials)
			}
		}
		if res.Stats.Polished || res.Stats.Trials != len(job.Presets) {
			t.Errorf("stats = %+v: no polish and one solve per preset were expected", res.Stats)
		}
	})

	t.Run("cut during a trial", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		start := time.Now()
		res, err := Run(ctx, j, nil)
		if err != nil {
			t.Fatal(err)
		}
		if took := time.Since(start); took > 20*time.Second {
			t.Errorf("the search ignored cancellation and took %v", took)
		}
		for _, c := range res.Candidates {
			checkRoutes(t, j, c)
			if !c.Truncated {
				t.Errorf("%s: a cut search must be marked truncated", c.Preset)
			}
			if len(c.Trials) != 1 {
				t.Errorf("%s: %d trials ran, but the first one used up the 300ms", c.Preset, len(c.Trials))
			}
			if c.Trials[0].Algorithm != job.AlgNone || c.Trials[0].Effort != job.EffortNormal {
				t.Errorf("%s: the baseline must come first, so a cut search still holds the quick quote: %+v", c.Preset, c.Trials[0])
			}
		}
	})

	t.Run("cut in the last trial", func(t *testing.T) {
		// Nothing is left unrun, so only the trial's own report can tell that the search was cut.
		j := *j
		j.Search = &job.Search{Experiments: []job.Experiment{{Algorithm: job.AlgNone}}}
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		res, err := Run(ctx, &j, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range res.Candidates {
			checkRoutes(t, &j, c)
			if !c.Truncated {
				t.Errorf("%s: a trial cut by the context must make the candidate truncated", c.Preset)
			}
		}
	})

	t.Run("max seconds", func(t *testing.T) {
		j := *j
		j.Limits.MaxSeconds = 1
		start := time.Now()
		res, err := Run(context.Background(), &j, nil)
		if err != nil {
			t.Fatal(err)
		}
		if took := time.Since(start); took > 20*time.Second {
			t.Errorf("MaxSeconds=1 took %v", took)
		}
		for _, c := range res.Candidates {
			checkRoutes(t, &j, c)
			if !c.Truncated {
				t.Errorf("%s: the time limit cut the search, the candidate must say so", c.Preset)
			}
		}
	})

	t.Run("the incumbent survives", func(t *testing.T) {
		small := lite(synthJob(10, 17))
		small.Presets = []string{job.Cheapest}
		plain := byPreset(run(t, small))
		j := *small
		j.Search = &job.Search{From: map[string][]job.Route{job.Cheapest: plain[job.Cheapest].Routes}, Experiments: defaultSearch().Experiments, Polish: job.EffortThorough}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		res, err := Run(ctx, &j, nil)
		if err != nil {
			t.Fatal(err)
		}
		c := res.Candidates[0]
		if !c.Truncated || slices.Compare(c.Score, plain[job.Cheapest].Score) > 0 {
			t.Errorf("a cancelled search must keep at least its incumbent: truncated=%v score %v vs %v", c.Truncated, c.Score, plain[job.Cheapest].Score)
		}
	})
}

func TestExperimentsAreNormalizedBeforeTheyRun(t *testing.T) {
	type planned struct {
		alg    string
		k      int
		effort string
		seed   uint64
	}
	e := func(alg string, k int, effort string) job.Experiment {
		return job.Experiment{Algorithm: alg, K: k, Effort: effort}
	}
	for _, tc := range []struct {
		name string
		n    int
		exps []job.Experiment
		want []planned
	}{
		{"the default strategy", 30,
			[]job.Experiment{e("kmedoids", 0, ""), e("kmeans", 0, ""), e("agglomerative", 0, ""), e("none", 0, "")},
			[]planned{{"none", 0, "normal", 1}, {"kmedoids", 0, "normal", 1}, {"kmeans", 0, "normal", 2}, {"agglomerative", 0, "normal", 3}}},
		{"a repeat is dropped and later seeds keep their place", 30,
			[]job.Experiment{e("kmeans", 3, "quick"), e("kmeans", 3, "quick"), e("kmedoids", 4, "thorough")},
			[]planned{{"kmeans", 3, "quick", 1}, {"kmedoids", 4, "thorough", 3}}},
		{"k beyond the shipments is skipped, k beyond the cap is clamped", 12,
			[]job.Experiment{e("kmeans", 13, ""), e("kmeans", 12, ""), e("kmedoids", 9, "")},
			[]planned{{"kmeans", 8, "normal", 2}, {"kmedoids", 8, "normal", 3}}},
		{"clamping can make two experiments one", 30,
			[]job.Experiment{e("kmeans", 9, ""), e("kmeans", 20, "")},
			[]planned{{"kmeans", 8, "normal", 1}}},
		{"none and dbscan ignore k", 30,
			[]job.Experiment{e("none", 5, "normal"), e("none", 0, "quick"), {Algorithm: "dbscan", K: 4, EpsMinutes: 5}, {Algorithm: "dbscan", EpsMinutes: 5}},
			[]planned{{"none", 0, "normal", 1}, {"none", 0, "quick", 2}, {"dbscan", 0, "normal", 3}}},
		{"a negative k is no k", 30, []job.Experiment{e("kmeans", -1, "")}, nil},
		{"the baseline keeps the plain solve's seed and goes first, wherever it is listed", 30,
			[]job.Experiment{e("kmeans", 2, ""), e("kmeans", 3, ""), e("none", 0, "normal")},
			[]planned{{"none", 0, "normal", 1}, {"kmeans", 2, "normal", 1}, {"kmeans", 3, "normal", 2}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j := synthJob(tc.n, 1)
			j.Search = &job.Search{Experiments: tc.exps}
			b, err := build(j)
			if err != nil {
				t.Fatal(err)
			}
			var got []planned
			for _, x := range b.planExperiments() {
				got = append(got, planned{x.Algorithm, x.K, x.Effort, x.seed})
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("planned %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEffortSetsTheIterationsPerWorker(t *testing.T) {
	for _, tc := range []struct {
		n, base int
		effort  string
		want    int
	}{
		{10, 0, "quick", 400}, {10, 0, "normal", 1500}, {10, 0, "", 1500}, {10, 0, "thorough", 5000},
		{60, 0, "quick", 800}, {60, 0, "normal", 3000}, {120, 0, "thorough", 20000},
		{10, 3000, "normal", 3000}, {10, 3000, "thorough", 10000}, {10, 3000, "quick", 800},
	} {
		j := synthJob(tc.n, 1)
		j.Limits.Iterations = tc.base
		b, err := build(j)
		if err != nil {
			t.Fatal(err)
		}
		if got := b.iterations(tc.effort); got != tc.want {
			t.Errorf("n=%d base=%d %q: %d iterations, want %d", tc.n, tc.base, tc.effort, got, tc.want)
		}
	}
}

func TestSearchRoundTripsTheWire(t *testing.T) {
	j := synthJob(9, 6)
	j.Search = &job.Search{
		Experiments: []job.Experiment{{Algorithm: job.AlgDBSCAN, EpsMinutes: 7.5, Effort: job.EffortQuick}, {Algorithm: job.AlgKMeans, K: 2}},
		From:        map[string][]job.Route{job.Cheapest: {{Type: "motor", Stops: []job.Stop{{Shipment: "s000", Kind: job.Pickup}}}}},
		Polish:      job.EffortQuick,
	}
	data, err := j.Encode()
	if err != nil {
		t.Fatal(err)
	}
	back, err := job.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(t, j.Search, back.Search) {
		t.Errorf("the search did not survive the wire:\n%+v\n%+v", j.Search, back.Search)
	}
}

// scaleCase is one row of the scale table.
type scaleCase struct {
	n int
	// iterations is the base of Limits.Iterations: the search keeps its shape (four experiments at normal, a
	// thorough polish) but each solve is shortened so that a row fits a test's budget. Zero is the default.
	iterations int
	// plainSeconds and searchSeconds are the safety valves of the two runs of the row. A search that needs
	// longer is cut and flagged: at these sizes the default thorough search takes minutes, not seconds.
	plainSeconds, searchSeconds int
	short                       bool // runs under -short too
}

// TestSearchAtScale runs a thorough-style search on synthetic jobs of 30, 60 and 120 shipments and logs what it
// costs and what it buys. Only validity and "never worse than the plain solve" are asserted, and the latter only
// when neither run was cut: whether clusters pay off depends on the instance. Wall time depends on the machine and
// its load, so the CPU time is logged too; the four presets run side by side, so an idle machine with four free
// cores needs about a quarter of it. A star marks a run the valve cut short.
func TestSearchAtScale(t *testing.T) {
	for _, tc := range []scaleCase{{30, 200, 15, 40, true}, {60, 100, 10, 40, false}, {120, 40, 10, 40, false}} {
		t.Run(fmt.Sprintf("n=%d", tc.n), func(t *testing.T) {
			if testing.Short() && !tc.short {
				t.Skip("a long search; run without -short")
			}
			if raceEnabled {
				t.Skip("the race detector makes the timings meaningless")
			}
			j := synthJob(tc.n, 31)
			j.Limits.Iterations, j.Limits.MaxSeconds = tc.iterations, tc.plainSeconds
			started, cpu := time.Now(), cpuTime()
			plain := run(t, j)
			plainWall, plainCPU := time.Since(started), cpuTime()-cpu

			j.Search, j.Limits.MaxSeconds = defaultSearch(), tc.searchSeconds
			started, cpu = time.Now(), cpuTime()
			res := run(t, j)
			wall, cpuUsed := time.Since(started), cpuTime()-cpu

			p := byPreset(plain)
			plainCut := slices.ContainsFunc(plain.Candidates, func(c job.Candidate) bool { return c.Truncated })
			improved, cut := 0, 0
			var detail []string
			for _, c := range res.Candidates {
				checkRoutes(t, j, c)
				if c.Facts == nil || !c.Facts.Valid {
					t.Errorf("%s: the winner must pass the audit: %+v", c.Preset, c.Facts)
				}
				cmp := slices.Compare(c.Score, p[c.Preset].Score)
				// A cut run says nothing about the other: its baseline may have been stopped earlier or later.
				if cmp > 0 && !c.Truncated && !plainCut {
					t.Errorf("%s: the search found %v, the plain solve %v", c.Preset, c.Score, p[c.Preset].Score)
				}
				if cmp < 0 {
					improved++
				}
				if c.Truncated {
					cut++
				}
				won := "?"
				for _, tr := range c.Trials {
					if tr.Won {
						won = tr.Kind
						if tr.Algorithm != "" {
							won = fmt.Sprintf("%s/k%d", tr.Algorithm, tr.K)
						}
					}
				}
				const first = 2 // the first objective after serving and deadlines
				detail = append(detail, fmt.Sprintf("%s %d -> %d (%s, %d trials)", c.Preset, p[c.Preset].Score[first], c.Score[first], won, len(c.Trials)))
			}
			mark := func(cut bool) string {
				if cut {
					return "*"
				}
				return ""
			}
			t.Logf("\nn=%d base iterations %d (0 = default), %d of %d presets improved, %d cut\n  plain  wall %s%s cpu %s\n  search wall %s%s cpu %s, %d solves\n  %s",
				tc.n, tc.iterations, improved, len(res.Candidates), cut,
				plainWall.Round(10*time.Millisecond), mark(plainCut), plainCPU.Round(10*time.Millisecond),
				wall.Round(10*time.Millisecond), mark(cut > 0), cpuUsed.Round(10*time.Millisecond), res.Stats.Trials,
				strings.Join(detail, "\n  "))
		})
	}
}
