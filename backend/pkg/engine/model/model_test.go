package model

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/engine/plan"
)

var t0 = time.Date(2026, 9, 14, 6, 0, 0, 0, time.UTC)

func at(h, m int) time.Time { return time.Date(2026, 9, 14, h, m, 0, 0, time.UTC) }

// pos places location k on a meridian so that location indexes follow k.
func pos(k int) plan.Position { return plan.Position{Lon: -75, Lat: 45 + 0.01*float64(k)} }

func vehicle(id string, end time.Time) plan.Resource {
	d := pos(0)
	return plan.Resource{
		ID: id, VehicleProfile: plan.VehicleProfile{Type: "truck"},
		Departure: &d, Arrival: &d,
		WorkingTimeWindow: &plan.TimeWindow{Begin: t0, End: end},
		Capacities:        map[string]float64{"w": 10},
		Tags:              []string{"driver"},
	}
}

func delivery(id string, k int, svc time.Duration) plan.Stop {
	return plan.Stop{Type: "single", ID: id, Position: pos(k), Kind: plan.KindDelivery, OperationDuration: plan.Duration(svc)}
}

func order(id string, stops ...plan.Stop) plan.Order { return plan.Order{ID: id, Stops: stops} }

// compileLine compiles pl and installs matrices where moving between location
// indexes i and j takes 600*|i-j| seconds and 5000*|i-j| meters.
func compileLine(t *testing.T, pl *plan.Plan, opt Options) *Problem {
	t.Helper()
	if pl.Objectives == nil {
		pl.Objectives = []string{"maximizeMandatoryStops", "maximizePreferredStops", "minimizeDelay", "minimizeCosts", "minimizeDistance"}
	}
	p, err := Compile(pl, opt)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	n := len(p.Locs)
	dur, dist := make([]float64, n*n), make([]float64, n*n)
	for i := range n {
		for j := range n {
			d := float64(i - j)
			if d < 0 {
				d = -d
			}
			dur[i*n+j], dist[i*n+j] = 600*d, 5000*d
		}
	}
	mats := make([]*Matrix, len(p.Profiles))
	for i := range mats {
		mats[i] = NewMatrix(n, dur, dist, "test")
	}
	if err := p.SetMatrices(mats); err != nil {
		t.Fatal(err)
	}
	return p
}

func nodeIdx(t *testing.T, p *Problem, id string) int {
	t.Helper()
	for i, n := range p.Nodes {
		if n.ID == id {
			return i
		}
	}
	t.Fatalf("no node %q", id)
	return -1
}

func TestEvalBasicTimeline(t *testing.T) {
	pl := &plan.Plan{
		Resources: []plan.Resource{vehicle("v", at(20, 0))},
		Orders: []plan.Order{
			order("o1", delivery("s1", 1, 5*time.Minute)),
			order("o2", delivery("s2", 2, 5*time.Minute)),
		},
	}
	p := compileLine(t, pl, Options{})
	ev := NewEvaluator(p).Eval(0, []int{0, 1})
	if !ev.OK {
		t.Fatalf("infeasible: %v", ev.Fail)
	}
	// 10 min to s1, 5 service, 10 to s2, 5 service, 20 back.
	if want := int64(50 * 60); ev.Work != want || ev.Elapsed != want {
		t.Errorf("work=%d elapsed=%d, want %d", ev.Work, ev.Elapsed, want)
	}
	if ev.Dist != 5000+5000+10000 || ev.Wait != 0 {
		t.Errorf("dist=%d wait=%d", ev.Dist, ev.Wait)
	}
	if ev.Depart != 0 {
		t.Errorf("depart = %d, want the window start", ev.Depart)
	}
}

func TestEvalDepartureShiftRemovesWaiting(t *testing.T) {
	s := delivery("s1", 1, 5*time.Minute)
	s.AuthorizedTimeWindows = []plan.TimeWindow{{Begin: at(9, 0), End: at(10, 0)}}
	pl := &plan.Plan{
		Resources: []plan.Resource{vehicle("v", at(20, 0))},
		Orders:    []plan.Order{order("o1", s)},
	}
	p := compileLine(t, pl, Options{})
	d := NewEvaluator(p).Detail(0, []int{0})
	if !d.Eval.OK {
		t.Fatalf("infeasible: %v", d.Eval.Fail)
	}
	if got, want := d.Stops[0].Begin, p.Seconds(at(9, 0)); got != want {
		t.Errorf("service starts at %d, want %d", got, want)
	}
	if d.Eval.Wait != 0 {
		t.Errorf("waiting %ds should have been absorbed by leaving later", d.Eval.Wait)
	}
	if want := p.Seconds(at(8, 50)); d.Depart != want {
		t.Errorf("depart = %d, want %d (08:50)", d.Depart, want)
	}
	if want := int64(10*60 + 5*60 + 10*60); d.Eval.Elapsed != want {
		t.Errorf("elapsed = %d, want %d", d.Eval.Elapsed, want)
	}
}

func TestEvalAuthorizedWindowMissIsInfeasible(t *testing.T) {
	s := delivery("s1", 1, 5*time.Minute)
	s.AuthorizedTimeWindows = []plan.TimeWindow{{Begin: at(6, 0), End: at(6, 5)}} // 10 min trip cannot make it
	pl := &plan.Plan{Resources: []plan.Resource{vehicle("v", at(20, 0))}, Orders: []plan.Order{order("o1", s)}}
	p := compileLine(t, pl, Options{})
	ev := NewEvaluator(p).Eval(0, []int{0})
	if ev.OK || ev.Fail != FailWindow {
		t.Errorf("want FailWindow, got ok=%v fail=%v", ev.OK, ev.Fail)
	}
}

func TestEvalPreferredWindowTargetedAndDropped(t *testing.T) {
	s := delivery("s1", 1, 5*time.Minute)
	s.PreferredTimeWindows = []plan.TimeWindow{{Begin: at(10, 0), End: at(11, 0)}}
	mk := func(end time.Time) *Problem {
		pl := &plan.Plan{Resources: []plan.Resource{vehicle("v", end)}, Orders: []plan.Order{order("o1", s)}}
		return compileLine(t, pl, Options{})
	}

	p := mk(at(20, 0))
	d := NewEvaluator(p).Detail(0, []int{0})
	if !d.Eval.OK || d.Eval.UnsatPref != 0 || d.Stops[0].PrefTW != 1 {
		t.Errorf("preferred window should be met: %+v", d.Eval)
	}
	if d.Stops[0].Begin < p.Seconds(at(10, 0)) {
		t.Errorf("service began before the preferred window: %d", d.Stops[0].Begin)
	}

	// The vehicle stops working at 08:00, so waiting for 10:00 is impossible.
	p = mk(at(8, 0))
	ev := NewEvaluator(p).Eval(0, []int{0})
	if !ev.OK {
		t.Fatalf("route must stay feasible by dropping the preference: %v", ev.Fail)
	}
	if ev.UnsatPref != 1 || ev.Delay != 0 {
		t.Errorf("unsat=%d delay=%d, want an early (not late) miss", ev.UnsatPref, ev.Delay)
	}
}

func TestEvalLatePreferredCountsDelay(t *testing.T) {
	s := delivery("s1", 1, 5*time.Minute)
	s.PreferredTimeWindows = []plan.TimeWindow{{Begin: at(6, 0), End: at(6, 5)}}
	pl := &plan.Plan{Resources: []plan.Resource{vehicle("v", at(20, 0))}, Orders: []plan.Order{order("o1", s)}}
	p := compileLine(t, pl, Options{})
	ev := NewEvaluator(p).Eval(0, []int{0})
	if !ev.OK || ev.UnsatPref != 1 || ev.Delay != 5*60 {
		t.Errorf("ok=%v unsat=%d delay=%d, want a 5 minute delay", ev.OK, ev.UnsatPref, ev.Delay)
	}
}

func TestEvalSlidingBreak(t *testing.T) {
	v := vehicle("v", at(23, 0))
	v.Breaks = []plan.Break{{Type: "workingDurationSlidingBreak", MinBreakDuration: plan.Duration(30 * time.Minute), MaxInterBreakDuration: plan.Duration(time.Hour)}}
	pl := &plan.Plan{
		Resources: []plan.Resource{v},
		Orders: []plan.Order{
			order("o1", delivery("s1", 1, 15*time.Minute)),
			order("o2", delivery("s2", 2, 15*time.Minute)),
			order("o3", delivery("s3", 3, 15*time.Minute)),
		},
	}
	p := compileLine(t, pl, Options{})
	d := NewEvaluator(p).Detail(0, []int{0, 1, 2})
	if !d.Eval.OK {
		t.Fatalf("infeasible: %v", d.Eval.Fail)
	}
	var breaks int64
	for _, s := range d.Stops {
		breaks += s.BreakBefore
	}
	breaks += d.EndBreak
	if breaks != 30*60 {
		t.Errorf("breaks total %ds, want exactly one 30 minute break", breaks)
	}
	if d.Stops[2].BreakBefore != 30*60 {
		t.Errorf("the break belongs before the third leg, got %v", d.Stops)
	}
	if want := d.Eval.Work + 30*60; d.Eval.Elapsed != want {
		t.Errorf("elapsed %d, want work+break = %d", d.Eval.Elapsed, want)
	}
}

func TestEvalMaxWorkingDuration(t *testing.T) {
	v := vehicle("v", at(23, 0))
	v.MaxWorkingDuration = plan.Duration(30 * time.Minute)
	// 10 min out + 25 min service + 10 min back = 45 min of work > 30 min.
	pl := &plan.Plan{Resources: []plan.Resource{v}, Orders: []plan.Order{order("o1", delivery("s1", 1, 25*time.Minute))}}
	p := compileLine(t, pl, Options{})
	if ev := NewEvaluator(p).Eval(0, []int{0}); ev.OK || ev.Fail != FailMaxWork {
		t.Errorf("want FailMaxWork, got ok=%v fail=%v", ev.OK, ev.Fail)
	}
}

func pdOrder(id string, pk, dk int, qty float64) plan.Order {
	pu := plan.Stop{Type: "single", ID: "p-" + id, Position: pos(pk), Kind: plan.KindPickup, Capacities: map[string]float64{"w": qty}}
	de := plan.Stop{Type: "single", ID: "d-" + id, Position: pos(dk), Kind: plan.KindDelivery, Capacities: map[string]float64{"w": qty}}
	return plan.Order{ID: id, Stops: []plan.Stop{pu, de}, SuccessiveStops: true}
}

func TestEvalCapacityAndPrecedence(t *testing.T) {
	pl := &plan.Plan{
		Resources: []plan.Resource{vehicle("v", at(20, 0))},
		Orders:    []plan.Order{pdOrder("a", 1, 2, 6), pdOrder("b", 3, 4, 6)},
	}
	p := compileLine(t, pl, Options{})
	ev := NewEvaluator(p)
	pa, da, pb, db := nodeIdx(t, p, "p-a"), nodeIdx(t, p, "d-a"), nodeIdx(t, p, "p-b"), nodeIdx(t, p, "d-b")

	if r := ev.Eval(0, []int{pa, pb, da, db}); r.OK || r.Fail != FailCapacity {
		t.Errorf("carrying 12 > 10: want FailCapacity, got ok=%v fail=%v", r.OK, r.Fail)
	}
	if r := ev.Eval(0, []int{pa, da, pb, db}); !r.OK {
		t.Errorf("sequential trips fit the capacity, got fail=%v", r.Fail)
	}
	if r := ev.Eval(0, []int{da, pa, pb, db}); r.OK || r.Fail != FailOrder {
		t.Errorf("delivery before pickup: want FailOrder, got ok=%v fail=%v", r.OK, r.Fail)
	}
}

func TestUndeclaredCapacityIsZeroUnlessOptedIn(t *testing.T) {
	mk := func(opt Options) *Problem {
		d := vehicle("v", at(20, 0))
		d.Capacities = nil
		s := delivery("s1", 1, time.Minute)
		s.Capacities = map[string]float64{"w": 1}
		return compileLine(t, &plan.Plan{Resources: []plan.Resource{d}, Orders: []plan.Order{order("o1", s)}}, opt)
	}
	if ev := NewEvaluator(mk(Options{})).Eval(0, []int{0}); ev.OK || ev.Fail != FailCapacity {
		t.Errorf("default: want FailCapacity, got ok=%v fail=%v", ev.OK, ev.Fail)
	}
	if ev := NewEvaluator(mk(Options{UnlimitedUndeclaredCapacity: true})).Eval(0, []int{0}); !ev.OK {
		t.Errorf("opt-in: want feasible, got %v", ev.Fail)
	}
}

func TestSequentialOrderRespectsListedSequence(t *testing.T) {
	a, b, c := delivery("a", 1, 0), delivery("b", 2, 0), delivery("c", 3, 0)
	pl := &plan.Plan{
		Resources: []plan.Resource{vehicle("v", at(20, 0))},
		Orders:    []plan.Order{{ID: "o", Stops: []plan.Stop{a, b, c}, SuccessiveStops: true}},
	}
	p := compileLine(t, pl, Options{})
	ev := NewEvaluator(p)
	if r := ev.Eval(0, []int{0, 1, 2}); !r.OK {
		t.Errorf("listed order must be feasible: %v", r.Fail)
	}
	if r := ev.Eval(0, []int{0, 2, 1}); r.OK {
		t.Error("a sequential order visited out of sequence must be infeasible")
	}
}

func TestWorkedHoursCost(t *testing.T) {
	c := &plan.WorkedHoursCost{CostFloor: 8, OvercostFloor: 10, OvercostCoeff: 1.25}
	for _, tc := range []struct {
		hours float64
		want  int64
	}{{5, 8000}, {9, 9000}, {10, 10000}, {12, 12500}} {
		if got := (&plan.ResourceCost{WorkedHours: c}).Milli(plan.Billing{WorkSec: int64(tc.hours * 3600)}); got != tc.want {
			t.Errorf("cost(%vh) = %d, want %d", tc.hours, got, tc.want)
		}
	}
}

func TestVehicleCostAppliesOnlyWhenUsed(t *testing.T) {
	v := vehicle("v", at(20, 0))
	v.Cost = &plan.ResourceCost{WorkedHours: &plan.WorkedHoursCost{CostFloor: 8, OvercostFloor: 10, OvercostCoeff: 1.25}}
	pl := &plan.Plan{Resources: []plan.Resource{v}, Orders: []plan.Order{order("o1", delivery("s1", 1, time.Minute))}}
	p := compileLine(t, pl, Options{})
	ev := NewEvaluator(p)
	if r := ev.Eval(0, nil); r.Cost != 0 || !r.OK {
		t.Errorf("an idle vehicle is free, got %+v", r)
	}
	if r := ev.Eval(0, []int{0}); r.Cost != 8000 {
		t.Errorf("a used vehicle pays the floor, got %d", r.Cost)
	}
}

func TestGlobalConstraintLimitsVehiclesTouchingATag(t *testing.T) {
	mkStop := func(id string, k int) plan.Stop {
		s := delivery(id, k, time.Minute)
		s.Tags = []string{"key:set"}
		return s
	}
	gc := plan.GlobalConstraint{
		Type: "maxCumulatedCost", Maximum: 1, Name: "one-driver",
		CostsByResourceTag: map[string]plan.ResourceTagCosts{"driver": {CostsByStopTag: map[string]plan.StopTagCost{"key:set": {Using: 1}}}},
	}
	pl := &plan.Plan{
		Resources:         []plan.Resource{vehicle("v1", at(20, 0)), vehicle("v2", at(20, 0))},
		Orders:            []plan.Order{order("o1", mkStop("s1", 1)), order("o2", mkStop("s2", 2)), order("o3", delivery("s3", 3, time.Minute))},
		GlobalConstraints: []plan.GlobalConstraint{gc},
	}
	p := compileLine(t, pl, Options{})

	if len(p.Affinity) != 1 || len(p.Affinity[0]) != 2 {
		t.Fatalf("orders sharing the tag must form one affinity group, got %v", p.Affinity)
	}

	ev := NewEvaluator(p)
	together := []RouteEval{ev.Eval(0, []int{0, 1}), ev.Eval(1, []int{2})}
	if _, ok := p.Aggregate(together, 0); !ok {
		t.Error("one vehicle serving the whole tag must be allowed")
	}
	split := []RouteEval{ev.Eval(0, []int{0}), ev.Eval(1, []int{1})}
	if _, ok := p.Aggregate(split, 0); ok {
		t.Error("two vehicles touching the tag must violate the constraint")
	}
}

func TestCompileRepositoryPlan(t *testing.T) {
	pl, _, err := plan.Load("../testdata/plan.json")
	if err != nil {
		t.Fatal(err)
	}
	p, err := Compile(pl, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Vehicles) != 3 || len(p.Orders) != 22 || len(p.Nodes) != 32 {
		t.Errorf("vehicles=%d orders=%d nodes=%d", len(p.Vehicles), len(p.Orders), len(p.Nodes))
	}
	if len(p.Locs) != 23 {
		t.Errorf("unique locations = %d, want 23 (depot + 22 delivery points)", len(p.Locs))
	}
	if len(p.Profiles) != 2 {
		t.Errorf("profiles = %v, want driving-traffic and driving", p.Profiles)
	}
	if len(p.Affinity) != 1 || len(p.Affinity[0]) != 3 {
		t.Errorf("the three keychain orders must be bound together: %v", p.Affinity)
	}
	wantObj := []string{"maximizeMandatoryStops", "maximizePreferredStops", "minimizeDelay", "minimizeCosts", "minimizeDistance", "minimizeLargestTourDuration", "minimizeWorkingDuration"}
	got := p.ObjectiveOrder()
	for i := range wantObj {
		if got[i] != wantObj[i] {
			t.Fatalf("objective order = %v", got)
		}
	}
	// driver-1 declares no capacities: it must not be able to carry goods.
	if len(p.Dims) == 0 {
		t.Fatal("expected capacity dimensions")
	}
	for _, c := range p.Vehicles[2].Cap {
		if c != 0 {
			t.Errorf("driver-1 capacity = %v, want 0", p.Vehicles[2].Cap)
		}
	}
}

func TestWaitingCountsAsWorkingTime(t *testing.T) {
	early := delivery("s1", 1, 5*time.Minute)
	early.AuthorizedTimeWindows = []plan.TimeWindow{{Begin: at(6, 10), End: at(6, 20)}}
	late := delivery("s2", 2, 5*time.Minute)
	late.AuthorizedTimeWindows = []plan.TimeWindow{{Begin: at(9, 0), End: at(9, 30)}}

	mk := func(maxWork time.Duration) *Problem {
		v := vehicle("v", at(20, 0))
		v.MaxWorkingDuration = plan.Duration(maxWork)
		return compileLine(t, &plan.Plan{Resources: []plan.Resource{v}, Orders: []plan.Order{order("o1", early), order("o2", late)}}, Options{})
	}

	// The hard windows force about 2h45 of idling between the two stops, which
	// is working time: a 2 hour limit cannot be met, a 4 hour limit can.
	if ev := NewEvaluator(mk(2*time.Hour)).Eval(0, []int{0, 1}); ev.OK || ev.Fail != FailMaxWork {
		t.Errorf("a 2h limit must fail with FailMaxWork, got ok=%v fail=%v", ev.OK, ev.Fail)
	}
	ev := NewEvaluator(mk(4*time.Hour)).Eval(0, []int{0, 1})
	if !ev.OK {
		t.Fatalf("a 4h limit must be feasible: %v", ev.Fail)
	}
	if ev.Work <= ev.Active || ev.Wait < 2*3600 {
		t.Errorf("waiting must count as work: work=%d active=%d wait=%d", ev.Work, ev.Active, ev.Wait)
	}
}

func TestUnavoidableWaitingDropsAPreferenceNotTheRoute(t *testing.T) {
	early := delivery("s1", 1, 5*time.Minute)
	early.AuthorizedTimeWindows = []plan.TimeWindow{{Begin: at(6, 10), End: at(6, 20)}}
	late := delivery("s2", 2, 5*time.Minute)
	late.PreferredTimeWindows = []plan.TimeWindow{{Begin: at(9, 0), End: at(9, 30)}} // soft only

	v := vehicle("v", at(20, 0))
	v.MaxWorkingDuration = plan.Duration(2 * time.Hour)
	p := compileLine(t, &plan.Plan{Resources: []plan.Resource{v}, Orders: []plan.Order{order("o1", early), order("o2", late)}}, Options{})
	ev := NewEvaluator(p).Eval(0, []int{0, 1})
	if !ev.OK {
		t.Fatalf("the preference must be dropped so the route stays feasible, got %v", ev.Fail)
	}
	if ev.UnsatPref != 1 {
		t.Errorf("unsatisfied preferences = %d, want 1", ev.UnsatPref)
	}
}

func TestBreakRuleCountsWaitingTime(t *testing.T) {
	early := delivery("s1", 1, 5*time.Minute)
	early.AuthorizedTimeWindows = []plan.TimeWindow{{Begin: at(6, 10), End: at(6, 20)}}
	late := delivery("s2", 2, 5*time.Minute)
	late.AuthorizedTimeWindows = []plan.TimeWindow{{Begin: at(7, 40), End: at(8, 0)}}

	v := vehicle("v", at(20, 0))
	v.Breaks = []plan.Break{{Type: "workingDurationSlidingBreak", MinBreakDuration: plan.Duration(15 * time.Minute), MaxInterBreakDuration: plan.Duration(time.Hour)}}
	p := compileLine(t, &plan.Plan{Resources: []plan.Resource{v}, Orders: []plan.Order{order("o1", early), order("o2", late)}}, Options{})

	// 75 minutes of waiting at s2 alone exceeds the one hour allowed between breaks.
	if ev := NewEvaluator(p).Eval(0, []int{0, 1}); ev.OK || ev.Fail != FailBreak {
		t.Errorf("want FailBreak, got ok=%v fail=%v", ev.OK, ev.Fail)
	}
}

func TestEvalCostUsesWorkingTimeIncludingWaiting(t *testing.T) {
	early := delivery("s1", 1, 5*time.Minute)
	early.AuthorizedTimeWindows = []plan.TimeWindow{{Begin: at(6, 10), End: at(6, 20)}}
	late := delivery("s2", 2, 5*time.Minute)
	late.AuthorizedTimeWindows = []plan.TimeWindow{{Begin: at(15, 0), End: at(15, 30)}}

	v := vehicle("v", at(23, 0))
	v.Cost = &plan.ResourceCost{WorkedHours: &plan.WorkedHoursCost{CostFloor: 1, OvercostFloor: 100, OvercostCoeff: 1}}
	p := compileLine(t, &plan.Plan{Resources: []plan.Resource{v}, Orders: []plan.Order{order("o1", early), order("o2", late)}}, Options{})
	ev := NewEvaluator(p).Eval(0, []int{0, 1})
	if !ev.OK {
		t.Fatal(ev.Fail)
	}
	if want := v.Cost.Milli(plan.Billing{DistM: ev.Dist, WorkSec: ev.Work, Orders: 1}); ev.Cost != want || ev.Cost < 8000 {
		t.Errorf("cost %d should price the ~9 hours of working time (waiting included), want %d", ev.Cost, want)
	}
}

func TestResourceCostCombinesAllParts(t *testing.T) {
	c := &plan.ResourceCost{Fixed: 200, PerKm: 2.5, PerHour: 30}
	// 200 fixed + 12.345 km x 2.5 + 2 h x 30 = 200 + 30.8625 + 60 = 290.8625
	if got := c.Milli(plan.Billing{DistM: 12345, WorkSec: 2 * 3600}); got != 290863 {
		t.Errorf("cost = %d milli, want 290863", got)
	}
	if got := (&plan.ResourceCost{}).Milli(plan.Billing{DistM: 5000, WorkSec: 3600}); got != 0 {
		t.Errorf("an unpriced vehicle must be free, got %d", got)
	}
}

func TestEvalChargesFixedDistanceAndTimeCost(t *testing.T) {
	v := vehicle("v", at(20, 0))
	v.Cost = &plan.ResourceCost{Fixed: 100, PerKm: 2, PerHour: 12}
	p := compileLine(t, &plan.Plan{Resources: []plan.Resource{v}, Orders: []plan.Order{order("o1", delivery("s1", 1, 10*time.Minute))}}, Options{})
	ev := NewEvaluator(p)
	if r := ev.Eval(0, nil); r.Cost != 0 {
		t.Errorf("an unused vehicle pays nothing, got %d", r.Cost)
	}
	r := ev.Eval(0, []int{0})
	// Out and back: 10 km each way (5000 m per index step x 2 hops) and
	// 10 + 10 + 10 minutes of work.
	want := v.Cost.Milli(plan.Billing{DistM: r.Dist, WorkSec: r.Work, Orders: 1})
	if r.Cost != want || r.Cost <= 100_000 {
		t.Errorf("cost = %d, want %d (above the fixed 100)", r.Cost, want)
	}
}

// lunch is a fixed rest from 08:00 to 09:00, two hours after the vehicles open.
func lunch() plan.Break {
	return plan.Break{Type: plan.BreakFixed, Begin: at(8, 0), End: at(9, 0)}
}

// far returns orders that put the stop "s1" at location k, which compileLine
// places 10*k minutes from the depot; locations are numbered in order of
// appearance, so the orders before it only exist to take the lower numbers.
func far(k int, svc time.Duration) []plan.Order {
	var orders []plan.Order
	for i := 1; i < k; i++ {
		id := fmt.Sprintf("pad%d", i)
		orders = append(orders, order(id, delivery(id, i, time.Minute)))
	}
	return append(orders, order("o1", delivery("s1", k, svc)))
}

func TestFixedBreakSuspendsDrivingAcrossIt(t *testing.T) {
	v := vehicle("v", at(20, 0))
	v.Breaks = []plan.Break{lunch()}
	// Location 15 is 150 minutes away: the leg would end at 08:30, inside the break.
	pl := &plan.Plan{Resources: []plan.Resource{v}, Orders: far(15, 5*time.Minute)}
	p := compileLine(t, pl, Options{})
	d := NewEvaluator(p).Detail(0, []int{nodeIdx(t, p, "s1")})
	if !d.Eval.OK {
		t.Fatalf("infeasible: %v", d.Eval.Fail)
	}
	st := d.Stops[0]
	if want := p.Seconds(at(9, 30)); st.Arrival != want {
		t.Errorf("arrival %s, want 09:30 (150 min of driving plus the one hour break)", p.Time(st.Arrival).Format("15:04"))
	}
	if st.BreakBefore != 3600 || d.EndBreak != 0 {
		t.Errorf("break before the stop = %ds, at the end = %ds, want 3600 and 0", st.BreakBefore, d.EndBreak)
	}
	// 06:00 to 12:05 elapsed, of which one hour is rest.
	if d.Eval.Elapsed != 6*3600+5*60 || d.Eval.Work != 5*3600+5*60 {
		t.Errorf("elapsed %d, work %d", d.Eval.Elapsed, d.Eval.Work)
	}
}

func TestFixedBreakDelaysAServiceThatWouldOverlapIt(t *testing.T) {
	v := vehicle("v", at(20, 0))
	v.Breaks = []plan.Break{lunch()}
	// 110 minutes of driving reach location 11 at 07:50. The 20 minute service would run into the break.
	pl := &plan.Plan{Resources: []plan.Resource{v}, Orders: far(11, 20*time.Minute)}
	p := compileLine(t, pl, Options{})
	d := NewEvaluator(p).Detail(0, []int{nodeIdx(t, p, "s1")})
	if !d.Eval.OK {
		t.Fatalf("infeasible: %v", d.Eval.Fail)
	}
	st := d.Stops[0]
	if want := p.Seconds(at(9, 0)); st.Begin != want {
		t.Errorf("service starts %s, want 09:00 right after the break", p.Time(st.Begin).Format("15:04"))
	}
	// Leaving ten minutes later reaches the stop exactly when the break begins, so nothing is wasted waiting.
	if st.Wait != 0 || d.Depart != p.Seconds(at(6, 10)) {
		t.Errorf("wait %ds, depart %s: the departure should shift until the wait disappears", st.Wait, p.Time(d.Depart).Format("15:04"))
	}
	if st.BreakBefore != 3600 || d.Eval.Work != d.Eval.Elapsed-3600 {
		t.Errorf("rest %ds, work %d, elapsed %d", st.BreakBefore, d.Eval.Work, d.Eval.Elapsed)
	}
}

func TestFixedBreakDoesNotAffectToursThatAvoidIt(t *testing.T) {
	v := vehicle("v", at(20, 0))
	v.Breaks = []plan.Break{lunch()}
	pl := &plan.Plan{Resources: []plan.Resource{v}, Orders: []plan.Order{order("o1", delivery("s1", 1, 5*time.Minute))}}
	p := compileLine(t, pl, Options{})
	ev := NewEvaluator(p).Eval(0, []int{0})
	if !ev.OK || ev.Work != ev.Elapsed || ev.Elapsed != 25*60 {
		t.Errorf("a 25 minute tour before the break must be untouched: %+v", ev)
	}
}

func TestFixedBreakMakesTightWindowsInsideItUnreachable(t *testing.T) {
	v := vehicle("v", at(20, 0))
	v.Breaks = []plan.Break{lunch()}
	inside := delivery("s1", 1, 5*time.Minute)
	inside.AuthorizedTimeWindows = []plan.TimeWindow{{Begin: at(8, 10), End: at(8, 50)}}
	after := delivery("s2", 2, 5*time.Minute)
	after.AuthorizedTimeWindows = []plan.TimeWindow{{Begin: at(9, 0), End: at(10, 0)}}
	pl := &plan.Plan{Resources: []plan.Resource{v}, Orders: []plan.Order{order("o1", inside), order("o2", after)}}
	p := compileLine(t, pl, Options{})
	e := NewEvaluator(p)
	if ev := e.Eval(0, []int{nodeIdx(t, p, "s1")}); ev.OK || ev.Fail != FailWindow {
		t.Errorf("a window that lies entirely inside the break must be missed, got %+v", ev)
	}
	d := e.Detail(0, []int{nodeIdx(t, p, "s2")})
	if !d.Eval.OK || d.Stops[0].Begin != p.Seconds(at(9, 0)) {
		t.Errorf("a window opening when the break ends must work: %+v", d.Eval)
	}
	// After shifting its departure the vehicle reaches the stop at 08:00, when
	// the break starts, and the time spent waiting through the break is rest,
	// not idleness.
	if st := d.Stops[0]; st.Wait != 0 || st.BreakBefore != 3600 {
		t.Errorf("wait %ds, rest %ds, want 0 and 3600", st.Wait, st.BreakBefore)
	}
}

func TestFixedBreakAndSlidingBreakAreExclusive(t *testing.T) {
	v := vehicle("v", at(20, 0))
	v.Breaks = []plan.Break{lunch(), {Type: plan.BreakSliding, MinBreakDuration: plan.Duration(30 * time.Minute), MaxInterBreakDuration: plan.Duration(time.Hour)}}
	pl := &plan.Plan{Resources: []plan.Resource{v}, Orders: []plan.Order{order("o1", delivery("s1", 1, 5*time.Minute))}}
	p := compileLine(t, pl, Options{})
	if p.Vehicles[0].Fixed == nil || p.Vehicles[0].Break != nil || len(p.Warnings) == 0 {
		t.Errorf("only the first rule applies and the other is reported: fixed=%v sliding=%v warnings=%v", p.Vehicles[0].Fixed, p.Vehicles[0].Break, p.Warnings)
	}
}

func TestFixedBreakAdvance(t *testing.T) {
	f := &FixedBreak{Begin: 100, End: 200}
	for _, c := range []struct{ t, d, arrive, rest int64 }{
		{0, 50, 50, 0},     // done before the break
		{0, 100, 100, 0},   // arrives exactly when the break begins
		{0, 150, 250, 100}, // pauses for the whole break
		{150, 30, 230, 50}, // sets off during the break and waits for its end
		{200, 30, 230, 0},  // after the break
		{150, 0, 150, 0},   // not moving costs nothing
	} {
		if arrive, rest := f.advance(c.t, c.d); arrive != c.arrive || rest != c.rest {
			t.Errorf("advance(%d, %d) = %d, %d; want %d, %d", c.t, c.d, arrive, rest, c.arrive, c.rest)
		}
	}
}

func pickup(id string, k int, qty float64) plan.Stop {
	return plan.Stop{Type: "single", ID: id, Position: pos(k), Kind: plan.KindPickup, OperationDuration: plan.Duration(5 * time.Minute),
		Capacities: map[string]float64{"w": qty}}
}

// tripVehicle holds 10 units, may make several trips, and needs 5 minutes at the depot between them.
func tripVehicle() plan.Resource {
	v := vehicle("v", at(23, 0))
	v.MultiTrip, v.TurnaroundDuration = true, plan.Duration(5*time.Minute)
	return v
}

func TestMultiTripReturnsToTheDepotWhenFull(t *testing.T) {
	orders := []plan.Order{order("o1", pickup("s1", 1, 6)), order("o2", pickup("s2", 2, 6)), order("o3", pickup("s3", 3, 6))}
	single := vehicle("v", at(23, 0))
	ps := compileLine(t, &plan.Plan{Resources: []plan.Resource{single}, Orders: orders}, Options{})
	if ev := NewEvaluator(ps).Eval(0, []int{0, 1, 2}); ev.OK || ev.Fail != FailCapacity {
		t.Fatalf("18 units do not fit a vehicle that holds 10 and makes one trip: %+v", ev)
	}

	p := compileLine(t, &plan.Plan{Resources: []plan.Resource{tripVehicle()}, Orders: orders}, Options{})
	d := NewEvaluator(p).Detail(0, []int{0, 1, 2})
	if !d.Eval.OK {
		t.Fatalf("infeasible: %v", d.Eval.Fail)
	}
	if d.Trips != 3 || d.Stops[0].Reload || !d.Stops[1].Reload || !d.Stops[2].Reload {
		t.Errorf("trips %d, reloads %v %v %v, want a trip per stop", d.Trips, d.Stops[0].Reload, d.Stops[1].Reload, d.Stops[2].Reload)
	}
	// 10 min out and 5 min of service; then 10 min back, 5 min unloading and 20 min to the second stop
	// (the depot is index 0, each index step takes 600 s); then 20 back, 5 unloading and 30 out; 30 home.
	if d.Stops[1].ReloadArrive != 1500 || d.Stops[1].ReloadDepart != 1800 || d.Stops[1].Arrival != 3000 {
		t.Errorf("second trip: at the depot %d-%d, at the stop %d", d.Stops[1].ReloadArrive, d.Stops[1].ReloadDepart, d.Stops[1].Arrival)
	}
	if d.Eval.Elapsed != 8700 || d.Eval.Dist != 60000 {
		t.Errorf("elapsed %d s, distance %d m, want 8700 s and 60000 m", d.Eval.Elapsed, d.Eval.Dist)
	}
	if d.Stops[1].TravelSec != 600+1200 || d.Stops[1].TravelM != 5000+10000 {
		t.Errorf("the leg to the second stop goes through the depot: %d s, %d m", d.Stops[1].TravelSec, d.Stops[1].TravelM)
	}
	if d.Stops[1].Load[0] != 6 || d.Stops[2].Load[0] != 6 {
		t.Errorf("the load restarts at every trip: %v %v", d.Stops[1].Load, d.Stops[2].Load)
	}
}

func TestMultiTripStaysOneTripWhenEverythingFits(t *testing.T) {
	orders := []plan.Order{order("o1", pickup("s1", 1, 3)), order("o2", pickup("s2", 2, 3))}
	multi := compileLine(t, &plan.Plan{Resources: []plan.Resource{tripVehicle()}, Orders: orders}, Options{})
	single := compileLine(t, &plan.Plan{Resources: []plan.Resource{vehicle("v", at(23, 0))}, Orders: orders}, Options{})
	a, b := NewEvaluator(multi).Detail(0, []int{0, 1}), NewEvaluator(single).Detail(0, []int{0, 1})
	if a.Trips != 1 || a.Eval.Elapsed != b.Eval.Elapsed || a.Eval.Dist != b.Eval.Dist {
		t.Errorf("a vehicle that never fills up behaves as without multiTrip: %+v vs %+v", a.Eval, b.Eval)
	}
}

func TestMultiTripNeverSplitsAnOrder(t *testing.T) {
	// Order A is picked up at location 1 and delivered at location 3, order B is picked up at 2.
	// Carrying both takes 12 units, but the only place to unload would be in the middle of A.
	a := plan.Order{ID: "a", SuccessiveStops: true, Stops: []plan.Stop{pickup("ap", 1, 6), delivery("ad", 3, 5*time.Minute)}}
	a.Stops[1].Capacities = map[string]float64{"w": 6}
	b := order("b", pickup("bp", 2, 6))
	p := compileLine(t, &plan.Plan{Resources: []plan.Resource{tripVehicle()}, Orders: []plan.Order{a, b}}, Options{})
	e := NewEvaluator(p)
	ap, ad, bp := nodeIdx(t, p, "ap"), nodeIdx(t, p, "ad"), nodeIdx(t, p, "bp")
	if ev := e.Eval(0, []int{ap, bp, ad}); ev.OK || ev.Fail != FailCapacity {
		t.Errorf("unloading between A's pickup and delivery would lose A's goods: %+v", ev)
	}
	if d := e.Detail(0, []int{ap, ad, bp}); !d.Eval.OK || d.Trips != 1 {
		t.Errorf("delivering A first leaves room for B: %+v", d.Eval)
	}
}

func TestMultiTripLoadsDeliveriesPerTrip(t *testing.T) {
	// Two deliveries of 6 units that start at the depot: each trip takes one.
	orders := []plan.Order{order("o1", delivery("s1", 1, 5*time.Minute)), order("o2", delivery("s2", 2, 5*time.Minute))}
	orders[0].Stops[0].Capacities, orders[1].Stops[0].Capacities = map[string]float64{"w": 6}, map[string]float64{"w": 6}
	p := compileLine(t, &plan.Plan{Resources: []plan.Resource{tripVehicle()}, Orders: orders}, Options{})
	d := NewEvaluator(p).Detail(0, []int{0, 1})
	if !d.Eval.OK || d.Trips != 2 || d.InitialLoad[0] != 6 || d.Stops[0].Load[0] != 0 || d.Stops[1].Load[0] != 0 {
		t.Errorf("trips %d, initial load %v, loads %v %v: %+v", d.Trips, d.InitialLoad, d.Stops[0].Load, d.Stops[1].Load, d.Eval)
	}
}

func TestMultiTripCannotCarryWhatNoTripCan(t *testing.T) {
	p := compileLine(t, &plan.Plan{Resources: []plan.Resource{tripVehicle()}, Orders: []plan.Order{order("o1", pickup("s1", 1, 11))}}, Options{})
	if ev := NewEvaluator(p).Eval(0, []int{0}); ev.OK || ev.Fail != FailCapacity {
		t.Errorf("a single order above the capacity fits no trip: %+v", ev)
	}
}

func TestMultiTripWithAFixedBreak(t *testing.T) {
	v := tripVehicle()
	v.Breaks = []plan.Break{lunch()}
	orders := []plan.Order{order("o1", pickup("s1", 1, 6)), order("o2", pickup("s2", 2, 6)), order("o3", pickup("s3", 3, 6))}
	p := compileLine(t, &plan.Plan{Resources: []plan.Resource{v}, Orders: orders}, Options{})
	// The same three trips as without a break, which end 8700 s after 06:00, at 08:25.
	// The drive home starts at 07:55 and is paused from 08:00 to 09:00.
	d := NewEvaluator(p).Detail(0, []int{0, 1, 2})
	if !d.Eval.OK || d.Trips != 3 {
		t.Fatalf("%+v", d.Eval)
	}
	if d.EndBreak != 3600 || d.Eval.Work != 8700 || d.Eval.Elapsed != 8700+3600 {
		t.Errorf("rest before the end %d s, work %d s, elapsed %d s, want 3600, 8700 and 12300", d.EndBreak, d.Eval.Work, d.Eval.Elapsed)
	}
}

func TestMultiTripPlanFields(t *testing.T) {
	pl, _, err := plan.Parse([]byte(`{"resources":[{"id":"m","multiTrip":true,"turnaroundDuration":"PT10M","departure":{"lon":1,"lat":2}},
		{"id":"n","turnaroundDuration":"PT5M","departure":{"lon":1,"lat":2}}],
		"orders":[{"id":"o","stops":[{"type":"single","id":"s","kind":"pickup","position":{"lon":1.1,"lat":2.1}}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !pl.Resources[0].MultiTrip || pl.Resources[0].TurnaroundDuration.Std() != 10*time.Minute {
		t.Errorf("not parsed: %+v", pl.Resources[0])
	}
	p, err := Compile(pl, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Vehicles[0].MultiTrip || p.Vehicles[0].Turnaround != 600 || p.Vehicles[1].MultiTrip || p.Vehicles[1].Turnaround != 0 {
		t.Errorf("compiled: %+v %+v", p.Vehicles[0], p.Vehicles[1])
	}
	var reported int
	for _, w := range p.Warnings {
		if strings.Contains(w, "turnaroundDuration") {
			reported++
			if !strings.Contains(w, `"n"`) {
				t.Errorf("the warning should name the resource: %s", w)
			}
		}
	}
	if reported != 1 {
		t.Errorf("a turnaround without multiTrip should be reported once: %v", p.Warnings)
	}
}
