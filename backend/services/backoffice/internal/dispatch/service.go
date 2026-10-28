package dispatch

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/pkg/routeplan"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/alerts"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/apix"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/changes"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/fleet"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/pg"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/plans"
)

// Message keys of the frontend catalog.
const (
	msgDriverNotFound      = "srv.api.driverNotFound"
	msgPlanInactive        = "srv.ops.err.planInactive"
	msgRouteHasDriver      = "srv.ops.err.routeHasDriver"
	msgDriverUnavailable   = "srv.ops.err.driverUnavailable"
	msgDriverNoVehicle     = "srv.ops.err.driverNoVehicle"
	msgVehicleMismatch     = "srv.ops.err.vehicleMismatch"
	msgOverCapacity        = "srv.ops.err.overCapacity"
	msgRouteStarted        = "srv.ops.err.routeStarted"
	msgAllRoutesHaveDriver = "srv.api.allRoutesHaveDriver"
	msgNoDriverForRoute    = "srv.api.noDriverForRoute"

	msgAssigned         = "srv.ops.event.assigned"
	msgAutoAssignedWhy  = "srv.ops.event.autoAssignedWhy"
	msgNoDriverNow      = "srv.ops.event.noDriverNow"
	msgNoDriverSchedule = "srv.ops.event.noDriverScheduled"
	msgPickNearest      = "srv.ops.pick.nearest"
)

// MsgRouteFrozen is a key the catalog does not have yet: "This plan is being cancelled; its routes cannot be
// assigned right now." It answers a manual assignment (or unassignment) on a plan with a cancel pending.
const MsgRouteFrozen = "srv.ops.err.routeFrozen"

// dispatchLock is the advisory-lock key that serializes decisions about drivers.
const dispatchLock int64 = 0x6c6f6f6e675f6473 // "loong_ds"

// settle is how long the worker waits after being woken: whoever woke it is still inside the transaction that
// makes the news visible.
const settle = 100 * time.Millisecond

// Service makes the dispatch decisions.
type Service struct {
	db    *pgxpool.Pool
	pub   *eventbus.Publisher
	fleet *fleet.Service
	feed  *changes.Feed
	log   *slog.Logger
	now   func() time.Time
	wake  chan struct{}

	alerts *alerts.Notifier
}

func New(db *pgxpool.Pool, pub *eventbus.Publisher, fleet *fleet.Service, feed *changes.Feed, log *slog.Logger, now func() time.Time) *Service {
	return &Service{db: db, pub: pub, fleet: fleet, feed: feed, log: log, now: now, wake: make(chan struct{}, 1), alerts: alerts.New(feed)}
}

// Kick asks the worker to look for waiting routes soon. It never blocks and does nothing when no worker runs.
func (s *Service) Kick() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// decide starts a decision about a plan's routes: it locks the plan, then the dispatch lock, so that decisions
// are made one at a time and each sees every driver's schedule as the last one left it. (Everything else takes
// the plan lock first and never the dispatch lock, so locks are always taken in the same order.)
func (s *Service) decide(ctx context.Context, tx pgx.Tx, planID string) (*plans.Aggregate, error) {
	a, err := plans.Lock(ctx, tx, planID)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, dispatchLock); err != nil {
		return nil, fmt.Errorf("dispatch: lock: %w", err)
	}
	return a, nil
}

// world is the fleet as a decision sees it: every driver with his vehicle, and the routes keeping each busy.
type world struct {
	members []fleet.Member
	busy    map[string][]contracts.Route
	now     time.Time
}

func (s *Service) world(ctx context.Context, q pg.Querier) (world, error) {
	members, err := s.fleet.Roster(ctx, q)
	if err != nil {
		return world{}, err
	}
	active, err := plans.ActiveRoutes(ctx, q)
	if err != nil {
		return world{}, err
	}
	busy := map[string][]contracts.Route{}
	for _, r := range active {
		busy[r.DriverID] = append(busy[r.DriverID], r)
	}
	return world{members: members, busy: busy, now: s.now()}, nil
}

// assess measures every driver against a route.
func (w world) assess(plan contracts.Plan, route contracts.Route) []verdict {
	load := LoadOf(plan, route)
	out := make([]verdict, len(w.members))
	for i, m := range w.members {
		out[i] = assess(m, w.busy[m.Driver.ID], plan, route, load, w.now)
	}
	return out
}

// pick returns the drivers who may take the route, best first.
func (w world) pick(plan contracts.Plan, route contracts.Route) []candidate {
	var cands []candidate
	for _, v := range w.assess(plan, route) {
		if v.refusal == assignable {
			cands = append(cands, v.candidate)
		}
	}
	nearestFirst(cands)
	return cands
}

// notOpen is why a route cannot be given to a driver now, or nil when it can. A plan with a cancel under way is
// frozen: whatever the cancel decides, a driver must not be handed a route that may be cancelled any moment.
func notOpen(a *plans.Aggregate, route *plans.Route) *platform.APIError {
	switch {
	case a.Plan.Status == contracts.PlanStatusCancelled || a.Plan.Status == contracts.PlanStatusDone:
		return platform.Conflict(contracts.Msg{Key: msgPlanInactive})
	case route.Status != contracts.RouteStatusAwaitingDriver:
		return platform.Conflict(contracts.Msg{Key: msgRouteHasDriver})
	case route.Hold != "" || a.Plan.PendingAction != "":
		return platform.Conflict(contracts.Msg{Key: MsgRouteFrozen})
	}
	return nil
}

// refuse is the conflict that tells the admin why a driver cannot take the route.
func (v verdict) refuse(route contracts.Route) error {
	driver, vehicle := v.member.Driver, v.member.Vehicle
	var msg contracts.Msg
	switch v.refusal {
	case unavailable:
		msg = contracts.Msg{Key: msgDriverUnavailable, Params: map[string]any{"driver": driver.Name}}
	case noVehicle:
		msg = contracts.Msg{Key: msgDriverNoVehicle, Params: map[string]any{"driver": driver.Name}}
	case wrongType:
		msg = contracts.Msg{Key: msgVehicleMismatch, Params: map[string]any{
			"need": vehicleWord(route.VehicleType), "driver": driver.Name, "have": vehicleWord(vehicle.Type),
		}}
	case overCapacity:
		msg = contracts.Msg{Key: msgOverCapacity, Params: map[string]any{"model": vehicle.Model}}
	default:
		return nil
	}
	return apix.Field(platform.Conflict(msg), "driverId")
}

func vehicleWord(t contracts.VehicleType) contracts.Msg {
	return contracts.Msg{Key: "srv.ops.vehicle." + string(t)}
}

// Assign gives a route to a driver chosen by an admin. The driver must pass the same checks automatic dispatch
// applies, and the route must be waiting for one.
func (s *Service) Assign(ctx context.Context, planID, routeID, driverID string) (contracts.Plan, error) {
	var out contracts.Plan
	err := platform.WithTx(ctx, s.db, func(tx pgx.Tx) error {
		a, err := s.decide(ctx, tx, planID)
		if err != nil {
			return err
		}
		route, err := a.Route(routeID)
		if err != nil {
			return err
		}
		if err := notOpen(a, route); err != nil {
			return err
		}
		w, err := s.world(ctx, tx)
		if err != nil {
			return err
		}
		for _, v := range w.assess(a.Plan, route.Route) {
			if v.member.Driver.ID != driverID {
				continue
			}
			if err := v.refuse(route.Route); err != nil {
				return err
			}
			if err := s.assign(ctx, tx, a, route, v.candidate, nil); err != nil {
				return err
			}
			out = a.Snapshot()
			return nil
		}
		return platform.NotFound(contracts.Msg{Key: msgDriverNotFound})
	})
	if err != nil {
		return contracts.Plan{}, err
	}
	s.changed(planID)
	return out, nil
}

// Unassign takes a route back from its driver; it becomes available to dispatch again. A route already
// waiting is left as it is, and one that has started cannot be taken back.
func (s *Service) Unassign(ctx context.Context, planID, routeID string) (contracts.Plan, error) {
	var out contracts.Plan
	released := false
	err := platform.WithTx(ctx, s.db, func(tx pgx.Tx) error {
		a, err := plans.Lock(ctx, tx, planID)
		if err != nil {
			return err
		}
		route, err := a.Route(routeID)
		if err != nil {
			return err
		}
		out = a.Snapshot()
		switch {
		case route.Status == contracts.RouteStatusAwaitingDriver:
			return nil
		case route.Status == contracts.RouteStatusRunning || route.Status == contracts.RouteStatusDone:
			return platform.Conflict(contracts.Msg{Key: msgRouteStarted})
		case a.Frozen():
			return platform.Conflict(contracts.Msg{Key: MsgRouteFrozen})
		}

		now := s.now()
		if err := plans.Release(ctx, tx, s.pub, a, route, contracts.UnassignAdmin, plans.UnassignedNote(route.ID), now); err != nil {
			return err
		}
		a.Sync(now)
		if err := plans.Save(ctx, tx, a); err != nil {
			return err
		}
		out, released = a.Snapshot(), true
		return nil
	})
	if err != nil {
		return contracts.Plan{}, err
	}
	if released {
		s.changed(planID)
		s.Kick()
	}
	return out, nil
}

// AutoAssignPlan gives every waiting route of a plan its best driver, all or none: a route no driver can take
// refuses the whole request.
func (s *Service) AutoAssignPlan(ctx context.Context, planID string) (contracts.Plan, error) {
	var out contracts.Plan
	err := platform.WithTx(ctx, s.db, func(tx pgx.Tx) error {
		a, err := s.decide(ctx, tx, planID)
		if err != nil {
			return err
		}
		switch {
		case a.Plan.Status == contracts.PlanStatusCancelled || a.Plan.Status == contracts.PlanStatusDone:
			return platform.Conflict(contracts.Msg{Key: msgPlanInactive})
		case a.Frozen():
			return platform.Conflict(contracts.Msg{Key: MsgRouteFrozen})
		}
		assigned := false
		for i := range a.Routes {
			route := &a.Routes[i]
			if route.Status != contracts.RouteStatusAwaitingDriver {
				continue
			}
			// A fresh view per route: the previous one just took a driver.
			w, err := s.world(ctx, tx)
			if err != nil {
				return err
			}
			cands := w.pick(a.Plan, route.Route)
			if len(cands) == 0 {
				return platform.Conflict(contracts.Msg{Key: msgNoDriverForRoute, Params: map[string]any{
					"vehicle": contracts.Msg{Key: "srv.api.vehicleLower." + string(route.VehicleType)},
					"route":   plans.RouteNumber(route.ID),
				}})
			}
			if err := s.assign(ctx, tx, a, route, cands[0], new(pickReason(cands[0]))); err != nil {
				return err
			}
			assigned = true
		}
		if !assigned {
			return platform.Conflict(contracts.Msg{Key: msgAllRoutesHaveDriver})
		}
		out = a.Snapshot()
		return nil
	})
	if err != nil {
		return contracts.Plan{}, err
	}
	s.changed(planID)
	return out, nil
}

// AutoAssignRoute tries to give one waiting route its best driver and reports whether it did. A route that
// already has a driver, or whose plan is closed, is not an error: it is a replayed or concurrent attempt that
// found the work done. When no driver fits, the plan's timeline says so once.
func (s *Service) AutoAssignRoute(ctx context.Context, w plans.Waiting) (bool, error) {
	assigned, noted := false, false
	err := platform.WithTx(ctx, s.db, func(tx pgx.Tx) error {
		a, err := s.decide(ctx, tx, w.PlanID)
		if err != nil {
			return err
		}
		route, err := a.Route(w.RouteID)
		if err != nil {
			return err
		}
		if notOpen(a, route) != nil {
			return nil
		}
		world, err := s.world(ctx, tx)
		if err != nil {
			return err
		}
		cands := world.pick(a.Plan, route.Route)
		if len(cands) > 0 {
			assigned = true
			return s.assign(ctx, tx, a, route, cands[0], new(pickReason(cands[0])))
		}
		key := msgNoDriverNow
		if a.Plan.Schedule.Mode == contracts.ScheduleModeScheduled {
			key = msgNoDriverSchedule
		}
		now := s.now()
		note := contracts.Msg{Key: key, Params: map[string]any{"vehicle": vehicleWord(route.VehicleType)}}
		if noted = a.NoteOnce(apix.NewID("evt"), now, note); noted {
			a.Sync(now)
			if err := plans.Save(ctx, tx, a); err != nil {
				return err
			}
			// Nobody to take it: the admins are told once per plan, as the mock does when a new plan is left
			// without a driver.
			customer, err := plans.CustomerName(ctx, tx, a.Plan.UserID)
			if err != nil {
				return fmt.Errorf("dispatch: customer name: %w", err)
			}
			return s.alerts.NewPlan(ctx, tx, plans.NeedsDriverKey(a.Plan.ID), now, a.Plan.ID, a.Plan.Code, customer, len(a.Routes))
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	if assigned || noted {
		s.changed(w.PlanID)
	}
	return assigned, nil
}

// assign gives the route to the candidate inside tx: the route is moved to the driver's position and set to
// leave at its time, handed over, and announced as route.assigned. The plan is saved here, so the caller may
// go on to the next route. reason, when set, is why an automatic choice fell on this driver.
func (s *Service) assign(ctx context.Context, tx pgx.Tx, a *plans.Aggregate, route *plans.Route, c candidate, reason *contracts.Msg) error {
	now := s.now()
	driver, vehicle := c.member.Driver, *c.member.Vehicle
	from := route.StartPosition
	if driver.Position != nil {
		from = *driver.Position
	}
	startAt := departure(a.Plan, route.Route, from, now)
	route.Route = routeplan.Assign(routeplan.RebaseStart(route.Route, from, startAt), driver.ID, vehicle.ID, now)
	route.AssignVersion++

	a.Note(apix.NewID("evt"), now, assignedNote(driver, vehicle, reason))
	a.Sync(now)
	if err := plans.Save(ctx, tx, a); err != nil {
		return err
	}
	customer, err := plans.CustomerName(ctx, tx, a.Plan.UserID)
	if err != nil {
		return fmt.Errorf("dispatch: customer name: %w", err)
	}
	return s.pub.Enqueue(ctx, tx, eventbus.Event{
		Type: contracts.TypeRouteAssigned, Key: route.ID, AggregateVersion: route.AssignVersion,
		Data: contracts.RouteAssigned{
			PlanID: a.Plan.ID, PlanCode: a.Plan.Code, CustomerName: customer,
			DriverID: driver.ID, DriverName: driver.Name, VehicleID: vehicle.ID, StartAt: route.StartAt, Route: route.Route,
		},
	})
}

// changed tells the consoles a plan and the fleet's duty roster changed.
func (s *Service) changed(planID string) {
	s.feed.Plan(planID)
	s.feed.Fleet()
}

// pickReason says why automatic dispatch chose a driver, for the plan's timeline.
func pickReason(c candidate) contracts.Msg {
	return contracts.Msg{Key: msgPickNearest, Params: map[string]any{"km": routeplan.Round1(c.roadKm), "eta": c.etaMin}}
}

func assignedNote(d fleet.Driver, v contracts.Vehicle, reason *contracts.Msg) contracts.Msg {
	params := map[string]any{"driver": d.Name, "plate": v.Plate}
	if reason == nil {
		return contracts.Msg{Key: msgAssigned, Params: params}
	}
	params["reason"] = *reason
	return contracts.Msg{Key: msgAutoAssignedWhy, Params: params}
}
