package projections

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/live"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/notify"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/tasks"
)

// keyCancelRunning is the message of a vote against cancelling a route that has left (the mock's refusal of
// cancelling a running plan).
const keyCancelRunning = "srv.api.cancelRunning"

// planCancelRequested casts this service's vote, one per route of the plan, as part of the handler's
// transaction. The votes leave through the outbox keyed by plan, in the order of the route ids:
//
//   - the route is known and has not started: it is held for the request and the vote is "accepted". The hold
//     is what stops driver.startRoute; it lasts until the request commits (the route is closed) or aborts;
//   - the route runs or is done: "rejected", and nothing changes;
//   - the route is not known yet (its route.assigned is on the way): a tombstone stands in for the hold, the
//     vote is "accepted", and the assignment will be ignored when it arrives.
//
// A route is decided while it is locked, together with driver.startRoute, so a start and a request race to a
// single outcome. Voting again for the same request (a redelivery under another event id) votes the same.
func (p *Projections) planCancelRequested(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	e, err := env.Payload[contracts.PlanCancelRequested]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	if e.PlanID == "" || e.RequestID == "" {
		return eventbus.Permanent(errors.New("plan.cancel.requested without plan or request id"))
	}
	if len(e.RouteIDs) == 0 {
		p.log.WarnContext(ctx, "plan.cancel.requested without routes: no vote cast", "planId", e.PlanID, "requestId", e.RequestID)
		return nil
	}
	// Id order, without repeats: two requests voting on the same routes then lock them in the same order.
	for _, routeID := range slices.Compact(slices.Sorted(slices.Values(e.RouteIDs))) {
		reject, err := p.decide(ctx, tx, e, routeID, env.OccurredAt.Time)
		if err != nil {
			return err
		}
		if err := p.vote(ctx, tx, env.ID, e, routeID, reject); err != nil {
			return err
		}
	}
	return nil
}

// decide settles the fate of one route for the request: held, tombstoned, or refused (a non-nil message).
func (p *Projections) decide(ctx context.Context, tx pgx.Tx, e contracts.PlanCancelRequested, routeID string, at time.Time) (*contracts.Msg, error) {
	if err := tasks.LockRoute(ctx, tx, routeID); err != nil {
		return nil, err
	}
	task, err := tasks.Lock(ctx, tx, routeID)
	switch {
	case errors.Is(err, tasks.ErrNotFound):
		return nil, tasks.AddTombstone(ctx, tx, routeID, e.RequestID, e.PlanID, at)
	case err != nil:
		return nil, err
	case task.Cancelled():
		return nil, nil // already closed by a cancel: nothing left to hold
	case task.Started():
		return &contracts.Msg{Key: keyCancelRunning}, nil
	}
	if task.Hold != "" && task.Hold != e.RequestID {
		p.log.WarnContext(ctx, "route held for another cancel request: the newer one takes over", "routeId", routeID, "held", task.Hold, "requestId", e.RequestID)
	}
	return nil, tasks.Hold(ctx, tx, routeID, e.RequestID)
}

// vote enqueues the vote for a route: accepted, or rejected with the reason reject. Votes are not part of any
// aggregate-version family; console tells them apart by request and route id.
func (p *Projections) vote(ctx context.Context, tx pgx.Tx, causedBy string, e contracts.PlanCancelRequested, routeID string, reject *contracts.Msg) error {
	event := eventbus.Event{
		Type: contracts.TypePlanCancelAccepted, Key: e.PlanID, CausationID: causedBy,
		Data: contracts.PlanCancelAccepted{PlanID: e.PlanID, RequestID: e.RequestID, RouteID: routeID},
	}
	if reject != nil {
		event.Type = contracts.TypePlanCancelRejected
		event.Data = contracts.PlanCancelRejected{PlanID: e.PlanID, RequestID: e.RequestID, RouteID: routeID, Reason: *reject}
	}
	return p.pub.Enqueue(ctx, tx, event)
}

// planCancelled commits the cancel: the routes the request held are closed as cancelled, which takes them off
// the driver's list, and the driver is told. The tombstones stay, so that no assignment of the plan can ever
// bring a task back; what was parked behind them is thrown away. A repeated commit finds nothing held and
// does nothing.
func (p *Projections) planCancelled(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	e, err := env.Payload[contracts.PlanCancelled]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	if e.PlanID == "" {
		return eventbus.Permanent(errors.New("plan.cancelled without plan id"))
	}
	closed, err := tasks.Cancel(ctx, tx, e.PlanID, e.RequestID, env.OccurredAt.Time)
	if err != nil {
		return err
	}
	if err := tasks.DropParked(ctx, tx, e.PlanID); err != nil { // nothing of this plan will ever be applied
		return err
	}
	for _, h := range closed {
		if err := live.ToDriver(ctx, tx, h.DriverID, live.PlansChanged(h.PlanID)); err != nil {
			return err
		}
		if err := notify.Cancelled(ctx, tx, notify.Job(h), e.Reason, env.OccurredAt.Time); err != nil {
			return err
		}
	}
	p.log.InfoContext(ctx, "plan cancelled: routes closed", "planId", e.PlanID, "requestId", e.RequestID, "routes", len(closed))
	return nil
}

// planCancelAborted releases the holds of the request and forgets its tombstones: the plan goes on as before and
// the routes can be started again.
func (p *Projections) planCancelAborted(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	e, err := env.Payload[contracts.PlanCancelAborted]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	if e.RequestID == "" {
		return eventbus.Permanent(errors.New("plan.cancel.aborted without request id"))
	}
	released, err := tasks.Release(ctx, tx, e.RequestID)
	if err != nil {
		return err
	}
	forgotten, err := tasks.DropTombstones(ctx, tx, e.RequestID)
	if err != nil {
		return err
	}
	// What dispatch said about those routes while their tombstone stood is no longer ignorable: apply it now, as
	// if it had just arrived (a route another request still has a tombstone for keeps waiting).
	parked, err := tasks.TakeParked(ctx, tx, forgotten)
	if err != nil {
		return err
	}
	for _, k := range parked {
		if k.Assigned == nil {
			continue // the newest word was an unassignment: the route is back in the queue, there is nothing to give
		}
		replay := contracts.Envelope{
			ID: k.EventID, Type: contracts.TypeRouteAssigned, Key: k.RouteID, AggregateVersion: k.Version,
			OccurredAt: contracts.At(k.OccurredAt), Data: k.Assigned,
		}
		if err := p.routeAssigned(ctx, tx, replay); err != nil {
			return err
		}
	}
	p.log.InfoContext(ctx, "plan cancel aborted: holds released", "planId", e.PlanID, "requestId", e.RequestID,
		"released", len(released), "tombstones", len(forgotten), "assignmentsApplied", len(parked))
	return nil
}
