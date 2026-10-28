package plans

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/apix"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/changes"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/pg"
)

// Message keys of the frontend catalog.
const (
	msgCancelRunning = "srv.api.cancelRunning"
	msgPlanInactive  = "srv.api.planInactive"
)

// MsgCancelPending is a key the catalog does not have yet: "A cancellation of this order is already in progress."
// It answers an admin who cancels a plan that already has a cancel pending.
const MsgCancelPending = "srv.api.cancelPending"

// Service answers the admin console's reads of the plan projection (plans.list, plans.get, live.routes) and
// takes an admin's plans.cancel.
type Service struct {
	pool *pgxpool.Pool
	db   pg.Querier
	pub  *eventbus.Publisher
	feed *changes.Feed
	now  func() time.Time
}

func NewService(pool *pgxpool.Pool, pub *eventbus.Publisher, feed *changes.Feed, now func() time.Time) *Service {
	return &Service{pool: pool, db: pool, pub: pub, feed: feed, now: now}
}

// Mount registers the methods; all of them are for admins.
func (s *Service) Mount(rpc *platform.RPC) {
	type getIn struct {
		ID string `json:"id"`
	}
	type listIn struct {
		Filter Filter `json:"filter"`
	}
	type cancelIn struct {
		ID     string `json:"id"`
		Reason string `json:"reason"`
	}
	rpc.Handle("plans.cancel", apix.Admin(func(ctx context.Context, in cancelIn) (contracts.Plan, error) {
		admin, _ := platform.PrincipalFrom(ctx)
		return s.Cancel(ctx, in.ID, in.Reason, admin.Name, platform.LocaleFrom(ctx))
	}))
	rpc.Handle("plans.list", apix.Admin(func(ctx context.Context, in listIn) ([]Summary, error) {
		return s.List(ctx, in.Filter)
	}))
	rpc.Handle("plans.get", apix.Admin(func(ctx context.Context, in getIn) (contracts.Plan, error) {
		return s.Get(ctx, in.ID)
	}))
	rpc.Handle("live.routes", apix.Admin(func(ctx context.Context, _ struct{}) ([]ActiveRoute, error) {
		return s.LiveRoutes(ctx)
	}))
}

// Filter is the frontend's PlanFilter.
type Filter struct {
	Status      []contracts.PlanStatus `json:"status"`
	UserID      string                 `json:"userId"`
	Q           string                 `json:"q"`
	VehicleType contracts.VehicleType  `json:"vehicleType"`
	// NeedsDriver keeps only plans with a route that has no driver.
	NeedsDriver bool           `json:"needsDriver"`
	From        contracts.Time `json:"from"`
	To          contracts.Time `json:"to"`
}

// Cancel is an admin's request to cancel a confirmed plan. Only console-api owns what a plan has cost and been
// paid, and only driver-api knows whether a driver has left, so nothing is cancelled here: the plan is marked
// pending, console-api is asked (plan.cancel.asked, with the admin's name and language for the default reason
// and the timeline) and the outcome comes back as plan.cancel.requested, then plan.cancelled or an abort. The
// plan is answered as it now stands, with its pending action.
//
// A plan under way or finished, or with a cancel already pending, answers with a conflict.
func (s *Service) Cancel(ctx context.Context, planID, reason, askedBy string, locale platform.Locale) (contracts.Plan, error) {
	var out contracts.Plan
	err := platform.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		a, err := Lock(ctx, tx, planID)
		if err != nil {
			return err
		}
		switch {
		case a.Plan.Status == contracts.PlanStatusRunning:
			return platform.Conflict(contracts.Msg{Key: msgCancelRunning})
		case a.Plan.Status == contracts.PlanStatusDone || a.Plan.Status == contracts.PlanStatusCancelled:
			return platform.Conflict(contracts.Msg{Key: msgPlanInactive})
		case a.Frozen():
			return platform.Conflict(contracts.Msg{Key: MsgCancelPending})
		}
		a.Plan.PendingAction = PendingCancel
		a.Sync(s.now())
		if err := Save(ctx, tx, a); err != nil {
			return err
		}
		out = a.Snapshot()
		// Not part of the plan.version family: console-api numbers those. The request is deduplicated by the
		// inbox and, once console-api answers, by its request id.
		return s.pub.Enqueue(ctx, tx, eventbus.Event{
			Type: contracts.TypePlanCancelAsked, Key: planID,
			Data: contracts.PlanCancelAsked{PlanID: planID, Reason: strings.TrimSpace(reason), AskedBy: askedBy, Locale: string(locale)},
		})
	})
	if err != nil {
		return contracts.Plan{}, err
	}
	s.feed.Plan(planID)
	return out, nil
}

// Get returns one plan with its routes. Only confirmed plans exist here, never drafts.
func (s *Service) Get(ctx context.Context, id string) (contracts.Plan, error) {
	a, err := Load(ctx, s.db, id)
	if err != nil {
		return contracts.Plan{}, err
	}
	return a.Snapshot(), nil
}

type planRow struct {
	plan     contracts.Plan
	customer string
}

// List returns the plan table rows matching the filter, most recently updated first.
func (s *Service) List(ctx context.Context, f Filter) ([]Summary, error) {
	rows, err := s.Rows(ctx, f)
	if err != nil {
		return nil, err
	}
	var driverIDs []string
	for _, row := range rows {
		for _, r := range row.Plan.Routes {
			if r.DriverID != "" {
				driverIDs = append(driverIDs, r.DriverID)
			}
		}
	}
	names, err := s.driverNames(ctx, driverIDs)
	if err != nil {
		return nil, err
	}

	out := make([]Summary, 0, len(rows))
	for _, row := range rows {
		sum := Summarize(row.Plan, row.Customer, names)
		switch {
		case f.NeedsDriver && sum.RoutesWithoutDriver == 0,
			f.VehicleType != "" && !slices.Contains(sum.VehicleTypes, f.VehicleType),
			!sum.Matches(f.Q):
			continue
		}
		out = append(out, sum)
	}
	return out, nil
}

// Row is a plan with its routes and the name of its customer (the customer's id while customer.registered has not
// arrived).
type Row struct {
	Plan     contracts.Plan
	Customer string
}

// Rows returns the plans matching the parts of the filter the database decides (status, customer, creation time),
// each with its routes, most recently updated first. List builds the table rows from it; the AI tools read the
// projection through it.
func (s *Service) Rows(ctx context.Context, f Filter) ([]Row, error) {
	rows, err := s.matching(ctx, f)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.plan.ID
	}
	routes, err := s.routesOf(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]Row, len(rows))
	for i, r := range rows {
		r.plan.Routes = routes[r.plan.ID]
		out[i] = Row{Plan: r.plan, Customer: r.customer}
	}
	return out, nil
}

// IDOf resolves what a person calls a plan, its code ("PL-2041", any case) or its id, to the plan's id; an unknown
// reference is PlanNotFound.
func (s *Service) IDOf(ctx context.Context, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	var id string
	err := s.db.QueryRow(ctx, `SELECT id FROM plans WHERE id = $1 OR upper(code) = upper($1) ORDER BY (id = $1) DESC LIMIT 1`, ref).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", PlanNotFound()
	}
	if err != nil {
		return "", fmt.Errorf("plans: find %q: %w", ref, err)
	}
	return id, nil
}

// matching reads the plans that match the filter parts the database can decide.
func (s *Service) matching(ctx context.Context, f Filter) ([]planRow, error) {
	statuses := make([]string, len(f.Status))
	for i, st := range f.Status {
		statuses[i] = string(st)
	}
	var from, to *time.Time
	if !f.From.IsZero() {
		from = &f.From.Time
	}
	if !f.To.IsZero() {
		to = &f.To.Time
	}
	rows, err := s.db.Query(ctx,
		`SELECT p.plan, COALESCE(c.name, p.user_id)
		   FROM plans p LEFT JOIN customers c ON c.id = p.user_id
		  WHERE (cardinality($1::text[]) = 0 OR p.status = ANY($1))
		    AND ($2 = '' OR p.user_id = $2)
		    AND ($3::timestamptz IS NULL OR p.created_at >= $3)
		    AND ($4::timestamptz IS NULL OR p.created_at <= $4)
		  ORDER BY p.updated_at DESC, p.id`, statuses, f.UserID, from, to)
	if err != nil {
		return nil, fmt.Errorf("plans: list: %w", err)
	}
	defer rows.Close()
	var out []planRow
	for rows.Next() {
		var body []byte
		var row planRow
		if err := rows.Scan(&body, &row.customer); err != nil {
			return nil, fmt.Errorf("plans: scan plan: %w", err)
		}
		if err := json.Unmarshal(body, &row.plan); err != nil {
			return nil, fmt.Errorf("plans: decode plan: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// routesOf reads the routes of the given plans, in route order.
func (s *Service) routesOf(ctx context.Context, planIDs []string) (map[string][]contracts.Route, error) {
	rows, err := s.db.Query(ctx, `SELECT plan_id, route FROM routes WHERE plan_id = ANY($1) ORDER BY plan_id, seq`, planIDs)
	if err != nil {
		return nil, fmt.Errorf("plans: routes: %w", err)
	}
	defer rows.Close()
	out := map[string][]contracts.Route{}
	for rows.Next() {
		var planID string
		var body []byte
		if err := rows.Scan(&planID, &body); err != nil {
			return nil, fmt.Errorf("plans: scan route: %w", err)
		}
		var route contracts.Route
		if err := json.Unmarshal(body, &route); err != nil {
			return nil, fmt.Errorf("plans: decode route: %w", err)
		}
		out[planID] = append(out[planID], route)
	}
	return out, rows.Err()
}

func (s *Service) driverNames(ctx context.Context, ids []string) (map[string]string, error) {
	rows, err := s.db.Query(ctx, `SELECT id, name FROM drivers WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("plans: driver names: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("plans: scan driver name: %w", err)
		}
		out[id] = name
	}
	return out, rows.Err()
}

// ActiveRoute is the frontend's ActiveRouteLive: a route out on the map.
type ActiveRoute struct {
	PlanID       string          `json:"planId"`
	PlanCode     string          `json:"planCode"`
	CustomerName string          `json:"customerName"`
	DriverName   string          `json:"driverName,omitempty"`
	Route        contracts.Route `json:"route"`
}

// LiveRoutes lists the routes that are assigned or running.
func (s *Service) LiveRoutes(ctx context.Context) ([]ActiveRoute, error) {
	rows, err := s.db.Query(ctx,
		`SELECT p.id, p.code, COALESCE(c.name, p.user_id), COALESCE(d.name, ''), r.route
		   FROM routes r
		   JOIN plans p ON p.id = r.plan_id
		   LEFT JOIN customers c ON c.id = p.user_id
		   LEFT JOIN drivers d ON d.id = r.driver_id
		  WHERE r.status IN ('ditugaskan', 'berjalan')
		  ORDER BY r.start_at, r.id`)
	if err != nil {
		return nil, fmt.Errorf("plans: live routes: %w", err)
	}
	defer rows.Close()
	out := []ActiveRoute{}
	for rows.Next() {
		var live ActiveRoute
		var body []byte
		if err := rows.Scan(&live.PlanID, &live.PlanCode, &live.CustomerName, &live.DriverName, &body); err != nil {
			return nil, fmt.Errorf("plans: scan live route: %w", err)
		}
		if err := json.Unmarshal(body, &live.Route); err != nil {
			return nil, fmt.Errorf("plans: decode route: %w", err)
		}
		out = append(out, live)
	}
	return out, rows.Err()
}
