package projections

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/drivers"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/drivertest"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/fleet"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/settings"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/tasks"
)

// These tests need Postgres (TEST_DATABASE_URL) and skip without it. They call the handlers the way the
// consumer does: inside a transaction, with an envelope.

type fixture struct {
	t    *testing.T
	pool *pgxpool.Pool
	p    *Projections
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pool := drivertest.Pool(t)
	return fixture{t, pool, New(drivertest.Logger(), drivertest.Publisher(pool))}
}

// apply delivers one event to a handler and returns its error.
func (f fixture) apply(handler eventbus.Handler, eventType, key string, version int64, data any) error {
	f.t.Helper()
	env, err := contracts.NewEnvelope("backoffice-api", eventType, key, version, data)
	if err != nil {
		f.t.Fatal(err)
	}
	env.OccurredAt = contracts.At(drivertest.Epoch)
	return platform.WithTx(f.t.Context(), f.pool, func(tx pgx.Tx) error { return handler(f.t.Context(), tx, env) })
}

func (f fixture) mustApply(handler eventbus.Handler, eventType, key string, version int64, data any) {
	f.t.Helper()
	if err := f.apply(handler, eventType, key, version, data); err != nil {
		f.t.Fatalf("%s v%d: %v", eventType, version, err)
	}
}

func (f fixture) provision(id, phone string) {
	f.t.Helper()
	f.mustApply(f.p.driverProvisioned, contracts.TypeDriverProvisioned, id, 1, drivertest.Provisioned(f.t, id, phone, "123456"))
}

func (f fixture) task(routeID string) tasks.Task {
	f.t.Helper()
	task, err := tasks.Lock(f.t.Context(), f.pool, routeID)
	if err != nil {
		f.t.Fatalf("task %s: %v", routeID, err)
	}
	return task
}

const route = "pln-1:r1"

func (f fixture) assign(version int64, driverID string, mutate func(*contracts.RouteAssigned)) error {
	f.t.Helper()
	e := drivertest.Assigned(route, driverID)
	if mutate != nil {
		mutate(&e)
	}
	return f.apply(f.p.routeAssigned, contracts.TypeRouteAssigned, route, version, e)
}

func (f fixture) unassign(version int64) error {
	f.t.Helper()
	return f.apply(f.p.routeUnassigned, contracts.TypeRouteUnassigned, route, version,
		contracts.RouteUnassigned{PlanID: "pln-1", RouteID: route, DriverID: "drv-1"})
}

// run puts the stored route on the road, as the execution service would.
func (f fixture) run(status contracts.RouteStatus) {
	f.t.Helper()
	task := f.task(route)
	task.Route.Status = status
	task.Route.Stops[0].Status = contracts.StopStatusArrived
	task.ExecVersion = 3
	if err := tasks.Put(f.t.Context(), f.pool, task, drivertest.Epoch); err != nil {
		f.t.Fatal(err)
	}
}

func TestProvisioningCreatesADriverWhoCanBeFoundByPhone(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "0812-3456-7801")

	acct, err := drivers.FindAccount(t.Context(), f.pool, "081234567801")
	if err != nil || acct.ID != "drv-1" || acct.Name != "Budi Santoso" {
		t.Fatalf("account = %+v, %v", acct, err)
	}
	d, err := drivers.Get(t.Context(), f.pool, "drv-1")
	if err != nil || d.Status != drivers.StatusOffline || d.CompletedTrips != 0 || d.VehicleID != "veh-1" || d.JoinedAt.IsZero() {
		t.Errorf("driver = %+v, %v", d, err)
	}

	f.provision("drv-1", "0812-3456-7801") // the same event again changes nothing
	var n int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM drivers`).Scan(&n); err != nil || n != 1 {
		t.Errorf("%d drivers, %v", n, err)
	}
}

func TestProvisioningRefusesWhatNoRetryCanFix(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "081234567801")
	good := drivertest.Provisioned(t, "drv-2", "081234567802", "123456")

	for name, mutate := range map[string]func(*contracts.DriverProvisioned){
		"no PIN hash":         func(e *contracts.DriverProvisioned) { e.PinHash = "" },
		"a plain PIN":         func(e *contracts.DriverProvisioned) { e.PinHash = "123456" },
		"no name":             func(e *contracts.DriverProvisioned) { e.Name = " " },
		"no phone":            func(e *contracts.DriverProvisioned) { e.Phone = "n/a" },
		"another one's phone": func(e *contracts.DriverProvisioned) { e.Phone = "+62 812 3456 7801" },
	} {
		e := good
		mutate(&e)
		if err := f.apply(f.p.driverProvisioned, contracts.TypeDriverProvisioned, e.DriverID, 1, e); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := drivers.Get(t.Context(), f.pool, "drv-2"); err == nil {
		t.Error("a refused event created the driver")
	}
}

func TestUpdateChangesMasterDataAndAResetPINEndsTheSessions(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	f.provision("drv-1", "081234567801")
	sessions := platform.NewPgSessionStore(f.pool)
	later := time.Now().Add(time.Hour)
	for _, token := range []string{"a", "b"} {
		if err := sessions.Create(ctx, platform.HashToken(token), platform.Principal{Role: contracts.RoleDriver, UserID: "drv-1", Name: "Budi"}, later); err != nil {
			t.Fatal(err)
		}
	}

	// a change of name and vehicle keeps the PIN and the sessions
	f.mustApply(f.p.driverUpdated, contracts.TypeDriverUpdated, "drv-1", 2,
		contracts.DriverUpdated{DriverID: "drv-1", Name: "Budi S.", Phone: "081234567801", VehicleID: "veh-2"})
	if d, _ := drivers.Get(ctx, f.pool, "drv-1"); d.Name != "Budi S." || d.VehicleID != "veh-2" {
		t.Errorf("driver = %+v", d)
	}
	if _, err := sessions.Lookup(ctx, platform.HashToken("a")); err != nil {
		t.Errorf("a change of name ended the session: %v", err)
	}

	// a reset PIN does not
	reset := drivertest.Provisioned(t, "drv-1", "081234567801", "654321").PinHash
	f.mustApply(f.p.driverUpdated, contracts.TypeDriverUpdated, "drv-1", 3,
		contracts.DriverUpdated{DriverID: "drv-1", Name: "Budi S.", Phone: "081234567801", PinHash: reset})
	for _, token := range []string{"a", "b"} {
		if _, err := sessions.Lookup(ctx, platform.HashToken(token)); err == nil {
			t.Errorf("session %s survived a PIN reset", token)
		}
	}
	if acct, _ := drivers.FindAccount(ctx, f.pool, "081234567801"); acct.PinHash != reset {
		t.Error("the PIN hash was not replaced")
	}
}

func TestUpdateOlderThanWhatWeHaveIsDropped(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "081234567801")
	f.mustApply(f.p.driverUpdated, contracts.TypeDriverUpdated, "drv-1", 5,
		contracts.DriverUpdated{DriverID: "drv-1", Name: "Newest", Phone: "081234567801", VehicleID: "veh-1"})
	f.mustApply(f.p.driverUpdated, contracts.TypeDriverUpdated, "drv-1", 4,
		contracts.DriverUpdated{DriverID: "drv-1", Name: "Stale", Phone: "081234567801", VehicleID: "veh-1"})
	if d, _ := drivers.Get(t.Context(), f.pool, "drv-1"); d.Name != "Newest" {
		t.Errorf("name = %q: a stale update overwrote a newer one", d.Name)
	}
}

func TestUpdateOfADriverNotProvisionedYetIsRetried(t *testing.T) {
	f := newFixture(t)
	err := f.apply(f.p.driverUpdated, contracts.TypeDriverUpdated, "drv-1", 2,
		contracts.DriverUpdated{DriverID: "drv-1", Name: "Budi", Phone: "081234567801"})
	if err == nil {
		t.Fatal("accepted an update for a driver that does not exist: it must fail so that it is retried")
	}
}

func TestAssignmentCreatesTheTaskAndKeepsTheDeliveryCodes(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "081234567801")
	f.mustApply(f.p.routeAssigned, contracts.TypeRouteAssigned, route, 1, drivertest.Assigned(route, "drv-1"))

	task := f.task(route)
	if task.PlanCode != "PL-2001" || task.CustomerName != "Dina Pramesti" || task.DriverID != "drv-1" || task.AssignVersion != 1 || task.ExecVersion != 0 {
		t.Errorf("task = %+v", task)
	}
	if task.Route.Status != contracts.RouteStatusAssigned || task.Route.DriverID != "drv-1" || len(task.Route.Stops) != 3 {
		t.Errorf("route = %s for %s with %d stops", task.Route.Status, task.Route.DriverID, len(task.Route.Stops))
	}
	if task.Route.Stops[1].DeliveryCode != drivertest.CodeA || task.Route.Stops[2].DeliveryCode != drivertest.CodeB {
		t.Error("the delivery codes were not kept: the OTP could not be checked")
	}
	active, err := tasks.Active(t.Context(), f.pool, "drv-1")
	if err != nil || len(active) != 1 {
		t.Errorf("active = %d, %v", len(active), err)
	}
}

func TestAssignmentForAnUnknownDriverFailsSoItIsRetried(t *testing.T) {
	f := newFixture(t)
	if err := f.assign(1, "drv-1", nil); err == nil {
		t.Fatal("accepted a route for a driver who does not exist")
	}
	if _, err := tasks.Lock(t.Context(), f.pool, route); err == nil {
		t.Error("a failed assignment left a task behind")
	}
}

func TestAnOlderAssignmentDoesNotOverwriteANewerOne(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "081234567801")
	f.provision("drv-2", "081234567802")

	f.mustApply(f.p.routeAssigned, contracts.TypeRouteAssigned, route, 2, withCost(drivertest.Assigned(route, "drv-2"), 30000))
	for _, version := range []int64{1, 2} { // older, and a repeat of the current version
		f.mustApply(f.p.routeAssigned, contracts.TypeRouteAssigned, route, version, withCost(drivertest.Assigned(route, "drv-1"), 11111))
	}

	task := f.task(route)
	if task.DriverID != "drv-2" || task.Route.CostRp != 30000 || task.AssignVersion != 2 {
		t.Errorf("task = driver %s, cost %d, version %d; want the version 2 assignment kept", task.DriverID, task.Route.CostRp, task.AssignVersion)
	}
}

func withCost(e contracts.RouteAssigned, cost int64) contracts.RouteAssigned {
	e.Route.CostRp = cost
	return e
}

func TestANewerAssignmentReplacesAnUnstartedTask(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "081234567801")
	f.provision("drv-2", "081234567802")
	f.mustApply(f.p.routeAssigned, contracts.TypeRouteAssigned, route, 1, drivertest.Assigned(route, "drv-1"))
	f.mustApply(f.p.routeAssigned, contracts.TypeRouteAssigned, route, 3, drivertest.Assigned(route, "drv-2"))

	if task := f.task(route); task.DriverID != "drv-2" || task.Route.DriverID != "drv-2" || task.AssignVersion != 3 {
		t.Errorf("task = %+v", task)
	}
	if active, _ := tasks.Active(t.Context(), f.pool, "drv-1"); len(active) != 0 {
		t.Error("the first driver still has the task")
	}
}

func TestAssignmentNeverTouchesARouteThatIsRunning(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "081234567801")
	f.provision("drv-2", "081234567802")
	f.mustApply(f.p.routeAssigned, contracts.TypeRouteAssigned, route, 1, drivertest.Assigned(route, "drv-1"))
	f.run(contracts.RouteStatusRunning)

	f.mustApply(f.p.routeAssigned, contracts.TypeRouteAssigned, route, 2, drivertest.Assigned(route, "drv-2"))

	task := f.task(route)
	if task.DriverID != "drv-1" || task.Route.Status != contracts.RouteStatusRunning || task.Route.Stops[0].Status != contracts.StopStatusArrived || task.ExecVersion != 3 || task.AssignVersion != 1 {
		t.Errorf("task = driver %s, %s, first stop %s, exec %d, assign %d; execution must be left alone", task.DriverID, task.Route.Status, task.Route.Stops[0].Status, task.ExecVersion, task.AssignVersion)
	}
}

func TestUnassignmentTakesAnUnstartedRouteBackAndKeepsTheRow(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "081234567801")
	f.mustApply(f.p.routeAssigned, contracts.TypeRouteAssigned, route, 1, drivertest.Assigned(route, "drv-1"))
	f.mustApply(f.p.routeUnassigned, contracts.TypeRouteUnassigned, route, 2, contracts.RouteUnassigned{PlanID: "pln-1", RouteID: route, DriverID: "drv-1"})

	task := f.task(route)
	if task.DriverID != "" || task.Route.Status != contracts.RouteStatusAwaitingDriver || task.Route.DriverID != "" || task.AssignVersion != 2 {
		t.Errorf("task = %+v", task)
	}
	if active, _ := tasks.Active(t.Context(), f.pool, "drv-1"); len(active) != 0 {
		t.Errorf("the driver still lists %d tasks", len(active))
	}

	// the assignment it replaced, delivered late, must not bring the task back
	f.mustApply(f.p.routeAssigned, contracts.TypeRouteAssigned, route, 1, drivertest.Assigned(route, "drv-1"))
	if active, _ := tasks.Active(t.Context(), f.pool, "drv-1"); len(active) != 0 {
		t.Error("a stale assignment resurrected the task")
	}
	// a genuinely newer one does
	f.mustApply(f.p.routeAssigned, contracts.TypeRouteAssigned, route, 3, drivertest.Assigned(route, "drv-1"))
	if active, _ := tasks.Active(t.Context(), f.pool, "drv-1"); len(active) != 1 {
		t.Error("a newer assignment did not give the task back")
	}
}

func TestUnassignmentOfARunningRouteIsIgnored(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "081234567801")
	f.mustApply(f.p.routeAssigned, contracts.TypeRouteAssigned, route, 1, drivertest.Assigned(route, "drv-1"))
	f.run(contracts.RouteStatusRunning)

	if err := f.unassign(2); err != nil {
		t.Fatalf("a refused unassignment must be logged, not failed (it would be retried and dead-lettered): %v", err)
	}
	task := f.task(route)
	if task.DriverID != "drv-1" || task.Route.Status != contracts.RouteStatusRunning || task.Route.DriverID != "drv-1" || task.AssignVersion != 1 {
		t.Errorf("task = driver %s, %s, assign %d; the running route must stay with its driver", task.DriverID, task.Route.Status, task.AssignVersion)
	}
	if active, _ := tasks.Active(t.Context(), f.pool, "drv-1"); len(active) != 1 {
		t.Error("the driver lost the route they are on")
	}

	f.run(contracts.RouteStatusDone) // and not one that is done either
	if err := f.unassign(3); err != nil {
		t.Fatal(err)
	}
	if task := f.task(route); task.DriverID != "drv-1" || task.Route.Status != contracts.RouteStatusDone {
		t.Errorf("a finished route was taken back: %+v", task)
	}
}

func TestAnOlderUnassignmentIsDropped(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "081234567801")
	f.mustApply(f.p.routeAssigned, contracts.TypeRouteAssigned, route, 4, drivertest.Assigned(route, "drv-1"))
	if err := f.unassign(3); err != nil {
		t.Fatal(err)
	}
	if task := f.task(route); task.DriverID != "drv-1" || task.Route.Status != contracts.RouteStatusAssigned {
		t.Errorf("a stale unassignment took the route back: %+v", task)
	}
}

func TestUnassigningAnUnknownRouteIsHarmless(t *testing.T) {
	f := newFixture(t)
	if err := f.unassign(1); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Lock(t.Context(), f.pool, route); err == nil {
		t.Error("created a task out of an unassignment")
	}
}

func TestMalformedDispatchEventsAreRefused(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "081234567801")
	for name, mutate := range map[string]func(*contracts.RouteAssigned){
		"no driver": func(e *contracts.RouteAssigned) { e.DriverID = "" },
		"no plan":   func(e *contracts.RouteAssigned) { e.PlanID = "" },
		"no route":  func(e *contracts.RouteAssigned) { e.Route.ID = "" },
	} {
		if err := f.assign(1, "drv-1", mutate); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestFleetKeepsTheNewestVehicleAndForgetsRemovedOnes(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	v := contracts.Vehicle{ID: "veh-1", Type: contracts.VehicleTypeMotor, Plate: "B 1 A", Model: "Vario", Status: contracts.VehicleStatusActive}
	f.mustApply(f.p.vehicleUpserted, contracts.TypeVehicleUpserted, "veh-1", 2, contracts.VehicleUpserted{Vehicle: v})
	old := v
	old.Model = "Old"
	f.mustApply(f.p.vehicleUpserted, contracts.TypeVehicleUpserted, "veh-1", 1, contracts.VehicleUpserted{Vehicle: old})
	if got, _ := fleet.Get(ctx, f.pool, "veh-1"); got == nil || got.Model != "Vario" {
		t.Errorf("vehicle = %+v, want the version 2 model", got)
	}

	f.mustApply(f.p.vehicleRemoved, contracts.TypeVehicleRemoved, "veh-1", 3, contracts.VehicleRemoved{VehicleID: "veh-1"})
	if got, _ := fleet.Get(ctx, f.pool, "veh-1"); got != nil {
		t.Errorf("removed vehicle = %+v", got)
	}
	if err := f.apply(f.p.vehicleUpserted, contracts.TypeVehicleUpserted, "veh-x", 1, contracts.VehicleUpserted{}); err == nil {
		t.Error("accepted a vehicle without an id")
	}
}

func TestMeShowsTheProjectedVehicle(t *testing.T) {
	f := newFixture(t)
	f.provision("drv-1", "081234567801")
	f.mustApply(f.p.vehicleUpserted, contracts.TypeVehicleUpserted, "veh-1", 1,
		contracts.VehicleUpserted{Vehicle: contracts.Vehicle{ID: "veh-1", Plate: "B 1234 ABC", Model: "Vario 160", Type: contracts.VehicleTypeMotor}})

	profile, err := drivers.NewService(f.pool, drivertest.Publisher(f.pool), time.Now).Me(context.Background(), "drv-1")
	if err != nil || profile.Vehicle == nil || profile.Vehicle.Plate != "B 1234 ABC" {
		t.Errorf("profile = %+v, %v", profile, err)
	}
}

func TestSettingsFollowTheTopicByVersionAndIgnoreWhatTheyDoNotKnow(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	announce := func(version int64, e contracts.SettingsUpdated) error {
		return f.apply(f.p.settingsUpdated, contracts.TypeSettingsUpdated, e.Key, version, e)
	}

	if err := announce(2, contracts.SettingsUpdated{Key: contracts.SettingsKeyAI, AI: &contracts.AIFeatures{Copilot: true}}); err != nil {
		t.Fatal(err)
	}
	if err := announce(1, contracts.SettingsUpdated{Key: contracts.SettingsKeyAI, AI: &contracts.AIFeatures{Vision: true}}); err != nil {
		t.Fatal(err)
	}
	if err := announce(1, contracts.SettingsUpdated{Key: contracts.SettingsKeyDispatch, Dispatch: &contracts.DispatchSettings{Auto: false}}); err != nil {
		t.Fatal(err)
	}
	got, err := settings.Read(ctx, f.pool)
	if err != nil || got.AI != (contracts.AIFeatures{Copilot: true}) || got.Dispatch.Auto {
		t.Errorf("settings = %+v, %v; want the version 2 AI group and dispatch off", got, err)
	}

	if err := announce(1, contracts.SettingsUpdated{Key: "payments"}); err != nil {
		t.Errorf("a group of the future must be ignored, not retried: %v", err)
	}
	// a value that is missing can never be applied: refused (dead-lettered by the consumer, see the app tests)
	if err := announce(9, contracts.SettingsUpdated{Key: contracts.SettingsKeyAI}); err == nil {
		t.Error("an event without its value was accepted")
	}
}
