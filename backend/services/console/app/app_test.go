package app

import (
	"encoding/json/v2"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/pkg/routeplan"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/plans"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/testkit"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/wallet"
)

func twoShipments() []contracts.Shipment {
	return []contracts.Shipment{testkit.Shipment("s1"), testkit.Shipment("s2")}
}

// await waits for an event that satisfies match, skipping the others.
func await(t *testing.T, events <-chan platform.Event, what string, match func(platform.Event) bool) {
	t.Helper()
	timeout := time.After(20 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("the event stream ended while waiting for %s", what)
			}
			if match(ev) {
				return
			}
		case <-timeout:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func drain(events <-chan platform.Event) {
	for {
		select {
		case <-events:
		default:
			return
		}
	}
}

// The whole story through the HTTP API and Kafka: a shipper signs up, tops up, drafts a plan, gets options,
// pays once even when the request is repeated, and then follows the plan as other services report on it.
func TestFromSignUpToADeliveredPlan(t *testing.T) {
	svc := startService(t)
	dina, dinaID := svc.register("dina@contoh.id")
	events := dina.events()

	var session *contracts.Session
	dina.rpc("auth.session", nil).into(&session)
	if session == nil || session.UserID != dinaID || session.Role != contracts.RoleCustomer || session.Name != "Dina Pramesti" {
		t.Fatalf("session = %+v", session)
	}

	// The wallet: a top-up needs its Idempotency-Key, repeating it opens one top-up, paying credits the wallet.
	topUp := map[string]any{"input": map[string]any{"amountRp": 50_000, "method": "qris"}}
	r := dina.rpc("wallet.createTopUp", topUp)
	if f := r.failure(); r.status != http.StatusUnprocessableEntity || f.Msg.Key != "srv.rpc.idempotencyKeyRequired" {
		t.Errorf("a top-up without a key: %d %+v", r.status, f)
	}
	var top contracts.TopUp
	dina.rpcKeyed("wallet.createTopUp", "topup-1", topUp).into(&top)
	again := dina.rpcKeyed("wallet.createTopUp", "topup-1", topUp)
	var replayed contracts.TopUp
	again.into(&replayed)
	if again.header.Get("Idempotent-Replayed") != "true" || replayed.ID != top.ID {
		t.Errorf("the repeated request opened another top-up: %+v", replayed)
	}
	if n := testkit.Count(t, svc.pool, `SELECT count(*) FROM top_ups`); n != 1 {
		t.Errorf("%d top-ups, want 1", n)
	}
	dina.rpc("wallet.simulatePay", map[string]any{"id": top.ID}).into(&top)
	var w wallet.Wallet
	dina.rpc("wallet.get", nil).into(&w)
	if top.Status != contracts.TopUpStatusSucceeded || w.BalanceRp != 50_000 || len(w.PendingTopUps) != 0 {
		t.Fatalf("after paying: top-up %s, wallet %+v", top.Status, w)
	}

	// A draft cannot be priced while back office has offered no vehicle.
	var plan contracts.Plan
	dina.rpc("plans.createDraft", nil).into(&plan)
	dina.rpc("plans.saveDraft", map[string]any{"id": plan.ID, "patch": map[string]any{"shipments": twoShipments(), "draftStep": 2}}).into(&plan)
	_, r = dina.stream("plans.computeOptions", map[string]any{"id": plan.ID})
	if f := r.failure(); r.status != http.StatusInternalServerError || f.Code != "layanan" || f.Msg.Key != "srv.engine.noVehicles" {
		t.Fatalf("without vehicles: %d %+v", r.status, f)
	}
	svc.publish(contracts.TypeVehicleUpserted, "veh-aaa", 1, contracts.VehicleUpserted{Vehicle: testkit.Vehicle("veh-aaa", contracts.VehicleTypeMotor)})
	svc.waitFor("the vehicle to be projected", func() bool {
		return testkit.Count(t, svc.pool, `SELECT count(*) FROM fleet_vehicles`) == 1
	})

	// Options stream as NDJSON: four progress lines in the frontend's shape, then the options.
	lines, r := dina.stream("plans.computeOptions", map[string]any{"id": plan.ID})
	if len(lines) != 5 || r.header.Get("Content-Type") != "application/x-ndjson" {
		t.Fatalf("%d lines of %q: %s", len(lines), r.header.Get("Content-Type"), r.body)
	}
	if lines[0] != `{"type":"progress","step":1,"total":4,"label":"Reading 4 locations"}` {
		t.Errorf("first line = %s", lines[0])
	}
	var done struct {
		Type      string              `json:"type"`
		OptionSet contracts.OptionSet `json:"optionSet"`
	}
	if err := json.Unmarshal([]byte(lines[4]), &done); err != nil || done.Type != "done" || len(done.OptionSet.Options) != 1 {
		t.Fatalf("last line = %s (%v)", lines[4], err)
	}
	optionID := done.OptionSet.Options[0].ID
	dina.rpc("plans.chooseOption", map[string]any{"id": plan.ID, "optionId": optionID}).into(&plan)
	if plan.Status != contracts.PlanStatusOptionsReady || plan.SelectedOptionID != optionID {
		t.Fatalf("chosen plan = %s %q", plan.Status, plan.SelectedOptionID)
	}

	// Confirming pays once, however often the request is repeated with its key.
	confirm := map[string]any{"id": plan.ID, "paymentMethod": "saldo"}
	if r := dina.rpc("plans.confirm", confirm); r.status != http.StatusUnprocessableEntity {
		t.Errorf("confirm without a key: %d", r.status)
	}
	first := dina.rpcKeyed("plans.confirm", "confirm-1", confirm)
	first.into(&plan)
	second := dina.rpcKeyed("plans.confirm", "confirm-1", confirm)
	if second.status != http.StatusOK || second.header.Get("Idempotent-Replayed") != "true" || string(second.body) != string(first.body) {
		t.Errorf("repeating the confirmation: status %d replayed %q", second.status, second.header.Get("Idempotent-Replayed"))
	}
	if plan.Status != contracts.PlanStatusAwaitingDriver || len(plan.Routes) != 1 {
		t.Fatalf("confirmed plan = %s with %d routes", plan.Status, len(plan.Routes))
	}
	dina.rpc("wallet.get", nil).into(&w)
	if w.BalanceRp != 25_000 || testkit.Count(t, svc.pool, `SELECT count(*) FROM wallet_entries WHERE kind = 'pembayaran'`) != 1 {
		t.Errorf("balance %d: the plan was not charged exactly once", w.BalanceRp)
	}
	// A different key is a different request, and the plan is no longer for sale.
	if r := dina.rpcKeyed("plans.confirm", "confirm-2", confirm); r.status != http.StatusConflict || r.failure().Msg.Key != "srv.api.planNotReady" {
		t.Errorf("confirming a confirmed plan: %d %s", r.status, r.body)
	}

	// What the relay published: one of each fact, on the topic that owns it.
	planTopic := svc.topic(contracts.TopicPlan, 1)
	if len(planTopic) != 1 || typeOf(planTopic[0]) != contracts.TypePlanConfirmed || string(planTopic[0].Key) != plan.ID {
		t.Errorf("plan topic = %+v", planTopic)
	}
	var walletTypes []string
	for _, rec := range svc.topic(contracts.TopicWallet, 3) {
		walletTypes = append(walletTypes, typeOf(rec))
	}
	slices.Sort(walletTypes)
	if !slices.Equal(walletTypes, []string{contracts.TypeTopUpCreated, contracts.TypeTopUpPaid, contracts.TypeWalletCharged}) {
		t.Errorf("wallet topic = %v", walletTypes)
	}
	if custTopic := svc.topic(contracts.TopicCustomer, 1); len(custTopic) != 1 || typeOf(custTopic[0]) != contracts.TypeCustomerRegistered {
		t.Errorf("customer topic = %+v", custTopic)
	}

	// Back office and the driver report; the console follows.
	route := plan.Routes[0]
	now := time.Now().UTC()
	assigned := routeplan.Assign(routeplan.RebaseStart(route, contracts.LatLng{Lat: -6.27, Lng: 106.80}, now.Add(2*time.Minute)), "drv-1", "veh-aaa", now)
	drain(events)
	svc.publish(contracts.TypeRouteAssigned, route.ID, 1, contracts.RouteAssigned{
		PlanID: plan.ID, PlanCode: plan.Code, CustomerName: "Dina Pramesti", DriverID: "drv-1", VehicleID: "veh-aaa", StartAt: assigned.StartAt, Route: assigned,
	})
	await(t, events, "the assignment to reach the dashboard", func(ev platform.Event) bool { return ev.Type == "plans.changed" && ev.PlanID == plan.ID })
	dina.rpc("plans.get", map[string]any{"id": plan.ID}).into(&plan)
	if plan.Status != contracts.PlanStatusAssigned || plan.Routes[0].DriverID != "drv-1" {
		t.Fatalf("after assignment: %s, driver %q", plan.Status, plan.Routes[0].DriverID)
	}

	svc.publish(contracts.TypeRouteStarted, route.ID, 1, contracts.RouteStarted{PlanID: plan.ID, RouteID: route.ID, DriverID: "drv-1", StartedAt: contracts.At(now.Add(3 * time.Minute))})
	version := int64(1)
	for i, stop := range route.Stops {
		minute := time.Duration(10*(i+1)) * time.Minute
		version++
		svc.publish(contracts.TypeStopArrived, route.ID, version, contracts.StopArrived{PlanID: plan.ID, RouteID: route.ID, StopID: stop.ID, ArrivedAt: contracts.At(now.Add(minute))})
		version++
		done := contracts.StopCompleted{PlanID: plan.ID, RouteID: route.ID, StopID: stop.ID, CompletedAt: contracts.At(now.Add(minute + 3*time.Minute))}
		if stop.Kind == contracts.StopKindDropoff {
			done.Proof = &contracts.DeliveryProof{Kind: contracts.ProofKindOTP, At: done.CompletedAt}
		}
		svc.publish(contracts.TypeStopCompleted, route.ID, version, done)
	}
	svc.publish(contracts.TypeRouteCompleted, route.ID, version+1, contracts.RouteCompleted{PlanID: plan.ID, RouteID: route.ID, DriverID: "drv-1", FinishedAt: contracts.At(now.Add(50 * time.Minute))})
	svc.waitFor("the plan to be finished", func() bool {
		dina.rpc("plans.get", map[string]any{"id": plan.ID}).into(&plan)
		return plan.Status == contracts.PlanStatusDone
	})

	if plan.FinishedAt.IsZero() || plan.Events[len(plan.Events)-1].Text.Key != "srv.ops.event.allDone" {
		t.Errorf("finishedAt %v, last event %+v", plan.FinishedAt, plan.Events[len(plan.Events)-1].Text)
	}
	for _, s := range plan.Routes[0].Stops {
		if s.Status != contracts.StopStatusDone || s.ArrivedAt.IsZero() {
			t.Errorf("stop %s is %s", s.ID, s.Status)
		}
	}
	var list []plans.Summary
	dina.rpc("plans.list", nil).into(&list)
	if len(list) != 1 || list[0].Status != contracts.PlanStatusDone || list[0].Progress != (plans.Progress{Done: 4, Total: 4}) {
		t.Errorf("list = %+v", list)
	}
	var ledger []wallet.Entry
	dina.rpc("wallet.transactions", nil).into(&ledger)
	if len(ledger) != 2 || ledger[0].Kind != "pembayaran" || ledger[0].AmountRp != -25_000 || ledger[0].PlanID != plan.ID || ledger[1].Kind != "topup" || ledger[1].BalanceAfterRp != 50_000 {
		t.Errorf("ledger = %+v", ledger)
	}
}

func TestCustomersAreIsolatedFromEachOther(t *testing.T) {
	svc := startService(t)
	dina, _ := svc.register("dina@contoh.id")
	raka, _ := svc.register("raka@contoh.id")
	var plan contracts.Plan
	dina.rpc("plans.createDraft", nil).into(&plan)

	r := raka.rpc("plans.get", map[string]any{"id": plan.ID})
	if f := r.failure(); r.status != http.StatusForbidden || f.Code != "ditolak" || f.Msg.Key != "srv.api.planNoAccess" {
		t.Errorf("reading another customer's plan: %d %+v", r.status, f)
	}
	for method, body := range map[string]any{
		"plans.saveDraft":      map[string]any{"id": plan.ID, "patch": map[string]any{"draftStep": 3}},
		"plans.remove":         map[string]any{"id": plan.ID},
		"plans.chooseOption":   map[string]any{"id": plan.ID, "optionId": "x"},
		"plans.computeOptions": map[string]any{"id": plan.ID},
	} {
		if r := raka.rpc(method, body); r.status != http.StatusForbidden {
			t.Errorf("%s on another customer's plan: status %d", method, r.status)
		}
	}
	var theirs []plans.Summary
	raka.rpc("plans.list", nil).into(&theirs)
	if len(theirs) != 0 {
		t.Errorf("Raka lists %+v", theirs)
	}
	if r := dina.rpc("plans.get", map[string]any{"id": plan.ID}); r.status != http.StatusOK {
		t.Errorf("the owner lost access: %d", r.status)
	}
}

func TestEveryCustomerMethodRefusesOtherCallers(t *testing.T) {
	svc := startService(t)
	dina, _ := svc.register("dina@contoh.id")
	var plan contracts.Plan
	dina.rpc("plans.createDraft", nil).into(&plan)

	// A driver of another service's making: a valid session, but not a customer's.
	const driverToken = "driver-session-token"
	hash := platform.HashToken(driverToken)
	_, err := svc.pool.Exec(t.Context(),
		`INSERT INTO sessions (token_hash, role, user_id, name, expires_at) VALUES ($1, 'driver', 'drv-1', 'Budi', now() + interval '1 hour')`, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	driver := caller{svc: svc, token: driverToken}

	id := map[string]any{"id": plan.ID}
	methods := map[string]any{
		"wallet.get":           nil,
		"wallet.transactions":  nil,
		"wallet.createTopUp":   map[string]any{"input": map[string]any{"amountRp": 50_000, "method": "qris"}},
		"wallet.getTopUp":      id,
		"wallet.cancelTopUp":   id,
		"wallet.simulatePay":   id,
		"plans.list":           nil,
		"plans.get":            id,
		"plans.createDraft":    nil,
		"plans.saveDraft":      map[string]any{"id": plan.ID, "patch": map[string]any{}},
		"plans.computeOptions": id,
		"plans.chooseOption":   map[string]any{"id": plan.ID, "optionId": "x"},
		"plans.confirm":        map[string]any{"id": plan.ID, "paymentMethod": "saldo"},
		"plans.remove":         id,
		"plans.cancel":         map[string]any{"id": plan.ID, "reason": "changed my mind"},
		"plans.reschedule":     map[string]any{"id": plan.ID, "schedule": map[string]any{"mode": "sekarang"}},

		"account.profile":          nil,
		"account.update":           map[string]any{"patch": map[string]any{"name": "Someone"}},
		"account.addresses.add":    map[string]any{"input": map[string]any{"label": "Home"}},
		"account.addresses.update": map[string]any{"id": "adr-x", "patch": map[string]any{}},
		"account.addresses.remove": map[string]any{"id": "adr-x"},
		"notifications.list":       nil,
		"notifications.markRead":   nil,
	}
	for method, body := range methods {
		if body == nil {
			body = struct{}{}
		}
		for who, c := range map[string]caller{"anonymous": svc.anonymous(), "bad token": {svc: svc, token: "nonsense"}} {
			r := c.rpcKeyed(method, "key-"+who+"-"+method, body)
			if f := r.failure(); r.status != http.StatusUnauthorized || f.Code != "ditolak" || f.Msg.Key != "srv.api.signInFirst" {
				t.Errorf("%s as %s: %d %+v, want 401 signInFirst", method, who, r.status, f)
			}
		}
		r := driver.rpcKeyed(method, "key-driver-"+method, body)
		if f := r.failure(); r.status != http.StatusForbidden || f.Code != "ditolak" || f.Msg.Key != "srv.api.roleDenied" {
			t.Errorf("%s as a driver: %d %+v, want 403 roleDenied", method, r.status, f)
		}
	}
	if r := svc.anonymous().do(http.MethodGet, "/v1/events", nil, nil); r.status != http.StatusUnauthorized {
		t.Errorf("the event stream without a token: %d", r.status)
	}
	// Nothing the refused callers tried left a trace.
	if n := testkit.Count(t, svc.pool, `SELECT count(*) FROM plans`); n != 1 {
		t.Errorf("%d plans, want only Dina's draft", n)
	}
}

func TestSigningInAndOut(t *testing.T) {
	svc := startService(t)
	svc.register("dina@contoh.id")
	anon := svc.anonymous()

	var session *contracts.Session
	anon.rpc("auth.session", nil).into(&session)
	if session != nil {
		t.Errorf("an anonymous caller has a session: %+v", session)
	}
	if r := anon.rpc("auth.loginCustomer", map[string]string{"email": "dina@contoh.id", "password": "wrong"}); r.status != http.StatusUnprocessableEntity || r.failure().Msg.Key != "srv.api.badCredentials" {
		t.Errorf("wrong password: %d %s", r.status, r.body)
	}
	var signed struct {
		UserID string `json:"userId"`
		Token  string `json:"token"`
	}
	anon.rpc("auth.loginCustomer", map[string]string{"email": "dina@contoh.id", "password": "demo1234"}).into(&signed)
	dina := caller{svc: svc, token: signed.Token}
	if r := dina.rpc("wallet.get", nil); r.status != http.StatusOK {
		t.Fatalf("signed in but refused: %d", r.status)
	}

	dina.rpc("auth.logout", nil).into(&struct{}{})
	if r := dina.rpc("wallet.get", nil); r.status != http.StatusUnauthorized {
		t.Errorf("the token still works after logging out: %d", r.status)
	}
	dina.rpc("auth.session", nil).into(&session)
	if session != nil {
		t.Errorf("a signed-out token still has a session: %+v", session)
	}

	if r := anon.rpc("auth.register", map[string]any{"input": map[string]string{"name": "Other", "email": "dina@contoh.id", "phone": "0812345678", "password": "demo1234"}}); r.status != http.StatusConflict || r.failure().Field != "email" {
		t.Errorf("registering a taken email: %d %s", r.status, r.body)
	}
}

func TestMethodsOutsideThisSliceAreNotServed(t *testing.T) {
	svc := startService(t, func(c *Config) { c.SimulatePayments = false })
	dina, _ := svc.register("dina@contoh.id")
	for _, method := range []string{
		"places.search", "places.describe", "ai.insights", "finance.overview", "demo.state", "auth.loginAdmin",
		"fleet.vehicles.list", "fleet.drivers.list", // the company's fleet and its drivers are not for customers
		"wallet.simulatePay", // only when SIMULATE_PAYMENTS is on
	} {
		r := dina.rpc(method, nil)
		if f := r.failure(); r.status != http.StatusNotFound || f.Code != "tidak_ditemukan" || f.Msg.Key != "srv.rpc.unknownMethod" {
			t.Errorf("%s: %d %+v, want 404 tidak_ditemukan", method, r.status, f)
		}
	}
}

func TestHealthEndpoints(t *testing.T) {
	svc := startService(t)
	for _, path := range []string{"/healthz", "/readyz"} {
		if r := svc.anonymous().do(http.MethodGet, path, nil, nil); r.status != http.StatusOK {
			t.Errorf("%s: %d %s", path, r.status, r.body)
		}
	}
}
