package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

// The storage of the two-phase plan cancel (docs/phase2.md section 1). Three things can happen to a route when a
// cancel is requested, and all of them are decided while the route is serialised (LockRoute, then Lock):
//
//   - the route is known and has not started: it is held (Hold), and released again if the cancel is aborted
//     (Release) or closed as cancelled if it commits (Cancel);
//   - the route is not known yet: a tombstone (AddTombstone) makes its assignment be ignored when it arrives;
//   - the route runs or is done: it is left alone and the vote is a refusal.

// LockRoute serialises what decides whether a route exists with what decides whether it is cancelled, until the
// transaction ends (the lock is per schema, like the tables). Without it a vote could see no route while the assignment is being stored, and both would
// succeed: a route that was voted away and yet can be started. A vote on several routes locks them in id order
// (see the caller), so two votes cannot wait for each other.
func LockRoute(ctx context.Context, q platform.Querier, routeID string) error {
	if _, err := q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || '/' || $1, 0))`, routeID); err != nil {
		return fmt.Errorf("tasks: lock route %s: %w", routeID, err)
	}
	return nil
}

// Hold puts the route on hold for a cancel request: it cannot be started until the hold is released. Call it with
// the route locked.
func Hold(ctx context.Context, q platform.Querier, routeID, requestID string) error {
	_, err := q.Exec(ctx, `UPDATE routes SET hold = $2 WHERE id = $1`, routeID, requestID)
	return err
}

// Held is a route a cancel request touched.
type Held struct {
	RouteID  string
	PlanID   string
	PlanCode string
	// DriverID is empty when dispatch had taken the route back.
	DriverID string
}

const heldColumns = `id, plan_id, plan_code, COALESCE(driver_id, '')`

// Release lifts the holds of an aborted request and returns the routes that were held. Routes the request
// already closed stay closed.
func Release(ctx context.Context, db DB, requestID string) ([]Held, error) {
	rows, err := db.Query(ctx,
		`UPDATE routes SET hold = NULL WHERE hold = $1 AND cancelled_at IS NULL RETURNING `+heldColumns, requestID)
	if err != nil {
		return nil, fmt.Errorf("tasks: release holds: %w", err)
	}
	return collectHeld(rows)
}

// Cancel closes the routes of the plan that the committed request held, as of at, and returns them. They keep
// their hold, which is how a repeated commit finds nothing left to close. An empty requestID closes the plan's
// held routes whatever request holds them.
func Cancel(ctx context.Context, db DB, planID, requestID string, at time.Time) ([]Held, error) {
	rows, err := db.Query(ctx,
		`UPDATE routes SET cancelled_at = $3, updated_at = $3
		  WHERE plan_id = $1 AND hold IS NOT NULL AND ($2 = '' OR hold = $2)
		    AND cancelled_at IS NULL AND status IN ('menunggu_driver', 'ditugaskan')
		  RETURNING `+heldColumns, planID, requestID, at)
	if err != nil {
		return nil, fmt.Errorf("tasks: cancel held routes: %w", err)
	}
	return collectHeld(rows)
}

func collectHeld(rows pgx.Rows) ([]Held, error) {
	defer rows.Close()
	var out []Held
	for rows.Next() {
		var h Held
		if err := rows.Scan(&h.RouteID, &h.PlanID, &h.PlanCode, &h.DriverID); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// AddTombstone records that the request voted for a route this service does not know yet. Repeating it changes
// nothing.
func AddTombstone(ctx context.Context, q platform.Querier, routeID, requestID, planID string, at time.Time) error {
	_, err := q.Exec(ctx,
		`INSERT INTO route_tombstones (route_id, request_id, plan_id, created_at) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`,
		routeID, requestID, planID, at)
	return err
}

// Tombstoned reports whether a cancel request voted for the route before it was known, so that its assignment
// must be ignored.
func Tombstoned(ctx context.Context, q platform.Querier, routeID string) (bool, error) {
	var found bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM route_tombstones WHERE route_id = $1)`, routeID).Scan(&found)
	return found, err
}

// DropTombstones deletes the tombstones of an aborted request and returns the routes they stood for.
func DropTombstones(ctx context.Context, db DB, requestID string) ([]string, error) {
	rows, err := db.Query(ctx, `DELETE FROM route_tombstones WHERE request_id = $1 RETURNING route_id`, requestID)
	if err != nil {
		return nil, fmt.Errorf("tasks: drop tombstones: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Parked is the newest thing dispatch said about a tombstoned route, kept in case the cancel is aborted.
type Parked struct {
	RouteID    string
	PlanID     string
	Version    int64
	EventID    string
	OccurredAt time.Time
	// Assigned is the route.assigned payload; nil when the newest event was an unassignment.
	Assigned []byte
}

// Park keeps p unless something newer is parked already.
func Park(ctx context.Context, q platform.Querier, p Parked) error {
	_, err := q.Exec(ctx,
		`INSERT INTO parked_assignments (route_id, plan_id, version, event_id, occurred_at, assigned)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (route_id) DO UPDATE
		    SET plan_id = EXCLUDED.plan_id, version = EXCLUDED.version, event_id = EXCLUDED.event_id,
		        occurred_at = EXCLUDED.occurred_at, assigned = EXCLUDED.assigned
		  WHERE parked_assignments.version < EXCLUDED.version`,
		p.RouteID, p.PlanID, p.Version, p.EventID, p.OccurredAt, p.Assigned)
	return err
}

// TakeParked removes and returns what was parked for the routes, except for those a tombstone of another request
// still stands for.
func TakeParked(ctx context.Context, db DB, routeIDs []string) ([]Parked, error) {
	rows, err := db.Query(ctx,
		`DELETE FROM parked_assignments p
		  WHERE route_id = ANY($1) AND NOT EXISTS (SELECT 1 FROM route_tombstones t WHERE t.route_id = p.route_id)
		  RETURNING route_id, plan_id, version, event_id, occurred_at, assigned`, routeIDs)
	if err != nil {
		return nil, fmt.Errorf("tasks: take parked assignments: %w", err)
	}
	defer rows.Close()
	var out []Parked
	for rows.Next() {
		var p Parked
		if err := rows.Scan(&p.RouteID, &p.PlanID, &p.Version, &p.EventID, &p.OccurredAt, &p.Assigned); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DropParked forgets what was parked for the plan's routes: its cancel committed, so none of it will ever apply.
func DropParked(ctx context.Context, q platform.Querier, planID string) error {
	_, err := q.Exec(ctx, `DELETE FROM parked_assignments WHERE plan_id = $1`, planID)
	return err
}
