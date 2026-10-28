package wallet

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/testkit"
)

func newWallet(t *testing.T) (*Service, *pgxpool.Pool, string) {
	t.Helper()
	pool := testkit.DB(t)
	return New(pool, testkit.Publisher(pool)), pool, testkit.Customer(t, pool, "Dina")
}

func apiError(t *testing.T, err error) *platform.APIError {
	t.Helper()
	apiErr, ok := errors.AsType[*platform.APIError](err)
	if !ok {
		t.Fatalf("want a platform.APIError, got %T: %v", err, err)
	}
	return apiErr
}

// charge runs ChargePlan in a transaction of its own, like plans.Confirm does inside its bigger one.
func charge(s *Service, customerID, planID string, amountRp int64) error {
	ctx := context.Background()
	return platform.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		return s.ChargePlan(ctx, tx, customerID, planID, "PL-"+planID, amountRp)
	})
}

func TestTopUpAmountAndMethodAreChecked(t *testing.T) {
	s := &Service{} // refuses before it touches the database
	tests := []struct {
		name   string
		in     TopUpInput
		field  string
		msgKey string
	}{
		{"zero", TopUpInput{0, contracts.TopUpMethodQRIS}, "amountRp", "dom.topup.errEnter"},
		{"negative", TopUpInput{-5, contracts.TopUpMethodQRIS}, "amountRp", "dom.topup.errEnter"},
		{"fraction", TopUpInput{10_000.5, contracts.TopUpMethodQRIS}, "amountRp", "dom.topup.errInteger"},
		{"too small", TopUpInput{TopUpMinRp - 1, contracts.TopUpMethodQRIS}, "amountRp", "dom.topup.errMin"},
		{"too large", TopUpInput{TopUpMaxRp + 1, contracts.TopUpMethodQRIS}, "amountRp", "dom.topup.errMax"},
		{"unknown method", TopUpInput{50_000, "cash"}, "method", "srv.ops.err.chooseMethod"},
		{"no method", TopUpInput{50_000, ""}, "method", "srv.ops.err.chooseMethod"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.CreateTopUp(t.Context(), "cus-1", tt.in)
			got := apiError(t, err)
			if got.Code != platform.CodeInvalid || got.Field != tt.field || got.Msg.Key != tt.msgKey {
				t.Errorf("got %s %q %q, want %s %q %q", got.Code, got.Field, got.Msg.Key, platform.CodeInvalid, tt.field, tt.msgKey)
			}
		})
	}
}

func TestCreateTopUpWaitsForPaymentAndIsAnnounced(t *testing.T) {
	s, pool, customer := newWallet(t)
	created, err := s.CreateTopUp(t.Context(), customer, TopUpInput{AmountRp: 100_000, Method: contracts.TopUpMethodVABCA})
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != contracts.TopUpStatusPending || created.UserID != customer || created.AmountRp != 100_000 {
		t.Errorf("created = %+v", created)
	}
	// The bank's prefix plus the last 11 digits of the customer's phone number (081234567890).
	if want := "3935881234567890"; created.Reference != want {
		t.Errorf("virtual account = %q, want %q", created.Reference, want)
	}
	if got := created.ExpiresAt.Sub(created.CreatedAt.Time); got != 24*time.Hour {
		t.Errorf("a virtual account lasts %v, want 24h", got)
	}
	qris, err := s.CreateTopUp(t.Context(), customer, TopUpInput{AmountRp: 20_000, Method: contracts.TopUpMethodQRIS})
	if err != nil {
		t.Fatal(err)
	}
	if got := qris.ExpiresAt.Sub(qris.CreatedAt.Time); got != 30*time.Minute || qris.Reference[:6] != "LOONG-" {
		t.Errorf("qris = %+v lasts %v", qris, got)
	}

	w, err := s.Get(t.Context(), customer)
	if err != nil {
		t.Fatal(err)
	}
	if w.BalanceRp != 0 || len(w.PendingTopUps) != 2 || w.PendingTopUps[0].ID != qris.ID {
		t.Errorf("wallet = %+v, want no balance and both top-ups, newest first", w)
	}

	envs := testkit.Outbox(t, pool, contracts.TypeTopUpCreated)
	if len(envs) != 2 || envs[0].Key != customer || envs[0].AggregateVersion != 1 || envs[1].AggregateVersion != 2 {
		t.Fatalf("outbox = %+v, want two topup.created keyed by the customer with versions 1 and 2", envs)
	}
	if p, err := envs[0].Payload[contracts.TopUpCreated](); err != nil || p.TopUp.ID != created.ID {
		t.Errorf("payload = %+v, %v", p, err)
	}
}

func TestOnlyThreeTopUpsMayWaitAtOnce(t *testing.T) {
	s, _, customer := newWallet(t)
	for range MaxPendingTopUps {
		if _, err := s.CreateTopUp(t.Context(), customer, TopUpInput{AmountRp: 50_000, Method: contracts.TopUpMethodQRIS}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.CreateTopUp(t.Context(), customer, TopUpInput{AmountRp: 50_000, Method: contracts.TopUpMethodQRIS})
	if got := apiError(t, err); got.Code != platform.CodeConflict || got.Msg.Key != "srv.ops.err.tooManyPending" {
		t.Errorf("got %+v", got)
	}
}

func TestSimulatedPaymentCreditsTheWalletOnce(t *testing.T) {
	s, pool, customer := newWallet(t)
	top, err := s.CreateTopUp(t.Context(), customer, TopUpInput{AmountRp: 75_000, Method: contracts.TopUpMethodQRIS})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 { // the second call must change nothing
		paid, err := s.SimulatePay(t.Context(), customer, top.ID)
		if err != nil {
			t.Fatal(err)
		}
		if paid.Status != contracts.TopUpStatusSucceeded || paid.PaidAt.IsZero() {
			t.Errorf("paid = %+v", paid)
		}
	}
	w, err := s.Get(t.Context(), customer)
	if err != nil {
		t.Fatal(err)
	}
	if w.BalanceRp != 75_000 || len(w.PendingTopUps) != 0 {
		t.Errorf("wallet = %+v, want 75000 and nothing pending", w)
	}
	if n := testkit.Count(t, pool, `SELECT count(*) FROM wallet_entries WHERE kind = 'topup'`); n != 1 {
		t.Errorf("%d top-up entries, want 1", n)
	}
	if n := len(testkit.Outbox(t, pool, contracts.TypeTopUpPaid)); n != 1 {
		t.Errorf("%d topup.paid events, want 1", n)
	}
	entries, err := s.Transactions(t.Context(), customer)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
	if e := entries[0]; e.Kind != "topup" || e.AmountRp != 75_000 || e.BalanceAfterRp != 75_000 || e.TopUpID != top.ID || e.PlanID != "" || e.Title.Key != "srv.ops.wallet.topUp" {
		t.Errorf("entry = %+v", e)
	}
}

func TestLedgerBalanceAlwaysEqualsTheSumOfEntries(t *testing.T) {
	s, pool, customer := newWallet(t)
	testkit.Fund(t, pool, customer, 100_000)
	top, err := s.CreateTopUp(t.Context(), customer, TopUpInput{AmountRp: 50_000, Method: contracts.TopUpMethodQRIS})
	if err != nil {
		t.Fatal(err)
	}
	steps := []func() error{
		func() error { return charge(s, customer, "p1", 30_000) },
		func() error { _, err := s.SimulatePay(t.Context(), customer, top.ID); return err },
		func() error { return charge(s, customer, "p2", 90_000) },
		func() error { return charge(s, customer, "p3", 500_000) }, // short: must change nothing
		func() error { return charge(s, customer, "p1", 1_000) },   // already charged: must change nothing
		func() error { return charge(s, customer, "p4", 30_000) },
	}
	for i, step := range steps {
		_ = step() // some are meant to fail
		var balance, sum int64
		if err := pool.QueryRow(t.Context(),
			`SELECT w.balance_rp, COALESCE(sum(e.delta_rp), 0)
			   FROM wallets w LEFT JOIN wallet_entries e USING (customer_id) WHERE w.customer_id = $1 GROUP BY w.balance_rp`, customer).Scan(&balance, &sum); err != nil {
			t.Fatal(err)
		}
		if balance != sum {
			t.Fatalf("after step %d the balance is %d but the entries sum to %d", i+1, balance, sum)
		}
	}

	entries, err := s.Transactions(t.Context(), customer)
	if err != nil {
		t.Fatal(err)
	}
	// Oldest first, each entry's balanceAfter follows from the one before it.
	var running int64
	for i := len(entries) - 1; i >= 0; i-- {
		running += entries[i].AmountRp
		if entries[i].BalanceAfterRp != running {
			t.Errorf("entry %s says balance %d after it, the running sum is %d", entries[i].ID, entries[i].BalanceAfterRp, running)
		}
	}
	if len(entries) != 5 { // seed, p1, top-up, p2, p4
		t.Errorf("%d entries, want 5: %+v", len(entries), entries)
	}
	if w, _ := s.Get(t.Context(), customer); w.BalanceRp != 100_000-30_000+50_000-90_000-30_000 {
		t.Errorf("balance = %d", w.BalanceRp)
	}
}

func TestShortBalanceIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	s, pool, customer := newWallet(t)
	testkit.Fund(t, pool, customer, 10_000)
	before := testkit.OutboxTypes(t, pool)

	err := charge(s, customer, "p1", 25_000)
	got := apiError(t, err)
	if got.Code != platform.CodeInvalid || got.Field != "paymentMethod" || got.Msg.Key != "srv.ops.err.insufficientBalanceDetail" {
		t.Fatalf("got %+v", got)
	}
	if got.Msg.Params["balance"] != int64(10_000) || got.Msg.Params["cost"] != int64(25_000) {
		t.Errorf("params = %v, want the balance and the cost", got.Msg.Params)
	}
	if n := testkit.Count(t, pool, `SELECT count(*) FROM wallet_entries WHERE kind = 'pembayaran'`); n != 0 {
		t.Errorf("%d payment entries written", n)
	}
	if after := testkit.OutboxTypes(t, pool); len(after) != len(before) {
		t.Errorf("events published %v -> %v", before, after)
	}
}

func TestAPlanCannotBeChargedTwice(t *testing.T) {
	s, pool, customer := newWallet(t)
	testkit.Fund(t, pool, customer, 100_000)
	if err := charge(s, customer, "p1", 20_000); err != nil {
		t.Fatal(err)
	}
	err := charge(s, customer, "p1", 20_000)
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "23505" {
		t.Fatalf("second charge = %v, want a unique violation from the ledger", err)
	}
	if w, _ := s.Get(t.Context(), customer); w.BalanceRp != 80_000 {
		t.Errorf("balance = %d, want 80000: the plan was charged once", w.BalanceRp)
	}
	charged := testkit.Outbox(t, pool, contracts.TypeWalletCharged)
	if len(charged) != 1 {
		t.Fatalf("%d wallet.charged events, want 1", len(charged))
	}
	if p, err := charged[0].Payload[contracts.WalletCharged](); err != nil || p.PlanID != "p1" || p.AmountRp != 20_000 || p.BalanceAfterRp != 80_000 || p.CustomerID != customer {
		t.Errorf("payload = %+v, %v", p, err)
	}
}

func TestOverdueTopUpsExpireWhenTheWalletIsRead(t *testing.T) {
	s, pool, customer := newWallet(t)
	start := time.Date(2026, 10, 21, 3, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return start }
	qris, err := s.CreateTopUp(t.Context(), customer, TopUpInput{AmountRp: 20_000, Method: contracts.TopUpMethodQRIS})
	if err != nil {
		t.Fatal(err)
	}
	va, err := s.CreateTopUp(t.Context(), customer, TopUpInput{AmountRp: 20_000, Method: contracts.TopUpMethodVABRI})
	if err != nil {
		t.Fatal(err)
	}

	s.now = func() time.Time { return start.Add(31 * time.Minute) } // the QR code is over, the account is not
	w, err := s.Get(t.Context(), customer)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.PendingTopUps) != 1 || w.PendingTopUps[0].ID != va.ID {
		t.Errorf("pending = %+v, want only the virtual account", w.PendingTopUps)
	}
	if got, err := s.GetTopUp(t.Context(), customer, qris.ID); err != nil || got.Status != contracts.TopUpStatusExpired {
		t.Errorf("qris = %+v, %v", got, err)
	}
	if _, err := s.SimulatePay(t.Context(), customer, qris.ID); apiError(t, err).Msg.Key != "srv.ops.err.topUpExpired" {
		t.Errorf("paying an expired top-up: %v", err)
	}
	// Announced once, however often it is read.
	if expired := testkit.Outbox(t, pool, contracts.TypeTopUpExpired); len(expired) != 1 {
		t.Errorf("%d topup.expired events, want 1", len(expired))
	}
}

func TestTopUpsBelongToTheirOwner(t *testing.T) {
	s, pool, owner := newWallet(t)
	other := testkit.Customer(t, pool, "Raka")
	top, err := s.CreateTopUp(t.Context(), owner, TopUpInput{AmountRp: 20_000, Method: contracts.TopUpMethodQRIS})
	if err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() error{
		"get":    func() error { _, err := s.GetTopUp(t.Context(), other, top.ID); return err },
		"pay":    func() error { _, err := s.SimulatePay(t.Context(), other, top.ID); return err },
		"cancel": func() error { _, err := s.CancelTopUp(t.Context(), other, top.ID); return err },
	} {
		if got := apiError(t, call()); got.Code != platform.CodeDenied || got.Status != 403 {
			t.Errorf("%s: got %+v, want 403 ditolak", name, got)
		}
	}
	if _, err := s.GetTopUp(t.Context(), owner, "top-missing"); apiError(t, err).Status != 404 {
		t.Errorf("unknown top-up: %v", err)
	}
	if got, _ := s.GetTopUp(t.Context(), owner, top.ID); got.Status != contracts.TopUpStatusPending {
		t.Errorf("the other customer's attempts changed the top-up: %+v", got)
	}
}

func TestCancelledTopUpCannotBePaid(t *testing.T) {
	s, pool, customer := newWallet(t)
	top, err := s.CreateTopUp(t.Context(), customer, TopUpInput{AmountRp: 20_000, Method: contracts.TopUpMethodQRIS})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := s.CancelTopUp(t.Context(), customer, top.ID)
	if err != nil || cancelled.Status != contracts.TopUpStatusCancelled {
		t.Fatalf("cancel = %+v, %v", cancelled, err)
	}
	if _, err := s.CancelTopUp(t.Context(), customer, top.ID); apiError(t, err).Msg.Key != "srv.ops.err.onlyPendingCancel" {
		t.Errorf("cancelling twice: %v", err)
	}
	if _, err := s.SimulatePay(t.Context(), customer, top.ID); apiError(t, err).Msg.Key != "srv.ops.err.topUpCancelled" {
		t.Errorf("paying a cancelled top-up: %v", err)
	}
	if w, _ := s.Get(t.Context(), customer); w.BalanceRp != 0 || len(w.PendingTopUps) != 0 {
		t.Errorf("wallet = %+v", w)
	}
	// the back office learns of it: exactly one event, carrying the cancelled top-up (the refusals above published none)
	events := testkit.Outbox(t, pool, contracts.TypeTopUpCancelled)
	if len(events) != 1 {
		t.Fatalf("%d topup.cancelled events, want 1", len(events))
	}
	got, err := events[0].Payload[contracts.TopUpCancelled]()
	if err != nil || got.TopUp.ID != top.ID || got.TopUp.Status != contracts.TopUpStatusCancelled {
		t.Errorf("event = %+v, %v", got, err)
	}
}

func TestWalletEventsAreNumberedPerCustomer(t *testing.T) {
	s, pool, customer := newWallet(t)
	other := testkit.Customer(t, pool, "Raka")
	top, _ := s.CreateTopUp(t.Context(), customer, TopUpInput{AmountRp: 50_000, Method: contracts.TopUpMethodQRIS})
	if _, err := s.CreateTopUp(t.Context(), other, TopUpInput{AmountRp: 50_000, Method: contracts.TopUpMethodQRIS}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SimulatePay(t.Context(), customer, top.ID); err != nil {
		t.Fatal(err)
	}
	if err := charge(s, customer, "p1", 10_000); err != nil {
		t.Fatal(err)
	}

	versions := map[string][]int64{}
	for _, typ := range []string{contracts.TypeTopUpCreated, contracts.TypeTopUpPaid, contracts.TypeWalletCharged} {
		for _, env := range testkit.Outbox(t, pool, typ) {
			versions[env.Key] = append(versions[env.Key], env.AggregateVersion)
		}
	}
	// created, paid, charged for the first customer (outbox order is per type here, so compare as a set).
	if got := versions[customer]; len(got) != 3 {
		t.Fatalf("customer versions = %v", got)
	}
	seen := map[int64]bool{}
	for _, v := range versions[customer] {
		seen[v] = true
	}
	if !seen[1] || !seen[2] || !seen[3] {
		t.Errorf("versions %v are not 1, 2, 3", versions[customer])
	}
	if got := versions[other]; len(got) != 1 || got[0] != 1 {
		t.Errorf("other customer's versions = %v, want [1]: each wallet counts for itself", got)
	}
}
