package analysis

import (
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/mseptiaan/loong.lat/backend/pkg/engine/geo"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/model"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/solver"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/testutil"
)

func TestSummarizeRepositoryPlan(t *testing.T) {
	p := testutil.RepoProblem(t)
	s := Summarize(p)

	if s.Orders != 22 || s.Stops != 32 || len(s.Vehicles) != 3 {
		t.Fatalf("counts: orders=%d stops=%d vehicles=%d", s.Orders, s.Stops, len(s.Vehicles))
	}
	driver := s.Vehicles[2]
	if driver.IDs[0] != "driver-1" || driver.CanCarryGoods {
		t.Errorf("driver-1 must be reported as unable to carry goods: %+v", driver)
	}
	if driver.EligibleOrders != 3 {
		t.Errorf("driver-1 can only serve the 3 keychain orders, got %d", driver.EligibleOrders)
	}
	if len(s.MustStayTogether) != 1 || len(s.MustStayTogether[0]) != 3 {
		t.Errorf("keychain group missing: %v", s.MustStayTogether)
	}
	if len(s.RestrictedOrders) == 0 || !strings.Contains(s.RestrictedOrders[0], "truck-1") {
		t.Errorf("restricted orders not summarized: %v", s.RestrictedOrders)
	}
	if s.Objectives[0] != "maximizeMandatoryStops" {
		t.Errorf("objectives: %v", s.Objectives)
	}
	if h, _ := s.Workload["one_vehicle_work_hours_estimate"].(float64); h <= 0 {
		t.Errorf("workload estimate missing: %v", s.Workload)
	}
}

func TestDiagnoseSolution(t *testing.T) {
	p := testutil.RepoProblem(t)
	sol, _ := solver.Solve(p, nil, solver.Options{Seed: 1, Iterations: 200, Workers: 1})
	d := Diagnose(p, sol, nil)

	if len(d.Routes) != 3 {
		t.Fatalf("routes: %d", len(d.Routes))
	}
	var dist float64
	for _, r := range d.Routes {
		dist += r.DistanceKm
	}
	if want := float64(sol.M[model.ObjDist]) / 1000; dist < want-0.1 || dist > want+0.1 {
		t.Errorf("route distances sum to %.2f km, solution says %.2f km", dist, want)
	}
	if d.Integrity != nil {
		t.Error("integrity needs a clustering")
	}
	if sol.VehiclesUsed() < 3 {
		found := false
		for _, h := range d.Hints {
			found = found || strings.Contains(h, "vehicles are used")
		}
		if !found {
			t.Errorf("idle vehicles should be explained in the hints: %v", d.Hints)
		}
	}
}

func TestClusterIntegrity(t *testing.T) {
	p := testutil.RepoProblem(t)
	sol, _ := solver.Solve(p, nil, solver.Options{Seed: 1, Iterations: 100, Workers: 1})
	cl := make([]int, len(p.Orders)) // everything in one cluster
	in := Diagnose(p, sol, cl).Integrity
	if in == nil || in.Clusters != 1 {
		t.Fatalf("integrity = %+v", in)
	}
	if in.SplitClusters == 0 && sol.VehiclesUsed() > 1 {
		t.Error("a cluster spread over several vehicles must count as split")
	}
}

func TestUnservedOrdersAreExplained(t *testing.T) {
	p := testutil.RepoProblem(t)
	sol := model.NewSolution(p)
	d := Diagnose(p, sol, nil)
	if len(d.Unserved) != len(p.Orders) {
		t.Fatalf("unserved = %d, want %d", len(d.Unserved), len(p.Orders))
	}
	for _, u := range d.Unserved {
		if len(u.Reasons) == 0 {
			t.Errorf("order %s has no explanation", u.Order)
		}
	}
}

func TestObjectiveValuesFollowPriorityOrder(t *testing.T) {
	p := testutil.RepoProblem(t)
	var m model.Metrics
	m[model.ObjDist] = 12345
	vals := ObjectiveValues(p, m)
	if len(vals) != len(p.Objectives) || vals[0].Objective != "maximizeMandatoryStops" {
		t.Fatalf("values: %+v", vals)
	}
	for _, v := range vals {
		if v.Objective == "minimizeDistance" && (v.Value != 12.35 || v.Unit != "km") {
			t.Errorf("distance = %+v", v)
		}
	}
}

// TestJabodetabekSamplePlan pins what plan_jabodetabek.json promises: a mixed
// fleet that works 07:00-18:00 with a fixed lunch break, refrigerated vehicles
// that only take cold orders, and three orders that no vehicle can serve.
func TestJabodetabekSamplePlan(t *testing.T) {
	p := testutil.RepoProblemFile(t, "plan_jabodetabek.json")
	if len(p.Orders) < 200 {
		t.Fatalf("%d orders, want at least 200", len(p.Orders))
	}
	byCity := map[string]int{}
	for _, o := range p.Orders {
		byCity[strings.Split(o.ID, "-")[1]]++
	}
	for _, city := range []string{"jkt", "bgr", "dpk", "tng", "bks"} {
		if byCity[city] < 30 {
			t.Errorf("only %d orders in %s", byCity[city], city)
		}
	}

	for _, v := range p.Vehicles {
		fb := v.Fixed
		if fb == nil || p.Local(v.Window.Begin).Format("15:04") != "07:00" || p.Local(v.Window.End).Format("15:04") != "18:00" ||
			p.Local(fb.Begin).Format("15:04") != "12:00" || p.Local(fb.End).Format("15:04") != "13:00" {
			t.Fatalf("%s must work 07:00-18:00 with a 12:00-13:00 break", v.ID)
		}
	}

	// Motors make several trips; no other vehicle does.
	for _, v := range p.Vehicles {
		if isMotor := strings.HasPrefix(v.ID, "motor-"); v.MultiTrip != isMotor || (isMotor && v.Turnaround != 600) {
			t.Errorf("%s: multiTrip=%v turnaround=%ds", v.ID, v.MultiTrip, v.Turnaround)
		}
	}

	var impossible []string
	for o, vehicles := range Eligibility(p) {
		cold := slices.Contains(p.Orders[o].RequiredSkills, "cold")
		if len(vehicles) == 0 {
			impossible = append(impossible, p.Orders[o].ID)
		}
		for _, v := range vehicles {
			if refrigerated := slices.Contains(p.Vehicles[v].SkillNames, "cold"); refrigerated != cold {
				t.Errorf("%s (cold=%v) is servable by %s (refrigerated=%v)", p.Orders[o].ID, cold, p.Vehicles[v].ID, refrigerated)
			}
		}
	}
	// A pickup window inside the lunch hour, one that closes two hours before
	// the van could arrive, and one that opens when the shift is nearly over.
	want := []string{"order-bgr-031", "order-bgr-034", "order-jkt-001"}
	slices.Sort(impossible)
	if !slices.Equal(impossible, want) {
		t.Errorf("orders without any eligible vehicle: %v, want %v", impossible, want)
	}
}

// TestJabodetabekHazardousTruckCannotFinishItsDay proves that, whatever the
// routes, the one truck allowed to carry hazardous waste must leave some of it
// behind for lack of time. Any tour from the depot through all of its pickups is
// at least as long as the minimum spanning tree of those places (straight-line
// distances, which roads can only exceed), and it drives at most at full speed.
// Serving every pickup then takes longer than the shift.
func TestJabodetabekHazardousTruckCannotFinishItsDay(t *testing.T) {
	p := testutil.RepoProblemFile(t, "plan_jabodetabek.json")
	var trucks []int
	for v := range p.Vehicles {
		if slices.Contains(p.Vehicles[v].SkillNames, "hazmat") {
			trucks = append(trucks, v)
		}
	}
	if len(trucks) != 1 {
		t.Fatalf("%d vehicles carry hazardous waste, want exactly 1", len(trucks))
	}
	veh := &p.Vehicles[trucks[0]]
	speed := p.Profiles[veh.Profile].SpeedKmh / 3.6 // m/s

	var service int64
	var orders int
	sites := []geo.LatLon{p.Locs[veh.Start]}
	for o := range p.Orders {
		if !slices.Contains(p.Orders[o].RequiredSkills, "hazmat") {
			continue
		}
		orders++
		for _, n := range p.Orders[o].Nodes {
			service += p.Nodes[n].Service
			sites = append(sites, p.Locs[p.Nodes[n].Loc])
		}
	}

	// Prim's algorithm.
	inTree := make([]bool, len(sites))
	best := make([]float64, len(sites))
	for i := range best {
		best[i] = math.Inf(1)
	}
	best[0] = 0
	var mst float64
	for range sites {
		next := -1
		for i := range sites {
			if !inTree[i] && (next < 0 || best[i] < best[next]) {
				next = i
			}
		}
		inTree[next] = true
		mst += best[next]
		for i := range sites {
			if !inTree[i] {
				best[i] = min(best[i], geo.Haversine(sites[next], sites[i]))
			}
		}
	}

	// The lunch hour is not working time.
	available := veh.Window.End - veh.Window.Begin - (veh.Fixed.End - veh.Fixed.Begin)
	needed := service + int64(mst/speed)
	t.Logf("%d hazardous pickups: %.1f h of service plus %.0f km of driving at %.0f km/h = at least %.1f h, the shift offers %.1f h",
		orders, float64(service)/3600, mst/1000, speed*3.6, float64(needed)/3600, float64(available)/3600)
	if needed <= available {
		t.Errorf("the hazardous pickups could all fit in one shift")
	}
	// Each of them can be served on its own, so what is left behind is a matter of time alone.
	for o, vehicles := range Eligibility(p) {
		if slices.Contains(p.Orders[o].RequiredSkills, "hazmat") && !slices.Contains(vehicles, trucks[0]) {
			t.Errorf("%s cannot be served even alone by the hazardous waste truck", p.Orders[o].ID)
		}
	}
}
