// Package tasks stores the routes this service executes: the snapshot dispatch assigned, with the delivery
// codes kept server-side, and the bookkeeping that orders its events.
package tasks

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/pkg/routeplan"
)

// ErrNotFound means no task has the route id.
var ErrNotFound = errors.New("tasks: no such route")

// Task is a route with the context this service keeps around it.
type Task struct {
	PlanID       string
	PlanCode     string
	CustomerName string
	// DriverID is empty once dispatch took the route back.
	DriverID string
	// Route is the full snapshot, delivery codes included: use ForApp before anything leaves the service.
	Route contracts.Route
	// AssignVersion is routes.assign_version of dispatch: the newest assignment event applied.
	AssignVersion int64
	// ExecVersion counts the execution events published for the route; it is their AggregateVersion.
	ExecVersion int64
	UpdatedAt   time.Time

	// Hold is the id of the plan cancel request that holds the route, or "": a held route cannot be started.
	// CancelledAt is when a committed cancel closed the route; a cancelled route is no task any more. DelayStop and
	// DelayStep are the last route.delayed published (the stop it was measured at, in steps of delayStepMin).
	// These four are read with the task but written by their own functions (cancel.go, delay.go of execution),
	// never by Put, so that no write of the route can wipe them.
	Hold        string
	CancelledAt time.Time
	DelayStop   string
	DelayStep   int
}

// Cancelled reports whether a committed plan cancel closed the route.
func (t Task) Cancelled() bool { return !t.CancelledAt.IsZero() }

// Started reports whether execution began: from then on the route belongs to this service alone and dispatch
// can no longer change it.
func (t Task) Started() bool {
	return t.Route.Status == contracts.RouteStatusRunning || t.Route.Status == contracts.RouteStatusDone
}

// ForApp is the route as the driver app may see it: without the delivery codes, which only the recipient and
// this service know. The stops are copied, so r is left intact.
func ForApp(r contracts.Route) contracts.Route {
	stops := make([]contracts.RouteStop, len(r.Stops))
	copy(stops, r.Stops)
	for i := range stops {
		stops[i].DeliveryCode = ""
	}
	r.Stops = stops
	return r
}

// DriverRoute is the frontend's DriverRoute: a task as the app lists it.
type DriverRoute struct {
	PlanID       string          `json:"planId"`
	PlanCode     string          `json:"planCode"`
	CustomerName string          `json:"customerName"`
	Route        contracts.Route `json:"route"`
}

// ForApp is the task as the app lists it.
func (t Task) ForApp() DriverRoute {
	return DriverRoute{PlanID: t.PlanID, PlanCode: t.PlanCode, CustomerName: t.CustomerName, Route: ForApp(t.Route)}
}

// Plan is what the app's actions answer with: the frontend's Plan as far as this service knows it. Plans belong
// to console-api, so only the identity, the driver's own route (without codes) and a status derived from that
// route are real; the app reads the route from it. Schedule and priority are neutral values for the required
// fields. Empty lists encode as [].
func (t Task) Plan() contracts.Plan {
	route := ForApp(t.Route)
	return contracts.Plan{
		ID:        t.PlanID,
		Code:      t.PlanCode,
		Status:    routeplan.StatusOf([]contracts.Route{route}),
		Schedule:  contracts.Schedule{Mode: contracts.ScheduleModeNow},
		Priority:  contracts.Priority{Preset: contracts.PresetBalanced},
		Routes:    []contracts.Route{route},
		CreatedAt: contracts.At(t.UpdatedAt),
		UpdatedAt: contracts.At(t.UpdatedAt),
	}
}

// DB is what the list queries need: the pool or a transaction.
type DB interface {
	platform.Querier
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

const taskColumns = `plan_id, plan_code, customer_name, COALESCE(driver_id, ''), route, assign_version, exec_version, updated_at,
	COALESCE(hold, ''), cancelled_at, COALESCE(delay_stop, ''), delay_step`

type scanner interface{ Scan(dest ...any) error }

func scanTask(row scanner) (Task, error) {
	var (
		t         Task
		raw       []byte
		cancelled *time.Time
	)
	err := row.Scan(&t.PlanID, &t.PlanCode, &t.CustomerName, &t.DriverID, &raw, &t.AssignVersion, &t.ExecVersion, &t.UpdatedAt,
		&t.Hold, &cancelled, &t.DelayStop, &t.DelayStep)
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, err
	}
	if err := json.Unmarshal(raw, &t.Route); err != nil {
		return Task{}, fmt.Errorf("tasks: decode route: %w", err)
	}
	if cancelled != nil {
		t.CancelledAt = *cancelled
	}
	return t, nil
}

// Lock returns the task of a route and locks its row until the transaction ends: every change of a route goes
// through it, so the actions of one route are serialised and their execution versions cannot collide.
func Lock(ctx context.Context, q platform.Querier, routeID string) (Task, error) {
	return scanTask(q.QueryRow(ctx, `SELECT `+taskColumns+` FROM routes WHERE id = $1 FOR UPDATE`, routeID))
}

// Put writes the task, creating it if needed; the hold, the cancellation and the delay marker are not part of
// it (see Task). at is the time of the change.
func Put(ctx context.Context, q platform.Querier, t Task, at time.Time) error {
	raw, err := json.Marshal(t.Route)
	if err != nil {
		return fmt.Errorf("tasks: encode route: %w", err)
	}
	var finishedAt *time.Time
	if !t.Route.FinishedAt.IsZero() {
		finishedAt = &t.Route.FinishedAt.Time
	}
	_, err = q.Exec(ctx,
		`INSERT INTO routes (id, plan_id, plan_code, customer_name, driver_id, status, route, assign_version, exec_version,
		                     cost_rp, distance_km, duration_min, stop_count, finished_at, updated_at)
		 VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		 ON CONFLICT (id) DO UPDATE
		    SET plan_id = EXCLUDED.plan_id, plan_code = EXCLUDED.plan_code, customer_name = EXCLUDED.customer_name,
		        driver_id = EXCLUDED.driver_id, status = EXCLUDED.status, route = EXCLUDED.route,
		        assign_version = EXCLUDED.assign_version, exec_version = EXCLUDED.exec_version,
		        cost_rp = EXCLUDED.cost_rp, distance_km = EXCLUDED.distance_km, duration_min = EXCLUDED.duration_min,
		        stop_count = EXCLUDED.stop_count, finished_at = EXCLUDED.finished_at, updated_at = EXCLUDED.updated_at`,
		t.Route.ID, t.PlanID, t.PlanCode, t.CustomerName, t.DriverID, string(t.Route.Status), raw, t.AssignVersion, t.ExecVersion,
		t.Route.CostRp, t.Route.DistanceKm, t.Route.DurationMin, len(t.Route.Stops), finishedAt, at)
	return err
}

// Active lists the driver's routes that are assigned or running, earliest departure first. A route a cancel
// closed is not among them.
func Active(ctx context.Context, db DB, driverID string) ([]Task, error) {
	rows, err := db.Query(ctx,
		`SELECT `+taskColumns+` FROM routes
		  WHERE driver_id = $1 AND status IN ('ditugaskan', 'berjalan') AND cancelled_at IS NULL
		  ORDER BY route->>'startAt', id`, driverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RunningRouteID is the id of the route the driver is on, or "".
func RunningRouteID(ctx context.Context, q platform.Querier, driverID string) (string, error) {
	var id string
	err := q.QueryRow(ctx,
		`SELECT id FROM routes WHERE driver_id = $1 AND status = 'berjalan' ORDER BY updated_at DESC, id LIMIT 1`, driverID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// Finished is the summary of a route the driver completed.
type Finished struct {
	PlanID      string
	PlanCode    string
	RouteID     string
	FinishedAt  time.Time
	Stops       int
	DistanceKm  float64
	DurationMin float64
	CostRp      int64
}

// FinishedSince lists the driver's completed routes finished at or after since, newest first; limit 0 means all.
func FinishedSince(ctx context.Context, db DB, driverID string, since time.Time, limit int) ([]Finished, error) {
	rows, err := db.Query(ctx,
		`SELECT plan_id, plan_code, id, finished_at, stop_count, distance_km, duration_min, cost_rp
		   FROM routes
		  WHERE driver_id = $1 AND status = 'selesai' AND finished_at >= $2
		  ORDER BY finished_at DESC, id
		  LIMIT NULLIF($3::bigint, 0)`, driverID, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Finished
	for rows.Next() {
		var f Finished
		if err := rows.Scan(&f.PlanID, &f.PlanCode, &f.RouteID, &f.FinishedAt, &f.Stops, &f.DistanceKm, &f.DurationMin, &f.CostRp); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
