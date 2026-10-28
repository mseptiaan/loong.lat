package exact

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/engine/model"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/plan"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/solver"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/testutil"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/validate"
)

var day = time.Date(2026, 9, 14, 8, 0, 0, 0, time.UTC)

func at(h, m int) time.Time {
	return day.Add(time.Duration(h-8)*time.Hour + time.Duration(m)*time.Minute)
}

// install gives every profile an estimated matrix; a profile with a speed
// derives its times from the distances, as the workspace does.
func install(t testing.TB, p *model.Problem) {
	t.Helper()
	n, estDur, estDist := testutil.EstimateMatrix(p.Locs)
	mats := make([]*model.Matrix, len(p.Profiles))
	for i, prof := range p.Profiles {
		dur := estDur
		if prof.SpeedKmh > 0 {
			dur = make([]float64, len(estDist))
			for k, d := range estDist {
				dur[k] = d / (prof.SpeedKmh / 3.6)
			}
		}
		mats[i] = model.NewMatrix(n, dur, estDist, "estimate")
	}
	if err := p.SetMatrices(mats); err != nil {
		t.Fatal(err)
	}
}

func TestExactFindsTheKnownOptimumOnALine(t *testing.T) {
	// Depot and four deliveries on a meridian: the best tour goes out to the
	// farthest and back, whatever order the stops are listed in.
	depot := plan.Position{Lon: -75, Lat: 45}
	pl := &plan.Plan{Objectives: []string{"maximizeMandatoryStops", "minimizeDistance"}}
	pl.Resources = []plan.Resource{{ID: "v", VehicleProfile: plan.VehicleProfile{Type: "truck"}, Departure: &depot, Arrival: &depot,
		WorkingTimeWindow: &plan.TimeWindow{Begin: day, End: day.Add(12 * time.Hour)}, Capacities: map[string]float64{"w": 100}}}
	for i, k := range []int{3, 1, 4, 2} {
		pl.Orders = append(pl.Orders, plan.Order{ID: fmt.Sprintf("o%d", i), Stops: []plan.Stop{{
			Type: "single", ID: fmt.Sprintf("s%d", i), Kind: plan.KindDelivery,
			Position:          plan.Position{Lon: -75, Lat: 45 + 0.01*float64(k)},
			OperationDuration: plan.Duration(time.Minute), Capacities: map[string]float64{"w": 1}}}})
	}
	p, err := model.Compile(pl, model.Options{})
	if err != nil {
		t.Fatal(err)
	}
	install(t, p)
	res, err := Solve(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Out to the farthest stop and back. Each leg is rounded to a whole meter,
	// so the sum of legs may differ from twice the longest by a few meters.
	var farthest int64
	for l := 1; l < len(p.Locs); l++ {
		farthest = max(farthest, p.Mats[0].TravelDist(0, l))
	}
	want := 2 * farthest
	if got := res.Solution.M[model.ObjDist]; got < want-5 || got > want+5 {
		t.Errorf("optimal distance = %d, want about %d", got, want)
	}
	if r := validate.Check(p, res.Solution); !r.Valid {
		t.Errorf("the exact solution must validate: %v", r.Violations)
	}
}

func TestExactLeavesOutWhatCannotBeServed(t *testing.T) {
	depot := plan.Position{Lon: -75, Lat: 45}
	pl := &plan.Plan{Objectives: []string{"maximizeMandatoryStops", "minimizeDistance"}}
	pl.Resources = []plan.Resource{{ID: "v", VehicleProfile: plan.VehicleProfile{Type: "truck"}, Departure: &depot, Arrival: &depot,
		WorkingTimeWindow: &plan.TimeWindow{Begin: day, End: day.Add(12 * time.Hour)}, Capacities: map[string]float64{"w": 5}}}
	for i, q := range []float64{3, 9} { // the second order is heavier than the vehicle can carry
		pl.Orders = append(pl.Orders, plan.Order{ID: fmt.Sprintf("o%d", i), Stops: []plan.Stop{{
			Type: "single", ID: fmt.Sprintf("s%d", i), Kind: plan.KindDelivery,
			Position: plan.Position{Lon: -75, Lat: 45.01 + 0.01*float64(i)}, Capacities: map[string]float64{"w": q}}}})
	}
	p, err := model.Compile(pl, model.Options{})
	if err != nil {
		t.Fatal(err)
	}
	install(t, p)
	res, err := Solve(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Solution.Unassigned) != 1 || res.Solution.M[model.ObjUnserved] != 1 {
		t.Errorf("exactly the heavy order must be left out: %v", res.Solution.Unassigned)
	}
}

func TestExactRefusesLargeProblems(t *testing.T) {
	depot := plan.Position{Lon: -75, Lat: 45}
	pl := &plan.Plan{}
	pl.Resources = []plan.Resource{{ID: "v", Departure: &depot, Arrival: &depot}}
	for i := range MaxOrders + 1 {
		pl.Orders = append(pl.Orders, plan.Order{ID: fmt.Sprintf("o%d", i), Stops: []plan.Stop{{
			Type: "single", ID: fmt.Sprintf("s%d", i), Kind: plan.KindDelivery, Position: plan.Position{Lon: -75, Lat: 45.01 + 0.01*float64(i)}}}})
	}
	p, err := model.Compile(pl, model.Options{})
	if err != nil {
		t.Fatal(err)
	}
	install(t, p)
	if _, err := Solve(p, 0); !errors.Is(err, ErrTooLarge) {
		t.Errorf("want ErrTooLarge, got %v", err)
	}
}

// randomPlan builds a small, varied instance: pickup-and-delivery and plain
// orders, vehicles that differ in capacity, speed and skills, hard and soft
// time windows, and different objective priorities.
func randomPlan(rng *rand.Rand) *plan.Plan {
	depot := plan.Position{Lon: -75.7, Lat: 45.42}
	scatter := func() plan.Position {
		return plan.Position{Lon: depot.Lon + (rng.Float64()-0.5)*0.30, Lat: depot.Lat + (rng.Float64()-0.5)*0.20}
	}
	objectives := [][]string{
		{"maximizeMandatoryStops", "maximizePreferredStops", "minimizeDelay", "minimizeDistance", "minimizeLargestTourDuration", "minimizeWorkingDuration"},
		{"maximizeMandatoryStops", "minimizeLargestTourDuration", "minimizeDistance", "minimizeWorkingDuration"},
		{"maximizeMandatoryStops", "minimizeCosts", "minimizeDistance", "minimizeLargestTourDuration"},
	}
	pl := &plan.Plan{TZ: "UTC", Objectives: objectives[rng.IntN(len(objectives))]}

	for i := range 2 + rng.IntN(3) {
		r := plan.Resource{
			ID: fmt.Sprintf("v%d", i), Departure: &depot, Arrival: &depot,
			VehicleProfile:    plan.VehicleProfile{Type: "truck", AvgSpeedKmh: []float64{0, 25, 40, 60}[rng.IntN(4)]},
			Capacities:        map[string]float64{"w": float64([]int{6, 9, 14}[rng.IntN(3)])},
			WorkingTimeWindow: &plan.TimeWindow{Begin: at(8, 0), End: at(8+8+rng.IntN(4), 0)},
		}
		if rng.IntN(2) == 0 {
			r.Skills = []string{"cold"}
		}
		if rng.IntN(3) == 0 {
			r.Cost = &plan.ResourceCost{WorkedHours: &plan.WorkedHoursCost{CostFloor: 2 + float64(rng.IntN(3)), OvercostFloor: 8, OvercostCoeff: 1.5}}
		}
		if rng.IntN(4) == 0 {
			r.MaxWorkingDuration = plan.Duration(time.Duration(5+rng.IntN(3)) * time.Hour)
		}
		pl.Resources = append(pl.Resources, r)
	}

	for i := range 3 + rng.IntN(3) { // 3 to 5 orders
		qty := map[string]float64{"w": float64(1 + rng.IntN(6))}
		svc := plan.Duration(time.Duration(3+rng.IntN(8)) * time.Minute)
		del := plan.Stop{Type: "single", ID: fmt.Sprintf("d%d", i), Kind: plan.KindDelivery, Position: scatter(), OperationDuration: svc, Capacities: qty}
		switch rng.IntN(4) {
		case 0: // hard window
			b := 9 + rng.IntN(5)
			del.AuthorizedTimeWindows = []plan.TimeWindow{{Begin: at(b, 0), End: at(b+2+rng.IntN(3), 0)}}
		case 1: // soft window
			b := 9 + rng.IntN(6)
			del.PreferredTimeWindows = []plan.TimeWindow{{Begin: at(b, 0), End: at(b+1, 30)}}
		}
		o := plan.Order{ID: fmt.Sprintf("o%d", i), Stops: []plan.Stop{del}}
		if rng.IntN(2) == 0 {
			pick := plan.Stop{Type: "single", ID: fmt.Sprintf("p%d", i), Kind: plan.KindPickup, Position: depot, OperationDuration: plan.Duration(2 * time.Minute), Capacities: qty}
			o.Stops, o.SuccessiveStops = []plan.Stop{pick, del}, true
		}
		if rng.IntN(4) == 0 {
			o.RequiredSkills = []string{"cold"}
		}
		pl.Orders = append(pl.Orders, o)
	}
	return pl
}

// withLunch gives every vehicle of pl a fixed rest of 30 to 60 minutes that
// starts between 09:30 and 11:30.
func withLunch(pl *plan.Plan, rng *rand.Rand) {
	begin := at(9, 30).Add(time.Duration(rng.IntN(5)) * 30 * time.Minute)
	end := begin.Add(time.Duration(30+30*rng.IntN(2)) * time.Minute)
	for i := range pl.Resources {
		pl.Resources[i].Breaks = []plan.Break{{Type: plan.BreakFixed, Begin: begin, End: end}}
	}
}

// withTrips leaves one or two small vehicles that return to the depot between
// trips, with up to ten minutes spent there each time, so that serving every
// order often takes more than one trip.
func withTrips(pl *plan.Plan, rng *rand.Rand) {
	pl.Resources = pl.Resources[:1+rng.IntN(min(2, len(pl.Resources)))]
	for i := range pl.Resources {
		pl.Resources[i].MultiTrip = true
		pl.Resources[i].TurnaroundDuration = plan.Duration(time.Duration(rng.IntN(11)) * time.Minute)
		pl.Resources[i].Capacities = map[string]float64{"w": 6}
	}
}

// withLoong turns pl into a courier job the way loong.lat poses it: every vehicle
// starts somewhere of its own and finishes at its last drop (an open route), pays
// the tariff (fixed + per km + per hour + per extra order + a weight surcharge,
// the approach to the first stop free for some), and every delivery may carry a
// soft deadline. The objectives are the loong presets, which include the sum of
// the arrival times and the weighted blend.
func withLoong(pl *plan.Plan, rng *rand.Rand) {
	scatter := func() *plan.Position {
		return &plan.Position{Lon: -75.7 + (rng.Float64()-0.5)*0.30, Lat: 45.42 + (rng.Float64()-0.5)*0.20}
	}
	for i := range pl.Resources {
		r := &pl.Resources[i]
		r.Departure, r.Arrival, r.OpenRoute = scatter(), nil, true
		r.Cost = &plan.ResourceCost{
			Fixed: 6000, PerKm: 1.8, PerHour: 9000, PerOrder: 2500 * float64(rng.IntN(2)),
			Surcharge:    &plan.LoadSurcharge{Dimension: "w", Above: 4, PerUnit: 80},
			FreeApproach: rng.IntN(2) == 0,
		}
	}
	for i := range pl.Orders {
		stops := pl.Orders[i].Stops
		last := &stops[len(stops)-1]
		last.AuthorizedTimeWindows, last.PreferredTimeWindows = nil, nil
		if rng.IntN(2) == 0 {
			last.PreferredTimeWindows = []plan.TimeWindow{{Begin: at(8, 0), End: at(9+rng.IntN(3), 30*rng.IntN(2))}}
		}
	}
	pl.Blend = &plan.Blend{Cost: 1, SumArrival: 20, Dist: 3, Makespan: 10}
	pl.Objectives = [][]string{
		{"maximizeMandatoryStops", "minimizeDelay", "minimizeCosts", "minimizeDistance", "minimizeSumArrival"},
		{"maximizeMandatoryStops", "minimizeDelay", "minimizeDistance", "minimizeCosts", "minimizeSumArrival"},
		{"maximizeMandatoryStops", "minimizeDelay", "minimizeLargestTourDuration", "minimizeSumArrival", "minimizeCosts"},
		{"maximizeMandatoryStops", "minimizeDelay", "minimizeBlend"},
	}[rng.IntN(4)]
}

// TestHeuristicMatchesTheOptimumWithLoongSettings repeats the comparison for
// open routes, the loong tariff, soft deadlines and the new objectives. Open
// routes with a free first leg have a rougher landscape for the local search
// (roughly 3 instances in 1000 stay one step short of the optimum), so up to
// 1% of the instances may end suboptimal. Quotes of up to MaxOrders orders do
// not depend on this: they are solved exactly.
func TestHeuristicMatchesTheOptimumWithLoongSettings(t *testing.T) {
	compareWithExact(t, "loong", withLoong, 0.01)
}

// TestHeuristicMatchesTheProvenOptimum is the main reason this package exists:
// on many small random instances the heuristic must reach exactly the optimum
// the exhaustive search proves.
func TestHeuristicMatchesTheProvenOptimum(t *testing.T) {
	compareWithExact(t, "", nil, 0)
}

// TestHeuristicMatchesTheOptimumWithAFixedBreak repeats the comparison when
// every vehicle rests at a fixed time of day.
func TestHeuristicMatchesTheOptimumWithAFixedBreak(t *testing.T) {
	compareWithExact(t, "lunch", withLunch, 0)
}

// TestHeuristicMatchesTheOptimumWithSeveralTrips repeats the comparison when
// vehicles may unload at the depot in the middle of the day.
func TestHeuristicMatchesTheOptimumWithSeveralTrips(t *testing.T) {
	compareWithExact(t, "trips", withTrips, 0)
}

// compareWithExact solves random instances both ways. The heuristic may never beat
// the proof, and may end suboptimal on at most tolerance of the instances.
func compareWithExact(t *testing.T, name string, tweak func(*plan.Plan, *rand.Rand), tolerance float64) {
	instances, first := 60, uint64(0)
	if testing.Short() {
		instances = 12
	}
	// VRP_EXACT_INSTANCES=1000 VRP_EXACT_FIRST_SEED=5000 widens the check.
	if v, err := strconv.Atoi(os.Getenv("VRP_EXACT_INSTANCES")); err == nil && v > 0 {
		instances = v
	}
	if v, err := strconv.ParseUint(os.Getenv("VRP_EXACT_FIRST_SEED"), 10, 64); err == nil {
		first = v
	}
	var gaps, resting, trips int
	for seed := first; seed < first+uint64(instances); seed++ {
		rng := rand.New(rand.NewPCG(seed, seed*7919+1))
		pl := randomPlan(rng)
		if tweak != nil {
			tweak(pl, rng)
		}
		p, err := model.Compile(pl, model.Options{})
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		install(t, p)

		ex, err := Solve(p, 0)
		if err != nil {
			t.Fatalf("seed %d: exact: %v", seed, err)
		}
		if r := validate.Check(p, ex.Solution); !r.Valid {
			t.Fatalf("seed %d: the exact solution fails validation: %v", seed, r.Violations)
		}
		heur, _ := solver.Solve(p, nil, solver.Options{Seed: 1, Iterations: 1500, Workers: 2})
		if r := validate.Check(p, heur); !r.Valid {
			t.Fatalf("seed %d: the heuristic solution fails validation: %v", seed, r.Violations)
		}
		resting += restingRoutes(p, ex.Solution)
		trips += tripRoutes(p, ex.Solution)

		switch c := p.Compare(&heur.M, &ex.Solution.M); {
		case c < 0:
			t.Errorf("seed %d: the heuristic beat the 'exhaustive' optimum, so the exact search misses solutions\n heuristic %v\n exact     %v\n%s",
				seed, heur.M, ex.Solution.M, heur.Describe())
		case c > 0:
			gaps++
			logf := t.Errorf
			if tolerance > 0 {
				logf = t.Logf
			}
			logf("seed %d: the heuristic is suboptimal (objectives %v)\n heuristic %v\n optimum   %v\nheuristic routes:\n%soptimal routes:\n%s",
				seed, p.ObjectiveOrder(), heur.M, ex.Solution.M, heur.Describe(), ex.Solution.Describe())
		}
	}
	if float64(gaps) > tolerance*float64(instances) {
		t.Errorf("%s: %d of %d instances are suboptimal, more than the allowed %.1f%%", name, gaps, instances, tolerance*100)
	}
	t.Logf("%s: %d instances, %d suboptimal results, %d optimal routes rest during their tour, %d make several trips", name, instances, gaps, resting, trips)
	if name == "lunch" && resting == 0 {
		t.Error("no optimal route overlaps the fixed break, so the test does not exercise it")
	}
	if name == "trips" && trips == 0 {
		t.Error("no optimal route makes several trips, so the test does not exercise them")
	}
}

// restingRoutes counts the routes of s whose tour includes part of a fixed break.
func restingRoutes(p *model.Problem, s *model.Solution) int {
	ev := model.NewEvaluator(p)
	var n int
	for v, route := range s.Routes {
		if fb := p.Vehicles[v].Fixed; fb != nil && len(route) > 0 {
			if d := ev.Detail(v, route); fb.Overlap(d.Depart, d.Arrive) > 0 {
				n++
			}
		}
	}
	return n
}

// tripRoutes counts the routes of s that return to the depot in the middle of the day.
func tripRoutes(p *model.Problem, s *model.Solution) int {
	ev := model.NewEvaluator(p)
	var n int
	for v, route := range s.Routes {
		if len(route) > 0 && ev.Detail(v, route).Trips > 1 {
			n++
		}
	}
	return n
}
