package routeplan

import (
	"errors"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

// The lifecycle of a route as its owners and followers see it. Every service
// that keeps a copy of a route applies these functions to it, with the time the
// event carried, so all copies change the same way. They are pure: a route goes
// in, a changed copy comes out.

// Transition errors. A follower applying events in order can treat them as a
// stale or duplicate event; the driver service turns them into answers.
var (
	ErrStopNotFound  = errors.New("routeplan: no such stop on the route")
	ErrNotAssigned   = errors.New("routeplan: the route has no driver yet")
	ErrNotRunning    = errors.New("routeplan: the route is not running")
	ErrNotActiveStop = errors.New("routeplan: another stop comes first")
	ErrNotEnRoute    = errors.New("routeplan: the stop was already reached")
	ErrNotArrived    = errors.New("routeplan: the stop has not been reached yet")
)

// Assign gives the route to a driver. The caller has already moved its start to
// the driver's position (RebaseStart); startAt is the planned departure.
func Assign(r contracts.Route, driverID, vehicleID string, at time.Time) contracts.Route {
	r.DriverID, r.VehicleID = driverID, vehicleID
	r.Status, r.AssignedAt = contracts.RouteStatusAssigned, contracts.At(at)
	return r
}

// Unassign takes the route back from its driver.
func Unassign(r contracts.Route) contracts.Route {
	r.DriverID, r.VehicleID = "", ""
	r.Status, r.AssignedAt = contracts.RouteStatusAwaitingDriver, contracts.Time{}
	return r
}

// Start sets the route off: it runs, the first stop is being driven to, and the
// arrival times count from now.
func Start(r contracts.Route, at time.Time) (contracts.Route, error) {
	if r.Status != contracts.RouteStatusAssigned {
		return r, ErrNotAssigned
	}
	r.Status, r.StartedAt, r.LegStartedAt = contracts.RouteStatusRunning, contracts.At(at), contracts.At(at)
	r.StartAt = contracts.At(at)
	r.Stops = cloneStops(r.Stops)
	if len(r.Stops) > 0 {
		r.Stops[0].Status = contracts.StopStatusEnRoute
	}
	return ReanchorETAs(r, 0, at), nil
}

// Arrive marks the active stop as reached.
func Arrive(r contracts.Route, stopID string, at time.Time) (contracts.Route, error) {
	i, err := activeStop(r, stopID)
	if err != nil {
		return r, err
	}
	if r.Stops[i].Status != contracts.StopStatusEnRoute {
		return r, ErrNotEnRoute
	}
	r.Stops = cloneStops(r.Stops)
	r.Stops[i].Status, r.Stops[i].ArrivedAt = contracts.StopStatusArrived, contracts.At(at)
	return r, nil
}

// Close finishes the stop (done or problem) and moves the route on to its next
// open stop, or finishes the route when none is left. finished reports the latter.
// A drop-off carries its proof; a problem carries the issue as a message.
func Close(r contracts.Route, stopID string, at time.Time, result contracts.StopStatus, proof *contracts.DeliveryProof, issue *contracts.Msg) (_ contracts.Route, finished bool, err error) {
	if result != contracts.StopStatusDone && result != contracts.StopStatusProblem {
		return r, false, errors.New("routeplan: a stop closes as done or problem")
	}
	i, err := activeStop(r, stopID)
	if err != nil {
		return r, false, err
	}
	if st := r.Stops[i].Status; result == contracts.StopStatusDone && st != contracts.StopStatusArrived {
		return r, false, ErrNotArrived
	}
	r.Stops = cloneStops(r.Stops)
	s := &r.Stops[i]
	s.Status, s.CompletedAt = result, contracts.At(at)
	if s.ArrivedAt.IsZero() {
		s.ArrivedAt = contracts.At(at)
	}
	if proof != nil {
		s.Proof = proof
	}
	if issue != nil {
		s.Issue = issue
	}

	next := OpenIndex(r)
	if next < 0 {
		r.Status, r.FinishedAt = contracts.RouteStatusDone, contracts.At(at)
		return r, true, nil
	}
	r.Stops[next].Status = contracts.StopStatusEnRoute
	r.LegStartedAt = contracts.At(at)
	return ReanchorETAs(r, next, at), false, nil
}

// activeStop finds the stop the driver is at, which must be the first open one on a running route.
func activeStop(r contracts.Route, stopID string) (int, error) {
	if r.Status != contracts.RouteStatusRunning {
		return -1, ErrNotRunning
	}
	idx := -1
	for i, s := range r.Stops {
		if s.ID == stopID {
			idx = i
		}
	}
	if idx < 0 {
		return -1, ErrStopNotFound
	}
	if OpenIndex(r) != idx {
		return -1, ErrNotActiveStop
	}
	return idx, nil
}

func cloneStops(stops []contracts.RouteStop) []contracts.RouteStop {
	out := make([]contracts.RouteStop, len(stops))
	copy(out, stops)
	return out
}
