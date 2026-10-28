package plans

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/routeplan"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/alerts"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/changes"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/settings"
)

// More catalog keys, for the plan's timeline.
const (
	msgDriverDeparted = "srv.api.event.driverDeparted"
	msgDriverFallback = "srv.ops.driver"
	msgArrived        = "srv.ops.event.driverArrived"
	msgPickupDone     = "srv.ops.event.pickupDone"
	msgDropoffDone    = "srv.ops.event.dropoffDone"
	msgPickupProblem  = "srv.ops.event.pickupProblem"
	msgDropoffProblem = "srv.ops.event.dropoffProblem"
	msgAllDone        = "srv.ops.event.allDone"

	msgCancelled       = "srv.api.event.cancelled"
	msgCancelledReason = "srv.api.event.cancelledReason"
	msgRescheduled     = "srv.api.event.rescheduled"
	msgRescheduledNow  = "srv.api.event.rescheduledNow"
)

// Projection holds the consumer handlers that build the projection from other services' events. They run inside
// the consumer's transaction, announce each change to the admin consoles, and tell the admins what needs them.
// pub is for the events a handler itself causes (route.unassigned when a reschedule returns routes to the queue).
type Projection struct {
	feed   *changes.Feed
	pub    *eventbus.Publisher
	alerts *alerts.Notifier
}

func NewProjection(feed *changes.Feed, pub *eventbus.Publisher) *Projection {
	return &Projection{feed: feed, pub: pub, alerts: alerts.New(feed)}
}

// PlanConfirmed (loong.plan.v1) adds a confirmed plan and its routes, waiting for a driver.
func (p *Projection) PlanConfirmed(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.PlanConfirmed]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	if err := validateConfirmed(ev.Plan); err != nil {
		return eventbus.Permanent(err)
	}
	inserted, err := Insert(ctx, tx, ev.Plan)
	if err != nil {
		return err
	}
	if !inserted {
		return nil
	}
	if _, err := tx.Exec(ctx, `UPDATE plans SET plan_version = $2 WHERE id = $1`, ev.Plan.ID, env.AggregateVersion); err != nil {
		return fmt.Errorf("plans: plan version: %w", err)
	}
	// With automatic dispatch on, the worker tells the admins when it finds nobody (see dispatch); with it off the
	// plan waits for a hand, which is news right away.
	auto, err := settings.DispatchAuto(ctx, tx)
	if err != nil {
		return err
	}
	if !auto {
		name, err := CustomerName(ctx, tx, ev.Plan.UserID)
		if err != nil {
			return err
		}
		if err := p.alerts.NewPlan(ctx, tx, NeedsDriverKey(ev.Plan.ID), env.OccurredAt.Time, ev.Plan.ID, ev.Plan.Code, name, len(ev.Plan.Routes)); err != nil {
			return err
		}
	}
	p.feed.Plan(ev.Plan.ID)
	return nil
}

// NeedsDriverKey is the notification key that makes "plan PL-x needs a driver" be told once per plan, whichever of
// the confirmation and the dispatch worker tells it first.
func NeedsDriverKey(planID string) string { return "needs-driver:" + planID }

// validateConfirmed rejects a snapshot dispatch could not work with. It is checked once: the same data would
// fail the same way on every retry.
func validateConfirmed(plan contracts.Plan) error {
	switch {
	case plan.ID == "" || plan.Code == "" || plan.UserID == "":
		return errors.New("plan.confirmed: plan without id, code or user")
	case plan.Status != contracts.PlanStatusAwaitingDriver:
		return fmt.Errorf("plan.confirmed: plan %s has status %q, want %q", plan.ID, plan.Status, contracts.PlanStatusAwaitingDriver)
	case len(plan.Routes) == 0:
		return fmt.Errorf("plan.confirmed: plan %s has no routes", plan.ID)
	}
	for _, r := range plan.Routes {
		switch {
		case r.ID == "" || r.PlanID != plan.ID:
			return fmt.Errorf("plan.confirmed: route %q does not belong to plan %s", r.ID, plan.ID)
		case r.Status != contracts.RouteStatusAwaitingDriver || r.DriverID != "":
			return fmt.Errorf("plan.confirmed: route %s is not waiting for a driver", r.ID)
		case len(r.Stops) == 0:
			return fmt.Errorf("plan.confirmed: route %s has no stops", r.ID)
		}
	}
	return nil
}

// CustomerRegistered (loong.customer.v1) remembers the customer: his name for the plan rows, and what the
// users list shows. A new customer is news for the admins.
func (p *Projection) CustomerRegistered(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.CustomerRegistered]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	if ev.CustomerID == "" {
		return eventbus.Permanent(errors.New("customer.registered: no customer id"))
	}
	changed, inserted, err := upsertCustomer(ctx, tx, ev.CustomerID, ev.Name, ev.Email, ev.Phone, env.AggregateVersion)
	if err != nil {
		return err
	}
	if !ev.CreatedAt.IsZero() {
		if _, err := tx.Exec(ctx, `UPDATE customers SET created_at = $2 WHERE id = $1`, ev.CustomerID, ev.CreatedAt.Time); err != nil {
			return fmt.Errorf("plans: customer created at: %w", err)
		}
	}
	if inserted {
		if err := p.alerts.NewUser(ctx, tx, env.ID, env.OccurredAt.Time, ev.Name); err != nil {
			return err
		}
	}
	if changed {
		p.feed.Users()
	}
	return nil
}

// CustomerUpdated (loong.customer.v1) follows a change of contact data; plan rows show the new name.
func (p *Projection) CustomerUpdated(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.CustomerUpdated]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	if ev.CustomerID == "" {
		return eventbus.Permanent(errors.New("customer.updated: no customer id"))
	}
	changed, _, err := upsertCustomer(ctx, tx, ev.CustomerID, ev.Name, ev.Email, ev.Phone, env.AggregateVersion)
	if changed {
		p.feed.Plans()
		p.feed.Users()
	}
	return err
}

// upsertCustomer stores the customer's contact data unless he is already known at that version or a newer one.
// It reports whether it wrote anything and whether the customer was new.
func upsertCustomer(ctx context.Context, tx pgx.Tx, id, name, email, phone string, version int64) (changed, inserted bool, err error) {
	err = tx.QueryRow(ctx,
		`INSERT INTO customers (id, name, email, phone, version) VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (id) DO UPDATE
		    SET name = EXCLUDED.name, email = EXCLUDED.email, phone = EXCLUDED.phone, version = EXCLUDED.version
		 WHERE customers.version < EXCLUDED.version
		 RETURNING (xmax = 0)`, id, name, email, phone, version).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil // the customer is known at that version or a newer one
	}
	return err == nil, inserted, err
}

// RouteStarted (loong.execution.v1) sets the route off.
func (p *Projection) RouteStarted(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.RouteStarted]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	return p.execute(ctx, tx, env, ev.PlanID, ev.RouteID, ev.StartedAt.Time, func(a *Aggregate, r *Route) (*contracts.Msg, error) {
		started, err := routeplan.Start(r.Route, ev.StartedAt.Time)
		if err != nil {
			return nil, err
		}
		r.Route = started
		var name any = contracts.Msg{Key: msgDriverFallback}
		if err := tx.QueryRow(ctx, `SELECT name FROM drivers WHERE id = $1`, ev.DriverID).Scan(&name); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		return &contracts.Msg{Key: msgDriverDeparted, Params: map[string]any{"name": name}}, nil
	})
}

// StopArrived (loong.execution.v1) marks the active stop as reached.
func (p *Projection) StopArrived(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.StopArrived]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	return p.execute(ctx, tx, env, ev.PlanID, ev.RouteID, ev.ArrivedAt.Time, func(a *Aggregate, r *Route) (*contracts.Msg, error) {
		arrived, err := routeplan.Arrive(r.Route, ev.StopID, ev.ArrivedAt.Time)
		if err != nil {
			return nil, err
		}
		r.Route = arrived
		return &contracts.Msg{Key: msgArrived, Params: map[string]any{"place": placeOf(r.Route, ev.StopID)}}, nil
	})
}

// StopCompleted (loong.execution.v1) finishes a stop; the last one finishes the route.
func (p *Projection) StopCompleted(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.StopCompleted]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	return p.execute(ctx, tx, env, ev.PlanID, ev.RouteID, ev.CompletedAt.Time, func(a *Aggregate, r *Route) (*contracts.Msg, error) {
		closed, _, err := routeplan.Close(r.Route, ev.StopID, ev.CompletedAt.Time, contracts.StopStatusDone, ev.Proof, nil)
		if err != nil {
			return nil, err
		}
		r.Route = closed
		key := msgDropoffDone
		if kindOf(r.Route, ev.StopID) == contracts.StopKindPickup {
			key = msgPickupDone
		}
		return &contracts.Msg{Key: key, Params: map[string]any{"place": placeOf(r.Route, ev.StopID)}}, nil
	})
}

// StopIssue (loong.execution.v1) closes a stop as problematic, with the issue the driver reported.
func (p *Projection) StopIssue(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.StopIssue]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	return p.execute(ctx, tx, env, ev.PlanID, ev.RouteID, ev.ReportedAt.Time, func(a *Aggregate, r *Route) (*contracts.Msg, error) {
		closed, _, err := routeplan.Close(r.Route, ev.StopID, ev.ReportedAt.Time, contracts.StopStatusProblem, nil, &ev.Issue)
		if err != nil {
			return nil, err
		}
		r.Route = closed
		key := msgDropoffProblem
		if kindOf(r.Route, ev.StopID) == contracts.StopKindPickup {
			key = msgPickupProblem
		}
		place := placeOf(r.Route, ev.StopID)
		driver, err := driverName(ctx, tx, r.DriverID)
		if err != nil {
			return nil, err
		}
		if err := p.alerts.StopProblem(ctx, tx, env.ID, ev.ReportedAt.Time, a.Plan.ID, a.Plan.Code, driver, ev.Issue, place); err != nil {
			return nil, err
		}
		return &contracts.Msg{Key: key, Params: map[string]any{"place": place, "issue": ev.Issue}}, nil
	})
}

// RouteCompleted (loong.execution.v1) confirms what closing the last stop already did. A route that is not
// finished yet means the stop events are still on their way.
func (p *Projection) RouteCompleted(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.RouteCompleted]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	return p.execute(ctx, tx, env, ev.PlanID, ev.RouteID, ev.FinishedAt.Time, func(a *Aggregate, r *Route) (*contracts.Msg, error) {
		if r.Status != contracts.RouteStatusDone {
			return nil, errors.New("the route is not finished yet")
		}
		return nil, nil
	})
}

// RouteDelayed (loong.execution.v1) records how late the route runs.
func (p *Projection) RouteDelayed(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.RouteDelayed]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	return p.execute(ctx, tx, env, ev.PlanID, ev.RouteID, env.OccurredAt.Time, func(a *Aggregate, r *Route) (*contracts.Msg, error) {
		r.DelayMin = ev.DelayMin
		driver, err := driverName(ctx, tx, r.DriverID)
		if err != nil {
			return nil, err
		}
		minutes := int(math.Round(ev.DelayMin))
		return nil, p.alerts.Delay(ctx, tx, env.ID, env.OccurredAt.Time, a.Plan.ID, a.Plan.Code, driver, minutes)
	})
}

// execute applies one execution event to its route under the plan's lock. The event is dropped when its
// version is not newer than the last one applied (a duplicate or a late arrival). step changes the route with
// the shared routeplan rules and may return a line for the plan's timeline; at is the time the event carries. It is
// called only for an event that is applied, so it is also where an event's notification is written.
//
// A plan or route not projected yet, or a transition that does not fit the route's state, means topics
// overtook each other: the plain error makes the consumer retry until the earlier events have arrived.
func (p *Projection) execute(ctx context.Context, tx pgx.Tx, env contracts.Envelope, planID, routeID string, at time.Time, step func(*Aggregate, *Route) (*contracts.Msg, error)) error {
	a, err := Lock(ctx, tx, planID)
	if err != nil {
		return fmt.Errorf("%s for route %s: %w", env.Type, routeID, err)
	}
	r, err := a.Route(routeID)
	if err != nil {
		return fmt.Errorf("%s for plan %s: %w", env.Type, planID, err)
	}
	if env.AggregateVersion <= r.ExecVersion {
		return nil
	}

	note, err := step(a, r)
	if err != nil {
		return fmt.Errorf("apply %s to route %s: %w", env.Type, routeID, err)
	}
	r.ExecVersion = env.AggregateVersion
	if note != nil {
		a.Note("evt-"+env.ID, at, *note)
	}
	wasDone := a.Plan.Status == contracts.PlanStatusDone
	a.Sync(at)
	if !wasDone && a.Plan.Status == contracts.PlanStatusDone {
		a.Note("evt-"+env.ID+"-done", at, contracts.Msg{Key: msgAllDone})
	}
	if err := Save(ctx, tx, a); err != nil {
		return err
	}
	p.feed.Plan(planID)
	return nil
}

// driverName is the name of a driver, or "" when none is known (the route has no driver, or his master data has not
// arrived).
func driverName(ctx context.Context, tx pgx.Tx, driverID string) (string, error) {
	if driverID == "" {
		return "", nil
	}
	var name string
	err := tx.QueryRow(ctx, `SELECT name FROM drivers WHERE id = $1`, driverID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return name, err
}

func stopOf(r contracts.Route, stopID string) (contracts.RouteStop, bool) {
	for _, s := range r.Stops {
		if s.ID == stopID {
			return s, true
		}
	}
	return contracts.RouteStop{}, false
}

func placeOf(r contracts.Route, stopID string) string {
	s, _ := stopOf(r, stopID)
	return s.Waypoint.Name
}

func kindOf(r contracts.Route, stopID string) contracts.StopKind {
	s, _ := stopOf(r, stopID)
	return s.Kind
}
