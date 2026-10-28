package dispatch

import (
	"cmp"
	"context"
	"slices"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/pkg/routeplan"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/apix"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/plans"
)

// Message keys of the suggestion's reasons.
const (
	msgSugDistance = "srv.ops.sug.distance"
	msgSugRating   = "srv.ops.sug.rating"
	msgSugFits     = "srv.ops.sug.fits"
	msgSugTooBig   = "srv.ops.sug.tooBig"
)

// Suggestion is the frontend's DriverSuggestion: a driver the admin could give a route to.
type Suggestion struct {
	DriverID           string                `json:"driverId"`
	DriverName         string                `json:"driverName"`
	VehicleID          string                `json:"vehicleId"`
	VehicleType        contracts.VehicleType `json:"vehicleType"`
	Plate              string                `json:"plate"`
	Rating             float64               `json:"rating"`
	DistanceToPickupKm float64               `json:"distanceToPickupKm"`
	EtaToPickupMin     int                   `json:"etaToPickupMin"`
	Fits               bool                  `json:"fits"`
	// Score is 0 to 100; higher suits the route better.
	Score   int             `json:"score"`
	Reasons []contracts.Msg `json:"reasons"`
}

// Suggest lists the drivers who could take a route, best first. A driver who is offline, on duty, too far,
// busy or in the wrong kind of vehicle is not listed, since assigning him would be refused. One whose vehicle
// is too small is listed with fits=false and the reason, so the admin sees why he does not qualify.
func (s *Service) Suggest(ctx context.Context, planID, routeID string) ([]Suggestion, error) {
	a, err := plans.Load(ctx, s.db, planID)
	if err != nil {
		return nil, err
	}
	route, err := a.Route(routeID)
	if err != nil {
		return nil, err
	}
	if a.Frozen() {
		return []Suggestion{}, nil // a cancel is under way: nobody is to be sent to this plan's routes
	}
	w, err := s.world(ctx, s.db)
	if err != nil {
		return nil, err
	}
	load := LoadOf(a.Plan, route.Route)
	out := []Suggestion{}
	for _, v := range w.assess(a.Plan, route.Route) {
		if v.refusal == assignable || v.refusal == overCapacity {
			out = append(out, suggestion(v.candidate, load))
		}
	}
	slices.SortStableFunc(out, func(a, b Suggestion) int {
		return cmp.Or(cmp.Compare(b.Score, a.Score), cmp.Compare(a.DistanceToPickupKm, b.DistanceToPickupKm), cmp.Compare(a.DriverID, b.DriverID))
	})
	return out, nil
}

func suggestion(c candidate, load Load) Suggestion {
	driver, vehicle := c.member.Driver, c.member.Vehicle
	km := routeplan.Round1(c.roadKm)
	fit := contracts.Msg{Key: msgSugTooBig, Params: map[string]any{"model": vehicle.Model}}
	if c.fits {
		fit = contracts.Msg{Key: msgSugFits, Params: map[string]any{"kg": load.WeightKg, "model": vehicle.Model}}
	}
	return Suggestion{
		DriverID: driver.ID, DriverName: driver.Name, VehicleID: vehicle.ID, VehicleType: vehicle.Type, Plate: vehicle.Plate,
		Rating: driver.Rating, DistanceToPickupKm: km, EtaToPickupMin: c.etaMin, Fits: c.fits, Score: c.score(),
		Reasons: []contracts.Msg{
			{Key: msgSugDistance, Params: map[string]any{"km": km, "eta": c.etaMin}},
			{Key: msgSugRating, Params: map[string]any{"rating": driver.Rating, "count": driver.CompletedTrips}},
			fit,
		},
	}
}

// Mount registers dispatch.suggest, assign, unassign and autoAssign, all for admins.
func (s *Service) Mount(rpc *platform.RPC) {
	type routeIn struct {
		PlanID  string `json:"planId"`
		RouteID string `json:"routeId"`
	}
	type assignIn struct {
		PlanID   string `json:"planId"`
		RouteID  string `json:"routeId"`
		DriverID string `json:"driverId"`
	}
	type planIn struct {
		PlanID string `json:"planId"`
	}
	rpc.Handle("dispatch.suggest", apix.Admin(func(ctx context.Context, in routeIn) ([]Suggestion, error) {
		return s.Suggest(ctx, in.PlanID, in.RouteID)
	}))
	rpc.Handle("dispatch.assign", apix.Admin(func(ctx context.Context, in assignIn) (contracts.Plan, error) {
		return s.Assign(ctx, in.PlanID, in.RouteID, in.DriverID)
	}))
	rpc.Handle("dispatch.unassign", apix.Admin(func(ctx context.Context, in routeIn) (contracts.Plan, error) {
		return s.Unassign(ctx, in.PlanID, in.RouteID)
	}))
	rpc.Handle("dispatch.autoAssign", apix.Admin(func(ctx context.Context, in planIn) (contracts.Plan, error) {
		return s.AutoAssignPlan(ctx, in.PlanID)
	}))
}
