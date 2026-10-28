package roads

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"net/url"
	"slices"
	"sync"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

// None of our vehicles may use toll roads. Mapbox's Matrix API cannot leave them out (it has no exclude, and a
// request that names one is refused), but its Directions API can. So the table is built from Directions requests.
//
// A request through the waypoints a, b, a, c, ... returns the distance of every leg between consecutive waypoints, so
// one request measures up to 24 ordered pairs and a table of 12 places takes about 6 requests, not 132. For each leg
// to be the route a request between just its two places would give, the route must be allowed to turn back at a
// waypoint (continue_straight=false): measured against Mapbox, with the default a third of the legs came out
// different, with false none did.
//
// A place that lies on a toll road cannot be reached without it. Mapbox then routes over the toll road anyway; the
// distance is still the road's own.
const (
	maxLegs  = maxCoords - 1 // a Directions request takes 25 waypoints, so 24 legs
	parallel = 8             // requests in flight at once for one table (Mapbox allows 300 a minute)
)

// errNoRoute marks a leg Mapbox cannot route (no road between the places, or no road near one of them).
var errNoRoute = errors.New("mapbox: no route")

// walkOver returns a sequence of places whose consecutive pairs include every arc in missing, where missing[i] lists
// the places a leg from i must reach. When the walk is stuck, because no wanted arc leaves the place it is at, it
// jumps to a place that still has one; the jump is an ordinary pair that gets measured without being needed.
func walkOver(missing [][]int) []int {
	next := make([][]int, len(missing))
	left := 0
	for i, a := range missing {
		next[i] = slices.Clone(a)
		left += len(a)
	}
	if left == 0 {
		return nil
	}
	cur := slices.IndexFunc(next, func(a []int) bool { return len(a) > 0 })
	walk := []int{cur}
	for left > 0 {
		if len(next[cur]) == 0 {
			cur = slices.IndexFunc(next, func(a []int) bool { return len(a) > 0 })
			walk = append(walk, cur)
			continue
		}
		cur, next[cur] = next[cur][0], next[cur][1:]
		walk = append(walk, cur)
		left--
	}
	return walk
}

// windows cuts a walk into requests of at most 25 waypoints. Neighbouring windows share their joint, so every leg of
// the walk is in exactly one window.
func windows(walk []int) [][]int {
	var out [][]int
	for lo := 0; lo < len(walk)-1; lo += maxLegs {
		out = append(out, walk[lo:min(lo+maxCoords, len(walk))])
	}
	return out
}

// leg is the measured distance of one leg; ok is false when Mapbox has no route for it.
type leg struct {
	meters float64
	ok     bool
}

type legsResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Routes  []struct {
		Legs []struct {
			Distance float64 `json:"distance"`
		} `json:"legs"`
	} `json:"routes"`
}

// legs asks Directions for the legs between consecutive coords, over roads without tolls. A route that cannot be
// found for any leg fails the whole request with errNoRoute.
func (m *Mapbox) legs(ctx context.Context, coords []contracts.LatLng) ([]float64, error) {
	q := url.Values{"overview": {"false"}, "exclude": {"toll"}, "continue_straight": {"false"}, "access_token": {m.Token}}
	endpoint := fmt.Sprintf("%s/directions/v5/mapbox/driving/%s?%s", m.base(), url.PathEscape(coordList(coords)), q.Encode())
	body, err := m.get(ctx, endpoint)
	if err != nil {
		var ae *apiError
		if errors.As(err, &ae) && ae.noRoute() {
			return nil, errNoRoute
		}
		return nil, err
	}
	var res legsResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("mapbox: decode directions: %w", err)
	}
	switch res.Code {
	case "Ok":
	case "NoRoute", "NoSegment":
		return nil, errNoRoute
	default:
		return nil, fmt.Errorf("mapbox: directions %s: %s", res.Code, res.Message)
	}
	if len(res.Routes) == 0 || len(res.Routes[0].Legs) != len(coords)-1 {
		return nil, fmt.Errorf("mapbox: directions returned a route of the wrong shape for %d waypoints", len(coords))
	}
	out := make([]float64, len(coords)-1)
	for i, l := range res.Routes[0].Legs {
		out[i] = l.Distance
	}
	return out, nil
}

// measureWalk measures the legs of a window. When a request fails for want of a route, it is split in two (the halves
// share their joint) until the leg without a route stands alone; that leg alone is reported as having none.
func (m *Mapbox) measureWalk(ctx context.Context, places []contracts.LatLng, win []int) ([]leg, error) {
	coords := make([]contracts.LatLng, len(win))
	for i, p := range win {
		coords[i] = places[p]
	}
	d, err := m.legs(ctx, coords)
	if err == nil {
		out := make([]leg, len(d))
		for i, v := range d {
			out[i] = leg{meters: v, ok: true}
		}
		return out, nil
	}
	if !errors.Is(err, errNoRoute) {
		return nil, err
	}
	if len(win) == 2 {
		return []leg{{}}, nil
	}
	mid := len(win) / 2
	left, err := m.measureWalk(ctx, places, win[:mid+1])
	if err != nil {
		return nil, err
	}
	right, err := m.measureWalk(ctx, places, win[mid:])
	if err != nil {
		return nil, err
	}
	return append(left, right...), nil
}

// matrixAvoidingTolls builds the table of distances over roads without tolls. Pairs already in the cache cost
// nothing; the rest are measured by walking through them.
func (m *Mapbox) matrixAvoidingTolls(ctx context.Context, places []contracts.LatLng) (Matrix, error) {
	n := len(places)
	out := Matrix{N: n, DistM: make([]int32, n*n), Source: "mapbox-cache"}
	missing := make([][]int, n)
	for i := range n {
		for j := range n {
			if i == j {
				continue
			}
			if m.Cache != nil {
				if d, ok := m.Cache.Get(places[i], places[j]); ok {
					out.DistM[i*n+j] = d
					continue
				}
			}
			missing[i] = append(missing[i], j)
		}
	}
	walk := walkOver(missing)
	if len(walk) == 0 {
		return out, nil
	}
	out.Source = "mapbox"

	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
		done     = make([]bool, n*n)
		slots    = make(chan struct{}, parallel)
	)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for _, win := range windows(walk) {
		wg.Go(func() {
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				return
			}
			legs, err := m.measureWalk(ctx, places, win)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				return
			}
			for k, l := range legs {
				a, b := win[k], win[k+1]
				if done[a*n+b] {
					continue
				}
				done[a*n+b] = true
				if !l.ok {
					out.DistM[a*n+b] = EstimateMeters(places[a], places[b])
					out.Estimated++
					continue
				}
				meters := int32(math.Round(l.meters))
				out.DistM[a*n+b] = meters
				if m.Cache != nil {
					m.Cache.Put(places[a], places[b], meters)
				}
			}
		})
	}
	wg.Wait()
	if firstErr != nil {
		return Matrix{}, firstErr
	}
	return out, nil
}
