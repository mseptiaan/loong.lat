package dispatch

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/changes"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/fleet"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/plans"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/settings"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/testkit"
)

// These tests need a real Postgres (TEST_DATABASE_URL) and skip without one.

// rig is a service with its collaborators on a fresh schema; events it publishes stay in the outbox.
type rig struct {
	pool       *pgxpool.Pool
	fleet      *fleet.Service
	svc        *Service
	projection *plans.Projection
	settings   *settings.Service

	mu       sync.Mutex
	nextCar  int
	presence map[string]int64
}

func newRig(t *testing.T) *rig { return newRigAt(t, time.Now) }

// newRigAt is newRig with the clock the dispatch rules and the roster go by, for tests that move time.
func newRigAt(t *testing.T, now func() time.Time) *rig {
	t.Helper()
	pool := testkit.Pool(t)
	feed := changes.New(platform.NewHub(), time.Millisecond)
	pub := testkit.Publisher(pool)
	f := fleet.New(pool, pub, feed, now, CommitLead)
	svc := New(pool, pub, f, feed, testkit.Discard, now)
	r := &rig{
		pool: pool, fleet: f, svc: svc, projection: plans.NewProjection(feed, pub),
		settings: settings.New(pool, pub, feed, svc.Kick), presence: map[string]int64{},
	}
	r.apply(t, r.projection.CustomerRegistered, contracts.TypeCustomerRegistered, "cus-1", 1,
		contracts.CustomerRegistered{CustomerID: "cus-1", Name: "Dina Pramesti"})
	return r
}

func (r *rig) apply(t *testing.T, h eventbus.Handler, eventType, key string, version int64, data any) {
	t.Helper()
	if err := testkit.Apply(r.pool, h, eventType, key, version, data); err != nil {
		t.Fatalf("%s: %v", eventType, err)
	}
}

// driver creates a driver with a vehicle of the type and brings him online km north of the pickup point.
func (r *rig) driver(t *testing.T, vt contracts.VehicleType, km float64) string {
	t.Helper()
	d := r.offlineDriver(t, vt)
	r.goOnline(t, d, km)
	return d
}

// offlineDriver creates a driver who never reported being online.
func (r *rig) offlineDriver(t *testing.T, vt contracts.VehicleType) string {
	t.Helper()
	r.mu.Lock()
	r.nextCar++
	n := r.nextCar
	r.mu.Unlock()
	capacity := map[contracts.VehicleType][3]float64{contracts.VehicleTypeMotor: {20, 0.12, 55}, contracts.VehicleTypeBlindvan: {500, 3.2, 190}}[vt]
	v, err := r.fleet.CreateVehicle(t.Context(), fleet.VehicleInput{
		Type: vt, Plate: fmt.Sprintf("B %04d AB", n), Model: "Model " + string(vt), MaxWeightKg: capacity[0], MaxVolumeM3: capacity[1],
		MaxDimensionCm: capacity[2], Status: contracts.VehicleStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	d, err := r.fleet.CreateDriver(t.Context(), fleet.DriverInput{
		Name: fmt.Sprintf("Driver %d", n), Phone: fmt.Sprintf("08123456%04d", n), VehicleID: v.ID, PIN: "123456",
	})
	if err != nil {
		t.Fatal(err)
	}
	return d.ID
}

func (r *rig) goOnline(t *testing.T, driverID string, km float64) {
	t.Helper()
	pos := testkit.North(testkit.Kemang, km)
	r.mu.Lock()
	r.presence[driverID]++
	version := r.presence[driverID]
	r.mu.Unlock()
	r.apply(t, r.fleet.Online, contracts.TypeDriverOnline, driverID, version, contracts.DriverOnline{DriverID: driverID, Position: &pos})
}

// confirm projects a confirmed plan of "now" routes: one route per given vehicle type and weight.
func (r *rig) confirm(t *testing.T, planID string, routes ...contracts.Route) contracts.Plan {
	t.Helper()
	plan := testkit.Plan(planID, routes...)
	r.apply(t, r.projection.PlanConfirmed, contracts.TypePlanConfirmed, planID, 1, contracts.PlanConfirmed{Plan: plan})
	return plan
}

func nowRoute(planID string, n int, vt contracts.VehicleType, weightKg float64) contracts.Route {
	return testkit.Route(planID, n, vt, testkit.Kemang, weightKg, time.Now().Add(2*time.Minute))
}

func (r *rig) plan(t *testing.T, id string) contracts.Plan {
	t.Helper()
	a, err := plans.Load(t.Context(), r.pool, id)
	if err != nil {
		t.Fatal(err)
	}
	return a.Snapshot()
}

func conflictKey(t *testing.T, err error) string {
	t.Helper()
	apiErr, ok := errors.AsType[*platform.APIError](err)
	if !ok || apiErr.Code != platform.CodeConflict {
		t.Fatalf("error = %v, want a conflict (bentrok)", err)
	}
	return apiErr.Msg.Key
}

func TestAssignRefusesDriversWhoCannotTakeTheRoute(t *testing.T) {
	r := newRig(t)
	r.confirm(t, "pln-1", nowRoute("pln-1", 1, contracts.VehicleTypeMotor, 5))
	r.confirm(t, "pln-2", nowRoute("pln-2", 1, contracts.VehicleTypeMotor, 25)) // more than a motorbike carries

	offline := r.offlineDriver(t, contracts.VehicleTypeMotor)
	tooFar := r.driver(t, contracts.VehicleTypeMotor, Radius+3)
	van := r.driver(t, contracts.VehicleTypeBlindvan, 1)
	motor := r.driver(t, contracts.VehicleTypeMotor, 1)

	tests := []struct {
		name    string
		plan    string
		driver  string
		wantKey string
	}{
		{"an offline driver", "pln-1", offline, "srv.ops.err.driverUnavailable"},
		{"a driver too far from the pickup", "pln-1", tooFar, "srv.ops.err.driverUnavailable"},
		{"a blind van for a motorbike route", "pln-1", van, "srv.ops.err.vehicleMismatch"},
		{"a load over the vehicle's capacity", "pln-2", motor, "srv.ops.err.overCapacity"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := r.svc.Assign(t.Context(), tt.plan, tt.plan+":r1", tt.driver)
			if got := conflictKey(t, err); got != tt.wantKey {
				t.Errorf("message = %s, want %s", got, tt.wantKey)
			}
		})
	}
	if n := len(testkit.Outbox(t, r.pool, contracts.TypeRouteAssigned)); n != 0 {
		t.Errorf("%d route.assigned events after only refused assignments", n)
	}
	if got := r.plan(t, "pln-1").Routes[0]; got.Status != contracts.RouteStatusAwaitingDriver || got.DriverID != "" {
		t.Errorf("route = %s with driver %q after refusals, want it untouched", got.Status, got.DriverID)
	}

	if _, err := r.svc.Assign(t.Context(), "pln-1", "pln-1:r1", "drv-unknown"); !isNotFound(err) {
		t.Errorf("unknown driver: %v, want not found", err)
	}
	if _, err := r.svc.Assign(t.Context(), "pln-1", "pln-1:r9", motor); !isNotFound(err) {
		t.Errorf("unknown route: %v, want not found", err)
	}
	if _, err := r.svc.Assign(t.Context(), "pln-9", "pln-9:r1", motor); !isNotFound(err) {
		t.Errorf("unknown plan: %v, want not found", err)
	}
}

func isNotFound(err error) bool {
	apiErr, ok := errors.AsType[*platform.APIError](err)
	return ok && apiErr.Code == platform.CodeNotFound
}

func TestAssignHandsTheRouteToTheDriverAndAnnouncesIt(t *testing.T) {
	r := newRig(t)
	r.confirm(t, "pln-1", nowRoute("pln-1", 1, contracts.VehicleTypeMotor, 5))
	driverID := r.driver(t, contracts.VehicleTypeMotor, 2)

	plan, err := r.svc.Assign(t.Context(), "pln-1", "pln-1:r1", driverID)
	if err != nil {
		t.Fatal(err)
	}
	route := plan.Routes[0]
	if plan.Status != contracts.PlanStatusAssigned || route.Status != contracts.RouteStatusAssigned || route.DriverID != driverID || route.VehicleID == "" {
		t.Fatalf("plan %s, route %s driver %q vehicle %q: want ditugaskan with the driver and his vehicle", plan.Status, route.Status, route.DriverID, route.VehicleID)
	}
	if d := route.StartPosition.Lat - testkit.North(testkit.Kemang, 2).Lat; d > 1e-9 || d < -1e-9 {
		t.Errorf("start position %v, want the driver's position: the route is rebased to him", route.StartPosition)
	}
	if route.Stops[0].LegKm < 1.5 {
		t.Errorf("first leg %v km, want it recomputed from the driver's position (about 2.5 km)", route.Stops[0].LegKm)
	}

	events := testkit.Outbox(t, r.pool, contracts.TypeRouteAssigned)
	if len(events) != 1 {
		t.Fatalf("%d route.assigned events, want 1", len(events))
	}
	env := events[0]
	if env.Key != "pln-1:r1" || env.AggregateVersion != 1 || env.Source != "backoffice-api" {
		t.Errorf("envelope key %q version %d source %q, want the route id at version 1 from backoffice-api", env.Key, env.AggregateVersion, env.Source)
	}
	payload, err := env.Payload[contracts.RouteAssigned]()
	if err != nil {
		t.Fatal(err)
	}
	if payload.PlanCode != "PL-pln-1" || payload.CustomerName != "Dina Pramesti" || payload.DriverID != driverID ||
		payload.VehicleID != route.VehicleID || payload.Route.Status != contracts.RouteStatusAssigned ||
		payload.Route.DriverID != driverID || !payload.StartAt.Equal(payload.Route.StartAt.Time) {
		t.Errorf("payload = %+v, want plan code, customer name, driver, vehicle and a full assigned route snapshot", payload)
	}

	drivers, err := r.fleet.Drivers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range drivers {
		if d.ID == driverID && d.Status != fleet.StatusOnDuty {
			t.Errorf("driver status %s, want bertugas: his route departs within the hour", d.Status)
		}
	}
	if len(plan.Events) == 0 || plan.Events[len(plan.Events)-1].Text.Key != "srv.ops.event.assigned" {
		t.Errorf("timeline = %+v, want it to end with the assignment", plan.Events)
	}
}

func TestAssignRefusesAStartedOrTakenRoute(t *testing.T) {
	r := newRig(t)
	r.confirm(t, "pln-1", nowRoute("pln-1", 1, contracts.VehicleTypeMotor, 5))
	first := r.driver(t, contracts.VehicleTypeMotor, 1)
	second := r.driver(t, contracts.VehicleTypeMotor, 2)
	if _, err := r.svc.Assign(t.Context(), "pln-1", "pln-1:r1", first); err != nil {
		t.Fatal(err)
	}

	_, err := r.svc.Assign(t.Context(), "pln-1", "pln-1:r1", second)
	if got := conflictKey(t, err); got != "srv.ops.err.routeHasDriver" {
		t.Errorf("assigning a taken route: %s", got)
	}

	r.apply(t, r.projection.RouteStarted, contracts.TypeRouteStarted, "pln-1:r1", 1,
		contracts.RouteStarted{PlanID: "pln-1", RouteID: "pln-1:r1", DriverID: first, StartedAt: contracts.Now()})
	_, err = r.svc.Assign(t.Context(), "pln-1", "pln-1:r1", second)
	conflictKey(t, err)
	_, err = r.svc.Unassign(t.Context(), "pln-1", "pln-1:r1")
	if got := conflictKey(t, err); got != "srv.ops.err.routeStarted" {
		t.Errorf("unassigning a started route: %s", got)
	}
	if n := len(testkit.Outbox(t, r.pool, contracts.TypeRouteAssigned)); n != 1 {
		t.Errorf("%d route.assigned events, want only the first", n)
	}
}

func TestConcurrentAssignsOfOneRouteMakeOneAssignment(t *testing.T) {
	r := newRig(t)
	r.confirm(t, "pln-1", nowRoute("pln-1", 1, contracts.VehicleTypeMotor, 5))
	var drivers []string
	for i := range 4 {
		drivers = append(drivers, r.driver(t, contracts.VehicleTypeMotor, float64(i+1)))
	}

	const attempts = 8
	errs := make([]error, attempts)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range attempts {
		wg.Go(func() {
			<-start
			_, errs[i] = r.svc.Assign(context.Background(), "pln-1", "pln-1:r1", drivers[i%len(drivers)])
		})
	}
	close(start)
	wg.Wait()

	won := 0
	for _, err := range errs {
		switch {
		case err == nil:
			won++
		default:
			conflictKey(t, err)
		}
	}
	if won != 1 {
		t.Fatalf("%d assignments succeeded, want exactly 1 (errors: %v)", won, errs)
	}
	events := testkit.Outbox(t, r.pool, contracts.TypeRouteAssigned)
	if len(events) != 1 {
		t.Fatalf("%d route.assigned events, want exactly one", len(events))
	}
	if events[0].AggregateVersion != 1 {
		t.Errorf("route.assigned at version %d, want 1", events[0].AggregateVersion)
	}
	var version int64
	if err := r.pool.QueryRow(t.Context(), `SELECT assign_version FROM routes WHERE id = 'pln-1:r1'`).Scan(&version); err != nil || version != 1 {
		t.Errorf("assign_version = %d, %v; want 1", version, err)
	}
}

func TestOneDriverIsNotGivenTwoRoutesAtTheSameTime(t *testing.T) {
	r := newRig(t)
	r.confirm(t, "pln-1", nowRoute("pln-1", 1, contracts.VehicleTypeMotor, 5))
	r.confirm(t, "pln-2", nowRoute("pln-2", 1, contracts.VehicleTypeMotor, 5))
	driverID := r.driver(t, contracts.VehicleTypeMotor, 1)

	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, planID := range []string{"pln-1", "pln-2"} {
		wg.Go(func() { _, errs[i] = r.svc.Assign(context.Background(), planID, planID+":r1", driverID) })
	}
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("errors %v: exactly one of two overlapping routes may go to one driver", errs)
	}
}

func TestAutoAssignPicksTheNearestEligibleDriver(t *testing.T) {
	r := newRig(t)
	r.confirm(t, "pln-1", nowRoute("pln-1", 1, contracts.VehicleTypeMotor, 5))
	r.offlineDriver(t, contracts.VehicleTypeMotor) // never online: not a candidate however close
	r.driver(t, contracts.VehicleTypeBlindvan, 0.1)
	r.driver(t, contracts.VehicleTypeMotor, 7)
	nearest := r.driver(t, contracts.VehicleTypeMotor, 2)
	r.driver(t, contracts.VehicleTypeMotor, 4)

	assigned, err := r.svc.AutoAssignRoute(t.Context(), plans.Waiting{PlanID: "pln-1", RouteID: "pln-1:r1"})
	if err != nil || !assigned {
		t.Fatalf("assigned = %v, %v", assigned, err)
	}
	plan := r.plan(t, "pln-1")
	if plan.Routes[0].DriverID != nearest {
		t.Errorf("driver %q, want the nearest eligible one %q", plan.Routes[0].DriverID, nearest)
	}
	last := plan.Events[len(plan.Events)-1].Text
	if last.Key != "srv.ops.event.autoAssignedWhy" {
		t.Errorf("timeline ends with %s, want the automatic assignment with its reason", last.Key)
	}

	// asking again changes nothing: the route is taken
	assigned, err = r.svc.AutoAssignRoute(t.Context(), plans.Waiting{PlanID: "pln-1", RouteID: "pln-1:r1"})
	if err != nil || assigned {
		t.Errorf("second attempt: assigned = %v, %v; want a quiet no-op", assigned, err)
	}
	if n := len(testkit.Outbox(t, r.pool, contracts.TypeRouteAssigned)); n != 1 {
		t.Errorf("%d route.assigned events, want 1", n)
	}
}

func TestAutoAssignLeavesThePlanWaitingUntilADriverComesOnline(t *testing.T) {
	r := newRig(t)
	r.confirm(t, "pln-1", nowRoute("pln-1", 1, contracts.VehicleTypeMotor, 5))
	sleeper := r.offlineDriver(t, contracts.VehicleTypeMotor)
	w := plans.Waiting{PlanID: "pln-1", RouteID: "pln-1:r1"}

	for range 2 { // a retry must not repeat the note
		if assigned, err := r.svc.AutoAssignRoute(t.Context(), w); err != nil || assigned {
			t.Fatalf("assigned = %v, %v; want nobody to be found", assigned, err)
		}
	}
	plan := r.plan(t, "pln-1")
	if plan.Status != contracts.PlanStatusAwaitingDriver || plan.Routes[0].DriverID != "" {
		t.Errorf("plan %s with driver %q, want it still waiting", plan.Status, plan.Routes[0].DriverID)
	}
	notes := 0
	for _, e := range plan.Events {
		if e.Text.Key == "srv.ops.event.noDriverNow" {
			notes++
		}
	}
	if notes != 1 {
		t.Errorf("%d notes saying no driver was found, want 1", notes)
	}

	r.goOnline(t, sleeper, 1.5)
	if assigned, err := r.svc.AutoAssignRoute(t.Context(), w); err != nil || !assigned {
		t.Fatalf("after the driver came online: assigned = %v, %v", assigned, err)
	}
	if got := r.plan(t, "pln-1").Routes[0].DriverID; got != sleeper {
		t.Errorf("driver %q, want %q", got, sleeper)
	}
}

func TestAutoAssignPlanIsAllOrNothing(t *testing.T) {
	r := newRig(t)
	r.confirm(t, "pln-1", nowRoute("pln-1", 1, contracts.VehicleTypeMotor, 5), nowRoute("pln-1", 2, contracts.VehicleTypeBlindvan, 100))
	r.driver(t, contracts.VehicleTypeMotor, 1)

	_, err := r.svc.AutoAssignPlan(t.Context(), "pln-1")
	if got := conflictKey(t, err); got != "srv.api.noDriverForRoute" {
		t.Fatalf("message = %s, want no driver for the van route", got)
	}
	if n := len(testkit.Outbox(t, r.pool, contracts.TypeRouteAssigned)); n != 0 {
		t.Errorf("%d route.assigned events although the request was refused: the motor route must not be assigned alone", n)
	}
	if got := r.plan(t, "pln-1").Routes[0].Status; got != contracts.RouteStatusAwaitingDriver {
		t.Errorf("motor route is %s, want it rolled back to waiting", got)
	}

	r.driver(t, contracts.VehicleTypeBlindvan, 2)
	plan, err := r.svc.AutoAssignPlan(t.Context(), "pln-1")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != contracts.PlanStatusAssigned || plan.Routes[0].DriverID == "" || plan.Routes[1].DriverID == "" {
		t.Errorf("plan %s, drivers %q %q; want both routes assigned", plan.Status, plan.Routes[0].DriverID, plan.Routes[1].DriverID)
	}
	if n := len(testkit.Outbox(t, r.pool, contracts.TypeRouteAssigned)); n != 2 {
		t.Errorf("%d route.assigned events, want 2", n)
	}
	_, err = r.svc.AutoAssignPlan(t.Context(), "pln-1")
	if got := conflictKey(t, err); got != "srv.api.allRoutesHaveDriver" {
		t.Errorf("assigning an assigned plan: %s", got)
	}
}

func TestUnassignReleasesTheRouteForAnotherDriver(t *testing.T) {
	r := newRig(t)
	r.confirm(t, "pln-1", nowRoute("pln-1", 1, contracts.VehicleTypeMotor, 5))
	first := r.driver(t, contracts.VehicleTypeMotor, 1)
	if _, err := r.svc.Assign(t.Context(), "pln-1", "pln-1:r1", first); err != nil {
		t.Fatal(err)
	}

	plan, err := r.svc.Unassign(t.Context(), "pln-1", "pln-1:r1")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != contracts.PlanStatusAwaitingDriver || plan.Routes[0].DriverID != "" || plan.Routes[0].Status != contracts.RouteStatusAwaitingDriver {
		t.Errorf("plan %s, route %s driver %q: want it back in the queue", plan.Status, plan.Routes[0].Status, plan.Routes[0].DriverID)
	}
	out := testkit.Outbox(t, r.pool, contracts.TypeRouteUnassigned)
	if len(out) != 1 || out[0].Key != "pln-1:r1" || out[0].AggregateVersion != 2 {
		t.Fatalf("route.unassigned events = %+v, want one for the route at version 2", out)
	}
	if ev, _ := out[0].Payload[contracts.RouteUnassigned](); ev.DriverID != first {
		t.Errorf("unassigned driver %q, want %q", ev.DriverID, first)
	}

	if _, err := r.svc.Unassign(t.Context(), "pln-1", "pln-1:r1"); err != nil {
		t.Errorf("unassigning a waiting route: %v, want a no-op", err)
	}
	if n := len(testkit.Outbox(t, r.pool, contracts.TypeRouteUnassigned)); n != 1 {
		t.Errorf("%d route.unassigned events after the no-op, want 1", n)
	}

	second := r.driver(t, contracts.VehicleTypeMotor, 2)
	if _, err := r.svc.Assign(t.Context(), "pln-1", "pln-1:r1", second); err != nil {
		t.Fatal(err)
	}
	assigned := testkit.Outbox(t, r.pool, contracts.TypeRouteAssigned)
	if len(assigned) != 2 || assigned[1].AggregateVersion != 3 {
		t.Errorf("assign versions: %d events, last at %d; want 2 events, the last at version 3", len(assigned), assigned[len(assigned)-1].AggregateVersion)
	}
}

func TestSuggestListsOnlyDriversWhoCouldTakeTheRoute(t *testing.T) {
	r := newRig(t)
	r.confirm(t, "pln-1", nowRoute("pln-1", 1, contracts.VehicleTypeMotor, 25)) // too heavy for a motorbike's 20 kg
	r.confirm(t, "pln-2", nowRoute("pln-2", 1, contracts.VehicleTypeMotor, 5))
	r.offlineDriver(t, contracts.VehicleTypeMotor)
	r.driver(t, contracts.VehicleTypeBlindvan, 1)
	r.driver(t, contracts.VehicleTypeMotor, Radius+5)
	near := r.driver(t, contracts.VehicleTypeMotor, 1)
	farther := r.driver(t, contracts.VehicleTypeMotor, 6)

	got, err := r.svc.Suggest(t.Context(), "pln-2", "pln-2:r1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].DriverID != near || got[1].DriverID != farther {
		t.Fatalf("suggestions = %+v, want the two motorbike drivers in range, nearest first", got)
	}
	s := got[0]
	if !s.Fits || s.Score <= got[1].Score || len(s.Reasons) != 3 || s.Reasons[0].Key != "srv.ops.sug.distance" ||
		s.Reasons[1].Key != "srv.ops.sug.rating" || s.Reasons[2].Key != "srv.ops.sug.fits" {
		t.Errorf("suggestion = %+v, want fits, a higher score than the farther one, and the mock's three reasons", s)
	}

	heavy, err := r.svc.Suggest(t.Context(), "pln-1", "pln-1:r1")
	if err != nil {
		t.Fatal(err)
	}
	if len(heavy) != 2 || heavy[0].Fits || heavy[0].Reasons[2].Key != "srv.ops.sug.tooBig" {
		t.Errorf("heavy suggestions = %+v, want the motorbike drivers marked as not fitting, with the reason", heavy)
	}
}

func TestTheWorkerAssignsOnlyWhileAutomaticDispatchIsOn(t *testing.T) {
	r := newRig(t)
	if _, err := r.settings.Update(t.Context(), settings.Patch{Dispatch: &settings.DispatchPatch{Auto: new(false)}}); err != nil {
		t.Fatal(err)
	}
	r.confirm(t, "pln-1", nowRoute("pln-1", 1, contracts.VehicleTypeMotor, 5))
	driverID := r.driver(t, contracts.VehicleTypeMotor, 1)

	r.svc.pass(t.Context())
	if got := r.plan(t, "pln-1").Routes[0].DriverID; got != "" {
		t.Fatalf("driver %q assigned while automatic dispatch is off", got)
	}

	// Switching it on wakes the worker, even though its sweep is an hour away.
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- r.svc.Run(ctx, time.Hour) }()
	t.Cleanup(func() { cancel(); <-done })
	if _, err := r.settings.Update(t.Context(), settings.Patch{Dispatch: &settings.DispatchPatch{Auto: new(true)}}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the worker to assign the route after the switch", func() bool {
		return r.plan(t, "pln-1").Routes[0].DriverID == driverID
	})

	// and off again: a new plan is left alone
	if _, err := r.settings.Update(t.Context(), settings.Patch{Dispatch: &settings.DispatchPatch{Auto: new(false)}}); err != nil {
		t.Fatal(err)
	}
	r.confirm(t, "pln-2", nowRoute("pln-2", 1, contracts.VehicleTypeMotor, 5))
	r.driver(t, contracts.VehicleTypeMotor, 1)
	r.svc.Kick()
	time.Sleep(3 * settle)
	if got := r.plan(t, "pln-2").Routes[0].DriverID; got != "" {
		t.Errorf("driver %q assigned after switching automatic dispatch off", got)
	}
}

func TestScheduledRoutesWaitForTheCommitLead(t *testing.T) {
	r := newRig(t)
	soon := testkit.Route("pln-soon", 1, contracts.VehicleTypeMotor, testkit.Kemang, 5, time.Now().Add(CommitLead-20*time.Minute))
	later := testkit.Route("pln-later", 1, contracts.VehicleTypeMotor, testkit.Kemang, 5, time.Now().Add(CommitLead+30*time.Minute))
	for _, route := range []contracts.Route{soon, later} {
		plan := testkit.Plan(route.PlanID, route)
		plan.Schedule = contracts.Schedule{Mode: contracts.ScheduleModeScheduled, Window: &contracts.TimeWindow{From: route.StartAt, To: contracts.At(route.StartAt.Add(time.Hour))}}
		r.apply(t, r.projection.PlanConfirmed, contracts.TypePlanConfirmed, plan.ID, 1, contracts.PlanConfirmed{Plan: plan})
	}
	r.driver(t, contracts.VehicleTypeMotor, 1)
	r.driver(t, contracts.VehicleTypeMotor, 2)

	r.svc.pass(t.Context())
	if got := r.plan(t, "pln-soon").Routes[0].DriverID; got == "" {
		t.Error("a route departing within the commit lead was not assigned")
	}
	if got := r.plan(t, "pln-later").Routes[0].DriverID; got != "" {
		t.Errorf("driver %q assigned to a route that departs after the commit lead; it should wait for the sweep", got)
	}
}

func TestTheWorkerServesNowPlansOldestFirst(t *testing.T) {
	r := newRig(t)
	older := testkit.Plan("pln-older", nowRoute("pln-older", 1, contracts.VehicleTypeMotor, 5))
	newer := testkit.Plan("pln-newer", nowRoute("pln-newer", 1, contracts.VehicleTypeMotor, 5))
	older.CreatedAt = contracts.At(time.Now().Add(-2 * time.Hour))
	newer.CreatedAt = contracts.At(time.Now().Add(-time.Hour))
	for _, p := range []contracts.Plan{newer, older} { // arrival order is the reverse of age
		r.apply(t, r.projection.PlanConfirmed, contracts.TypePlanConfirmed, p.ID, 1, contracts.PlanConfirmed{Plan: p})
	}
	only := r.driver(t, contracts.VehicleTypeMotor, 1) // one driver for two overlapping routes

	r.svc.pass(t.Context())
	if got := r.plan(t, "pln-older").Routes[0].DriverID; got != only {
		t.Errorf("the older plan's driver is %q, want the only driver %q", got, only)
	}
	if got := r.plan(t, "pln-newer").Routes[0].DriverID; got != "" {
		t.Errorf("the newer plan got %q: the one driver went to the plan that waited longest", got)
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
