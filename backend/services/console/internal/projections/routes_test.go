package projections

import (
	"context"
	"encoding/json/v2"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform/platformtest"
	"github.com/mseptiaan/loong.lat/backend/pkg/routeplan"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/plans"
)

// listenForChanges subscribes to what notify announces, to see that a followed change tells the dashboard. The
// channel belongs to the whole test database, so announcements about other customers (tests of other packages run
// side by side) are skipped: the returned function yields the next one that mentions the customer.
func listenForChanges(t *testing.T, customer string) func() string {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), os.Getenv(platformtest.EnvDatabaseURL))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(context.WithoutCancel(t.Context())) })
	if _, err := conn.Exec(t.Context(), `LISTEN console_events`); err != nil {
		t.Fatal(err)
	}
	return func() string {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		for {
			n, err := conn.WaitForNotification(ctx)
			if err != nil {
				t.Fatalf("no announcement after the change: %v", err)
			}
			if strings.Contains(n.Payload, customer) {
				return n.Payload
			}
		}
	}
}

func TestARouteIsFollowedFromAssignmentToCompletion(t *testing.T) {
	s := newShop(t)
	nextAnnouncement := listenForChanges(t, s.customer)

	s.mustApply(assignHandler, s.assigned(1, "drv-1", "veh-aaa", at(1)))
	rec := s.record()
	if rec.Plan.Status != contracts.PlanStatusAssigned || rec.Plan.Routes[0].DriverID != "drv-1" || rec.Plan.Routes[0].Status != contracts.RouteStatusAssigned {
		t.Fatalf("after assignment: plan %s, route %+v", rec.Plan.Status, rec.Plan.Routes[0].Status)
	}
	if rec.Plan.Routes[0].Stops[2].DeliveryCode != "4321" {
		t.Error("the delivery code was lost")
	}
	last := rec.Plan.Events[len(rec.Plan.Events)-1].Text
	if last.Key != "srv.ops.event.assigned" || last.Params["plate"] != "B 1234 aaa" {
		t.Errorf("timeline says %+v, want the vehicle's plate", last)
	}
	if got := nextAnnouncement(); !strings.Contains(got, `"plans.changed"`) || !strings.Contains(got, `"planId":"pln-1"`) || !strings.Contains(got, s.customer) {
		t.Errorf("announcement = %s", got)
	}

	s.mustApply(startHandler, s.started(1, at(5)))
	rec = s.record()
	if rec.Plan.Status != contracts.PlanStatusRunning || rec.Plan.Routes[0].Status != contracts.RouteStatusRunning || rec.Plan.Routes[0].Stops[0].Status != contracts.StopStatusEnRoute {
		t.Fatalf("after start: plan %s, first stop %s", rec.Plan.Status, rec.Plan.Routes[0].Stops[0].Status)
	}

	s.mustApply(arriveHandler, s.arrived(2, 1, at(10)))
	stop := s.route().Stops[0]
	if stop.Status != contracts.StopStatusArrived || !stop.ArrivedAt.Equal(at(10)) {
		t.Errorf("after arriving: %s at %v", stop.Status, stop.ArrivedAt)
	}

	s.mustApply(completeHandler, s.completed(3, 1, at(14)))
	r := s.route()
	if r.Stops[0].Status != contracts.StopStatusDone || r.Stops[1].Status != contracts.StopStatusEnRoute || !r.Stops[1].ArriveAt.Equal(at(14).Add(5*time.Minute)) {
		t.Errorf("after the first pickup: stops %s, %s; next ETA %v (re-anchored to the completion)", r.Stops[0].Status, r.Stops[1].Status, r.Stops[1].ArriveAt)
	}

	s.mustApply(arriveHandler, s.arrived(4, 2, at(20)))
	s.mustApply(completeHandler, s.completed(5, 2, at(23)))
	s.mustApply(arriveHandler, s.arrived(6, 3, at(30)))
	s.mustApply(completeHandler, s.completed(7, 3, at(33)))
	if p := s.route().Stops[2].Proof; p == nil || p.Kind != contracts.ProofKindOTP {
		t.Errorf("the drop-off's proof = %+v", p)
	}
	s.mustApply(arriveHandler, s.arrived(8, 4, at(40)))
	if got := s.record().Plan.Status; got != contracts.PlanStatusRunning {
		t.Fatalf("before the last stop is done the plan is %s", got)
	}
	s.mustApply(completeHandler, s.completed(9, 4, at(43)))

	rec = s.record()
	if rec.Plan.Status != contracts.PlanStatusDone || !rec.Plan.FinishedAt.Equal(at(43)) || rec.Plan.Routes[0].Status != contracts.RouteStatusDone {
		t.Fatalf("after the last stop: plan %s finished %v", rec.Plan.Status, rec.Plan.FinishedAt)
	}
	want := []string{
		"srv.api.event.confirmed", "srv.ops.event.assigned", "srv.ops.seed.departed",
		"srv.ops.event.driverArrived", "srv.ops.event.pickupDone",
		"srv.ops.event.driverArrived", "srv.ops.event.pickupDone",
		"srv.ops.event.driverArrived", "srv.ops.event.dropoffDone",
		"srv.ops.event.driverArrived", "srv.ops.event.dropoffDone", "srv.ops.event.allDone",
	}
	if got := eventKeys(rec.Plan); !slices.Equal(got, want) {
		t.Errorf("timeline\n got %v\nwant %v", got, want)
	}
	if places := rec.Plan.Events[4].Text.Params["place"]; places != "Kemang" {
		t.Errorf("the first pickup names %v", places)
	}
	if !rec.Plan.UpdatedAt.Equal(at(43)) {
		t.Errorf("updatedAt = %v, want the last event's time", rec.Plan.UpdatedAt)
	}

	// The plan is over, so the route's own completion has nothing left to say.
	s.mustApply(routeDoneHandler, s.routeCompleted(10, at(43)))
	if after := s.record(); !slices.Equal(eventKeys(after.Plan), want) || after.Plan.Status != contracts.PlanStatusDone {
		t.Errorf("route.completed changed the timeline or status: %v", eventKeys(after.Plan))
	}
	if after := s.record(); after.Version != 1 {
		t.Errorf("plan.version = %d: following the route is not a commercial change", after.Version)
	}
}

func TestACompletedRouteOfAnOpenPlanChangesNothingVisible(t *testing.T) {
	s := newShop(t)
	s.withSecondRoute() // the plan stays open when the first route is done
	s.mustApply(assignHandler, s.assigned(1, "drv-1", "veh-aaa", at(1)))
	s.mustApply(startHandler, s.started(1, at(5)))
	for i, v := 1, int64(2); i <= 4; i, v = i+1, v+2 {
		s.mustApply(arriveHandler, s.arrived(v, i, at(10*i)))
		s.mustApply(completeHandler, s.completed(v+1, i, at(10*i+3)))
	}
	rec := s.record()
	if rec.Plan.Routes[0].Status != contracts.RouteStatusDone || rec.Plan.Status != contracts.PlanStatusAwaitingDriver || !rec.Plan.FinishedAt.IsZero() {
		t.Fatalf("route %s, plan %s finished %v; the second route is still waiting for a driver", rec.Plan.Routes[0].Status, rec.Plan.Status, rec.Plan.FinishedAt)
	}
	keys := eventKeys(rec.Plan)

	s.mustApply(routeDoneHandler, s.routeCompleted(10, at(43)))
	after := s.record()
	if !slices.Equal(eventKeys(after.Plan), keys) || after.Plan.Status != contracts.PlanStatusAwaitingDriver {
		t.Errorf("route.completed changed the timeline or status: %v", eventKeys(after.Plan))
	}
	if v := after.RouteVersions[s.routeID]; v != (plans.RouteVersions{Assign: 1, Exec: 10}) {
		t.Errorf("route versions = %+v, want assign 1 and exec 10, kept apart", v)
	}
}

func snapshot(t *testing.T, rec plans.Record) string {
	t.Helper()
	b, err := json.Marshal(rec, json.Deterministic(true)) // message params are maps: fix their order
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestOlderAndRepeatedEventsChangeNothing(t *testing.T) {
	s := newShop(t)
	s.mustApply(assignHandler, s.assigned(1, "drv-1", "veh-aaa", at(1)))
	s.mustApply(startHandler, s.started(1, at(5)))
	s.mustApply(arriveHandler, s.arrived(2, 1, at(10)))
	before := snapshot(t, s.record())

	stale := map[string]struct {
		h func() error
	}{
		"the same event again":       {func() error { return s.apply(arriveHandler, s.arrived(2, 1, at(30))) }},
		"an older execution event":   {func() error { return s.apply(startHandler, s.started(1, at(31))) }},
		"an older assignment":        {func() error { return s.apply(assignHandler, s.assigned(1, "drv-2", "veh-bbb", at(32))) }},
		"an older unassignment":      {func() error { return s.apply(unassignHandler, s.unassigned(1, at(2))) }},
		"a completion from the past": {func() error { return s.apply(completeHandler, s.completed(2, 1, at(33))) }},
	}
	for name, c := range stale {
		if err := c.h(); err != nil {
			t.Errorf("%s: %v (it should be dropped quietly, not retried)", name, err)
		}
		if after := snapshot(t, s.record()); after != before {
			t.Errorf("%s changed the plan", name)
		}
	}
}

func TestEachFamilyKeepsItsOwnVersion(t *testing.T) {
	s := newShop(t)
	// Version 1 of the execution family follows version 1 of dispatch: neither counter blocks the other.
	s.mustApply(assignHandler, s.assigned(1, "drv-1", "veh-aaa", at(1)))
	s.mustApply(startHandler, s.started(1, at(5)))
	if got := s.record().RouteVersions[s.routeID]; got != (plans.RouteVersions{Assign: 1, Exec: 1}) {
		t.Errorf("versions = %+v", got)
	}
}

func TestAssignmentsThatReachUsAfterTheRouteStartedAreSuperseded(t *testing.T) {
	s := newShop(t)
	s.mustApply(assignHandler, s.assigned(1, "drv-1", "veh-aaa", at(1)))
	s.mustApply(startHandler, s.started(1, at(5)))
	before := snapshot(t, s.record())

	// The topics are read independently: a re-assignment decided before the start can arrive after it.
	s.mustApply(unassignHandler, s.unassigned(2, at(2)))
	s.mustApply(assignHandler, s.assigned(3, "drv-2", "veh-bbb", at(6)))
	if after := snapshot(t, s.record()); after != before {
		t.Error("an assignment event undid a route that is already running")
	}
	if r := s.route(); r.Status != contracts.RouteStatusRunning || r.DriverID != "drv-1" {
		t.Errorf("route = %s driven by %s", r.Status, r.DriverID)
	}
}

func TestAReassignedRouteGoesBackToWaitingAndThenToTheNewDriver(t *testing.T) {
	s := newShop(t)
	s.mustApply(assignHandler, s.assigned(1, "drv-1", "veh-aaa", at(1)))
	s.mustApply(unassignHandler, s.unassigned(2, at(2)))
	rec := s.record()
	if r := rec.Plan.Routes[0]; rec.Plan.Status != contracts.PlanStatusAwaitingDriver || r.DriverID != "" || r.Status != contracts.RouteStatusAwaitingDriver || !r.AssignedAt.IsZero() {
		t.Fatalf("after unassigning: plan %s, route %s driver %q", rec.Plan.Status, r.Status, r.DriverID)
	}
	if last := rec.Plan.Events[len(rec.Plan.Events)-1].Text; last.Key != "srv.ops.event.unassigned" || last.Params["n"] != "1" {
		t.Errorf("timeline says %+v, want the route number", last)
	}

	s.mustApply(assignHandler, s.assigned(3, "drv-2", "veh-unknown", at(4)))
	rec = s.record()
	if r := rec.Plan.Routes[0]; rec.Plan.Status != contracts.PlanStatusAssigned || r.DriverID != "drv-2" {
		t.Errorf("after reassigning: plan %s, driver %q", rec.Plan.Status, r.DriverID)
	}
	// A vehicle the console has not heard of is named by its id rather than failing the event.
	if last := rec.Plan.Events[len(rec.Plan.Events)-1].Text; last.Params["plate"] != "veh-unknown" {
		t.Errorf("timeline says %+v", last)
	}
}

func TestATransitionTheRouteIsNotReadyForIsRetriedNotDropped(t *testing.T) {
	s := newShop(t)
	// Nothing assigned yet: the execution topic got ahead of the dispatch topic.
	before := snapshot(t, s.record())
	err := s.apply(startHandler, s.started(1, at(5)))
	if !errors.Is(err, routeplan.ErrNotAssigned) {
		t.Fatalf("start before assignment: %v", err)
	}
	if snapshot(t, s.record()) != before {
		t.Error("a failed event left a trace")
	}

	s.mustApply(assignHandler, s.assigned(1, "drv-1", "veh-aaa", at(1)))
	s.mustApply(startHandler, s.started(1, at(5)))
	for name, c := range map[string]struct {
		h    func() error
		want error
	}{
		"completing before arriving":  {func() error { return s.apply(completeHandler, s.completed(2, 1, at(9))) }, routeplan.ErrNotArrived},
		"arriving at a later stop":    {func() error { return s.apply(arriveHandler, s.arrived(2, 3, at(9))) }, routeplan.ErrNotActiveStop},
		"finishing a route that runs": {func() error { return s.apply(routeDoneHandler, s.routeCompleted(2, at(9))) }, errRouteNotFinished},
	} {
		if err := c.h(); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	if got := s.record().RouteVersions[s.routeID].Exec; got != 1 {
		t.Errorf("exec version = %d: refused events must not advance it", got)
	}
}

func TestAProblemAtAStopClosesItAndMovesOn(t *testing.T) {
	s := newShop(t)
	s.mustApply(assignHandler, s.assigned(1, "drv-1", "veh-aaa", at(1)))
	s.mustApply(startHandler, s.started(1, at(5)))
	s.mustApply(arriveHandler, s.arrived(2, 1, at(10)))
	s.mustApply(issueHandler, s.issue(3, 1, at(12)))

	rec := s.record()
	r := rec.Plan.Routes[0]
	if r.Stops[0].Status != contracts.StopStatusProblem || r.Stops[0].Issue == nil || r.Stops[0].Issue.Key != "srv.ops.issue.withNote" || r.Stops[1].Status != contracts.StopStatusEnRoute {
		t.Errorf("stops = %s (issue %+v), %s", r.Stops[0].Status, r.Stops[0].Issue, r.Stops[1].Status)
	}
	last := rec.Plan.Events[len(rec.Plan.Events)-1].Text
	if last.Key != "srv.ops.event.pickupProblem" || last.Params["place"] != "Kemang" {
		t.Fatalf("timeline says %+v", last)
	}
	if issue, ok := last.Params["issue"].(map[string]any); !ok || issue["key"] != "srv.ops.issue.withNote" {
		t.Errorf("the problem is shown as %+v, want the driver's message", last.Params["issue"])
	}
	if sum := plans.Summarize(rec.Plan, "Dina"); sum.ProblemStops != 1 || !sum.HasAnomaly {
		t.Errorf("summary = %d problem stops, anomaly %v", sum.ProblemStops, sum.HasAnomaly)
	}
}

func TestAPlanThatIsOverIgnoresLateEvents(t *testing.T) {
	s := newShop(t)
	s.mustApply(assignHandler, s.assigned(1, "drv-1", "veh-aaa", at(1)))
	s.mustApply(startHandler, s.started(1, at(5)))
	for i, stop := 1, 2; i <= 4; i, stop = i+1, stop+2 {
		s.mustApply(arriveHandler, s.arrived(int64(stop), i, at(10*i)))
		s.mustApply(completeHandler, s.completed(int64(stop+1), i, at(10*i+3)))
	}
	if got := s.record().Plan.Status; got != contracts.PlanStatusDone {
		t.Fatalf("plan is %s", got)
	}
	before := snapshot(t, s.record())
	s.mustApply(arriveHandler, s.arrived(99, 1, at(500)))
	s.mustApply(unassignHandler, s.unassigned(99, at(501)))
	if snapshot(t, s.record()) != before {
		t.Error("a finished plan was changed by a late event")
	}
}
