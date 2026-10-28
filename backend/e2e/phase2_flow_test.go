//go:build e2e

package e2e

import (
	"strconv"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

// The features of phase 2 (docs/phase2.md) across the three services: cancelling and rescheduling plans, drivers
// who leave, notifications, users and finance. Each test starts from clean databases and topics.

const (
	customerEmail    = "dina@loong.test"
	customerPassword = "dina-secret-1"
)

// assignedPlan confirms a quoted plan and waits until every route has a driver.
func (w *world) assignedPlan(c *session) (contracts.Plan, int64) {
	w.t.Helper()
	plan, cost := w.confirmedPlan(c)
	eventually(w.t, "every route assigned", 40*time.Second, func() (bool, string) {
		p := w.plan(c, plan.ID)
		for _, r := range p.Routes {
			if r.DriverID == "" {
				return false, "route " + r.ID + " has no driver (plan " + string(p.Status) + ")"
			}
		}
		return p.Status == contracts.PlanStatusAssigned, "plan status " + string(p.Status)
	})
	return w.plan(c, plan.ID), cost
}

func (w *world) driverTasks(d *session) int {
	w.t.Helper()
	var tasks []struct {
		PlanID string `json:"planId"`
	}
	d.must("driver.routes", nil, &tasks)
	return len(tasks)
}

func (w *world) notifications(s *session) []contracts.AppNotification {
	w.t.Helper()
	var list []contracts.AppNotification
	s.must("notifications.list", nil, &list)
	return list
}

func (w *world) driverStatus(admin *session, id string) string {
	w.t.Helper()
	var drivers []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	admin.must("fleet.drivers.list", nil, &drivers)
	for _, d := range drivers {
		if d.ID == id {
			return d.Status
		}
	}
	return "unknown"
}

func TestACustomerCancelsAnAssignedPlanAndIsRefundedOnce(t *testing.T) {
	w := newWorld(t)
	t.Cleanup(func() {
		if t.Failed() {
			w.dump()
		}
	})
	admin := w.admin()
	driverID := w.provisionFleet(admin)
	d := w.goOnline()
	c := w.customer()
	plan, cost := w.assignedPlan(c)
	if got := w.balance(c); got != 200000-cost {
		t.Fatalf("balance after paying = %d, want %d", got, 200000-cost)
	}
	eventually(t, "the driver to be on duty with the task", 30*time.Second, func() (bool, string) {
		return w.driverTasks(d) > 0 && w.driverStatus(admin, driverID) == "bertugas", "the task has not reached the driver"
	})

	var cancelling contracts.Plan
	c.must("plans.cancel", map[string]any{"id": plan.ID, "reason": "salah alamat"}, &cancelling)
	if cancelling.Status == contracts.PlanStatusCancelled && cancelling.PendingAction != "" {
		t.Errorf("a cancelled plan has nothing pending: %+v", cancelling)
	}
	if err := c.call("plans.cancel", map[string]any{"id": plan.ID}, nil); err == nil && w.plan(c, plan.ID).Status != contracts.PlanStatusCancelled {
		t.Error("cancelling a plan whose cancel is in flight must be refused")
	}

	eventually(t, "the plan cancelled", 40*time.Second, func() (bool, string) {
		p := w.plan(c, plan.ID)
		return p.Status == contracts.PlanStatusCancelled && p.PendingAction == "", "plan " + string(p.Status) + " pending=" + p.PendingAction
	})
	if p := w.plan(c, plan.ID); p.CancelReason != "salah alamat" {
		t.Errorf("cancel reason = %q", p.CancelReason)
	}
	eventually(t, "the refund", 20*time.Second, func() (bool, string) {
		got := w.balance(c)
		return got == 200000, "balance " + itoa(got)
	})
	time.Sleep(2 * time.Second) // a duplicate refund would have landed by now
	if got := w.balance(c); got != 200000 {
		t.Errorf("balance after the cancel = %d, want 200000 (refunded exactly once)", got)
	}

	eventually(t, "the driver's task gone and the driver free", 30*time.Second, func() (bool, string) {
		n, st := w.driverTasks(d), w.driverStatus(admin, driverID)
		return n == 0 && st == "online", itoa(int64(n)) + " tasks, driver " + st
	})
	eventually(t, "the back office to see the plan cancelled", 30*time.Second, func() (bool, string) {
		var p contracts.Plan
		if err := admin.call("plans.get", map[string]any{"id": plan.ID}, &p); err != nil {
			return false, err.Error()
		}
		return p.Status == contracts.PlanStatusCancelled && p.PendingAction == "", "plan " + string(p.Status)
	})

	eventually(t, "the notifications of the three audiences", 30*time.Second, func() (bool, string) {
		cn, dn := w.notifications(c), w.notifications(d)
		return len(cn) >= 2 && len(dn) >= 2, itoa(int64(len(cn))) + " customer, " + itoa(int64(len(dn))) + " driver notifications"
	})
	cn := w.notifications(c)
	ids := []string{cn[0].ID}
	c.must("notifications.markRead", map[string]any{"ids": ids}, nil)
	if after := w.notifications(c); !after[0].Read || after[len(after)-1].Read {
		t.Errorf("markRead(ids) marks only those: %+v", after)
	}

	if dl := deadLetters(w.ctx, t, w.brokers); len(dl) > 0 {
		t.Errorf("events were dead-lettered: %v", dl)
	}
}

func TestACancelIsRefusedOnceTheDriverHasStarted(t *testing.T) {
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
	plan, cost := w.assignedPlan(c)

	var tasks []struct {
		PlanID string          `json:"planId"`
		Route  contracts.Route `json:"route"`
	}
	eventually(t, "the task at the driver", 30*time.Second, func() (bool, string) {
		d.must("driver.routes", nil, &tasks)
		return len(tasks) > 0, "no task yet"
	})
	d.must("driver.startRoute", map[string]any{"planId": tasks[0].PlanID, "routeId": tasks[0].Route.ID}, nil)

	eventually(t, "the console to see the plan running", 30*time.Second, func() (bool, string) {
		p := w.plan(c, plan.ID)
		return p.Status == contracts.PlanStatusRunning, "plan " + string(p.Status)
	})
	if err := c.call("plans.cancel", map[string]any{"id": plan.ID}, nil); err == nil {
		t.Fatal("a running plan must not be cancellable")
	}
	if err := admin.call("plans.cancel", map[string]any{"id": plan.ID}, nil); err == nil {
		t.Fatal("a running plan must not be cancellable by an admin either")
	}
	if got := w.balance(c); got != 200000-cost {
		t.Errorf("a refused cancel must not move money: balance %d, want %d", got, 200000-cost)
	}
	if p := w.plan(c, plan.ID); p.PendingAction != "" || p.Status != contracts.PlanStatusRunning {
		t.Errorf("a refused cancel leaves the plan alone: %s pending=%q", p.Status, p.PendingAction)
	}
}

func TestAnAdminCancelsAPlanFromTheBackOffice(t *testing.T) {
	w := newWorld(t)
	t.Cleanup(func() {
		if t.Failed() {
			w.dump()
		}
	})
	admin := w.admin()
	w.provisionFleet(admin)
	w.goOnline()
	c := w.customer()
	plan, _ := w.assignedPlan(c)

	var asked contracts.Plan
	admin.must("plans.cancel", map[string]any{"id": plan.ID, "reason": "pelanggan menelepon"}, &asked)

	eventually(t, "the plan cancelled at the console", 40*time.Second, func() (bool, string) {
		p := w.plan(c, plan.ID)
		return p.Status == contracts.PlanStatusCancelled && p.PendingAction == "", "plan " + string(p.Status) + " pending=" + p.PendingAction
	})
	if p := w.plan(c, plan.ID); p.CancelReason != "pelanggan menelepon" {
		t.Errorf("cancel reason = %q", p.CancelReason)
	}
	eventually(t, "the refund", 20*time.Second, func() (bool, string) {
		got := w.balance(c)
		return got == 200000, "balance " + itoa(got)
	})
	eventually(t, "the back office to settle", 30*time.Second, func() (bool, string) {
		var p contracts.Plan
		if err := admin.call("plans.get", map[string]any{"id": plan.ID}, &p); err != nil {
			return false, err.Error()
		}
		return p.Status == contracts.PlanStatusCancelled && p.PendingAction == "", "plan " + string(p.Status) + " pending=" + p.PendingAction
	})
}

func TestReschedulingSendsTheRoutesBackThroughDispatch(t *testing.T) {
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
	plan, _ := w.assignedPlan(c)

	from := time.Now().Add(3 * time.Hour).Truncate(time.Minute)
	schedule := contracts.Schedule{Mode: contracts.ScheduleModeScheduled, Window: &contracts.TimeWindow{From: contracts.At(from), To: contracts.At(from.Add(time.Hour))}}
	var moved contracts.Plan
	c.must("plans.reschedule", map[string]any{"id": plan.ID, "schedule": schedule}, &moved)
	if moved.Schedule.Mode != contracts.ScheduleModeScheduled {
		t.Errorf("the answer carries the new schedule: %+v", moved.Schedule)
	}
	// too soon is refused with the mock's rule
	soon := contracts.Schedule{Mode: contracts.ScheduleModeScheduled, Window: &contracts.TimeWindow{From: contracts.At(time.Now().Add(5 * time.Minute)), To: contracts.At(time.Now().Add(time.Hour))}}
	if err := c.call("plans.reschedule", map[string]any{"id": plan.ID, "schedule": soon}, nil); err == nil {
		t.Error("a schedule less than 20 minutes ahead must be refused")
	}

	eventually(t, "the routes re-timed and assigned again", 60*time.Second, func() (bool, string) {
		p := w.plan(c, plan.ID)
		for _, r := range p.Routes {
			if r.DriverID == "" {
				return false, "route " + r.ID + " has no driver (plan " + string(p.Status) + ")"
			}
			if got := r.StartAt.Time; got.Sub(from).Abs() > 2*time.Second {
				return false, "route starts at " + got.Format(time.RFC3339) + ", want " + from.Format(time.RFC3339)
			}
		}
		return p.Schedule.Mode == contracts.ScheduleModeScheduled && p.Status == contracts.PlanStatusAssigned, "plan " + string(p.Status)
	})
	if w.driverTasks(d) == 0 {
		t.Error("the driver has the re-timed task")
	}
	var done contracts.Plan
	if err := c.call("plans.reschedule", map[string]any{"id": "pln-unknown", "schedule": schedule}, &done); err == nil {
		t.Error("an unknown plan cannot be rescheduled")
	}
}

func TestADriverWhoGoesOfflineReleasesTheirRoutes(t *testing.T) {
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
	plan, _ := w.assignedPlan(c)
	eventually(t, "the task at the driver", 30*time.Second, func() (bool, string) { return w.driverTasks(d) > 0, "no task yet" })

	d.must("driver.setOnline", map[string]any{"online": false}, nil)
	eventually(t, "the routes back in the queue", 40*time.Second, func() (bool, string) {
		p := w.plan(c, plan.ID)
		for _, r := range p.Routes {
			if r.DriverID != "" {
				return false, "route " + r.ID + " still belongs to a driver"
			}
		}
		return p.Status == contracts.PlanStatusAwaitingDriver, "plan " + string(p.Status)
	})
	eventually(t, "the task gone from the driver", 30*time.Second, func() (bool, string) { return w.driverTasks(d) == 0, "task still listed" })
	eventually(t, "an admin notification", 30*time.Second, func() (bool, string) {
		return len(w.notifications(admin)) > 0, "no notification"
	})

	d.must("driver.setOnline", map[string]any{"online": true}, nil)
	d.must("driver.ping", map[string]any{"position": map[string]any{"lat": -6.2610, "lng": 106.8130}, "speedKmh": 0}, nil)
	eventually(t, "dispatch to find the driver again", 40*time.Second, func() (bool, string) {
		p := w.plan(c, plan.ID)
		return p.Status == contracts.PlanStatusAssigned, "plan " + string(p.Status)
	})
}

func TestUsersAndFinanceFollowTheWalletAndBlockingWorks(t *testing.T) {
	w := newWorld(t)
	t.Cleanup(func() {
		if t.Failed() {
			w.dump()
		}
	})
	admin := w.admin()
	w.provisionFleet(admin)
	w.goOnline()
	c := w.customer()
	_, cost := w.confirmedPlan(c)

	type summary struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Blocked    bool   `json:"blocked"`
		PlansCount int    `json:"plansCount"`
		BalanceRp  int64  `json:"balanceRp"`
	}
	var user summary
	eventually(t, "the customer with the balance in the back office", 40*time.Second, func() (bool, string) {
		var list []summary
		if err := admin.call("users.list", nil, &list); err != nil {
			return false, err.Error()
		}
		for _, u := range list {
			if u.Name == "Dina Pelanggan" {
				user = u
				return u.BalanceRp == 200000-cost && u.PlansCount == 1, "balance " + itoa(u.BalanceRp) + ", plans " + itoa(int64(u.PlansCount))
			}
		}
		return false, "customer not listed"
	})

	// what the chosen option saved is shared with the back office, whose list rows (and Analytics) show it
	eventually(t, "the savings of the plan in the back office list", 30*time.Second, func() (bool, string) {
		var rows []struct {
			SavingsRp  *int64   `json:"savingsRp"`
			SavingsPct *float64 `json:"savingsPct"`
		}
		if err := admin.call("plans.list", nil, &rows); err != nil {
			return false, err.Error()
		}
		if len(rows) != 1 || rows[0].SavingsRp == nil || rows[0].SavingsPct == nil {
			return false, "no savings on the row yet"
		}
		return *rows[0].SavingsRp > 0 && *rows[0].SavingsPct > 0, "batching three parcels saves money, got " + itoa(*rows[0].SavingsRp)
	})

	var overview struct {
		TotalBalanceRp   int64 `json:"totalBalanceRp"`
		UsersWithBalance int   `json:"usersWithBalance"`
		PaidTodayRp      int64 `json:"paidTodayRp"`
		PaidTodayCount   int   `json:"paidTodayCount"`
	}
	admin.must("finance.overview", nil, &overview)
	if overview.TotalBalanceRp != 200000-cost || overview.UsersWithBalance != 1 || overview.PaidTodayRp != 200000 || overview.PaidTodayCount != 1 {
		t.Errorf("overview = %+v, want balance %d, one user, 200000 paid today in one top-up", overview, 200000-cost)
	}
	var topUps []struct {
		AmountRp int64  `json:"amountRp"`
		Status   string `json:"status"`
	}
	admin.must("finance.topUps", nil, &topUps)
	if len(topUps) != 1 || topUps[0].AmountRp != 200000 {
		t.Errorf("top-ups = %+v", topUps)
	}
	var txs []struct {
		Kind      string `json:"kind"`
		AmountRp  int64  `json:"amountRp"`
		BalanceRp int64  `json:"balanceAfterRp"`
	}
	admin.must("finance.transactions", map[string]any{"userId": user.ID}, &txs)
	if len(txs) != 2 {
		t.Fatalf("ledger = %+v, want the top-up and the payment", txs)
	}

	// blocking: the customer is refused at sign-in and loses the session; unblocking lets them back in
	var blocked summary
	admin.must("users.setBlocked", map[string]any{"id": user.ID, "blocked": true}, &blocked)
	if !blocked.Blocked {
		t.Errorf("setBlocked answers the new state: %+v", blocked)
	}
	eventually(t, "the console to refuse the blocked customer", 30*time.Second, func() (bool, string) {
		s := &session{t: t, base: w.consoleURL}
		return s.call("auth.loginCustomer", map[string]any{"email": customerEmail, "password": customerPassword}, nil) != nil, "sign-in still works"
	})
	if err := c.call("wallet.get", nil, nil); err == nil {
		t.Error("a blocked customer's session must stop working")
	}
	admin.must("users.setBlocked", map[string]any{"id": user.ID, "blocked": false}, nil)
	eventually(t, "the unblocked customer back in", 30*time.Second, func() (bool, string) {
		s := &session{t: t, base: w.consoleURL}
		return s.call("auth.loginCustomer", map[string]any{"email": customerEmail, "password": customerPassword}, nil) == nil, "sign-in still refused"
	})
}

type settings struct {
	AI       contracts.AIFeatures       `json:"ai"`
	Dispatch contracts.DispatchSettings `json:"dispatch"`
}

func TestSettingsAndAccountAreServedByTheConsole(t *testing.T) {
	w := newWorld(t)
	admin := w.admin()
	c := w.customer()

	// the admin turns auto-dispatch off; the console's settings follow through Kafka
	admin.must("settings.update", map[string]any{"patch": map[string]any{"dispatch": map[string]any{"auto": false}}}, nil)
	eventually(t, "the console to learn the setting", 30*time.Second, func() (bool, string) {
		var s settings
		if err := c.call("settings.get", nil, &s); err != nil {
			return false, err.Error()
		}
		return !s.Dispatch.Auto, "dispatch.auto still on"
	})
	var s settings
	c.must("settings.get", nil, &s)
	if s.AI.Copilot || s.AI.Vision || s.AI.Voice || s.AI.Intent {
		t.Errorf("without a configured AI gateway every AI flag is off: %+v", s.AI)
	}

	// profile and saved addresses live in the console; the back office hears about a renamed customer
	var profile struct {
		Name           string `json:"name"`
		SavedAddresses []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"savedAddresses"`
	}
	c.must("account.update", map[string]any{"patch": map[string]any{"name": "Dina P."}}, &profile)
	if profile.Name != "Dina P." {
		t.Errorf("update answers the new profile: %+v", profile)
	}
	var added struct {
		ID string `json:"id"`
	}
	c.must("account.addresses.add", map[string]any{"input": map[string]any{
		"label": "Kantor", "contactName": "Dina", "contactPhone": "081200000001",
		"place": map[string]any{"name": "SCBD", "address": "SCBD, Jakarta", "position": map[string]any{"lat": -6.2266, "lng": 106.8096}},
	}}, &added)
	c.must("account.profile", nil, &profile)
	if len(profile.SavedAddresses) != 1 || profile.SavedAddresses[0].ID != added.ID {
		t.Errorf("profile = %+v, want the saved address", profile)
	}
	c.must("account.addresses.remove", map[string]any{"id": added.ID}, nil)
	c.must("account.profile", nil, &profile)
	if len(profile.SavedAddresses) != 0 {
		t.Errorf("profile after remove = %+v", profile)
	}
	eventually(t, "the back office to show the new name", 30*time.Second, func() (bool, string) {
		var list []struct {
			Name string `json:"name"`
		}
		if err := admin.call("users.list", nil, &list); err != nil {
			return false, err.Error()
		}
		return len(list) == 1 && list[0].Name == "Dina P.", "users: " + itoa(int64(len(list)))
	})
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
