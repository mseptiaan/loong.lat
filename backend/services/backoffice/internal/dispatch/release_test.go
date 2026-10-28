package dispatch

import (
	"sync"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/fleet"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/testkit"
)

// These tests need a real Postgres (TEST_DATABASE_URL) and skip without one.

// clock is a time source a test moves by hand.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Now().Truncate(time.Millisecond)} }

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// confirmScheduled projects a plan scheduled for start with one motorbike route.
func (r *rig) confirmScheduled(t *testing.T, planID string, start time.Time) {
	t.Helper()
	route := testkit.Route(planID, 1, contracts.VehicleTypeMotor, testkit.Kemang, 5, start)
	plan := testkit.Plan(planID, route)
	plan.Schedule = contracts.Schedule{Mode: contracts.ScheduleModeScheduled, Window: &contracts.TimeWindow{From: route.StartAt, To: contracts.At(start.Add(time.Hour))}}
	r.apply(t, r.projection.PlanConfirmed, contracts.TypePlanConfirmed, planID, 1, contracts.PlanConfirmed{Plan: plan})
}

func (r *rig) assignTo(t *testing.T, planID, driverID string) {
	t.Helper()
	if _, err := r.svc.Assign(t.Context(), planID, planID+":r1", driverID); err != nil {
		t.Fatalf("assign %s: %v", planID, err)
	}
}

// offline delivers driver.offline with the version.
func (r *rig) offline(t *testing.T, driverID string, version int64) contracts.Envelope {
	t.Helper()
	env, err := contracts.NewEnvelope("driver-api", contracts.TypeDriverOffline, driverID, version, contracts.DriverOffline{DriverID: driverID})
	if err != nil {
		t.Fatal(err)
	}
	if err := testkit.ApplyEnvelope(r.pool, r.svc.DriverOffline, env); err != nil {
		t.Fatal(err)
	}
	return env
}

// speak delivers a position of the driver recorded at the time.
func (r *rig) speak(t *testing.T, driverID string, at time.Time) {
	t.Helper()
	r.apply(t, r.fleet.Position, contracts.TypeDriverPosition, driverID, 0, contracts.DriverPosition{
		DriverID: driverID, Position: testkit.North(testkit.Kemang, 1), RecordedAt: contracts.At(at),
	})
}

func (r *rig) route(t *testing.T, planID string) contracts.Route {
	t.Helper()
	return r.plan(t, planID).Routes[0]
}

func (r *rig) driverStatus(t *testing.T, driverID string) fleet.Status {
	t.Helper()
	drivers, err := r.fleet.Drivers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range drivers {
		if d.ID == driverID {
			return d.Status
		}
	}
	t.Fatalf("driver %s not found", driverID)
	return ""
}

func notices(t *testing.T, r *rig, titleKey string) []contracts.AppNotification {
	t.Helper()
	var out []contracts.AppNotification
	for _, n := range testkit.AdminNotifications(t, r.pool) {
		if n.Title.Key == titleKey {
			out = append(out, n)
		}
	}
	return out
}

func unassignedEvents(t *testing.T, r *rig) []contracts.RouteUnassigned {
	t.Helper()
	var out []contracts.RouteUnassigned
	for _, env := range testkit.Outbox(t, r.pool, contracts.TypeRouteUnassigned) {
		ev, err := env.Payload[contracts.RouteUnassigned]()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, ev)
	}
	return out
}

func TestADriverGoingOfflineReturnsEveryRouteHeHasNotStarted(t *testing.T) {
	r := newRig(t)
	gone := r.driver(t, contracts.VehicleTypeMotor, 1)
	stays := r.driver(t, contracts.VehicleTypeMotor, 2)
	runner := r.driver(t, contracts.VehicleTypeMotor, 3)
	r.confirm(t, "pln-now", nowRoute("pln-now", 1, contracts.VehicleTypeMotor, 5))
	r.confirmScheduled(t, "pln-later", time.Now().Add(5*time.Hour)) // far ahead: still his, until he is gone
	r.confirm(t, "pln-other", nowRoute("pln-other", 1, contracts.VehicleTypeMotor, 5))
	r.confirm(t, "pln-run", nowRoute("pln-run", 1, contracts.VehicleTypeMotor, 5))
	r.assignTo(t, "pln-later", gone) // first: once his near route is his, he is on duty and takes no other
	r.assignTo(t, "pln-now", gone)
	r.assignTo(t, "pln-other", stays)
	r.assignTo(t, "pln-run", runner)
	r.apply(t, r.projection.RouteStarted, contracts.TypeRouteStarted, "pln-run:r1", 1,
		contracts.RouteStarted{PlanID: "pln-run", RouteID: "pln-run:r1", DriverID: runner, StartedAt: contracts.Now()})

	env := r.offline(t, gone, 2)

	for _, planID := range []string{"pln-now", "pln-later"} {
		route := r.route(t, planID)
		if route.Status != contracts.RouteStatusAwaitingDriver || route.DriverID != "" || route.VehicleID != "" {
			t.Errorf("%s: route %s with driver %q, want it back in the queue", planID, route.Status, route.DriverID)
		}
		if got := r.plan(t, planID).Status; got != contracts.PlanStatusAwaitingDriver {
			t.Errorf("%s: plan %s, want menunggu_driver", planID, got)
		}
	}
	if got := r.route(t, "pln-other"); got.DriverID != stays || got.Status != contracts.RouteStatusAssigned {
		t.Errorf("another driver's route: %s with %q, want it untouched", got.Status, got.DriverID)
	}
	if got := r.route(t, "pln-run"); got.DriverID != runner || got.Status != contracts.RouteStatusRunning {
		t.Errorf("a running route: %s with %q, want it untouched", got.Status, got.DriverID)
	}
	r.offline(t, runner, 2) // a driver on the road who drops offline keeps the route he is driving
	if got := r.route(t, "pln-run"); got.DriverID != runner || got.Status != contracts.RouteStatusRunning {
		t.Errorf("a running route after its driver went offline: %s with %q, want it never released", got.Status, got.DriverID)
	}

	events := testkit.Outbox(t, r.pool, contracts.TypeRouteUnassigned)
	if len(events) != 2 {
		t.Fatalf("%d route.unassigned events, want one per returned route", len(events))
	}
	for _, env := range events {
		ev, _ := env.Payload[contracts.RouteUnassigned]()
		if ev.Reason != contracts.UnassignDriverOffline || ev.DriverID != gone || env.AggregateVersion != 2 {
			t.Errorf("route.unassigned = %+v at version %d, want reason driver_offline for his driver at assign version 2", ev, env.AggregateVersion)
		}
	}
	if last := r.plan(t, "pln-now").Events; last[len(last)-1].Text.Key != "srv.api.event.driverOfflineReleased" {
		t.Errorf("timeline ends with %+v, want the release", last[len(last)-1].Text)
	}
	if got := r.driverStatus(t, gone); got != fleet.StatusOffline {
		t.Errorf("driver is %s, want offline", got)
	}
	notes := notices(t, r, "srv.api.notif.released.title")
	if len(notes) != 1 || notes[0].Body.Params["name"] != "Driver 1" || notes[0].Body.Params["count"] != float64(2) {
		t.Fatalf("notices = %+v, want one: the driver's name and two jobs returned", notes)
	}

	// delivered again (or late), nothing more happens
	if err := testkit.ApplyEnvelope(r.pool, r.svc.DriverOffline, env); err != nil {
		t.Fatal(err)
	}
	if n := len(testkit.Outbox(t, r.pool, contracts.TypeRouteUnassigned)); n != 2 || len(notices(t, r, "srv.api.notif.released.title")) != 1 {
		t.Errorf("a redelivered driver.offline released more: %d events, %d notices", n, len(notices(t, r, "srv.api.notif.released.title")))
	}
}

func TestAnOfflineEventOlderThanThePresenceDoesNotTakeRoutesBack(t *testing.T) {
	r := newRig(t)
	d := r.driver(t, contracts.VehicleTypeMotor, 1) // online at presence version 1
	r.confirm(t, "pln-1", nowRoute("pln-1", 1, contracts.VehicleTypeMotor, 5))
	r.goOnline(t, d, 1) // version 2: he went offline and came back before the first event reached us
	r.assignTo(t, "pln-1", d)

	r.offline(t, d, 2) // a late "offline" of version 2, superseded by the online at version 2: not newer
	if got := r.route(t, "pln-1"); got.DriverID != d {
		t.Errorf("driver %q after a stale offline, want the route kept", got.DriverID)
	}
	if n := len(testkit.Outbox(t, r.pool, contracts.TypeRouteUnassigned)); n != 0 {
		t.Errorf("%d route.unassigned for a stale offline", n)
	}
}

func TestAnOfflineDriverWithoutRoutesChangesNoQueue(t *testing.T) {
	r := newRig(t)
	d := r.driver(t, contracts.VehicleTypeMotor, 1)
	r.offline(t, d, 2)
	if n := len(testkit.Outbox(t, r.pool, contracts.TypeRouteUnassigned)); n != 0 || len(testkit.AdminNotifications(t, r.pool)) != 1 { // the sign-up of the fixture's customer
		t.Errorf("%d events, %d notices for a driver with nothing to return", n, len(testkit.AdminNotifications(t, r.pool)))
	}
	if got := r.driverStatus(t, d); got != fleet.StatusOffline {
		t.Errorf("driver is %s, want offline", got)
	}
}

func TestASilentDriverLosesOnlyTheRoutesThatDepartSoon(t *testing.T) {
	c := newClock()
	r := newRigAt(t, c.now)
	silent := r.driver(t, contracts.VehicleTypeMotor, 1)
	r.confirm(t, "pln-soon", testkit.Route("pln-soon", 1, contracts.VehicleTypeMotor, testkit.Kemang, 5, c.now().Add(10*time.Minute)))
	r.confirmScheduled(t, "pln-far", c.now().Add(3*time.Hour))
	r.assignTo(t, "pln-far", silent) // first: once his near route is his, he is on duty and takes no other
	r.assignTo(t, "pln-soon", silent)

	// heard from recently enough: nothing happens
	c.advance(fleet.PresenceTTL - 10*time.Second)
	if n, err := r.svc.ReleaseSilent(t.Context()); err != nil || n != 0 {
		t.Fatalf("released %d routes (%v) while the driver is within the presence window", n, err)
	}

	// silent for longer than the presence window: the route that leaves within the commit lead goes, the far one stays
	c.advance(time.Minute)
	n, err := r.svc.ReleaseSilent(t.Context())
	if err != nil || n != 1 {
		t.Fatalf("released %d routes (%v), want only the one departing within %v", n, err, CommitLead)
	}
	if got := r.route(t, "pln-soon"); got.Status != contracts.RouteStatusAwaitingDriver || got.DriverID != "" {
		t.Errorf("near route: %s with %q, want it back in the queue", got.Status, got.DriverID)
	}
	if got := r.route(t, "pln-far"); got.DriverID != silent || got.Status != contracts.RouteStatusAssigned {
		t.Errorf("far route: %s with %q, want it kept: a closed app hours before a job is normal", got.Status, got.DriverID)
	}
	events := unassignedEvents(t, r)
	if len(events) != 1 || events[0].RouteID != "pln-soon:r1" || events[0].Reason != contracts.UnassignDriverSilent || events[0].DriverID != silent {
		t.Fatalf("route.unassigned = %+v, want one for the near route with reason driver_silent", events)
	}
	if notes := notices(t, r, "srv.api.notif.released.title"); len(notes) != 1 || notes[0].Body.Params["count"] != float64(1) {
		t.Errorf("notices = %+v, want one telling that a job went back", notes)
	} else if notes[0].Body.Key != "srv.api.notif.releasedSilent.body" {
		t.Errorf("the notice says %q: a driver who fell silent did not go offline", notes[0].Body.Key)
	}
	if last := r.plan(t, "pln-soon").Events; last[len(last)-1].Text.Key != "srv.api.event.driverSilentReleased" {
		t.Errorf("timeline line %q, want the one for a driver who stopped reporting", last[len(last)-1].Text.Key)
	}

	// time passes: the far route is near now, and still nobody has heard from him
	c.advance(2*time.Hour + 10*time.Minute)
	if n, err := r.svc.ReleaseSilent(t.Context()); err != nil || n != 1 {
		t.Fatalf("released %d routes (%v), want the far route now that it departs soon", n, err)
	}
	if got := r.route(t, "pln-far"); got.DriverID != "" {
		t.Errorf("far route still has %q", got.DriverID)
	}
	if n, err := r.svc.ReleaseSilent(t.Context()); err != nil || n != 0 {
		t.Errorf("released %d routes (%v) with nothing left to release", n, err)
	}
}

func TestASilentDriverWhoSpeaksUpKeepsHisRoutesAndARunningRouteIsNeverReleased(t *testing.T) {
	c := newClock()
	r := newRigAt(t, c.now)
	chatty := r.driver(t, contracts.VehicleTypeMotor, 1)
	runner := r.driver(t, contracts.VehicleTypeMotor, 2)
	r.confirm(t, "pln-1", testkit.Route("pln-1", 1, contracts.VehicleTypeMotor, testkit.Kemang, 5, c.now().Add(10*time.Minute)))
	r.confirm(t, "pln-2", testkit.Route("pln-2", 1, contracts.VehicleTypeMotor, testkit.Kemang, 5, c.now().Add(10*time.Minute)))
	r.assignTo(t, "pln-1", chatty)
	r.assignTo(t, "pln-2", runner)
	r.apply(t, r.projection.RouteStarted, contracts.TypeRouteStarted, "pln-2:r1", 1,
		contracts.RouteStarted{PlanID: "pln-2", RouteID: "pln-2:r1", DriverID: runner, StartedAt: contracts.At(c.now())})

	c.advance(fleet.PresenceTTL + 5*time.Minute)
	r.speak(t, chatty, c.now().Add(-time.Second)) // a position fix a second ago
	if n, err := r.svc.ReleaseSilent(t.Context()); err != nil || n != 0 {
		t.Fatalf("released %d routes (%v): one driver spoke up and the other is on the road", n, err)
	}
	if got := r.route(t, "pln-1"); got.DriverID != chatty {
		t.Errorf("route of the driver who spoke up: %q, want it kept", got.DriverID)
	}
	if got := r.route(t, "pln-2"); got.DriverID != runner || got.Status != contracts.RouteStatusRunning {
		t.Errorf("a running route: %s with %q, want it never released, however quiet the driver", got.Status, got.DriverID)
	}
}

func TestTheWorkerReleasesSilentRoutesAndHandsThemToADriverWhoIsThere(t *testing.T) {
	c := newClock()
	r := newRigAt(t, c.now)
	silent := r.driver(t, contracts.VehicleTypeMotor, 1)
	r.confirm(t, "pln-1", testkit.Route("pln-1", 1, contracts.VehicleTypeMotor, testkit.Kemang, 5, c.now().Add(10*time.Minute)))
	r.assignTo(t, "pln-1", silent)

	standby := r.driver(t, contracts.VehicleTypeMotor, 3)
	c.advance(fleet.PresenceTTL + time.Minute)
	r.speak(t, standby, c.now())

	r.svc.pass(t.Context())
	got := r.route(t, "pln-1")
	if got.DriverID != standby || got.Status != contracts.RouteStatusAssigned {
		t.Fatalf("route: %s with %q, want it taken from the silent driver and given to %q in the same round", got.Status, got.DriverID, standby)
	}
	if events := unassignedEvents(t, r); len(events) != 1 || events[0].Reason != contracts.UnassignDriverSilent {
		t.Errorf("route.unassigned = %+v, want one with reason driver_silent", events)
	}
	assigned := testkit.Outbox(t, r.pool, contracts.TypeRouteAssigned)
	if len(assigned) != 2 || assigned[1].AggregateVersion != 3 {
		t.Errorf("%d route.assigned events (last at version %d), want the assignment, then the new one at version 3", len(assigned), assigned[len(assigned)-1].AggregateVersion)
	}
}
