package model

import (
	"strings"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/engine/plan"
)

func TestOpenRouteEndsAtTheLastStop(t *testing.T) {
	build := func(open bool) RouteEval {
		v := vehicle("v", at(20, 0))
		v.OpenRoute = open
		pl := &plan.Plan{
			Resources: []plan.Resource{v},
			Orders: []plan.Order{
				order("o1", delivery("s1", 1, 5*time.Minute)),
				order("o2", delivery("s2", 2, 5*time.Minute)),
			},
		}
		p := compileLine(t, pl, Options{})
		return NewEvaluator(p).Eval(0, []int{0, 1})
	}
	closed, open := build(false), build(true)
	if !closed.OK || !open.OK {
		t.Fatalf("infeasible: %v %v", closed.Fail, open.Fail)
	}
	// 10 min to s1, 5 service, 10 to s2, 5 service; the closed route drives 20 min back.
	if want := int64(30 * 60); open.Work != want || open.Elapsed != want || open.Arrive-open.Depart != want {
		t.Errorf("open route: work=%d elapsed=%d, want %d", open.Work, open.Elapsed, want)
	}
	if open.Dist != 10000 || closed.Dist != 20000 {
		t.Errorf("distance: open=%d closed=%d, want 10000 and 20000", open.Dist, closed.Dist)
	}
}

// tariffPlan is one pickup-and-delivery order of 12 kg priced like the loong motorbike tariff.
func tariffPlan(free bool) *plan.Plan {
	v := vehicle("v", at(20, 0))
	v.OpenRoute = true
	v.Capacities = map[string]float64{"w": 20}
	v.Cost = &plan.ResourceCost{
		Fixed: 6000, PerKm: 1800, PerHour: 9000, PerOrder: 2500,
		Surcharge:    &plan.LoadSurcharge{Dimension: "w", Above: 10, PerUnit: 800},
		FreeApproach: free,
	}
	pick := plan.Stop{Type: "single", ID: "p1", Position: pos(1), Kind: plan.KindPickup, OperationDuration: plan.Duration(4 * time.Minute), Capacities: map[string]float64{"w": 12}}
	drop := plan.Stop{Type: "single", ID: "d1", Position: pos(3), Kind: plan.KindDelivery, OperationDuration: plan.Duration(3 * time.Minute), Capacities: map[string]float64{"w": 12}}
	return &plan.Plan{Resources: []plan.Resource{v}, Orders: []plan.Order{{ID: "o1", Stops: []plan.Stop{pick, drop}, SuccessiveStops: true}}}
}

func TestTariffBillsTheRouteTheWayLoongPricesIt(t *testing.T) {
	// Legs: 5 km (10 min) to the pickup, 5 km (10 min) on to the drop, 4 + 3 min of service: 10 km, 27 min.
	for _, tc := range []struct {
		name string
		free bool
		want int64 // milli cost units
	}{
		// 6000 + 1800*10 + 9000*(27/60) + 2500 + 800*(12-10), all in Rp.
		{"approach billed", false, 6_000_000 + 18_000_000 + 4_050_000 + 2_500_000 + 1_600_000},
		// The 5 km and 10 min to the pickup are free: 1800*5 and 9000*(17/60).
		{"approach free", true, 6_000_000 + 9_000_000 + 2_550_000 + 2_500_000 + 1_600_000},
	} {
		p := compileLine(t, tariffPlan(tc.free), Options{})
		ev := NewEvaluator(p).Eval(0, []int{0, 1})
		if !ev.OK {
			t.Fatalf("%s: infeasible: %v", tc.name, ev.Fail)
		}
		if ev.Cost != tc.want {
			t.Errorf("%s: cost = %d milli, want %d", tc.name, ev.Cost, tc.want)
		}
		// The distance objective follows the bill: it leaves out the free approach.
		wantBilled := int64(10000)
		if tc.free {
			wantBilled = 5000
		}
		if ev.Dist != 10000 || ev.Billed != wantBilled {
			t.Errorf("%s: driven %d m, billed %d m, want 10000 and %d", tc.name, ev.Dist, ev.Billed, wantBilled)
		}
		s := NewSolution(p)
		s.Routes[0], s.Unassigned = []int{0, 1}, nil
		s.Recompute(NewEvaluator(p))
		if s.M[ObjDist] != wantBilled {
			t.Errorf("%s: the distance objective is %d m, want the billed %d", tc.name, s.M[ObjDist], wantBilled)
		}
	}
}

func TestSurchargeOnlyAppliesAboveTheThreshold(t *testing.T) {
	c := &plan.ResourceCost{Surcharge: &plan.LoadSurcharge{Dimension: "w", Above: 10, PerUnit: 800}}
	for load, want := range map[float64]int64{0: 0, 10: 0, 10.5: 400_000, 12: 1_600_000} {
		if got := c.Milli(plan.Billing{Load: load}); got != want {
			t.Errorf("surcharge(%v kg) = %d, want %d", load, got, want)
		}
	}
}

func TestSumArrivalAndBlendFollowTheDeliveries(t *testing.T) {
	v := vehicle("v", at(20, 0))
	v.OpenRoute = true
	pl := &plan.Plan{
		Resources:  []plan.Resource{v},
		Orders:     []plan.Order{order("o1", delivery("s1", 1, 5*time.Minute)), order("o2", delivery("s2", 2, 5*time.Minute))},
		Objectives: []string{"maximizeMandatoryStops", "minimizeBlend"},
		Blend:      &plan.Blend{Cost: 1, SumArrival: 20, Dist: 3, Makespan: 10},
	}
	p := compileLine(t, pl, Options{})
	s := NewSolution(p)
	s.Routes[0], s.Unassigned = []int{0, 1}, nil
	s.Recompute(NewEvaluator(p))
	if !s.Feasible {
		t.Fatal("infeasible")
	}
	// Arrivals 10 min and 10+5+10 = 25 min after the departure.
	if got, want := s.M[ObjSumArrival], int64(35*60); got != want {
		t.Errorf("sum of arrivals = %d s, want %d", got, want)
	}
	if got, want := s.M[ObjBlend], 20*s.M[ObjSumArrival]+3*s.M[ObjDist]+10*s.M[ObjMakespan]; got != want {
		t.Errorf("blend = %d, want %d", got, want)
	}
	// A route that reaches the near stop last has larger arrival times.
	worse := NewSolution(p)
	worse.Routes[0], worse.Unassigned = []int{1, 0}, nil
	worse.Recompute(NewEvaluator(p))
	if !p.Less(&s.M, &worse.M) {
		t.Errorf("visiting the near stop first must be better: %v vs %v", s.M, worse.M)
	}
}

func TestCompileRejectsIncompleteLoongSettings(t *testing.T) {
	v := vehicle("v", at(20, 0))
	base := func() *plan.Plan {
		return &plan.Plan{Resources: []plan.Resource{v}, Orders: []plan.Order{order("o1", delivery("s1", 1, time.Minute))}}
	}
	blend := base()
	blend.Objectives = []string{"maximizeMandatoryStops", "minimizeBlend"}
	if _, err := Compile(blend, Options{}); err == nil || !strings.Contains(err.Error(), "blend") {
		t.Errorf("minimizeBlend without weights must be rejected, got %v", err)
	}
	dim := base()
	dim.Resources[0].Cost = &plan.ResourceCost{Surcharge: &plan.LoadSurcharge{Dimension: "nope", Above: 1, PerUnit: 1}}
	if _, err := Compile(dim, Options{}); err == nil || !strings.Contains(err.Error(), "surcharge") {
		t.Errorf("a surcharge on an unknown dimension must be rejected, got %v", err)
	}
}
