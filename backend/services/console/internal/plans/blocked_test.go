package plans

import (
	"testing"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/testkit"
)

func (w *world) setBlocked(blocked bool) {
	w.t.Helper()
	if _, err := w.pool.Exec(w.t.Context(), `UPDATE customers SET blocked = $2 WHERE id = $1`, w.customer, blocked); err != nil {
		w.t.Fatal(err)
	}
}

func TestABlockedCustomerCanNeitherStartNorPayForAPlan(t *testing.T) {
	w := newWorld(t)
	testkit.Fund(t, w.pool, w.customer, 100_000)
	ready := w.ready("s1", "s2")
	balance, before := w.balance(), len(testkit.OutboxTypes(t, w.pool))
	plansBefore := testkit.Count(t, w.pool, `SELECT count(*) FROM plans`)

	w.setBlocked(true)
	_, err := w.svc.CreateDraft(t.Context(), w.customer)
	wantError(t, err, platform.CodeDenied, "srv.ops.err.accountBlocked")
	_, err = w.svc.Confirm(t.Context(), w.customer, ready.ID, contracts.PaymentWallet)
	wantError(t, err, platform.CodeDenied, "srv.ops.err.accountBlocked")

	if got := w.stored(ready.ID).Plan.Status; got != contracts.PlanStatusOptionsReady {
		t.Errorf("plan = %s: the refused confirmation changed it", got)
	}
	if w.balance() != balance || len(testkit.OutboxTypes(t, w.pool)) != before || testkit.Count(t, w.pool, `SELECT count(*) FROM plans`) != plansBefore {
		t.Error("a blocked customer's refused request charged, published or created something")
	}

	// Unblocked again, the very same plan goes through.
	w.setBlocked(false)
	if _, err := w.svc.Confirm(t.Context(), w.customer, ready.ID, contracts.PaymentWallet); err != nil {
		t.Errorf("after unblocking: %v", err)
	}
	if _, err := w.svc.CreateDraft(t.Context(), w.customer); err != nil {
		t.Errorf("after unblocking: %v", err)
	}
}

func TestConfirmingNotifiesTheCustomerOnce(t *testing.T) {
	w := newWorld(t)
	p := w.confirmed()

	notes := w.notices()
	if len(notes) != 1 || notes[0].Title.Key != "srv.api.notif.confirmed.title" || notes[0].Body.Key != "srv.api.notif.confirmed.body" ||
		notes[0].Body.Params["code"] != p.Code || notes[0].PlanID != p.ID || notes[0].Read {
		t.Fatalf("notifications = %+v, want one unread 'order confirmed' about %s", notes, p.Code)
	}
	// A confirmation that fails notifies nobody.
	ready := w.ready("s1")
	_, err := w.svc.Confirm(t.Context(), w.customer, ready.ID, contracts.PaymentWallet) // the wallet is empty
	wantError(t, err, platform.CodeInvalid, "srv.ops.err.insufficientBalanceDetail")
	if got := len(w.notices()); got != 1 {
		t.Errorf("%d notifications after a refused confirmation, want still 1", got)
	}
}
