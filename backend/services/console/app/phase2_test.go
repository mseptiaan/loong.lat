package app

import (
	"encoding/json/v2"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/account"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/plans"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/settings"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/testkit"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/wallet"
)

// confirmedPlan takes a plan of the caller's through a top-up, a quote and a paid confirmation: they topped up
// 50 000 and the plan cost 25 000 of it.
func (s *service) confirmedPlan(c caller) contracts.Plan {
	s.t.Helper()
	if testkit.Count(s.t, s.pool, `SELECT count(*) FROM fleet_vehicles`) == 0 {
		s.publish(contracts.TypeVehicleUpserted, "veh-aaa", 1, contracts.VehicleUpserted{Vehicle: testkit.Vehicle("veh-aaa", contracts.VehicleTypeMotor)})
		s.waitFor("the vehicle to be projected", func() bool { return testkit.Count(s.t, s.pool, `SELECT count(*) FROM fleet_vehicles`) == 1 })
	}
	var top contracts.TopUp
	c.rpcKeyed("wallet.createTopUp", "topup-"+time.Now().String(), map[string]any{"input": map[string]any{"amountRp": 50_000, "method": "qris"}}).into(&top)
	c.rpc("wallet.simulatePay", map[string]any{"id": top.ID}).into(&top)

	var plan contracts.Plan
	c.rpc("plans.createDraft", nil).into(&plan)
	c.rpc("plans.saveDraft", map[string]any{"id": plan.ID, "patch": map[string]any{"shipments": twoShipments(), "draftStep": 2}}).into(&plan)
	lines, r := c.stream("plans.computeOptions", map[string]any{"id": plan.ID})
	if r.status != http.StatusOK || len(lines) != 5 {
		s.t.Fatalf("computeOptions: %d, %d lines", r.status, len(lines))
	}
	var done struct {
		OptionSet contracts.OptionSet `json:"optionSet"`
	}
	if err := json.Unmarshal([]byte(lines[4]), &done); err != nil {
		s.t.Fatal(err)
	}
	c.rpc("plans.chooseOption", map[string]any{"id": plan.ID, "optionId": done.OptionSet.Options[0].ID}).into(&plan)
	c.rpcKeyed("plans.confirm", "confirm-"+plan.ID, map[string]any{"id": plan.ID, "paymentMethod": "saldo"}).into(&plan)
	return plan
}

func (c caller) plan(id string) contracts.Plan {
	c.svc.t.Helper()
	var plan contracts.Plan
	c.rpc("plans.get", map[string]any{"id": id}).into(&plan)
	return plan
}

func (c caller) balance() int64 {
	c.svc.t.Helper()
	var w wallet.Wallet
	c.rpc("wallet.get", nil).into(&w)
	return w.BalanceRp
}

func (c caller) notifications() []contracts.AppNotification {
	c.svc.t.Helper()
	var list []contracts.AppNotification
	c.rpc("notifications.list", nil).into(&list)
	return list
}

func titleKeys(list []contracts.AppNotification) []string {
	keys := []string{}
	for _, n := range list {
		keys = append(keys, n.Title.Key)
	}
	slices.Sort(keys)
	return keys
}

// awaitAll waits until events of every one of these types have been seen, in whatever order they come.
func awaitAll(t *testing.T, events <-chan platform.Event, types ...string) {
	t.Helper()
	missing := map[string]bool{}
	for _, typ := range types {
		missing[typ] = true
	}
	timeout := time.After(20 * time.Second)
	for len(missing) > 0 {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("the event stream ended while waiting for %v", missing)
			}
			delete(missing, ev.Type)
		case <-timeout:
			t.Fatalf("timed out waiting for %v", missing)
		}
	}
}

// typesOn lists the event types on a topic that the console published (the relay is asynchronous, so it waits
// for want of them).
func (s *service) typesOn(topic string, want int) []string {
	s.t.Helper()
	var types []string
	for _, rec := range s.topic(topic, want) {
		types = append(types, typeOf(rec))
	}
	slices.Sort(types)
	return types
}

func TestCancellingAPaidPlanThroughTheWholeStack(t *testing.T) {
	svc := startService(t)
	dina, _ := svc.register("dina@contoh.id")
	plan := svc.confirmedPlan(dina)
	route := plan.Routes[0]
	events := dina.events()
	if got := dina.balance(); got != 25_000 {
		t.Fatalf("balance before = %d", got)
	}

	// The customer asks, in Indonesian: the plan is marked and every route is asked to vote.
	r := dina.do(http.MethodPost, "/v1/rpc/plans.cancel", map[string]any{"id": plan.ID, "reason": ""}, map[string]string{"Accept-Language": "id"})
	var pending contracts.Plan
	r.into(&pending)
	if pending.PendingAction != "cancel" || pending.Status != contracts.PlanStatusAwaitingDriver {
		t.Fatalf("after asking: %s, pendingAction %q", pending.Status, pending.PendingAction)
	}
	if got := svc.typesOn(contracts.TopicPlan, 2); !slices.Equal(got, []string{contracts.TypePlanCancelRequested, contracts.TypePlanConfirmed}) {
		t.Fatalf("plan topic = %v", got)
	}
	var rows []plans.Summary
	dina.rpc("plans.list", nil).into(&rows)
	if len(rows) != 1 || rows[0].PendingAction != "cancel" {
		t.Errorf("the list while cancelling = %+v, want the row marked cancel", rows)
	}
	if r := dina.rpc("plans.cancel", map[string]any{"id": plan.ID}); r.status != http.StatusConflict || r.failure().Msg.Key != "srv.api.cancelPending" {
		t.Errorf("cancelling twice: %d %s", r.status, r.body)
	}
	var requestID string
	if err := svc.pool.QueryRow(t.Context(), `SELECT request_id FROM plan_cancel_requests WHERE plan_id = $1`, plan.ID).Scan(&requestID); err != nil {
		t.Fatal(err)
	}

	// driver-api votes through Kafka; the console commits.
	drain(events)
	svc.publish(contracts.TypePlanCancelAccepted, plan.ID, 0, contracts.PlanCancelAccepted{PlanID: plan.ID, RequestID: requestID, RouteID: route.ID})
	awaitAll(t, events, "plans.changed", "wallet.changed", "notifications.changed")
	got := dina.plan(plan.ID)
	if got.Status != contracts.PlanStatusCancelled || got.PendingAction != "" || got.CancelReason != "Dibatalkan pengirim" {
		t.Fatalf("plan = %s %q reason %q", got.Status, got.PendingAction, got.CancelReason)
	}
	dina.rpc("plans.list", nil).into(&rows)
	if len(rows) != 1 || rows[0].PendingAction != "" || rows[0].Status != contracts.PlanStatusCancelled {
		t.Errorf("the list after the commit = %+v, want a cancelled row without the mark", rows)
	}
	if got := dina.balance(); got != 50_000 {
		t.Errorf("balance after the refund = %d, want 50000", got)
	}
	var ledger []wallet.Entry
	dina.rpc("wallet.transactions", nil).into(&ledger)
	if len(ledger) != 3 || ledger[0].Kind != "refund" || ledger[0].AmountRp != 25_000 || ledger[0].PlanID != plan.ID || ledger[0].BalanceAfterRp != 50_000 {
		t.Errorf("ledger = %+v", ledger)
	}
	if got := svc.typesOn(contracts.TopicPlan, 4); !slices.Equal(got, []string{contracts.TypePlanCancelAccepted, contracts.TypePlanCancelRequested, contracts.TypePlanCancelled, contracts.TypePlanConfirmed}) {
		t.Errorf("plan topic = %v", got)
	}
	if got := svc.typesOn(contracts.TopicWallet, 5); !slices.Equal(got, []string{contracts.TypeTopUpCreated, contracts.TypeTopUpPaid, contracts.TypeWalletCharged, contracts.TypeWalletRefunded}) {
		t.Errorf("wallet topic = %v", got)
	}

	// The bell menu has the story; reading it is announced for the customer's other tabs.
	want := []string{"srv.api.notif.cancelled.title", "srv.api.notif.confirmed.title", "srv.api.notif.refund.title", "srv.ops.wallet.topUpDoneTitle"}
	list := dina.notifications()
	if got := titleKeys(list); !slices.Equal(got, want) {
		t.Fatalf("notifications = %v, want %v", got, want)
	}
	drain(events)
	dina.rpc("notifications.markRead", map[string]any{"ids": []string{list[0].ID}}).into(&struct{}{})
	await(t, events, "the read mark to be announced", func(ev platform.Event) bool { return ev.Type == "notifications.changed" })
	unread := 0
	for _, n := range dina.notifications() {
		if !n.Read {
			unread++
		} else if n.ID != list[0].ID {
			t.Errorf("notification %s was marked read, only %s was asked for", n.ID, list[0].ID)
		}
	}
	if unread != 3 {
		t.Errorf("%d unread, want 3", unread)
	}
	dina.rpc("notifications.markRead", nil).into(&struct{}{})
	for _, n := range dina.notifications() {
		if !n.Read {
			t.Errorf("%s is still unread after marking all", n.Title.Key)
		}
	}

	// Another customer sees none of it.
	raka, _ := svc.register("raka@contoh.id")
	if n := len(raka.notifications()); n != 0 {
		t.Errorf("Raka has %d notifications", n)
	}
}

func TestACancelNobodyAnswersIsAbortedAndALateVoteIsIgnored(t *testing.T) {
	svc := startService(t, func(c *Config) { c.CancelTimeout = 600 * time.Millisecond })
	dina, _ := svc.register("dina@contoh.id")
	plan := svc.confirmedPlan(dina)
	events := dina.events()

	var pending contracts.Plan
	dina.rpc("plans.cancel", map[string]any{"id": plan.ID, "reason": "plans changed"}).into(&pending)
	var requestID string
	if err := svc.pool.QueryRow(t.Context(), `SELECT request_id FROM plan_cancel_requests WHERE plan_id = $1`, plan.ID).Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	await(t, events, "the sweeper to give up", func(ev platform.Event) bool { return ev.Type == "notifications.changed" })

	got := dina.plan(plan.ID)
	if got.Status != contracts.PlanStatusAwaitingDriver || got.PendingAction != "" || got.CancelReason != "" {
		t.Fatalf("after the timeout: %s %q reason %q: the plan goes on as before", got.Status, got.PendingAction, got.CancelReason)
	}
	if got := svc.typesOn(contracts.TopicPlan, 3); !slices.Equal(got, []string{contracts.TypePlanCancelAborted, contracts.TypePlanCancelRequested, contracts.TypePlanConfirmed}) {
		t.Errorf("plan topic = %v", got)
	}
	if !slices.Contains(titleKeys(dina.notifications()), "srv.api.notif.cancelAborted.title") {
		t.Errorf("the customer was not told: %v", titleKeys(dina.notifications()))
	}

	// The vote that arrives after the abort changes nothing, and does not make the console retry for ever.
	svc.publish(contracts.TypePlanCancelAccepted, plan.ID, 0, contracts.PlanCancelAccepted{PlanID: plan.ID, RequestID: requestID, RouteID: plan.Routes[0].ID})
	svc.waitFor("the late vote to be consumed", func() bool {
		return testkit.Count(t, svc.pool, `SELECT count(*) FROM inbox WHERE consumer = 'console-test.cancel'`) >= 1
	})
	if got := dina.plan(plan.ID); got.Status != contracts.PlanStatusAwaitingDriver || got.PendingAction != "" {
		t.Errorf("a late vote changed the plan: %s %q", got.Status, got.PendingAction)
	}
	if got := dina.balance(); got != 25_000 {
		t.Errorf("balance = %d: nothing was refunded", got)
	}
	if dead := svc.topic(contracts.DLQTopic(contracts.TopicPlan), 0); len(dead) != 0 {
		t.Errorf("%d dead letters", len(dead))
	}
}

func TestAnAdminsRequestArrivesOnTheTopicAndIsAnsweredThere(t *testing.T) {
	svc := startService(t)
	dina, _ := svc.register("dina@contoh.id")
	plan := svc.confirmedPlan(dina)

	svc.publish(contracts.TypePlanCancelAsked, plan.ID, 0, contracts.PlanCancelAsked{PlanID: plan.ID, Reason: "fraud check", AskedBy: "Admin Ayu", Locale: "en"})
	svc.waitFor("the plan to be marked", func() bool { return dina.plan(plan.ID).PendingAction == "cancel" })

	// A request for a plan this console does not have is refused to the asker.
	svc.publish(contracts.TypePlanCancelAsked, "pln-ghost", 0, contracts.PlanCancelAsked{PlanID: "pln-ghost", AskedBy: "Admin Ayu"})
	var refusal *contracts.Envelope
	svc.waitFor("the refusal", func() bool {
		for _, rec := range svc.topic(contracts.TopicPlan, 3) {
			if typeOf(rec) == contracts.TypePlanCancelRejected {
				env, err := contracts.Decode(rec.Value)
				if err != nil {
					t.Fatal(err)
				}
				refusal = &env
			}
		}
		return refusal != nil
	})
	ev, err := refusal.Payload[contracts.PlanCancelRejected]()
	if err != nil || ev.PlanID != "pln-ghost" || ev.RouteID != "" || ev.Reason.Key != "srv.ops.err.planNotFound" {
		t.Errorf("refusal = %+v, %v", ev, err)
	}

	// The drivers agree: the plan is cancelled for the admin's reason.
	var requestID string
	if err := svc.pool.QueryRow(t.Context(), `SELECT request_id FROM plan_cancel_requests WHERE plan_id = $1`, plan.ID).Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	svc.publish(contracts.TypePlanCancelAccepted, plan.ID, 0, contracts.PlanCancelAccepted{PlanID: plan.ID, RequestID: requestID, RouteID: plan.Routes[0].ID})
	svc.waitFor("the plan to be cancelled", func() bool { return dina.plan(plan.ID).Status == contracts.PlanStatusCancelled })
	if got := dina.plan(plan.ID); got.CancelReason != "fraud check" || got.PendingAction != "" {
		t.Errorf("plan = reason %q pending %q", got.CancelReason, got.PendingAction)
	}
	if got := dina.balance(); got != 50_000 {
		t.Errorf("balance = %d, want the refund", got)
	}
	if dead := svc.topic(contracts.DLQTopic(contracts.TopicPlan), 0); len(dead) != 0 {
		t.Errorf("%d dead letters: the console must tolerate reading its own refusal", len(dead))
	}
}

func TestReschedulingOverHTTP(t *testing.T) {
	svc := startService(t)
	dina, _ := svc.register("dina@contoh.id")
	plan := svc.confirmedPlan(dina)

	soon := map[string]any{"mode": "terjadwal", "window": map[string]any{"from": time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339), "to": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}}
	r := dina.rpc("plans.reschedule", map[string]any{"id": plan.ID, "schedule": soon})
	if f := r.failure(); r.status != http.StatusUnprocessableEntity || f.Field != "schedule" || f.Msg.Key != "srv.api.scheduleTooSoon" {
		t.Errorf("five minutes ahead: %d %+v", r.status, f)
	}

	start := time.Now().Add(3 * time.Hour).UTC().Truncate(time.Second)
	later := map[string]any{"mode": "terjadwal", "window": map[string]any{"from": start.Format(time.RFC3339), "to": start.Add(time.Hour).Format(time.RFC3339)}}
	var got contracts.Plan
	dina.rpc("plans.reschedule", map[string]any{"id": plan.ID, "schedule": later}).into(&got)
	if got.Schedule.Mode != contracts.ScheduleModeScheduled || !got.Routes[0].StartAt.Equal(start) {
		t.Errorf("rescheduled plan = %+v, route starts %v", got.Schedule, got.Routes[0].StartAt)
	}
	if types := svc.typesOn(contracts.TopicPlan, 2); !slices.Equal(types, []string{contracts.TypePlanConfirmed, contracts.TypePlanRescheduled}) {
		t.Errorf("plan topic = %v", types)
	}
}

func TestAccountOverHTTP(t *testing.T) {
	svc := startService(t)
	dina, dinaID := svc.register("dina@contoh.id")
	svc.register("raka@contoh.id")

	var me account.User
	dina.rpc("account.profile", nil).into(&me)
	if me.ID != dinaID || me.Name != "Dina Pramesti" || me.Email != "dina@contoh.id" || me.Phone != "081234567801" || me.Blocked || me.SavedAddresses == nil || len(me.SavedAddresses) != 0 {
		t.Fatalf("profile = %+v", me)
	}

	dina.rpc("account.update", map[string]any{"patch": map[string]any{"name": "Dina P.", "phone": "+62 811 2222 333"}}).into(&me)
	if me.Name != "Dina P." || me.Phone != "08112222333" || me.Email != "dina@contoh.id" {
		t.Errorf("updated = %+v", me)
	}
	var session *contracts.Session
	dina.rpc("auth.session", nil).into(&session)
	if session == nil || session.Name != "Dina P." {
		t.Errorf("the session still says %+v", session)
	}
	if r := dina.rpc("account.update", map[string]any{"patch": map[string]any{"email": "Raka@contoh.id"}}); r.status != http.StatusConflict || r.failure().Field != "email" || r.failure().Msg.Key != "srv.api.emailUsed" {
		t.Errorf("taking Raka's email: %d %s", r.status, r.body)
	}
	if r := dina.rpc("account.update", map[string]any{"patch": map[string]any{"name": " "}}); r.status != http.StatusUnprocessableEntity || r.failure().Field != "name" {
		t.Errorf("a blank name: %d %s", r.status, r.body)
	}
	if types := svc.typesOn(contracts.TopicCustomer, 3); !slices.Equal(types, []string{contracts.TypeCustomerRegistered, contracts.TypeCustomerRegistered, contracts.TypeCustomerUpdated}) {
		t.Errorf("customer topic = %v: one update, none for the refused ones", types)
	}

	place := map[string]any{"name": "Kemang", "address": "Jl. Kemang 1", "position": map[string]any{"lat": -6.26, "lng": 106.81}}
	var home account.SavedAddress
	dina.rpc("account.addresses.add", map[string]any{"input": map[string]any{"label": "Home", "place": place, "contactName": "Dina", "contactPhone": "0811"}}).into(&home)
	if home.ID == "" || home.Label != "Home" || home.Place.Name != "Kemang" || home.ContactName != "Dina" {
		t.Fatalf("added = %+v", home)
	}
	if r := dina.rpc("account.addresses.add", map[string]any{"input": map[string]any{"label": "", "place": place}}); r.status != http.StatusUnprocessableEntity || r.failure().Field != "label" || r.failure().Msg.Key != "srv.api.addressLabelRequired" {
		t.Errorf("an address without a label: %d %s", r.status, r.body)
	}
	var updated account.SavedAddress
	dina.rpc("account.addresses.update", map[string]any{"id": home.ID, "patch": map[string]any{"label": "Mum's"}}).into(&updated)
	if updated.Label != "Mum's" || updated.ContactName != "Dina" {
		t.Errorf("updated = %+v", updated)
	}
	if r := dina.rpc("account.addresses.update", map[string]any{"id": "adr-ghost", "patch": map[string]any{}}); r.status != http.StatusNotFound || r.failure().Msg.Key != "srv.api.addressNotFound" {
		t.Errorf("updating a stranger: %d %s", r.status, r.body)
	}
	dina.rpc("account.profile", nil).into(&me)
	if len(me.SavedAddresses) != 1 || me.SavedAddresses[0].Label != "Mum's" {
		t.Errorf("address book = %+v", me.SavedAddresses)
	}
	dina.rpc("account.addresses.remove", map[string]any{"id": home.ID}).into(&struct{}{})
	dina.rpc("account.profile", nil).into(&me)
	if len(me.SavedAddresses) != 0 {
		t.Errorf("address book after removing = %+v", me.SavedAddresses)
	}
}

func TestBlockingAndUnblockingACustomerThroughTheCustomerTopic(t *testing.T) {
	svc := startService(t)
	dina, dinaID := svc.register("dina@contoh.id")
	raka, _ := svc.register("raka@contoh.id")
	if r := dina.rpc("wallet.get", nil); r.status != http.StatusOK {
		t.Fatalf("before blocking: %d", r.status)
	}

	svc.publish(contracts.TypeCustomerBlocked, dinaID, 1, contracts.CustomerBlocked{CustomerID: dinaID})
	svc.waitFor("the session to end", func() bool { return dina.rpc("wallet.get", nil).status == http.StatusUnauthorized })
	if r := raka.rpc("wallet.get", nil); r.status != http.StatusOK {
		t.Errorf("another customer's session ended: %d", r.status)
	}
	login := map[string]string{"email": "dina@contoh.id", "password": "demo1234"}
	if r := svc.anonymous().rpc("auth.loginCustomer", login); r.status != http.StatusForbidden || r.failure().Code != "ditolak" || r.failure().Msg.Key != "srv.api.accountBlocked" {
		t.Errorf("signing in while blocked: %d %s", r.status, r.body)
	}

	svc.publish(contracts.TypeCustomerUnblocked, dinaID, 2, contracts.CustomerUnblocked{CustomerID: dinaID})
	// Signing in is rate limited, so the wait watches the stored decision instead of retrying the login every
	// few milliseconds (a slow machine used up the allowance and the sign-in below was refused with 429).
	svc.waitFor("the customer to be unblocked", func() bool {
		return testkit.Count(t, svc.pool, `SELECT count(*) FROM customers WHERE id = $1 AND blocked`, dinaID) == 0
	})
	var signed struct {
		Token string `json:"token"`
	}
	svc.anonymous().rpc("auth.loginCustomer", login).into(&signed)
	again := caller{svc: svc, token: signed.Token}
	if r := again.rpc("plans.createDraft", nil); r.status != http.StatusOK {
		t.Errorf("creating a plan after unblocking: %d %s", r.status, r.body)
	}
}

func TestSettingsComeFromTheTopicAndAIIsOfferedOnlyWithAGateway(t *testing.T) {
	for _, tc := range []struct {
		name    string
		aiURL   string
		wantAI  contracts.AIFeatures
		wantAI2 contracts.AIFeatures
	}{
		{"without a gateway", "", contracts.AIFeatures{}, contracts.AIFeatures{}},
		{"with a gateway", "http://ai.example.test", settings.Default.AI, contracts.AIFeatures{Vision: false, Voice: true, Intent: false, Copilot: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := startService(t, func(c *Config) { c.AIURL = tc.aiURL })
			dina, _ := svc.register("dina@contoh.id")
			events := dina.events()

			var got settings.App
			dina.rpc("settings.get", nil).into(&got)
			if got.AI != tc.wantAI || !got.Dispatch.Auto {
				t.Fatalf("before anything arrived: %+v, want AI %+v and automatic dispatch", got, tc.wantAI)
			}
			if r := svc.anonymous().rpc("settings.get", nil); r.status != http.StatusUnauthorized {
				t.Errorf("settings without signing in: %d", r.status)
			}

			svc.publish(contracts.TypeSettingsUpdated, contracts.SettingsKeyAI, 1, contracts.SettingsUpdated{
				Key: contracts.SettingsKeyAI, AI: &contracts.AIFeatures{Vision: false, Voice: true, Intent: false, Copilot: true},
			})
			svc.publish(contracts.TypeSettingsUpdated, contracts.SettingsKeyDispatch, 1, contracts.SettingsUpdated{
				Key: contracts.SettingsKeyDispatch, Dispatch: &contracts.DispatchSettings{Auto: false},
			})
			await(t, events, "settings.changed", func(ev platform.Event) bool { return ev.Type == "settings.changed" })
			svc.waitFor("both settings to be stored", func() bool { return testkit.Count(t, svc.pool, `SELECT count(*) FROM settings`) == 2 })
			dina.rpc("settings.get", nil).into(&got)
			if got.AI != tc.wantAI2 || got.Dispatch.Auto {
				t.Errorf("after the admin's choices: %+v, want AI %+v and manual dispatch", got, tc.wantAI2)
			}
		})
	}
}

func TestAnyRoleMayReadTheSettings(t *testing.T) {
	svc := startService(t)
	const driverToken = "driver-session-token"
	hash := platform.HashToken(driverToken)
	if _, err := svc.pool.Exec(t.Context(),
		`INSERT INTO sessions (token_hash, role, user_id, name, expires_at) VALUES ($1, 'driver', 'drv-1', 'Budi', now() + interval '1 hour')`, hash[:]); err != nil {
		t.Fatal(err)
	}
	var got settings.App
	caller{svc: svc, token: driverToken}.rpc("settings.get", nil).into(&got)
	if !got.Dispatch.Auto {
		t.Errorf("settings = %+v", got)
	}
}
