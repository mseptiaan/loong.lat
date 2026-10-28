// Package finance is the admin console's view of the money in the customers' wallets. console-api owns the
// wallets; this package keeps a projection of what it announces on the wallet topic (balances, the ledger and
// the top-ups) and answers finance.overview, finance.topUps and finance.transactions from it.
//
// Wallet events are keyed by customer and numbered by console-api's per-wallet counter. A ledger line is written
// once whatever the order (its id is the wallet transaction's), and a balance or a top-up's state is replaced
// only by an event with a higher version, so a duplicate or a late arrival changes nothing.
package finance

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/apix"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/changes"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/pg"
)

// Message keys of the frontend catalog.
const (
	msgUserNotFound = "srv.ops.err.userNotFound"
	msgTopUp        = "srv.ops.wallet.topUp"
	msgPayment      = "srv.ops.wallet.payment"
	msgRefund       = "srv.ops.wallet.refund"
	msgTopUpMethod  = "dom.topupMethod."
)

// Ledger line kinds, the frontend's WalletTxKind.
const (
	kindTopUp   = "topup"
	kindPayment = "pembayaran"
	kindRefund  = "refund"
)

// transactionsMax is how many ledger lines of a customer are served.
const transactionsMax = 200

const (
	day = 24 * time.Hour
	// wib is the offset of Western Indonesia Time, whose midnight starts "today" in the overview.
	wib = 7 * time.Hour
)

// deletedUser is the name shown for a top-up whose customer is not (or no longer) known: the catalog text
// srv.ops.wallet.deletedUser in the language of the request, as the field is a plain string.
var deletedUser = map[platform.Locale]string{platform.LocaleEN: "Deleted user", platform.LocaleID: "Pengguna dihapus"}

// Overview is the frontend's FinanceOverview.
type Overview struct {
	// TotalBalanceRp is what the customers hold in their wallets: money of theirs that the platform keeps.
	TotalBalanceRp   int64 `json:"totalBalanceRp"`
	UsersWithBalance int   `json:"usersWithBalance"`
	PendingCount     int   `json:"pendingCount"`
	PendingAmountRp  int64 `json:"pendingAmountRp"`
	// PaidTodayRp counts the top-ups paid since midnight WIB.
	PaidTodayRp    int64 `json:"paidTodayRp"`
	PaidTodayCount int   `json:"paidTodayCount"`
	// Paid30dRp counts the top-ups paid in the last 30 days.
	Paid30dRp    int64 `json:"paid30dRp"`
	Paid30dCount int   `json:"paid30dCount"`
}

// AdminTopUp is the frontend's AdminTopUp: a top-up with the customer it belongs to.
type AdminTopUp struct {
	contracts.TopUp
	UserName  string `json:"userName"`
	UserEmail string `json:"userEmail"`
}

// TopUpFilter is the frontend's TopUpFilter.
type TopUpFilter struct {
	Status []contracts.TopUpStatus `json:"status"`
	UserID string                  `json:"userId"`
	Method contracts.TopUpMethod   `json:"method"`
	// Q matches the customer's name or email, the reference (virtual account or code) or the top-up's id.
	Q string `json:"q"`
}

// Tx is the frontend's WalletTx: one line of a customer's ledger.
type Tx struct {
	ID             string         `json:"id"`
	UserID         string         `json:"userId"`
	At             contracts.Time `json:"at"`
	Kind           string         `json:"kind"`
	AmountRp       int64          `json:"amountRp"`
	BalanceAfterRp int64          `json:"balanceAfterRp"`
	Title          contracts.Msg  `json:"title"`
	PlanID         string         `json:"planId,omitempty"`
	TopUpID        string         `json:"topUpId,omitempty"`
}

// Service keeps the wallet projection and serves the finance methods.
type Service struct {
	pool *pgxpool.Pool
	feed *changes.Feed
	now  func() time.Time
}

func New(pool *pgxpool.Pool, feed *changes.Feed, now func() time.Time) *Service {
	return &Service{pool: pool, feed: feed, now: now}
}

/* ---------- the projection (loong.wallet.v1) ---------- */

// TopUpCreated (loong.wallet.v1) records a new top-up waiting for payment.
func (s *Service) TopUpCreated(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.TopUpCreated]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	return s.topUpChanged(ctx, tx, env, ev.TopUp)
}

// TopUpExpired (loong.wallet.v1) records that a top-up was not paid in time.
func (s *Service) TopUpExpired(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.TopUpExpired]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	return s.topUpChanged(ctx, tx, env, ev.TopUp)
}

// TopUpCancelled (loong.wallet.v1) records a top-up the customer withdrew before paying.
func (s *Service) TopUpCancelled(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.TopUpCancelled]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	return s.topUpChanged(ctx, tx, env, ev.TopUp)
}

func (s *Service) topUpChanged(ctx context.Context, tx pgx.Tx, env contracts.Envelope, t contracts.TopUp) error {
	if err := validTopUp(env.Type, t); err != nil {
		return eventbus.Permanent(err)
	}
	changed, err := upsertTopUp(ctx, tx, t, env.AggregateVersion)
	if changed {
		s.feed.Wallet()
	}
	return err
}

func validTopUp(eventType string, t contracts.TopUp) error {
	if t.ID == "" || t.UserID == "" {
		return fmt.Errorf("%s: top-up without id or customer", eventType)
	}
	return nil
}

// TopUpPaid (loong.wallet.v1) records the payment: the top-up, its ledger line (the wallet transaction it made,
// with the balance after it) and the new balance.
func (s *Service) TopUpPaid(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.TopUpPaid]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	t := ev.TopUp
	if err := validTopUp(env.Type, t); err != nil {
		return eventbus.Permanent(err)
	}
	if _, err := upsertTopUp(ctx, tx, t, env.AggregateVersion); err != nil {
		return err
	}

	// A producer that does not number the transaction gets a line of its own per top-up, and the balance it
	// leaves is worked out from the one held.
	id, balanceAfter := ev.TxID, ev.BalanceAfterRp
	if id == "" {
		id = "wtx-" + t.ID
		if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT balance_rp FROM wallet_balances WHERE customer_id = $1), 0) + $2`, t.UserID, t.AmountRp).Scan(&balanceAfter); err != nil {
			return fmt.Errorf("finance: balance of %s: %w", t.UserID, err)
		}
	}
	at := env.OccurredAt.Time
	if !t.PaidAt.IsZero() {
		at = t.PaidAt.Time
	}
	line := ledgerLine{id: id, customerID: t.UserID, at: at, kind: kindTopUp, amountRp: t.AmountRp, balanceAfterRp: balanceAfter, topUpID: t.ID, topUpMethod: string(t.Method)}
	return s.post(ctx, tx, env, line)
}

// WalletCharged (loong.wallet.v1) records a plan paid from the wallet.
func (s *Service) WalletCharged(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.WalletCharged]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	if ev.TxID == "" || ev.CustomerID == "" {
		return eventbus.Permanent(fmt.Errorf("%s: no transaction or customer id", env.Type))
	}
	return s.post(ctx, tx, env, ledgerLine{
		id: ev.TxID, customerID: ev.CustomerID, at: env.OccurredAt.Time, kind: kindPayment,
		amountRp: -ev.AmountRp, balanceAfterRp: ev.BalanceAfterRp, planID: ev.PlanID,
	})
}

// WalletRefunded (loong.wallet.v1) records a charge returned to the wallet.
func (s *Service) WalletRefunded(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.WalletRefunded]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	if ev.TxID == "" || ev.CustomerID == "" {
		return eventbus.Permanent(fmt.Errorf("%s: no transaction or customer id", env.Type))
	}
	return s.post(ctx, tx, env, ledgerLine{
		id: ev.TxID, customerID: ev.CustomerID, at: env.OccurredAt.Time, kind: kindRefund,
		amountRp: ev.AmountRp, balanceAfterRp: ev.BalanceAfterRp, planID: ev.PlanID,
	})
}

// ledgerLine is a wallet transaction as it is stored.
type ledgerLine struct {
	id, customerID string
	at             time.Time
	kind           string
	amountRp       int64
	balanceAfterRp int64
	planID         string
	topUpID        string
	topUpMethod    string
}

// post writes the ledger line (once: a line with that id is left as it is) and moves the balance to the one the
// transaction left, unless a newer event set it already.
func (s *Service) post(ctx context.Context, tx pgx.Tx, env contracts.Envelope, l ledgerLine) error {
	if _, err := tx.Exec(ctx,
		`INSERT INTO wallet_entries (id, customer_id, at, kind, amount_rp, balance_after_rp, plan_id, topup_id, topup_method, version)
		 VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''), NULLIF($9, ''), $10)
		 ON CONFLICT (id) DO NOTHING`,
		l.id, l.customerID, l.at, l.kind, l.amountRp, l.balanceAfterRp, l.planID, l.topUpID, l.topUpMethod, env.AggregateVersion); err != nil {
		return fmt.Errorf("finance: ledger line %s: %w", l.id, err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO wallet_balances (customer_id, balance_rp, version) VALUES ($1, $2, $3)
		 ON CONFLICT (customer_id) DO UPDATE SET balance_rp = EXCLUDED.balance_rp, version = EXCLUDED.version
		 WHERE wallet_balances.version < EXCLUDED.version`,
		l.customerID, l.balanceAfterRp, env.AggregateVersion); err != nil {
		return fmt.Errorf("finance: balance of %s: %w", l.customerID, err)
	}
	s.feed.Wallet()
	return nil
}

// upsertTopUp stores the top-up, or its new state when the event is newer than the one held.
func upsertTopUp(ctx context.Context, tx pgx.Tx, t contracts.TopUp, version int64) (bool, error) {
	var paidAt *time.Time
	if !t.PaidAt.IsZero() {
		paidAt = &t.PaidAt.Time
	}
	tag, err := tx.Exec(ctx,
		`INSERT INTO topups (id, customer_id, amount_rp, method, status, created_at, expires_at, paid_at, reference, version)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 ON CONFLICT (id) DO UPDATE SET status = EXCLUDED.status, paid_at = EXCLUDED.paid_at, expires_at = EXCLUDED.expires_at, version = EXCLUDED.version
		 WHERE topups.version < EXCLUDED.version`,
		t.ID, t.UserID, t.AmountRp, string(t.Method), string(t.Status), t.CreatedAt.Time, t.ExpiresAt.Time, paidAt, t.Reference, version)
	if err != nil {
		return false, fmt.Errorf("finance: top-up %s: %w", t.ID, err)
	}
	return tag.RowsAffected() > 0, nil
}

/* ---------- the methods ---------- */

// Mount registers finance.overview, finance.topUps and finance.transactions, all for admins.
func (s *Service) Mount(rpc *platform.RPC) {
	type topUpsIn struct {
		Filter TopUpFilter `json:"filter"`
	}
	type txIn struct {
		UserID string `json:"userId"`
	}
	rpc.Handle("finance.overview", apix.Admin(func(ctx context.Context, _ struct{}) (Overview, error) {
		return s.Overview(ctx)
	}))
	rpc.Handle("finance.topUps", apix.Admin(func(ctx context.Context, in topUpsIn) ([]AdminTopUp, error) {
		return s.TopUps(ctx, in.Filter, platform.LocaleFrom(ctx))
	}))
	rpc.Handle("finance.transactions", apix.Admin(func(ctx context.Context, in txIn) ([]Tx, error) {
		return s.Transactions(ctx, in.UserID)
	}))
}

// Overview sums what the wallets hold and the top-ups that wait or were paid. A top-up past its expiry is not
// pending even when its expired event has not arrived yet.
func (s *Service) Overview(ctx context.Context) (Overview, error) {
	now := s.now()
	today := startOfDayWIB(now)
	last30 := now.Add(-30 * day)
	var o Overview
	err := s.pool.QueryRow(ctx,
		`SELECT
		   COALESCE((SELECT sum(balance_rp) FROM wallet_balances), 0)::bigint,
		   (SELECT count(*) FROM wallet_balances WHERE balance_rp > 0),
		   count(*) FILTER (WHERE status = 'menunggu' AND expires_at > $1),
		   COALESCE(sum(amount_rp) FILTER (WHERE status = 'menunggu' AND expires_at > $1), 0)::bigint,
		   COALESCE(sum(amount_rp) FILTER (WHERE status = 'berhasil' AND paid_at >= $2), 0)::bigint,
		   count(*) FILTER (WHERE status = 'berhasil' AND paid_at >= $2),
		   COALESCE(sum(amount_rp) FILTER (WHERE status = 'berhasil' AND paid_at >= $3), 0)::bigint,
		   count(*) FILTER (WHERE status = 'berhasil' AND paid_at >= $3)
		 FROM topups`, now, today, last30).
		Scan(&o.TotalBalanceRp, &o.UsersWithBalance, &o.PendingCount, &o.PendingAmountRp,
			&o.PaidTodayRp, &o.PaidTodayCount, &o.Paid30dRp, &o.Paid30dCount)
	if err != nil {
		return Overview{}, fmt.Errorf("finance: overview: %w", err)
	}
	return o, nil
}

// startOfDayWIB is the last midnight of Western Indonesia Time at or before t.
func startOfDayWIB(t time.Time) time.Time {
	return t.Add(wib).Truncate(day).Add(-wib)
}

// TopUps lists the top-ups matching the filter, newest first. A top-up still pending past its expiry is shown as
// expired.
func (s *Service) TopUps(ctx context.Context, f TopUpFilter, locale platform.Locale) ([]AdminTopUp, error) {
	statuses := make([]string, len(f.Status))
	for i, st := range f.Status {
		statuses[i] = string(st)
	}
	now := s.now()
	rows, err := s.pool.Query(ctx,
		`SELECT t.id, t.customer_id, t.amount_rp, t.method, t.status, t.created_at, t.expires_at, t.paid_at, t.reference,
		        COALESCE(c.name, ''), COALESCE(c.email, ''), c.id IS NOT NULL
		   FROM topups t LEFT JOIN customers c ON c.id = t.customer_id
		  WHERE ($1 = '' OR t.customer_id = $1) AND ($2 = '' OR t.method = $2)
		  ORDER BY t.created_at DESC, t.id DESC`, f.UserID, string(f.Method))
	if err != nil {
		return nil, fmt.Errorf("finance: top-ups: %w", err)
	}
	defer rows.Close()

	needle := strings.ToLower(strings.TrimSpace(f.Q))
	out := []AdminTopUp{}
	for rows.Next() {
		var t AdminTopUp
		var method, status string
		var createdAt, expiresAt time.Time
		var paidAt *time.Time
		var known bool
		if err := rows.Scan(&t.ID, &t.UserID, &t.AmountRp, &method, &status, &createdAt, &expiresAt, &paidAt, &t.Reference,
			&t.UserName, &t.UserEmail, &known); err != nil {
			return nil, fmt.Errorf("finance: scan top-up: %w", err)
		}
		t.Method, t.Status = contracts.TopUpMethod(method), contracts.TopUpStatus(status)
		t.CreatedAt, t.ExpiresAt = contracts.At(createdAt), contracts.At(expiresAt)
		if paidAt != nil {
			t.PaidAt = contracts.At(*paidAt)
		}
		if t.Status == contracts.TopUpStatusPending && !expiresAt.After(now) {
			t.Status = contracts.TopUpStatusExpired
		}
		if !known {
			t.UserName = deletedUser[locale]
			if t.UserName == "" {
				t.UserName = deletedUser[platform.LocaleEN]
			}
		}
		switch {
		case len(statuses) > 0 && !slices.Contains(statuses, string(t.Status)):
			continue
		case needle != "" && !slices.ContainsFunc([]string{t.UserName, t.UserEmail, t.Reference, t.ID},
			func(field string) bool { return strings.Contains(strings.ToLower(field), needle) }):
			continue
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Transactions is a customer's ledger, newest first, at most the latest 200 lines.
func (s *Service) Transactions(ctx context.Context, customerID string) ([]Tx, error) {
	var known bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM customers WHERE id = $1)`, customerID).Scan(&known); err != nil {
		return nil, fmt.Errorf("finance: customer %s: %w", customerID, err)
	}
	if !known {
		return nil, platform.NotFound(contracts.Msg{Key: msgUserNotFound})
	}
	return Ledger(ctx, s.pool, customerID)
}

// Ledger reads a customer's ledger lines, newest first. The title of a payment or a refund names the plan by its
// code, which the plan projection knows (the id stands in until the plan has arrived).
func Ledger(ctx context.Context, q pg.Querier, customerID string) ([]Tx, error) {
	rows, err := q.Query(ctx,
		`SELECT e.id, e.at, e.kind, e.amount_rp, e.balance_after_rp, COALESCE(e.plan_id, ''), COALESCE(e.topup_id, ''),
		        COALESCE(e.topup_method, ''), COALESCE(p.code, '')
		   FROM wallet_entries e LEFT JOIN plans p ON p.id = e.plan_id
		  WHERE e.customer_id = $1
		  ORDER BY e.at DESC, e.version DESC, e.id DESC
		  LIMIT $2`, customerID, transactionsMax)
	if err != nil {
		return nil, fmt.Errorf("finance: ledger: %w", err)
	}
	defer rows.Close()
	out := []Tx{}
	for rows.Next() {
		t := Tx{UserID: customerID}
		var at time.Time
		var method, code string
		if err := rows.Scan(&t.ID, &at, &t.Kind, &t.AmountRp, &t.BalanceAfterRp, &t.PlanID, &t.TopUpID, &method, &code); err != nil {
			return nil, fmt.Errorf("finance: scan ledger line: %w", err)
		}
		t.At = contracts.At(at)
		switch t.Kind {
		case kindTopUp:
			t.Title = contracts.Msg{Key: msgTopUp, Params: map[string]any{"method": contracts.Msg{Key: msgTopUpMethod + method}}}
		case kindPayment:
			t.Title = contracts.Msg{Key: msgPayment, Params: map[string]any{"code": cmp.Or(code, t.PlanID)}}
		case kindRefund:
			t.Title = contracts.Msg{Key: msgRefund, Params: map[string]any{"code": cmp.Or(code, t.PlanID)}}
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
