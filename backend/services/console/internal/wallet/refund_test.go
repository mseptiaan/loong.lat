package wallet

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/notices"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/testkit"
)

// refund runs RefundPlan in a transaction of its own, like the cancel does inside its bigger one.
func refund(s *Service, customerID, planID string) (int64, error) {
	ctx := context.Background()
	var amount int64
	err := platform.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		amount, err = s.RefundPlan(ctx, tx, customerID, planID, "PL-"+planID)
		return err
	})
	return amount, err
}

func TestARefundReturnsWhatWasChargedInFullAndOnlyOnce(t *testing.T) {
	s, pool, customer := newWallet(t)
	testkit.Fund(t, pool, customer, 100_000)
	if err := charge(s, customer, "p1", 30_000); err != nil {
		t.Fatal(err)
	}
	if err := charge(s, customer, "p2", 20_000); err != nil {
		t.Fatal(err)
	}

	got, err := refund(s, customer, "p1")
	if err != nil || got != 30_000 {
		t.Fatalf("refund = %d, %v, want 30000", got, err)
	}
	for range 3 { // the same plan again: nothing more
		if again, err := refund(s, customer, "p1"); err != nil || again != 0 {
			t.Fatalf("second refund = %d, %v, want 0", again, err)
		}
	}
	if w, _ := s.Get(t.Context(), customer); w.BalanceRp != 100_000-30_000-20_000+30_000 {
		t.Errorf("balance = %d: p1 refunded once, p2 untouched", w.BalanceRp)
	}

	entries, err := s.Transactions(t.Context(), customer)
	if err != nil {
		t.Fatal(err)
	}
	var e *Entry
	for i := range entries {
		if entries[i].Kind == "refund" {
			if e != nil {
				t.Fatal("two refund entries")
			}
			e = &entries[i]
		}
	}
	if e == nil || e.AmountRp != 30_000 || e.PlanID != "p1" || e.TopUpID != "" || e.BalanceAfterRp != 80_000 || e.Title.Key != "srv.ops.wallet.refund" || e.Title.Params["code"] != "PL-p1" {
		t.Fatalf("refund entry = %+v", e)
	}

	events := testkit.Outbox(t, pool, contracts.TypeWalletRefunded)
	if len(events) != 1 {
		t.Fatalf("%d wallet.refunded events, want 1", len(events))
	}
	ev, err := events[0].Payload[contracts.WalletRefunded]()
	if err != nil || ev.PlanID != "p1" || ev.AmountRp != 30_000 || ev.BalanceAfterRp != 80_000 || ev.CustomerID != customer || ev.TxID != e.ID {
		t.Errorf("wallet.refunded = %+v, %v", ev, err)
	}
	if events[0].Key != customer {
		t.Errorf("keyed %s, want the customer", events[0].Key)
	}
}

func TestAPlanThatWasNeverChargedHasNothingToRefund(t *testing.T) {
	s, pool, customer := newWallet(t)
	other := testkit.Customer(t, pool, "Raka")
	testkit.Fund(t, pool, other, 50_000)
	if err := charge(s, other, "p9", 25_000); err != nil {
		t.Fatal(err)
	}
	before := len(testkit.OutboxTypes(t, pool))

	if got, err := refund(s, customer, "p-unpaid"); err != nil || got != 0 {
		t.Errorf("refund of an unpaid plan = %d, %v", got, err)
	}
	// Somebody else's charge is not this customer's to get back.
	if got, err := refund(s, customer, "p9"); err != nil || got != 0 {
		t.Errorf("refund of another customer's payment = %d, %v", got, err)
	}
	if n := len(testkit.OutboxTypes(t, pool)); n != before {
		t.Errorf("%d events published for refunds of nothing", n-before)
	}
	if w, _ := s.Get(t.Context(), customer); w.BalanceRp != 0 {
		t.Errorf("balance = %d", w.BalanceRp)
	}
}

func TestRefundsKeepTheLedgerAndTheBalanceInStep(t *testing.T) {
	s, pool, customer := newWallet(t)
	testkit.Fund(t, pool, customer, 100_000)
	for _, step := range []func() error{
		func() error { return charge(s, customer, "p1", 30_000) },
		func() error { _, err := refund(s, customer, "p1"); return err },
		func() error { return charge(s, customer, "p2", 100_000) },
		func() error { _, err := refund(s, customer, "p2"); return err },
		func() error { _, err := refund(s, customer, "p2"); return err },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	var balance, sum int64
	err := pool.QueryRow(t.Context(),
		`SELECT w.balance_rp, sum(e.delta_rp) FROM wallets w JOIN wallet_entries e USING (customer_id) WHERE w.customer_id = $1 GROUP BY w.balance_rp`, customer).Scan(&balance, &sum)
	if err != nil || balance != sum || balance != 100_000 {
		t.Errorf("balance %d, entries sum %d (%v), want both 100000", balance, sum, err)
	}
}

func TestAPaidTopUpIsAnnouncedWithItsLedgerLineAndNotifiedOnce(t *testing.T) {
	s, pool, customer := newWallet(t)
	top, err := s.CreateTopUp(t.Context(), customer, TopUpInput{AmountRp: 75_000, Method: contracts.TopUpMethodQRIS})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := s.SimulatePay(t.Context(), customer, top.ID); err != nil {
			t.Fatal(err)
		}
	}

	entries, _ := s.Transactions(t.Context(), customer)
	envs := testkit.Outbox(t, pool, contracts.TypeTopUpPaid)
	if len(envs) != 1 || len(entries) != 1 {
		t.Fatalf("%d topup.paid events, %d ledger entries", len(envs), len(entries))
	}
	ev, err := envs[0].Payload[contracts.TopUpPaid]()
	if err != nil || ev.TxID != entries[0].ID || ev.BalanceAfterRp != 75_000 || ev.TopUp.ID != top.ID || ev.TopUp.Status != contracts.TopUpStatusSucceeded {
		t.Errorf("topup.paid = %+v, %v; want the ledger line %s and the balance after it", ev, err, entries[0].ID)
	}

	list, err := notices.List(t.Context(), pool, notices.Customer(customer))
	if err != nil || len(list) != 1 {
		t.Fatalf("notifications = %+v, %v", list, err)
	}
	if n := list[0]; n.Title.Key != "srv.ops.wallet.topUpDoneTitle" || n.Body.Key != "srv.ops.wallet.topUpDoneBody" || n.Body.Params["amount"] != float64(75_000) || n.Read || n.PlanID != "" {
		t.Errorf("notification = %+v", n)
	}
}
