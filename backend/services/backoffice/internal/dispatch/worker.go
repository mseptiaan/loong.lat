package dispatch

import (
	"context"
	"time"

	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/plans"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/settings"
)

// Run is the dispatch worker: while ctx lives it gives waiting routes drivers, when automatic dispatch is on.
// It looks every sweep, and sooner when something may have made a route assignable (Kick): a plan was
// confirmed, a driver came online, a route was released, automatic dispatch was switched on. Each attempt is its
// own transaction under the locks of Assign, so a replayed or concurrent attempt finds the route taken and does
// nothing; several workers may run side by side.
func (s *Service) Run(ctx context.Context, sweep time.Duration) error {
	ticker := time.NewTicker(sweep)
	defer ticker.Stop()
	for ctx.Err() == nil {
		s.pass(ctx)
		select {
		case <-ctx.Done():
		case <-ticker.C:
		case <-s.wake:
			sleep(ctx, settle)
		}
	}
	return nil
}

// pass makes one round: it first takes back the routes of drivers who went silent, then gives the waiting routes
// (those too included) a driver. Failures are logged and left for the next round.
func (s *Service) pass(ctx context.Context) {
	if _, err := s.ReleaseSilent(ctx); err != nil && ctx.Err() == nil {
		s.log.ErrorContext(ctx, "dispatch: release routes of silent drivers", "error", err)
	}
	auto, err := settings.DispatchAuto(ctx, s.db)
	if err != nil || !auto {
		if err != nil && ctx.Err() == nil {
			s.log.ErrorContext(ctx, "dispatch: read settings", "error", err)
		}
		return
	}
	waiting, err := plans.WaitingRoutes(ctx, s.db, s.now().Add(CommitLead))
	if err != nil {
		if ctx.Err() == nil {
			s.log.ErrorContext(ctx, "dispatch: list waiting routes", "error", err)
		}
		return
	}
	for _, w := range waiting {
		if _, err := s.AutoAssignRoute(ctx, w); err != nil && ctx.Err() == nil {
			s.log.ErrorContext(ctx, "dispatch: assign route", "plan", w.PlanID, "route", w.RouteID, "error", err)
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
