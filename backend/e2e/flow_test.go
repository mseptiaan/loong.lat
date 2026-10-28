//go:build e2e

package e2e

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/optimizerapi"
)

// The people of the story.
const (
	driverPhone = "081234567890"
	driverPIN   = "482913"
)

// login result of every service: the frontend's Session plus the bearer token.
type login struct {
	contracts.Session
	Token string `json:"token"`
}

type quotedSet struct {
	Type      string              `json:"type"`
	OptionSet contracts.OptionSet `json:"optionSet"`
}

// shipments are the three same-pickup parcels of the parity fixtures: batching them into one route saves money.
func shipments(t *testing.T) []contracts.Shipment {
	t.Helper()
	data, err := os.ReadFile("../testdata/parity/batching.request.json")
	if err != nil {
		t.Fatal(err)
	}
	var req optimizerapi.QuoteRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatal(err)
	}
	return req.Shipments
}

func (w *world) admin() *session {
	s := &session{t: w.t, base: w.backofficeURL}
	var l login
	s.must("auth.loginAdmin", map[string]any{"email": adminEmail, "password": adminPassword}, &l)
	s.token = l.Token
	return s
}

// provisionFleet creates a motorbike, a blindvan and a driver on the motorbike, and turns auto-dispatch on.
func (w *world) provisionFleet(admin *session) (driverID string) {
	w.t.Helper()
	vehicle := func(typ, plate, model string, kg, m3, dim float64, fare contracts.FareConfig) contracts.Vehicle {
		var v contracts.Vehicle
		admin.must("fleet.vehicles.create", map[string]any{"input": map[string]any{
			"type": typ, "plate": plate, "model": model, "maxWeightKg": kg, "maxVolumeM3": m3, "maxDimensionCm": dim, "fare": fare, "status": "aktif",
		}}, &v)
		return v
	}
	motor := vehicle("motor", "B 1234 LNG", "Honda Beat", 20, 0.12, 55,
		contracts.FareConfig{Base: 6000, PerKm: 1800, PerMin: 150, PerExtraStop: 2500, SurchargePerKg: 800, SurchargeAboveKg: 10})
	vehicle("blindvan", "B 9876 LNG", "Daihatsu Gran Max", 500, 3.2, 190,
		contracts.FareConfig{Base: 45000, PerKm: 4500, PerMin: 500, PerExtraStop: 8000, SurchargePerKg: 150, SurchargeAboveKg: 300})

	var driver struct {
		ID string `json:"id"`
	}
	admin.must("fleet.drivers.create", map[string]any{"input": map[string]any{
		"name": "Budi Santoso", "phone": driverPhone, "vehicleId": motor.ID, "pin": driverPIN,
	}}, &driver)

	admin.must("settings.update", map[string]any{"patch": map[string]any{"dispatch": map[string]any{"auto": true}}}, nil)
	return driver.ID
}

// goOnline signs the driver in and puts them near the pickups. The account is created in driver-api by an event from the
// back office, so the test waits for it in the database rather than by trying to log in: failed logins are rate limited.
func (w *world) goOnline() *session {
	w.t.Helper()
	conn, err := pgx.Connect(w.ctx, w.dbURL("driver"))
	if err != nil {
		w.t.Fatal(err)
	}
	defer conn.Close(w.ctx)
	eventually(w.t, "the driver account to reach driver-api", 60*time.Second, func() (bool, string) {
		var n int
		if err := conn.QueryRow(w.ctx, `SELECT count(*) FROM drivers`).Scan(&n); err != nil {
			return false, err.Error()
		}
		return n == 1, fmt.Sprintf("%d driver accounts", n)
	})

	s := &session{t: w.t, base: w.driverURL}
	var l login
	s.must("auth.loginDriver", map[string]any{"phone": driverPhone, "pin": driverPIN}, &l)
	s.token = l.Token
	s.must("driver.setOnline", map[string]any{"online": true}, nil)
	s.must("driver.ping", map[string]any{"position": map[string]any{"lat": -6.2610, "lng": 106.8130}, "speedKmh": 0}, nil)
	return s
}

// customer registers, tops up the wallet and returns the signed-in session with the balance.
func (w *world) customer() *session {
	w.t.Helper()
	s := &session{t: w.t, base: w.consoleURL}
	var l login
	s.must("auth.register", map[string]any{"input": map[string]any{
		"name": "Dina Pelanggan", "email": "dina@loong.test", "phone": "081200000001", "password": "dina-secret-1",
	}}, &l)
	s.token = l.Token

	var topUp contracts.TopUp
	s.must("wallet.createTopUp", map[string]any{"input": map[string]any{"amountRp": 200000, "method": "va_bca"}}, &topUp, "Idempotency-Key", "topup-1")
	s.must("wallet.simulatePay", map[string]any{"id": topUp.ID}, nil)
	return s
}

func (w *world) balance(c *session) int64 {
	w.t.Helper()
	var wallet struct {
		BalanceRp int64 `json:"balanceRp"`
	}
	c.must("wallet.get", nil, &wallet)
	return wallet.BalanceRp
}

// quotedPlan drafts the three parcels, asks for a quote and chooses the recommended option.
func (w *world) quotedPlan(c *session) (contracts.Plan, contracts.PlanOption) {
	w.t.Helper()
	var plan contracts.Plan
	c.must("plans.createDraft", nil, &plan)
	c.must("plans.saveDraft", map[string]any{"id": plan.ID, "patch": map[string]any{
		"shipments": shipments(w.t),
		"schedule":  map[string]any{"mode": "sekarang"},
		"priority":  map[string]any{"preset": "termurah"},
		"draftStep": 3,
	}}, &plan)

	// The vehicles arrive in console-api over Kafka; quoting works as soon as they have.
	var set contracts.OptionSet
	eventually(w.t, "a quote", 60*time.Second, func() (bool, string) {
		events, err := c.stream("plans.computeOptions", map[string]any{"id": plan.ID})
		if err != nil {
			return false, err.Error()
		}
		var steps int
		for _, raw := range events {
			var ev quotedSet
			if err := json.Unmarshal(raw, &ev); err != nil {
				w.t.Fatalf("bad event %s: %v", raw, err)
			}
			switch ev.Type {
			case "progress":
				steps++
			case "done":
				set = ev.OptionSet
				if steps != 4 {
					w.t.Errorf("%d progress events before done, the wizard shows exactly 4", steps)
				}
				return true, ""
			case "error":
				return false, string(raw)
			}
		}
		return false, "no done event"
	})
	if len(set.Options) == 0 {
		w.t.Fatal("the quote has no options")
	}
	var chosen contracts.PlanOption
	for _, o := range set.Options {
		if o.Recommended {
			chosen = o
		}
	}
	if chosen.ID == "" {
		w.t.Fatal("no recommended option")
	}
	c.must("plans.chooseOption", map[string]any{"id": plan.ID, "optionId": chosen.ID}, &plan)
	return plan, chosen
}

// confirm pays for the chosen option and returns the plan as the console shows it right after.
func (w *world) confirm(c *session, plan contracts.Plan) contracts.Plan {
	w.t.Helper()
	c.must("plans.confirm", map[string]any{"id": plan.ID, "paymentMethod": "saldo"}, &plan, "Idempotency-Key", "confirm-"+plan.ID)
	return plan
}

// confirmedPlan is quotedPlan followed by confirm; it also returns the cost of the chosen option.
func (w *world) confirmedPlan(c *session) (contracts.Plan, int64) {
	w.t.Helper()
	plan, chosen := w.quotedPlan(c)
	return w.confirm(c, plan), chosen.Totals.CostRp
}

func (w *world) plan(c *session, id string) contracts.Plan {
	w.t.Helper()
	var p contracts.Plan
	c.must("plans.get", map[string]any{"id": id}, &p)
	return p
}

// deliver has the driver run every route of their task list to the end.
func (w *world) deliver(d, c *session, planID string) int {
	w.t.Helper()
	var tasks []struct {
		PlanID string          `json:"planId"`
		Route  contracts.Route `json:"route"`
	}
	d.must("driver.routes", nil, &tasks)
	if len(tasks) == 0 {
		w.t.Fatal("the driver has no task")
	}
	codes := map[string]string{} // the customer's app shows the hand-over codes; the driver's never does
	for _, r := range w.plan(c, planID).Routes {
		for _, s := range r.Stops {
			codes[s.ID] = s.DeliveryCode
		}
	}
	for _, task := range tasks {
		d.must("driver.startRoute", map[string]any{"planId": task.PlanID, "routeId": task.Route.ID}, nil)
		for _, stop := range task.Route.Stops {
			if stop.DeliveryCode != "" {
				w.t.Errorf("the driver's task list leaks the delivery code of %s", stop.ID)
			}
			args := map[string]any{"planId": task.PlanID, "routeId": task.Route.ID, "stopId": stop.ID}
			d.must("driver.arrive", args, nil)
			if stop.Kind == contracts.StopKindDropoff {
				args["proof"] = map[string]any{"kind": "otp", "value": codes[stop.ID]}
			}
			d.must("driver.complete", args, nil)
		}
	}
	return len(tasks)
}

func TestAPlanLivesAcrossTheServices(t *testing.T) {
	w := newWorld(t)
	t.Cleanup(func() {
		if t.Failed() {
			w.dump()
		}
	})
	admin := w.admin()
	w.provisionFleet(admin)
	d := w.goOnline()
	c := w.customer()
	if got := w.balance(c); got != 200000 {
		t.Fatalf("balance after the top-up = %d, want 200000", got)
	}

	plan, cost := w.confirmedPlan(c)
	if plan.Status != contracts.PlanStatusAwaitingDriver && plan.Status != contracts.PlanStatusAssigned {
		t.Errorf("a confirmed plan is waiting for or has a driver, got %s", plan.Status)
	}
	if got := w.balance(c); got != 200000-cost {
		t.Errorf("balance after paying = %d, want %d", got, 200000-cost)
	}
	for _, r := range plan.Routes {
		for _, s := range r.Stops {
			if s.Kind == contracts.StopKindDropoff && len(s.DeliveryCode) != 4 {
				t.Errorf("stop %s has no hand-over code for the customer", s.ID)
			}
		}
	}

	// Confirming again with the same key is the same answer and costs nothing more; a new key finds the plan already paid.
	var again contracts.Plan
	c.must("plans.confirm", map[string]any{"id": plan.ID, "paymentMethod": "saldo"}, &again, "Idempotency-Key", "confirm-"+plan.ID)
	if got := w.balance(c); got != 200000-cost {
		t.Errorf("a repeated confirmation charged again: balance %d", got)
	}
	if err := c.call("plans.confirm", map[string]any{"id": plan.ID, "paymentMethod": "saldo"}, nil, "Idempotency-Key", "another-key"); err == nil {
		t.Error("a plan that was already confirmed must not be confirmable again")
	}

	// Back office: the plan joins the queue and auto-dispatch gives every route to the driver; the console follows.
	eventually(t, "the plan in the back office queue", 60*time.Second, func() (bool, string) {
		var list []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		if err := admin.call("plans.list", nil, &list); err != nil {
			return false, err.Error()
		}
		for _, p := range list {
			if p.ID == plan.ID {
				return true, ""
			}
		}
		return false, "plan not listed"
	})
	eventually(t, "every route assigned to the driver", 40*time.Second, func() (bool, string) {
		p := w.plan(c, plan.ID)
		for _, r := range p.Routes {
			if r.DriverID == "" {
				return false, "route " + r.ID + " still has no driver (plan " + string(p.Status) + ")"
			}
		}
		return p.Status == contracts.PlanStatusAssigned, "plan status " + string(p.Status)
	})

	routes := w.deliver(d, c, plan.ID)

	eventually(t, "the console to see the plan done", 30*time.Second, func() (bool, string) {
		p := w.plan(c, plan.ID)
		return p.Status == contracts.PlanStatusDone, "plan status " + string(p.Status)
	})
	eventually(t, "the back office to see the plan done", 30*time.Second, func() (bool, string) {
		var p contracts.Plan
		if err := admin.call("plans.get", map[string]any{"id": plan.ID}, &p); err != nil {
			return false, err.Error()
		}
		return p.Status == contracts.PlanStatusDone, "plan status " + string(p.Status)
	})

	done := w.plan(c, plan.ID)
	if done.FinishedAt.IsZero() || len(done.Events) < 3 {
		t.Errorf("a finished plan has a finish time and a timeline: %+v", done.Events)
	}
	for _, r := range done.Routes {
		if r.Status != contracts.RouteStatusDone {
			t.Errorf("route %s is %s", r.ID, r.Status)
		}
		for _, s := range r.Stops {
			if s.Status != contracts.StopStatusDone {
				t.Errorf("stop %s is %s", s.ID, s.Status)
			}
			if s.Kind == contracts.StopKindDropoff && s.Proof == nil {
				t.Errorf("drop-off %s has no proof of delivery", s.ID)
			}
		}
	}

	var history []struct {
		RouteID string `json:"routeId"`
	}
	d.must("driver.history", nil, &history)
	if len(history) != routes {
		t.Errorf("driver history has %d routes, want %d", len(history), routes)
	}

	if dl := deadLetters(w.ctx, t, w.brokers); len(dl) > 0 {
		t.Errorf("events were dead-lettered: %v", dl)
	}
}

// A back office that is down while a plan is confirmed catches up when it returns: the event waited in Kafka.
func TestTheBackOfficeCatchesUpAfterARestart(t *testing.T) {
	w := newWorld(t)
	admin := w.admin()
	w.provisionFleet(admin)
	d := w.goOnline()
	c := w.customer()
	// Quote first: it needs the vehicles to have reached the console, not the back office.
	plan, _ := w.quotedPlan(c)

	w.stopBackoffice()
	plan = w.confirm(c, plan)

	time.Sleep(2 * time.Second)
	if p := w.plan(c, plan.ID); p.Status != contracts.PlanStatusAwaitingDriver {
		t.Fatalf("with the back office down the plan waits for a driver, got %s", p.Status)
	}
	var tasks []jsontext.Value
	d.must("driver.routes", nil, &tasks)
	if len(tasks) != 0 {
		t.Fatalf("the driver got %d tasks while dispatch was down", len(tasks))
	}

	w.startBackoffice()
	eventually(t, "the restarted back office to dispatch the plan", 40*time.Second, func() (bool, string) {
		p := w.plan(c, plan.ID)
		return p.Status == contracts.PlanStatusAssigned, "plan status " + string(p.Status)
	})
	w.deliver(d, c, plan.ID)
	eventually(t, "the plan done", 30*time.Second, func() (bool, string) {
		p := w.plan(c, plan.ID)
		return p.Status == contracts.PlanStatusDone, "plan status " + string(p.Status)
	})
}
