package projections

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/routeplan"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/drivers"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/live"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/notify"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/tasks"
)

// routeAssigned creates the task, or replaces the snapshot of one that has not started. Older versions are
// dropped; so is anything for a route that already runs or is done, because execution is this service's own and
// the snapshot of dispatch would wipe out what the driver did. It is also not applied for a route a plan cancel has
// voted for before it was known (a tombstone) and for one the cancel closed: the plan is going or gone. (For the
// tombstone it is parked, and applied after all if the cancel is aborted.) A driver who newly has the route is told.
func (p *Projections) routeAssigned(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	e, err := env.Payload[contracts.RouteAssigned]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	if e.PlanID == "" || e.DriverID == "" || e.Route.ID == "" {
		return eventbus.Permanent(errors.New("route.assigned without plan, driver or route id"))
	}
	// Serialised with the votes of a plan cancel: either the vote sees this route, or this sees the tombstone.
	if err := tasks.LockRoute(ctx, tx, e.Route.ID); err != nil {
		return err
	}
	if tombstoned, err := tasks.Tombstoned(ctx, tx, e.Route.ID); err != nil {
		return err
	} else if tombstoned {
		p.log.InfoContext(ctx, "route.assigned for a route a plan cancel voted away ignored", "routeId", e.Route.ID, "planId", e.PlanID)
		return p.park(ctx, tx, env, e.PlanID, e.Route.ID, env.Data)
	}
	if _, err := drivers.Get(ctx, tx, e.DriverID); err != nil {
		if errors.Is(err, drivers.ErrNotFound) {
			// The driver's provisioning travels on another topic and may simply not have arrived: retry.
			return fmt.Errorf("driver %s is not provisioned yet: %w", e.DriverID, err)
		}
		return err
	}

	assignedAt := e.Route.AssignedAt.Time
	if assignedAt.IsZero() {
		assignedAt = env.OccurredAt.Time
	}
	route := routeplan.Assign(e.Route, e.DriverID, e.VehicleID, assignedAt)
	if !e.StartAt.IsZero() {
		route.StartAt = e.StartAt
	}
	route.PlanID = e.PlanID

	task := tasks.Task{PlanID: e.PlanID, PlanCode: e.PlanCode, CustomerName: e.CustomerName, DriverID: e.DriverID, Route: route, AssignVersion: env.AggregateVersion}
	previousDriver := ""
	current, err := tasks.Lock(ctx, tx, route.ID)
	switch {
	case errors.Is(err, tasks.ErrNotFound):
	case err != nil:
		return err
	case env.AggregateVersion <= current.AssignVersion:
		p.log.DebugContext(ctx, "stale route.assigned dropped", "routeId", route.ID, "version", env.AggregateVersion, "have", current.AssignVersion)
		return nil
	case current.Cancelled():
		p.log.InfoContext(ctx, "route.assigned for a cancelled route ignored", "routeId", route.ID)
		return nil
	case current.Started():
		p.log.WarnContext(ctx, "route.assigned for a route that is already running or done ignored", "routeId", route.ID, "status", current.Route.Status)
		return nil
	default:
		task.ExecVersion, previousDriver = current.ExecVersion, current.DriverID
	}

	if err := tasks.Put(ctx, tx, task, env.OccurredAt.Time); err != nil {
		return err
	}
	if previousDriver != e.DriverID {
		if err := live.ToDriver(ctx, tx, previousDriver, live.PlansChanged(e.PlanID)); err != nil {
			return err
		}
	}
	if err := live.ToDriver(ctx, tx, e.DriverID, live.PlansChanged(e.PlanID)); err != nil {
		return err
	}
	if previousDriver == e.DriverID {
		return nil
	}
	return notify.NewJob(ctx, tx, task, env.ID, env.OccurredAt.Time)
}

// routeUnassigned takes the route back from the driver and keeps the row, so that an older assignment arriving
// late cannot bring the task back. The driver is told why (the event's Reason), except when they went offline
// themselves. A route that already runs is not taken back: that is logged, not an error.
func (p *Projections) routeUnassigned(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	e, err := env.Payload[contracts.RouteUnassigned]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	if e.RouteID == "" {
		return eventbus.Permanent(errors.New("route.unassigned without route id"))
	}
	if err := tasks.LockRoute(ctx, tx, e.RouteID); err != nil {
		return err
	}
	task, err := tasks.Lock(ctx, tx, e.RouteID)
	switch {
	case errors.Is(err, tasks.ErrNotFound):
		if tombstoned, err := tasks.Tombstoned(ctx, tx, e.RouteID); err != nil || tombstoned {
			// the newest word of dispatch on a route a cancel voted away: if it is aborted, this is what holds
			return cmp.Or(err, p.park(ctx, tx, env, e.PlanID, e.RouteID, nil))
		}
		p.log.DebugContext(ctx, "route.unassigned for an unknown route ignored", "routeId", e.RouteID)
		return nil
	case err != nil:
		return err
	case env.AggregateVersion <= task.AssignVersion:
		p.log.DebugContext(ctx, "stale route.unassigned dropped", "routeId", e.RouteID, "version", env.AggregateVersion, "have", task.AssignVersion)
		return nil
	case task.Cancelled():
		p.log.DebugContext(ctx, "route.unassigned for a cancelled route ignored", "routeId", e.RouteID)
		return nil
	case task.Started():
		p.log.WarnContext(ctx, "route.unassigned for a route that is already running or done refused", "routeId", e.RouteID, "status", task.Route.Status)
		return nil
	}

	driverID := task.DriverID
	task.Route, task.DriverID, task.AssignVersion = routeplan.Unassign(task.Route), "", env.AggregateVersion
	if err := tasks.Put(ctx, tx, task, env.OccurredAt.Time); err != nil {
		return err
	}
	if err := live.ToDriver(ctx, tx, driverID, live.PlansChanged(task.PlanID)); err != nil {
		return err
	}
	job := notify.Job{RouteID: e.RouteID, PlanID: task.PlanID, PlanCode: task.PlanCode, DriverID: driverID}
	return notify.Unassigned(ctx, tx, job, e.Reason, env.ID, env.OccurredAt.Time)
}

// park keeps what dispatch said (an assignment, or with data nil an unassignment) about a route whose cancel
// voted it away, in case the cancel is aborted: dispatch does not say it again. An event without a plan id is
// kept under the one of the route.
func (p *Projections) park(ctx context.Context, tx pgx.Tx, env contracts.Envelope, planID, routeID string, data []byte) error {
	if planID == "" {
		planID = strings.SplitN(routeID, ":", 2)[0]
	}
	return tasks.Park(ctx, tx, tasks.Parked{
		RouteID: routeID, PlanID: planID, Version: env.AggregateVersion, EventID: env.ID, OccurredAt: env.OccurredAt.Time, Assigned: data,
	})
}
