package solver

import (
	"context"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/engine/model"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/plan"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/testutil"
)

var t0 = time.Date(2026, 9, 14, 6, 0, 0, 0, time.UTC)

func repoProblem(tb testing.TB) *model.Problem { return testutil.RepoProblem(tb) }

// check fails the test unless s is a complete, feasible solution.
func check(t *testing.T, s *model.Solution) {
	t.Helper()
	ev := model.NewEvaluator(s.P)
	fresh := model.NewSolution(s.P)
	copy(fresh.Routes, s.Routes)
	fresh.Unassigned = s.Unassigned
	fresh.Recompute(ev)
	if !fresh.Feasible {
		t.Fatalf("solution is infeasible:\n%s", s.Describe())
	}
	if fresh.M != s.M {
		t.Errorf("cached metrics drifted from a fresh evaluation:\n cached %v\n fresh  %v", s.M, fresh.M)
	}
	seen := map[int]int{}
	for _, route := range s.Routes {
		for _, n := range route {
			seen[n]++
		}
	}
	for _, o := range s.Unassigned {
		for _, n := range s.P.Orders[o].Nodes {
			seen[n]++
		}
	}
	for n := range s.P.Nodes {
		if seen[n] != 1 {
			t.Errorf("node %s appears %d times", s.P.Nodes[n].ID, seen[n])
		}
	}
}

func TestSolveRepositoryPlanServesEverything(t *testing.T) {
	p := repoProblem(t)
	s, stats := Solve(p, nil, Options{Seed: 1, Iterations: 400, Workers: 2})
	check(t, s)
	if len(s.Unassigned) != 0 {
		t.Errorf("unassigned orders: %v", s.Unassigned)
	}
	if stats.Final != s.M {
		t.Errorf("stats do not match the solution")
	}
	// The three keychain orders are bound to a single driver.
	ov := s.OrderVehicle()
	var keychain []int
	for o, ord := range p.Orders {
		if len(ord.ID) > 8 && ord.ID[:8] == "order-ke" {
			keychain = append(keychain, ov[o])
		}
	}
	if len(keychain) != 3 || keychain[0] != keychain[1] || keychain[1] != keychain[2] {
		t.Errorf("keychain orders must share one vehicle, got %v", keychain)
	}
	// driver-1 (index 2) cannot carry goods: it may only have keychain stops.
	for _, n := range s.Routes[2] {
		if p.Nodes[n].Demand != nil {
			t.Errorf("driver-1 was given goods: %s", p.Nodes[n].ID)
		}
	}
}

func TestImprovementNeverWorsensConstruction(t *testing.T) {
	p := repoProblem(t)
	s, stats := Solve(p, nil, Options{Seed: 7, Iterations: 300, Workers: 2})
	if p.Less(&stats.Initial, &stats.Final) {
		t.Errorf("improvement made things worse:\n initial %v\n final   %v", stats.Initial, stats.Final)
	}
	check(t, s)
}

func TestSolveIsDeterministicForOneWorker(t *testing.T) {
	p := repoProblem(t)
	a, _ := Solve(p, nil, Options{Seed: 5, Iterations: 200, Workers: 1})
	b, _ := Solve(p, nil, Options{Seed: 5, Iterations: 200, Workers: 1})
	if a.M != b.M {
		t.Errorf("same seed gave different metrics:\n%v\n%v", a.M, b.M)
	}
}

func TestSolveWithClusteringAssignment(t *testing.T) {
	p := repoProblem(t)
	// Fabricated clustering: split orders into three groups by index.
	cl := make([]int, len(p.Orders))
	for o := range cl {
		cl[o] = o % 3
	}
	s, _ := Solve(p, cl, Options{Seed: 2, Iterations: 200, Workers: 1})
	check(t, s)
	if len(s.Unassigned) != 0 {
		t.Errorf("unassigned orders: %v", s.Unassigned)
	}
}

func TestTimeLimitStopsSearchEarly(t *testing.T) {
	p := repoProblem(t)
	start := time.Now()
	s, stats := Solve(p, nil, Options{Seed: 1, Iterations: 1_000_000, Workers: 1, TimeLimit: 300 * time.Millisecond})
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("solve ignored the time limit and took %v", took)
	}
	if stats.Iterations >= 1_000_000 {
		t.Error("the search ran to its iteration cap instead of stopping at the time limit")
	}
	// Even when the limit cuts the search short (slow machines, race detector)
	// the construction phase must already have produced a valid solution.
	check(t, s)
}

func TestCancellingTheContextEndsTheSolveWithAValidSolution(t *testing.T) {
	p := repoProblem(t)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	s, stats := SolveContext(ctx, p, nil, Options{Seed: 1, Iterations: 10_000_000, Workers: 2})
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("solve ignored the cancelled context and took %v", took)
	}
	if !stats.Truncated {
		t.Error("a cancelled solve must report Truncated")
	}
	check(t, s)

	// An already cancelled context still yields a complete evaluated solution.
	done, stop := context.WithCancel(context.Background())
	stop()
	s, stats = SolveContext(done, p, nil, Options{Seed: 1, Iterations: 1000, Workers: 1})
	if !stats.Truncated {
		t.Error("a solve on a cancelled context must report Truncated")
	}
	check(t, s)
}

func TestACompletedSolveIsNotTruncated(t *testing.T) {
	p := repoProblem(t)
	if _, stats := Solve(p, nil, Options{Seed: 1, Iterations: 100, Workers: 1}); stats.Truncated {
		t.Error("a solve that ran to its iteration cap must not report Truncated")
	}
}

// lineProblem builds a problem on a line: location k costs 600*k seconds and
// 5000*k meters from the depot, all deliveries without further constraints.
func lineProblem(t *testing.T, vehicles int, capacity float64, stops []int, demand float64) *model.Problem {
	t.Helper()
	pos := func(k int) plan.Position { return plan.Position{Lon: -75, Lat: 45 + 0.01*float64(k)} }
	d := pos(0)
	pl := &plan.Plan{Objectives: []string{"maximizeMandatoryStops", "minimizeDistance", "minimizeLargestTourDuration"}}
	for i := range vehicles {
		pl.Resources = append(pl.Resources, plan.Resource{
			ID: string(rune('a' + i)), VehicleProfile: plan.VehicleProfile{Type: "truck"},
			Departure: &d, Arrival: &d,
			WorkingTimeWindow: &plan.TimeWindow{Begin: t0, End: t0.Add(20 * time.Hour)},
			Capacities:        map[string]float64{"w": capacity},
		})
	}
	// Register depot as location 0 and stops in the order given by k, so the
	// matrix below can be defined on location indexes.
	for _, k := range stops {
		pl.Orders = append(pl.Orders, plan.Order{ID: "o" + string(rune('0'+k)), Stops: []plan.Stop{{
			Type: "single", ID: "s" + string(rune('0'+k)), Position: pos(k), Kind: plan.KindDelivery,
			OperationDuration: plan.Duration(time.Minute), Capacities: map[string]float64{"w": demand},
		}}})
	}
	p, err := model.Compile(pl, model.Options{})
	if err != nil {
		t.Fatal(err)
	}
	n := len(p.Locs)
	dur, dist := make([]float64, n*n), make([]float64, n*n)
	lat := func(i int) float64 { return (p.Locs[i].Lat - 45) / 0.01 }
	for i := range n {
		for j := range n {
			d := lat(i) - lat(j)
			if d < 0 {
				d = -d
			}
			dur[i*n+j], dist[i*n+j] = 600*d, 5000*d
		}
	}
	mats := make([]*model.Matrix, len(p.Profiles))
	for i := range mats {
		mats[i] = model.NewMatrix(n, dur, dist, "line")
	}
	if err := p.SetMatrices(mats); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSolveFindsOptimalOutAndBackOnALine(t *testing.T) {
	// One vehicle, stops at 1..6 on a line: the optimum is to go to 6 and back,
	// 2*6*5000 = 60000 m, regardless of how the stops are listed.
	p := lineProblem(t, 1, 100, []int{4, 1, 6, 3, 2, 5}, 1)
	s, _ := Solve(p, nil, Options{Seed: 1, Iterations: 300, Workers: 1})
	check(t, s)
	if got := s.M[model.ObjDist]; got != 60000 {
		t.Errorf("distance = %d, want the optimum 60000", got)
	}
}

func TestSolveUsesSecondVehicleWhenCapacityRequiresIt(t *testing.T) {
	// Capacity 3 per vehicle and 6 unit orders: two vehicles are mandatory.
	p := lineProblem(t, 2, 3, []int{1, 2, 3, 4, 5, 6}, 1)
	s, _ := Solve(p, nil, Options{Seed: 1, Iterations: 300, Workers: 1})
	check(t, s)
	if len(s.Unassigned) != 0 {
		t.Fatalf("unassigned: %v", s.Unassigned)
	}
	if s.VehiclesUsed() != 2 {
		t.Errorf("vehicles used = %d, want 2", s.VehiclesUsed())
	}
}

func TestUnservableOrderIsReportedNotDropped(t *testing.T) {
	// One order is heavier than any vehicle can carry.
	p := lineProblem(t, 1, 3, []int{1, 2}, 5)
	s, _ := Solve(p, nil, Options{Seed: 1, Iterations: 50, Workers: 1})
	if len(s.Unassigned) != 2 || s.M[model.ObjUnserved] != 2 {
		t.Errorf("unassigned=%v unserved=%d, want both orders unserved", s.Unassigned, s.M[model.ObjUnserved])
	}
	check(t, s)
}

func BenchmarkSolveRepositoryPlan(b *testing.B) {
	p := repoProblem(b)
	for b.Loop() {
		Solve(p, nil, Options{Seed: 1, Iterations: 200, Workers: 1})
	}
}

func TestRefineNeverWorsensAndKeepsFeasibility(t *testing.T) {
	p := repoProblem(t)
	first, _ := Solve(p, nil, Options{Seed: 3, Iterations: 150, Workers: 1, SkipFleetReduction: true})
	refined, stats := Refine(p, first, Options{Seed: 9, Iterations: 600, Workers: 2})
	check(t, refined)
	if p.Less(&first.M, &refined.M) {
		t.Errorf("refining made the solution worse:\n before %v\n after  %v", first.M, refined.M)
	}
	if stats.Initial != first.M {
		t.Errorf("a refinement must start from the given solution")
	}
	// The input must be left untouched.
	again := first.Clone()
	again.Recompute(model.NewEvaluator(p))
	if again.M != first.M {
		t.Error("Refine modified the solution it was given")
	}
}

func TestFleetReductionDropsAVehicleWhenOneIsEnough(t *testing.T) {
	// Six small orders fit one vehicle easily, but start from a solution that
	// spreads them over three vehicles. With a fixed cost per vehicle, the
	// refinement must find the single-vehicle plan.
	p := lineProblemWithFixedCost(t)
	spread := model.NewSolution(p)
	ev := model.NewEvaluator(p)
	for o := range p.Orders {
		v := o % 3
		spread.Routes[v] = append(spread.Routes[v], p.Orders[o].Nodes...)
	}
	spread.Unassigned = nil
	spread.Recompute(ev)
	if !spread.Feasible || spread.VehiclesUsed() != 3 {
		t.Fatalf("setup: feasible=%v vehicles=%d", spread.Feasible, spread.VehiclesUsed())
	}

	best, _ := Refine(p, spread, Options{Seed: 1, Iterations: 300, Workers: 1})
	check(t, best)
	if best.VehiclesUsed() != 1 {
		t.Errorf("a fixed cost per vehicle should make the search drop two of three vehicles, got %d", best.VehiclesUsed())
	}
}

// lineProblemWithFixedCost is lineProblem with three vehicles that each cost 100 when used.
func lineProblemWithFixedCost(t *testing.T) *model.Problem {
	t.Helper()
	pos := func(k int) plan.Position { return plan.Position{Lon: -75, Lat: 45 + 0.01*float64(k)} }
	d := pos(0)
	pl := &plan.Plan{Objectives: []string{"maximizeMandatoryStops", "minimizeCosts", "minimizeDistance"}}
	for i := range 3 {
		pl.Resources = append(pl.Resources, plan.Resource{
			ID: string(rune('a' + i)), VehicleProfile: plan.VehicleProfile{Type: "truck"},
			Departure: &d, Arrival: &d,
			WorkingTimeWindow: &plan.TimeWindow{Begin: t0, End: t0.Add(20 * time.Hour)},
			Capacities:        map[string]float64{"w": 100},
			Cost:              &plan.ResourceCost{Fixed: 100},
		})
	}
	for k := 1; k <= 6; k++ {
		pl.Orders = append(pl.Orders, plan.Order{ID: "o" + string(rune('0'+k)), Stops: []plan.Stop{{
			Type: "single", ID: "s" + string(rune('0'+k)), Position: pos(k), Kind: plan.KindDelivery,
			OperationDuration: plan.Duration(time.Minute), Capacities: map[string]float64{"w": 1}}}})
	}
	p, err := model.Compile(pl, model.Options{})
	if err != nil {
		t.Fatal(err)
	}
	n := len(p.Locs)
	dur, dist := make([]float64, n*n), make([]float64, n*n)
	lat := func(i int) float64 { return (p.Locs[i].Lat - 45) / 0.01 }
	for i := range n {
		for j := range n {
			x := lat(i) - lat(j)
			if x < 0 {
				x = -x
			}
			dur[i*n+j], dist[i*n+j] = 600*x, 5000*x
		}
	}
	mats := make([]*model.Matrix, len(p.Profiles))
	for i := range mats {
		mats[i] = model.NewMatrix(n, dur, dist, "line")
	}
	if err := p.SetMatrices(mats); err != nil {
		t.Fatal(err)
	}
	return p
}
