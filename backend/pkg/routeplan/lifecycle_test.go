package routeplan

import (
	"errors"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

var t0 = time.Date(2026, 10, 21, 3, 0, 0, 0, time.UTC)

func assigned(t *testing.T) contracts.Route {
	t.Helper()
	routes, err := RoutesFromOption("PL-1", option(), nil)
	if err != nil {
		t.Fatal(err)
	}
	r := routes[0]
	r.VehicleType = contracts.VehicleTypeMotor
	return Assign(RebaseStart(r, r.StartPosition, t0), "drv-1", "veh-1", t0)
}

func TestARouteRunsFromAssignmentToCompletion(t *testing.T) {
	r := assigned(t)
	if r.Status != contracts.RouteStatusAssigned || r.DriverID != "drv-1" || r.AssignedAt.IsZero() {
		t.Fatalf("assign: %+v", r)
	}
	r, err := Start(r, t0.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != contracts.RouteStatusRunning || r.Stops[0].Status != contracts.StopStatusEnRoute || r.StartedAt.IsZero() {
		t.Fatalf("start: %+v", r)
	}
	// Arrival times count from the departure.
	depart := t0.Add(5 * time.Minute)
	if want := depart.Add(time.Duration(r.Stops[0].LegMin*60_000) * time.Millisecond); !r.Stops[0].ArriveAt.Equal(want) {
		t.Errorf("first arrival = %v, want %v", r.Stops[0].ArriveAt, want)
	}

	first, second := r.Stops[0].ID, r.Stops[1].ID
	if _, err := Arrive(r, second, t0); !errors.Is(err, ErrNotActiveStop) {
		t.Errorf("arriving at the second stop first: %v", err)
	}
	r, err = Arrive(r, first, t0.Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Arrive(r, first, t0); !errors.Is(err, ErrNotEnRoute) {
		t.Errorf("arriving twice: %v", err)
	}

	r, finished, err := Close(r, first, t0.Add(14*time.Minute), contracts.StopStatusDone, nil, nil)
	if err != nil || finished {
		t.Fatalf("close first: finished=%v err=%v", finished, err)
	}
	if r.Stops[1].Status != contracts.StopStatusEnRoute || !r.LegStartedAt.Equal(t0.Add(14*time.Minute)) {
		t.Errorf("the next stop must be driven to from the close time: %+v", r.Stops[1])
	}
	if want := t0.Add(14*time.Minute + 8*time.Minute); !r.Stops[1].ArriveAt.Equal(want) {
		t.Errorf("second arrival = %v, want %v (its leg from the close time)", r.Stops[1].ArriveAt, want)
	}

	r, err = Arrive(r, second, t0.Add(22*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	proof := &contracts.DeliveryProof{Kind: contracts.ProofKindOTP, At: contracts.At(t0.Add(23 * time.Minute))}
	r, finished, err = Close(r, second, t0.Add(23*time.Minute), contracts.StopStatusDone, proof, nil)
	if err != nil || !finished {
		t.Fatalf("close last: finished=%v err=%v", finished, err)
	}
	if r.Status != contracts.RouteStatusDone || r.FinishedAt.IsZero() || r.Stops[1].Proof == nil {
		t.Errorf("finish: %+v", r)
	}
}

func TestTransitionsAreCheckedAndNeverEditTheirInput(t *testing.T) {
	r := assigned(t)
	if _, err := Arrive(r, r.Stops[0].ID, t0); !errors.Is(err, ErrNotRunning) {
		t.Errorf("arriving before the route runs: %v", err)
	}
	if _, err := Start(Unassign(r), t0); !errors.Is(err, ErrNotAssigned) {
		t.Errorf("starting an unassigned route: %v", err)
	}
	started, _ := Start(r, t0)
	if r.Status != contracts.RouteStatusAssigned || r.Stops[0].Status != contracts.StopStatusWaiting {
		t.Error("Start edited the route it was given")
	}
	if _, err := Arrive(started, "nope", t0); !errors.Is(err, ErrStopNotFound) {
		t.Errorf("unknown stop: %v", err)
	}
	if _, _, err := Close(started, started.Stops[0].ID, t0, contracts.StopStatusDone, nil, nil); !errors.Is(err, ErrNotArrived) {
		t.Errorf("closing a stop that was not reached: %v", err)
	}
	if _, _, err := Close(started, started.Stops[0].ID, t0, contracts.StopStatusWaiting, nil, nil); err == nil {
		t.Error("a stop closes as done or problem only")
	}
}

func TestAProblemClosesTheStopWithItsIssue(t *testing.T) {
	r, _ := Start(assigned(t), t0)
	issue := contracts.Msg{Key: "dom.issue.lainnya"}
	r, finished, err := Close(r, r.Stops[0].ID, t0.Add(9*time.Minute), contracts.StopStatusProblem, nil, &issue)
	if err != nil || finished {
		t.Fatalf("finished=%v err=%v", finished, err)
	}
	s := r.Stops[0]
	if s.Status != contracts.StopStatusProblem || s.Issue == nil || s.Issue.Key != "dom.issue.lainnya" || s.ArrivedAt.IsZero() {
		t.Errorf("problem stop: %+v", s)
	}
	if r.Stops[1].Status != contracts.StopStatusEnRoute {
		t.Error("the route moves on after a problem")
	}
}

func TestUnassignReturnsTheRouteToTheQueue(t *testing.T) {
	r := Unassign(assigned(t))
	if r.Status != contracts.RouteStatusAwaitingDriver || r.DriverID != "" || r.VehicleID != "" || !r.AssignedAt.IsZero() {
		t.Errorf("%+v", r)
	}
}
