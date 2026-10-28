package users

import (
	"encoding/json/v2"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/changes"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/finance"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/plans"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/testkit"
)

// These tests need a real Postgres (TEST_DATABASE_URL) and skip without one.

var t0 = time.Date(2026, time.October, 21, 3, 0, 0, 0, time.UTC)

type rig struct {
	pool       *pgxpool.Pool
	svc        *Service
	projection *plans.Projection
	finance    *finance.Service
}

func newRig(t *testing.T) *rig {
	t.Helper()
	pool := testkit.Pool(t)
	feed, pub := changes.New(platform.NewHub(), time.Millisecond), testkit.Publisher(pool)
	plansSvc := plans.NewService(pool, pub, feed, time.Now)
	return &rig{
		pool: pool, svc: New(pool, pub, feed, plansSvc),
		projection: plans.NewProjection(feed, pub), finance: finance.New(pool, feed, time.Now),
	}
}

func (r *rig) deliver(t *testing.T, h eventbus.Handler, eventType, key string, version int64, data any) {
	t.Helper()
	if err := testkit.Apply(r.pool, h, eventType, key, version, data); err != nil {
		t.Fatalf("%s: %v", eventType, err)
	}
}

func (r *rig) customer(t *testing.T, id, name, email, phone string, created time.Time) {
	t.Helper()
	r.deliver(t, r.projection.CustomerRegistered, contracts.TypeCustomerRegistered, id, 1,
		contracts.CustomerRegistered{CustomerID: id, Name: name, Email: email, Phone: phone, CreatedAt: contracts.At(created)})
}

// plan projects a confirmed plan of the customer with the status and cost, created at the time.
func (r *rig) plan(t *testing.T, id, customer string, status contracts.PlanStatus, costRp int64, created time.Time) {
	t.Helper()
	plan := testkit.Plan(id, testkit.Route(id, 1, contracts.VehicleTypeMotor, testkit.Kemang, 5, t0))
	plan.UserID, plan.TotalCostRp, plan.CreatedAt, plan.UpdatedAt = customer, costRp, contracts.At(created), contracts.At(created)
	r.deliver(t, r.projection.PlanConfirmed, contracts.TypePlanConfirmed, id, 1, contracts.PlanConfirmed{Plan: plan})
	if status == contracts.PlanStatusAwaitingDriver {
		return
	}
	err := platform.WithTx(t.Context(), r.pool, func(tx pgx.Tx) error {
		a, err := plans.Lock(t.Context(), tx, id)
		if err != nil {
			return err
		}
		a.Plan.Status = status
		return plans.Save(t.Context(), tx, a)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (r *rig) balance(t *testing.T, customer string, balanceRp int64, version int64) {
	t.Helper()
	r.deliver(t, r.finance.WalletRefunded, contracts.TypeWalletRefunded, customer, version, contracts.WalletRefunded{
		TxID: "wtx-" + customer, CustomerID: customer, PlanID: "pln-x", AmountRp: balanceRp, BalanceAfterRp: balanceRp,
	})
}

// story: three customers.
//
//	cus-1 Dina Pramesti   plans: p1 finished Rp 40.000, p2 finished Rp 20.000, p3 waiting Rp 20.000, p4 cancelled Rp 20.000
//	                      -> 4 plans, 2 finished, spend Rp 60.000, last plan = p3's creation; wallet Rp 125.000
//	cus-2 Agus Salim      plans: p5 finished Rp 20.000 -> 1 plan, 1 finished, spend Rp 20.000; empty wallet
//	cus-3 Budi Santoso    no plans, wallet Rp 5.000
func (r *rig) story(t *testing.T) {
	t.Helper()
	r.customer(t, "cus-1", "Dina Pramesti", "dina@contoh.id", "081234567890", t0.Add(-72*time.Hour))
	r.customer(t, "cus-2", "Agus Salim", "agus@contoh.id", "081298765432", t0.Add(-48*time.Hour))
	r.customer(t, "cus-3", "Budi Santoso", "budi@contoh.id", "081311112222", t0.Add(-24*time.Hour))
	r.plan(t, "pln-1", "cus-1", contracts.PlanStatusDone, 40_000, t0.Add(-10*time.Hour))
	r.plan(t, "pln-2", "cus-1", contracts.PlanStatusDone, 20_000, t0.Add(-9*time.Hour))
	r.plan(t, "pln-3", "cus-1", contracts.PlanStatusAwaitingDriver, 20_000, t0.Add(-1*time.Hour))
	r.plan(t, "pln-4", "cus-1", contracts.PlanStatusCancelled, 20_000, t0.Add(-5*time.Hour))
	r.plan(t, "pln-5", "cus-2", contracts.PlanStatusDone, 20_000, t0.Add(-8*time.Hour))
	r.balance(t, "cus-1", 125_000, 1)
	r.balance(t, "cus-3", 5_000, 1)
}

func TestListSummarisesEachCustomerAndOrdersBySpend(t *testing.T) {
	r := newRig(t)
	r.story(t)

	got, err := r.svc.List(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != "cus-1" || got[1].ID != "cus-2" || got[2].ID != "cus-3" {
		t.Fatalf("users = %+v, want Dina (Rp 60.000), Agus (Rp 20.000), Budi (nothing) in that order", got)
	}
	dina, agus, budi := got[0], got[1], got[2]
	if dina.PlansCount != 4 || dina.CompletedCount != 2 || dina.TotalSpendRp != 60_000 || dina.BalanceRp != 125_000 || dina.Blocked {
		t.Errorf("Dina = %+v, want 4 plans, 2 finished, spend 60.000 (only finished plans), balance 125.000", dina)
	}
	if want := t0.Add(-1 * time.Hour); !dina.LastPlanAt.Equal(want) {
		t.Errorf("Dina's last plan %v, want the creation of the newest, %v", dina.LastPlanAt, want)
	}
	if dina.Name != "Dina Pramesti" || dina.Email != "dina@contoh.id" || dina.Phone != "081234567890" || !dina.CreatedAt.Equal(t0.Add(-72*time.Hour)) {
		t.Errorf("Dina's identity = %+v", dina)
	}
	if agus.PlansCount != 1 || agus.CompletedCount != 1 || agus.TotalSpendRp != 20_000 || agus.BalanceRp != 0 {
		t.Errorf("Agus = %+v, want 1 plan, 1 finished, spend 20.000, nothing in the wallet", agus)
	}
	if budi.PlansCount != 0 || budi.TotalSpendRp != 0 || budi.BalanceRp != 5_000 || !budi.LastPlanAt.IsZero() {
		t.Errorf("Budi = %+v, want no plans, no last plan, 5.000 in the wallet", budi)
	}

	body, err := json.Marshal(budi)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if _, ok := wire["lastPlanAt"]; ok {
		t.Errorf("lastPlanAt is on the wire for a customer without plans: %s", body)
	}
	for _, f := range []string{"id", "name", "email", "phone", "createdAt", "blocked", "plansCount", "completedCount", "totalSpendRp", "balanceRp"} {
		if _, ok := wire[f]; !ok {
			t.Errorf("UserSummary lacks %q: %s", f, body)
		}
	}
}

func TestListSearchesNameEmailAndPhone(t *testing.T) {
	r := newRig(t)
	r.story(t)
	ids := func(q string) []string {
		t.Helper()
		got, err := r.svc.List(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, u := range got {
			out = append(out, u.ID)
		}
		return out
	}
	for q, want := range map[string][]string{
		"  DINA ":       {"cus-1"},
		"salim":         {"cus-2"},
		"budi@contoh":   {"cus-3"},
		"0813":          {"cus-3"},
		"contoh.id":     {"cus-1", "cus-2", "cus-3"},
		"%":             {},
		"no such thing": {},
	} {
		if got := ids(q); !slices.Equal(got, want) {
			t.Errorf("q=%q: %v, want %v", q, got, want)
		}
	}
}

func TestGetReturnsTheUserTheSummaryAndHisPlansNewestFirst(t *testing.T) {
	r := newRig(t)
	r.story(t)

	got, err := r.svc.Get(t.Context(), "cus-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.User.ID != "cus-1" || got.User.Email != "dina@contoh.id" || got.User.Blocked || got.User.SavedAddresses == nil || len(got.User.SavedAddresses) != 0 {
		t.Errorf("user = %+v, want him with no saved addresses (console-api owns those) as an empty list", got.User)
	}
	if got.Summary.PlansCount != 4 || got.Summary.TotalSpendRp != 60_000 {
		t.Errorf("summary = %+v", got.Summary)
	}
	var ids []string
	for _, p := range got.Plans {
		ids = append(ids, p.ID)
		if p.CustomerName != "Dina Pramesti" {
			t.Errorf("plan row %s names %q", p.ID, p.CustomerName)
		}
	}
	if want := []string{"pln-3", "pln-4", "pln-2", "pln-1"}; !slices.Equal(ids, want) {
		t.Errorf("plans = %v, want them newest created first %v", ids, want)
	}
	body, err := json.Marshal(got.User)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if list, ok := wire["savedAddresses"].([]any); !ok || len(list) != 0 {
		t.Errorf("savedAddresses on the wire = %v, want []", wire["savedAddresses"])
	}

	if _, err := r.svc.Get(t.Context(), "cus-404"); !isStatus(err, platform.CodeNotFound) {
		t.Errorf("unknown user: %v, want not found", err)
	}
}

func isStatus(err error, code platform.ErrorCode) bool {
	apiErr, ok := errors.AsType[*platform.APIError](err)
	return ok && apiErr.Code == code
}

func TestSetBlockedAnnouncesOnlyChangesAndNumbersThemItself(t *testing.T) {
	r := newRig(t)
	r.story(t)

	got, err := r.svc.SetBlocked(t.Context(), "cus-1", true)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Blocked || got.ID != "cus-1" || got.PlansCount != 4 {
		t.Fatalf("answer = %+v, want Dina's summary, blocked", got)
	}
	blocked := testkit.Outbox(t, r.pool, contracts.TypeCustomerBlocked)
	if len(blocked) != 1 || blocked[0].Key != "cus-1" || blocked[0].AggregateVersion != 1 {
		t.Fatalf("customer.blocked = %+v, want one for Dina at its own version 1", blocked)
	}
	if ev, _ := blocked[0].Payload[contracts.CustomerBlocked](); ev.CustomerID != "cus-1" {
		t.Errorf("payload = %+v", ev)
	}
	if after, err := r.svc.Get(t.Context(), "cus-1"); err != nil || !after.User.Blocked || !after.Summary.Blocked {
		t.Errorf("user after blocking = %+v, %v; want the projection to say blocked", after.User, err)
	}

	// blocking the blocked changes and announces nothing
	if _, err := r.svc.SetBlocked(t.Context(), "cus-1", true); err != nil {
		t.Fatal(err)
	}
	if n := len(testkit.Outbox(t, r.pool, contracts.TypeCustomerBlocked)); n != 1 {
		t.Errorf("%d customer.blocked after blocking twice, want one", n)
	}

	got, err = r.svc.SetBlocked(t.Context(), "cus-1", false)
	if err != nil || got.Blocked {
		t.Fatalf("unblock: %+v, %v", got, err)
	}
	unblocked := testkit.Outbox(t, r.pool, contracts.TypeCustomerUnblocked)
	if len(unblocked) != 1 || unblocked[0].AggregateVersion != 2 {
		t.Fatalf("customer.unblocked = %+v, want one at version 2: blocking and unblocking share a counter", unblocked)
	}
	if _, err := r.svc.SetBlocked(t.Context(), "cus-1", false); err != nil {
		t.Fatal(err)
	}
	if n := len(testkit.Outbox(t, r.pool, contracts.TypeCustomerUnblocked)); n != 1 {
		t.Errorf("%d customer.unblocked after unblocking twice, want one", n)
	}

	if _, err := r.svc.SetBlocked(t.Context(), "cus-404", true); !isStatus(err, platform.CodeNotFound) {
		t.Errorf("unknown user: %v, want not found", err)
	}
}

func TestABlockSurvivesTheCustomersLaterUpdates(t *testing.T) {
	r := newRig(t)
	r.story(t)
	if _, err := r.svc.SetBlocked(t.Context(), "cus-2", true); err != nil {
		t.Fatal(err)
	}
	r.deliver(t, r.projection.CustomerUpdated, contracts.TypeCustomerUpdated, "cus-2", 2,
		contracts.CustomerUpdated{CustomerID: "cus-2", Name: "Agus S.", Email: "agus.s@contoh.id", Phone: "0800"})
	r.deliver(t, r.projection.CustomerRegistered, contracts.TypeCustomerRegistered, "cus-2", 1,
		contracts.CustomerRegistered{CustomerID: "cus-2", Name: "Agus Salim"})

	got, err := r.svc.Get(t.Context(), "cus-2")
	if err != nil {
		t.Fatal(err)
	}
	if !got.User.Blocked || got.User.Name != "Agus S." || got.User.Email != "agus.s@contoh.id" {
		t.Errorf("user = %+v, want the new contact data and still blocked", got.User)
	}
}
