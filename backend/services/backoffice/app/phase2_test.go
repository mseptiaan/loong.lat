package app_test

import (
	"bufio"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/app"
)

// These tests play console-api and driver-api for the parts of the cancel, the reschedule, the driver's release
// and the wallet that cross services. They need TEST_DATABASE_URL and skip without it.

func (s *stack) notifications() []contracts.AppNotification {
	s.t.Helper()
	var list []contracts.AppNotification
	s.admin("notifications.list", map[string]any{}, &list)
	return list
}

func (s *stack) waitNotification(what, titleKey string) contracts.AppNotification {
	s.t.Helper()
	var found contracts.AppNotification
	eventually(s.t, what, func() bool {
		for _, n := range s.notifications() {
			if n.Title.Key == titleKey {
				found = n
				return true
			}
		}
		return false
	})
	return found
}

// drivers are the drivers by id with their status.
func (s *stack) driverStatus(driverID string) string {
	s.t.Helper()
	var drivers []map[string]any
	s.admin("fleet.drivers.list", map[string]any{}, &drivers)
	for _, d := range drivers {
		if d["id"] == driverID {
			status, _ := d["status"].(string)
			return status
		}
	}
	return ""
}

// assigned brings the plan to the point where its one route is assigned to the driver.
func (s *stack) assigned(planID, driverID string) {
	s.t.Helper()
	s.confirm(planID, nowRoute(planID, 1, contracts.VehicleTypeMotor))
	s.waitPlan(planID, "the route to be assigned", func(p contracts.Plan) bool { return p.Routes[0].DriverID == driverID })
}

func TestAnAdminsCancelRunsThroughConsolesTwoPhaseCommit(t *testing.T) {
	s := start(t, nil)
	driverID := s.setup(contracts.VehicleTypeMotor, "B 3412 KJR", "081234567801")
	s.produce("console-api", contracts.TypeCustomerRegistered, "cus-1", 1, contracts.CustomerRegistered{CustomerID: "cus-1", Name: "Dina Pramesti"})
	s.goOnline(driverID, 1, 1)
	s.assigned("pln-1", driverID)

	// the admin asks: the plan is pending and console-api is asked, in the admin's language
	status, body := s.callWith(s.adminToken, "plans.cancel", map[string]any{"id": "pln-1", "reason": "customer called"}, map[string]string{"Accept-Language": "id"})
	if status != http.StatusOK || !strings.Contains(string(body), `"pendingAction":"cancel"`) {
		t.Fatalf("plans.cancel: %d %s, want the plan back with its pending action", status, body)
	}
	var asked []contracts.Envelope
	for _, env := range records(t, s.brokers, contracts.TopicPlan, 2) {
		if env.Type == contracts.TypePlanCancelAsked {
			asked = append(asked, env)
		}
	}
	if len(asked) != 1 || asked[0].Key != "pln-1" {
		t.Fatalf("plan.cancel.asked = %+v, want one keyed by the plan", asked)
	}
	if ev, _ := asked[0].Payload[contracts.PlanCancelAsked](); ev.Reason != "customer called" || ev.AskedBy != "Rani Wulandari" || ev.Locale != "id" || ev.PlanID != "pln-1" {
		t.Errorf("plan.cancel.asked = %+v", ev)
	}
	status, body = s.call(s.adminToken, "plans.cancel", map[string]any{"id": "pln-1"})
	if status != http.StatusConflict || !strings.Contains(string(body), "srv.api.cancelPending") {
		t.Errorf("a second cancel: %d %s, want a conflict saying one is pending", status, body)
	}

	// console-api freezes the plan: the route is held for the request, and an admin cannot take it back meanwhile
	s.produce("console-api", contracts.TypePlanCancelRequested, "pln-1", 2, contracts.PlanCancelRequested{
		PlanID: "pln-1", RequestID: "req-1", RequestedBy: contracts.CancelByAdmin, RouteIDs: []string{"pln-1:r1"},
	})
	eventually(t, "the route to be held for the request", func() bool {
		var held int
		if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM routes WHERE plan_id = 'pln-1' AND hold = 'req-1'`).Scan(&held); err != nil {
			t.Fatal(err)
		}
		return held == 1
	})
	if status, body := s.call(s.adminToken, "dispatch.unassign", map[string]any{"planId": "pln-1", "routeId": "pln-1:r1"}); status != http.StatusConflict || !strings.Contains(string(body), "srv.ops.err.routeFrozen") {
		t.Errorf("taking back a frozen route: %d %s, want 409 with the frozen message", status, body)
	}

	// the vote came in and console-api commits: the plan is cancelled and the driver is free again
	s.produce("console-api", contracts.TypePlanCancelled, "pln-1", 3, contracts.PlanCancelled{
		PlanID: "pln-1", RequestID: "req-1", RequestedBy: contracts.CancelByAdmin, Reason: "customer called",
	})
	plan := s.waitPlan("pln-1", "the plan to be cancelled", func(p contracts.Plan) bool { return p.Status == contracts.PlanStatusCancelled })
	if plan.PendingAction != "" || plan.CancelReason != "customer called" || plan.Routes[0].DriverID != "" || plan.Routes[0].Status != contracts.RouteStatusAwaitingDriver {
		t.Errorf("cancelled plan: pending %q, reason %q, route %s with driver %q", plan.PendingAction, plan.CancelReason, plan.Routes[0].Status, plan.Routes[0].DriverID)
	}
	eventually(t, "the driver to be free", func() bool { return s.driverStatus(driverID) == "online" })
	if got := records(t, s.brokers, contracts.TopicDispatch, 1); len(got) != 1 || got[0].Type != contracts.TypeRouteAssigned {
		t.Errorf("dispatch topic = %+v, want only the assignment: a cancel releases the driver without route.unassigned", got)
	}
	var list []map[string]any
	s.admin("plans.list", map[string]any{"filter": map[string]any{"needsDriver": true}}, &list)
	if len(list) != 0 {
		t.Errorf("queue = %v, want the cancelled plan out of it", list)
	}
	status, body = s.call(s.adminToken, "plans.cancel", map[string]any{"id": "pln-1"})
	if status != http.StatusConflict || !strings.Contains(string(body), "srv.api.planInactive") {
		t.Errorf("cancelling a cancelled plan: %d %s", status, body)
	}
}

func TestACustomersCancelNotifiesTheAdminsAndAnAbortFreesTheRoute(t *testing.T) {
	s := start(t, nil)
	driverID := s.setup(contracts.VehicleTypeMotor, "B 3412 KJR", "081234567801")
	s.produce("console-api", contracts.TypeCustomerRegistered, "cus-1", 1, contracts.CustomerRegistered{CustomerID: "cus-1", Name: "Dina Pramesti"})
	s.confirm("pln-1", nowRoute("pln-1", 1, contracts.VehicleTypeMotor))
	s.confirm("pln-2", nowRoute("pln-2", 1, contracts.VehicleTypeMotor))
	s.waitPlan("pln-2", "both plans to be projected", func(contracts.Plan) bool { return true })
	s.waitPlan("pln-1", "both plans to be projected", func(contracts.Plan) bool { return true })

	// the customer asks to cancel pln-1: its route is frozen while a driver comes online and the worker looks
	s.produce("console-api", contracts.TypePlanCancelRequested, "pln-1", 2, contracts.PlanCancelRequested{
		PlanID: "pln-1", RequestID: "req-1", RequestedBy: contracts.CancelByCustomer, RouteIDs: []string{"pln-1:r1"},
	})
	s.waitPlan("pln-1", "the plan to be pending", func(p contracts.Plan) bool { return p.PendingAction == "cancel" })
	s.goOnline(driverID, 1, 1)
	s.waitPlan("pln-2", "the other plan to get the driver", func(p contracts.Plan) bool { return p.Routes[0].DriverID == driverID })
	if plan, _ := s.plan("pln-1"); plan.Routes[0].DriverID != "" {
		t.Fatalf("the frozen route went to %q", plan.Routes[0].DriverID)
	}
	var list []map[string]any
	s.admin("plans.list", map[string]any{"filter": map[string]any{"status": []string{"menunggu_driver"}}}, &list)
	if len(list) != 1 || list[0]["pendingAction"] != "cancel" {
		t.Errorf("list = %v, want the pending plan to say so", list)
	}

	// console-api gives up on that attempt: the route is free, and taken by a driver who is there
	other := s.setup(contracts.VehicleTypeMotor, "B 7777 ZZZ", "081234567802")
	s.goOnline(other, 1, 1)
	s.produce("console-api", contracts.TypePlanCancelAborted, "pln-1", 3, contracts.PlanCancelAborted{PlanID: "pln-1", RequestID: "req-1", Reason: contracts.Msg{Key: "srv.api.cancelRunning"}})
	plan := s.waitPlan("pln-1", "the route to be assigned after the abort", func(p contracts.Plan) bool { return p.Routes[0].DriverID == other })
	if plan.PendingAction != "" {
		t.Errorf("pending %q after the abort", plan.PendingAction)
	}

	// a second attempt that commits, asked for by the customer: the admins hear of it
	s.produce("console-api", contracts.TypePlanCancelRequested, "pln-1", 4, contracts.PlanCancelRequested{PlanID: "pln-1", RequestID: "req-2", RequestedBy: contracts.CancelByCustomer})
	s.produce("console-api", contracts.TypePlanCancelled, "pln-1", 5, contracts.PlanCancelled{PlanID: "pln-1", RequestID: "req-2", RequestedBy: contracts.CancelByCustomer, Reason: "plans changed", RefundRp: 20000})
	s.waitPlan("pln-1", "the plan to be cancelled", func(p contracts.Plan) bool { return p.Status == contracts.PlanStatusCancelled })
	n := s.waitNotification("the admins to be told of the cancel", "srv.api.notif.cancelled.title")
	if n.PlanID != "pln-1" || n.Title.Params["code"] != "PL-pln-1" || n.Body.Params["name"] != "Dina Pramesti" || n.Read {
		t.Errorf("notification = %+v", n)
	}
}

func TestAnAdminHearsThatConsoleRefusedTheCancel(t *testing.T) {
	s := start(t, nil)
	s.confirm("pln-1", nowRoute("pln-1", 1, contracts.VehicleTypeMotor))
	s.waitPlan("pln-1", "the plan to be projected", func(contracts.Plan) bool { return true })
	s.admin("plans.cancel", map[string]any{"id": "pln-1"}, nil)
	s.waitPlan("pln-1", "the plan to be pending", func(p contracts.Plan) bool { return p.PendingAction == "cancel" })

	s.produce("console-api", contracts.TypePlanCancelRejected, "pln-1", 0, contracts.PlanCancelRejected{PlanID: "pln-1", Reason: contracts.Msg{Key: "srv.api.cancelRunning"}})
	plan := s.waitPlan("pln-1", "the plan to be released from the pending cancel", func(p contracts.Plan) bool { return p.PendingAction == "" })
	if plan.Status != contracts.PlanStatusAwaitingDriver {
		t.Errorf("plan %s, want it as it was", plan.Status)
	}
	n := s.waitNotification("the refusal to reach the admins", "srv.api.notif.cancelRefused.title")
	if n.PlanID != "pln-1" || n.Body.Key != "srv.api.notif.cancelRefused.body" {
		t.Errorf("notification = %+v", n)
	}
}

func TestAReschedulePutsTheRouteBackInTheQueueAndTheWorkerAssignsItAgain(t *testing.T) {
	s := start(t, nil)
	driverID := s.setup(contracts.VehicleTypeMotor, "B 3412 KJR", "081234567801")
	s.goOnline(driverID, 1, 1)
	s.assigned("pln-1", driverID)

	plan, _ := s.plan("pln-1")
	moved := contracts.PlanRescheduled{PlanID: "pln-1", Schedule: contracts.Schedule{Mode: contracts.ScheduleModeNow}}
	for _, r := range plan.Routes {
		r.StartAt = contracts.At(time.Now().Add(6 * time.Minute))
		moved.Routes = append(moved.Routes, r)
	}
	s.produce("console-api", contracts.TypePlanRescheduled, "pln-1", 2, moved)

	events := records(t, s.brokers, contracts.TopicDispatch, 3)
	var types []string
	for _, e := range events {
		types = append(types, e.Type)
	}
	if len(events) != 3 || types[0] != contracts.TypeRouteAssigned || types[1] != contracts.TypeRouteUnassigned || types[2] != contracts.TypeRouteAssigned {
		t.Fatalf("dispatch topic = %v, want assigned, unassigned (rescheduled), assigned again", types)
	}
	if ev, _ := events[1].Payload[contracts.RouteUnassigned](); ev.Reason != contracts.UnassignRescheduled || events[1].AggregateVersion != 2 || events[2].AggregateVersion != 3 {
		t.Errorf("unassigned %+v at version %d, reassigned at %d; want reason rescheduled, versions 2 and 3", ev, events[1].AggregateVersion, events[2].AggregateVersion)
	}
	if got, _ := s.plan("pln-1"); got.Routes[0].DriverID != driverID || got.Status != contracts.PlanStatusAssigned {
		t.Errorf("plan %s, driver %q, want him back on it", got.Status, got.Routes[0].DriverID)
	}
}

func TestADriverGoingOfflineReturnsHisRouteAndTheAdminsAreTold(t *testing.T) {
	s := start(t, nil)
	driverID := s.setup(contracts.VehicleTypeMotor, "B 3412 KJR", "081234567801")
	s.goOnline(driverID, 1, 1)
	s.assigned("pln-1", driverID)

	s.produce("driver-api", contracts.TypeDriverOffline, driverID, 2, contracts.DriverOffline{DriverID: driverID})
	plan := s.waitPlan("pln-1", "the route to be returned", func(p contracts.Plan) bool { return p.Routes[0].DriverID == "" })
	if plan.Status != contracts.PlanStatusAwaitingDriver {
		t.Errorf("plan %s, want menunggu_driver", plan.Status)
	}
	events := records(t, s.brokers, contracts.TopicDispatch, 2)
	if len(events) != 2 || events[1].Type != contracts.TypeRouteUnassigned {
		t.Fatalf("dispatch topic = %+v, want the assignment, then the release", events)
	}
	if ev, _ := events[1].Payload[contracts.RouteUnassigned](); ev.Reason != contracts.UnassignDriverOffline || ev.DriverID != driverID {
		t.Errorf("route.unassigned = %+v", ev)
	}
	n := s.waitNotification("the admins to hear about the returned job", "srv.api.notif.released.title")
	if n.Body.Params["name"] != "Budi Santoso" || n.Body.Params["count"] != float64(1) {
		t.Errorf("notification = %+v", n)
	}
}

func TestSilentDriversAreReleasedByTheWorkerOnItsOwnClock(t *testing.T) {
	var offset atomic.Int64 // nanoseconds the worker's clock is ahead
	clock := func() time.Time { return time.Now().Add(time.Duration(offset.Load())) }
	s := start(t, func(c *app.Config) { c.Now = clock })
	driverID := s.setup(contracts.VehicleTypeMotor, "B 3412 KJR", "081234567801")
	s.goOnline(driverID, 1, 1)
	s.assigned("pln-1", driverID)

	// minutes pass without a word from the driver app (this test owns offset: the worker only reads the clock)
	offset.Store(int64(10 * time.Minute))
	plan := s.waitPlan("pln-1", "the silent driver to lose the route", func(p contracts.Plan) bool { return p.Routes[0].DriverID == "" })
	if plan.Status != contracts.PlanStatusAwaitingDriver {
		t.Errorf("plan %s", plan.Status)
	}
	events := records(t, s.brokers, contracts.TopicDispatch, 2)
	if len(events) != 2 {
		t.Fatalf("dispatch topic = %+v, want assignment and release", events)
	}
	if ev, _ := events[1].Payload[contracts.RouteUnassigned](); ev.Reason != contracts.UnassignDriverSilent {
		t.Errorf("route.unassigned = %+v, want reason driver_silent", ev)
	}
}

func TestWalletEventsFeedTheFinancePagesAndTheUsersList(t *testing.T) {
	s := start(t, nil)
	s.produce("console-api", contracts.TypeCustomerRegistered, "cus-1", 1, contracts.CustomerRegistered{
		CustomerID: "cus-1", Name: "Dina Pramesti", Email: "dina@contoh.id", Phone: "081234567890", CreatedAt: contracts.Now(),
	})
	created := time.Now().Add(-time.Hour)
	top := contracts.TopUp{
		ID: "top-1", UserID: "cus-1", AmountRp: 100_000, Method: contracts.TopUpMethodQRIS, Status: contracts.TopUpStatusPending,
		CreatedAt: contracts.At(created), ExpiresAt: contracts.At(created.Add(24 * time.Hour)), Reference: "LOONG-000001",
	}
	s.produce("console-api", contracts.TypeTopUpCreated, "cus-1", 1, contracts.TopUpCreated{TopUp: top})
	s.confirm("pln-1", nowRoute("pln-1", 1, contracts.VehicleTypeMotor))
	top.Status, top.PaidAt = contracts.TopUpStatusSucceeded, contracts.Now()
	s.produce("console-api", contracts.TypeTopUpPaid, "cus-1", 2, contracts.TopUpPaid{TopUp: top, TxID: "wtx-1", BalanceAfterRp: 100_000})
	s.produce("console-api", contracts.TypeWalletCharged, "cus-1", 3, contracts.WalletCharged{TxID: "wtx-2", CustomerID: "cus-1", PlanID: "pln-1", AmountRp: 20_000, BalanceAfterRp: 80_000})

	var overview map[string]any
	eventually(t, "the wallet events to be projected", func() bool {
		s.admin("finance.overview", map[string]any{}, &overview)
		return overview["totalBalanceRp"] == float64(80_000)
	})
	if overview["usersWithBalance"] != float64(1) || overview["paidTodayRp"] != float64(100_000) || overview["paid30dCount"] != float64(1) || overview["pendingCount"] != float64(0) {
		t.Errorf("overview = %v", overview)
	}

	var topUps []map[string]any
	s.admin("finance.topUps", map[string]any{"filter": map[string]any{"status": []string{"berhasil"}, "q": "dina"}}, &topUps)
	if len(topUps) != 1 || topUps[0]["userName"] != "Dina Pramesti" || topUps[0]["userEmail"] != "dina@contoh.id" || topUps[0]["id"] != "top-1" {
		t.Errorf("top-ups = %v", topUps)
	}
	var ledger []map[string]any
	s.admin("finance.transactions", map[string]any{"userId": "cus-1"}, &ledger)
	if len(ledger) != 2 || ledger[0]["id"] != "wtx-2" || ledger[0]["amountRp"] != float64(-20_000) || ledger[1]["balanceAfterRp"] != float64(100_000) {
		t.Errorf("ledger = %v", ledger)
	}
	if status, body := s.call(s.adminToken, "finance.transactions", map[string]any{"userId": "cus-404"}); status != http.StatusNotFound {
		t.Errorf("unknown customer: %d %s, want 404", status, body)
	}

	var users []map[string]any
	eventually(t, "the customer in the users list", func() bool {
		s.admin("users.list", map[string]any{"q": "dina"}, &users)
		return len(users) == 1 && users[0]["plansCount"] == float64(1)
	})
	if users[0]["balanceRp"] != float64(80_000) || users[0]["blocked"] != false || users[0]["totalSpendRp"] != float64(0) {
		t.Errorf("users = %v, want the balance of the wallet projection and no spend (the plan is not finished)", users)
	}
	var detail struct {
		User    map[string]any   `json:"user"`
		Summary map[string]any   `json:"summary"`
		Plans   []map[string]any `json:"plans"`
	}
	s.admin("users.get", map[string]any{"id": "cus-1"}, &detail)
	if detail.User["email"] != "dina@contoh.id" || len(detail.Plans) != 1 || detail.Plans[0]["id"] != "pln-1" {
		t.Errorf("detail = %+v", detail)
	}

	// blocking is announced for console-api to enforce
	var blocked map[string]any
	s.admin("users.setBlocked", map[string]any{"id": "cus-1", "blocked": true}, &blocked)
	if blocked["blocked"] != true || blocked["id"] != "cus-1" {
		t.Errorf("setBlocked = %v", blocked)
	}
	var seen []contracts.Envelope
	for _, env := range records(t, s.brokers, contracts.TopicCustomer, 2) {
		if env.Type == contracts.TypeCustomerBlocked {
			seen = append(seen, env)
		}
	}
	if len(seen) != 1 || seen[0].Key != "cus-1" || seen[0].Source != "backoffice-api" {
		t.Errorf("customer.blocked = %+v, want one from backoffice-api keyed by the customer", seen)
	}
}

func TestNotificationsAreWrittenReadAndAnnounced(t *testing.T) {
	s := start(t, nil)
	s.admin("settings.update", map[string]any{"patch": map[string]any{"dispatch": map[string]any{"auto": false}}}, nil)

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
	events := make(chan string, 32)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			if data, ok := strings.CutPrefix(scanner.Text(), "data: "); ok {
				events <- data
			}
		}
	}()
	waitFor := func(want string) {
		t.Helper()
		deadline := time.After(15 * time.Second)
		for {
			select {
			case data := <-events:
				if data == want {
					return
				}
			case <-deadline:
				t.Fatalf("no %s on the event stream", want)
			}
		}
	}

	s.produce("console-api", contracts.TypeCustomerRegistered, "cus-1", 1, contracts.CustomerRegistered{CustomerID: "cus-1", Name: "Dina Pramesti"})
	s.confirm("pln-1", nowRoute("pln-1", 1, contracts.VehicleTypeMotor))
	waitFor(`{"type":"notifications.changed"}`)
	n := s.waitNotification("a plan that needs a driver", "srv.api.notif.newPlan.title")
	if n.PlanID != "pln-1" || n.Title.Params["code"] != "PL-pln-1" || n.Body.Params["name"] != "Dina Pramesti" || n.Body.Params["count"] != float64(1) || n.Read || n.Audience != "admin" || n.At.IsZero() {
		t.Fatalf("notification = %+v", n)
	}
	s.waitNotification("the sign-up", "srv.api.notif.newUser.title")
	time.Sleep(400 * time.Millisecond) // let the hints of the two notifications go by
	for len(events) > 0 {
		<-events
	}

	// read one: it is marked and the other consoles are told
	s.admin("notifications.markRead", map[string]any{"ids": []string{n.ID}}, nil)
	waitFor(`{"type":"notifications.changed"}`)
	for _, got := range s.notifications() {
		if got.ID == n.ID && !got.Read {
			t.Errorf("notification %s is not read after markRead", got.ID)
		}
		if got.ID != n.ID && got.Read {
			t.Errorf("notification %s was marked read too", got.ID)
		}
	}
	s.admin("notifications.markRead", map[string]any{}, nil)
	for _, got := range s.notifications() {
		if !got.Read {
			t.Errorf("notification %s is unread after marking everything read", got.ID)
		}
	}
	if status, _ := s.call(s.adminToken, "notifications.markRead", map[string]any{"ids": []string{"ntf-none"}}); status != http.StatusOK {
		t.Errorf("marking an unknown id read: %d, want it ignored", status)
	}
}
