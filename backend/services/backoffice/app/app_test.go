package app_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus/eventbustest"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform/platformtest"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/app"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/testkit"
)

// These tests start the whole service (app.Start) on a fresh Postgres schema and an in-process Kafka and talk to
// it as its neighbours and the admin console do. They need TEST_DATABASE_URL and skip without it.

const adminPassword = "admin-password"

// driverPIN is the PIN of the drivers the tests create. It shares no digits run with their phone numbers, so
// finding it in an event can only mean it leaked.
const driverPIN = "908172"

type stack struct {
	t          *testing.T
	base       string
	brokers    []string
	pool       *pgxpool.Pool
	adminToken string
}

// start runs the service with the dispatch worker sweeping every 100ms; tweak may change the configuration.
func start(t *testing.T, tweak func(*app.Config)) *stack {
	t.Helper()
	pool := platformtest.Pool(t)
	var schema string
	if err := pool.QueryRow(t.Context(), `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	dsn, err := url.Parse(os.Getenv(platformtest.EnvDatabaseURL))
	if err != nil {
		t.Fatal(err)
	}
	q := dsn.Query()
	q.Set("search_path", schema)
	dsn.RawQuery = q.Encode()

	var topics []string
	for _, spec := range contracts.Topics() {
		topics = append(topics, spec.Name)
	}
	brokers := eventbustest.NewCluster(t, topics...)

	cfg := app.Config{
		HTTPAddr: "127.0.0.1:0", DatabaseURL: dsn.String(), KafkaBrokers: brokers, GroupPrefix: "test",
		AdminEmail: "admin@loong.lat", AdminPassword: adminPassword, AdminName: "Rani Wulandari",
		DispatchWorker: true, SweepInterval: 100 * time.Millisecond,
	}
	if tweak != nil {
		tweak(&cfg)
	}
	ctx, cancel := context.WithCancel(t.Context())
	a, err := app.Start(ctx, cfg, testkit.Discard)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		if err := a.Wait(); err != nil {
			t.Errorf("the service stopped with %v", err)
		}
	})

	s := &stack{t: t, base: "http://" + a.Addr(), brokers: brokers, pool: pool}
	var login struct {
		Token string `json:"token"`
	}
	s.mustCall("", "auth.loginAdmin", map[string]any{"email": "admin@loong.lat", "password": adminPassword}, &login)
	s.adminToken = login.Token
	return s
}

// call posts an RPC method and returns the status and the body.
func (s *stack) call(token, method string, in any) (int, []byte) {
	s.t.Helper()
	return s.callWith(token, method, in, nil)
}

// callWith is call with more request headers (Accept-Language, say).
func (s *stack) callWith(token, method string, in any, header map[string]string) (int, []byte) {
	s.t.Helper()
	body, err := json.Marshal(in)
	if err != nil {
		s.t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(s.t.Context(), http.MethodPost, s.base+"/v1/rpc/"+method, bytes.NewReader(body))
	if err != nil {
		s.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		s.t.Fatal(err)
	}
	return resp.StatusCode, data
}

// mustCall is call that expects 200 and decodes the answer into out (when not nil).
func (s *stack) mustCall(token, method string, in, out any) {
	s.t.Helper()
	status, data := s.call(token, method, in)
	if status != http.StatusOK {
		s.t.Fatalf("%s: %d %s", method, status, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			s.t.Fatalf("%s: decode %s: %v", method, data, err)
		}
	}
}

// admin is mustCall as the signed-in admin.
func (s *stack) admin(method string, in, out any) {
	s.t.Helper()
	s.mustCall(s.adminToken, method, in, out)
}

// produce publishes an event the way another service would.
func (s *stack) produce(source, eventType, key string, version int64, data any) {
	s.t.Helper()
	eventbustest.Produce(s.t, s.brokers, eventbustest.Record(s.t, source, eventType, key, version, data))
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (s *stack) plan(id string) (contracts.Plan, bool) {
	s.t.Helper()
	status, data := s.call(s.adminToken, "plans.get", map[string]any{"id": id})
	if status != http.StatusOK {
		return contracts.Plan{}, false
	}
	var plan contracts.Plan
	if err := json.Unmarshal(data, &plan); err != nil {
		s.t.Fatal(err)
	}
	return plan, true
}

// waitPlan waits until the plan is projected and the condition on it holds.
func (s *stack) waitPlan(id, what string, cond func(contracts.Plan) bool) contracts.Plan {
	s.t.Helper()
	var got contracts.Plan
	eventually(s.t, what, func() bool {
		plan, ok := s.plan(id)
		got = plan
		return ok && cond(plan)
	})
	return got
}

// setup creates a vehicle and a driver through the console's methods and returns the driver's id.
func (s *stack) setup(vehicleType contracts.VehicleType, plate, phone string) string {
	s.t.Helper()
	var vehicle contracts.Vehicle
	s.admin("fleet.vehicles.create", map[string]any{"input": map[string]any{
		"type": vehicleType, "plate": plate, "model": "Honda Vario 160", "maxWeightKg": 20, "maxVolumeM3": 0.12,
		"maxDimensionCm": 55, "status": "aktif", "fare": contracts.FareConfig{Base: 6000, PerKm: 1800},
	}}, &vehicle)
	var driver struct {
		ID string `json:"id"`
	}
	s.admin("fleet.drivers.create", map[string]any{"input": map[string]any{
		"name": "Budi Santoso", "phone": phone, "vehicleId": vehicle.ID, "pin": driverPIN,
	}}, &driver)
	return driver.ID
}

func (s *stack) goOnline(driverID string, version int64, km float64) {
	s.t.Helper()
	pos := testkit.North(testkit.Kemang, km)
	s.produce("driver-api", contracts.TypeDriverOnline, driverID, version, contracts.DriverOnline{DriverID: driverID, Position: &pos})
}

func (s *stack) confirm(planID string, routes ...contracts.Route) {
	s.t.Helper()
	s.produce("console-api", contracts.TypePlanConfirmed, planID, 1, contracts.PlanConfirmed{Plan: testkit.Plan(planID, routes...)})
}

func nowRoute(planID string, n int, vt contracts.VehicleType) contracts.Route {
	return testkit.Route(planID, n, vt, testkit.Kemang, 5, time.Now().Add(2*time.Minute))
}

func records(t *testing.T, brokers []string, topic string, want int) []contracts.Envelope {
	t.Helper()
	var out []contracts.Envelope
	for _, rec := range eventbustest.ReadTopic(t, brokers, topic, want) {
		env, err := contracts.Decode(rec.Value)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, env)
	}
	return out
}

func TestEveryMethodRefusesAnonymousAndNonAdminCallers(t *testing.T) {
	s := start(t, nil)
	sessions := platform.NewSessions(platform.NewPgSessionStore(s.pool), time.Hour, testkit.Discard)
	customer, err := sessions.Start(t.Context(), platform.Principal{Role: contracts.RoleCustomer, UserID: "cus-1", Name: "Dina"})
	if err != nil {
		t.Fatal(err)
	}
	driver, err := sessions.Start(t.Context(), platform.Principal{Role: contracts.RoleDriver, UserID: "drv-1", Name: "Budi"})
	if err != nil {
		t.Fatal(err)
	}

	methods := []string{
		"fleet.vehicles.list", "fleet.vehicles.create", "fleet.vehicles.update", "fleet.vehicles.remove",
		"fleet.drivers.list", "fleet.drivers.create", "fleet.drivers.update",
		"plans.list", "plans.get", "plans.cancel", "live.routes", "live.idleDrivers",
		"dispatch.suggest", "dispatch.assign", "dispatch.unassign", "dispatch.autoAssign",
		"settings.get", "settings.update",
		"users.list", "users.get", "users.setBlocked",
		"finance.overview", "finance.topUps", "finance.transactions",
		"notifications.list", "notifications.markRead",
		"ai.copilot", "ai.insights",
	}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			if status, body := s.call("", method, map[string]any{}); status != http.StatusUnauthorized {
				t.Errorf("without a session: %d %s, want 401", status, body)
			}
			for role, token := range map[string]string{"customer": customer, "driver": driver} {
				status, body := s.call(token, method, map[string]any{})
				if status != http.StatusForbidden || !strings.Contains(string(body), `"ditolak"`) {
					t.Errorf("as a %s: %d %s, want 403 ditolak", role, status, body)
				}
			}
		})
	}
	if status, _ := s.call(s.adminToken, "plans.list", map[string]any{}); status != http.StatusOK {
		t.Errorf("the admin was refused: %d", status)
	}
}

func TestMethodsOfOtherServicesAreNotRegistered(t *testing.T) {
	s := start(t, nil)
	for _, method := range []string{
		"ai.readImage", "ai.parseSpeech", "demo.state", "demo.reset", "plans.confirm", "plans.reschedule", "driver.routes", "auth.loginCustomer",
		"account.profile", "wallet.get", "wallet.createTopUp",
	} {
		status, body := s.call(s.adminToken, method, map[string]any{})
		if status != http.StatusNotFound || !strings.Contains(string(body), `"tidak_ditemukan"`) {
			t.Errorf("%s: %d %s, want 404 tidak_ditemukan", method, status, body)
		}
	}
}

func TestFromConfirmedPlanToFinishedRoute(t *testing.T) {
	s := start(t, nil)
	driverID := s.setup(contracts.VehicleTypeMotor, "B 3412 KJR", "081234567801")

	// what the neighbours learn about the driver and the vehicle
	prov := records(t, s.brokers, contracts.TopicDriver, 1)
	if len(prov) != 1 || prov[0].Type != contracts.TypeDriverProvisioned || prov[0].Key != driverID {
		t.Fatalf("driver topic = %+v, want one driver.provisioned keyed by the driver", prov)
	}
	ev, _ := prov[0].Payload[contracts.DriverProvisioned]()
	if ok, err := platform.VerifySecret(driverPIN, ev.PinHash); err != nil || !ok || strings.Contains(string(prov[0].Data), driverPIN) {
		t.Errorf("driver.provisioned must carry an argon2id hash of the PIN and never the PIN: %s", prov[0].Data)
	}
	cars := records(t, s.brokers, contracts.TopicFleet, 2)
	if len(cars) != 2 || cars[0].Key != cars[1].Key || cars[0].Type != contracts.TypeVehicleUpserted {
		t.Errorf("fleet topic = %+v, want two vehicle.upserted of one vehicle (created, then with its driver)", cars)
	}

	// console-api confirms a plan of a customer we know
	s.produce("console-api", contracts.TypeCustomerRegistered, "cus-1", 1, contracts.CustomerRegistered{CustomerID: "cus-1", Name: "Dina Pramesti"})
	s.confirm("pln-1", nowRoute("pln-1", 1, contracts.VehicleTypeMotor))

	plan := s.waitPlan("pln-1", "the plan to reach the queue", func(contracts.Plan) bool { return true })
	if plan.Status != contracts.PlanStatusAwaitingDriver {
		t.Fatalf("plan %s, want menunggu_driver while no driver is online", plan.Status)
	}
	var list []map[string]any
	s.admin("plans.list", map[string]any{"filter": map[string]any{"needsDriver": true}}, &list)
	if len(list) != 1 || list[0]["id"] != "pln-1" || list[0]["routesWithoutDriver"] != float64(1) {
		t.Fatalf("queue = %v, want the plan waiting for a driver", list)
	}
	eventually(t, "the customer's name on the queue row", func() bool {
		s.admin("plans.list", map[string]any{}, &list)
		return len(list) == 1 && list[0]["customerName"] == "Dina Pramesti"
	})
	if got := records(t, s.brokers, contracts.TopicDispatch, 0); len(got) != 0 {
		t.Fatalf("%d dispatch events before any driver is online", len(got))
	}

	// a driver comes online near the pickup: the worker assigns him
	s.goOnline(driverID, 1, 2)
	plan = s.waitPlan("pln-1", "the route to be assigned", func(p contracts.Plan) bool { return p.Status == contracts.PlanStatusAssigned })
	if plan.Routes[0].DriverID != driverID {
		t.Fatalf("route driver %q, want %q", plan.Routes[0].DriverID, driverID)
	}
	assigned := records(t, s.brokers, contracts.TopicDispatch, 1)
	if len(assigned) != 1 || assigned[0].Type != contracts.TypeRouteAssigned || assigned[0].Key != "pln-1:r1" || assigned[0].AggregateVersion != 1 {
		t.Fatalf("dispatch topic = %+v, want exactly one route.assigned for the route at version 1", assigned)
	}
	payload, _ := assigned[0].Payload[contracts.RouteAssigned]()
	if payload.DriverID != driverID || payload.CustomerName != "Dina Pramesti" || payload.PlanCode != "PL-pln-1" {
		t.Errorf("route.assigned = %+v", payload)
	}
	var live []map[string]any
	s.admin("live.routes", map[string]any{}, &live)
	if len(live) != 1 || live[0]["driverName"] != "Budi Santoso" {
		t.Errorf("live routes = %v, want the assigned route with the driver's name", live)
	}
	var drivers []map[string]any
	s.admin("fleet.drivers.list", map[string]any{}, &drivers)
	if len(drivers) != 1 || drivers[0]["status"] != "bertugas" {
		t.Errorf("drivers = %v, want the driver on duty", drivers)
	}

	// driver-api reports the run
	exec := func(version int64, eventType string, data any) {
		s.produce("driver-api", eventType, "pln-1:r1", version, data)
	}
	exec(1, contracts.TypeRouteStarted, contracts.RouteStarted{PlanID: "pln-1", RouteID: "pln-1:r1", DriverID: driverID, StartedAt: contracts.Now()})
	s.waitPlan("pln-1", "the plan to run", func(p contracts.Plan) bool { return p.Status == contracts.PlanStatusRunning })
	exec(2, contracts.TypeStopArrived, contracts.StopArrived{PlanID: "pln-1", RouteID: "pln-1:r1", StopID: "pln-1:r1:s1", ArrivedAt: contracts.Now()})
	completed := contracts.StopCompleted{PlanID: "pln-1", RouteID: "pln-1:r1", StopID: "pln-1:r1:s1", CompletedAt: contracts.Now()}
	exec(3, contracts.TypeStopCompleted, completed)
	exec(3, contracts.TypeStopCompleted, completed) // the same fact twice
	exec(2, contracts.TypeStopArrived, contracts.StopArrived{PlanID: "pln-1", RouteID: "pln-1:r1", StopID: "pln-1:r1:s1", ArrivedAt: contracts.Now()})
	exec(4, contracts.TypeStopArrived, contracts.StopArrived{PlanID: "pln-1", RouteID: "pln-1:r1", StopID: "pln-1:r1:s2", ArrivedAt: contracts.Now()})
	exec(5, contracts.TypeStopCompleted, contracts.StopCompleted{PlanID: "pln-1", RouteID: "pln-1:r1", StopID: "pln-1:r1:s2", CompletedAt: contracts.Now()})
	exec(6, contracts.TypeRouteCompleted, contracts.RouteCompleted{PlanID: "pln-1", RouteID: "pln-1:r1", DriverID: driverID, FinishedAt: contracts.Now()})

	done := s.waitPlan("pln-1", "the plan to finish", func(p contracts.Plan) bool { return p.Status == contracts.PlanStatusDone })
	route := done.Routes[0]
	if route.Status != contracts.RouteStatusDone || route.Stops[0].Status != contracts.StopStatusDone || route.Stops[1].Status != contracts.StopStatusDone || done.FinishedAt.IsZero() {
		t.Errorf("route %s, stops %s/%s, plan finished %v", route.Status, route.Stops[0].Status, route.Stops[1].Status, done.FinishedAt)
	}
	s.admin("fleet.drivers.list", map[string]any{}, &drivers)
	if drivers[0]["status"] != "online" || drivers[0]["completedTrips"] != float64(1) {
		t.Errorf("driver after the run = %v, want online with one trip", drivers[0])
	}
	if n := len(records(t, s.brokers, contracts.TopicDispatch, 0)); n != 1 {
		t.Errorf("%d dispatch events at the end, want still just the one assignment", n)
	}
}

func TestFleetMethodsAnswerInTheFrontendsShapes(t *testing.T) {
	s := start(t, nil)
	driverID := s.setup(contracts.VehicleTypeMotor, "B 3412 KJR", "081234567801")

	var vehicles []contracts.Vehicle
	s.admin("fleet.vehicles.list", map[string]any{}, &vehicles)
	if len(vehicles) != 1 || vehicles[0].DriverID != driverID || vehicles[0].Fare.Base != 6000 {
		t.Fatalf("vehicles = %+v, want the vehicle with its driver and fare", vehicles)
	}
	var updated contracts.Vehicle
	s.admin("fleet.vehicles.update", map[string]any{"id": vehicles[0].ID, "patch": map[string]any{"model": "Yamaha NMAX", "fare": contracts.FareConfig{Base: 7000}}}, &updated)
	if updated.Model != "Yamaha NMAX" || updated.Fare.Base != 7000 || updated.Plate != "B 3412 KJR" {
		t.Errorf("updated = %+v", updated)
	}

	status, body := s.call(s.adminToken, "fleet.vehicles.update", map[string]any{"id": vehicles[0].ID, "patch": map[string]any{"plate": "nonsense"}})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d %s, want 422", status, body)
	}
	var wire struct {
		Code  string `json:"code"`
		Field string `json:"field"`
		Msg   struct {
			Key string `json:"key"`
		} `json:"msg"`
	}
	if err := json.Unmarshal(body, &wire); err != nil || wire.Code != "tidak_valid" || wire.Field != "plate" || wire.Msg.Key != "srv.api.plateInvalid" {
		t.Errorf("error body = %s, want tidak_valid on plate with the catalog key", body)
	}

	var driver map[string]any
	s.admin("fleet.drivers.update", map[string]any{"id": driverID, "patch": map[string]any{"name": "Budi S.", "vehicleId": "", "status": "offline"}}, &driver)
	if driver["name"] != "Budi S." || driver["vehicleId"] != nil || driver["status"] != "offline" {
		t.Errorf("driver = %v, want renamed and without a vehicle", driver)
	}
	s.admin("fleet.vehicles.remove", map[string]any{"id": vehicles[0].ID}, nil)
	s.admin("fleet.vehicles.list", map[string]any{}, &vehicles)
	if len(vehicles) != 0 {
		t.Errorf("%d vehicles after removing the only one", len(vehicles))
	}
}

func TestSettingsToggleStopsAndStartsAutomaticDispatch(t *testing.T) {
	s := start(t, nil)
	driverID := s.setup(contracts.VehicleTypeMotor, "B 3412 KJR", "081234567801")
	var current struct {
		Dispatch struct {
			Auto bool `json:"auto"`
		} `json:"dispatch"`
		AI struct {
			Vision bool `json:"vision"`
		} `json:"ai"`
	}
	s.admin("settings.get", map[string]any{}, &current)
	if !current.Dispatch.Auto || !current.AI.Vision {
		t.Fatalf("settings = %+v, want the defaults (everything on)", current)
	}
	s.admin("settings.update", map[string]any{"patch": map[string]any{"dispatch": map[string]any{"auto": false}}}, &current)
	if current.Dispatch.Auto {
		t.Fatal("update did not switch automatic dispatch off")
	}

	s.confirm("pln-1", nowRoute("pln-1", 1, contracts.VehicleTypeMotor))
	s.goOnline(driverID, 1, 1)
	s.waitPlan("pln-1", "the plan to be projected", func(contracts.Plan) bool { return true })
	eventually(t, "the driver to be online", func() bool {
		var drivers []map[string]any
		s.admin("fleet.drivers.list", map[string]any{}, &drivers)
		return len(drivers) == 1 && drivers[0]["status"] == "online"
	})
	time.Sleep(600 * time.Millisecond) // several sweeps
	if plan, _ := s.plan("pln-1"); plan.Routes[0].DriverID != "" {
		t.Fatalf("driver %q assigned while automatic dispatch is off", plan.Routes[0].DriverID)
	}

	s.admin("settings.update", map[string]any{"patch": map[string]any{"dispatch": map[string]any{"auto": true}}}, nil)
	plan := s.waitPlan("pln-1", "the route to be assigned after switching on", func(p contracts.Plan) bool { return p.Routes[0].DriverID != "" })
	if plan.Routes[0].DriverID != driverID {
		t.Errorf("driver %q, want %q", plan.Routes[0].DriverID, driverID)
	}

	// both switches are on the compacted settings topic, keyed by the group, in order of version
	events := records(t, s.brokers, contracts.TopicSettings, 2)
	if len(events) != 2 || events[0].Key != contracts.SettingsKeyDispatch || events[1].Key != contracts.SettingsKeyDispatch ||
		events[0].AggregateVersion != 1 || events[1].AggregateVersion != 2 {
		t.Fatalf("settings events = %+v, want the dispatch group at versions 1 and 2", events)
	}
	if on, _ := events[1].Payload[contracts.SettingsUpdated](); on.Dispatch == nil || !on.Dispatch.Auto {
		t.Errorf("last settings event = %+v, want auto on", on)
	}
}

func TestWithoutTheWorkerAnAdminAssignsByHand(t *testing.T) {
	s := start(t, func(c *app.Config) { c.DispatchWorker = false })
	driverID := s.setup(contracts.VehicleTypeMotor, "B 3412 KJR", "081234567801")
	s.confirm("pln-1", nowRoute("pln-1", 1, contracts.VehicleTypeMotor))
	s.goOnline(driverID, 1, 1)
	s.waitPlan("pln-1", "the plan to be projected", func(contracts.Plan) bool { return true })
	eventually(t, "the driver to be online", func() bool {
		var drivers []map[string]any
		s.admin("fleet.drivers.list", map[string]any{}, &drivers)
		return len(drivers) == 1 && drivers[0]["status"] == "online"
	})
	time.Sleep(400 * time.Millisecond)
	if plan, _ := s.plan("pln-1"); plan.Routes[0].DriverID != "" {
		t.Fatalf("driver %q assigned with the worker off", plan.Routes[0].DriverID)
	}

	var suggestions []map[string]any
	s.admin("dispatch.suggest", map[string]any{"planId": "pln-1", "routeId": "pln-1:r1"}, &suggestions)
	if len(suggestions) != 1 || suggestions[0]["driverId"] != driverID || suggestions[0]["fits"] != true {
		t.Fatalf("suggestions = %v, want the one driver", suggestions)
	}
	var plan contracts.Plan
	s.admin("dispatch.assign", map[string]any{"planId": "pln-1", "routeId": "pln-1:r1", "driverId": driverID}, &plan)
	if plan.Status != contracts.PlanStatusAssigned || plan.Routes[0].DriverID != driverID {
		t.Fatalf("assigned plan = %s, driver %q", plan.Status, plan.Routes[0].DriverID)
	}
	status, body := s.call(s.adminToken, "dispatch.assign", map[string]any{"planId": "pln-1", "routeId": "pln-1:r1", "driverId": driverID})
	if status != http.StatusConflict || !strings.Contains(string(body), `"bentrok"`) {
		t.Errorf("assigning a taken route: %d %s, want 409 bentrok", status, body)
	}
	s.admin("dispatch.unassign", map[string]any{"planId": "pln-1", "routeId": "pln-1:r1"}, &plan)
	if plan.Status != contracts.PlanStatusAwaitingDriver {
		t.Errorf("plan %s after unassign, want menunggu_driver", plan.Status)
	}
	events := records(t, s.brokers, contracts.TopicDispatch, 2)
	if len(events) != 2 || events[0].Type != contracts.TypeRouteAssigned || events[1].Type != contracts.TypeRouteUnassigned || events[1].AggregateVersion != 2 {
		t.Errorf("dispatch topic = %+v, want route.assigned then route.unassigned at version 2", events)
	}
}

func TestAPoisonEventGoesToTheDeadLetterTopicWithoutStoppingTheConsumer(t *testing.T) {
	s := start(t, nil)
	bad := testkit.Plan("pln-bad", nowRoute("pln-bad", 1, contracts.VehicleTypeMotor))
	bad.Status = contracts.PlanStatusDraft // a draft is never a confirmed plan
	s.produce("console-api", contracts.TypePlanConfirmed, "pln-bad", 1, contracts.PlanConfirmed{Plan: bad})
	s.confirm("pln-good", nowRoute("pln-good", 1, contracts.VehicleTypeMotor))

	s.waitPlan("pln-good", "the valid plan behind the poison one", func(contracts.Plan) bool { return true })
	dead := eventbustest.ReadTopic(t, s.brokers, contracts.DLQTopic(contracts.TopicPlan), 1)
	if len(dead) != 1 || string(dead[0].Key) != "pln-bad" {
		t.Fatalf("dead letters = %d, want the poison plan only", len(dead))
	}
	if _, ok := s.plan("pln-bad"); ok {
		t.Error("the poison plan was projected")
	}
}

func TestTheEventStreamAnnouncesChangesToAdmins(t *testing.T) {
	s := start(t, nil)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, s.base+"/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+s.adminToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status %d, content type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	events := make(chan string, 16)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			if data, ok := strings.CutPrefix(scanner.Text(), "data: "); ok {
				events <- data
			}
		}
	}()

	s.confirm("pln-1", nowRoute("pln-1", 1, contracts.VehicleTypeMotor))
	s.admin("settings.update", map[string]any{"patch": map[string]any{"ai": map[string]any{"voice": false}}}, nil)
	seen := map[string]bool{}
	deadline := time.After(15 * time.Second)
	for !(seen[`{"type":"plans.changed","planId":"pln-1"}`] && seen[`{"type":"settings.changed"}`]) {
		select {
		case data := <-events:
			seen[data] = true
		case <-deadline:
			t.Fatalf("saw only %v, want plans.changed for the new plan and settings.changed", seen)
		}
	}

	anonymous, err := http.Get(s.base + "/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	anonymous.Body.Close()
	if anonymous.StatusCode != http.StatusUnauthorized {
		t.Errorf("the event stream without a token: %d, want 401", anonymous.StatusCode)
	}
}

func TestStartRefusesAnIncompleteConfiguration(t *testing.T) {
	for name, cfg := range map[string]app.Config{
		"no database":                 {KafkaBrokers: []string{"localhost:1"}},
		"no brokers":                  {DatabaseURL: "postgres://x"},
		"an email without a password": {DatabaseURL: "postgres://x", KafkaBrokers: []string{"localhost:1"}, AdminEmail: "a@b.c"},
		"a password without an email": {DatabaseURL: "postgres://x", KafkaBrokers: []string{"localhost:1"}, AdminPassword: "password1"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := app.Start(t.Context(), cfg, testkit.Discard); err == nil {
				t.Error("Start accepted the configuration")
			}
		})
	}
}

func TestConfigFromEnv(t *testing.T) {
	cfg := app.ConfigFromEnv(platform.NewEnvFrom(map[string]string{"DATABASE_URL": "postgres://x", "KAFKA_BROKERS": "a:1, b:2"}))
	if cfg.HTTPAddr != ":8082" || !cfg.DispatchWorker || cfg.SeedDemo || cfg.GroupPrefix != "backoffice" || cfg.SessionTTL != 720*time.Hour ||
		len(cfg.KafkaBrokers) != 2 || cfg.AdminName != "Admin" {
		t.Errorf("defaults = %+v", cfg)
	}
	cfg = app.ConfigFromEnv(platform.NewEnvFrom(map[string]string{
		"DATABASE_URL": "postgres://x", "KAFKA_BROKERS": "a:1", "HTTP_ADDR": ":9", "DISPATCH_WORKER": "false", "SEED_DEMO": "true",
		"ADMIN_EMAIL": "a@b.c", "ADMIN_PASSWORD": "password1", "SESSION_TTL": "1h",
	}))
	if cfg.HTTPAddr != ":9" || cfg.DispatchWorker || !cfg.SeedDemo || cfg.AdminEmail != "a@b.c" || cfg.SessionTTL != time.Hour {
		t.Errorf("configured = %+v", cfg)
	}
	env := platform.NewEnvFrom(nil)
	app.ConfigFromEnv(env)
	if err := env.Err(); err == nil || !strings.Contains(err.Error(), "DATABASE_URL") || !strings.Contains(err.Error(), "KAFKA_BROKERS") {
		t.Errorf("env error = %v, want both missing variables reported", err)
	}
}
