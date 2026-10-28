// Package projections keeps the console's plans in step with what happens after confirmation: back office
// assigns drivers (loong.dispatch.v1) and drivers run the routes (loong.execution.v1). The console owns neither
// decision, so it only follows: each event is applied to the plan's route with the rules every service shares
// (pkg/routeplan), the plan's status is derived again from its routes, and the shipper's open dashboards are
// told to refetch.
package projections

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/notices"
	"github.com/mseptiaan/loong.lat/backend/pkg/routeplan"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/fleet"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/notify"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/plans"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/proofs"
)

// RegisterDispatch handles back office's assignments of routes to drivers.
func RegisterDispatch(c *eventbus.Consumer) {
	c.Handle(contracts.TypeRouteAssigned, routeAssigned)
	c.Handle(contracts.TypeRouteUnassigned, routeUnassigned)
}

// RegisterExecution handles what drivers do on their routes.
func RegisterExecution(c *eventbus.Consumer) {
	c.Handle(contracts.TypeRouteStarted, routeStarted)
	c.Handle(contracts.TypeStopArrived, stopArrived)
	c.Handle(contracts.TypeStopCompleted, stopCompleted)
	c.Handle(contracts.TypeStopIssue, stopIssue)
	c.Handle(contracts.TypeRouteCompleted, routeCompleted)
	c.Handle(contracts.TypeRouteDelayed, routeDelayed)
}

// family is a stream of events about a route with its own numbering: each owner counts its own events.
type family int

const (
	dispatchEvents  family = iota // numbered by backoffice (assign_version)
	executionEvents               // numbered by the driver service (exec_version)
)

// applied is the version of the last event of the family that was applied to the route.
func (f family) applied(v *plans.RouteVersions) *int64 {
	if f == dispatchEvents {
		return &v.Assign
	}
	return &v.Exec
}

// transition changes one route and says what to write on the plan's timeline.
type transition func(route contracts.Route) (contracts.Route, []contracts.Msg, error)

// notice is what the shipper is told about an event, once it has been applied: before and after are the route
// as it was and as it is, and ok is false when the event is no news to them.
type notice func(p contracts.Plan, before, after contracts.Route) (title, body contracts.Msg, ok bool)

// follow applies an event to a route of a plan inside the consumer's transaction.
//
// An event for a plan the console does not have yet is a transient condition (another topic is behind), and so
// is a transition the route is not ready for (the event that prepares it is still on its way): both come back as
// plain errors, which the consumer retries. What can never become valid is permanent: an unreadable payload, a
// route that is not part of the plan, a stop the route does not have.
//
// The shipper's bell menu is written in the same transaction (keyed by the event, so a redelivery adds nothing):
// the notice the event brings, if any, and "order completed" when it finished the plan.
func follow(ctx context.Context, tx pgx.Tx, env contracts.Envelope, planID, routeID string, fam family, at time.Time, step transition, news notice) error {
	rec, err := plans.Lock(ctx, tx, planID)
	if errors.Is(err, plans.ErrNotFound) {
		return fmt.Errorf("%s: plan %s is not known yet: %w", env.Type, planID, err)
	}
	if err != nil {
		return err
	}
	i := slices.IndexFunc(rec.Plan.Routes, func(r contracts.Route) bool { return r.ID == routeID })
	if i < 0 {
		return eventbus.Permanent(fmt.Errorf("%s: route %s is not part of plan %s", env.Type, routeID, planID))
	}
	if s := rec.Plan.Status; s == contracts.PlanStatusDone || s == contracts.PlanStatusCancelled {
		return nil
	}

	versions := rec.RouteVersions[routeID]
	// Drop what is old or already applied. A route the driver has started is past dispatch: an assignment event
	// that reaches us now (the topics are consumed independently) describes an earlier arrangement.
	if env.AggregateVersion <= *fam.applied(&versions) || (fam == dispatchEvents && versions.Exec > 0) {
		return nil
	}

	if at.IsZero() {
		at = env.OccurredAt.Time
	}
	route, texts, err := step(rec.Plan.Routes[i])
	if errors.Is(err, routeplan.ErrStopNotFound) {
		return eventbus.Permanent(fmt.Errorf("%s: route %s: %w", env.Type, routeID, err))
	}
	if err != nil {
		return fmt.Errorf("%s: route %s: %w", env.Type, routeID, err)
	}

	p := &rec.Plan
	before := p.Routes[i]
	p.Routes[i] = route
	*fam.applied(&versions) = env.AggregateVersion
	if rec.RouteVersions == nil {
		rec.RouteVersions = map[string]plans.RouteVersions{}
	}
	rec.RouteVersions[routeID] = versions
	for _, text := range texts {
		plans.AppendEvent(p, at, text)
	}
	finished := false
	if status := routeplan.StatusOf(p.Routes); status != p.Status {
		p.Status = status
		if status == contracts.PlanStatusDone {
			finished = true
			p.FinishedAt = contracts.At(at)
			plans.AppendEvent(p, at, contracts.Msg{Key: "srv.ops.event.allDone"})
		}
	}
	if err := plans.Save(ctx, tx, rec); err != nil {
		return err
	}
	if err := notify.PlanChanged(ctx, tx, p.UserID, p.ID); err != nil {
		return err
	}

	if news != nil {
		if title, body, ok := news(*p, before, route); ok {
			err := notify.Notice(ctx, tx, p.UserID, notices.Notice{At: at, PlanID: p.ID, Key: env.ID + ":event", Title: title, Body: body})
			if err != nil {
				return err
			}
		}
	}
	if finished {
		err := notify.Notice(ctx, tx, p.UserID, notices.Notice{
			At: at, PlanID: p.ID, Key: env.ID + ":done",
			Title: contracts.Msg{Key: "srv.ops.ntf.orderDoneTitle"},
			Body:  contracts.Msg{Key: "srv.ops.ntf.orderDoneBody", Params: map[string]any{"code": p.Code}},
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// driverNamed is how the shipper's texts name the driver: the events do not carry a name, so it is the word
// "driver" in the reader's language.
var driverNamed = contracts.Msg{Key: "srv.ops.driver"}

// assignedNotice tells the shipper a driver took the route.
func assignedNotice(p contracts.Plan, _, _ contracts.Route) (contracts.Msg, contracts.Msg, bool) {
	return contracts.Msg{Key: "srv.ops.ntf.driverAssignedTitle"},
		contracts.Msg{Key: "srv.ops.ntf.driverAssignedBody", Params: map[string]any{"driver": driverNamed, "code": p.Code}}, true
}

// startedNotice tells the shipper the driver is on the way to the first stop.
func startedNotice(p contracts.Plan, _, after contracts.Route) (contracts.Msg, contracts.Msg, bool) {
	place := ""
	if len(after.Stops) > 0 {
		place = after.Stops[0].Waypoint.Name
	}
	return contracts.Msg{Key: "srv.api.notif.driverEnRoute.title"},
		contracts.Msg{Key: "srv.api.notif.driverEnRoute.body", Params: map[string]any{"name": driverNamed, "place": place, "code": p.Code}}, true
}

// stopNotice builds the notice about one stop of the route, found by id.
func stopNotice(stopID string, build func(p contracts.Plan, stop contracts.RouteStop) (contracts.Msg, contracts.Msg, bool)) notice {
	return func(p contracts.Plan, _, after contracts.Route) (contracts.Msg, contracts.Msg, bool) {
		stop, ok := stopOf(after, stopID)
		if !ok {
			return contracts.Msg{}, contracts.Msg{}, false
		}
		return build(p, stop)
	}
}

func routeAssigned(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.RouteAssigned]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	if ev.Route.PlanID != ev.PlanID {
		return eventbus.Permanent(fmt.Errorf("%s: route %s belongs to plan %s, not %s", env.Type, ev.Route.ID, ev.Route.PlanID, ev.PlanID))
	}
	// The snapshot is already rebased to the driver's position, so it replaces the route whole. The line names the
	// driver of this order when the event carries the name (older events do not: then it says "Driver").
	return follow(ctx, tx, env, ev.PlanID, ev.Route.ID, dispatchEvents, env.OccurredAt.Time,
		func(contracts.Route) (contracts.Route, []contracts.Msg, error) {
			plate, known, err := fleet.Plate(ctx, tx, ev.VehicleID)
			if err != nil {
				return contracts.Route{}, nil, err
			}
			if !known {
				plate = ev.VehicleID
			}
			var driver any = contracts.Msg{Key: "srv.ops.driver"}
			if name := strings.TrimSpace(ev.DriverName); name != "" {
				driver = name
			}
			text := contracts.Msg{Key: "srv.ops.event.assigned", Params: map[string]any{"driver": driver, "plate": plate}}
			return ev.Route, []contracts.Msg{text}, nil
		}, assignedNotice)
}

func routeUnassigned(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.RouteUnassigned]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	return follow(ctx, tx, env, ev.PlanID, ev.RouteID, dispatchEvents, env.OccurredAt.Time,
		func(r contracts.Route) (contracts.Route, []contracts.Msg, error) {
			text := contracts.Msg{Key: "srv.ops.event.unassignedPlain"}
			if _, n, ok := strings.CutLast(r.ID, ":r"); ok {
				text = contracts.Msg{Key: "srv.ops.event.unassigned", Params: map[string]any{"n": n}}
			}
			return routeplan.Unassign(r), []contracts.Msg{text}, nil
		}, nil)
}

// routeStarted says "Driver departed" without a name: the event does not carry one.
func routeStarted(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.RouteStarted]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	return follow(ctx, tx, env, ev.PlanID, ev.RouteID, executionEvents, ev.StartedAt.Time,
		func(r contracts.Route) (contracts.Route, []contracts.Msg, error) {
			r, err := routeplan.Start(r, ev.StartedAt.Time)
			return r, []contracts.Msg{{Key: "srv.ops.seed.departed"}}, err
		}, startedNotice)
}

func stopArrived(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.StopArrived]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	return follow(ctx, tx, env, ev.PlanID, ev.RouteID, executionEvents, ev.ArrivedAt.Time,
		func(r contracts.Route) (contracts.Route, []contracts.Msg, error) {
			r, err := routeplan.Arrive(r, ev.StopID, ev.ArrivedAt.Time)
			if err != nil {
				return r, nil, err
			}
			stop, _ := stopOf(r, ev.StopID)
			return r, []contracts.Msg{{Key: "srv.ops.event.driverArrived", Params: map[string]any{"place": stop.Waypoint.Name}}}, nil
		},
		stopNotice(ev.StopID, func(p contracts.Plan, stop contracts.RouteStop) (contracts.Msg, contracts.Msg, bool) {
			title := "srv.ops.ntf.arrivedDropoffTitle"
			if stop.Kind == contracts.StopKindPickup {
				title = "srv.ops.ntf.arrivedPickupTitle"
			}
			return contracts.Msg{Key: title},
				contracts.Msg{Key: "srv.ops.ntf.arrivedBody", Params: map[string]any{"driver": driverNamed, "place": stop.Waypoint.Name, "code": p.Code}}, true
		}))
}

func stopCompleted(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.StopCompleted]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	// photoDropoff says that this event stored a photo as the proof of a drop-off. It is set when the transition runs,
	// not read off the event: follow drops an event that is old, already applied or for a plan that is over, and then
	// no proof was stored for the AI to read.
	photoDropoff := false
	err = follow(ctx, tx, env, ev.PlanID, ev.RouteID, executionEvents, ev.CompletedAt.Time,
		func(r contracts.Route) (contracts.Route, []contracts.Msg, error) {
			r, _, err := routeplan.Close(r, ev.StopID, ev.CompletedAt.Time, contracts.StopStatusDone, ev.Proof, nil)
			if err != nil {
				return r, nil, err
			}
			stop, _ := stopOf(r, ev.StopID)
			photoDropoff = stop.Kind == contracts.StopKindDropoff && ev.Proof != nil && ev.Proof.Kind == contracts.ProofKindPhoto && ev.Proof.Photo != ""
			key := "srv.ops.event.dropoffDone"
			if stop.Kind == contracts.StopKindPickup {
				key = "srv.ops.event.pickupDone"
			}
			return r, []contracts.Msg{{Key: key, Params: map[string]any{"place": stop.Waypoint.Name}}}, nil
		},
		// Only a delivery is news to the shipper; picking up is not.
		stopNotice(ev.StopID, func(p contracts.Plan, stop contracts.RouteStop) (contracts.Msg, contracts.Msg, bool) {
			return contracts.Msg{Key: "srv.ops.ntf.deliveredTitle"},
				contracts.Msg{Key: "srv.ops.ntf.deliveredBody", Params: map[string]any{"place": stop.Waypoint.Name, "code": p.Code}},
				stop.Kind == contracts.StopKindDropoff
		}))
	if err != nil || !photoDropoff {
		return err
	}
	// The AI reads the photo later, in the proofs worker: here a row is all it costs, and it commits with the proof
	// (a redelivery finds the row and adds nothing).
	return proofs.Enqueue(ctx, tx, ev.PlanID, ev.RouteID, ev.StopID)
}

func stopIssue(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.StopIssue]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	return follow(ctx, tx, env, ev.PlanID, ev.RouteID, executionEvents, ev.ReportedAt.Time,
		func(r contracts.Route) (contracts.Route, []contracts.Msg, error) {
			r, _, err := routeplan.Close(r, ev.StopID, ev.ReportedAt.Time, contracts.StopStatusProblem, nil, &ev.Issue)
			if err != nil {
				return r, nil, err
			}
			stop, _ := stopOf(r, ev.StopID)
			key := "srv.ops.event.dropoffProblem"
			if stop.Kind == contracts.StopKindPickup {
				key = "srv.ops.event.pickupProblem"
			}
			return r, []contracts.Msg{{Key: key, Params: map[string]any{"place": stop.Waypoint.Name, "issue": ev.Issue}}}, nil
		},
		stopNotice(ev.StopID, func(p contracts.Plan, stop contracts.RouteStop) (contracts.Msg, contracts.Msg, bool) {
			return contracts.Msg{Key: "srv.ops.ntf.problemCustomerTitle"},
				contracts.Msg{Key: "srv.ops.ntf.problemCustomerBody", Params: map[string]any{"issue": ev.Issue, "place": stop.Waypoint.Name, "code": p.Code}}, true
		}))
}

var errRouteNotFinished = errors.New("the route still has open stops")

// routeCompleted changes nothing when the route is already done: closing its last stop finished it. Otherwise a
// stop event is still on its way, so the event is retried.
func routeCompleted(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.RouteCompleted]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	return follow(ctx, tx, env, ev.PlanID, ev.RouteID, executionEvents, ev.FinishedAt.Time,
		func(r contracts.Route) (contracts.Route, []contracts.Msg, error) {
			if r.Status != contracts.RouteStatusDone {
				return r, nil, errRouteNotFinished
			}
			return r, nil, nil
		}, nil)
}

// routeDelayed records how late the driver is running, once it has got worse: the route's delayMin becomes the
// reported total, the timeline says by how much it grew, and the shipper is told about the new total. A report
// that is not worse than what is known (a repeat, a late older one, or a route that is not running) changes
// nothing.
func routeDelayed(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.RouteDelayed]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	return follow(ctx, tx, env, ev.PlanID, ev.RouteID, executionEvents, env.OccurredAt.Time,
		func(r contracts.Route) (contracts.Route, []contracts.Msg, error) {
			if r.Status != contracts.RouteStatusRunning || !(ev.DelayMin > r.DelayMin) {
				return r, nil, nil
			}
			grew := ev.DelayMin - r.DelayMin
			r.DelayMin = ev.DelayMin
			return r, []contracts.Msg{{Key: "srv.api.event.delayInjected", Params: map[string]any{"minutes": grew}}}, nil
		},
		func(p contracts.Plan, before, after contracts.Route) (contracts.Msg, contracts.Msg, bool) {
			return contracts.Msg{Key: "srv.api.notif.delayCustomer.title"},
				contracts.Msg{Key: "srv.api.notif.delayCustomer.body", Params: map[string]any{"code": p.Code, "count": int(math.Round(after.DelayMin))}},
				after.DelayMin > before.DelayMin
		})
}

func stopOf(r contracts.Route, stopID string) (contracts.RouteStop, bool) {
	i := slices.IndexFunc(r.Stops, func(s contracts.RouteStop) bool { return s.ID == stopID })
	if i < 0 {
		return contracts.RouteStop{}, false
	}
	return r.Stops[i], true
}
