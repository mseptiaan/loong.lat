package plans

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/pkg/routeplan"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/notify"
)

// Rescheduling timings, the mock's: a scheduled departure must be at least this far ahead, and "now" means the
// driver leaves a few minutes from now.
const (
	rescheduleLead = 20 * time.Minute
	departNowDelay = 6 * time.Minute
)

// Reschedule moves a confirmed plan that no driver has left with to another time (plans.reschedule). The routes
// are re-timed to it (every stop's ETA follows) and plan.rescheduled carries the result; back office then takes
// the routes back from their drivers and assigns them again for the new time.
func (s *Service) Reschedule(ctx context.Context, customerID, id string, schedule contracts.Schedule) (contracts.Plan, error) {
	if schedule.Mode != contracts.ScheduleModeNow && schedule.Mode != contracts.ScheduleModeScheduled {
		return contracts.Plan{}, platform.Invalid("schedule", contracts.Msg{Key: platform.MsgBadRequest})
	}
	var out contracts.Plan
	err := platform.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		rec, err := lockOwned(ctx, tx, customerID, id)
		if err != nil {
			return err
		}
		p := &rec.Plan
		if p.Status != contracts.PlanStatusAwaitingDriver && p.Status != contracts.PlanStatusAssigned {
			return platform.Conflict(contracts.Msg{Key: "srv.api.rescheduleLate"})
		}
		if p.PendingAction != "" {
			return platform.Conflict(contracts.Msg{Key: msgCancelPending})
		}

		now := s.now()
		startAt := now.Add(departNowDelay)
		if schedule.Mode == contracts.ScheduleModeScheduled {
			if schedule.Window == nil || schedule.Window.From.IsZero() || schedule.Window.From.Time.Before(now.Add(rescheduleLead)) {
				return platform.Invalid("schedule", contracts.Msg{Key: "srv.api.scheduleTooSoon"})
			}
			startAt = schedule.Window.From.Time
		}

		p.Schedule = schedule
		// Routes that have not left are re-timed from where they start (a driver's position, once assigned, is
		// already the start). A route that is finished keeps its history.
		for i, r := range p.Routes {
			if r.Status == contracts.RouteStatusAwaitingDriver || r.Status == contracts.RouteStatusAssigned {
				p.Routes[i] = routeplan.RebaseStart(r, r.StartPosition, startAt)
			}
		}
		text := contracts.Msg{Key: "srv.api.event.rescheduledNow"}
		if schedule.Mode == contracts.ScheduleModeScheduled {
			text = contracts.Msg{Key: "srv.api.event.rescheduled", Params: map[string]any{"time": startAt.UnixMilli()}}
		}
		AppendEvent(p, now, text)
		rec.Version++
		if err := Save(ctx, tx, rec); err != nil {
			return err
		}
		err = s.pub.Enqueue(ctx, tx, eventbus.Event{
			Type: contracts.TypePlanRescheduled, Key: p.ID, AggregateVersion: rec.Version,
			Data: contracts.PlanRescheduled{PlanID: p.ID, Schedule: p.Schedule, Routes: p.Routes},
		})
		if err != nil {
			return err
		}
		out = *p
		return notify.PlanChanged(ctx, tx, customerID, id)
	})
	return out, err
}
