// Package wallet is the shipper's balance: a ledger whose entries are the only thing that moves money, top-ups
// that credit it, and the payment a plan is charged with. Real payment providers are out of scope: a top-up is
// paid by simulatePay (development and tests) until one is connected.
package wallet

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/notices"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/notify"
)

// Limits shared with the frontend (packages/domain/src/wallet.ts).
const (
	TopUpMinRp       = 10_000
	TopUpMaxRp       = 5_000_000
	MaxPendingTopUps = 3
	transactionsMax  = 200
)

// Ledger entry kinds are the frontend's WalletTxKind.
const (
	kindTopUp   = "topup"
	kindPayment = "pembayaran"
	kindRefund  = "refund"
)

// Wallet is what wallet.get answers.
type Wallet struct {
	BalanceRp int64 `json:"balanceRp"`
	// PendingTopUps are those waiting for payment and not yet expired, newest first.
	PendingTopUps []contracts.TopUp `json:"pendingTopUps"`
}

// Entry is one ledger line, the frontend's WalletTx. AmountRp is negative when the balance went down.
type Entry struct {
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

// TopUpInput is the frontend's TopUpInput. The amount is a number so that a fractional one reaches us as such
// and gets the "whole rupiah" message instead of failing to decode.
type TopUpInput struct {
	AmountRp float64               `json:"amountRp"`
	Method   contracts.TopUpMethod `json:"method"`
}

// Service keeps the wallets.
type Service struct {
	pool *pgxpool.Pool
	pub  *eventbus.Publisher
	now  func() time.Time
}

func New(pool *pgxpool.Pool, pub *eventbus.Publisher) *Service {
	return &Service{pool: pool, pub: pub, now: time.Now}
}

// lock takes the customer's wallet row. Every wallet transaction starts here, so they queue per customer and
// always take their locks in the same order (wallet before top-up rows), which rules out deadlocks.
func lock(ctx context.Context, tx pgx.Tx, customerID string) (balanceRp int64, err error) {
	err = tx.QueryRow(ctx, `SELECT balance_rp FROM wallets WHERE customer_id = $1 FOR UPDATE`, customerID).Scan(&balanceRp)
	if err != nil {
		return 0, fmt.Errorf("wallet of %s: %w", customerID, err)
	}
	return balanceRp, nil
}

// post writes a ledger entry and moves the balance by the same amount, so balance_rp is always the sum of the
// entries. The wallet row must be locked by the caller.
func post(ctx context.Context, tx pgx.Tx, customerID, kind, reference string, deltaRp int64, title contracts.Msg, at time.Time) (Entry, error) {
	var balanceAfter int64
	if err := tx.QueryRow(ctx,
		`UPDATE wallets SET balance_rp = balance_rp + $2 WHERE customer_id = $1 RETURNING balance_rp`,
		customerID, deltaRp).Scan(&balanceAfter); err != nil {
		return Entry{}, fmt.Errorf("move balance of %s: %w", customerID, err)
	}
	titleJSON, err := json.Marshal(title)
	if err != nil {
		return Entry{}, err
	}
	e := Entry{
		ID: "wtx-" + uuid.NewV7().String(), UserID: customerID, At: contracts.At(at), Kind: kind,
		AmountRp: deltaRp, BalanceAfterRp: balanceAfter, Title: title,
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO wallet_entries (id, customer_id, at, kind, delta_rp, balance_after_rp, reference, title)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		e.ID, customerID, at, kind, deltaRp, balanceAfter, reference, titleJSON); err != nil {
		return Entry{}, fmt.Errorf("write ledger entry: %w", err)
	}
	if kind == kindTopUp {
		e.TopUpID = reference
	} else {
		e.PlanID = reference
	}
	return e, nil
}

// publish enqueues a wallet-topic event keyed by the customer, numbered by the wallet's own counter.
func (s *Service) publish(ctx context.Context, tx pgx.Tx, customerID, eventType string, data any) error {
	var version int64
	if err := tx.QueryRow(ctx, `UPDATE wallets SET version = version + 1 WHERE customer_id = $1 RETURNING version`, customerID).Scan(&version); err != nil {
		return fmt.Errorf("number %s: %w", eventType, err)
	}
	return s.pub.Enqueue(ctx, tx, eventbus.Event{Type: eventType, Key: customerID, AggregateVersion: version, Data: data})
}

// ChargePlan pays a plan from the wallet inside the caller's transaction: it locks the wallet, refuses when the
// balance is short (before anything was written, so the caller's transaction has nothing to undo), debits it,
// and enqueues wallet.charged. A plan can be charged once: the ledger's (kind, reference) key rejects a second
// charge even if the caller's own checks failed.
func (s *Service) ChargePlan(ctx context.Context, tx pgx.Tx, customerID, planID, planCode string, amountRp int64) error {
	if amountRp <= 0 {
		return fmt.Errorf("wallet: cannot charge %d for plan %s", amountRp, planID)
	}
	balance, err := lock(ctx, tx, customerID)
	if err != nil {
		return err
	}
	if balance < amountRp {
		return platform.Invalid("paymentMethod", contracts.Msg{
			Key: "srv.ops.err.insufficientBalanceDetail", Params: map[string]any{"balance": balance, "cost": amountRp},
		})
	}
	entry, err := post(ctx, tx, customerID, kindPayment, planID, -amountRp,
		contracts.Msg{Key: "srv.ops.wallet.payment", Params: map[string]any{"code": planCode}}, s.now())
	if err != nil {
		return err
	}
	err = s.publish(ctx, tx, customerID, contracts.TypeWalletCharged, contracts.WalletCharged{
		TxID: entry.ID, CustomerID: customerID, PlanID: planID, AmountRp: amountRp, BalanceAfterRp: entry.BalanceAfterRp,
	})
	if err != nil {
		return err
	}
	return notify.WalletChanged(ctx, tx, customerID)
}

// RefundPlan gives back, inside the caller's transaction, what the plan was charged: the whole payment, once. It
// writes the refund's ledger entry, enqueues wallet.refunded and announces the change, and returns the amount. A
// plan that was not paid from the wallet, or was refunded before, gets nothing (0, nil): the ledger's
// (kind, reference) key makes a second refund impossible even if the caller's own checks failed.
func (s *Service) RefundPlan(ctx context.Context, tx pgx.Tx, customerID, planID, planCode string) (refundedRp int64, err error) {
	if _, err := lock(ctx, tx, customerID); err != nil {
		return 0, err
	}
	var paid int64
	err = tx.QueryRow(ctx,
		`SELECT -delta_rp FROM wallet_entries WHERE customer_id = $1 AND kind = $2 AND reference = $3`,
		customerID, kindPayment, planID).Scan(&paid)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("find the payment of %s: %w", planID, err)
	}
	var refunded bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM wallet_entries WHERE kind = $1 AND reference = $2)`, kindRefund, planID).Scan(&refunded); err != nil {
		return 0, fmt.Errorf("find the refund of %s: %w", planID, err)
	}
	if refunded {
		return 0, nil
	}
	entry, err := post(ctx, tx, customerID, kindRefund, planID, paid,
		contracts.Msg{Key: "srv.ops.wallet.refund", Params: map[string]any{"code": planCode}}, s.now())
	if err != nil {
		return 0, err
	}
	err = s.publish(ctx, tx, customerID, contracts.TypeWalletRefunded, contracts.WalletRefunded{
		TxID: entry.ID, CustomerID: customerID, PlanID: planID, AmountRp: paid, BalanceAfterRp: entry.BalanceAfterRp,
	})
	if err != nil {
		return 0, err
	}
	return paid, notify.WalletChanged(ctx, tx, customerID)
}

// Get is the balance and the top-ups still waiting for payment. Reading expires the overdue ones first, as a
// payment provider would have by then.
func (s *Service) Get(ctx context.Context, customerID string) (Wallet, error) {
	var w Wallet
	err := platform.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		balance, err := lock(ctx, tx, customerID)
		if err != nil {
			return err
		}
		if err := s.expireOverdue(ctx, tx, customerID); err != nil {
			return err
		}
		pending, err := queryTopUps(ctx, tx, `WHERE customer_id = $1 AND status = 'menunggu' ORDER BY created_at DESC`, customerID)
		w = Wallet{BalanceRp: balance, PendingTopUps: pending}
		return err
	})
	return w, err
}

// Transactions is the customer's ledger, newest first.
func (s *Service) Transactions(ctx context.Context, customerID string) ([]Entry, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, at, kind, delta_rp, balance_after_rp, reference, title
		   FROM wallet_entries WHERE customer_id = $1 ORDER BY seq DESC LIMIT $2`, customerID, transactionsMax)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []Entry{}
	for rows.Next() {
		var (
			e         = Entry{UserID: customerID}
			at        time.Time
			reference string
			title     []byte
		)
		if err := rows.Scan(&e.ID, &at, &e.Kind, &e.AmountRp, &e.BalanceAfterRp, &reference, &title); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(title, &e.Title); err != nil {
			return nil, fmt.Errorf("ledger entry %s title: %w", e.ID, err)
		}
		e.At = contracts.At(at)
		if e.Kind == kindTopUp {
			e.TopUpID = reference
		} else {
			e.PlanID = reference
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// validateAmount is validateTopUpAmount of the frontend; ok is false with the message to show when it fails.
func validateAmount(amountRp float64) (msg contracts.Msg, ok bool) {
	switch {
	case amountRp <= 0:
		return contracts.Msg{Key: "dom.topup.errEnter"}, false
	case amountRp != math.Trunc(amountRp):
		return contracts.Msg{Key: "dom.topup.errInteger"}, false
	case amountRp < TopUpMinRp:
		return contracts.Msg{Key: "dom.topup.errMin", Params: map[string]any{"min": TopUpMinRp}}, false
	case amountRp > TopUpMaxRp:
		return contracts.Msg{Key: "dom.topup.errMax", Params: map[string]any{"max": TopUpMaxRp}}, false
	}
	return contracts.Msg{}, true
}

// A top-up that is missing or someone else's is answered 404 or 403.
func notFoundTopUp() *platform.APIError {
	return platform.NotFound(contracts.Msg{Key: "srv.ops.err.topUpNotFound"})
}

func forbiddenTopUp() *platform.APIError {
	return platform.Denied(contracts.Msg{Key: "srv.ops.err.topUpForbidden"})
}

// CreateTopUp opens a top-up that waits for payment. Retries are made safe by the Idempotency-Key the RPC layer
// requires, so a repeated request does not open a second one.
func (s *Service) CreateTopUp(ctx context.Context, customerID string, in TopUpInput) (contracts.TopUp, error) {
	if msg, ok := validateAmount(in.AmountRp); !ok {
		return contracts.TopUp{}, platform.Invalid("amountRp", msg)
	}
	if !validMethod(in.Method) {
		return contracts.TopUp{}, platform.Invalid("method", contracts.Msg{Key: "srv.ops.err.chooseMethod"})
	}
	var created contracts.TopUp
	err := platform.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := lock(ctx, tx, customerID); err != nil {
			return err
		}
		if err := s.expireOverdue(ctx, tx, customerID); err != nil {
			return err
		}
		var pending int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM top_ups WHERE customer_id = $1 AND status = 'menunggu'`, customerID).Scan(&pending); err != nil {
			return err
		}
		if pending >= MaxPendingTopUps {
			return platform.Conflict(contracts.Msg{Key: "srv.ops.err.tooManyPending", Params: map[string]any{"count": MaxPendingTopUps}})
		}
		var phone string
		if err := tx.QueryRow(ctx, `SELECT phone FROM customers WHERE id = $1`, customerID).Scan(&phone); err != nil {
			return err
		}

		now, id := s.now(), "top-"+uuid.NewV7().String()
		created = contracts.TopUp{
			ID: id, UserID: customerID, AmountRp: int64(in.AmountRp), Method: in.Method, Status: contracts.TopUpStatusPending,
			CreatedAt: contracts.At(now), ExpiresAt: contracts.At(now.Add(topUpTTL(in.Method))),
			Reference: topUpReference(in.Method, phone, id),
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO top_ups (id, customer_id, amount_rp, method, status, reference, created_at, expires_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			id, customerID, created.AmountRp, string(created.Method), string(created.Status), created.Reference,
			created.CreatedAt.Time, created.ExpiresAt.Time); err != nil {
			return err
		}
		if err := s.publish(ctx, tx, customerID, contracts.TypeTopUpCreated, contracts.TopUpCreated{TopUp: created}); err != nil {
			return err
		}
		return notify.WalletChanged(ctx, tx, customerID)
	})
	return created, err
}

// GetTopUp is one of the customer's top-ups, expired first if it is overdue.
func (s *Service) GetTopUp(ctx context.Context, customerID, id string) (contracts.TopUp, error) {
	var t contracts.TopUp
	err := platform.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := lock(ctx, tx, customerID); err != nil {
			return err
		}
		if err := s.expireOverdue(ctx, tx, customerID); err != nil {
			return err
		}
		var err error
		t, err = ownTopUp(ctx, tx, customerID, id)
		return err
	})
	return t, err
}

// SimulatePay marks a top-up paid and credits the wallet: what a provider's confirmation will do. Paying twice
// credits once.
func (s *Service) SimulatePay(ctx context.Context, customerID, id string) (contracts.TopUp, error) {
	var t contracts.TopUp
	err := platform.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := lock(ctx, tx, customerID); err != nil {
			return err
		}
		if err := s.expireOverdue(ctx, tx, customerID); err != nil {
			return err
		}
		var err error
		if t, err = ownTopUp(ctx, tx, customerID, id); err != nil {
			return err
		}
		switch t.Status {
		case contracts.TopUpStatusSucceeded:
			return nil
		case contracts.TopUpStatusExpired:
			return platform.Conflict(contracts.Msg{Key: "srv.ops.err.topUpExpired"})
		case contracts.TopUpStatusCancelled:
			return platform.Conflict(contracts.Msg{Key: "srv.ops.err.topUpCancelled"})
		}

		now := s.now()
		t.Status, t.PaidAt = contracts.TopUpStatusSucceeded, contracts.At(now)
		if _, err := tx.Exec(ctx, `UPDATE top_ups SET status = $2, paid_at = $3 WHERE id = $1`, id, string(t.Status), t.PaidAt.Time); err != nil {
			return err
		}
		title := contracts.Msg{Key: "srv.ops.wallet.topUp", Params: map[string]any{
			"method": contracts.Msg{Key: "dom.topupMethod." + string(t.Method)},
		}}
		entry, err := post(ctx, tx, customerID, kindTopUp, id, t.AmountRp, title, now)
		if err != nil {
			return err
		}
		err = s.publish(ctx, tx, customerID, contracts.TypeTopUpPaid, contracts.TopUpPaid{TopUp: t, TxID: entry.ID, BalanceAfterRp: entry.BalanceAfterRp})
		if err != nil {
			return err
		}
		err = notify.Notice(ctx, tx, customerID, notices.Notice{
			At: now, Key: "topup-paid:" + id,
			Title: contracts.Msg{Key: "srv.ops.wallet.topUpDoneTitle"},
			Body:  contracts.Msg{Key: "srv.ops.wallet.topUpDoneBody", Params: map[string]any{"amount": t.AmountRp}},
		})
		if err != nil {
			return err
		}
		return notify.WalletChanged(ctx, tx, customerID)
	})
	return t, err
}

// CancelTopUp withdraws a top-up that has not been paid. The wallet topic has no event for it (back office
// learns of it when the instruction would have expired), so none is published.
func (s *Service) CancelTopUp(ctx context.Context, customerID, id string) (contracts.TopUp, error) {
	var t contracts.TopUp
	err := platform.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := lock(ctx, tx, customerID); err != nil {
			return err
		}
		if err := s.expireOverdue(ctx, tx, customerID); err != nil {
			return err
		}
		var err error
		if t, err = ownTopUp(ctx, tx, customerID, id); err != nil {
			return err
		}
		if t.Status != contracts.TopUpStatusPending {
			return platform.Conflict(contracts.Msg{Key: "srv.ops.err.onlyPendingCancel"})
		}
		t.Status = contracts.TopUpStatusCancelled
		if _, err := tx.Exec(ctx, `UPDATE top_ups SET status = $2 WHERE id = $1`, id, string(t.Status)); err != nil {
			return err
		}
		if err := s.publish(ctx, tx, customerID, contracts.TypeTopUpCancelled, contracts.TopUpCancelled{TopUp: t}); err != nil {
			return err
		}
		return notify.WalletChanged(ctx, tx, customerID)
	})
	return t, err
}

// expireOverdue marks the customer's unpaid top-ups past their deadline as expired and announces each.
func (s *Service) expireOverdue(ctx context.Context, tx pgx.Tx, customerID string) error {
	rows, err := tx.Query(ctx,
		`UPDATE top_ups SET status = 'kedaluwarsa'
		  WHERE customer_id = $1 AND status = 'menunggu' AND expires_at <= $2
		  RETURNING `+topUpColumns, customerID, s.now())
	if err != nil {
		return err
	}
	expired, err := collectTopUps(rows)
	if err != nil {
		return err
	}
	for _, t := range expired {
		if err := s.publish(ctx, tx, customerID, contracts.TypeTopUpExpired, contracts.TopUpExpired{TopUp: t}); err != nil {
			return err
		}
	}
	if len(expired) > 0 {
		return notify.WalletChanged(ctx, tx, customerID)
	}
	return nil
}

const topUpColumns = `id, customer_id, amount_rp, method, status, reference, created_at, expires_at, paid_at`

func collectTopUps(rows pgx.Rows) ([]contracts.TopUp, error) {
	defer rows.Close()
	topUps := []contracts.TopUp{}
	for rows.Next() {
		var (
			t                contracts.TopUp
			method, status   string
			created, expires time.Time
			paid             *time.Time
		)
		if err := rows.Scan(&t.ID, &t.UserID, &t.AmountRp, &method, &status, &t.Reference, &created, &expires, &paid); err != nil {
			return nil, err
		}
		t.Method, t.Status = contracts.TopUpMethod(method), contracts.TopUpStatus(status)
		t.CreatedAt, t.ExpiresAt = contracts.At(created), contracts.At(expires)
		if paid != nil {
			t.PaidAt = contracts.At(*paid)
		}
		topUps = append(topUps, t)
	}
	return topUps, rows.Err()
}

func queryTopUps(ctx context.Context, tx pgx.Tx, where string, args ...any) ([]contracts.TopUp, error) {
	rows, err := tx.Query(ctx, `SELECT `+topUpColumns+` FROM top_ups `+where, args...)
	if err != nil {
		return nil, err
	}
	return collectTopUps(rows)
}

// ownTopUp reads a top-up and locks its row; it must be the customer's.
func ownTopUp(ctx context.Context, tx pgx.Tx, customerID, id string) (contracts.TopUp, error) {
	topUps, err := queryTopUps(ctx, tx, `WHERE id = $1 FOR UPDATE`, id)
	switch {
	case err != nil:
		return contracts.TopUp{}, err
	case len(topUps) == 0:
		return contracts.TopUp{}, notFoundTopUp()
	case topUps[0].UserID != customerID:
		return contracts.TopUp{}, forbiddenTopUp()
	}
	return topUps[0], nil
}

var vaPrefixes = map[contracts.TopUpMethod]string{
	contracts.TopUpMethodVABCA:     "39358",
	contracts.TopUpMethodVAMandiri: "88608",
	contracts.TopUpMethodVABNI:     "8808",
	contracts.TopUpMethodVABRI:     "26215",
}

func validMethod(m contracts.TopUpMethod) bool {
	_, isVA := vaPrefixes[m]
	return isVA || m == contracts.TopUpMethodQRIS
}

// topUpTTL is how long payment instructions stay valid: half an hour for a QR code, a day for a virtual account.
func topUpTTL(m contracts.TopUpMethod) time.Duration {
	if m == contracts.TopUpMethodQRIS {
		return 30 * time.Minute
	}
	return 24 * time.Hour
}

// topUpReference is what the payer is told to pay to. A virtual account is the bank's prefix plus the
// customer's phone number, so it stays the same for them; a QR code is new for every top-up. Until a provider
// issues real ones these are placeholders in the same shape.
func topUpReference(m contracts.TopUpMethod, phone, topUpID string) string {
	prefix, isVA := vaPrefixes[m]
	if !isVA {
		return "LOONG-" + strings.ToUpper(topUpID[len(topUpID)-12:])
	}
	digits := phone[max(0, len(phone)-11):]
	return prefix + strings.Repeat("0", 11-len(digits)) + digits
}
