package plans

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/routeplan"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/apix"
)

// Message keys of the plan's timeline when a route loses its driver.
const (
	msgUnassigned      = "srv.ops.event.unassigned"
	msgUnassignedPlain = "srv.ops.event.unassignedPlain"
)

// RouteNumber is the "1" of a route id like "pln-…:r1", or "" when the id has no such suffix.
func RouteNumber(routeID string) string {
	_, n, _ := strings.CutLast(routeID, ":r")
	return n
}

// UnassignedNote is the timeline line for a route taken from its driver.
func UnassignedNote(routeID string) contracts.Msg {
	if n := RouteNumber(routeID); n != "" {
		return contracts.Msg{Key: msgUnassigned, Params: map[string]any{"n": n}}
	}
	return contracts.Msg{Key: msgUnassignedPlain}
}

// Release takes an assigned route back from its driver inside tx: the route waits for a driver again, its
// assignment version goes up, the plan's timeline says why (note) and route.unassigned tells the neighbours
// (reason is one of contracts.Unassign*). The caller saves the aggregate; it is the one place that decides when.
func Release(ctx context.Context, tx pgx.Tx, pub *eventbus.Publisher, a *Aggregate, route *Route, reason string, note contracts.Msg, at time.Time) error {
	driverID := route.DriverID
	route.Route = routeplan.Unassign(route.Route)
	route.AssignVersion++
	a.Note(apix.NewID("evt"), at, note)
	return pub.Enqueue(ctx, tx, eventbus.Event{
		Type: contracts.TypeRouteUnassigned, Key: route.ID, AggregateVersion: route.AssignVersion,
		Data: contracts.RouteUnassigned{PlanID: a.Plan.ID, RouteID: route.ID, DriverID: driverID, Reason: reason},
	})
}
