package plans

import (
	"encoding/json/v2"
	"slices"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/testkit"
)

var t0 = time.Date(2026, time.October, 21, 3, 0, 0, 0, time.UTC)

func twoRoutePlan() contracts.Plan {
	a := testkit.Route("pln-1", 1, contracts.VehicleTypeMotor, testkit.Kemang, 5, t0)
	b := testkit.Route("pln-1", 2, contracts.VehicleTypeBlindvan, testkit.North(testkit.Kemang, 5), 80, t0)
	return testkit.Plan("pln-1", a, b)
}

func TestSummarizeAWaitingPlan(t *testing.T) {
	plan := twoRoutePlan()
	got := Summarize(plan, "Dina", nil)
	if got.CustomerName != "Dina" || got.Code != "PL-pln-1" || got.Status != contracts.PlanStatusAwaitingDriver {
		t.Errorf("identity = %+v", got)
	}
	if got.Shipments != 2 || got.Stops != 4 || got.RoutesWithoutDriver != 2 || got.Progress != (Progress{0, 4}) {
		t.Errorf("counts: shipments %d stops %d without driver %d progress %+v", got.Shipments, got.Stops, got.RoutesWithoutDriver, got.Progress)
	}
	if !slices.Equal(got.VehicleTypes, []contracts.VehicleType{contracts.VehicleTypeMotor, contracts.VehicleTypeBlindvan}) {
		t.Errorf("vehicle types = %v, want one per route", got.VehicleTypes)
	}
	if got.FromLabel != "Pickup pln-1:r1" || got.ToLabel != "Dropoff pln-1:r2 +1" {
		t.Errorf("labels %q → %q, want first pickup → last drop-off with +1 for the second shipment", got.FromLabel, got.ToLabel)
	}
	if got.DistanceKm != 6 || got.DurationMin != 20 || got.TotalCostRp != 40000 || !got.StartAt.Equal(t0) {
		t.Errorf("totals: %v km, %v min, Rp %d, start %v", got.DistanceKm, got.DurationMin, got.TotalCostRp, got.StartAt)
	}
}

func TestSummaryCarriesWhatTheChosenOptionSaved(t *testing.T) {
	plan := twoRoutePlan()
	if got := Summarize(plan, "Dina", nil); got.SavingsRp != nil || got.SavingsPct != nil {
		t.Errorf("a plan that was shared without savings must not invent them: %v / %v", got.SavingsRp, got.SavingsPct)
	}
	rp, pct := int64(-1500), 7.5 // negative: the faster option cost more
	plan.SavingsRp, plan.SavingsPct = &rp, &pct
	got := Summarize(plan, "Dina", nil)
	if got.SavingsRp == nil || *got.SavingsRp != -1500 || got.SavingsPct == nil || *got.SavingsPct != 7.5 {
		t.Errorf("savings = %v / %v", got.SavingsRp, got.SavingsPct)
	}
}

func TestSummarizeAPlanUnderWay(t *testing.T) {
	plan := twoRoutePlan()
	plan.Status = contracts.PlanStatusRunning
	plan.Routes[0].DriverID, plan.Routes[0].Status, plan.Routes[0].DelayMin = "drv-1", contracts.RouteStatusRunning, 12.4
	plan.Routes[0].Stops[0].Status = contracts.StopStatusDone
	plan.Routes[0].Stops[1].Status = contracts.StopStatusProblem

	got := Summarize(plan, "Dina", map[string]string{"drv-1": "Budi"})
	if got.RoutesWithoutDriver != 1 || !slices.Equal(got.DriverNames, []string{"Budi"}) {
		t.Errorf("without driver %d, drivers %v; want the second route open and Budi listed", got.RoutesWithoutDriver, got.DriverNames)
	}
	if got.Progress != (Progress{2, 4}) || got.ProblemStops != 1 || got.DelayMin != 12 || !got.HasAnomaly {
		t.Errorf("progress %+v, problems %d, delay %d, anomaly %v; want 2 of 4 closed, 1 problem, 12 min late and flagged", got.Progress, got.ProblemStops, got.DelayMin, got.HasAnomaly)
	}

	plan.Status = contracts.PlanStatusDone
	if got := Summarize(plan, "Dina", nil); got.RoutesWithoutDriver != 0 {
		t.Errorf("a finished plan has %d routes without driver, want 0", got.RoutesWithoutDriver)
	}
}

func TestSummaryMatchesCodeCustomerPlacesAndDrivers(t *testing.T) {
	plan := twoRoutePlan()
	plan.Routes[0].DriverID = "drv-1"
	s := Summarize(plan, "Dina Pramesti", map[string]string{"drv-1": "Budi Santoso"})
	for q, want := range map[string]bool{
		"": true, "pl-pln": true, "DINA": true, "pickup": true, " budi ": true, "gilang": false, "zzz": false,
	} {
		if got := s.Matches(q); got != want {
			t.Errorf("Matches(%q) = %v, want %v", q, got, want)
		}
	}
}

func TestSyncDerivesThePlanStatusFromItsRoutes(t *testing.T) {
	a := &Aggregate{Plan: twoRoutePlan()}
	a.Routes = []Route{{Route: a.Plan.Routes[0]}, {Route: a.Plan.Routes[1]}}
	a.Plan.Routes = nil

	steps := []struct {
		first, second contracts.RouteStatus
		want          contracts.PlanStatus
	}{
		{contracts.RouteStatusAssigned, contracts.RouteStatusAwaitingDriver, contracts.PlanStatusAwaitingDriver},
		{contracts.RouteStatusAssigned, contracts.RouteStatusAssigned, contracts.PlanStatusAssigned},
		{contracts.RouteStatusRunning, contracts.RouteStatusAssigned, contracts.PlanStatusRunning},
		{contracts.RouteStatusDone, contracts.RouteStatusDone, contracts.PlanStatusDone},
	}
	for _, s := range steps {
		a.Routes[0].Status, a.Routes[1].Status = s.first, s.second
		a.Sync(t0)
		if a.Plan.Status != s.want {
			t.Errorf("routes %s + %s: plan %s, want %s", s.first, s.second, a.Plan.Status, s.want)
		}
	}
	if a.Plan.FinishedAt.IsZero() || !a.Plan.UpdatedAt.Equal(t0) {
		t.Errorf("finished %v updated %v, want both stamped at the event time", a.Plan.FinishedAt, a.Plan.UpdatedAt)
	}

	a.Plan.Status = contracts.PlanStatusCancelled
	a.Routes[0].Status, a.Routes[1].Status = contracts.RouteStatusAssigned, contracts.RouteStatusAssigned
	a.Sync(t0)
	if a.Plan.Status != contracts.PlanStatusCancelled {
		t.Errorf("a cancelled plan became %s", a.Plan.Status)
	}
	if got := a.Snapshot(); len(got.Routes) != 2 {
		t.Errorf("snapshot has %d routes, want both", len(got.Routes))
	}
}

func TestNoteOnceRecognisesATextThatWentThroughJSON(t *testing.T) {
	a := &Aggregate{Plan: contracts.Plan{}}
	note := contracts.Msg{Key: "srv.ops.event.noDriverNow", Params: map[string]any{"vehicle": contracts.Msg{Key: "srv.ops.vehicle.motor"}}}
	if !a.NoteOnce("e1", t0, note) {
		t.Fatal("the first note must be added")
	}
	// store and reload: nested messages come back as plain maps
	raw, err := json.Marshal(a.Plan)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded contracts.Plan
	if err := json.Unmarshal(raw, &reloaded); err != nil {
		t.Fatal(err)
	}
	a = &Aggregate{Plan: reloaded}
	if a.NoteOnce("e2", t0, note) {
		t.Error("the same text again right after itself must not be added")
	}
	other := contracts.Msg{Key: note.Key, Params: map[string]any{"vehicle": contracts.Msg{Key: "srv.ops.vehicle.blindvan"}}}
	if !a.NoteOnce("e3", t0, other) || len(a.Plan.Events) != 2 {
		t.Errorf("a different text must be added: %d events", len(a.Plan.Events))
	}
}

// The cases below are worked out by hand from the mock's summary.ts: the delay and the anomaly flag look only at
// routes that are not finished, a delay of 10 minutes is an anomaly and 9.4 is not, any problem stop is one,
// and a plan that is no longer open has no route "without a driver".
func TestSummarizeFollowsTheMocksRules(t *testing.T) {
	route := func(status contracts.RouteStatus, driver string, delay float64, stops ...contracts.StopStatus) contracts.Route {
		r := contracts.Route{ID: "r", Status: status, DriverID: driver, DelayMin: delay, VehicleType: contracts.VehicleTypeMotor}
		for _, s := range stops {
			r.Stops = append(r.Stops, contracts.RouteStop{Status: s})
		}
		return r
	}
	done, running, waiting := contracts.RouteStatusDone, contracts.RouteStatusRunning, contracts.RouteStatusAwaitingDriver
	tests := []struct {
		name        string
		status      contracts.PlanStatus
		routes      []contracts.Route
		wantDelay   int
		wantAnomaly bool
		wantProblem int
		wantNoDrv   int
		wantDone    int
		wantTotal   int
	}{
		{
			"a finished route's delay does not count: 20 min on the finished one, 4.5 on the running one",
			contracts.PlanStatusRunning,
			[]contracts.Route{
				route(done, "d1", 20, contracts.StopStatusDone, contracts.StopStatusDone),
				route(running, "d2", 4.5, contracts.StopStatusDone, contracts.StopStatusEnRoute),
			},
			5, false, 0, 0, 3, 4,
		},
		{
			"exactly 10 minutes is an anomaly", contracts.PlanStatusRunning,
			[]contracts.Route{route(running, "d1", 10, contracts.StopStatusEnRoute)},
			10, true, 0, 0, 0, 1,
		},
		{
			"9.4 minutes is not, and rounds to 9", contracts.PlanStatusRunning,
			[]contracts.Route{route(running, "d1", 9.4, contracts.StopStatusEnRoute)},
			9, false, 0, 0, 0, 1,
		},
		{
			"a problem stop is an anomaly even on time, and counts as closed", contracts.PlanStatusRunning,
			[]contracts.Route{route(running, "d1", 0, contracts.StopStatusProblem, contracts.StopStatusEnRoute)},
			0, true, 1, 0, 1, 2,
		},
		{
			"routes that wait have no driver while the plan is open", contracts.PlanStatusAwaitingDriver,
			[]contracts.Route{route(waiting, "", 0, contracts.StopStatusWaiting), route(contracts.RouteStatusAssigned, "d1", 0, contracts.StopStatusWaiting)},
			0, false, 0, 1, 0, 2,
		},
		{
			"a cancelled plan has none without a driver", contracts.PlanStatusCancelled,
			[]contracts.Route{route(waiting, "", 0, contracts.StopStatusWaiting)},
			0, false, 0, 0, 0, 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := contracts.Plan{ID: "p", Status: tt.status, Routes: tt.routes}
			got := Summarize(plan, "Dina", map[string]string{"d1": "A", "d2": "B"})
			if got.DelayMin != tt.wantDelay || got.HasAnomaly != tt.wantAnomaly || got.ProblemStops != tt.wantProblem ||
				got.RoutesWithoutDriver != tt.wantNoDrv || got.Progress != (Progress{tt.wantDone, tt.wantTotal}) {
				t.Errorf("delay %d anomaly %v problems %d without driver %d progress %+v; want %d %v %d %d {%d %d}",
					got.DelayMin, got.HasAnomaly, got.ProblemStops, got.RoutesWithoutDriver, got.Progress,
					tt.wantDelay, tt.wantAnomaly, tt.wantProblem, tt.wantNoDrv, tt.wantDone, tt.wantTotal)
			}
		})
	}
}

func TestSummarizeShowsAPendingCancel(t *testing.T) {
	plan := twoRoutePlan()
	plan.PendingAction = PendingCancel
	if got := Summarize(plan, "Dina", nil); got.PendingAction != "cancel" {
		t.Errorf("pending action %q, want cancel", got.PendingAction)
	}
	body, err := json.Marshal(Summarize(twoRoutePlan(), "Dina", nil))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["pendingAction"]; ok {
		t.Error("pendingAction is on the wire although nothing is pending")
	}
}
