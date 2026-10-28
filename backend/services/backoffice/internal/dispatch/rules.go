// Package dispatch decides which driver takes which route. The rules are plain code, not AI: a driver must be
// online, free, in range and able to carry the load; among those the nearest wins. Admins can assign by hand,
// and a worker assigns automatically when the setting is on. Every decision is one transaction that updates the
// route projection and enqueues the event that tells driver-api.
package dispatch

import (
	"cmp"
	"math"
	"slices"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/routeplan"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/fleet"
)

const (
	// Radius is how far, in a straight line, a driver may be from the first pickup of a "now" route.
	Radius = 15.0
	// CommitLead is how long before it departs a scheduled route starts to claim a driver: further out it is
	// left waiting, and an assigned route this close to departure makes its driver "on duty".
	CommitLead = 60 * time.Minute

	// buffer is the rest kept between two jobs of one driver.
	buffer = 15 * time.Minute
	// assignDelay is how long after being assigned a driver leaves for a "now" route.
	assignDelay = 2 * time.Minute
	// unknownKm is the distance assumed for a driver whose position is not known.
	unknownKm = 8.0
)

// Load is what a route carries, to be compared with a vehicle's capacity.
type Load struct {
	WeightKg       float64
	VolumeM3       float64
	MaxDimensionCm float64
}

// LoadOf sums the items of the shipments the route serves. The route's own total weight counts too, and the
// larger wins: a snapshot whose items were trimmed to fit an event must not make a load look lighter.
func LoadOf(plan contracts.Plan, route contracts.Route) Load {
	served := map[string]bool{}
	for _, s := range route.Stops {
		served[s.ShipmentID] = true
	}
	var l Load
	for _, shipment := range plan.Shipments {
		if !served[shipment.ID] {
			continue
		}
		for _, it := range shipment.Items {
			qty := float64(it.Qty)
			l.WeightKg += qty * it.WeightKg
			l.VolumeM3 += qty * it.LengthCm * it.WidthCm * it.HeightCm / 1_000_000
			l.MaxDimensionCm = max(l.MaxDimensionCm, it.LengthCm, it.WidthCm, it.HeightCm)
		}
	}
	l.WeightKg = max(l.WeightKg, route.TotalWeightKg)
	return l
}

// Fits reports whether a vehicle can carry the load.
func (l Load) Fits(v contracts.Vehicle) bool {
	return l.WeightKg <= v.MaxWeightKg && l.VolumeM3 <= v.MaxVolumeM3 && l.MaxDimensionCm <= v.MaxDimensionCm
}

// refusal is why a driver cannot take a route.
type refusal int

const (
	assignable refusal = iota
	// unavailable: not online, off the road (vehicle in service), too far for a "now" route, or busy with
	// another job around that time.
	unavailable
	noVehicle
	wrongType
	overCapacity
)

// candidate is a driver measured against a route.
type candidate struct {
	member fleet.Member
	// straightKm and roadKm are the distance to the first pickup: as the crow flies (what the radius limits) and
	// as the road estimate (what the driver drives, and the ETA is computed from).
	straightKm, roadKm float64
	etaMin             int
	fits               bool
}

// verdict is a candidate with the reason it was refused, if it was. A refused candidate still carries its
// measurements: suggestions show over-capacity drivers with their numbers.
type verdict struct {
	candidate
	refusal refusal
}

// assess measures one driver against a route and decides whether he may take it. busy are his other assigned or
// running routes. The checks run in the order the console's error messages are most useful: availability,
// vehicle, type, range and schedule, and capacity last, so only a driver who could otherwise drive it is shown
// as "too big".
func assess(m fleet.Member, busy []contracts.Route, plan contracts.Plan, route contracts.Route, load Load, now time.Time) verdict {
	v := verdict{candidate: candidate{member: m}}
	driver, vehicle := m.Driver, m.Vehicle
	switch {
	case driver.Status != fleet.StatusOnline || len(route.Stops) == 0:
		v.refusal = unavailable
		return v
	case vehicle == nil:
		v.refusal = noVehicle
		return v
	case vehicle.Status != contracts.VehicleStatusActive:
		v.refusal = unavailable
		return v
	case vehicle.Type != route.VehicleType:
		v.refusal = wrongType
		return v
	}

	v.straightKm, v.roadKm = unknownKm, unknownKm
	if driver.Position != nil {
		v.straightKm = routeplan.HaversineKm(*driver.Position, route.Stops[0].Waypoint.Position)
		v.roadKm = v.straightKm * routeplan.RoadFactor
	}
	speed := routeplan.SpeedKmh(vehicle.Type, routeplan.HourWIB(now))
	v.etaMin = max(1, int(math.Round(v.roadKm/speed*60)))
	v.fits = load.Fits(*vehicle)

	scheduled := plan.Schedule.Mode == contracts.ScheduleModeScheduled
	from, to := occupancy(route, scheduled, v.etaMin, now)
	switch {
	case !scheduled && v.straightKm > Radius, busyDuring(busy, from, to, now):
		v.refusal = unavailable
	case !v.fits:
		v.refusal = overCapacity
	}
	return v
}

// occupancy is the span a driver is taken up by the route: for a "now" route from now until he has reached the
// first pickup and finished; for a scheduled one from when he would set off to its end.
func occupancy(route contracts.Route, scheduled bool, etaMin int, now time.Time) (from, to time.Time) {
	eta := minutes(float64(etaMin))
	length := minutes(route.DurationMin)
	if scheduled {
		return route.StartAt.Add(-eta), route.StartAt.Add(length)
	}
	return now, now.Add(eta + length)
}

// busyDuring reports whether any of the routes occupies its driver at a time that, with the rest between jobs,
// overlaps [from, to].
func busyDuring(routes []contracts.Route, from, to, now time.Time) bool {
	return slices.ContainsFunc(routes, func(r contracts.Route) bool {
		start, end := window(r, now)
		return start.Before(to.Add(buffer)) && end.Add(buffer).After(from)
	})
}

// window is the span an assigned or running route occupies its driver. A running route that is late still
// occupies him for a while longer.
func window(r contracts.Route, now time.Time) (start, end time.Time) {
	running := r.Status == contracts.RouteStatusRunning
	start = r.StartAt.Time
	if running && !r.StartedAt.IsZero() {
		start = r.StartedAt.Time
	}
	end = start.Add(minutes(r.DurationMin))
	if running && end.Before(now) {
		end = now.Add(buffer)
	}
	return start, end
}

func minutes(m float64) time.Duration { return time.Duration(m * float64(time.Minute)) }

// nearestFirst orders assignable candidates the way automatic dispatch picks: nearest, then best rated, then by
// id so a tie is always settled the same way.
func nearestFirst(cands []candidate) {
	slices.SortFunc(cands, func(a, b candidate) int {
		return cmp.Or(
			cmp.Compare(a.straightKm, b.straightKm),
			cmp.Compare(b.member.Driver.Rating, a.member.Driver.Rating),
			cmp.Compare(a.member.Driver.ID, b.member.Driver.ID),
		)
	})
}

// score rates a candidate from 0 to 100 for the admin's suggestion list: nearer, better rated and able to carry
// the load score higher.
func (c candidate) score() int {
	s := 100 - c.roadKm*7 - (4.95-c.member.Driver.Rating)*40
	if !c.fits {
		s -= 60
	}
	return int(math.Max(0, math.Min(100, math.Round(s))))
}

// departure is when a route should leave for a driver assigned now: in two minutes, or later when the schedule's
// window opens later than the driver would need to get there.
func departure(plan contracts.Plan, route contracts.Route, from contracts.LatLng, now time.Time) time.Time {
	start := now.Add(assignDelay)
	if plan.Schedule.Mode != contracts.ScheduleModeScheduled || plan.Schedule.Window == nil {
		return start
	}
	opens := plan.Schedule.Window.From.Time
	approach := minutes(routeplan.RebaseStart(route, from, opens).Stops[0].LegMin)
	return laterOf(start, opens.Add(-approach))
}

func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
