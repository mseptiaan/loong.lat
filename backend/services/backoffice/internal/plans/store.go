// Package plans is the plan projection: the confirmed plans backoffice-api learned from plan.confirmed, with
// their routes, kept current by its own dispatch decisions and by driver-api's execution events. It also
// serves what the admin console reads from it.
package plans

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/pkg/routeplan"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/pg"
)

// Message keys of the frontend catalog.
const (
	msgPlanNotFound  = "srv.ops.err.planNotFound"
	msgRouteNotFound = "srv.ops.err.routeNotFound"
)

// PlanNotFound is the answer for an unknown plan id.
func PlanNotFound() *platform.APIError {
	return platform.NotFound(contracts.Msg{Key: msgPlanNotFound})
}

// Route is a route of the projection with the two versions that order the events about it.
type Route struct {
	contracts.Route
	// AssignVersion counts our own route.assigned / route.unassigned events for the route.
	AssignVersion int64
	// ExecVersion is the version of the last execution event applied (driver-api's counter).
	ExecVersion int64
	// Hold is the id of the cancel request that froze the route: dispatch leaves it alone until the request is
	// committed or aborted. Empty when the route is free.
	Hold string
}

// Aggregate is a plan with its routes, the unit that is locked, changed and saved together. Plan.Routes is
// not kept in step: read Routes, or Snapshot for a complete contracts.Plan.
type Aggregate struct {
	Plan   contracts.Plan
	Routes []Route
	// Version is the highest plan.version of console-api's events applied to the plan.
	Version int64
}

// Held is the id of the cancel request that froze the plan's routes, or "" when none did.
func (a *Aggregate) Held() string {
	for _, r := range a.Routes {
		if r.Hold != "" {
			return r.Hold
		}
	}
	return ""
}

// Frozen reports whether a cancel is under way: dispatch must not assign, take back or suggest anything on a
// frozen plan.
func (a *Aggregate) Frozen() bool { return a.Plan.PendingAction != "" || a.Held() != "" }

// Freeze holds every route of the plan for a cancel request and marks the plan as having a cancel pending.
func (a *Aggregate) Freeze(requestID string) {
	for i := range a.Routes {
		a.Routes[i].Hold = requestID
	}
	a.Plan.PendingAction = PendingCancel
}

// Thaw lets go of the holds and clears the pending action.
func (a *Aggregate) Thaw() {
	for i := range a.Routes {
		a.Routes[i].Hold = ""
	}
	a.Plan.PendingAction = ""
}

// PendingCancel is Plan.PendingAction while a cancel waits for the drivers' votes.
const PendingCancel = "cancel"

// Route returns the route with the given id.
func (a *Aggregate) Route(id string) (*Route, error) {
	for i := range a.Routes {
		if a.Routes[i].ID == id {
			return &a.Routes[i], nil
		}
	}
	return nil, platform.NotFound(contracts.Msg{Key: msgRouteNotFound})
}

func (a *Aggregate) routes() []contracts.Route {
	out := make([]contracts.Route, len(a.Routes))
	for i, r := range a.Routes {
		out[i] = r.Route
	}
	return out
}

// Snapshot is the plan as clients see it, routes included.
func (a *Aggregate) Snapshot() contracts.Plan {
	plan := a.Plan
	plan.Routes = a.routes()
	return plan
}

// Sync recomputes the plan's status from its routes after a change at time at, and stamps the update. A
// cancelled plan stays cancelled.
func (a *Aggregate) Sync(at time.Time) {
	if a.Plan.Status != contracts.PlanStatusCancelled {
		a.Plan.Status = routeplan.StatusOf(a.routes())
	}
	if a.Plan.Status == contracts.PlanStatusDone && a.Plan.FinishedAt.IsZero() {
		a.Plan.FinishedAt = contracts.At(at)
	}
	a.Plan.UpdatedAt = contracts.At(at)
}

// Note appends a line to the plan's timeline.
func (a *Aggregate) Note(id string, at time.Time, text contracts.Msg) {
	a.Plan.Events = append(a.Plan.Events, contracts.PlanEvent{ID: id, At: contracts.At(at), Text: text})
}

// NoteOnce is Note unless the timeline already ends with the same text; it reports whether it added the line.
// It keeps a retried action ("no driver found yet") from filling the timeline.
func (a *Aggregate) NoteOnce(id string, at time.Time, text contracts.Msg) bool {
	if n := len(a.Plan.Events); n > 0 && sameMsg(a.Plan.Events[n-1].Text, text) {
		return false
	}
	a.Note(id, at, text)
	return true
}

// sameMsg compares messages as the wire sees them: a stored one has been through JSON, so nested values have
// different Go types than a freshly built one.
func sameMsg(a, b contracts.Msg) bool {
	ja, errA := json.Marshal(a, json.Deterministic(true))
	jb, errB := json.Marshal(b, json.Deterministic(true))
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}

// Insert stores a confirmed plan and its routes. It reports false, changing nothing, when the plan is already
// known: a plan is confirmed once, so a repeated plan.confirmed must not undo assignments made since.
func Insert(ctx context.Context, q pg.Querier, plan contracts.Plan) (bool, error) {
	routes := plan.Routes
	plan.Routes = nil
	body, err := json.Marshal(plan)
	if err != nil {
		return false, fmt.Errorf("plans: encode plan: %w", err)
	}
	tag, err := q.Exec(ctx,
		`INSERT INTO plans (id, code, user_id, status, schedule_mode, plan, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT (id) DO NOTHING`,
		plan.ID, plan.Code, plan.UserID, string(plan.Status), string(plan.Schedule.Mode), body,
		plan.CreatedAt.Time, plan.UpdatedAt.Time)
	if err != nil {
		return false, fmt.Errorf("plans: insert plan: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	for i, r := range routes {
		raw, err := json.Marshal(r)
		if err != nil {
			return false, fmt.Errorf("plans: encode route: %w", err)
		}
		_, err = q.Exec(ctx,
			`INSERT INTO routes (id, plan_id, seq, vehicle_type, status, driver_id, vehicle_id, start_at, route)
			 VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), NULLIF($7, ''), $8, $9)`,
			r.ID, plan.ID, i, string(r.VehicleType), string(r.Status), r.DriverID, r.VehicleID, r.StartAt.Time, raw)
		if err != nil {
			return false, fmt.Errorf("plans: insert route: %w", err)
		}
	}
	return true, nil
}

// Lock loads a plan with its routes and keeps the plan row locked until tx ends. Everything that changes a
// plan or its routes starts here, so those changes happen one at a time per plan.
func Lock(ctx context.Context, tx pgx.Tx, planID string) (*Aggregate, error) {
	return load(ctx, tx, planID, true)
}

// Load reads a plan with its routes without locking.
func Load(ctx context.Context, q pg.Querier, planID string) (*Aggregate, error) {
	return load(ctx, q, planID, false)
}

func load(ctx context.Context, q pg.Querier, planID string, lock bool) (*Aggregate, error) {
	query := `SELECT plan, plan_version FROM plans WHERE id = $1`
	if lock {
		query += ` FOR UPDATE`
	}
	var raw []byte
	var a Aggregate
	if err := q.QueryRow(ctx, query, planID).Scan(&raw, &a.Version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, PlanNotFound()
		}
		return nil, fmt.Errorf("plans: load plan: %w", err)
	}
	if err := json.Unmarshal(raw, &a.Plan); err != nil {
		return nil, fmt.Errorf("plans: decode plan %s: %w", planID, err)
	}

	rows, err := q.Query(ctx, `SELECT route, assign_version, exec_version, COALESCE(hold, '') FROM routes WHERE plan_id = $1 ORDER BY seq`, planID)
	if err != nil {
		return nil, fmt.Errorf("plans: load routes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var route Route
		var body []byte
		if err := rows.Scan(&body, &route.AssignVersion, &route.ExecVersion, &route.Hold); err != nil {
			return nil, fmt.Errorf("plans: scan route: %w", err)
		}
		if err := json.Unmarshal(body, &route.Route); err != nil {
			return nil, fmt.Errorf("plans: decode route of %s: %w", planID, err)
		}
		a.Routes = append(a.Routes, route)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("plans: load routes: %w", err)
	}
	return &a, nil
}

// Save writes the plan and all its routes back. The caller holds the lock from Lock.
func Save(ctx context.Context, q pg.Querier, a *Aggregate) error {
	plan := a.Plan
	plan.Routes = nil
	body, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("plans: encode plan: %w", err)
	}
	if _, err := q.Exec(ctx,
		`UPDATE plans SET status = $2, plan = $3, updated_at = $4, plan_version = $5, pending_action = NULLIF($6, '') WHERE id = $1`,
		plan.ID, string(plan.Status), body, plan.UpdatedAt.Time, a.Version, plan.PendingAction); err != nil {
		return fmt.Errorf("plans: save plan: %w", err)
	}
	for _, r := range a.Routes {
		raw, err := json.Marshal(r.Route)
		if err != nil {
			return fmt.Errorf("plans: encode route: %w", err)
		}
		if _, err := q.Exec(ctx,
			`UPDATE routes SET status = $2, driver_id = NULLIF($3, ''), vehicle_id = NULLIF($4, ''), start_at = $5,
			        route = $6, assign_version = $7, exec_version = $8, hold = NULLIF($9, '')
			  WHERE id = $1`,
			r.ID, string(r.Status), r.DriverID, r.VehicleID, r.StartAt.Time, raw, r.AssignVersion, r.ExecVersion, r.Hold); err != nil {
			return fmt.Errorf("plans: save route: %w", err)
		}
	}
	return nil
}

// ActiveRoutes returns every route that has a driver and is not finished: what keeps a driver busy.
func ActiveRoutes(ctx context.Context, q pg.Querier) ([]contracts.Route, error) {
	rows, err := q.Query(ctx, `SELECT route FROM routes WHERE status IN ('ditugaskan', 'berjalan')`)
	if err != nil {
		return nil, fmt.Errorf("plans: active routes: %w", err)
	}
	defer rows.Close()
	var out []contracts.Route
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, fmt.Errorf("plans: scan route: %w", err)
		}
		var route contracts.Route
		if err := json.Unmarshal(body, &route); err != nil {
			return nil, fmt.Errorf("plans: decode route: %w", err)
		}
		out = append(out, route)
	}
	return out, rows.Err()
}

// Waiting is a route that needs a driver.
type Waiting struct {
	PlanID  string
	RouteID string
}

// WaitingRoutes lists the routes of open plans that have no driver, in the order dispatch should serve them:
// "now" plans first, the longest waiting first, then scheduled plans by departure. A scheduled route is
// listed only once it departs before startsBefore: further out it is not worth a driver's time yet. A route
// frozen by a cancel, or of a plan with one pending, is not listed.
func WaitingRoutes(ctx context.Context, q pg.Querier, startsBefore time.Time) ([]Waiting, error) {
	rows, err := q.Query(ctx,
		`SELECT r.plan_id, r.id
		   FROM routes r JOIN plans p ON p.id = r.plan_id
		  WHERE r.status = 'menunggu_driver' AND r.hold IS NULL AND p.pending_action IS NULL
		    AND p.status IN ('menunggu_driver', 'ditugaskan', 'berjalan')
		    AND (p.schedule_mode <> 'terjadwal' OR r.start_at <= $1)
		  ORDER BY (p.schedule_mode = 'terjadwal'),
		           CASE WHEN p.schedule_mode = 'terjadwal' THEN r.start_at ELSE p.created_at END,
		           r.plan_id, r.seq`, startsBefore)
	if err != nil {
		return nil, fmt.Errorf("plans: waiting routes: %w", err)
	}
	defer rows.Close()
	var out []Waiting
	for rows.Next() {
		var w Waiting
		if err := rows.Scan(&w.PlanID, &w.RouteID); err != nil {
			return nil, fmt.Errorf("plans: scan waiting route: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// CustomerName is the name of a customer, or the id itself while customer.registered has not arrived.
func CustomerName(ctx context.Context, q pg.Querier, customerID string) (string, error) {
	var name string
	err := q.QueryRow(ctx, `SELECT name FROM customers WHERE id = $1`, customerID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return customerID, nil
	}
	return name, err
}
