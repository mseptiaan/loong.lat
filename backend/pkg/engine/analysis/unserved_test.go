package analysis

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/engine/model"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/plan"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/testutil"
)

var day = time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)

func at(h, m int) time.Time {
	return day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
}

// speed is 10 m/s, so that a leg of d meters takes d/10 seconds.
const speed = 36.0

var depot = plan.Position{Lon: -75, Lat: 45}

// lat places a stop that many degrees north of the depot: 0.1 degrees is about
// 15 km on the estimated road network, or 25 minutes at the test speed.
func lat(deg float64) plan.Position { return plan.Position{Lon: -75, Lat: 45 + deg} }

func vehicle(id string, begin, end time.Time, capacity float64, skills ...string) plan.Resource {
	return plan.Resource{
		ID: id, VehicleProfile: plan.VehicleProfile{Type: "van", AvgSpeedKmh: speed},
		Departure: &depot, Arrival: &depot, Skills: skills,
		WorkingTimeWindow: &plan.TimeWindow{Begin: begin, End: end},
		Capacities:        map[string]float64{"weight": capacity},
	}
}

// pickup is a single-stop order. windows are start windows as hour, minute pairs.
func pickup(id string, where plan.Position, weight float64, service time.Duration, skills []string, windows ...[4]int) plan.Order {
	st := plan.Stop{
		Type: "single", ID: "s-" + id, Kind: plan.KindPickup, Position: where,
		OperationDuration: plan.Duration(service), Capacities: map[string]float64{"weight": weight},
	}
	for _, w := range windows {
		st.AuthorizedTimeWindows = append(st.AuthorizedTimeWindows, plan.TimeWindow{Begin: at(w[0], w[1]), End: at(w[2], w[3])})
	}
	return plan.Order{ID: id, Stops: []plan.Stop{st}, RequiredSkills: skills}
}

func compile(t *testing.T, resources []plan.Resource, orders ...plan.Order) *model.Problem {
	t.Helper()
	pl := &plan.Plan{
		TZ: "UTC", Resources: resources, Orders: orders,
		Objectives: []string{"maximizeMandatoryStops", "minimizeDistance"},
	}
	p, err := model.Compile(pl, model.Options{})
	if err != nil {
		t.Fatal(err)
	}
	n, _, estDist := testutil.EstimateMatrix(p.Locs)
	dur := make([]float64, len(estDist))
	for i, d := range estDist {
		dur[i] = d / (speed / 3.6)
	}
	mats := make([]*model.Matrix, len(p.Profiles))
	for i := range mats {
		mats[i] = model.NewMatrix(n, dur, estDist, "estimate")
	}
	if err := p.SetMatrices(mats); err != nil {
		t.Fatal(err)
	}
	return p
}

// leftOut returns the explanation of the order with the given index when the
// vehicles serve the given orders (by index, per vehicle) and that one is left out.
func leftOut(t *testing.T, p *model.Problem, order int, served ...[]int) Unserved {
	t.Helper()
	s := model.NewSolution(p)
	s.Unassigned = s.Unassigned[:0]
	for o := range p.Orders {
		placed := false
		for v, orders := range served {
			for _, so := range orders {
				if so == o {
					s.Routes[v] = append(s.Routes[v], p.Orders[o].Nodes...)
					placed = true
				}
			}
		}
		if !placed {
			s.Unassigned = append(s.Unassigned, o)
		}
	}
	s.Recompute(model.NewEvaluator(p))
	for _, u := range explainUnserved(p, s, model.NewEvaluator(p)) {
		if u.Order == p.Orders[order].ID {
			return u
		}
	}
	t.Fatalf("order %d is not unserved", order)
	return Unserved{}
}

func wantCause(t *testing.T, u Unserved, cause string, mentions ...string) {
	t.Helper()
	if u.Cause != cause {
		t.Errorf("cause %q, want %q; reason: %s", u.Cause, cause, u.Reason)
	}
	for _, m := range mentions {
		if !strings.Contains(u.Reason, m) {
			t.Errorf("the reason should mention %q: %s", m, u.Reason)
		}
	}
	if len(u.Reasons) == 0 && cause != CauseNotPlaced {
		t.Errorf("the technical breakdown is missing")
	}
}

func TestUnservedNoVehicleHasTheSkill(t *testing.T) {
	p := compile(t, []plan.Resource{vehicle("v", at(7, 0), at(18, 0), 100)},
		pickup("o1", lat(0.05), 5, 5*time.Minute, []string{"cold"}))
	wantCause(t, leftOut(t, p, 0), CauseNoSkill, `"cold"`)
}

func TestUnservedTooHeavyForEveryVehicle(t *testing.T) {
	p := compile(t, []plan.Resource{vehicle("v", at(7, 0), at(18, 0), 10)},
		pickup("o1", lat(0.05), 50, 5*time.Minute, nil))
	wantCause(t, leftOut(t, p, 0), CauseTooHeavy, "50 weight", "10 weight")
}

func TestUnservedWindowInsideTheFixedBreak(t *testing.T) {
	v := vehicle("v", at(7, 0), at(18, 0), 100)
	v.Breaks = []plan.Break{{Type: plan.BreakFixed, Begin: at(12, 0), End: at(13, 0)}}
	// Open 12:00-12:50 with a 10 minute service: a start before 12:00 is too early and every later one is in the break.
	p := compile(t, []plan.Resource{v}, pickup("o1", lat(0.05), 5, 10*time.Minute, nil, [4]int{12, 0, 12, 40}))
	wantCause(t, leftOut(t, p, 0), CauseWindowInBreak, "12:00–12:40", "jam istirahat 12:00–13:00")
}

func TestUnservedWindowThatNoVehicleCanReach(t *testing.T) {
	// About 45 minutes away, but it closes at 07:05 and the shift opens at 07:00.
	p := compile(t, []plan.Resource{vehicle("v", at(7, 0), at(18, 0), 100)},
		pickup("o1", lat(0.18), 5, 5*time.Minute, nil, [4]int{7, 0, 7, 5}))
	wantCause(t, leftOut(t, p, 0), CauseWindowUnreachable, "07:00–07:05", "paling cepat baru tiba pukul 07:4")
}

func TestUnservedShiftEndsBeforeTheDriverIsBack(t *testing.T) {
	// It opens at 17:00 and is 80 minutes from the depot, so the van is home after 18:00.
	p := compile(t, []plan.Resource{vehicle("v", at(7, 0), at(18, 0), 100)},
		pickup("o1", lat(0.33), 5, 10*time.Minute, nil, [4]int{17, 0, 17, 45}))
	wantCause(t, leftOut(t, p, 0), CauseShiftTooShort, "akhir shift pukul 18:00", "melewati")
}

func TestUnservedDriverHasNoTimeLeft(t *testing.T) {
	// Five hours of shift; each pickup takes 70 minutes and the trip is 25 minutes each way.
	// Three of them fit (about 4 h 20), a fourth would take it past 12:00.
	p := compile(t, []plan.Resource{vehicle("van-1", at(7, 0), at(12, 0), 1000)},
		pickup("a", lat(0.1), 5, 70*time.Minute, nil),
		pickup("b", lat(0.1), 5, 70*time.Minute, nil),
		pickup("c", lat(0.1), 5, 70*time.Minute, nil),
		pickup("d", lat(0.1), 5, 70*time.Minute, nil))
	u := leftOut(t, p, 3, []int{0, 1, 2})
	wantCause(t, u, CauseDriverTimeFull, "Waktu pengemudi habis", "Satu-satunya kendaraan", "(van-1)", "melewati akhir shift pukul 12:00")
}

func TestUnservedVehicleIsFull(t *testing.T) {
	p := compile(t, []plan.Resource{vehicle("truck", at(7, 0), at(18, 0), 30)},
		pickup("a", lat(0.05), 10, 5*time.Minute, nil),
		pickup("b", lat(0.06), 10, 5*time.Minute, nil),
		pickup("c", lat(0.07), 15, 5*time.Minute, nil))
	wantCause(t, leftOut(t, p, 2, []int{0, 1}), CauseVehicleFull, "Kapasitas penuh", "(truck)", "20 dari 30 weight", "butuh 15 weight")
}

func TestUnservedEveryPositionBreaksAWindow(t *testing.T) {
	// X can start 08:00-08:05 and D 08:10-08:15, both 30 minutes long at the same place:
	// before X, X starts too late; after X, D does.
	p := compile(t, []plan.Resource{vehicle("v", at(7, 0), at(18, 0), 100)},
		pickup("x", lat(0.02), 5, 30*time.Minute, nil, [4]int{8, 0, 8, 5}),
		pickup("d", lat(0.02), 5, 30*time.Minute, nil, [4]int{8, 10, 8, 15}))
	wantCause(t, leftOut(t, p, 1, []int{0}), CauseScheduleConflict, "jendela waktunya")
}

func TestUnservedThatStillFitsIsSaidSo(t *testing.T) {
	p := compile(t, []plan.Resource{vehicle("v", at(7, 0), at(18, 0), 100)},
		pickup("o1", lat(0.05), 5, 5*time.Minute, nil))
	wantCause(t, leftOut(t, p, 0), CauseNotPlaced, "masih muat", "v")
}

func TestUnservedStopsCarryTheirPlaceAndWindows(t *testing.T) {
	v := vehicle("v", at(7, 0), at(18, 0), 100)
	p := compile(t, []plan.Resource{v}, pickup("o1", lat(0.05), 5, 10*time.Minute, []string{"cold"}, [4]int{13, 0, 14, 50}))
	u := leftOut(t, p, 0)
	if len(u.Stops) != 1 {
		t.Fatalf("stops: %+v", u.Stops)
	}
	st := u.Stops[0]
	if st.ID != "s-o1" || st.Lat < 45.049 || st.Lat > 45.051 || st.ServiceMin != 10 || fmt.Sprint(st.StartWindows) != "[13:00–14:50]" {
		t.Errorf("stop: %+v", st)
	}
}

// The three orders of the Jabodetabek sample that no vehicle can serve are
// each explained by what makes them impossible.
func TestJabodetabekImpossibleOrdersAreExplained(t *testing.T) {
	p := testutil.RepoProblemFile(t, "plan_jabodetabek.json")
	s := model.NewSolution(p)
	got := map[string]Unserved{}
	for _, u := range explainUnserved(p, s, model.NewEvaluator(p)) {
		got[u.Order] = u
	}
	wantCause(t, got["order-jkt-001"], CauseWindowInBreak, "jam istirahat 12:00–13:00")
	wantCause(t, got["order-bgr-034"], CauseWindowUnreachable, "tidak terkejar", "paling lambat pukul 07:50")
	wantCause(t, got["order-bgr-031"], CauseShiftTooShort, "akhir shift pukul 18:00")
}

func TestMultiTripVehiclesAreDescribedAndCounted(t *testing.T) {
	a, b := vehicle("one-trip", at(7, 0), at(18, 0), 15), vehicle("rounds", at(7, 0), at(18, 0), 15)
	b.MultiTrip, b.TurnaroundDuration = true, plan.Duration(10*time.Minute)
	p := compile(t, []plan.Resource{a, b},
		pickup("x", lat(0.05), 10, 5*time.Minute, nil), pickup("y", lat(0.06), 10, 5*time.Minute, nil), pickup("z", lat(0.07), 10, 5*time.Minute, nil))

	sum := Summarize(p)
	if len(sum.Vehicles) != 2 || sum.Vehicles[0].MultiTrip || !sum.Vehicles[1].MultiTrip || sum.Vehicles[1].TurnaroundMin != 10 {
		t.Errorf("a vehicle that makes several trips is a different kind from one that does not: %+v", sum.Vehicles)
	}

	s := model.NewSolution(p)
	s.Unassigned = nil
	s.Routes[1] = []int{0, 1, 2}
	s.Recompute(model.NewEvaluator(p))
	d := Diagnose(p, s, nil)
	if !s.Feasible || d.Routes[1].Trips != 3 || d.Routes[0].Trips != 0 {
		t.Errorf("feasible %v, trips %d and %d", s.Feasible, d.Routes[0].Trips, d.Routes[1].Trips)
	}
}

// Several trips are not a way round the weight of one order.
func TestUnservedOrderAboveEveryTripCapacity(t *testing.T) {
	v := vehicle("rounds", at(7, 0), at(18, 0), 10)
	v.MultiTrip = true
	p := compile(t, []plan.Resource{v}, pickup("big", lat(0.05), 50, 5*time.Minute, nil))
	wantCause(t, leftOut(t, p, 0), CauseTooHeavy, "50 weight")
}
