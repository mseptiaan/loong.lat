package execution

import (
	"context"
	"log/slog"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/pkg/routeplan"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/tasks"
)

// DelayStepMin is the size of a delay step in minutes: route.delayed is published when a route's delay reaches
// one step (5 minutes) and again each time it reaches the next.
const DelayStepMin = 5

// DefaultSweepInterval is how often the sweeper looks at the running routes when it is not told otherwise.
const DefaultSweepInterval = 30 * time.Second

// delayOf is how late a running route is, and at which stop. A running route's clock is the one of
// routeplan.ReanchorETAs: when the route starts, and each time a stop closes, the arrival time of every stop
// still ahead is counted from that moment, so the ETA of the stop the driver is on (the first open one) is when
// they should get there. On the way to it the delay is the time since that ETA passed. A driver who is at the stop
// is not late any more, nor later by waiting there: the delay is how late they arrived. Never negative; zero for a
// route that does not run.
func delayOf(r contracts.Route, now time.Time) (stopID string, minutes float64) {
	i := routeplan.OpenIndex(r)
	if r.Status != contracts.RouteStatusRunning || i < 0 {
		return "", 0
	}
	stop := r.Stops[i]
	if stop.ArriveAt.IsZero() {
		return stop.ID, 0
	}
	var late time.Duration
	switch stop.Status {
	case contracts.StopStatusEnRoute:
		late = now.Sub(stop.ArriveAt.Time)
	case contracts.StopStatusArrived:
		late = stop.ArrivedAt.Sub(stop.ArriveAt.Time)
	}
	return stop.ID, max(0, late.Minutes())
}

// delayStep is the number of whole steps a delay has reached: 0 below the first step.
func delayStep(minutes float64) int { return int(minutes / DelayStepMin) }

// due says whether a route.delayed is owed for the delay at the stop: it has reached a step that was not
// published for that stop yet. Falling back, or staying within the published step, owes nothing.
func due(t tasks.Task, stopID string, minutes float64) bool {
	step := delayStep(minutes)
	return step >= 1 && (t.DelayStop != stopID || step > t.DelayStep)
}

// Sweeper publishes route.delayed for running routes that fall behind. The last step published for a route is
// stored with it, so a restart, or another replica sweeping at the same time, does not repeat a step.
type Sweeper struct {
	db       *pgxpool.Pool
	pub      *eventbus.Publisher
	now      func() time.Time
	log      *slog.Logger
	interval time.Duration
}

// NewSweeper returns a sweeper that looks every interval (DefaultSweepInterval when not positive) at the time of now.
func NewSweeper(db *pgxpool.Pool, pub *eventbus.Publisher, now func() time.Time, log *slog.Logger, interval time.Duration) *Sweeper {
	if interval <= 0 {
		interval = DefaultSweepInterval
	}
	return &Sweeper{db: db, pub: pub, now: now, log: log, interval: interval}
}

// Run sweeps at once and then every interval until ctx ends. A failed sweep is logged and tried again at the
// next tick: the delay is still there then.
func (s *Sweeper) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		if _, err := s.Sweep(ctx); err != nil && ctx.Err() == nil {
			s.log.ErrorContext(ctx, "delay sweep failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Sweep looks at every running route once, publishes route.delayed for those that owe it, and returns how many
// it published. A route that fails does not stop the others; the first failure is returned at the end.
func (s *Sweeper) Sweep(ctx context.Context) (published int, err error) {
	now := s.now()
	running, err := tasks.Running(ctx, s.db)
	if err != nil {
		return 0, err
	}
	var firstErr error
	for _, t := range running {
		if stop, minutes := delayOf(t.Route, now); !due(t, stop, minutes) {
			continue
		}
		sent, err := s.publish(ctx, t.Route.ID, now)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if sent {
			published++
		}
	}
	return published, firstErr
}

// publish re-reads the route under its lock, so that a step another sweeper or the driver's own progress has
// settled meanwhile is not published, and sends route.delayed as the route's next execution event.
func (s *Sweeper) publish(ctx context.Context, routeID string, now time.Time) (sent bool, err error) {
	err = platform.WithTx(ctx, s.db, func(tx pgx.Tx) error {
		t, err := tasks.Lock(ctx, tx, routeID)
		if err != nil {
			return err
		}
		stop, minutes := delayOf(t.Route, now)
		if t.Cancelled() || !due(t, stop, minutes) {
			return nil
		}
		t.ExecVersion++
		err = s.pub.Enqueue(ctx, tx, eventbus.Event{
			Type: contracts.TypeRouteDelayed, Key: routeID, AggregateVersion: t.ExecVersion,
			Data: contracts.RouteDelayed{PlanID: t.PlanID, RouteID: routeID, DelayMin: math.Floor(minutes)},
		})
		if err != nil {
			return err
		}
		sent = true
		return tasks.MarkDelay(ctx, tx, routeID, t.ExecVersion, stop, delayStep(minutes))
	})
	return sent && err == nil, err
}
