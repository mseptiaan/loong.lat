package ai

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/pkg/routeplan"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/changes"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/dispatch"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/finance"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/fleet"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/plans"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/settings"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/testkit"
)

// These tests need a real Postgres (TEST_DATABASE_URL) and skip without one.

// now is 19:00 in Jakarta on 21 October 2026: "today" began at 17:00 UTC the evening before.
var now = time.Date(2026, time.October, 21, 12, 0, 0, 0, time.UTC)

func clock() time.Time { return now }

// deliveryCode is on every drop-off of the seeded plans.
const deliveryCode = "482913"

func ago(d time.Duration) time.Time { return now.Add(-d) }

// rig is the backoffice's services on a fresh schema, with a clock that stands still at now.
type rig struct {
	t          *testing.T
	pool       *pgxpool.Pool
	pub        *eventbus.Publisher
	fleet      *fleet.Service
	dispatch   *dispatch.Service
	plans      *plans.Service
	finance    *finance.Service
	settings   *settings.Service
	projection *plans.Projection

	presence map[string]int64
	vehicles map[string]string // driver id -> vehicle id
}

func newRig(t *testing.T) *rig {
	t.Helper()
	pool := testkit.Pool(t)
	feed := changes.New(platform.NewHub(), time.Millisecond)
	pub := testkit.Publisher(pool)
	f := fleet.New(pool, pub, feed, clock, dispatch.CommitLead)
	d := dispatch.New(pool, pub, f, feed, testkit.Discard, clock)
	return &rig{
		t: t, pool: pool, pub: pub, fleet: f, dispatch: d,
		plans: plans.NewService(pool, pub, feed, clock), finance: finance.New(pool, feed, clock),
		settings: settings.New(pool, pub, feed, d.Kick), projection: plans.NewProjection(feed, pub),
		presence: map[string]int64{}, vehicles: map[string]string{},
	}
}

// service builds the AI service over the rig with the given model (nil: no gateway).
func (r *rig) service(model aiapi.Model, tweak ...func(*Options)) *Service {
	o := Options{
		Model: model, Pool: r.pool, Plans: r.plans, Fleet: r.fleet, Dispatch: r.dispatch, Finance: r.finance,
		Log: testkit.Discard, Now: clock, Burst: 100, RatePerMinute: 6000,
	}
	for _, fn := range tweak {
		fn(&o)
	}
	return New(o)
}

// feed builds an event as its producer would have published it at the given time.
func feedEvent(t *testing.T, eventType, key string, version int64, at time.Time, data any) contracts.Envelope {
	t.Helper()
	env, err := contracts.NewEnvelope("test", eventType, key, version, data)
	if err != nil {
		t.Fatal(err)
	}
	env.OccurredAt = contracts.At(at)
	return env
}

func (r *rig) apply(h eventbus.Handler, eventType, key string, version int64, data any) {
	r.t.Helper()
	if err := testkit.ApplyEnvelope(r.pool, h, feedEvent(r.t, eventType, key, version, now, data)); err != nil {
		r.t.Fatalf("%s: %v", eventType, err)
	}
}

func (r *rig) customer(id, name string) {
	r.t.Helper()
	r.apply(r.projection.CustomerRegistered, contracts.TypeCustomerRegistered, id, 1, contracts.CustomerRegistered{CustomerID: id, Name: name})
}

// driver creates a driver with a vehicle of the type; an online one reports a position km north of Kemang.
func (r *rig) driver(name string, vt contracts.VehicleType, plate string, km float64, online bool) string {
	r.t.Helper()
	capacity := map[contracts.VehicleType][3]float64{contracts.VehicleTypeMotor: {20, 0.12, 55}, contracts.VehicleTypeBlindvan: {500, 3.2, 190}}[vt]
	v, err := r.fleet.CreateVehicle(r.t.Context(), fleet.VehicleInput{
		Type: vt, Plate: plate, Model: "Model " + string(vt), MaxWeightKg: capacity[0], MaxVolumeM3: capacity[1],
		MaxDimensionCm: capacity[2], Status: contracts.VehicleStatusActive,
	})
	if err != nil {
		r.t.Fatal(err)
	}
	d, err := r.fleet.CreateDriver(r.t.Context(), fleet.DriverInput{
		Name: name, Phone: fmt.Sprintf("0812%08d", len(r.vehicles)+1), VehicleID: v.ID, PIN: "123456",
	})
	if err != nil {
		r.t.Fatal(err)
	}
	r.vehicles[d.ID] = v.ID
	if online {
		pos := testkit.North(testkit.Kemang, km)
		r.presence[d.ID]++
		r.apply(r.fleet.Online, contracts.TypeDriverOnline, d.ID, r.presence[d.ID], contracts.DriverOnline{DriverID: d.ID, Position: &pos})
	}
	return d.ID
}

// planSpec describes a confirmed plan to seed.
type planSpec struct {
	id, code, customer string
	created            time.Time
	// scheduledFor makes a scheduled plan that departs then; zero is a "now" plan.
	scheduledFor time.Time
	pickup       string
	routes       []routeSpec
}

type routeSpec struct {
	vehicle  contracts.VehicleType
	weightKg float64
	// pickupKm is how far north of Kemang the pickup is (negative: south).
	pickupKm float64
}

func (r *rig) confirm(p planSpec) contracts.Plan {
	r.t.Helper()
	start := now.Add(2 * time.Minute)
	if !p.scheduledFor.IsZero() {
		start = p.scheduledFor
	}
	var routes []contracts.Route
	for i, rs := range p.routes {
		route := testkit.Route(p.id, i+1, rs.vehicle, testkit.North(testkit.Kemang, rs.pickupKm), rs.weightKg, start)
		if p.pickup != "" {
			route.Stops[0].Waypoint.Name = p.pickup
		}
		route.Stops[1].DeliveryCode = deliveryCode // the OTP the recipient gives the driver: no tool may ever show it
		routes = append(routes, route)
	}
	plan := testkit.Plan(p.id, routes...)
	plan.Code, plan.UserID = p.code, cmp.Or(p.customer, "cus-1")
	plan.CreatedAt, plan.UpdatedAt, plan.ConfirmedAt = contracts.At(p.created), contracts.At(p.created), contracts.At(p.created)
	if !p.scheduledFor.IsZero() {
		plan.Schedule = contracts.Schedule{Mode: contracts.ScheduleModeScheduled, Window: &contracts.TimeWindow{From: contracts.At(p.scheduledFor), To: contracts.At(p.scheduledFor.Add(time.Hour))}}
	}
	env := feedEvent(r.t, contracts.TypePlanConfirmed, p.id, 1, p.created, contracts.PlanConfirmed{Plan: plan})
	if err := testkit.ApplyEnvelope(r.pool, r.projection.PlanConfirmed, env); err != nil {
		r.t.Fatalf("plan.confirmed %s: %v", p.id, err)
	}
	return plan
}

// mutate changes a plan in the projection the way the events of the other services would, then saves it.
func (r *rig) mutate(planID string, fn func(a *plans.Aggregate)) {
	r.t.Helper()
	err := platform.WithTx(r.t.Context(), r.pool, func(tx pgx.Tx) error {
		a, err := plans.Lock(r.t.Context(), tx, planID)
		if err != nil {
			return err
		}
		before := a.Plan.UpdatedAt
		fn(a)
		touched := a.Plan.UpdatedAt // a change that dates the plan itself is kept
		a.Sync(now)
		if touched != before {
			a.Plan.UpdatedAt = touched
		}
		return plans.Save(r.t.Context(), tx, a)
	})
	if err != nil {
		r.t.Fatalf("mutate %s: %v", planID, err)
	}
}

// assign gives route n (1-based) of the plan to the driver, and optionally starts it.
func (r *rig) assign(planID string, n int, driverID string, start bool, delayMin float64) {
	r.t.Helper()
	r.mutate(planID, func(a *plans.Aggregate) {
		rt := &a.Routes[n-1]
		rt.Route = routeplan.Assign(rt.Route, driverID, r.vehicles[driverID], ago(30*time.Minute))
		if start {
			var err error
			if rt.Route, err = routeplan.Start(rt.Route, ago(20*time.Minute)); err != nil {
				r.t.Fatal(err)
			}
		}
		rt.DelayMin = delayMin
	})
}

// finish runs route n of the plan to its end, every stop done, finishing at the given time.
func (r *rig) finish(planID string, n int, driverID string, at time.Time, delayMin float64) {
	r.t.Helper()
	r.mutate(planID, func(a *plans.Aggregate) {
		rt := &a.Routes[n-1]
		rt.Route = routeplan.Assign(rt.Route, driverID, r.vehicles[driverID], at.Add(-time.Hour))
		var err error
		if rt.Route, err = routeplan.Start(rt.Route, at.Add(-40*time.Minute)); err != nil {
			r.t.Fatal(err)
		}
		for _, s := range append([]contracts.RouteStop(nil), rt.Stops...) {
			if rt.Route, err = routeplan.Arrive(rt.Route, s.ID, at.Add(-30*time.Minute)); err != nil {
				r.t.Fatal(err)
			}
			if rt.Route, _, err = routeplan.Close(rt.Route, s.ID, at, contracts.StopStatusDone, nil, nil); err != nil {
				r.t.Fatal(err)
			}
		}
		rt.DelayMin = delayMin
		a.Plan.FinishedAt = contracts.At(at)
	})
}

// problem makes the first stop of the route a problem stop the driver reported.
func (r *rig) problem(planID string, n int, driverID string, issue contracts.Msg) {
	r.t.Helper()
	r.mutate(planID, func(a *plans.Aggregate) {
		rt := &a.Routes[n-1]
		rt.Route = routeplan.Assign(rt.Route, driverID, r.vehicles[driverID], ago(40*time.Minute))
		var err error
		if rt.Route, err = routeplan.Start(rt.Route, ago(30*time.Minute)); err != nil {
			r.t.Fatal(err)
		}
		if rt.Route, _, err = routeplan.Close(rt.Route, rt.Stops[0].ID, ago(10*time.Minute), contracts.StopStatusProblem, nil, &issue); err != nil {
			r.t.Fatal(err)
		}
	})
}

// world is the story the tests read. Times are relative to now (19:00 WIB), the plans are of the customers
// Dina and Agus:
//
//	PL-2001  waiting, motor, confirmed 45 min ago        PL-2002  waiting, blind van, confirmed 10 min ago
//	PL-2003  scheduled for tomorrow 09:00 WIB, waiting   PL-2004  running, 12 min late (Eko)
//	PL-2005  running, a problem stop (Fani)              PL-2006  assigned for tomorrow 14:00 (Fani)
//	PL-2007  done 3 h ago, two routes (3 and 8 min late) PL-2008  cancelled
//	PL-2009  done, created 10 days ago (the previous period)
//
// and drivers Budi (motor, online 1 km north), Citra (motor, online 3 km), Dodi (blind van, online 2 km), Eko and
// Fani (busy with PL-2004/5/6), Gita (offline). A top-up of Rp 100.000 was paid 2 h ago and one of Rp 50.000 waits.
type world struct {
	budi, citra, dodi, eko, fani, gita string
}

func (r *rig) seed() world {
	r.t.Helper()
	r.customer("cus-1", "Dina Pramesti")
	r.customer("cus-2", "Agus Salim")
	w := world{
		budi:  r.driver("Budi", contracts.VehicleTypeMotor, "B 1001 AB", 1, true),
		citra: r.driver("Citra", contracts.VehicleTypeMotor, "B 1002 AB", 3, true),
		dodi:  r.driver("Dodi", contracts.VehicleTypeBlindvan, "B 1003 AB", 2, true),
		eko:   r.driver("Eko", contracts.VehicleTypeMotor, "B 1004 AB", 1, false),
		fani:  r.driver("Fani", contracts.VehicleTypeMotor, "B 1005 AB", 3, true),
		gita:  r.driver("Gita", contracts.VehicleTypeMotor, "B 1006 AB", 1, false),
	}
	motor := func(kg float64) []routeSpec { return []routeSpec{{contracts.VehicleTypeMotor, kg, 0}} }
	tomorrow9 := time.Date(2026, time.October, 22, 2, 0, 0, 0, time.UTC) // 09:00 WIB
	tomorrow14 := time.Date(2026, time.October, 22, 7, 0, 0, 0, time.UTC)

	r.confirm(planSpec{id: "pln-1", code: "PL-2001", customer: "cus-1", created: ago(45 * time.Minute), pickup: "Kemang Village", routes: motor(5)})
	r.confirm(planSpec{id: "pln-2", code: "PL-2002", customer: "cus-2", created: ago(10 * time.Minute), pickup: "Kemang Village", routes: []routeSpec{{contracts.VehicleTypeBlindvan, 100, 0}}})
	r.confirm(planSpec{id: "pln-3", code: "PL-2003", customer: "cus-1", created: ago(time.Hour), scheduledFor: tomorrow9, pickup: "Kemang Village", routes: motor(3)})
	r.confirm(planSpec{id: "pln-4", code: "PL-2004", customer: "cus-1", created: ago(3 * time.Hour), routes: motor(4)})
	r.confirm(planSpec{id: "pln-5", code: "PL-2005", customer: "cus-2", created: ago(4 * time.Hour), routes: motor(6)})
	r.confirm(planSpec{id: "pln-6", code: "PL-2006", customer: "cus-2", created: ago(5 * time.Hour), scheduledFor: tomorrow14, routes: motor(2)})
	r.confirm(planSpec{id: "pln-7", code: "PL-2007", customer: "cus-1", created: ago(30 * time.Hour), routes: []routeSpec{{contracts.VehicleTypeMotor, 2, 0}, {contracts.VehicleTypeMotor, 3, 1}}})
	r.confirm(planSpec{id: "pln-8", code: "PL-2008", customer: "cus-1", created: ago(24 * time.Hour), routes: motor(2)})
	r.confirm(planSpec{id: "pln-9", code: "PL-2009", customer: "cus-2", created: ago(10 * 24 * time.Hour), routes: motor(2)})

	r.assign("pln-4", 1, w.eko, true, 12)
	r.problem("pln-5", 1, w.fani, contracts.Msg{Key: "srv.driver.issueWithNote", Params: map[string]any{
		"issue": contracts.Msg{Key: "dom.issue.tidak_ada_orang"}, "note": "Pintu terkunci",
	}})
	r.assign("pln-6", 1, w.fani, false, 0)
	r.finish("pln-7", 1, w.citra, ago(3*time.Hour), 3)
	r.finish("pln-7", 2, w.citra, ago(3*time.Hour), 8)
	r.finish("pln-9", 1, w.budi, ago(9*24*time.Hour), 0)
	r.mutate("pln-8", func(a *plans.Aggregate) {
		a.Plan.Status = contracts.PlanStatusCancelled
		a.Plan.CancelReason = "Dibatalkan oleh pengirim"
		a.Plan.UpdatedAt = contracts.At(ago(2 * time.Hour))
	})

	paid := contracts.TopUp{ID: "tu-1", UserID: "cus-1", AmountRp: 100_000, Method: contracts.TopUpMethodQRIS, Status: contracts.TopUpStatusSucceeded,
		CreatedAt: contracts.At(ago(3 * time.Hour)), ExpiresAt: contracts.At(ago(2 * time.Hour)), PaidAt: contracts.At(ago(2 * time.Hour)), Reference: "REF-1"}
	waiting := contracts.TopUp{ID: "tu-2", UserID: "cus-2", AmountRp: 50_000, Method: contracts.TopUpMethodVABCA, Status: contracts.TopUpStatusPending,
		CreatedAt: contracts.At(ago(10 * time.Minute)), ExpiresAt: contracts.At(now.Add(20 * time.Minute)), Reference: "REF-2"}
	r.apply(r.finance.TopUpPaid, contracts.TypeTopUpPaid, "cus-1", 1, contracts.TopUpPaid{TopUp: paid, TxID: "wtx-1", BalanceAfterRp: 100_000})
	r.apply(r.finance.TopUpCreated, contracts.TypeTopUpCreated, "cus-2", 1, contracts.TopUpCreated{TopUp: waiting})
	return w
}

// harness is the AI service behind the real RPC layer: an HTTP server with the session and language middleware.
type harness struct {
	t        *testing.T
	url      string
	sessions *platform.Sessions
}

func serve(t *testing.T, svc *Service) *harness {
	t.Helper()
	rpc := platform.NewRPC(testkit.Discard)
	svc.Mount(rpc)
	mux := http.NewServeMux()
	rpc.Mount(mux)
	store := platform.NewSessions(platform.NewMemorySessionStore(), time.Hour, testkit.Discard)
	srv := httptest.NewServer(platform.Chain(mux, platform.LocaleMiddleware, store.Authenticate))
	t.Cleanup(srv.Close)
	return &harness{t: t, url: srv.URL, sessions: store}
}

// token signs in a user of the role and returns the bearer token.
func (h *harness) token(role contracts.Role, id string) string {
	h.t.Helper()
	token, err := h.sessions.Start(h.t.Context(), platform.Principal{Role: role, UserID: id, Name: "Rani Wulandari"})
	if err != nil {
		h.t.Fatal(err)
	}
	return token
}

// reply is an answer of the RPC layer: the status, and the body as lines (one for a plain answer, one per event of
// a stream).
type reply struct {
	status int
	body   []byte
	events []map[string]any
}

// errorMsg is the message key of a failure answer, with its params.
func (rp reply) errorMsg() (key string, params map[string]any) {
	var e struct {
		Code string `json:"code"`
		Msg  struct {
			Key    string         `json:"key"`
			Params map[string]any `json:"params"`
		} `json:"msg"`
	}
	if err := json.Unmarshal(rp.body, &e); err != nil {
		return "", nil
	}
	return e.Msg.Key, e.Msg.Params
}

// code is the frontend's ApiErrorCode of a failure answer.
func (rp reply) code() string {
	var e struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rp.body, &e)
	return e.Code
}

// call posts an RPC method and reads the whole answer.
func (h *harness) call(token, method string, in any, header map[string]string) reply {
	h.t.Helper()
	return h.callCtx(h.t.Context(), token, method, in, header, nil)
}

// callCtx is call with a context to cancel and an optional hook that sees every event as it arrives (return false
// to stop reading).
func (h *harness) callCtx(ctx context.Context, token, method string, in any, header map[string]string, each func(map[string]any) bool) reply {
	h.t.Helper()
	body, err := json.Marshal(in)
	if err != nil {
		h.t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url+"/v1/rpc/"+method, bytes.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return reply{}
		}
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := reply{status: resp.StatusCode}
	if resp.Header.Get("Content-Type") != "application/x-ndjson" {
		out.body, _ = io.ReadAll(resp.Body)
		return out
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		var ev map[string]any
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			h.t.Fatalf("bad stream line %q: %v", sc.Text(), err)
		}
		out.events = append(out.events, ev)
		out.body = append(out.body, sc.Bytes()...)
		out.body = append(out.body, '\n')
		if each != nil && !each(ev) {
			break
		}
	}
	return out
}

// text is the streamed answer: every token event joined.
func (rp reply) text() string {
	var b bytes.Buffer
	for _, ev := range rp.events {
		if ev["type"] == "token" {
			b.WriteString(ev["text"].(string))
		}
	}
	return b.String()
}

// traces are the tool events, in order.
func (rp reply) traces() []map[string]any {
	var out []map[string]any
	for _, ev := range rp.events {
		if ev["type"] == "tool" {
			out = append(out, ev["trace"].(map[string]any))
		}
	}
	return out
}

// done is the final event, or nil when the stream did not end with one.
func (rp reply) done() map[string]any {
	if n := len(rp.events); n > 0 && rp.events[n-1]["type"] == "done" {
		return rp.events[n-1]
	}
	return nil
}

// actions are the buttons of the done event.
func (rp reply) actions() []map[string]any {
	d := rp.done()
	if d == nil {
		return nil
	}
	var out []map[string]any
	for _, a := range d["actions"].([]any) {
		out = append(out, a.(map[string]any))
	}
	return out
}

// firstDriver is the id of any driver of the rig.
func (r *rig) firstDriver(t *testing.T) string {
	t.Helper()
	for id := range r.vehicles {
		return id
	}
	t.Fatal("no driver")
	return ""
}
