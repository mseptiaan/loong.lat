package dispatch

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/fleet"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/plans"
)

// msgDriverOfflineReleased is the timeline line of a route returned to the queue because its driver is gone.
const msgDriverOfflineReleased = "srv.api.event.driverOfflineReleased"

// msgDriverSilentReleased is the same line when the driver did not leave: his app simply stopped reporting.
const msgDriverSilentReleased = "srv.api.event.driverSilentReleased"

// DriverOffline (loong.driver.v1) records that a driver went offline and returns every route of his that has not
// started to the queue (route.unassigned, reason driver_offline), whatever its departure: he is not coming. A
// route under way is never released. The admins are told how many went back. An event older than the driver's
// current presence changes nothing, so a late offline cannot take back what he took on after coming online.
func (s *Service) DriverOffline(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	driverID, applied, err := s.fleet.ApplyOffline(ctx, tx, env)
	if err != nil || !applied {
		return err
	}
	_, err = s.releaseDriver(ctx, tx, driverID, contracts.UnassignDriverOffline, time.Time{}, env.OccurredAt.Time, env.ID)
	return err
}

// ReleaseSilent takes back the assigned routes of drivers whose app has gone quiet when it matters: the driver is
// online by his last report but has not been heard from for fleet.PresenceTTL, and the route departs within
// CommitLead. A route far in the future is kept (a closed app is normal hours before a job), and so is one under
// way. It reports how many routes it released.
func (s *Service) ReleaseSilent(ctx context.Context) (int, error) {
	now := s.now()
	silentBefore, departBefore := now.Add(-fleet.PresenceTTL), now.Add(CommitLead)
	rows, err := s.db.Query(ctx,
		`SELECT DISTINCT r.driver_id
		   FROM routes r JOIN drivers d ON d.id = r.driver_id
		  WHERE r.status = 'ditugaskan' AND r.start_at <= $2
		    AND d.online AND (d.last_seen IS NULL OR d.last_seen < $1)
		  ORDER BY r.driver_id`, silentBefore, departBefore)
	if err != nil {
		return 0, fmt.Errorf("dispatch: silent drivers: %w", err)
	}
	drivers, err := collect(rows)
	if err != nil {
		return 0, fmt.Errorf("dispatch: silent drivers: %w", err)
	}

	released := 0
	for _, driverID := range drivers {
		n := 0
		err := platform.WithTx(ctx, s.db, func(tx pgx.Tx) error {
			// Judged again under the driver's lock: he may have spoken since the list was made.
			var online bool
			var lastSeen *time.Time
			if err := tx.QueryRow(ctx, `SELECT online, last_seen FROM drivers WHERE id = $1 FOR UPDATE`, driverID).Scan(&online, &lastSeen); err != nil {
				return err
			}
			if !online || (lastSeen != nil && !lastSeen.Before(silentBefore)) {
				return nil
			}
			var err error
			n, err = s.releaseDriver(ctx, tx, driverID, contracts.UnassignDriverSilent, departBefore, now, "")
			return err
		})
		if err != nil {
			return released, fmt.Errorf("dispatch: release routes of %s: %w", driverID, err)
		}
		released += n
	}
	if released > 0 {
		s.Kick()
	}
	return released, nil
}

func collect(rows pgx.Rows) ([]string, error) {
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// releaseDriver returns the driver's assigned routes to the queue and tells the admins. departBefore, when not
// zero, limits it to routes departing by then. key makes the admin notification idempotent for an event (empty
// for the worker's own decisions). Plans are locked in id order, so two releases can never wait on each other.
func (s *Service) releaseDriver(ctx context.Context, tx pgx.Tx, driverID, reason string, departBefore, at time.Time, key string) (int, error) {
	var after *time.Time
	if !departBefore.IsZero() {
		after = &departBefore
	}
	rows, err := tx.Query(ctx,
		`SELECT plan_id, id FROM routes
		  WHERE driver_id = $1 AND status = 'ditugaskan' AND ($2::timestamptz IS NULL OR start_at <= $2)
		  ORDER BY plan_id, seq`, driverID, after)
	if err != nil {
		return 0, fmt.Errorf("dispatch: routes of %s: %w", driverID, err)
	}
	type routeRef struct{ planID, routeID string }
	var refs []routeRef
	for rows.Next() {
		var ref routeRef
		if err := rows.Scan(&ref.planID, &ref.routeID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("dispatch: scan route of %s: %w", driverID, err)
		}
		refs = append(refs, ref)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("dispatch: routes of %s: %w", driverID, err)
	}
	if len(refs) == 0 {
		return 0, nil
	}

	var name string
	if err := tx.QueryRow(ctx, `SELECT name FROM drivers WHERE id = $1`, driverID).Scan(&name); err != nil {
		return 0, fmt.Errorf("dispatch: driver %s: %w", driverID, err)
	}
	noteKey := msgDriverOfflineReleased
	if reason == contracts.UnassignDriverSilent {
		noteKey = msgDriverSilentReleased
	}
	note := contracts.Msg{Key: noteKey, Params: map[string]any{"name": name}}

	released := 0
	var planIDs []string // refs are ordered by plan
	for _, ref := range refs {
		if len(planIDs) == 0 || planIDs[len(planIDs)-1] != ref.planID {
			planIDs = append(planIDs, ref.planID)
		}
	}
	for _, planID := range planIDs {
		a, err := plans.Lock(ctx, tx, planID)
		if err != nil {
			return 0, err
		}
		changed := false
		for _, ref := range refs {
			if ref.planID != planID {
				continue
			}
			route, err := a.Route(ref.routeID)
			if err != nil {
				return 0, err
			}
			// Re-read under the plan's lock: it may have started or been handed on since the list was made.
			if route.Status != contracts.RouteStatusAssigned || route.DriverID != driverID || (after != nil && route.StartAt.After(*after)) {
				continue
			}
			if err := plans.Release(ctx, tx, s.pub, a, route, reason, note, at); err != nil {
				return 0, err
			}
			changed = true
			released++
		}
		if !changed {
			continue
		}
		a.Sync(at)
		if err := plans.Save(ctx, tx, a); err != nil {
			return 0, err
		}
		s.feed.Plan(planID)
	}
	if released == 0 {
		return 0, nil
	}
	s.feed.Fleet()
	if err := s.alerts.Released(ctx, tx, key, at, name, released, reason == contracts.UnassignDriverSilent); err != nil {
		return 0, err
	}
	return released, nil
}
