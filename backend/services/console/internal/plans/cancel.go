package plans

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/notices"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/notify"
)

// Cancelling a confirmed plan is a two-phase commit that this service coordinates (docs/phase2.md, section 1).
// The plan is marked pendingAction "cancel" and plan.cancel.requested names every route; driver-api answers with
// one vote per route; once every route accepted the plan is cancelled and refunded in one transaction
// (plan.cancelled), and a single rejection, or a deadline passing, aborts the attempt (plan.cancel.aborted) and
// leaves the plan as it was. A plan that was never confirmed has nobody to ask and is cancelled on the spot.
//
// Everything that changes a plan or its attempt runs in a transaction that holds the plan's row lock (taken first,
// the wallet's after it, like Confirm), so a vote, the sweeper and a new request queue up per plan and see each
// other's result.

// DefaultCancelTimeout is how long drivers have to vote before the attempt is aborted.
const DefaultCancelTimeout = 2 * time.Minute

// Message keys of the cancel, besides the frontend's own srv.api.cancelRunning and srv.api.planInactive.
// These are not in the frontend's catalog yet.
const (
	msgCancelPending = "srv.api.cancelPending"
	msgNoAnswer      = "srv.api.cancelNoAnswer"
)

// Request states in plan_cancel_requests.
const (
	cancelPending   = "pending"
	cancelCommitted = "committed"
	cancelAborted   = "aborted"
)

// pendingCancel is the plan's pendingAction while the votes are awaited.
const pendingCancel = "cancel"

// cancelRequest is one row of plan_cancel_requests.
type cancelRequest struct {
	ID        string
	PlanID    string
	By        string // contracts.CancelByCustomer or contracts.CancelByAdmin
	Requester string
	Reason    string
	Locale    platform.Locale
	RouteIDs  []string
	Status    string
	Deadline  time.Time
}

const requestColumns = `request_id, plan_id, requested_by, requester, reason, locale, route_ids, status, deadline`

func scanRequest(row pgx.Row) (cancelRequest, error) {
	var r cancelRequest
	var locale string
	err := row.Scan(&r.ID, &r.PlanID, &r.By, &r.Requester, &r.Reason, &locale, &r.RouteIDs, &r.Status, &r.Deadline)
	r.Locale = platform.Locale(locale)
	return r, err
}

// loadRequest reads an attempt and locks its row. found is false for an id the console never issued.
func loadRequest(ctx context.Context, tx pgx.Tx, id string) (req cancelRequest, found bool, err error) {
	req, err = scanRequest(tx.QueryRow(ctx, `SELECT `+requestColumns+` FROM plan_cancel_requests WHERE request_id = $1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return cancelRequest{}, false, nil
	}
	return req, err == nil, err
}

// cancelRefusal is the reason a plan cannot be cancelled now (a conflict), or nil. The rules and keys are the
// mock's, plus one for an attempt already in flight.
func cancelRefusal(p contracts.Plan) *platform.APIError {
	switch {
	case p.Status == contracts.PlanStatusRunning:
		return platform.Conflict(contracts.Msg{Key: "srv.api.cancelRunning"})
	case p.Status == contracts.PlanStatusDone || p.Status == contracts.PlanStatusCancelled:
		return platform.Conflict(contracts.Msg{Key: "srv.api.planInactive"})
	case p.PendingAction != "":
		return platform.Conflict(contracts.Msg{Key: msgCancelPending})
	}
	return nil
}

// defaultCancelReason is the text stored as the plan's cancelReason when nobody wrote one, in the language of
// whoever asked (the catalog's srv.api.cancelledBySender and srv.api.cancelledByAdmin; the field is a string).
func defaultCancelReason(locale platform.Locale, by string) string {
	byAdmin := by == contracts.CancelByAdmin
	switch {
	case locale == platform.LocaleID && byAdmin:
		return "Dibatalkan admin"
	case locale == platform.LocaleID:
		return "Dibatalkan pengirim"
	case byAdmin:
		return "Cancelled by admin"
	}
	return "Cancelled by sender"
}

// timelineCancelled is the plan's timeline line for a cancel, with the reason when one was given.
func timelineCancelled(reason string) contracts.Msg {
	if reason == "" {
		return contracts.Msg{Key: "srv.api.event.cancelled"}
	}
	return contracts.Msg{Key: "srv.api.event.cancelledReason", Params: map[string]any{"reason": reason}}
}

// Cancel is plans.cancel for the plan's owner.
func (s *Service) Cancel(ctx context.Context, customerID, id, reason string) (contracts.Plan, error) {
	reason = strings.TrimSpace(reason)
	locale := platform.LocaleFrom(ctx)
	var out contracts.Plan
	err := platform.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		rec, err := lockOwned(ctx, tx, customerID, id)
		if err != nil {
			return err
		}
		if refusal := cancelRefusal(rec.Plan); refusal != nil {
			return refusal
		}
		if editable(rec.Plan.Status) {
			err = s.cancelUnconfirmed(ctx, tx, &rec, reason, locale)
		} else {
			err = s.startCancel(ctx, tx, &rec, cancelRequest{By: contracts.CancelByCustomer, Reason: reason, Locale: locale})
		}
		out = rec.Plan
		return err
	})
	return out, err
}

// cancelUnconfirmed cancels a draft: nobody else knows of it and nothing was paid, so there is nothing to ask or
// refund and no event to publish.
func (s *Service) cancelUnconfirmed(ctx context.Context, tx pgx.Tx, rec *Record, reason string, locale platform.Locale) error {
	p := &rec.Plan
	p.Status = contracts.PlanStatusCancelled
	p.CancelReason = cmp.Or(reason, defaultCancelReason(locale, contracts.CancelByCustomer))
	AppendEvent(p, s.now(), timelineCancelled(reason))
	rec.Version++
	if err := Save(ctx, tx, *rec); err != nil {
		return err
	}
	return notify.PlanChanged(ctx, tx, p.UserID, p.ID)
}

// startCancel opens an attempt on a confirmed plan: it marks the plan, records the attempt and asks every route
// to be held and to vote. req carries who asks, why and in which language; the rest is filled in here.
func (s *Service) startCancel(ctx context.Context, tx pgx.Tx, rec *Record, req cancelRequest) error {
	p := &rec.Plan
	now := s.now()
	req.ID, req.PlanID, req.Status = "cnl-"+uuid.NewV7().String(), p.ID, cancelPending
	req.Deadline = now.Add(s.cancelTimeout)
	req.RouteIDs = make([]string, len(p.Routes))
	for i, r := range p.Routes {
		req.RouteIDs[i] = r.ID
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO plan_cancel_requests (`+requestColumns+`, requested_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		req.ID, req.PlanID, req.By, req.Requester, req.Reason, string(req.Locale), req.RouteIDs, req.Status, req.Deadline, now)
	if err != nil {
		return fmt.Errorf("record the cancel request: %w", err)
	}
	if len(req.RouteIDs) == 0 {
		return s.commitCancel(ctx, tx, rec, req) // no route to ask: nothing can object
	}

	p.PendingAction = pendingCancel
	p.UpdatedAt = contracts.At(now)
	rec.Version++
	if err := Save(ctx, tx, *rec); err != nil {
		return err
	}
	err = s.pub.Enqueue(ctx, tx, eventbus.Event{
		Type: contracts.TypePlanCancelRequested, Key: p.ID, AggregateVersion: rec.Version,
		Data: contracts.PlanCancelRequested{PlanID: p.ID, RequestID: req.ID, RequestedBy: req.By, RouteIDs: req.RouteIDs, Reason: req.Reason},
	})
	if err != nil {
		return err
	}
	return notify.PlanChanged(ctx, tx, p.UserID, p.ID)
}

// commitCancel finishes an attempt every route accepted: the plan is cancelled, the payment from the wallet is
// returned in full (once), and plan.cancelled tells everyone else.
func (s *Service) commitCancel(ctx context.Context, tx pgx.Tx, rec *Record, req cancelRequest) error {
	p := &rec.Plan
	now := s.now()
	reason := cmp.Or(req.Reason, defaultCancelReason(req.Locale, req.By))
	p.Status, p.PendingAction, p.CancelReason = contracts.PlanStatusCancelled, "", reason
	AppendEvent(p, now, timelineCancelled(req.Reason))
	rec.Version++
	if err := Save(ctx, tx, *rec); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE plan_cancel_requests SET status = $2, finished_at = $3 WHERE request_id = $1`, req.ID, cancelCommitted, now); err != nil {
		return err
	}

	var refundRp int64
	if p.PaymentMethod == contracts.PaymentWallet {
		var err error
		if refundRp, err = s.wallet.RefundPlan(ctx, tx, p.UserID, p.ID, p.Code); err != nil {
			return err
		}
	}
	// Only what the requester wrote travels: driver-api shows it to the driver as it is. The default text is the
	// plan's own cancelReason here, composed in the requester's language.
	err := s.pub.Enqueue(ctx, tx, eventbus.Event{
		Type: contracts.TypePlanCancelled, Key: p.ID, AggregateVersion: rec.Version,
		Data: contracts.PlanCancelled{PlanID: p.ID, RequestID: req.ID, RequestedBy: req.By, Reason: req.Reason, RefundRp: refundRp},
	})
	if err != nil {
		return err
	}

	body := contracts.Msg{Key: "srv.api.notif.taskCancelled.body", Params: map[string]any{"code": p.Code}}
	if req.Reason != "" {
		body = contracts.Msg{Key: "srv.api.notif.taskCancelled.bodyReason", Params: map[string]any{"code": p.Code, "reason": req.Reason}}
	}
	err = notify.Notice(ctx, tx, p.UserID, notices.Notice{
		At: now, PlanID: p.ID, Key: "cancel:" + req.ID + ":cancelled",
		Title: contracts.Msg{Key: "srv.api.notif.cancelled.title", Params: map[string]any{"code": p.Code}}, Body: body,
	})
	if err != nil {
		return err
	}
	if refundRp > 0 {
		err = notify.Notice(ctx, tx, p.UserID, notices.Notice{
			At: now, PlanID: p.ID, Key: "cancel:" + req.ID + ":refund",
			Title: contracts.Msg{Key: "srv.api.notif.refund.title"},
			Body:  contracts.Msg{Key: "srv.api.notif.refund.body", Params: map[string]any{"code": p.Code, "amount": refundRp}},
		})
		if err != nil {
			return err
		}
	}
	return notify.PlanChanged(ctx, tx, p.UserID, p.ID)
}

// abortCancel ends an attempt that cannot commit (a route refused, or the votes did not all arrive in time): the
// plan goes on as before and plan.cancel.aborted releases every hold. A customer who asked is told why; an
// admin's attempt is reported back to back office by the same event.
func (s *Service) abortCancel(ctx context.Context, tx pgx.Tx, rec *Record, req cancelRequest, reason contracts.Msg) error {
	p := &rec.Plan
	now := s.now()
	p.PendingAction = ""
	p.UpdatedAt = contracts.At(now)
	rec.Version++
	if err := Save(ctx, tx, *rec); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE plan_cancel_requests SET status = $2, finished_at = $3 WHERE request_id = $1`, req.ID, cancelAborted, now); err != nil {
		return err
	}
	err := s.pub.Enqueue(ctx, tx, eventbus.Event{
		Type: contracts.TypePlanCancelAborted, Key: p.ID, AggregateVersion: rec.Version,
		Data: contracts.PlanCancelAborted{PlanID: p.ID, RequestID: req.ID, Reason: reason},
	})
	if err != nil {
		return err
	}
	if req.By == contracts.CancelByCustomer {
		err = notify.Notice(ctx, tx, p.UserID, notices.Notice{
			At: now, PlanID: p.ID, Key: "cancel:" + req.ID + ":aborted",
			Title: contracts.Msg{Key: "srv.api.notif.cancelAborted.title"},
			Body:  contracts.Msg{Key: "srv.api.notif.cancelAborted.body", Params: map[string]any{"code": p.Code, "reason": reason}},
		})
		if err != nil {
			return err
		}
	}
	return notify.PlanChanged(ctx, tx, p.UserID, p.ID)
}

// RegisterCancel handles what the plan topic carries for this service: an admin's request to cancel
// (plan.cancel.asked from back office) and driver-api's votes. The topic also carries the console's own events,
// which have no handler here. Use a consumer group of its own.
func (s *Service) RegisterCancel(c *eventbus.Consumer) {
	c.Handle(contracts.TypePlanCancelAsked, s.onAsked)
	c.Handle(contracts.TypePlanCancelAccepted, s.onAccepted)
	c.Handle(contracts.TypePlanCancelRejected, s.onRejected)
}

func (s *Service) onAccepted(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	vote, err := env.Payload[contracts.PlanCancelAccepted]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	return s.onVote(ctx, tx, vote.PlanID, vote.RequestID, vote.RouteID, true, contracts.Msg{})
}

func (s *Service) onRejected(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	vote, err := env.Payload[contracts.PlanCancelRejected]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	return s.onVote(ctx, tx, vote.PlanID, vote.RequestID, vote.RouteID, false, vote.Reason)
}

// onAsked starts the cancel an admin asked back office for, after the same validation as a customer's own
// request. When the plan cannot be cancelled (it is running, finished, already cancelled or being cancelled)
// the answer is plan.cancel.rejected without a route id, so back office clears its own mark and tells the admin.
func (s *Service) onAsked(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.PlanCancelAsked]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	if ev.PlanID == "" {
		return eventbus.Permanent(fmt.Errorf("%s without a plan id", env.Type))
	}
	refuse := func(reason contracts.Msg) error {
		return s.pub.Enqueue(ctx, tx, eventbus.Event{
			Type: contracts.TypePlanCancelRejected, Key: ev.PlanID, CausationID: env.ID,
			Data: contracts.PlanCancelRejected{PlanID: ev.PlanID, Reason: reason},
		})
	}

	rec, err := Lock(ctx, tx, ev.PlanID)
	if errors.Is(err, ErrNotFound) {
		return refuse(contracts.Msg{Key: "srv.ops.err.planNotFound"})
	}
	if err != nil {
		return err
	}
	if refusal := cancelRefusal(rec.Plan); refusal != nil {
		return refuse(refusal.Msg)
	}
	if editable(rec.Plan.Status) { // a draft is the customer's alone: back office never sees one
		return refuse(contracts.Msg{Key: "srv.api.planInactive"})
	}
	return s.startCancel(ctx, tx, &rec, cancelRequest{
		By: contracts.CancelByAdmin, Requester: ev.AskedBy, Reason: strings.TrimSpace(ev.Reason), Locale: platform.ParseLocale(ev.Locale),
	})
}

// onVote counts one vote of driver-api. Votes that cannot matter are dropped quietly: a vote without a route
// (the console's own refusal of an admin's request, which this consumer reads too), for an attempt this console
// never made, for another plan, already decided, for a route the attempt did not ask, or one the route already
// gave. A rejection aborts the attempt; the last acceptance commits it.
func (s *Service) onVote(ctx context.Context, tx pgx.Tx, planID, requestID, routeID string, accepted bool, reason contracts.Msg) error {
	if planID == "" || requestID == "" || routeID == "" {
		return nil
	}
	// The plan first: a vote that arrives while the attempt is still being written or decided waits for it.
	rec, err := Lock(ctx, tx, planID)
	if errors.Is(err, ErrNotFound) {
		return eventbus.Permanent(fmt.Errorf("cancel vote for plan %s, which this console does not have", planID))
	}
	if err != nil {
		return err
	}
	req, found, err := loadRequest(ctx, tx, requestID)
	if err != nil {
		return err
	}
	if !found || req.PlanID != planID || req.Status != cancelPending || !containsRoute(req.RouteIDs, routeID) {
		return nil
	}
	tag, err := tx.Exec(ctx,
		`INSERT INTO plan_cancel_votes (request_id, route_id, accepted) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		requestID, routeID, accepted)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil
	}

	if !accepted {
		if reason.Key == "" {
			reason = contracts.Msg{Key: "srv.api.cancelRunning"}
		}
		return s.abortCancel(ctx, tx, &rec, req, reason)
	}
	var yes int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM plan_cancel_votes WHERE request_id = $1 AND accepted`, requestID).Scan(&yes); err != nil {
		return err
	}
	if yes < len(req.RouteIDs) {
		return nil
	}
	return s.commitCancel(ctx, tx, &rec, req)
}

func containsRoute(ids []string, id string) bool {
	for _, r := range ids {
		if r == id {
			return true
		}
	}
	return false
}

// SweepCancels aborts every attempt whose deadline has passed, with the reason that nobody answered, and returns
// how many it aborted. An attempt that was decided meanwhile is left alone.
func (s *Service) SweepCancels(ctx context.Context) (int, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT request_id, plan_id FROM plan_cancel_requests WHERE status = $1 AND deadline <= $2 ORDER BY deadline LIMIT 100`,
		cancelPending, s.now())
	if err != nil {
		return 0, err
	}
	type due struct{ requestID, planID string }
	var overdue []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.requestID, &d.planID); err != nil {
			rows.Close()
			return 0, err
		}
		overdue = append(overdue, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	aborted := 0
	var errs []error
	for _, d := range overdue {
		var did bool
		err := platform.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
			rec, err := Lock(ctx, tx, d.planID)
			if err != nil {
				return err
			}
			req, found, err := loadRequest(ctx, tx, d.requestID)
			if err != nil || !found || req.Status != cancelPending || req.Deadline.After(s.now()) {
				return err
			}
			did = true
			return s.abortCancel(ctx, tx, &rec, req, contracts.Msg{Key: msgNoAnswer})
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("abort cancel %s: %w", d.requestID, err))
		} else if did {
			aborted++
		}
	}
	return aborted, errors.Join(errs...)
}

// RunCancelSweeper calls SweepCancels every interval until ctx is done. A failed round is logged and tried again.
func (s *Service) RunCancelSweeper(ctx context.Context, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			n, err := s.SweepCancels(ctx)
			if err != nil && ctx.Err() == nil {
				s.log.ErrorContext(ctx, "cancel sweeper round failed", "error", err)
			}
			if n > 0 {
				s.log.InfoContext(ctx, "cancel requests aborted for lack of answers", "count", n)
			}
		}
	}
}
