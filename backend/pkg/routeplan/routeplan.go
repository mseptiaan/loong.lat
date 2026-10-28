// Package routeplan holds the rules of a route's clock, shared by every service
// that touches a route: how fast a vehicle goes at which hour, how long a stop
// takes, how a quoted route becomes a real one, how it is shifted to a driver's
// position, and how a plan's status follows from its routes.
//
// It is logic without data: services keep their own copies of routes (see the
// event-carried state in the design) and apply these rules to them.
package routeplan

import (
	"crypto/rand"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

/* ---------------- the clock ---------------- */

// Time spent at a stop, in minutes.
const (
	DwellPickupMin = 4
	DwellDropMin   = 3
)

// DwellMin is the time a courier spends at a stop of the given kind.
func DwellMin(k contracts.StopKind) float64 {
	if k == contracts.StopKindPickup {
		return DwellPickupMin
	}
	return DwellDropMin
}

// HourWIB is the hour of the day in Western Indonesia Time (UTC+7, no daylight saving).
func HourWIB(t time.Time) int { return (t.UTC().Hour() + 7) % 24 }

// TrafficFactor scales a vehicle's speed by the hour: Jakarta's rush hours are far slower.
func TrafficFactor(hour int) float64 {
	switch {
	case hour >= 7 && hour < 9:
		return 0.62
	case hour >= 16 && hour < 19:
		return 0.6
	case hour >= 11 && hour < 14:
		return 0.85
	case hour >= 22 || hour < 5:
		return 1.2
	}
	return 0.95
}

// BaseSpeedKmh is the free-flow speed of a vehicle type in the city.
func BaseSpeedKmh(t contracts.VehicleType) float64 {
	if t == contracts.VehicleTypeMotor {
		return 30
	}
	return 24
}

// SpeedKmh is a vehicle's speed at an hour of the day.
func SpeedKmh(t contracts.VehicleType, hour int) float64 {
	return BaseSpeedKmh(t) * TrafficFactor(hour)
}

// Round1 rounds to one decimal.
func Round1(n float64) float64 { return math.Round(n*10) / 10 }

// LegMin is how long a leg of legKm takes at the given hour; never less than a minute.
func LegMin(t contracts.VehicleType, legKm float64, hour int) float64 {
	return math.Max(1, Round1(legKm/SpeedKmh(t, hour)*60))
}

func plusMin(t time.Time, min float64) time.Time {
	return t.Add(time.Duration(min*60_000) * time.Millisecond)
}

/* ---------------- geography ---------------- */

// RoadFactor turns a straight-line distance into a road distance for dense Jakarta streets.
const RoadFactor = 1.28

const earthKm = 6371.0088

// HaversineKm is the great-circle distance in kilometres.
func HaversineKm(a, b contracts.LatLng) float64 {
	rad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat, dLng := rad(b.Lat-a.Lat), rad(b.Lng-a.Lng)
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(rad(a.Lat))*math.Cos(rad(b.Lat))*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthKm * math.Asin(math.Min(1, math.Sqrt(h)))
}

// RoadKm is the estimated road distance between two places.
func RoadKm(a, b contracts.LatLng) float64 { return HaversineKm(a, b) * RoadFactor }

/* ---------------- from a quote to real routes ---------------- */

// CodeFunc makes the hand-over code of a drop stop.
type CodeFunc func(stopID string) (string, error)

// RandomCode is a random 4-digit one-time code.
func RandomCode(string) (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(9000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%04d", 1000+n.Int64()), nil
}

// RoutesFromOption turns the routes of a chosen option into the plan's real
// routes: stable ids (<plan>:r1, <plan>:r1:s1, ...), waiting for a driver, with
// a hand-over code on every drop. code may be nil for RandomCode.
func RoutesFromOption(planID string, opt contracts.PlanOption, code CodeFunc) ([]contracts.Route, error) {
	if code == nil {
		code = RandomCode
	}
	out := make([]contracts.Route, len(opt.Routes))
	for i, r := range opt.Routes {
		id := fmt.Sprintf("%s:r%d", planID, i+1)
		r.ID, r.PlanID, r.Status = id, planID, contracts.RouteStatusAwaitingDriver
		stops := make([]contracts.RouteStop, len(r.Stops))
		for j, s := range r.Stops {
			s.ID, s.Status = fmt.Sprintf("%s:s%d", id, j+1), contracts.StopStatusWaiting
			if s.Kind == contracts.StopKindDropoff {
				c, err := code(s.ID)
				if err != nil {
					return nil, err
				}
				s.DeliveryCode = c
			}
			stops[j] = s
		}
		r.Stops = stops
		out[i] = r
	}
	return out, nil
}

// startMovedKm is how far a route's start may move before its drawn road path no longer applies.
const startMovedKm = 0.02

// RebaseStart moves a route's start (to the position of the driver assigned to
// it) and its departure time, and recomputes the first leg and every ETA. The
// price does not change: the way to the first pickup is not billed to the sender.
// The drawn path is dropped when the start moved; the owner draws it again.
func RebaseStart(r contracts.Route, start contracts.LatLng, startAt time.Time) contracts.Route {
	moved := HaversineKm(r.StartPosition, start) > startMovedKm
	r.StartPosition, r.StartAt = start, contracts.At(startAt)
	if moved {
		r.Geometry = nil
	}
	if len(r.Stops) == 0 {
		return r
	}
	legKm := RoadKm(start, r.Stops[0].Waypoint.Position)
	stops := make([]contracts.RouteStop, len(r.Stops))
	var eta float64
	for i, s := range r.Stops {
		if i == 0 {
			s.LegKm, s.LegMin = Round1(legKm), LegMin(r.VehicleType, legKm, HourWIB(startAt))
		}
		eta += s.LegMin
		s.EtaMin, s.ArriveAt = Round1(eta), contracts.At(plusMin(startAt, eta))
		eta += DwellMin(s.Kind)
		stops[i] = s
	}
	r.Stops, r.DurationMin = stops, Round1(eta)
	return r
}

// ReanchorETAs recomputes the arrival time of every stop from fromIndex on,
// given when the leg to that stop began. Earlier stops are left alone.
func ReanchorETAs(r contracts.Route, fromIndex int, legStart time.Time) contracts.Route {
	cursor := legStart
	stops := make([]contracts.RouteStop, len(r.Stops))
	for i, s := range r.Stops {
		if i >= fromIndex {
			cursor = plusMin(cursor, s.LegMin)
			s.ArriveAt = contracts.At(cursor)
			cursor = plusMin(cursor, DwellMin(s.Kind))
		}
		stops[i] = s
	}
	r.Stops = stops
	return r
}

/* ---------------- plan status ---------------- */

// OpenIndex is the index of the first stop that is not finished or problematic, or -1.
func OpenIndex(r contracts.Route) int {
	for i, s := range r.Stops {
		if s.Status != contracts.StopStatusDone && s.Status != contracts.StopStatusProblem {
			return i
		}
	}
	return -1
}

// StatusOf is the status of a confirmed plan that follows from its routes: running
// as soon as one runs, assigned once every route has a driver, finished when all are.
func StatusOf(routes []contracts.Route) contracts.PlanStatus {
	if len(routes) == 0 {
		return contracts.PlanStatusAwaitingDriver
	}
	allDone, allAssigned := true, true
	for _, r := range routes {
		if r.Status == contracts.RouteStatusRunning {
			return contracts.PlanStatusRunning
		}
		if r.Status != contracts.RouteStatusDone {
			allDone = false
		}
		if r.Status != contracts.RouteStatusAssigned && r.Status != contracts.RouteStatusDone {
			allAssigned = false
		}
	}
	switch {
	case allDone:
		return contracts.PlanStatusDone
	case allAssigned:
		return contracts.PlanStatusAssigned
	}
	return contracts.PlanStatusAwaitingDriver
}
