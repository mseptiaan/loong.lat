package plans

import (
	"encoding/json/v2"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/notices"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/testkit"
)

// confirmed is a plan the customer paid for from the wallet and handed over: 25 000 went out of the balance.
func (w *world) confirmed() contracts.Plan {
	w.t.Helper()
	testkit.Fund(w.t, w.pool, w.customer, testkit.OptionCostRp)
	p := w.ready("s1", "s2")
	p, err := w.svc.Confirm(w.t.Context(), w.customer, p.ID, contracts.PaymentWallet)
	if err != nil {
		w.t.Fatal(err)
	}
	return p
}

// addRoute gives a confirmed plan a second route, so that a cancel needs two votes.
func (w *world) addRoute(planID string) {
	w.t.Helper()
	err := platform.WithTx(w.t.Context(), w.pool, func(tx pgx.Tx) error {
		rec, err := Lock(w.t.Context(), tx, planID)
		if err != nil {
			return err
		}
		second := rec.Plan.Routes[0]
		second.ID = planID + ":r2"
		for i := range second.Stops {
			second.Stops[i].ID = second.ID + ":s" + string(rune('1'+i))
		}
		rec.Plan.Routes = append(rec.Plan.Routes, second)
		return Save(w.t.Context(), tx, rec)
	})
	if err != nil {
		w.t.Fatal(err)
	}
}

// setStatus forces what the projections would have made of the plan.
func (w *world) setStatus(planID string, status contracts.PlanStatus) {
	w.t.Helper()
	err := platform.WithTx(w.t.Context(), w.pool, func(tx pgx.Tx) error {
		rec, err := Lock(w.t.Context(), tx, planID)
		if err != nil {
			return err
		}
		rec.Plan.Status = status
		return Save(w.t.Context(), tx, rec)
	})
	if err != nil {
		w.t.Fatal(err)
	}
}

// request is the latest cancel attempt of a plan.
func (w *world) request(planID string) cancelRequest {
	w.t.Helper()
	req, err := scanRequest(w.pool.QueryRow(w.t.Context(),
		`SELECT `+requestColumns+` FROM plan_cancel_requests WHERE plan_id = $1 ORDER BY requested_at DESC, request_id DESC LIMIT 1`, planID))
	if err != nil {
		w.t.Fatalf("no cancel request for %s: %v", planID, err)
	}
	return req
}

func (w *world) requestCount(planID string) int {
	w.t.Helper()
	return testkit.Count(w.t, w.pool, `SELECT count(*) FROM plan_cancel_requests WHERE plan_id = $1`, planID)
}

// deliver hands an event to a handler in a transaction of its own, as the consumer does (without the inbox).
func (w *world) deliver(h eventbus.Handler, eventType string, data any) error {
	w.t.Helper()
	env, err := contracts.NewEnvelope("test", eventType, "k", 0, data)
	if err != nil {
		w.t.Fatal(err)
	}
	return platform.WithTx(w.t.Context(), w.pool, func(tx pgx.Tx) error { return h(w.t.Context(), tx, env) })
}

func (w *world) accept(planID, requestID, routeID string) {
	w.t.Helper()
	err := w.deliver(w.svc.onAccepted, contracts.TypePlanCancelAccepted, contracts.PlanCancelAccepted{PlanID: planID, RequestID: requestID, RouteID: routeID})
	if err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) reject(planID, requestID, routeID string, reason contracts.Msg) {
	w.t.Helper()
	err := w.deliver(w.svc.onRejected, contracts.TypePlanCancelRejected, contracts.PlanCancelRejected{PlanID: planID, RequestID: requestID, RouteID: routeID, Reason: reason})
	if err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) asked(planID, reason, locale string) error {
	w.t.Helper()
	return w.deliver(w.svc.onAsked, contracts.TypePlanCancelAsked, contracts.PlanCancelAsked{PlanID: planID, Reason: reason, AskedBy: "Admin Ayu", Locale: locale})
}

func (w *world) published(eventType string) int {
	w.t.Helper()
	return len(testkit.Outbox(w.t, w.pool, eventType))
}

func (w *world) refunds() int {
	w.t.Helper()
	return testkit.Count(w.t, w.pool, `SELECT count(*) FROM wallet_entries WHERE kind = 'refund' AND customer_id = $1`, w.customer)
}

func (w *world) notices() []contracts.AppNotification {
	w.t.Helper()
	list, err := notices.List(w.t.Context(), w.pool, notices.Customer(w.customer))
	if err != nil {
		w.t.Fatal(err)
	}
	return list
}

// noticeKeys are the title keys of the customer's bell menu, in alphabetical order (notices written in one
// transaction share their time).
func (w *world) noticeKeys() []string {
	var keys []string
	for _, n := range w.notices() {
		keys = append(keys, n.Title.Key)
	}
	slices.Sort(keys)
	return keys
}

func TestCancellingAConfirmedPlanAsksEveryRouteAndLeavesTheRestAlone(t *testing.T) {
	w := newWorld(t)
	p := w.confirmed()
	w.addRoute(p.ID)
	balance, before := w.balance(), len(testkit.OutboxTypes(t, w.pool))

	got, err := w.svc.Cancel(t.Context(), w.customer, p.ID, "  plans changed ")
	if err != nil {
		t.Fatal(err)
	}
	if got.PendingAction != "cancel" || got.Status != contracts.PlanStatusAwaitingDriver || got.CancelReason != "" {
		t.Errorf("plan = %s, pendingAction %q, cancelReason %q: still a live plan, marked as being cancelled", got.Status, got.PendingAction, got.CancelReason)
	}
	rec := w.stored(p.ID)
	if rec.Version != 2 || rec.Plan.PendingAction != "cancel" {
		t.Errorf("stored version %d, pendingAction %q; want 2 (confirmed was 1) and cancel", rec.Version, rec.Plan.PendingAction)
	}
	req := w.request(p.ID)
	if req.Status != cancelPending || req.By != contracts.CancelByCustomer || req.Reason != "plans changed" || !req.Deadline.Equal(w.clock.at.Add(DefaultCancelTimeout)) {
		t.Errorf("request = %+v", req)
	}
	if want := []string{p.ID + ":r1", p.ID + ":r2"}; !slices.Equal(req.RouteIDs, want) {
		t.Errorf("request waits for %v, want every route %v", req.RouteIDs, want)
	}

	if after := testkit.OutboxTypes(t, w.pool); !slices.Equal(after[before:], []string{contracts.TypePlanCancelRequested}) {
		t.Fatalf("published %v: only the request, the money and the plan stay as they are", after[before:])
	}
	env := testkit.Outbox(t, w.pool, contracts.TypePlanCancelRequested)[0]
	ev, _ := env.Payload[contracts.PlanCancelRequested]()
	if env.Key != p.ID || env.AggregateVersion != 2 || ev.RequestID != req.ID || ev.RequestedBy != "customer" || ev.Reason != "plans changed" || !slices.Equal(ev.RouteIDs, req.RouteIDs) {
		t.Errorf("plan.cancel.requested = key %s v%d %+v", env.Key, env.AggregateVersion, ev)
	}
	if w.balance() != balance || w.refunds() != 0 {
		t.Errorf("money moved before the drivers agreed: balance %d, refunds %d", w.balance(), w.refunds())
	}
}

func TestCancellingIsRefusedWhenThePlanCannotBeCancelled(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	type refusal struct {
		name   string
		setup  func(id string)
		status platform.ErrorCode
		key    string
	}
	for _, tc := range []refusal{
		{"running", func(id string) { w.setStatus(id, contracts.PlanStatusRunning) }, platform.CodeConflict, "srv.api.cancelRunning"},
		{"done", func(id string) { w.setStatus(id, contracts.PlanStatusDone) }, platform.CodeConflict, "srv.api.planInactive"},
		{"already cancelled", func(id string) { w.setStatus(id, contracts.PlanStatusCancelled) }, platform.CodeConflict, "srv.api.planInactive"},
	} {
		p := w.confirmed()
		tc.setup(p.ID)
		before := len(testkit.OutboxTypes(t, w.pool))
		_, err := w.svc.Cancel(ctx, w.customer, p.ID, "x")
		wantError(t, err, tc.status, tc.key)
		if w.requestCount(p.ID) != 0 || len(testkit.OutboxTypes(t, w.pool)) != before || w.stored(p.ID).Plan.PendingAction != "" {
			t.Errorf("%s: a refused cancel left a trace", tc.name)
		}
	}

	// A second cancel while the first waits for the votes.
	p := w.confirmed()
	if _, err := w.svc.Cancel(ctx, w.customer, p.ID, ""); err != nil {
		t.Fatal(err)
	}
	_, err := w.svc.Cancel(ctx, w.customer, p.ID, "again")
	wantError(t, err, platform.CodeConflict, "srv.api.cancelPending")
	if n := w.requestCount(p.ID); n != 1 || w.published(contracts.TypePlanCancelRequested) != 1 {
		t.Errorf("%d requests, %d plan.cancel.requested: a double cancel must not ask twice", n, w.published(contracts.TypePlanCancelRequested))
	}

	// Somebody else's plan, and one that does not exist.
	stranger := testkit.Customer(t, w.pool, "Raka")
	_, err = w.svc.Cancel(ctx, stranger, p.ID, "")
	wantError(t, err, platform.CodeDenied, "srv.api.planNoAccess")
	_, err = w.svc.Cancel(ctx, w.customer, "pln-nope", "")
	wantError(t, err, platform.CodeNotFound, "srv.ops.err.planNotFound")
}

func TestAPlanNobodyConfirmedIsCancelledAtOnceWithoutAskingAnyone(t *testing.T) {
	w := newWorld(t)
	balance := w.balance()

	draft := w.draft("s1")
	got, err := w.svc.Cancel(platform.WithLocale(t.Context(), platform.LocaleID), w.customer, draft.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != contracts.PlanStatusCancelled || got.PendingAction != "" || got.CancelReason != "Dibatalkan pengirim" {
		t.Errorf("draft = %s %q reason %q: cancelled, with the default reason in the request's language", got.Status, got.PendingAction, got.CancelReason)
	}
	if last := got.Events[len(got.Events)-1].Text; last.Key != "srv.api.event.cancelled" {
		t.Errorf("timeline ends with %+v", last)
	}

	ready := w.ready("s1", "s2")
	got, err = w.svc.Cancel(t.Context(), w.customer, ready.ID, "no longer needed")
	if err != nil {
		t.Fatal(err)
	}
	if last := got.Events[len(got.Events)-1].Text; got.CancelReason != "no longer needed" || last.Key != "srv.api.event.cancelledReason" || last.Params["reason"] != "no longer needed" {
		t.Errorf("reason %q, timeline ends with %+v", got.CancelReason, last)
	}
	english, _ := w.svc.Cancel(t.Context(), w.customer, w.draft("s1").ID, "")
	if english.CancelReason != "Cancelled by sender" {
		t.Errorf("default reason in English = %q", english.CancelReason)
	}

	if n := testkit.Count(t, w.pool, `SELECT count(*) FROM plan_cancel_requests`); n != 0 {
		t.Errorf("%d requests: nobody had to be asked", n)
	}
	if types := testkit.OutboxTypes(t, w.pool); len(types) != 0 {
		t.Errorf("published %v: nobody else knows these plans", types)
	}
	if w.balance() != balance || w.refunds() != 0 {
		t.Error("an unpaid plan was refunded")
	}
	// A cancelled draft is final.
	_, err = w.svc.Cancel(t.Context(), w.customer, draft.ID, "")
	wantError(t, err, platform.CodeConflict, "srv.api.planInactive")
}

func TestWhenEveryRouteAcceptsThePlanIsCancelledAndRefundedExactlyOnce(t *testing.T) {
	w := newWorld(t)
	p := w.confirmed()
	w.addRoute(p.ID)
	if _, err := w.svc.Cancel(platform.WithLocale(t.Context(), platform.LocaleID), w.customer, p.ID, ""); err != nil {
		t.Fatal(err)
	}
	req := w.request(p.ID)
	balance := w.balance()

	w.accept(p.ID, req.ID, p.ID+":r1")
	if rec := w.stored(p.ID); rec.Plan.Status == contracts.PlanStatusCancelled || rec.Plan.PendingAction != "cancel" || w.refunds() != 0 {
		t.Fatalf("one vote of two decided the cancel: %s %q", rec.Plan.Status, rec.Plan.PendingAction)
	}
	w.accept(p.ID, req.ID, p.ID+":r1") // the same vote again counts once
	if w.stored(p.ID).Plan.Status == contracts.PlanStatusCancelled {
		t.Fatal("a repeated vote counted twice")
	}
	w.accept(p.ID, req.ID, p.ID+":r2")

	rec := w.stored(p.ID)
	if rec.Plan.Status != contracts.PlanStatusCancelled || rec.Plan.PendingAction != "" || rec.Plan.CancelReason != "Dibatalkan pengirim" {
		t.Fatalf("plan = %s, pendingAction %q, reason %q", rec.Plan.Status, rec.Plan.PendingAction, rec.Plan.CancelReason)
	}
	if last := rec.Plan.Events[len(rec.Plan.Events)-1].Text; last.Key != "srv.api.event.cancelled" {
		t.Errorf("timeline ends with %+v", last)
	}
	if rec.Version != 3 || w.request(p.ID).Status != cancelCommitted {
		t.Errorf("version %d (confirmed 1, requested 2, cancelled 3), request %s", rec.Version, w.request(p.ID).Status)
	}
	if w.balance() != balance+testkit.OptionCostRp || w.refunds() != 1 {
		t.Errorf("balance %d, %d refunds; want %d and 1", w.balance(), w.refunds(), balance+testkit.OptionCostRp)
	}
	refund := testkit.Outbox(t, w.pool, contracts.TypeWalletRefunded)
	if len(refund) != 1 {
		t.Fatalf("%d wallet.refunded", len(refund))
	}
	if ev, _ := refund[0].Payload[contracts.WalletRefunded](); ev.PlanID != p.ID || ev.AmountRp != testkit.OptionCostRp || ev.BalanceAfterRp != w.balance() || ev.CustomerID != w.customer || ev.TxID == "" {
		t.Errorf("wallet.refunded = %+v", ev)
	}
	cancelled := testkit.Outbox(t, w.pool, contracts.TypePlanCancelled)
	if len(cancelled) != 1 || cancelled[0].Key != p.ID || cancelled[0].AggregateVersion != 3 {
		t.Fatalf("plan.cancelled = %+v", cancelled)
	}
	if ev, _ := cancelled[0].Payload[contracts.PlanCancelled](); ev.RequestID != req.ID || ev.RequestedBy != "customer" || ev.RefundRp != testkit.OptionCostRp || ev.Reason != "" {
		t.Errorf("plan.cancelled = %+v", ev)
	}
	ledger, err := walletEntries(t, w, "refund")
	if err != nil || len(ledger) != 1 || ledger[0].planID != p.ID || ledger[0].delta != testkit.OptionCostRp {
		t.Errorf("refund ledger entries = %+v, %v", ledger, err)
	}
	if want := []string{"srv.api.notif.cancelled.title", "srv.api.notif.confirmed.title", "srv.api.notif.refund.title"}; !slices.Equal(w.noticeKeys(), want) {
		t.Errorf("notifications = %v, want %v", w.noticeKeys(), want)
	}

	// Redelivery of every vote, and of a late rejection: nothing changes.
	w.accept(p.ID, req.ID, p.ID+":r1")
	w.accept(p.ID, req.ID, p.ID+":r2")
	w.reject(p.ID, req.ID, p.ID+":r1", contracts.Msg{Key: "srv.api.cancelRunning"})
	if w.refunds() != 1 || w.balance() != balance+testkit.OptionCostRp || w.published(contracts.TypePlanCancelled) != 1 ||
		w.published(contracts.TypeWalletRefunded) != 1 || w.published(contracts.TypePlanCancelAborted) != 0 || w.stored(p.ID).Version != 3 {
		t.Errorf("redelivery changed things: %d refunds, balance %d, %d cancelled, %d aborted", w.refunds(), w.balance(), w.published(contracts.TypePlanCancelled), w.published(contracts.TypePlanCancelAborted))
	}
}

type ledgerEntry struct {
	planID string
	delta  int64
}

func walletEntries(t *testing.T, w *world, kind string) ([]ledgerEntry, error) {
	t.Helper()
	rows, err := w.pool.Query(t.Context(), `SELECT reference, delta_rp FROM wallet_entries WHERE kind = $1 AND customer_id = $2`, kind, w.customer)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ledgerEntry
	for rows.Next() {
		var e ledgerEntry
		if err := rows.Scan(&e.planID, &e.delta); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func TestAPlanNotPaidFromTheWalletIsCancelledWithoutARefund(t *testing.T) {
	w := newWorld(t)
	p := w.confirmed()
	// Pretend the plan was paid some other way: the ledger still has its charge, which must not be refunded.
	err := platform.WithTx(t.Context(), w.pool, func(tx pgx.Tx) error {
		rec, err := Lock(t.Context(), tx, p.ID)
		if err != nil {
			return err
		}
		rec.Plan.PaymentMethod = contracts.PaymentCash
		return Save(t.Context(), tx, rec)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.Cancel(t.Context(), w.customer, p.ID, ""); err != nil {
		t.Fatal(err)
	}
	w.accept(p.ID, w.request(p.ID).ID, p.ID+":r1")

	if w.stored(p.ID).Plan.Status != contracts.PlanStatusCancelled || w.refunds() != 0 || w.published(contracts.TypeWalletRefunded) != 0 {
		t.Errorf("status %s, %d refunds", w.stored(p.ID).Plan.Status, w.refunds())
	}
	if ev, _ := testkit.Outbox(t, w.pool, contracts.TypePlanCancelled)[0].Payload[contracts.PlanCancelled](); ev.RefundRp != 0 {
		t.Errorf("plan.cancelled says %d was refunded", ev.RefundRp)
	}
}

func TestARejectedVoteAbortsTheCancelAndLeavesThePlanUntouched(t *testing.T) {
	w := newWorld(t)
	p := w.confirmed()
	w.addRoute(p.ID)
	if _, err := w.svc.Cancel(t.Context(), w.customer, p.ID, "x"); err != nil {
		t.Fatal(err)
	}
	req := w.request(p.ID)
	balance := w.balance()
	before := w.stored(p.ID)

	w.accept(p.ID, req.ID, p.ID+":r1")
	started := contracts.Msg{Key: "srv.api.cancelRunning"}
	w.reject(p.ID, req.ID, p.ID+":r2", started)

	rec := w.stored(p.ID)
	if rec.Plan.Status != before.Plan.Status || rec.Plan.PendingAction != "" || rec.Plan.CancelReason != "" || len(rec.Plan.Events) != len(before.Plan.Events) {
		t.Errorf("plan after the refusal = %s %q reason %q, %d events (was %d)", rec.Plan.Status, rec.Plan.PendingAction, rec.Plan.CancelReason, len(rec.Plan.Events), len(before.Plan.Events))
	}
	if rec.Version != 3 || w.request(p.ID).Status != cancelAborted {
		t.Errorf("version %d (requested 2, aborted 3), request %s", rec.Version, w.request(p.ID).Status)
	}
	if w.balance() != balance || w.refunds() != 0 || w.published(contracts.TypePlanCancelled) != 0 || w.published(contracts.TypeWalletRefunded) != 0 {
		t.Error("money or the plan moved although a route refused")
	}
	aborted := testkit.Outbox(t, w.pool, contracts.TypePlanCancelAborted)
	if len(aborted) != 1 || aborted[0].Key != p.ID || aborted[0].AggregateVersion != 3 {
		t.Fatalf("plan.cancel.aborted = %+v", aborted)
	}
	if ev, _ := aborted[0].Payload[contracts.PlanCancelAborted](); ev.RequestID != req.ID || ev.Reason.Key != started.Key {
		t.Errorf("plan.cancel.aborted = %+v", ev)
	}
	var told *contracts.AppNotification
	for _, n := range w.notices() {
		if n.Title.Key == "srv.api.notif.cancelAborted.title" {
			told = &n
		}
	}
	if told == nil || told.Body.Params["code"] != p.Code {
		t.Fatalf("the customer was not told the cancel was aborted: %+v", w.notices())
	}
	if reason, ok := told.Body.Params["reason"].(map[string]any); !ok || reason["key"] != started.Key { // a nested Msg, read back from JSON
		t.Errorf("the notification says why as %+v", told.Body.Params["reason"])
	}

	// The vote of the other route, arriving late, changes nothing.
	w.accept(p.ID, req.ID, p.ID+":r2")
	if w.stored(p.ID).Plan.Status != before.Plan.Status || w.published(contracts.TypePlanCancelled) != 0 || w.published(contracts.TypePlanCancelAborted) != 1 {
		t.Error("a vote after the abort was acted on")
	}

	// The plan can be cancelled again, and a vote of the first attempt does not count toward the second.
	if _, err := w.svc.Cancel(t.Context(), w.customer, p.ID, ""); err != nil {
		t.Fatal(err)
	}
	second := w.request(p.ID)
	if second.ID == req.ID {
		t.Fatal("the second attempt reuses the first one's id")
	}
	w.accept(p.ID, req.ID, p.ID+":r1")
	w.accept(p.ID, req.ID, p.ID+":r2")
	if w.stored(p.ID).Plan.PendingAction != "cancel" || w.published(contracts.TypePlanCancelled) != 0 {
		t.Error("the votes of an earlier attempt decided the new one")
	}
	w.reject(p.ID, "cnl-someone-elses", p.ID+":r1", started)
	if w.stored(p.ID).Plan.PendingAction != "cancel" {
		t.Error("a rejection naming an unknown attempt aborted the new one")
	}
	w.accept(p.ID, second.ID, p.ID+":r1")
	w.accept(p.ID, second.ID, p.ID+":r2")
	if w.stored(p.ID).Plan.Status != contracts.PlanStatusCancelled || w.refunds() != 1 {
		t.Errorf("the second attempt did not commit: %s, %d refunds", w.stored(p.ID).Plan.Status, w.refunds())
	}
}

func TestVotesThatCannotMatterAreIgnored(t *testing.T) {
	w := newWorld(t)
	p := w.confirmed()
	if _, err := w.svc.Cancel(t.Context(), w.customer, p.ID, ""); err != nil {
		t.Fatal(err)
	}
	req := w.request(p.ID)
	other := w.confirmed()

	reason := contracts.Msg{Key: "srv.api.cancelRunning"}
	w.reject(p.ID, "", p.ID+":r1", reason)             // no attempt named
	w.reject(p.ID, req.ID, "", reason)                 // the console's own refusal of an admin: no route
	w.reject(other.ID, req.ID, other.ID+":r1", reason) // the attempt belongs to another plan
	w.reject(p.ID, req.ID, p.ID+":r9", reason)         // a route the attempt did not ask
	w.accept(p.ID, req.ID, p.ID+":r9")
	if rec := w.stored(p.ID); rec.Plan.PendingAction != "cancel" || w.request(p.ID).Status != cancelPending || w.published(contracts.TypePlanCancelAborted) != 0 {
		t.Fatalf("an irrelevant vote changed the attempt: %q %s", rec.Plan.PendingAction, w.request(p.ID).Status)
	}
	if n := testkit.Count(t, w.pool, `SELECT count(*) FROM plan_cancel_votes`); n != 0 {
		t.Errorf("%d votes recorded, want none", n)
	}

	// A plan this console has never had is not something to retry for ever: it goes to the dead letters.
	err := w.deliver(w.svc.onAccepted, contracts.TypePlanCancelAccepted, contracts.PlanCancelAccepted{PlanID: "pln-ghost", RequestID: "cnl-x", RouteID: "pln-ghost:r1"})
	if err == nil {
		t.Error("a vote for an unknown plan was accepted")
	}
}

func TestNoAnswerInTimeAbortsTheCancelAndALateVoteIsIgnored(t *testing.T) {
	w := newWorld(t)
	p := w.confirmed()
	w.addRoute(p.ID)
	if _, err := w.svc.Cancel(t.Context(), w.customer, p.ID, ""); err != nil {
		t.Fatal(err)
	}
	req := w.request(p.ID)
	w.accept(p.ID, req.ID, p.ID+":r1") // one route answered, the other did not
	balance := w.balance()

	w.clock.advance(DefaultCancelTimeout - time.Second)
	if n, err := w.svc.SweepCancels(t.Context()); err != nil || n != 0 {
		t.Fatalf("swept %d (%v) before the deadline", n, err)
	}
	if w.stored(p.ID).Plan.PendingAction != "cancel" {
		t.Fatal("the attempt ended before its deadline")
	}

	w.clock.advance(time.Second)
	if n, err := w.svc.SweepCancels(t.Context()); err != nil || n != 1 {
		t.Fatalf("swept %d (%v) at the deadline, want 1", n, err)
	}
	rec := w.stored(p.ID)
	if rec.Plan.Status != contracts.PlanStatusAwaitingDriver || rec.Plan.PendingAction != "" || rec.Version != 3 || w.request(p.ID).Status != cancelAborted {
		t.Errorf("after the sweep: %s %q v%d request %s", rec.Plan.Status, rec.Plan.PendingAction, rec.Version, w.request(p.ID).Status)
	}
	aborted := testkit.Outbox(t, w.pool, contracts.TypePlanCancelAborted)
	if len(aborted) != 1 {
		t.Fatalf("%d plan.cancel.aborted", len(aborted))
	}
	if ev, _ := aborted[0].Payload[contracts.PlanCancelAborted](); ev.RequestID != req.ID || ev.Reason.Key != "srv.api.cancelNoAnswer" {
		t.Errorf("plan.cancel.aborted = %+v", ev)
	}
	if !slices.Contains(w.noticeKeys(), "srv.api.notif.cancelAborted.title") {
		t.Errorf("the customer was not told: %v", w.noticeKeys())
	}

	// The straggler's vote arrives after the abort: it is ignored, and a second sweep finds nothing.
	w.accept(p.ID, req.ID, p.ID+":r2")
	if n, _ := w.svc.SweepCancels(t.Context()); n != 0 {
		t.Errorf("the second sweep aborted %d", n)
	}
	if rec := w.stored(p.ID); rec.Plan.Status != contracts.PlanStatusAwaitingDriver || rec.Version != 3 || w.published(contracts.TypePlanCancelled) != 0 || w.refunds() != 0 || w.balance() != balance {
		t.Errorf("a late vote revived the cancel: %s v%d, %d cancelled", rec.Plan.Status, rec.Version, w.published(contracts.TypePlanCancelled))
	}
}

func TestACancelTimeoutCanBeConfigured(t *testing.T) {
	w := newWorld(t)
	w.svc.cancelTimeout = 30 * time.Second
	p := w.confirmed()
	if _, err := w.svc.Cancel(t.Context(), w.customer, p.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got, want := w.request(p.ID).Deadline, w.clock.at.Add(30*time.Second); !got.Equal(want) {
		t.Errorf("deadline = %v, want %v", got, want)
	}
	WithCancelTimeout(0)(w.svc) // a zero timeout would abort at once: it keeps the current one
	if w.svc.cancelTimeout != 30*time.Second {
		t.Errorf("timeout = %v", w.svc.cancelTimeout)
	}
}

func TestAnAdminsRequestGoesThroughTheSameVotesInTheAdminsLanguage(t *testing.T) {
	w := newWorld(t)
	p := w.confirmed()
	balance := w.balance()

	if err := w.asked(p.ID, "", "id"); err != nil {
		t.Fatal(err)
	}
	req := w.request(p.ID)
	if req.By != contracts.CancelByAdmin || req.Requester != "Admin Ayu" || req.Locale != platform.LocaleID || req.Status != cancelPending {
		t.Fatalf("request = %+v", req)
	}
	requested := testkit.Outbox(t, w.pool, contracts.TypePlanCancelRequested)
	if len(requested) != 1 {
		t.Fatalf("%d plan.cancel.requested", len(requested))
	}
	if ev, _ := requested[0].Payload[contracts.PlanCancelRequested](); ev.RequestedBy != "admin" || ev.RequestID != req.ID || !slices.Equal(ev.RouteIDs, []string{p.ID + ":r1"}) {
		t.Errorf("plan.cancel.requested = %+v", ev)
	}
	if got := w.stored(p.ID); got.Plan.PendingAction != "cancel" || got.Version != 2 {
		t.Errorf("plan = %q v%d", got.Plan.PendingAction, got.Version)
	}
	if w.published(contracts.TypePlanCancelRejected) != 0 {
		t.Error("an acceptable request was answered with a refusal")
	}

	w.accept(p.ID, req.ID, p.ID+":r1")
	rec := w.stored(p.ID)
	if rec.Plan.Status != contracts.PlanStatusCancelled || rec.Plan.CancelReason != "Dibatalkan admin" {
		t.Errorf("plan = %s, reason %q: the admin's default reason, in the admin's language", rec.Plan.Status, rec.Plan.CancelReason)
	}
	if ev, _ := testkit.Outbox(t, w.pool, contracts.TypePlanCancelled)[0].Payload[contracts.PlanCancelled](); ev.RequestedBy != "admin" || ev.RefundRp != testkit.OptionCostRp || ev.Reason != "" {
		t.Errorf("plan.cancelled = %+v: the default reason stays in the plan, the event carries only what was typed", ev)
	}
	if w.balance() != balance+testkit.OptionCostRp || w.refunds() != 1 {
		t.Errorf("balance %d, %d refunds", w.balance(), w.refunds())
	}
	// The customer did not ask, so they are told of the cancel and the refund: that is news to them.
	if want := []string{"srv.api.notif.cancelled.title", "srv.api.notif.confirmed.title", "srv.api.notif.refund.title"}; !slices.Equal(w.noticeKeys(), want) {
		t.Errorf("notifications = %v", w.noticeKeys())
	}

	// With the admin's reason in their own words, in English by default.
	q := w.confirmed()
	if err := w.asked(q.ID, "  duplicate order ", ""); err != nil {
		t.Fatal(err)
	}
	w.accept(q.ID, w.request(q.ID).ID, q.ID+":r1")
	if got := w.stored(q.ID).Plan; got.CancelReason != "duplicate order" {
		t.Errorf("reason = %q", got.CancelReason)
	}
	typed := testkit.Outbox(t, w.pool, contracts.TypePlanCancelled)
	if ev, _ := typed[len(typed)-1].Payload[contracts.PlanCancelled](); ev.Reason != "duplicate order" {
		t.Errorf("plan.cancelled carries reason %q, want what the admin typed", ev.Reason)
	}
	r := w.confirmed()
	if err := w.asked(r.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	w.accept(r.ID, w.request(r.ID).ID, r.ID+":r1")
	if got := w.stored(r.ID).Plan; got.CancelReason != "Cancelled by admin" {
		t.Errorf("default reason = %q", got.CancelReason)
	}
}

func TestAnAdminsRequestForAPlanThatCannotBeCancelledIsRefusedToBackOffice(t *testing.T) {
	w := newWorld(t)
	running, done, gone := w.confirmed(), w.confirmed(), w.confirmed()
	w.setStatus(running.ID, contracts.PlanStatusRunning)
	w.setStatus(done.ID, contracts.PlanStatusDone)
	w.setStatus(gone.ID, contracts.PlanStatusCancelled)
	pending := w.confirmed()
	if _, err := w.svc.Cancel(t.Context(), w.customer, pending.ID, ""); err != nil {
		t.Fatal(err)
	}
	draft := w.draft("s1")

	for _, tc := range []struct{ plan, key string }{
		{running.ID, "srv.api.cancelRunning"},
		{done.ID, "srv.api.planInactive"},
		{gone.ID, "srv.api.planInactive"},
		{pending.ID, "srv.api.cancelPending"},
		{draft.ID, "srv.api.planInactive"},
		{"pln-ghost", "srv.ops.err.planNotFound"},
	} {
		before := w.published(contracts.TypePlanCancelRejected)
		if err := w.asked(tc.plan, "x", "en"); err != nil {
			t.Fatalf("%s: %v", tc.plan, err)
		}
		rejected := testkit.Outbox(t, w.pool, contracts.TypePlanCancelRejected)
		if len(rejected) != before+1 {
			t.Fatalf("%s: %d refusals published, want %d", tc.plan, len(rejected), before+1)
		}
		last := rejected[len(rejected)-1]
		ev, _ := last.Payload[contracts.PlanCancelRejected]()
		if last.Key != tc.plan || ev.PlanID != tc.plan || ev.RouteID != "" || ev.Reason.Key != tc.key {
			t.Errorf("%s: refusal = key %s %+v, want an empty route and %s", tc.plan, last.Key, ev, tc.key)
		}
	}
	// Only the customer's own attempt on the pending plan exists; refused requests created none.
	if n := testkit.Count(t, w.pool, `SELECT count(*) FROM plan_cancel_requests`); n != 1 {
		t.Errorf("%d requests, want just the customer's", n)
	}
	for _, id := range []string{running.ID, done.ID, gone.ID} {
		if w.stored(id).Plan.PendingAction != "" {
			t.Errorf("%s was marked as being cancelled", id)
		}
	}
	// The refusal travels on the topic this service reads too, and must not be mistaken for a vote.
	if err := w.deliver(w.svc.onRejected, contracts.TypePlanCancelRejected, contracts.PlanCancelRejected{PlanID: pending.ID, Reason: contracts.Msg{Key: "srv.api.cancelPending"}}); err != nil {
		t.Fatal(err)
	}
	if w.stored(pending.ID).Plan.PendingAction != "cancel" {
		t.Error("the console's own refusal aborted the customer's attempt")
	}
}

func TestThePlanEventsOfCancellingFollowOneAnotherByPlanVersion(t *testing.T) {
	// requested, aborted and cancelled are the plan family: each one version higher than the last, so a
	// projection that drops what it has already applied sees them in order.
	w := newWorld(t)
	p := w.confirmed()
	if _, err := w.svc.Cancel(t.Context(), w.customer, p.ID, ""); err != nil {
		t.Fatal(err)
	}
	w.reject(p.ID, w.request(p.ID).ID, p.ID+":r1", contracts.Msg{Key: "srv.api.cancelRunning"})
	if _, err := w.svc.Cancel(t.Context(), w.customer, p.ID, ""); err != nil {
		t.Fatal(err)
	}
	w.accept(p.ID, w.request(p.ID).ID, p.ID+":r1")

	rows, err := w.pool.Query(t.Context(), `SELECT payload FROM outbox WHERE event_type LIKE 'plan.%' ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var (
		types    []string
		versions []int64
	)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			t.Fatal(err)
		}
		env, err := contracts.Decode([]byte(payload))
		if err != nil {
			t.Fatal(err)
		}
		types, versions = append(types, env.Type), append(versions, env.AggregateVersion)
	}
	wantTypes := []string{"plan.confirmed", "plan.cancel.requested", "plan.cancel.aborted", "plan.cancel.requested", "plan.cancelled"}
	if !slices.Equal(types, wantTypes) || !slices.Equal(versions, []int64{1, 2, 3, 4, 5}) {
		t.Errorf("events %v at versions %v, want %v at 1..5", types, versions, wantTypes)
	}
}

func TestAVoteThatArrivesWhileTheAttemptIsBeingWrittenIsNotLost(t *testing.T) {
	w := newWorld(t)
	p := w.confirmed()
	ctx := t.Context()

	// The attempt is written in a transaction that has not committed yet: the plan row is locked by it.
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // after a commit this is a no-op
	rec, err := Lock(ctx, tx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.svc.startCancel(ctx, tx, &rec, cancelRequest{By: contracts.CancelByCustomer, Locale: platform.LocaleEN}); err != nil {
		t.Fatal(err)
	}
	var requestID string
	if err := tx.QueryRow(ctx, `SELECT request_id FROM plan_cancel_requests WHERE plan_id = $1`, p.ID).Scan(&requestID); err != nil {
		t.Fatal(err)
	}

	// The vote is already here. It must wait for the state to be persisted, not be taken for a stranger's.
	voted := make(chan error, 1)
	go func() {
		env, err := contracts.NewEnvelope("driver-api", contracts.TypePlanCancelAccepted, p.ID, 0,
			contracts.PlanCancelAccepted{PlanID: p.ID, RequestID: requestID, RouteID: p.ID + ":r1"})
		if err != nil {
			voted <- err
			return
		}
		voted <- platform.WithTx(ctx, w.pool, func(tx pgx.Tx) error { return w.svc.onAccepted(ctx, tx, env) })
	}()
	select {
	case err := <-voted:
		t.Fatalf("the vote was handled (%v) while the attempt was uncommitted: it read a state that does not exist yet", err)
	case <-time.After(300 * time.Millisecond):
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-voted; err != nil {
		t.Fatal(err)
	}
	if got := w.stored(p.ID).Plan; got.Status != contracts.PlanStatusCancelled || got.PendingAction != "" {
		t.Errorf("plan = %s %q: the vote that waited was not counted", got.Status, got.PendingAction)
	}
	if w.refunds() != 1 {
		t.Errorf("%d refunds", w.refunds())
	}
}

// Whoever gets to a plan first decides it: the last vote and the sweeper race for the same attempt, and
// the same final vote may arrive twice at once. Always one outcome, never both, never two refunds.
func TestTheLastVoteTheSweeperAndARedeliveryRaceToOneOutcome(t *testing.T) {
	w := newWorld(t)
	committed, aborted := 0, 0
	for round := range 12 {
		p := w.confirmed()
		w.addRoute(p.ID)
		if _, err := w.svc.Cancel(t.Context(), w.customer, p.ID, ""); err != nil {
			t.Fatal(err)
		}
		req := w.request(p.ID)
		w.accept(p.ID, req.ID, p.ID+":r1")
		w.clock.advance(DefaultCancelTimeout) // the deadline has just passed: the sweeper is entitled to abort
		balance := w.balance()

		var wg sync.WaitGroup
		for range 2 {
			wg.Go(func() {
				env, err := contracts.NewEnvelope("driver-api", contracts.TypePlanCancelAccepted, p.ID, 0,
					contracts.PlanCancelAccepted{PlanID: p.ID, RequestID: req.ID, RouteID: p.ID + ":r2"})
				if err == nil {
					err = platform.WithTx(t.Context(), w.pool, func(tx pgx.Tx) error { return w.svc.onAccepted(t.Context(), tx, env) })
				}
				if err != nil {
					t.Errorf("round %d: vote: %v", round, err)
				}
			})
		}
		wg.Go(func() {
			if _, err := w.svc.SweepCancels(t.Context()); err != nil {
				t.Errorf("round %d: sweep: %v", round, err)
			}
		})
		wg.Wait()

		rec, status := w.stored(p.ID), w.request(p.ID).Status
		switch {
		case status == cancelCommitted && rec.Plan.Status == contracts.PlanStatusCancelled && w.balance() == balance+testkit.OptionCostRp:
			committed++
		case status == cancelAborted && rec.Plan.Status == contracts.PlanStatusAwaitingDriver && w.balance() == balance:
			aborted++
		default:
			t.Fatalf("round %d: request %s, plan %s, balance %d (was %d): not one clean outcome", round, status, rec.Plan.Status, w.balance(), balance)
		}
		if rec.Plan.PendingAction != "" || rec.Version != 3 {
			t.Fatalf("round %d: pendingAction %q, version %d", round, rec.Plan.PendingAction, rec.Version)
		}
		w.clock.advance(-DefaultCancelTimeout)
	}
	if got := w.refunds(); got != committed {
		t.Errorf("%d refunds for %d committed cancels", got, committed)
	}
	if got, want := w.published(contracts.TypePlanCancelled), committed; got != want {
		t.Errorf("%d plan.cancelled for %d commits", got, want)
	}
	if got, want := w.published(contracts.TypePlanCancelAborted), aborted; got != want {
		t.Errorf("%d plan.cancel.aborted for %d aborts", got, want)
	}
	t.Logf("%d committed, %d aborted", committed, aborted)
}

func TestACancelWithoutAnyRouteHasNobodyToAsk(t *testing.T) {
	w := newWorld(t)
	p := w.confirmed()
	err := platform.WithTx(t.Context(), w.pool, func(tx pgx.Tx) error {
		rec, err := Lock(t.Context(), tx, p.ID)
		if err != nil {
			return err
		}
		rec.Plan.Routes = nil
		return Save(t.Context(), tx, rec)
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := w.svc.Cancel(t.Context(), w.customer, p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != contracts.PlanStatusCancelled || got.PendingAction != "" || w.refunds() != 1 || w.published(contracts.TypePlanCancelRequested) != 0 {
		t.Errorf("plan %s %q, %d refunds, %d asked", got.Status, got.PendingAction, w.refunds(), w.published(contracts.TypePlanCancelRequested))
	}
}

// listed is the plan's row in the customer's plan list.
func (w *world) listed(planID string) Summary {
	w.t.Helper()
	rows, err := w.svc.List(w.t.Context(), w.customer, Filter{})
	if err != nil {
		w.t.Fatal(err)
	}
	for _, r := range rows {
		if r.ID == planID {
			return r
		}
	}
	w.t.Fatalf("plan %s is not in the list", planID)
	return Summary{}
}

// The list and the plan itself say "cancelling" exactly while an attempt is open: the mark is written in the
// transaction that opens the attempt and cleared in the one that finishes it, so neither can show it stale.
func TestListRowsAndPlansShowPendingActionOnlyWhileACancelIsOpen(t *testing.T) {
	w := newWorld(t)
	committed, aborted := w.confirmed(), w.confirmed()
	w.addRoute(aborted.ID)
	quiet := w.confirmed()

	shows := func(id string) (row, plan string) {
		got, err := w.svc.Get(t.Context(), w.customer, id)
		if err != nil {
			t.Fatal(err)
		}
		return w.listed(id).PendingAction, got.PendingAction
	}
	for _, p := range []contracts.Plan{committed, aborted, quiet} {
		if row, plan := shows(p.ID); row != "" || plan != "" {
			t.Fatalf("before any cancel %s shows %q / %q", p.ID, row, plan)
		}
	}

	for _, p := range []contracts.Plan{committed, aborted} {
		if _, err := w.svc.Cancel(t.Context(), w.customer, p.ID, ""); err != nil {
			t.Fatal(err)
		}
		if row, plan := shows(p.ID); row != "cancel" || plan != "cancel" {
			t.Errorf("with an open request %s shows %q / %q, want cancel on both", p.ID, row, plan)
		}
	}
	if row, plan := shows(quiet.ID); row != "" || plan != "" {
		t.Errorf("a plan nobody cancelled shows %q / %q", row, plan)
	}
	raw, err := json.Marshal(w.listed(committed.ID))
	if err != nil || !strings.Contains(string(raw), `"pendingAction":"cancel"`) {
		t.Errorf("wire form of the row = %s (%v)", raw, err)
	}
	if raw, _ := json.Marshal(w.listed(quiet.ID)); strings.Contains(string(raw), "pendingAction") {
		t.Errorf("a row without an open cancel carries the field: %s", raw)
	}

	// Committed: gone from both, and the request row is finished.
	w.accept(committed.ID, w.request(committed.ID).ID, committed.ID+":r1")
	row, plan := shows(committed.ID)
	if row != "" || plan != "" || w.request(committed.ID).Status != cancelCommitted || w.listed(committed.ID).Status != contracts.PlanStatusCancelled {
		t.Errorf("after the commit: %q / %q, request %s, row status %s", row, plan, w.request(committed.ID).Status, w.listed(committed.ID).Status)
	}

	// Aborted by a refusal: gone from both, the plan is as it was. Aborted by the sweeper: same for a second attempt.
	w.reject(aborted.ID, w.request(aborted.ID).ID, aborted.ID+":r1", contracts.Msg{Key: "srv.api.cancelRunning"})
	row, plan = shows(aborted.ID)
	if row != "" || plan != "" || w.request(aborted.ID).Status != cancelAborted || w.listed(aborted.ID).Status != contracts.PlanStatusAwaitingDriver {
		t.Errorf("after the abort: %q / %q, request %s, row status %s", row, plan, w.request(aborted.ID).Status, w.listed(aborted.ID).Status)
	}
	if _, err := w.svc.Cancel(t.Context(), w.customer, aborted.ID, ""); err != nil {
		t.Fatal(err)
	}
	if row, _ := shows(aborted.ID); row != "cancel" {
		t.Fatalf("a second attempt shows %q", row)
	}
	w.clock.advance(DefaultCancelTimeout)
	if n, err := w.svc.SweepCancels(t.Context()); err != nil || n != 1 {
		t.Fatalf("swept %d (%v)", n, err)
	}
	if row, plan := shows(aborted.ID); row != "" || plan != "" {
		t.Errorf("after the sweeper aborted it: %q / %q", row, plan)
	}

	// Whatever the state of the request rows, no plan shows the mark without an open request.
	rows, err := w.pool.Query(t.Context(), `SELECT id FROM plans WHERE snapshot->>'pendingAction' = 'cancel'
		AND NOT EXISTS (SELECT 1 FROM plan_cancel_requests r WHERE r.plan_id = plans.id AND r.status = 'pending')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		t.Errorf("plan %s is marked as being cancelled without a pending request", id)
	}
}
