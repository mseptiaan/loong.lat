package execution

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/drivers"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/live"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/tasks"
)

// Service executes the driver's routes. It is the authority on execution state: every action changes the route,
// the driver and the outbox in one transaction.
type Service struct {
	db  *pgxpool.Pool
	pub *eventbus.Publisher
	now func() time.Time
}

func NewService(db *pgxpool.Pool, pub *eventbus.Publisher, now func() time.Time) *Service {
	return &Service{db: db, pub: pub, now: now}
}

// Routes is driver.routes: the driver's assigned and running routes, delivery codes removed.
func (s *Service) Routes(ctx context.Context, driverID string) ([]tasks.DriverRoute, error) {
	list, err := tasks.Active(ctx, s.db, driverID)
	if err != nil {
		return nil, err
	}
	out := make([]tasks.DriverRoute, len(list))
	for i, t := range list {
		out[i] = t.ForApp()
	}
	return out, nil
}

// Start is driver.startRoute.
func (s *Service) Start(ctx context.Context, driverID, planID, routeID string) (tasks.Task, error) {
	return s.act(ctx, driverID, planID, routeID, func(t tasks.Task, at time.Time) (Step, error) {
		return start(t.Route, at)
	})
}

// Arrive is driver.arrive.
func (s *Service) Arrive(ctx context.Context, driverID, planID, routeID, stopID string) (tasks.Task, error) {
	return s.act(ctx, driverID, planID, routeID, func(t tasks.Task, at time.Time) (Step, error) {
		return arrive(t.Route, stopID, at)
	})
}

// Complete is driver.complete.
func (s *Service) Complete(ctx context.Context, driverID, planID, routeID, stopID string, proof *StopProof) (tasks.Task, error) {
	return s.act(ctx, driverID, planID, routeID, func(t tasks.Task, at time.Time) (Step, error) {
		return complete(t.Route, stopID, proof, at)
	})
}

// ReportIssue is driver.reportIssue.
func (s *Service) ReportIssue(ctx context.Context, driverID, planID, routeID, stopID string, kind contracts.IssueKind, note string) (tasks.Task, error) {
	return s.act(ctx, driverID, planID, routeID, func(t tasks.Task, at time.Time) (Step, error) {
		return reportIssue(t.Route, stopID, kind, note, at)
	})
}

// act applies one action of the driver to one of their routes. The route row is locked first, so the actions of
// a route queue up and see each other's results, and those of a plan cancel's vote: a route it holds cannot be
// acted on until the cancel commits (the task is gone) or aborts. A replay returns the stored task untouched.
// Otherwise the new route, the driver's state and one outbox row per fact (each the next exec_version) commit
// together or not at all.
func (s *Service) act(ctx context.Context, driverID, planID, routeID string, do func(tasks.Task, time.Time) (Step, error)) (tasks.Task, error) {
	at := contracts.At(s.now()).Time
	var task tasks.Task
	err := platform.WithTx(ctx, s.db, func(tx pgx.Tx) error {
		t, err := tasks.Lock(ctx, tx, routeID)
		switch {
		case errors.Is(err, tasks.ErrNotFound):
			return platform.NotFound(contracts.Msg{Key: keyRouteNotFound})
		case err != nil:
			return err
		case t.PlanID != planID:
			return platform.NotFound(contracts.Msg{Key: keyRouteNotFound})
		case t.DriverID != driverID:
			return platform.Denied(contracts.Msg{Key: keyNotYourRoute})
		case t.Cancelled():
			return platform.NotFound(contracts.Msg{Key: keyRouteNotFound}) // the plan was cancelled: no task any more
		case t.Hold != "":
			return conflict(keyRouteCancelling) // a cancel waits for the votes and this route has voted: it stays as it is
		}
		step, err := do(t, at)
		if err != nil {
			return err
		}
		task = t
		if step.Replay() {
			return nil
		}

		task.Route = step.Route
		for _, fact := range step.Facts {
			task.ExecVersion++
			err := s.pub.Enqueue(ctx, tx, eventbus.Event{Type: fact.Type, Key: routeID, AggregateVersion: task.ExecVersion, Data: fact.Data})
			if err != nil {
				return err
			}
		}
		if err := tasks.Put(ctx, tx, task, at); err != nil {
			return err
		}
		if err := s.settleDriver(ctx, tx, driverID, step, at); err != nil {
			return err
		}
		return live.ToDriver(ctx, tx, driverID, live.PlansChanged(planID))
	})
	return task, err
}

// settleDriver applies a step's effect to the driver: on duty while a route runs, at the stop they reached,
// one more trip when the route is finished. The route is saved before this, which FinishTrip relies on.
func (s *Service) settleDriver(ctx context.Context, tx pgx.Tx, driverID string, step Step, at time.Time) error {
	if step.Started {
		if err := drivers.MarkOnDuty(ctx, tx, driverID, at); err != nil {
			return err
		}
	}
	if step.Position != nil {
		if _, err := drivers.RecordPosition(ctx, tx, driverID, *step.Position, at); err != nil {
			return err
		}
	}
	if step.Finished {
		return drivers.FinishTrip(ctx, tx, driverID, at)
	}
	return nil
}
