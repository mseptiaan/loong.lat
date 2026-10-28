package plans

import (
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/optimizerapi"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/testkit"
)

func ptrEqual[T comparable](got *T, want T) bool { return got != nil && *got == want }

func TestSummaryOfADraftSaysWhatIsNotKnownYet(t *testing.T) {
	p := draftOf()
	p.ID, p.Code, p.Status, p.DraftStep = "pln-1", "PL-2001", contracts.PlanStatusDraft, 1
	s := Summarize(p, "Dina")
	if s.FromLabel != "Not selected yet" || s.ToLabel != "Not selected yet" || s.Shipments != 0 || s.Stops != 0 {
		t.Errorf("empty draft = %+v", s)
	}
	if s.TotalCostRp != nil || s.DistanceKm != nil || s.SavingsPct != nil || s.VehicleTypes == nil || s.DriverNames == nil {
		t.Errorf("an empty draft has no totals, and its lists are empty rather than missing: %+v", s)
	}

	p = draftOf("s1", "s2", "s3")
	s = Summarize(p, "Dina")
	if s.FromLabel != "Kemang s1" || s.ToLabel != "Senayan s3 +2" || s.Stops != 6 || s.Shipments != 3 {
		t.Errorf("drafted summary = %+v", s)
	}
}

func TestSummaryOfAPlanWithOptionsUsesTheChosenOne(t *testing.T) {
	p := draftOf("s1")
	p.Status = contracts.PlanStatusOptionsReady
	set := testkit.OptionSetFor(optimizerapi.QuoteRequest{PlanID: "pln-1", Shipments: p.Shipments}, 25_000)
	p.OptionSet = &set
	if s := Summarize(p, "Dina"); s.TotalCostRp != nil || s.SavingsRp != nil {
		t.Errorf("nothing is chosen yet but the summary shows %+v", s)
	}

	p.SelectedOptionID = set.Options[0].ID
	s := Summarize(p, "Dina")
	if !ptrEqual(s.TotalCostRp, 25_000) || !ptrEqual(s.SavingsRp, 1000) || !ptrEqual(s.SavingsPct, 4.0) || !ptrEqual(s.DistanceKm, 2.0) || !ptrEqual(s.DurationMin, 10.0) {
		t.Errorf("summary = cost %v savings %v/%v distance %v duration %v", s.TotalCostRp, s.SavingsRp, s.SavingsPct, s.DistanceKm, s.DurationMin)
	}
	if len(s.VehicleTypes) != 1 || s.VehicleTypes[0] != contracts.VehicleTypeMotor {
		t.Errorf("vehicle types = %v", s.VehicleTypes)
	}
}

func confirmedPlan(routes ...contracts.Route) contracts.Plan {
	p := draftOf("s1", "s2")
	p.Status, p.Routes, p.TotalCostRp = contracts.PlanStatusAwaitingDriver, routes, 25_000
	return p
}

func route(id string, status contracts.RouteStatus, stops ...contracts.StopStatus) contracts.Route {
	r := contracts.Route{ID: id, Status: status, VehicleType: contracts.VehicleTypeMotor, DistanceKm: 3.04, DurationMin: 20}
	for i, st := range stops {
		r.Stops = append(r.Stops, contracts.RouteStop{ID: id + ":s" + string(rune('1'+i)), Status: st, Waypoint: contracts.Waypoint{PlaceRef: contracts.PlaceRef{Name: id + " stop " + string(rune('1'+i))}}})
	}
	return r
}

func TestSummaryOfAConfirmedPlanFollowsItsRoutes(t *testing.T) {
	r1 := route("r1", contracts.RouteStatusRunning, contracts.StopStatusDone, contracts.StopStatusProblem, contracts.StopStatusEnRoute)
	r1.DriverID, r1.DelayMin = "drv-1", 11.6
	r2 := route("r2", contracts.RouteStatusAwaitingDriver, contracts.StopStatusWaiting, contracts.StopStatusWaiting)
	p := confirmedPlan(r1, r2)
	p.Status = contracts.PlanStatusRunning
	s := Summarize(p, "Dina")

	if s.FromLabel != "r1 stop 1" || s.ToLabel != "r2 stop 2 +1" || s.Stops != 5 {
		t.Errorf("labels %q → %q, %d stops", s.FromLabel, s.ToLabel, s.Stops)
	}
	if s.Progress != (Progress{Done: 2, Total: 5}) || s.ProblemStops != 1 || s.RoutesWithoutDriver != 1 {
		t.Errorf("progress %+v problems %d without driver %d", s.Progress, s.ProblemStops, s.RoutesWithoutDriver)
	}
	if s.DelayMin != 12 || !s.HasAnomaly {
		t.Errorf("delay %v anomaly %v, want the rounded delay of the unfinished route", s.DelayMin, s.HasAnomaly)
	}
	if !ptrEqual(s.DistanceKm, 6.1) || !ptrEqual(s.DurationMin, 20.0) || len(s.VehicleTypes) != 2 || len(s.DriverNames) != 0 {
		t.Errorf("distance %v duration %v vehicles %v drivers %v", s.DistanceKm, s.DurationMin, s.VehicleTypes, s.DriverNames)
	}
}

func TestFinishedRoutesDoNotCountAsDelayedOrWithoutDriver(t *testing.T) {
	done := route("r1", contracts.RouteStatusDone, contracts.StopStatusDone)
	done.DelayMin = 30
	p := confirmedPlan(done)
	p.Status = contracts.PlanStatusDone
	s := Summarize(p, "Dina")
	if s.DelayMin != 0 || s.HasAnomaly || s.RoutesWithoutDriver != 0 {
		t.Errorf("summary = delay %v anomaly %v without driver %d", s.DelayMin, s.HasAnomaly, s.RoutesWithoutDriver)
	}

	// A plan that is not (or no longer) waiting for drivers asks for none.
	p.Status = contracts.PlanStatusCancelled
	p.Routes[0].Status = contracts.RouteStatusAwaitingDriver
	if s := Summarize(p, "Dina"); s.RoutesWithoutDriver != 0 {
		t.Errorf("a cancelled plan needs %d drivers", s.RoutesWithoutDriver)
	}
}

func TestSummaryStartsWhereTheScheduleSays(t *testing.T) {
	from := time.Date(2026, 10, 22, 2, 0, 0, 0, time.UTC)
	p := draftOf("s1")
	p.Schedule = contracts.Schedule{Mode: contracts.ScheduleModeScheduled, Window: &contracts.TimeWindow{From: contracts.At(from), To: contracts.At(from.Add(time.Hour))}}
	if s := Summarize(p, "Dina"); !s.StartAt.Equal(from) || s.ScheduleMode != contracts.ScheduleModeScheduled {
		t.Errorf("a drafted schedule: start %v mode %s", s.StartAt, s.ScheduleMode)
	}
	r := route("r1", contracts.RouteStatusAssigned, contracts.StopStatusWaiting)
	r.StartAt = contracts.At(from.Add(10 * time.Minute))
	p.Routes = []contracts.Route{r}
	if s := Summarize(p, "Dina"); !s.StartAt.Equal(from.Add(10 * time.Minute)) {
		t.Errorf("a planned route departs at %v", s.StartAt)
	}
}
