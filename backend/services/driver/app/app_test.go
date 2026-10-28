package app_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus/eventbustest"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform/platformtest"
	"github.com/mseptiaan/loong.lat/backend/pkg/routeplan"
	"github.com/mseptiaan/loong.lat/backend/services/driver/app"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/drivertest"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/tasks"
)

// These tests start the whole service (HTTP, outbox relay, consumers, change hints) on a real Postgres schema
// (TEST_DATABASE_URL; skipped without it) and the in-process Kafka, and drive it as the app and the other
// services would: events in through Kafka, calls over HTTP, events out read from Kafka.

const (
	plan  = "pln-1"
	route = "pln-1:r1"
	stop1 = route + ":s1" // pickup
	stop2 = route + ":s2" // drop-off, drivertest.CodeA
	stop3 = route + ":s3" // drop-off, drivertest.CodeB
)

type service struct {
	t       *testing.T
	brokers []string
	base    string
	pool    *pgxpool.Pool // the service's own database, for looking in
	group   string        // the consumer group prefix
	sent    map[string]int
	app     *app.App
	cancel  context.CancelFunc
	once    sync.Once
	stopErr error
}

// stop shuts the service down, once, and returns what Wait reported.
func (s *service) stop() error {
	s.once.Do(func() {
		s.cancel()
		s.stopErr = s.app.Wait()
	})
	return s.stopErr
}

func start(t *testing.T) *service { return startWith(t, nil) }

// startWith starts the service with the configuration changed by tweak.
func startWith(t *testing.T, tweak func(*app.Config)) *service {
	t.Helper()
	pool := drivertest.Pool(t)
	brokers := eventbustest.NewCluster(t, contracts.TopicDriver, contracts.TopicDispatch, contracts.TopicFleet, contracts.TopicExecution,
		contracts.TopicDriverPosition, contracts.TopicPlan, contracts.TopicSettings)

	u, err := url.Parse(os.Getenv(platformtest.EnvDatabaseURL))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", pool.Config().ConnConfig.RuntimeParams["search_path"])
	u.RawQuery = q.Encode()

	group := fmt.Sprintf("t%d", time.Now().UnixNano())
	cfg := app.Config{
		HTTPAddr: "127.0.0.1:0", DatabaseURL: u.String(), KafkaBrokers: brokers,
		GroupPrefix: group, SessionTTL: time.Hour,
	}
	if tweak != nil {
		tweak(&cfg)
	}
	ctx, cancel := context.WithCancel(t.Context())
	a, err := app.Start(ctx, cfg, drivertest.Logger())
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	s := &service{t: t, brokers: brokers, base: "http://" + a.Addr(), pool: pool, group: group, sent: map[string]int{}, app: a, cancel: cancel}
	t.Cleanup(func() {
		if err := s.stop(); err != nil {
			t.Errorf("the service stopped with %v", err)
		}
	})
	s.warmUp()
	return s
}

// warmUp has every consumer group take one event before the test starts, so the group has joined and a test
// that sends an event early is not at the mercy of the join.
func (s *service) warmUp() {
	s.t.Helper()
	s.provision("drv-warm", "081200000000", "000000")
	s.produce(contracts.TypeRouteAssigned, "pln-warm:r1", 1, assignedNow("pln-warm:r1", "drv-warm"))
	s.produce(contracts.TypeVehicleUpserted, "veh-warm", 1, contracts.VehicleUpserted{Vehicle: contracts.Vehicle{ID: "veh-warm"}})
	// an abort of a request nobody made changes nothing and casts no vote; a settings group nobody reads is skipped
	s.produce(contracts.TypePlanCancelAborted, "pln-warm", 0, contracts.PlanCancelAborted{PlanID: "pln-warm", RequestID: "req-warm"})
	s.produce(contracts.TypeSettingsUpdated, "payments", 1, contracts.SettingsUpdated{Key: "payments"})
	s.eventually("the consumers to join", func() bool {
		return s.count(`SELECT count(*) FROM routes WHERE id = 'pln-warm:r1'`) == 1 && s.count(`SELECT count(*) FROM vehicles WHERE id = 'veh-warm'`) == 1 &&
			s.count(`SELECT count(*) FROM inbox WHERE consumer = $1`, s.group+".plan") == 1 &&
			s.count(`SELECT count(*) FROM inbox WHERE consumer = $1`, s.group+".settings") == 1
	})
}

func (s *service) produce(eventType, key string, version int64, data any) {
	s.t.Helper()
	topic, _ := contracts.TopicFor(eventType)
	eventbustest.Produce(s.t, s.brokers, eventbustest.Record(s.t, "backoffice-api", eventType, key, version, data))
	s.sent[topic]++
}

func (s *service) provision(id, phone, pin string) {
	s.t.Helper()
	s.produce(contracts.TypeDriverProvisioned, id, 1, drivertest.Provisioned(s.t, id, phone, pin))
}

// assignedNow is a route.assigned whose departure is a minute ago, so that starting it is allowed at any time.
func assignedNow(routeID, driverID string) contracts.RouteAssigned {
	e := drivertest.Assigned(routeID, driverID)
	e.Route = routeplan.RebaseStart(e.Route, e.Route.StartPosition, time.Now().Add(-time.Minute))
	e.StartAt = e.Route.StartAt
	return e
}

func (s *service) assign(routeID, driverID string, version int64) {
	s.t.Helper()
	s.produce(contracts.TypeRouteAssigned, routeID, version, assignedNow(routeID, driverID))
}

func (s *service) count(query string, args ...any) int {
	s.t.Helper()
	var n int
	if err := s.pool.QueryRow(s.t.Context(), query, args...).Scan(&n); err != nil {
		s.t.Fatal(err)
	}
	return n
}

func (s *service) eventually(what string, cond func() bool) {
	s.t.Helper()
	deadline := time.Now().Add(45 * time.Second) // generous: a group join under a loaded machine (race detector, Docker) can take a while
	for !cond() {
		if time.Now().After(deadline) {
			s.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// reply is what an RPC call answered.
type reply struct {
	t      *testing.T
	status int
	body   []byte
}

func (s *service) call(token, method string, in any) reply {
	s.t.Helper()
	body := []byte(`{}`)
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			s.t.Fatal(err)
		}
	}
	req, _ := http.NewRequestWithContext(s.t.Context(), http.MethodPost, s.base+"/v1/rpc/"+method, bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
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
	return reply{s.t, resp.StatusCode, data}
}

// ok decodes a successful answer.
func (r reply) ok(v any) {
	r.t.Helper()
	if r.status != http.StatusOK {
		r.t.Fatalf("status %d: %s", r.status, r.body)
	}
	if v != nil {
		if err := json.Unmarshal(r.body, v); err != nil {
			r.t.Fatalf("answer %s: %v", r.body, err)
		}
	}
}

// refused checks the answer is the error with this HTTP status, message key and form field.
func (r reply) refused(status int, key, field string) {
	r.t.Helper()
	var e struct {
		Msg   contracts.Msg `json:"msg"`
		Field string        `json:"field"`
	}
	if err := json.Unmarshal(r.body, &e); err != nil || r.status != status || e.Msg.Key != key || e.Field != field {
		r.t.Fatalf("answer = %d %s, want %d %q field %q", r.status, r.body, status, key, field)
	}
}

// login signs in over HTTP and returns the token.
func (s *service) login(phone, pin string) string {
	s.t.Helper()
	var out struct {
		contracts.Session
		Token string `json:"token"`
	}
	s.call("", "auth.loginDriver", map[string]any{"phone": phone, "pin": pin}).ok(&out)
	if out.Role != contracts.RoleDriver || out.Token == "" {
		s.t.Fatalf("login = %+v", out)
	}
	return out.Token
}

type published struct {
	Type    string
	Key     string
	Version int64
	Data    []byte
}

// published reads what the service put on a topic: the events its test sent there plus extra of its own.
func (s *service) published(topic string, extra int) []published {
	s.t.Helper()
	records := eventbustest.ReadTopic(s.t, s.brokers, topic, s.sent[topic]+extra)
	out := make([]published, 0, len(records))
	for _, rec := range records {
		env, err := contracts.Decode(rec.Value)
		if err != nil {
			s.t.Fatal(err)
		}
		out = append(out, published{env.Type, env.Key, env.AggregateVersion, env.Data})
	}
	return out
}

func only(events []published, eventType string) []published {
	var out []published
	for _, e := range events {
		if e.Type == eventType {
			out = append(out, e)
		}
	}
	return out
}

// stream is the driver's open SSE connection.
type stream struct{ events chan string }

func (s *service) openStream(token string) stream {
	s.t.Helper()
	req, _ := http.NewRequestWithContext(s.t.Context(), http.MethodGet, s.base+"/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req) // returns once the stream is subscribed
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		s.t.Fatalf("events: status %d", resp.StatusCode)
	}
	events := make(chan string, 32)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				events <- data
			}
		}
	}()
	return stream{events}
}

// expect waits for the next event.
func (st stream) expect(t *testing.T, want string) {
	t.Helper()
	select {
	case got := <-st.events:
		if got != want {
			t.Errorf("event = %s, want %s", got, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("no event, want %s", want)
	}
}

// What the app decodes (the frontend's DriverRoute and driver.me result).
type (
	driverRoute struct {
		PlanID       string          `json:"planId"`
		PlanCode     string          `json:"planCode"`
		CustomerName string          `json:"customerName"`
		Route        contracts.Route `json:"route"`
	}
	profile struct {
		Driver  map[string]any     `json:"driver"`
		Vehicle *contracts.Vehicle `json:"vehicle"`
	}
)

// signedUp has driver drv-1 (PIN 123456, vehicle veh-1) with the route assigned to them, and returns a token.
func signedUp(t *testing.T) (*service, string) {
	s := start(t)
	s.provision("drv-1", "0812-3456-7801", "123456")
	s.produce(contracts.TypeVehicleUpserted, "veh-1", 1, contracts.VehicleUpserted{Vehicle: contracts.Vehicle{ID: "veh-1", Type: contracts.VehicleTypeMotor, Plate: "B 1234 ABC", Model: "Vario 160", Status: contracts.VehicleStatusActive}})
	s.assign(route, "drv-1", 1)
	s.eventually("the route and the driver to arrive", func() bool {
		return s.count(`SELECT count(*) FROM routes WHERE id = $1`, route) == 1 && s.count(`SELECT count(*) FROM vehicles WHERE id = 'veh-1'`) == 1
	})
	return s, s.login("+62 812-3456-7801", "123456")
}

func TestADriversDayFromSignInToTheLastStop(t *testing.T) {
	s, token := signedUp(t)
	hints := s.openStream(token)

	// the profile: the driver as provisioned and the vehicle from the fleet
	var me profile
	s.call(token, "driver.me", nil).ok(&me)
	if me.Driver["name"] != "Budi Santoso" || me.Driver["status"] != "offline" || me.Driver["phone"] != "081234567801" || me.Vehicle == nil || me.Vehicle.Plate != "B 1234 ABC" {
		t.Errorf("driver.me = %+v", me)
	}
	var session *contracts.Session
	s.call(token, "auth.session", nil).ok(&session)
	if session == nil || session.UserID != "drv-1" || session.Role != contracts.RoleDriver {
		t.Errorf("auth.session = %+v", session)
	}
	if r := s.call("", "auth.session", nil); string(r.body) != "null" || r.status != http.StatusOK {
		t.Errorf("auth.session without a token = %d %s, want null", r.status, r.body)
	}

	// the task, as the app sees it: no delivery codes
	r := s.call(token, "driver.routes", nil)
	var tasks []driverRoute
	r.ok(&tasks)
	if len(tasks) != 1 || tasks[0].Route.ID != route || tasks[0].PlanCode != "PL-2001" || tasks[0].Route.Status != contracts.RouteStatusAssigned {
		t.Fatalf("driver.routes = %s", r.body)
	}
	assertNoCodes(t, "driver.routes", r.body)

	// presence: going online twice publishes once; the position ping carries no route yet
	s.call(token, "driver.setOnline", map[string]any{"online": true}).ok(nil)
	s.call(token, "driver.setOnline", map[string]any{"online": true}).ok(nil)
	s.call(token, "driver.ping", map[string]any{"position": map[string]float64{"lat": -6.27, "lng": 106.81}, "speedKmh": 0}).ok(nil)

	// the route: start, then arrive and complete every stop; the wrong code first, the repeats too
	var started contracts.Plan
	s.call(token, "driver.startRoute", map[string]any{"planId": plan, "routeId": route}).ok(&started)
	if started.ID != plan || started.Code != "PL-2001" || len(started.Routes) != 1 || started.Routes[0].Status != contracts.RouteStatusRunning || started.Status != contracts.PlanStatusRunning {
		t.Errorf("driver.startRoute = %+v", started)
	}
	s.call(token, "driver.startRoute", map[string]any{"planId": plan, "routeId": route}).ok(nil) // a retry
	s.call(token, "driver.ping", map[string]any{"position": map[string]float64{"lat": -6.268, "lng": 106.812}, "speedKmh": 24.5}).ok(nil)
	s.call(token, "driver.setOnline", map[string]any{"online": false}).refused(http.StatusConflict, "srv.api.finishActiveFirst", "")

	at := func(stop string) map[string]any {
		return map[string]any{"planId": plan, "routeId": route, "stopId": stop}
	}
	with := func(m map[string]any, k string, v any) map[string]any { m[k] = v; return m }
	s.call(token, "driver.complete", at(stop1)).refused(http.StatusConflict, "srv.api.markArrivedFirst", "")
	s.call(token, "driver.arrive", at(stop1)).ok(nil)
	s.call(token, "driver.arrive", at(stop1)).ok(nil)
	s.call(token, "driver.complete", at(stop1)).ok(nil) // a pickup needs no proof
	s.call(token, "driver.complete", at(stop1)).ok(nil)
	s.call(token, "driver.arrive", at(stop3)).refused(http.StatusConflict, "srv.api.finishPreviousStop", "")
	s.call(token, "driver.arrive", at(stop2)).ok(nil)
	s.call(token, "driver.complete", with(at(stop2), "proof", map[string]any{"kind": "otp", "value": "0000"})).refused(http.StatusUnprocessableEntity, "srv.api.otpMismatch", "proof")
	s.call(token, "driver.complete", with(at(stop2), "proof", map[string]any{"kind": "tanpa"})).refused(http.StatusUnprocessableEntity, "srv.api.proofRequired", "proof")
	hints.expect(t, `{"type":"plans.changed","planId":"pln-1"}`) // the app's own actions are hints for its other devices

	s.call(token, "driver.complete", with(at(stop2), "proof", map[string]any{"kind": "otp", "value": drivertest.CodeA})).ok(nil)
	s.call(token, "driver.complete", with(at(stop2), "proof", map[string]any{"kind": "otp", "value": drivertest.CodeA})).ok(nil)
	s.call(token, "driver.arrive", at(stop3)).ok(nil)
	var done contracts.Plan
	s.call(token, "driver.complete", with(at(stop3), "proof", map[string]any{"kind": "foto", "value": "bukti.jpg", "photo": drivertest.Photo()})).ok(&done)
	if done.Status != contracts.PlanStatusDone || done.Routes[0].Status != contracts.RouteStatusDone {
		t.Errorf("after the last stop: plan %s, route %s", done.Status, done.Routes[0].Status)
	}
	assertNoCodes(t, "driver.complete", mustJSON(t, done))

	// what the rest of the system learned: one event per action, versions rising by one
	execution := s.published(contracts.TopicExecution, 8)
	wantTypes := []string{
		contracts.TypeRouteStarted, contracts.TypeStopArrived, contracts.TypeStopCompleted, contracts.TypeStopArrived,
		contracts.TypeStopCompleted, contracts.TypeStopArrived, contracts.TypeStopCompleted, contracts.TypeRouteCompleted,
	}
	if len(execution) != len(wantTypes) {
		t.Fatalf("execution events = %v, want %v", types(execution), wantTypes)
	}
	for i, e := range execution {
		if e.Type != wantTypes[i] || e.Key != route || e.Version != int64(i+1) {
			t.Errorf("event %d = %s key %s v%d, want %s key %s v%d", i, e.Type, e.Key, e.Version, wantTypes[i], route, i+1)
		}
		assertNoCodes(t, e.Type, e.Data)
	}

	presence := s.published(contracts.TopicDriver, 1)
	if online := only(presence, contracts.TypeDriverOnline); len(online) != 1 || online[0].Key != "drv-1" || online[0].Version != 1 {
		t.Errorf("driver.online events = %+v, want one", online)
	}
	pings := s.published(contracts.TopicDriverPosition, 2)
	if len(pings) != 2 {
		t.Fatalf("%d position events, want 2", len(pings))
	}
	var first, second contracts.DriverPosition
	mustDecode(t, pings[0].Data, &first)
	mustDecode(t, pings[1].Data, &second)
	if pings[0].Key != "drv-1" || first.RouteID != "" || second.RouteID != route || second.SpeedKmh != 24.5 {
		t.Errorf("position events = %+v then %+v: the second ping is on the running route", first, second)
	}

	// afterwards: back online with a trip, paid and in the history
	s.call(token, "driver.me", nil).ok(&me)
	if me.Driver["status"] != "online" || me.Driver["completedTrips"] != float64(1) {
		t.Errorf("driver after the route = %+v", me.Driver)
	}
	var history []map[string]any
	s.call(token, "driver.history", nil).ok(&history)
	if len(history) != 1 || history[0]["routeId"] != route || history[0]["earningsRp"] != float64(15000) || history[0]["stops"] != float64(3) {
		t.Errorf("driver.history = %+v", history)
	}
	var earned struct {
		TodayRp    int64 `json:"todayRp"`
		TripsToday int   `json:"tripsToday"`
		ByDay      []any `json:"byDay"`
	}
	s.call(token, "driver.earnings", nil).ok(&earned)
	if earned.TodayRp != 15000 || earned.TripsToday != 1 || len(earned.ByDay) != 7 {
		t.Errorf("driver.earnings = %+v", earned)
	}
	var open []any
	s.call(token, "driver.routes", nil).ok(&open)
	if open == nil || len(open) != 0 {
		t.Errorf("driver.routes after the route = %v, want an empty list", open)
	}

	// signing out ends the session
	s.call(token, "auth.logout", nil).ok(nil)
	s.call(token, "driver.me", nil).refused(http.StatusUnauthorized, "srv.api.signInFirst", "")
}

func TestOnlyTheDriverOfARouteIsServedAndOnlyDriverMethods(t *testing.T) {
	s, token := signedUp(t)
	s.provision("drv-2", "081299999999", "654321")
	s.eventually("the second driver", func() bool { return s.count(`SELECT count(*) FROM drivers WHERE id = 'drv-2'`) == 1 })
	other := s.login("081299999999", "654321")

	// not signed in, or signed in as someone who is not a driver
	s.call("", "driver.me", nil).refused(http.StatusUnauthorized, "srv.api.signInFirst", "")
	s.call("garbage-token", "driver.routes", nil).refused(http.StatusUnauthorized, "srv.api.signInFirst", "")
	customer, err := platform.NewSessions(platform.NewPgSessionStore(s.pool), time.Hour, drivertest.Logger()).
		Start(t.Context(), platform.Principal{Role: contracts.RoleCustomer, UserID: "cus-1", Name: "Dina"})
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"driver.me", "driver.routes", "driver.setOnline", "driver.ping", "driver.history", "driver.earnings"} {
		s.call(customer, method, nil).refused(http.StatusForbidden, "srv.api.roleDenied", "")
	}
	s.call(customer, "driver.startRoute", map[string]any{"planId": plan, "routeId": route}).refused(http.StatusForbidden, "srv.api.roleDenied", "")
	s.call(customer, "notifications.list", nil).refused(http.StatusForbidden, "srv.api.roleDenied", "") // only the driver's own bell is here
	s.call("", "notifications.list", nil).refused(http.StatusUnauthorized, "srv.api.signInFirst", "")
	s.call("", "settings.get", nil).refused(http.StatusUnauthorized, "srv.api.signInFirst", "")

	// another driver cannot touch the route, and sees none of it
	for _, method := range []string{"driver.startRoute"} {
		s.call(other, method, map[string]any{"planId": plan, "routeId": route}).refused(http.StatusForbidden, "srv.api.notYourRoute", "")
	}
	s.call(token, "driver.startRoute", map[string]any{"planId": plan, "routeId": route}).ok(nil)
	stopCall := map[string]any{"planId": plan, "routeId": route, "stopId": stop1}
	s.call(other, "driver.arrive", stopCall).refused(http.StatusForbidden, "srv.api.notYourRoute", "")
	s.call(other, "driver.complete", stopCall).refused(http.StatusForbidden, "srv.api.notYourRoute", "")
	s.call(other, "driver.reportIssue", map[string]any{"planId": plan, "routeId": route, "stopId": stop1, "issue": "tidak_ada_orang"}).refused(http.StatusForbidden, "srv.api.notYourRoute", "")
	var theirs []any
	s.call(other, "driver.routes", nil).ok(&theirs)
	if len(theirs) != 0 {
		t.Errorf("the other driver lists %d routes", len(theirs))
	}
	if n := len(s.published(contracts.TopicExecution, 1)); n != 1 {
		t.Errorf("%d execution events, want only the owner's route.started", n)
	}

	// methods of the other services do not exist here
	for _, method := range []string{"plans.list", "fleet.vehicles.list", "wallet.get", "driver.suggest", "auth.loginCustomer", "settings.update", "ai.copilot"} {
		s.call(token, method, nil).refused(http.StatusNotFound, platform.MsgUnknownMethod, "")
	}
}

func TestSigningInNeedsTheRightPINAndIsRateLimited(t *testing.T) {
	s := start(t)
	s.provision("drv-1", "081234567801", "123456")
	s.eventually("the driver", func() bool { return s.count(`SELECT count(*) FROM drivers WHERE id = 'drv-1'`) == 1 })

	attempt := func(pin string) reply {
		return s.call("", "auth.loginDriver", map[string]any{"phone": "0812-3456-7801", "pin": pin})
	}
	for range 5 {
		attempt("000000").refused(http.StatusUnprocessableEntity, "srv.api.driverBadCredentials", "")
	}
	limited := attempt("123456") // right PIN, but too many attempts
	limited.refused(http.StatusTooManyRequests, platform.MsgTooManyRequest, "")
	if s.count(`SELECT count(*) FROM sessions`) != 0 {
		t.Error("a session was started for refused attempts")
	}
	other := s.call("", "auth.loginDriver", map[string]any{"phone": "081255555555", "pin": "000000"})
	other.refused(http.StatusUnprocessableEntity, "srv.api.driverBadCredentials", "")
}

func TestAnAssignmentThatArrivesBeforeItsDriverIsRetriedUntilTheDriverExists(t *testing.T) {
	s := start(t)
	s.assign("pln-7:r1", "drv-7", 1)
	time.Sleep(300 * time.Millisecond) // at least one attempt fails: the driver is not known
	if n := s.count(`SELECT count(*) FROM routes WHERE id = 'pln-7:r1'`); n != 0 {
		t.Fatalf("%d tasks for an unknown driver", n)
	}
	s.provision("drv-7", "081277777777", "123456")
	s.eventually("the retried assignment", func() bool { return s.count(`SELECT count(*) FROM routes WHERE id = 'pln-7:r1'`) == 1 })
}

func TestDispatchCannotWithdrawOrOverwriteARouteThatRuns(t *testing.T) {
	s, token := signedUp(t)
	s.call(token, "driver.startRoute", map[string]any{"planId": plan, "routeId": route}).ok(nil)
	s.provision("drv-2", "081299999999", "654321")

	// dispatch takes the route back and gives it to someone else: both are ignored. A sentinel route behind
	// them shows they were processed.
	s.produce(contracts.TypeRouteUnassigned, route, 2, contracts.RouteUnassigned{PlanID: plan, RouteID: route, DriverID: "drv-1"})
	s.assign(route, "drv-2", 3)
	s.assign("pln-8:r1", "drv-1", 1)
	s.eventually("the sentinel", func() bool { return s.count(`SELECT count(*) FROM routes WHERE id = 'pln-8:r1'`) == 1 })

	var routes []driverRoute
	s.call(token, "driver.routes", nil).ok(&routes)
	running := 0
	for _, r := range routes {
		if r.Route.ID == route && r.Route.Status == contracts.RouteStatusRunning && r.Route.DriverID == "drv-1" {
			running++
		}
	}
	if running != 1 {
		t.Errorf("routes = %+v: the running route must stay with its driver", routes)
	}
	if n := s.count(`SELECT count(*) FROM routes WHERE id = $1 AND driver_id = 'drv-1' AND status = 'berjalan' AND assign_version = 1`, route); n != 1 {
		t.Error("the running route changed")
	}
}

func TestAnOlderAssignmentDoesNotOverwriteANewerOneAndUnassignmentRemovesTheTask(t *testing.T) {
	s, token := signedUp(t)

	newer := assignedNow(route, "drv-1")
	newer.Route.CostRp = 30000
	older := assignedNow(route, "drv-1")
	older.Route.CostRp = 11111
	s.produce(contracts.TypeRouteAssigned, route, 5, newer)
	s.produce(contracts.TypeRouteAssigned, route, 2, older)
	s.assign("pln-8:r1", "drv-1", 1)
	s.eventually("the sentinel", func() bool { return s.count(`SELECT count(*) FROM routes WHERE id = 'pln-8:r1'`) == 1 })
	if n := s.count(`SELECT count(*) FROM routes WHERE id = $1 AND cost_rp = 30000 AND assign_version = 5`, route); n != 1 {
		t.Error("the version 2 assignment overwrote version 5")
	}

	s.produce(contracts.TypeRouteUnassigned, route, 6, contracts.RouteUnassigned{PlanID: plan, RouteID: route, DriverID: "drv-1"})
	s.eventually("the unassignment", func() bool {
		return s.count(`SELECT count(*) FROM routes WHERE id = $1 AND driver_id IS NULL`, route) == 1
	})
	var routes []driverRoute
	s.call(token, "driver.routes", nil).ok(&routes)
	for _, r := range routes {
		if r.Route.ID == route {
			t.Errorf("the unassigned route is still a task: %+v", r)
		}
	}
	s.call(token, "driver.startRoute", map[string]any{"planId": plan, "routeId": route}).refused(http.StatusForbidden, "srv.api.notYourRoute", "")
}

func TestNewTasksAndProfileChangesAreHintedToTheDriversStream(t *testing.T) {
	s, token := signedUp(t)
	hints := s.openStream(token)

	s.assign("pln-2:r1", "drv-1", 1)
	hints.expect(t, `{"type":"plans.changed","planId":"pln-2"}`)
	hints.expect(t, `{"type":"notifications.changed"}`) // the new job is in the bell

	s.produce(contracts.TypeDriverUpdated, "drv-1", 2, contracts.DriverUpdated{DriverID: "drv-1", Name: "Budi S.", Phone: "081234567801", VehicleID: "veh-1"})
	hints.expect(t, `{"type":"fleet.changed"}`)
	var me profile
	s.call(token, "driver.me", nil).ok(&me)
	if me.Driver["name"] != "Budi S." {
		t.Errorf("driver.me after the update = %+v", me.Driver)
	}

	s.produce(contracts.TypeVehicleRemoved, "veh-1", 2, contracts.VehicleRemoved{VehicleID: "veh-1"})
	hints.expect(t, `{"type":"fleet.changed"}`)
	var gone profile
	s.call(token, "driver.me", nil).ok(&gone)
	if gone.Vehicle != nil {
		t.Errorf("driver.me still shows the removed vehicle %+v", gone.Vehicle)
	}

	// a PIN reset signs the driver out
	reset := drivertest.Provisioned(t, "drv-1", "081234567801", "999999").PinHash
	s.produce(contracts.TypeDriverUpdated, "drv-1", 3, contracts.DriverUpdated{DriverID: "drv-1", Name: "Budi S.", Phone: "081234567801", PinHash: reset})
	s.eventually("the session to end", func() bool {
		return s.call(token, "driver.me", nil).status == http.StatusUnauthorized
	})
	s.login("081234567801", "999999")
}

func TestAMessageNoRetryCanFixGoesToTheDeadLetterTopicAndTheConsumerGoesOn(t *testing.T) {
	s := start(t)
	bad := drivertest.Provisioned(t, "drv-bad", "081211111111", "123456")
	bad.PinHash = "123456" // a plain PIN: never valid
	s.produce(contracts.TypeDriverProvisioned, "drv-bad", 1, bad)
	s.provision("drv-ok", "081222222222", "123456")
	s.eventually("the driver behind the bad message", func() bool { return s.count(`SELECT count(*) FROM drivers WHERE id = 'drv-ok'`) == 1 })

	if n := s.count(`SELECT count(*) FROM drivers WHERE id = 'drv-bad'`); n != 0 {
		t.Errorf("the plain PIN was accepted")
	}
	records := eventbustest.ReadTopic(t, s.brokers, contracts.DLQTopic(contracts.TopicDriver), 1)
	if len(records) != 1 || !strings.Contains(string(records[0].Value), "drv-bad") {
		t.Errorf("dead letters = %d, want the bad provisioning", len(records))
	}
}

func TestTheServiceAnswersItsProbesAndStopsGracefully(t *testing.T) {
	s := start(t)
	for _, path := range []string{"/healthz", "/readyz"} {
		resp, err := http.Get(s.base + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s = %d", path, resp.StatusCode)
		}
	}

	done := make(chan error, 1)
	go func() { done <- s.stop() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Wait = %v, want nil after a clean stop", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the service did not stop")
	}
	if resp, err := http.Get(s.base + "/healthz"); err == nil {
		resp.Body.Close()
		t.Error("the service still answers after it stopped")
	}
}

func TestConfigFromEnv(t *testing.T) {
	cfg := app.ConfigFromEnv(platform.NewEnvFrom(map[string]string{
		"DATABASE_URL": "postgres://driver@localhost/driver", "KAFKA_BROKERS": "a:9092, b:9092", "SESSION_TTL": "48h",
	}))
	if cfg.HTTPAddr != ":8081" || cfg.DatabaseURL != "postgres://driver@localhost/driver" || cfg.SessionTTL != 48*time.Hour ||
		len(cfg.KafkaBrokers) != 2 || cfg.KafkaBrokers[1] != "b:9092" {
		t.Errorf("config = %+v", cfg)
	}
	if cfg.AIURL != "" || cfg.DelaySweepInterval != 30*time.Second || cfg.GroupPrefix != "" {
		t.Errorf("config = %+v, want no AI gateway, a 30s delay sweep and the default group prefix", cfg)
	}

	cfg = app.ConfigFromEnv(platform.NewEnvFrom(map[string]string{
		"DATABASE_URL": "postgres://driver@localhost/driver", "KAFKA_BROKERS": "a:9092",
		"AI_URL": "http://ai:8091", "DELAY_SWEEP_INTERVAL": "5s", "GROUP_PREFIX": "driver-b",
	}))
	if cfg.AIURL != "http://ai:8091" || cfg.DelaySweepInterval != 5*time.Second || cfg.GroupPrefix != "driver-b" {
		t.Errorf("config = %+v", cfg)
	}

	env := platform.NewEnvFrom(map[string]string{"HTTP_ADDR": ":9000", "SESSION_TTL": "soon", "DELAY_SWEEP_INTERVAL": "often"})
	cfg = app.ConfigFromEnv(env)
	if cfg.HTTPAddr != ":9000" || env.Err() == nil {
		t.Errorf("config = %+v, err = %v; want the address and the problems with DATABASE_URL, SESSION_TTL and DELAY_SWEEP_INTERVAL", cfg, env.Err())
	}
}

func TestStartNeedsADatabaseAndBrokers(t *testing.T) {
	log := drivertest.Logger()
	for name, cfg := range map[string]app.Config{
		"nothing":     {},
		"no brokers":  {DatabaseURL: "postgres://x@localhost/x"},
		"no database": {KafkaBrokers: []string{"localhost:9092"}},
	} {
		if _, err := app.Start(t.Context(), cfg, log); err == nil {
			t.Errorf("%s: started", name)
		}
	}
}

// assertNoCodes fails if data mentions the delivery codes of the fixtures or the field that holds them.
func assertNoCodes(t *testing.T, what string, data []byte) {
	t.Helper()
	for _, secret := range []string{drivertest.CodeA, drivertest.CodeB, "deliveryCode"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Errorf("%s leaks %q", what, secret)
		}
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustDecode(t *testing.T, data []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}
}

func types(events []published) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Type
	}
	return out
}

// requestCancel is the event console publishes to start a cancel.
func (s *service) requestCancel(requestID string, routeIDs ...string) {
	s.t.Helper()
	s.produce(contracts.TypePlanCancelRequested, plan, 0, contracts.PlanCancelRequested{
		PlanID: plan, RequestID: requestID, RequestedBy: contracts.CancelByCustomer, RouteIDs: routeIDs,
	})
}

// votes reads the votes driver-api put on the plan topic (the events the test produced come first).
func (s *service) votes(want int) []published {
	s.t.Helper()
	var out []published
	for _, e := range s.published(contracts.TopicPlan, want) {
		if e.Type == contracts.TypePlanCancelAccepted || e.Type == contracts.TypePlanCancelRejected {
			out = append(out, e)
		}
	}
	return out
}

func (s *service) bell(token string) []contracts.AppNotification {
	s.t.Helper()
	var list []contracts.AppNotification
	s.call(token, "notifications.list", nil).ok(&list)
	return list
}

func TestAPlanCancelThroughKafkaVotesHoldsCommitsAndTellsTheDriver(t *testing.T) {
	s, token := signedUp(t)
	hints := s.openStream(token)
	other := "pln-1:r2" // the plan's second route: its assignment has not reached the driver service
	startArg := map[string]any{"planId": plan, "routeId": route}

	// a new task is in the bell; the app was hinted to refetch both
	eventuallyBell := func(want int) []contracts.AppNotification {
		var list []contracts.AppNotification
		s.eventually("the notifications", func() bool { list = s.bell(token); return len(list) >= want })
		return list
	}
	if list := eventuallyBell(1); list[0].Title.Key != "srv.ops.ntf.newJobTitle" || list[0].Body.Params["code"] != "PL-2001" || list[0].Audience != "driver:drv-1" {
		t.Errorf("the first notification = %+v", list[0])
	}

	// phase 1: one vote per route, both yes: the known route is held, the unknown one has a tombstone
	s.requestCancel("req-1", route, other)
	votes := s.votes(2)
	if len(votes) != 2 {
		t.Fatalf("votes = %+v, want two", votes)
	}
	for _, v := range votes {
		var accepted contracts.PlanCancelAccepted
		mustDecode(t, v.Data, &accepted)
		if v.Type != contracts.TypePlanCancelAccepted || v.Key != plan || v.Version != 0 || accepted.PlanID != plan || accepted.RequestID != "req-1" || (accepted.RouteID != route && accepted.RouteID != other) {
			t.Errorf("vote = %+v (%+v)", v, accepted)
		}
	}
	s.call(token, "driver.startRoute", startArg).refused(http.StatusConflict, "srv.api.routeCancelling", "")
	s.assign(other, "drv-1", 1) // the late assignment of the second route
	s.assign("pln-8:r1", "drv-1", 1)
	s.eventually("the sentinel", func() bool { return s.count(`SELECT count(*) FROM routes WHERE id = 'pln-8:r1'`) == 1 })
	if n := s.count(`SELECT count(*) FROM routes WHERE id = $1`, other); n != 0 {
		t.Error("the assignment of a route that was voted away created a task")
	}

	// phase 2, commit: the held task is closed and leaves the list; the driver is told and the app is hinted
	s.produce(contracts.TypePlanCancelled, plan, 0, contracts.PlanCancelled{PlanID: plan, RequestID: "req-1", RequestedBy: contracts.CancelByCustomer, Reason: "salah alamat"})
	s.eventually("the task to go", func() bool {
		var routes []driverRoute
		s.call(token, "driver.routes", nil).ok(&routes)
		return len(routes) == 1 && routes[0].Route.ID == "pln-8:r1"
	})
	var cancelled *contracts.AppNotification
	for _, n := range eventuallyBell(3) {
		if n.Title.Key == "srv.api.notif.taskCancelled.title" {
			cancelled = &n
		}
	}
	if cancelled == nil || cancelled.Body.Key != "srv.api.notif.taskCancelled.bodyReason" || cancelled.Body.Params["reason"] != "salah alamat" || cancelled.Body.Params["code"] != "PL-2001" {
		t.Errorf("cancellation notification = %+v", cancelled)
	}
	seen := map[string]bool{}
	for range 6 { // plans.changed for the task and the other plan, notifications.changed for each entry
		select {
		case e := <-hints.events:
			seen[e] = true
		case <-time.After(5 * time.Second):
		}
	}
	if !seen[`{"type":"notifications.changed"}`] || !seen[`{"type":"plans.changed","planId":"pln-1"}`] {
		t.Errorf("hints seen = %v, want notifications.changed and plans.changed for the plan", seen)
	}
	s.call(token, "driver.startRoute", startArg).refused(http.StatusNotFound, "srv.ops.err.routeNotFound", "")
	if got := s.count(`SELECT count(*) FROM route_tombstones WHERE request_id = 'req-1'`); got != 1 {
		t.Errorf("%d tombstones after the commit, want it kept", got)
	}
}

func TestAnAbortedCancelFreesTheRouteToStartAndAStartedRouteVotesNo(t *testing.T) {
	s, token := signedUp(t)
	s.assign("pln-3:r1", "drv-1", 1)
	s.eventually("the second task", func() bool { return s.count(`SELECT count(*) FROM routes WHERE id = 'pln-3:r1'`) == 1 })

	// pln-3:r1 starts first, so a cancel of its plan is refused by the vote; pln-1:r1 is held by another cancel
	s.call(token, "driver.startRoute", map[string]any{"planId": "pln-3", "routeId": "pln-3:r1"}).ok(nil)
	s.produce(contracts.TypePlanCancelRequested, "pln-3", 0, contracts.PlanCancelRequested{PlanID: "pln-3", RequestID: "req-run", RouteIDs: []string{"pln-3:r1"}})
	s.requestCancel("req-1", route)
	votes := s.votes(2)
	byRoute := map[string]published{}
	for _, v := range votes {
		var p struct {
			RouteID string        `json:"routeId"`
			Reason  contracts.Msg `json:"reason"`
		}
		mustDecode(t, v.Data, &p)
		byRoute[p.RouteID] = v
		if p.RouteID == "pln-3:r1" && (v.Type != contracts.TypePlanCancelRejected || p.Reason.Key != "srv.api.cancelRunning" || v.Key != "pln-3") {
			t.Errorf("the vote for a running route = %+v %+v, want a rejection with the mock's key, keyed by its plan", v, p)
		}
	}
	if len(byRoute) != 2 || byRoute[route].Type != contracts.TypePlanCancelAccepted {
		t.Fatalf("votes = %+v", votes)
	}
	s.call(token, "driver.startRoute", map[string]any{"planId": plan, "routeId": route}).refused(http.StatusConflict, "srv.api.routeCancelling", "")

	s.produce(contracts.TypePlanCancelAborted, plan, 0, contracts.PlanCancelAborted{PlanID: plan, RequestID: "req-1", Reason: contracts.Msg{Key: "srv.api.cancelAborted"}})
	s.eventually("the route to be startable again", func() bool {
		return s.call(token, "driver.startRoute", map[string]any{"planId": plan, "routeId": route}).status == http.StatusOK
	})
	if n := len(s.bell(token)); n != 2 {
		t.Errorf("%d notifications, want only the two new jobs: an abort tells the driver nothing", n)
	}
}

func TestUnassignmentTellsTheDriverWhyAndTheAppIsHinted(t *testing.T) {
	s, token := signedUp(t)
	hints := s.openStream(token)
	s.produce(contracts.TypeRouteUnassigned, route, 2, contracts.RouteUnassigned{PlanID: plan, RouteID: route, DriverID: "drv-1", Reason: contracts.UnassignDriverSilent})
	hints.expect(t, `{"type":"plans.changed","planId":"pln-1"}`)
	hints.expect(t, `{"type":"notifications.changed"}`)
	var found bool
	for _, n := range s.bell(token) {
		if n.Title.Key == "srv.api.notif.taskSilent.title" && n.Body.Key == "srv.api.notif.taskSilent.body" && n.Body.Params["code"] == "PL-2001" {
			found = true
		}
	}
	if !found {
		t.Errorf("the bell has no notice for the silent driver: %+v", s.bell(token))
	}
}

func TestMarkingNotificationsReadStaysWithinTheDriversOwnBell(t *testing.T) {
	s, token := signedUp(t)
	s.provision("drv-2", "081299999999", "654321")
	s.assign("pln-5:r1", "drv-1", 1)
	s.eventually("both tasks", func() bool { return s.count(`SELECT count(*) FROM routes WHERE id = 'pln-5:r1'`) == 1 })
	s.assign("pln-6:r1", "drv-2", 1)
	s.eventually("the other driver's task", func() bool { return s.count(`SELECT count(*) FROM routes WHERE id = 'pln-6:r1'`) == 1 })
	other := s.login("081299999999", "654321")

	list := s.bell(token)
	if len(list) != 2 {
		t.Fatalf("%d notifications, want two new jobs", len(list))
	}
	s.call(token, "notifications.markRead", map[string]any{"ids": []string{list[0].ID}}).ok(nil)
	after := s.bell(token)
	if !after[0].Read || after[1].Read {
		t.Errorf("markRead(ids) = %+v: only the named one is read", after)
	}
	for _, n := range s.bell(other) {
		if n.Audience != "driver:drv-2" || n.Read {
			t.Errorf("the other driver's bell was touched or leaked: %+v", n)
		}
	}
	s.call(token, "notifications.markRead", nil).ok(nil)
	for _, n := range s.bell(token) {
		if !n.Read {
			t.Errorf("markRead() left %+v unread", n)
		}
	}
	if theirs := s.bell(other); len(theirs) != 1 || theirs[0].Read {
		t.Errorf("the other driver's bell = %+v, want their one new job, unread", theirs)
	}
}

func TestSettingsAreServedFromTheTopicAndAIStaysOffWithoutAGateway(t *testing.T) {
	type answer struct {
		AI       contracts.AIFeatures       `json:"ai"`
		Dispatch contracts.DispatchSettings `json:"dispatch"`
	}
	allOn := contracts.AIFeatures{Vision: true, Voice: true, Intent: true, Copilot: true}

	for name, gateway := range map[string]string{"without a gateway": "", "with a gateway": "http://ai.test:8091"} {
		t.Run(name, func(t *testing.T) {
			s := startWith(t, func(c *app.Config) { c.AIURL = gateway })
			s.provision("drv-1", "0812-3456-7801", "123456")
			s.eventually("the driver", func() bool { return s.count(`SELECT count(*) FROM drivers WHERE id = 'drv-1'`) == 1 })
			token := s.login("0812-3456-7801", "123456")

			// nothing announced: the frontend's defaults, the AI ones only if there is something to call
			var got answer
			s.call(token, "settings.get", nil).ok(&got)
			wantAI := allOn
			if gateway == "" {
				wantAI = contracts.AIFeatures{}
			}
			if got.AI != wantAI || !got.Dispatch.Auto {
				t.Errorf("defaults = %+v, want ai %+v and automatic dispatch", got, wantAI)
			}

			// the admin switches voice off and dispatch off; an older version afterwards changes nothing
			s.produce(contracts.TypeSettingsUpdated, contracts.SettingsKeyAI, 2, contracts.SettingsUpdated{Key: contracts.SettingsKeyAI, AI: &contracts.AIFeatures{Vision: true, Intent: true, Copilot: true}})
			s.produce(contracts.TypeSettingsUpdated, contracts.SettingsKeyAI, 1, contracts.SettingsUpdated{Key: contracts.SettingsKeyAI, AI: &allOn})
			s.produce(contracts.TypeSettingsUpdated, contracts.SettingsKeyDispatch, 1, contracts.SettingsUpdated{Key: contracts.SettingsKeyDispatch, Dispatch: &contracts.DispatchSettings{Auto: false}})
			s.eventually("the settings", func() bool {
				s.call(token, "settings.get", nil).ok(&got)
				return !got.Dispatch.Auto
			})
			wantAI = contracts.AIFeatures{Vision: true, Intent: true, Copilot: true}
			if gateway == "" {
				wantAI = contracts.AIFeatures{}
			}
			if got.AI != wantAI {
				t.Errorf("ai = %+v, want %+v (admin AND gateway)", got.AI, wantAI)
			}

			// an event that carries no value can never be applied: it goes to the dead letters, the consumer goes on
			s.produce(contracts.TypeSettingsUpdated, contracts.SettingsKeyAI, 9, contracts.SettingsUpdated{Key: contracts.SettingsKeyAI})
			if dead := eventbustest.ReadTopic(t, s.brokers, contracts.DLQTopic(contracts.TopicSettings), 1); len(dead) != 1 {
				t.Errorf("%d dead letters, want the settings event without its value", len(dead))
			}
		})
	}
}

func TestARunningRouteThatFallsBehindIsReportedOnceThroughKafka(t *testing.T) {
	s := startWith(t, func(c *app.Config) { c.DelaySweepInterval = 50 * time.Millisecond })
	s.provision("drv-1", "0812-3456-7801", "123456")
	s.assign(route, "drv-1", 1)
	s.eventually("the route", func() bool { return s.count(`SELECT count(*) FROM routes WHERE id = $1`, route) == 1 })
	token := s.login("0812-3456-7801", "123456")
	s.call(token, "driver.startRoute", map[string]any{"planId": plan, "routeId": route}).ok(nil)

	// on time: nothing reported however often the sweeper looks
	time.Sleep(400 * time.Millisecond)
	if n := len(only(s.published(contracts.TopicExecution, 1), contracts.TypeRouteDelayed)); n != 0 {
		t.Fatalf("%d route.delayed for a route that is on time", n)
	}

	// the first stop's ETA was 8 minutes ago: the next sweep reports it, and the later ones do not repeat it
	task, err := tasks.Lock(t.Context(), s.pool, route)
	if err != nil {
		t.Fatal(err)
	}
	task.Route.Stops[0].ArriveAt = contracts.At(time.Now().Add(-8 * time.Minute))
	if err := tasks.Put(t.Context(), s.pool, task, time.Now()); err != nil {
		t.Fatal(err)
	}
	s.eventually("route.delayed", func() bool { return s.count(`SELECT count(*) FROM outbox WHERE event_type = 'route.delayed'`) == 1 })
	time.Sleep(400 * time.Millisecond)
	delayed := only(s.published(contracts.TopicExecution, 2), contracts.TypeRouteDelayed)
	if len(delayed) != 1 {
		t.Fatalf("%d route.delayed events, want exactly one", len(delayed))
	}
	var e contracts.RouteDelayed
	mustDecode(t, delayed[0].Data, &e)
	if delayed[0].Key != route || delayed[0].Version != 2 || e.PlanID != plan || e.RouteID != route || e.DelayMin != 8 {
		t.Errorf("event = %+v %+v, want 8 minutes for the route as execution version 2", delayed[0], e)
	}
}
