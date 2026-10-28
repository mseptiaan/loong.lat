package finance

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/changes"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/plans"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/testkit"
)

// These tests need a real Postgres (TEST_DATABASE_URL) and skip without one.

// now is 19:00 in Jakarta on 21 October 2026: "today" starts at 17:00 UTC the evening before.
var now = time.Date(2026, time.October, 21, 12, 0, 0, 0, time.UTC)

type rig struct {
	pool *pgxpool.Pool
	svc  *Service
}

func newRig(t *testing.T) *rig {
	t.Helper()
	pool := testkit.Pool(t)
	svc := New(pool, changes.New(platform.NewHub(), time.Millisecond), func() time.Time { return now })
	for _, c := range []struct{ id, name, email, phone string }{
		{"cus-1", "Dina Pramesti", "dina@contoh.id", "081234567890"},
		{"cus-2", "Agus Salim", "agus@contoh.id", "081298765432"},
	} {
		if _, err := pool.Exec(t.Context(), `INSERT INTO customers (id, name, email, phone, version) VALUES ($1, $2, $3, $4, 1)`, c.id, c.name, c.email, c.phone); err != nil {
			t.Fatal(err)
		}
	}
	return &rig{pool: pool, svc: svc}
}

// feed builds a wallet event of the customer at the wallet's version, as console-api would publish it.
func feed(t *testing.T, eventType, customer string, version int64, at time.Time, data any) contracts.Envelope {
	t.Helper()
	env, err := contracts.NewEnvelope("console-api", eventType, customer, version, data)
	if err != nil {
		t.Fatal(err)
	}
	env.OccurredAt = contracts.At(at)
	return env
}

func (r *rig) deliver(t *testing.T, h eventbus.Handler, env contracts.Envelope) {
	t.Helper()
	if err := testkit.ApplyEnvelope(r.pool, h, env); err != nil {
		t.Fatalf("%s: %v", env.Type, err)
	}
}

func hours(h float64) time.Duration { return time.Duration(h * float64(time.Hour)) }

func topUp(id, customer string, amount int64, method contracts.TopUpMethod, status contracts.TopUpStatus, created time.Time, ttl time.Duration, paid time.Time) contracts.TopUp {
	t := contracts.TopUp{
		ID: id, UserID: customer, AmountRp: amount, Method: method, Status: status,
		CreatedAt: contracts.At(created), ExpiresAt: contracts.At(created.Add(ttl)), Reference: "REF-" + id,
	}
	if !paid.IsZero() {
		t.PaidAt = contracts.At(paid)
	}
	return t
}

// load fills the projection with the story below and returns the envelopes in case a test wants to replay one.
//
//	cus-1  v1 top-up t1 (Rp 100.000, QRIS) created        v2 t1 paid today 10:00 WIB  -> balance 100.000
//	       v3 top-up t2 (Rp 50.000, BCA) created           v4 t2 paid yesterday 17:00 WIB -> 150.000
//	       v5 charged Rp 40.000 for pln-1 -> 110.000       v6 refunded Rp 40.000 -> 150.000
//	       v7 charged Rp 25.000 for pln-2 -> 125.000       v8 top-up t6 (Rp 30.000, BRI) created, v9 expired
//	cus-2  v1 top-up t3 (Rp 250.000, Mandiri) created 50 days ago, v2 paid 50 days ago -> 250.000
//	       v3 top-up t4 (Rp 75.000, QRIS) created 10 min ago, pending (valid for 30 min)
//	       v4 top-up t5 (Rp 20.000, BNI) created 2 h ago, valid for 1 h and not marked expired yet
//	       v5 charged Rp 100.000 for pln-3 -> 150.000
func (r *rig) load(t *testing.T) {
	t.Helper()
	s := r.svc
	pay := func(env contracts.Envelope, h eventbus.Handler) { r.deliver(t, h, env) }

	// today's midnight in Jakarta is 2026-10-20 17:00 UTC
	t1 := topUp("top-1", "cus-1", 100_000, contracts.TopUpMethodQRIS, contracts.TopUpStatusPending, now.Add(-hours(10)), 30*time.Minute, time.Time{})
	pay(feed(t, contracts.TypeTopUpCreated, "cus-1", 1, t1.CreatedAt.Time, contracts.TopUpCreated{TopUp: t1}), s.TopUpCreated)
	t1.Status, t1.PaidAt = contracts.TopUpStatusSucceeded, contracts.At(time.Date(2026, 10, 21, 3, 0, 0, 0, time.UTC))
	pay(feed(t, contracts.TypeTopUpPaid, "cus-1", 2, t1.PaidAt.Time, contracts.TopUpPaid{TopUp: t1, TxID: "wtx-1", BalanceAfterRp: 100_000}), s.TopUpPaid)

	t2 := topUp("top-2", "cus-1", 50_000, contracts.TopUpMethodVABCA, contracts.TopUpStatusPending, time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC), 24*time.Hour, time.Time{})
	pay(feed(t, contracts.TypeTopUpCreated, "cus-1", 3, t2.CreatedAt.Time, contracts.TopUpCreated{TopUp: t2}), s.TopUpCreated)
	t2.Status, t2.PaidAt = contracts.TopUpStatusSucceeded, contracts.At(time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC))
	pay(feed(t, contracts.TypeTopUpPaid, "cus-1", 4, t2.PaidAt.Time, contracts.TopUpPaid{TopUp: t2, TxID: "wtx-2", BalanceAfterRp: 150_000}), s.TopUpPaid)

	pay(feed(t, contracts.TypeWalletCharged, "cus-1", 5, time.Date(2026, 10, 21, 4, 0, 0, 0, time.UTC),
		contracts.WalletCharged{TxID: "wtx-3", CustomerID: "cus-1", PlanID: "pln-1", AmountRp: 40_000, BalanceAfterRp: 110_000}), s.WalletCharged)
	pay(feed(t, contracts.TypeWalletRefunded, "cus-1", 6, time.Date(2026, 10, 21, 5, 0, 0, 0, time.UTC),
		contracts.WalletRefunded{TxID: "wtx-4", CustomerID: "cus-1", PlanID: "pln-1", AmountRp: 40_000, BalanceAfterRp: 150_000}), s.WalletRefunded)
	pay(feed(t, contracts.TypeWalletCharged, "cus-1", 7, time.Date(2026, 10, 21, 6, 0, 0, 0, time.UTC),
		contracts.WalletCharged{TxID: "wtx-5", CustomerID: "cus-1", PlanID: "pln-2", AmountRp: 25_000, BalanceAfterRp: 125_000}), s.WalletCharged)
	t6 := topUp("top-6", "cus-1", 30_000, contracts.TopUpMethodVABRI, contracts.TopUpStatusPending, now.Add(-hours(30)), 24*time.Hour, time.Time{})
	pay(feed(t, contracts.TypeTopUpCreated, "cus-1", 8, t6.CreatedAt.Time, contracts.TopUpCreated{TopUp: t6}), s.TopUpCreated)
	t6.Status = contracts.TopUpStatusExpired
	pay(feed(t, contracts.TypeTopUpExpired, "cus-1", 9, t6.ExpiresAt.Time, contracts.TopUpExpired{TopUp: t6}), s.TopUpExpired)

	paid := now.Add(-50 * 24 * time.Hour)
	t3 := topUp("top-3", "cus-2", 250_000, contracts.TopUpMethodVAMandiri, contracts.TopUpStatusPending, paid.Add(-time.Hour), 24*time.Hour, time.Time{})
	pay(feed(t, contracts.TypeTopUpCreated, "cus-2", 1, t3.CreatedAt.Time, contracts.TopUpCreated{TopUp: t3}), s.TopUpCreated)
	t3.Status, t3.PaidAt = contracts.TopUpStatusSucceeded, contracts.At(paid)
	pay(feed(t, contracts.TypeTopUpPaid, "cus-2", 2, paid, contracts.TopUpPaid{TopUp: t3, TxID: "wtx-6", BalanceAfterRp: 250_000}), s.TopUpPaid)
	t4 := topUp("top-4", "cus-2", 75_000, contracts.TopUpMethodQRIS, contracts.TopUpStatusPending, now.Add(-10*time.Minute), 30*time.Minute, time.Time{})
	pay(feed(t, contracts.TypeTopUpCreated, "cus-2", 3, t4.CreatedAt.Time, contracts.TopUpCreated{TopUp: t4}), s.TopUpCreated)
	t5 := topUp("top-5", "cus-2", 20_000, contracts.TopUpMethodVABNI, contracts.TopUpStatusPending, now.Add(-2*time.Hour), time.Hour, time.Time{})
	pay(feed(t, contracts.TypeTopUpCreated, "cus-2", 4, t5.CreatedAt.Time, contracts.TopUpCreated{TopUp: t5}), s.TopUpCreated)
	pay(feed(t, contracts.TypeWalletCharged, "cus-2", 5, now.Add(-time.Hour),
		contracts.WalletCharged{TxID: "wtx-7", CustomerID: "cus-2", PlanID: "pln-3", AmountRp: 100_000, BalanceAfterRp: 150_000}), s.WalletCharged)

	// the plans the ledger lines name: pln-1 is known, pln-2 and pln-3 have not arrived
	plan := testkit.Plan("pln-1", testkit.Route("pln-1", 1, contracts.VehicleTypeMotor, testkit.Kemang, 5, now))
	if _, err := plans.Insert(t.Context(), r.pool, plan); err != nil {
		t.Fatal(err)
	}
}

func TestOverviewAddsUpWhatTheMockWouldForTheSameData(t *testing.T) {
	r := newRig(t)
	r.load(t)

	got, err := r.svc.Overview(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// balances: 125.000 + 150.000 = 275.000, both customers hold something.
	// pending: only top-4 (Rp 75.000); top-5 ran out an hour ago and top-6 was expired by console-api.
	// paid today (since 17:00 UTC the evening before): top-1 Rp 100.000. Last 30 days: top-1 and top-2, Rp 150.000
	// (top-3 was paid 50 days ago).
	want := Overview{
		TotalBalanceRp: 275_000, UsersWithBalance: 2, PendingCount: 1, PendingAmountRp: 75_000,
		PaidTodayRp: 100_000, PaidTodayCount: 1, Paid30dRp: 150_000, Paid30dCount: 2,
	}
	if got != want {
		t.Errorf("overview = %+v\nwant       %+v", got, want)
	}
}

func TestACancelledTopUpStopsCountingAsPending(t *testing.T) {
	r := newRig(t)
	waiting := topUp("top-9", "cus-1", 60_000, contracts.TopUpMethodQRIS, contracts.TopUpStatusPending, now.Add(-10*time.Minute), 30*time.Minute, time.Time{})
	r.deliver(t, r.svc.TopUpCreated, feed(t, contracts.TypeTopUpCreated, "cus-1", 1, waiting.CreatedAt.Time, contracts.TopUpCreated{TopUp: waiting}))
	if got, err := r.svc.Overview(t.Context()); err != nil || got.PendingCount != 1 || got.PendingAmountRp != 60_000 {
		t.Fatalf("overview before = %+v, %v; want the top-up waiting", got, err)
	}

	waiting.Status = contracts.TopUpStatusCancelled
	cancelled := feed(t, contracts.TypeTopUpCancelled, "cus-1", 2, now.Add(-5*time.Minute), contracts.TopUpCancelled{TopUp: waiting})
	r.deliver(t, r.svc.TopUpCancelled, cancelled)
	r.deliver(t, r.svc.TopUpCancelled, cancelled) // redelivered: the same state

	if got, err := r.svc.Overview(t.Context()); err != nil || got.PendingCount != 0 || got.PendingAmountRp != 0 {
		t.Errorf("overview after = %+v, %v; want nothing waiting", got, err)
	}
	list, err := r.svc.TopUps(t.Context(), TopUpFilter{Status: []contracts.TopUpStatus{contracts.TopUpStatusCancelled}}, platform.LocaleEN)
	if err != nil || len(list) != 1 || list[0].ID != "top-9" || list[0].Status != contracts.TopUpStatusCancelled {
		t.Errorf("cancelled top-ups = %+v, %v", list, err)
	}

	// a Created event that arrives late must not bring the top-up back to waiting
	r.deliver(t, r.svc.TopUpCreated, feed(t, contracts.TypeTopUpCreated, "cus-1", 1, waiting.CreatedAt.Time, contracts.TopUpCreated{TopUp: topUp("top-9", "cus-1", 60_000, contracts.TopUpMethodQRIS, contracts.TopUpStatusPending, now.Add(-10*time.Minute), 30*time.Minute, time.Time{})}))
	if got, _ := r.svc.Overview(t.Context()); got.PendingCount != 0 {
		t.Errorf("a stale top-up.created revived the cancelled top-up: %+v", got)
	}
}

func TestOverviewOfAnEmptyProjectionIsAllZeros(t *testing.T) {
	r := newRig(t)
	got, err := r.svc.Overview(t.Context())
	if err != nil || got != (Overview{}) {
		t.Errorf("overview = %+v, %v; want zeros", got, err)
	}
}

func TestStartOfDayIsMidnightInJakarta(t *testing.T) {
	for _, tt := range []struct{ at, want time.Time }{
		{time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC), time.Date(2026, 10, 20, 17, 0, 0, 0, time.UTC)},
		{time.Date(2026, 10, 20, 16, 59, 59, 0, time.UTC), time.Date(2026, 10, 19, 17, 0, 0, 0, time.UTC)},
		{time.Date(2026, 10, 20, 17, 0, 0, 0, time.UTC), time.Date(2026, 10, 20, 17, 0, 0, 0, time.UTC)},
	} {
		if got := startOfDayWIB(tt.at); !got.Equal(tt.want) {
			t.Errorf("startOfDayWIB(%v) = %v, want %v", tt.at, got, tt.want)
		}
	}
}

func topUpIDs(list []AdminTopUp) []string {
	var out []string
	for _, t := range list {
		out = append(out, t.ID)
	}
	return out
}

func TestTopUpsAreFilteredAndOrderedLikeTheMock(t *testing.T) {
	r := newRig(t)
	r.load(t)
	list := func(f TopUpFilter) []string {
		t.Helper()
		got, err := r.svc.TopUps(t.Context(), f, platform.LocaleEN)
		if err != nil {
			t.Fatal(err)
		}
		return topUpIDs(got)
	}

	// newest created first: top-4 (10 min ago), top-5 (2 h), top-1 (10 h), top-2 (27 h), top-6 (30 h), top-3 (50 days)
	tests := []struct {
		name   string
		filter TopUpFilter
		want   []string
	}{
		{"all, newest first", TopUpFilter{}, []string{"top-4", "top-5", "top-1", "top-2", "top-6", "top-3"}},
		{"paid", TopUpFilter{Status: []contracts.TopUpStatus{contracts.TopUpStatusSucceeded}}, []string{"top-1", "top-2", "top-3"}},
		{"waiting: one past its expiry is no longer waiting", TopUpFilter{Status: []contracts.TopUpStatus{contracts.TopUpStatusPending}}, []string{"top-4"}},
		{"expired: those expired by console-api and those past their expiry", TopUpFilter{Status: []contracts.TopUpStatus{contracts.TopUpStatusExpired}}, []string{"top-5", "top-6"}},
		{"waiting or paid", TopUpFilter{Status: []contracts.TopUpStatus{contracts.TopUpStatusPending, contracts.TopUpStatusSucceeded}}, []string{"top-4", "top-1", "top-2", "top-3"}},
		{"one customer", TopUpFilter{UserID: "cus-2"}, []string{"top-4", "top-5", "top-3"}},
		{"one method", TopUpFilter{Method: contracts.TopUpMethodQRIS}, []string{"top-4", "top-1"}},
		{"by customer name, ignoring case", TopUpFilter{Q: "  AGUS "}, []string{"top-4", "top-5", "top-3"}},
		{"by email", TopUpFilter{Q: "dina@"}, []string{"top-1", "top-2", "top-6"}},
		{"by reference", TopUpFilter{Q: "ref-top-2"}, []string{"top-2"}},
		{"by id", TopUpFilter{Q: "top-6"}, []string{"top-6"}},
		{"filters combine", TopUpFilter{UserID: "cus-1", Method: contracts.TopUpMethodVABCA, Status: []contracts.TopUpStatus{contracts.TopUpStatusSucceeded}}, []string{"top-2"}},
		{"nothing", TopUpFilter{Q: "zzz"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := list(tt.filter); !slices.Equal(got, tt.want) {
				t.Errorf("top-ups = %v, want %v", got, tt.want)
			}
		})
	}

	all, err := r.svc.TopUps(t.Context(), TopUpFilter{UserID: "cus-1", Method: contracts.TopUpMethodQRIS}, platform.LocaleEN)
	if err != nil || len(all) != 1 {
		t.Fatalf("top-ups = %+v, %v", all, err)
	}
	got := all[0]
	if got.UserName != "Dina Pramesti" || got.UserEmail != "dina@contoh.id" || got.Status != contracts.TopUpStatusSucceeded ||
		got.AmountRp != 100_000 || got.Reference != "REF-top-1" || got.PaidAt.IsZero() {
		t.Errorf("top-up = %+v, want the customer's name and email on the top-up, paid", got)
	}
	body, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"id", "userId", "amountRp", "method", "status", "createdAt", "expiresAt", "paidAt", "reference", "userName", "userEmail"} {
		if _, ok := wire[field]; !ok {
			t.Errorf("wire shape lacks %q: %s", field, body)
		}
	}
}

func TestATopUpOfAnUnknownCustomerShowsTheStandInName(t *testing.T) {
	r := newRig(t)
	ghost := topUp("top-9", "cus-gone", 10_000, contracts.TopUpMethodQRIS, contracts.TopUpStatusPending, now, time.Hour, time.Time{})
	r.deliver(t, r.svc.TopUpCreated, feed(t, contracts.TypeTopUpCreated, "cus-gone", 1, now, contracts.TopUpCreated{TopUp: ghost}))
	for locale, want := range map[platform.Locale]string{platform.LocaleEN: "Deleted user", platform.LocaleID: "Pengguna dihapus"} {
		got, err := r.svc.TopUps(t.Context(), TopUpFilter{}, locale)
		if err != nil || len(got) != 1 || got[0].UserName != want || got[0].UserEmail != "" {
			t.Errorf("%s: top-ups = %+v, %v; want the name %q and no email", locale, got, err, want)
		}
	}
}

func TestTransactionsAreTheLedgerNewestFirst(t *testing.T) {
	r := newRig(t)
	r.load(t)

	got, err := r.svc.Transactions(t.Context(), "cus-1")
	if err != nil {
		t.Fatal(err)
	}
	type line struct {
		id, kind string
		amount   int64
		after    int64
	}
	var lines []line
	for _, tx := range got {
		lines = append(lines, line{tx.ID, tx.Kind, tx.AmountRp, tx.BalanceAfterRp})
	}
	// newest first by the time of the transaction: wtx-1 was paid at 03:00 UTC on the 21st, wtx-2 on the 20th
	want := []line{
		{"wtx-5", "pembayaran", -25_000, 125_000}, {"wtx-4", "refund", 40_000, 150_000}, {"wtx-3", "pembayaran", -40_000, 110_000},
		{"wtx-1", "topup", 100_000, 100_000}, {"wtx-2", "topup", 50_000, 150_000},
	}
	if !slices.Equal(lines, want) {
		t.Fatalf("ledger = %v\nwant     %v", lines, want)
	}

	byID := map[string]Tx{}
	for _, tx := range got {
		byID[tx.ID] = tx
	}
	if tx := byID["wtx-1"]; tx.Title.Key != "srv.ops.wallet.topUp" || tx.TopUpID != "top-1" || tx.PlanID != "" {
		t.Errorf("top-up line = %+v", tx)
	} else if m, _ := tx.Title.Params["method"].(contracts.Msg); m.Key != "dom.topupMethod.qris" {
		t.Errorf("top-up title method = %+v, want the nested method message", tx.Title.Params["method"])
	}
	if tx := byID["wtx-3"]; tx.Title.Key != "srv.ops.wallet.payment" || tx.Title.Params["code"] != "PL-pln-1" || tx.PlanID != "pln-1" || tx.UserID != "cus-1" {
		t.Errorf("payment line = %+v, want the plan's code (the plan is projected)", tx)
	}
	if tx := byID["wtx-4"]; tx.Title.Key != "srv.ops.wallet.refund" || tx.Title.Params["code"] != "PL-pln-1" {
		t.Errorf("refund line = %+v", tx)
	}
	if tx := byID["wtx-5"]; tx.Title.Params["code"] != "pln-2" {
		t.Errorf("payment of a plan not projected yet: %+v, want the plan id as a stand-in", tx.Title)
	}

	other, err := r.svc.Transactions(t.Context(), "cus-2")
	if err != nil || len(other) != 2 || other[0].ID != "wtx-7" || other[1].ID != "wtx-6" {
		t.Errorf("cus-2 ledger = %+v, %v; want only his own two lines", other, err)
	}
	if _, err := r.svc.Transactions(t.Context(), "cus-404"); !isNotFound(err) {
		t.Errorf("unknown customer: %v, want not found", err)
	}
}

func isNotFound(err error) bool {
	apiErr, ok := errors.AsType[*platform.APIError](err)
	return ok && apiErr.Code == platform.CodeNotFound
}

func TestTheLedgerKeepsTheLatestTwoHundredLines(t *testing.T) {
	r := newRig(t)
	for i := int64(1); i <= 205; i++ {
		id := fmt.Sprintf("wtx-%03d", i)
		r.deliver(t, r.svc.WalletCharged, feed(t, contracts.TypeWalletCharged, "cus-1", i, now.Add(time.Duration(i)*time.Second),
			contracts.WalletCharged{TxID: id, CustomerID: "cus-1", PlanID: "pln-x", AmountRp: 1, BalanceAfterRp: 1000 - i}))
	}
	got, err := r.svc.Transactions(t.Context(), "cus-1")
	if err != nil || len(got) != 200 {
		t.Fatalf("%d lines, %v; want 200", len(got), err)
	}
	if got[0].BalanceAfterRp != 1000-205 || got[199].BalanceAfterRp != 1000-6 {
		t.Errorf("lines run from balance %d to %d, want the latest 200 (after 205 down to 6)", got[0].BalanceAfterRp, got[199].BalanceAfterRp)
	}
}

func balanceOf(t *testing.T, r *rig, customer string) (balance, version int64) {
	t.Helper()
	err := r.pool.QueryRow(t.Context(), `SELECT balance_rp, version FROM wallet_balances WHERE customer_id = $1`, customer).Scan(&balance, &version)
	if err != nil {
		t.Fatal(err)
	}
	return balance, version
}

func ledgerLines(t *testing.T, r *rig, customer string) int {
	t.Helper()
	var n int
	if err := r.pool.QueryRow(t.Context(), `SELECT count(*) FROM wallet_entries WHERE customer_id = $1`, customer).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestWalletEventsAreAppliedOnceAndInVersionOrder(t *testing.T) {
	r := newRig(t)
	r.load(t)
	if bal, version := balanceOf(t, r, "cus-1"); bal != 125_000 || version != 7 {
		t.Fatalf("balance %d at version %d, want 125.000 at 7 (the top-up events after it do not move it)", bal, version)
	}
	lines := ledgerLines(t, r, "cus-1")

	// the same event again: nothing moves
	again := feed(t, contracts.TypeWalletCharged, "cus-1", 5, time.Date(2026, 10, 21, 4, 0, 0, 0, time.UTC),
		contracts.WalletCharged{TxID: "wtx-3", CustomerID: "cus-1", PlanID: "pln-1", AmountRp: 40_000, BalanceAfterRp: 110_000})
	r.deliver(t, r.svc.WalletCharged, again)
	if bal, version := balanceOf(t, r, "cus-1"); bal != 125_000 || version != 7 || ledgerLines(t, r, "cus-1") != lines {
		t.Errorf("a replayed charge changed the projection: balance %d at %d, %d lines (was %d)", bal, version, ledgerLines(t, r, "cus-1"), lines)
	}

	// a late event of an older version still enters the ledger (once) but cannot turn the balance back
	late := feed(t, contracts.TypeWalletCharged, "cus-1", 2, time.Date(2026, 10, 20, 11, 0, 0, 0, time.UTC),
		contracts.WalletCharged{TxID: "wtx-late", CustomerID: "cus-1", PlanID: "pln-9", AmountRp: 5_000, BalanceAfterRp: 95_000})
	r.deliver(t, r.svc.WalletCharged, late)
	r.deliver(t, r.svc.WalletCharged, feed(t, contracts.TypeWalletCharged, "cus-1", 2, time.Date(2026, 10, 20, 11, 0, 0, 0, time.UTC),
		contracts.WalletCharged{TxID: "wtx-late", CustomerID: "cus-1", PlanID: "pln-9", AmountRp: 5_000, BalanceAfterRp: 95_000})) // same transaction, another envelope
	if bal, version := balanceOf(t, r, "cus-1"); bal != 125_000 || version != 7 {
		t.Errorf("balance %d at version %d after a late event, want it unchanged", bal, version)
	}
	if got := ledgerLines(t, r, "cus-1"); got != lines+1 {
		t.Errorf("%d ledger lines, want the late transaction once (%d)", got, lines+1)
	}

	// a top-up's state follows versions too: the paid state is not undone by a late "created"
	staleCreated := topUp("top-1", "cus-1", 100_000, contracts.TopUpMethodQRIS, contracts.TopUpStatusPending, now.Add(-hours(10)), 30*time.Minute, time.Time{})
	r.deliver(t, r.svc.TopUpCreated, feed(t, contracts.TypeTopUpCreated, "cus-1", 1, now, contracts.TopUpCreated{TopUp: staleCreated}))
	paid, err := r.svc.TopUps(t.Context(), TopUpFilter{Q: "top-1"}, platform.LocaleEN)
	if err != nil || len(paid) != 1 || paid[0].Status != contracts.TopUpStatusSucceeded {
		t.Errorf("top-up = %+v, %v; want it still paid", paid, err)
	}
}

func TestATopUpPaidWithoutATransactionIdGetsALineOfItsOwn(t *testing.T) {
	r := newRig(t)
	t1 := topUp("top-1", "cus-1", 40_000, contracts.TopUpMethodQRIS, contracts.TopUpStatusSucceeded, now.Add(-time.Hour), time.Hour, now)
	r.deliver(t, r.svc.WalletCharged, feed(t, contracts.TypeWalletCharged, "cus-1", 1, now.Add(-2*time.Hour),
		contracts.WalletCharged{TxID: "wtx-1", CustomerID: "cus-1", PlanID: "pln-1", AmountRp: 0, BalanceAfterRp: 10_000}))
	paid := feed(t, contracts.TypeTopUpPaid, "cus-1", 2, now, contracts.TopUpPaid{TopUp: t1})
	r.deliver(t, r.svc.TopUpPaid, paid)
	r.deliver(t, r.svc.TopUpPaid, paid)

	got, err := r.svc.Transactions(t.Context(), "cus-1")
	if err != nil || len(got) != 2 {
		t.Fatalf("ledger = %+v, %v", got, err)
	}
	if got[0].Kind != "topup" || got[0].AmountRp != 40_000 || got[0].BalanceAfterRp != 50_000 {
		t.Errorf("top-up line = %+v, want +40.000 on the held balance of 10.000", got[0])
	}
}

func TestMalformedWalletEventsAreDeadLetteredNotRetried(t *testing.T) {
	r := newRig(t)
	for name, tc := range map[string]struct {
		h    eventbus.Handler
		data any
	}{
		"a top-up without an id":   {r.svc.TopUpCreated, contracts.TopUpCreated{TopUp: contracts.TopUp{UserID: "cus-1"}}},
		"a paid top-up of nobody":  {r.svc.TopUpPaid, contracts.TopUpPaid{TopUp: contracts.TopUp{ID: "top-1"}}},
		"a charge without its id":  {r.svc.WalletCharged, contracts.WalletCharged{CustomerID: "cus-1"}},
		"a refund without a buyer": {r.svc.WalletRefunded, contracts.WalletRefunded{TxID: "wtx-1"}},
		"an undecodable payload":   {r.svc.TopUpExpired, struct{ TopUp string }{"x"}},
		"an undecodable cancel":    {r.svc.TopUpCancelled, struct{ TopUp string }{"x"}},
	} {
		t.Run(name, func(t *testing.T) {
			err := testkit.Apply(r.pool, tc.h, "wallet.test", "cus-1", 1, tc.data)
			if err == nil || !isPermanent(err) {
				t.Errorf("error = %v, want a permanent one", err)
			}
		})
	}
}

// isPermanent reports whether the handler marked the event as one that retrying cannot fix (eventbus.Permanent).
func isPermanent(err error) bool { return fmt.Sprintf("%T", err) == "eventbus.permanentError" }
