package roads

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// directionsFake answers Directions requests the way Mapbox does for several waypoints: the distance of each leg is
// 1000 m times the difference of the place numbers (the number is encoded in the longitude, see places).
type directionsFake struct {
	mu       sync.Mutex
	requests [][]int // the place numbers of each request's waypoints
	queries  []url.Values
	matrix   int // hits on the Matrix API, which must stay at zero
	noRoute  [2]int
	// noSegment answers a request that contains the arc with HTTP 422 NoSegment instead of 200 NoRoute.
	noSegment bool
}

func placeNumber(c string) int {
	lng, _ := strconv.ParseFloat(strings.Split(c, ",")[0], 64)
	return int((lng-106.8)*100 + 0.5)
}

func (f *directionsFake) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.HasPrefix(r.URL.Path, "/directions-matrix/") {
		f.matrix++
		http.Error(w, `{"message":"Request too large for custom parameters"}`, http.StatusUnprocessableEntity)
		return
	}
	if r.URL.Query().Get("access_token") != "tok" {
		http.Error(w, `{"message":"Not Authorized - Invalid Token"}`, http.StatusUnauthorized)
		return
	}
	raw, _ := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/directions/v5/mapbox/driving/"))
	coords := strings.Split(raw, ";")
	if len(coords) > maxCoords {
		http.Error(w, `{"code":"InvalidInput","message":"Too many coordinates"}`, http.StatusUnprocessableEntity)
		return
	}
	nums := make([]int, len(coords))
	for i, c := range coords {
		nums[i] = placeNumber(c)
	}
	f.requests = append(f.requests, nums)
	f.queries = append(f.queries, r.URL.Query())
	var legs []string
	for i := 0; i+1 < len(nums); i++ {
		if [2]int{nums[i], nums[i+1]} == f.noRoute {
			if f.noSegment {
				http.Error(w, `{"code":"NoSegment","message":"No road segment could be matched"}`, http.StatusUnprocessableEntity)
				return
			}
			fmt.Fprint(w, `{"code":"NoRoute","message":"No route found"}`)
			return
		}
		legs = append(legs, fmt.Sprintf(`{"distance":%d}`, 1000*abs(nums[i]-nums[i+1])))
	}
	fmt.Fprintf(w, `{"code":"Ok","routes":[{"legs":[%s]}]}`, strings.Join(legs, ","))
}

func newDirectionsFake(t *testing.T) (*directionsFake, *Mapbox) {
	t.Helper()
	f := &directionsFake{noRoute: [2]int{-1, -1}}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	return f, &Mapbox{Token: "tok", BaseURL: srv.URL, Backoff: time.Millisecond}
}

func TestWalkCoversEveryMissingArcInFewRequests(t *testing.T) {
	rnd := rand.New(rand.NewPCG(1, 2))
	for trial := range 200 {
		n := 2 + rnd.IntN(29)
		missing := make([][]int, n)
		want := map[[2]int]bool{}
		for i := range n {
			for j := range n {
				if i != j && rnd.IntN(3) > 0 {
					missing[i] = append(missing[i], j)
					want[[2]int{i, j}] = true
				}
			}
		}
		walk := walkOver(missing)
		if len(want) == 0 {
			if walk != nil {
				t.Fatalf("trial %d: nothing is missing, the walk is %v", trial, walk)
			}
			continue
		}
		covered := map[[2]int]int{}
		for k := 0; k+1 < len(walk); k++ {
			covered[[2]int{walk[k], walk[k+1]}]++
		}
		for arc := range want {
			if covered[arc] == 0 {
				t.Fatalf("trial %d (n=%d): arc %v is not on the walk", trial, n, arc)
			}
		}
		// every leg of the walk is in exactly one window, and no window is longer than a request may be
		legs := 0
		for _, win := range windows(walk) {
			if len(win) < 2 || len(win) > maxCoords {
				t.Fatalf("trial %d: window of %d waypoints", trial, len(win))
			}
			legs += len(win) - 1
		}
		if legs != len(walk)-1 {
			t.Fatalf("trial %d: windows hold %d legs, the walk has %d", trial, legs, len(walk)-1)
		}
		// Jumps cost a leg each. A sparse set of wanted arcs strands the walk more often than a full table does
		// (measured here: up to about 45% extra legs), so the bound is generous; the full table has its own test.
		if len(walk)-1 > len(want)+len(want)/2+n {
			t.Errorf("trial %d (n=%d): %d legs for %d wanted arcs", trial, n, len(walk)-1, len(want))
		}
	}
}

func TestATollFreeTableOfTwelvePlacesTakesAHandfulOfRequests(t *testing.T) {
	f, m := newDirectionsFake(t)
	m.Cache = NewMemoryCache(1000, time.Hour)
	got, err := m.Matrix(context.Background(), places(12))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.requests) > 7 {
		t.Errorf("%d requests for 132 pairs, want at most 7 (24 legs each)", len(f.requests))
	}
	if f.matrix != 0 {
		t.Errorf("the Matrix API was used %d times: it cannot avoid tolls", f.matrix)
	}
	for _, q := range f.queries {
		if q.Get("exclude") != "toll" || q.Get("continue_straight") != "false" {
			t.Fatalf("a request without the toll exclusion or with the default turning rule: %v", q)
		}
	}
	for i := range 12 {
		for j := range 12 {
			if want := int32(1000 * abs(i-j)); got.DistM[i*12+j] != want {
				t.Fatalf("distance %d->%d = %d, want %d", i, j, got.DistM[i*12+j], want)
			}
		}
	}
	if got.Source != "mapbox" || got.Estimated != 0 {
		t.Errorf("source %q, estimated %d", got.Source, got.Estimated)
	}

	before := len(f.requests)
	again, err := m.Matrix(context.Background(), places(12))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != before || again.Source != "mapbox-cache" {
		t.Errorf("a second table made %d more requests, source %q: it must come from the cache", len(f.requests)-before, again.Source)
	}
}

func TestOnlyWhatTheCacheLacksIsMeasured(t *testing.T) {
	f, m := newDirectionsFake(t)
	m.Cache = NewMemoryCache(1000, time.Hour)
	ps := places(6)
	for i := range 6 {
		for j := range 6 {
			if i != j && i != 3 && j != 3 { // everything except the pairs with place 3 is known
				m.Cache.Put(ps[i], ps[j], int32(1000*abs(i-j)))
			}
		}
	}
	got, err := m.Matrix(context.Background(), ps)
	if err != nil {
		t.Fatal(err)
	}
	legs := 0
	for _, r := range f.requests {
		legs += len(r) - 1
	}
	if legs > 10+6 { // the ten arcs with place 3, plus a few jumps
		t.Errorf("%d legs measured, only 10 pairs were missing", legs)
	}
	for _, r := range f.requests {
		touches := false
		for _, p := range r {
			touches = touches || p == 3
		}
		if !touches {
			t.Errorf("request %v does not involve place 3, whose pairs were the only missing ones", r)
		}
	}
	if got.DistM[3*6+0] != 3000 || got.DistM[1*6+4] != 3000 {
		t.Errorf("distances wrong: %v", got.DistM)
	}
}

func TestAnUnroutableLegIsIsolatedAndEstimated(t *testing.T) {
	for _, noSegment := range []bool{false, true} {
		t.Run(fmt.Sprintf("noSegment=%v", noSegment), func(t *testing.T) {
			f, m := newDirectionsFake(t)
			f.noRoute, f.noSegment = [2]int{0, 2}, noSegment
			got, err := m.Matrix(context.Background(), places(8))
			if err != nil {
				t.Fatal(err)
			}
			if got.Estimated != 1 {
				t.Errorf("estimated = %d, want only the one pair without a route", got.Estimated)
			}
			if d := got.DistM[0*8+2]; d <= 0 || d == 2000 {
				t.Errorf("the pair without a route must be a straight-line estimate, got %d", d)
			}
			for i := range 8 {
				for j := range 8 {
					if [2]int{i, j} != [2]int{0, 2} && got.DistM[i*8+j] != int32(1000*abs(i-j)) {
						t.Fatalf("distance %d->%d = %d: one pair without a route spoiled another", i, j, got.DistM[i*8+j])
					}
				}
			}
		})
	}
}

func TestTheTokenIsRefusedWithoutLeakingIt(t *testing.T) {
	_, m := newDirectionsFake(t)
	m.Token = "secret-token"
	_, err := m.Matrix(context.Background(), places(3))
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Errorf("a refused token must fail without leaking it: %v", err)
	}
}

func TestGeometryAvoidsTollsUnlessTheyAreAllowed(t *testing.T) {
	var seen []url.Values
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Query())
		mu.Unlock()
		fmt.Fprint(w, `{"code":"Ok","routes":[{"geometry":{"coordinates":[[106.8,-6.2],[106.81,-6.21]]}}]}`)
	}))
	defer srv.Close()
	for _, allow := range []bool{false, true} {
		seen = nil
		m := &Mapbox{Token: "tok", BaseURL: srv.URL, AllowTolls: allow}
		if _, err := m.Geometry(context.Background(), places(3)); err != nil {
			t.Fatal(err)
		}
		if len(seen) != 1 {
			t.Fatalf("%d requests", len(seen))
		}
		if got := seen[0].Get("exclude") == "toll"; got == allow {
			t.Errorf("allowTolls=%v: exclude=%q", allow, seen[0].Get("exclude"))
		}
	}
}

// With tolls allowed the table comes from the Matrix API again, as before.
func TestAllowingTollsUsesTheMatrixAPI(t *testing.T) {
	f, _ := newDirectionsFake(t)
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/directions/") {
			f.handler(w, r)
			return
		}
		n++
		fmt.Fprint(w, `{"code":"Ok","distances":[[0,1000],[1000,0]]}`)
	}))
	defer srv.Close()
	m := &Mapbox{Token: "tok", BaseURL: srv.URL, AllowTolls: true}
	if _, err := m.Matrix(context.Background(), places(2)); err != nil || n != 1 || len(f.requests) != 0 {
		t.Errorf("err %v, matrix requests %d, directions requests %d", err, n, len(f.requests))
	}
}
