package plans

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/routeplan"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/settings"
)

// The two-phase cancel as backoffice-api sees it (console-api coordinates; see docs/phase2.md):
//
//	plan.cancel.requested  every route of the plan is frozen: dispatch neither assigns nor suggests it
//	plan.cancelled         commit: the plan is cancelled and its assigned routes wait again, drivers freed
//	plan.cancel.aborted    the freeze is lifted and dispatch picks the routes up again
//	plan.cancel.rejected   with no route id: console-api refused what an admin asked (a vote carries a route id and
//	                       is not ours to act on)
//
// Handlers ignore requests that are not the plan's current one (a late abort of an old attempt must not thaw a
// newer one) and are idempotent, since a redelivered event only has to leave the state it already made.

// planOf locks the plan an event is about. An unknown plan is a plain error: its plan.confirmed is on its way (the
// consumer retries), and a plan that never arrives ends up in the dead-letter topic with the rest of its events.
func planOf(ctx context.Context, tx pgx.Tx, env contracts.Envelope, planID string) (*Aggregate, error) {
	a, err := Lock(ctx, tx, planID)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", env.Type, err)
	}
	return a, nil
}

// needPlan is the permanent error of an event that names no plan: it can never be applied.
func needPlan(env contracts.Envelope, planID string) error {
	if planID == "" {
		return eventbus.Permanent(fmt.Errorf("%s: no plan id", env.Type))
	}
	return nil
}

// CancelRequested (loong.plan.v1) freezes every route of the plan for the request.
func (p *Projection) CancelRequested(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.PlanCancelRequested]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	if ev.PlanID == "" || ev.RequestID == "" {
		return eventbus.Permanent(fmt.Errorf("%s: no plan id or request id", env.Type))
	}
	a, err := planOf(ctx, tx, env, ev.PlanID)
	if err != nil {
		return err
	}
	if a.Plan.Status == contracts.PlanStatusCancelled || a.Plan.Status == contracts.PlanStatusDone || a.Held() == ev.RequestID {
		return nil
	}
	a.Freeze(ev.RequestID)
	a.Version = max(a.Version, env.AggregateVersion)
	a.Sync(env.OccurredAt.Time)
	if err := Save(ctx, tx, a); err != nil {
		return err
	}
	p.feed.Plan(ev.PlanID)
	return nil
}

// CancelAborted (loong.plan.v1) lifts the freeze of the request: the plan goes on as before.
func (p *Projection) CancelAborted(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.PlanCancelAborted]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	if err := needPlan(env, ev.PlanID); err != nil {
		return err
	}
	if ev.RequestID == "" {
		return eventbus.Permanent(fmt.Errorf("%s: no request id", env.Type))
	}
	a, err := planOf(ctx, tx, env, ev.PlanID)
	if err != nil {
		return err
	}
	if a.Held() != ev.RequestID {
		return nil // not the request that froze the plan: late, or already settled
	}
	a.Thaw()
	a.Version = max(a.Version, env.AggregateVersion)
	a.Sync(env.OccurredAt.Time)
	if err := Save(ctx, tx, a); err != nil {
		return err
	}
	p.feed.Plan(ev.PlanID)
	return nil
}

// CancelRejected (loong.plan.v1) with no route id means console-api refused the cancel an admin asked for: the
// plan is no longer pending (unless another request froze it meanwhile) and the admins are told why.
func (p *Projection) CancelRejected(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.PlanCancelRejected]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	if ev.RouteID != "" {
		return nil // a driver's vote
	}
	if err := needPlan(env, ev.PlanID); err != nil {
		return err
	}
	a, err := planOf(ctx, tx, env, ev.PlanID)
	if err != nil {
		return err
	}
	if a.Held() == "" && a.Plan.PendingAction != "" {
		a.Plan.PendingAction = ""
		a.Sync(env.OccurredAt.Time)
		if err := Save(ctx, tx, a); err != nil {
			return err
		}
	}
	if err := p.alerts.CancelRefused(ctx, tx, env.ID, env.OccurredAt.Time, a.Plan.ID, a.Plan.Code, ev.Reason); err != nil {
		return err
	}
	p.feed.Plan(ev.PlanID)
	return nil
}

// CancelCommitted (loong.plan.v1) cancels the plan. Routes that had a driver wait again, without route.unassigned:
// driver-api closes its tasks from plan.cancelled itself, and a plan that is cancelled needs no new driver. The
// drivers are free from here on.
func (p *Projection) CancelCommitted(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.PlanCancelled]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	if err := needPlan(env, ev.PlanID); err != nil {
		return err
	}
	a, err := planOf(ctx, tx, env, ev.PlanID)
	if err != nil {
		return err
	}
	if held := a.Held(); a.Plan.Status == contracts.PlanStatusCancelled || (held != "" && ev.RequestID != "" && held != ev.RequestID) {
		return nil
	}

	at := env.OccurredAt.Time
	for i := range a.Routes {
		if r := &a.Routes[i]; r.Status == contracts.RouteStatusAssigned {
			r.Route = routeplan.Unassign(r.Route)
			r.AssignVersion++
		}
	}
	a.Thaw()
	a.Plan.Status = contracts.PlanStatusCancelled
	a.Plan.CancelReason = ev.Reason
	a.Version = max(a.Version, env.AggregateVersion)
	a.Note("evt-"+env.ID, at, cancelNote(ev.Reason))
	a.Sync(at)
	if err := Save(ctx, tx, a); err != nil {
		return err
	}
	if ev.RequestedBy == contracts.CancelByCustomer {
		name, err := CustomerName(ctx, tx, a.Plan.UserID)
		if err != nil {
			return err
		}
		if err := p.alerts.Cancelled(ctx, tx, env.ID, at, a.Plan.ID, a.Plan.Code, name); err != nil {
			return err
		}
	}
	p.feed.Plan(ev.PlanID)
	p.feed.Fleet()
	return nil
}

// defaultCancelReasons are the texts console-api and the mock give a cancel with no reason of the user's, in
// either language; the timeline does not repeat them next to "Order cancelled".
var defaultCancelReasons = map[string]bool{
	"cancelled by sender": true, "cancelled by admin": true, "dibatalkan pengirim": true, "dibatalkan admin": true,
	"dibatalkan oleh pengirim": true, "dibatalkan oleh admin": true,
}

func cancelNote(reason string) contracts.Msg {
	reason = strings.TrimSpace(reason)
	if reason == "" || defaultCancelReasons[strings.ToLower(reason)] {
		return contracts.Msg{Key: msgCancelled}
	}
	return contracts.Msg{Key: msgCancelledReason, Params: map[string]any{"reason": reason}}
}

// Rescheduled (loong.plan.v1) applies the new schedule and the re-timed routes. A route that had a driver goes
// back to the queue (route.unassigned, reason "rescheduled"): the driver may be busy at the new time, so the
// same machinery that assigns every route chooses again. Routes already running or finished are left as they
// are. When automatic dispatch is off the admins are told the plan waits for them.
func (p *Projection) Rescheduled(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.PlanRescheduled]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	if err := needPlan(env, ev.PlanID); err != nil {
		return err
	}
	a, err := planOf(ctx, tx, env, ev.PlanID)
	if err != nil {
		return err
	}
	if env.AggregateVersion <= a.Version {
		return nil
	}
	a.Version = env.AggregateVersion
	at := env.OccurredAt.Time
	if a.Plan.Status == contracts.PlanStatusCancelled || a.Plan.Status == contracts.PlanStatusDone {
		return Save(ctx, tx, a) // late: only the version moves
	}

	a.Plan.Schedule = ev.Schedule
	timed := make(map[string]contracts.Route, len(ev.Routes))
	for _, r := range ev.Routes {
		timed[r.ID] = r
	}
	for i := range a.Routes {
		cur := &a.Routes[i]
		fresh, ok := timed[cur.ID]
		if !ok || cur.Status == contracts.RouteStatusRunning || cur.Status == contracts.RouteStatusDone {
			continue
		}
		wasAssigned := cur.Status == contracts.RouteStatusAssigned
		// The new times are console-api's; who drives is ours.
		fresh.Status, fresh.DriverID, fresh.VehicleID, fresh.AssignedAt = cur.Status, cur.DriverID, cur.VehicleID, cur.AssignedAt
		cur.Route = fresh
		if wasAssigned {
			if err := Release(ctx, tx, p.pub, a, cur, contracts.UnassignRescheduled, UnassignedNote(cur.ID), at); err != nil {
				return err
			}
		}
	}
	a.Note("evt-"+env.ID, at, rescheduleNote(ev.Schedule))
	a.Sync(at)
	if err := Save(ctx, tx, a); err != nil {
		return err
	}

	if waiting := a.waitingRoutes(); waiting > 0 {
		auto, err := settings.DispatchAuto(ctx, tx)
		if err != nil {
			return err
		}
		if !auto {
			name, err := CustomerName(ctx, tx, a.Plan.UserID)
			if err != nil {
				return err
			}
			if err := p.alerts.Rescheduled(ctx, tx, env.ID, at, a.Plan.ID, a.Plan.Code, name, waiting); err != nil {
				return err
			}
		}
	}
	p.feed.Plan(ev.PlanID)
	p.feed.Fleet()
	return nil
}

// waitingRoutes counts the routes that need a driver.
func (a *Aggregate) waitingRoutes() int {
	n := 0
	for _, r := range a.Routes {
		if r.Status == contracts.RouteStatusAwaitingDriver {
			n++
		}
	}
	return n
}

func rescheduleNote(s contracts.Schedule) contracts.Msg {
	if s.Mode == contracts.ScheduleModeScheduled && s.Window != nil {
		return contracts.Msg{Key: msgRescheduled, Params: map[string]any{"time": s.Window.From.UnixMilli()}}
	}
	return contracts.Msg{Key: msgRescheduledNow}
}
