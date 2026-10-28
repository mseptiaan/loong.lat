package roads

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

func places(n int) []contracts.LatLng {
	out := make([]contracts.LatLng, n)
	for i := range out {
		out[i] = contracts.LatLng{Lat: -6.2 - 0.01*float64(i), Lng: 106.8 + 0.01*float64(i)}
	}
	return out
}

// fakeMapbox answers matrix requests with |i-j| km for the places of a request, honoring sources and destinations.
func fakeMapbox(t *testing.T, requests *atomic.Int32, nullPair [2]int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Query().Get("access_token") != "tok" {
			http.Error(w, `{"message":"Not Authorized - Invalid Token"}`, http.StatusUnauthorized)
			return
		}
		raw, _ := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/directions-matrix/v1/mapbox/driving/"))
		coords := strings.Split(raw, ";")
		if len(coords) > maxCoords {
			http.Error(w, `{"message":"Too many coordinates"}`, http.StatusUnprocessableEntity)
			return
		}
		idx := func(param string) []int {
			if s := r.URL.Query().Get(param); s != "" {
				var out []int
				for _, p := range strings.Split(s, ";") {
					v, _ := strconv.Atoi(p)
					out = append(out, v)
				}
				return out
			}
			out := make([]int, len(coords))
			for i := range out {
				out[i] = i
			}
			return out
		}
		// The place number is encoded in the longitude: 106.8 + 0.01*i.
		num := func(c string) int {
			lng, _ := strconv.ParseFloat(strings.Split(c, ",")[0], 64)
			return int((lng-106.8)*100 + 0.5)
		}
		var rows []string
		for _, s := range idx("sources") {
			var cells []string
			for _, d := range idx("destinations") {
				a, b := num(coords[s]), num(coords[d])
				switch {
				case a == b:
					cells = append(cells, "0")
				case [2]int{a, b} == nullPair:
					cells = append(cells, "null")
				default:
					cells = append(cells, strconv.Itoa(1000*abs(a-b)))
				}
			}
			rows = append(rows, "["+strings.Join(cells, ",")+"]")
		}
		fmt.Fprintf(w, `{"code":"Ok","distances":[%s]}`, strings.Join(rows, ","))
	}))
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func TestMatrixOfASmallTableIsOneRequest(t *testing.T) {
	var n atomic.Int32
	srv := fakeMapbox(t, &n, [2]int{-1, -1})
	defer srv.Close()
	m := &Mapbox{AllowTolls: true, Token: "tok", BaseURL: srv.URL}
	got, err := m.Matrix(context.Background(), places(5))
	if err != nil {
		t.Fatal(err)
	}
	if n.Load() != 1 || got.Source != "mapbox" || got.Estimated != 0 {
		t.Errorf("requests %d, source %s, estimated %d", n.Load(), got.Source, got.Estimated)
	}
	if got.DistM[0*5+3] != 3000 || got.DistM[4*5+1] != 3000 {
		t.Errorf("distances wrong: %v", got.DistM)
	}
}

func TestMatrixOfALargeTableIsFetchedInBlocks(t *testing.T) {
	var n atomic.Int32
	srv := fakeMapbox(t, &n, [2]int{-1, -1})
	defer srv.Close()
	m := &Mapbox{AllowTolls: true, Token: "tok", BaseURL: srv.URL, Pace: time.Millisecond}
	got, err := m.Matrix(context.Background(), places(30))
	if err != nil {
		t.Fatal(err)
	}
	if n.Load() != 9 { // three blocks of up to twelve, every pair of blocks once
		t.Errorf("%d requests, want 9", n.Load())
	}
	for _, p := range [][2]int{{0, 29}, {29, 0}, {13, 14}, {12, 25}, {5, 5}} {
		if want := int32(1000 * abs(p[0]-p[1])); got.DistM[p[0]*30+p[1]] != want {
			t.Errorf("distance %v = %d, want %d", p, got.DistM[p[0]*30+p[1]], want)
		}
	}
}

func TestUnroutablePairsAreEstimatedAndCounted(t *testing.T) {
	var n atomic.Int32
	srv := fakeMapbox(t, &n, [2]int{0, 2})
	defer srv.Close()
	m := &Mapbox{AllowTolls: true, Token: "tok", BaseURL: srv.URL}
	got, err := m.Matrix(context.Background(), places(3))
	if err != nil {
		t.Fatal(err)
	}
	if got.Estimated != 1 {
		t.Errorf("estimated = %d, want 1", got.Estimated)
	}
	if got.DistM[0*3+2] <= 0 || got.DistM[0*3+2] == 2000 {
		t.Errorf("the unroutable pair must be a straight-line estimate, got %d", got.DistM[0*3+2])
	}
}

func TestCachedPairsCostNoRequest(t *testing.T) {
	var n atomic.Int32
	srv := fakeMapbox(t, &n, [2]int{-1, -1})
	defer srv.Close()
	m := &Mapbox{AllowTolls: true, Token: "tok", BaseURL: srv.URL, Cache: NewMemoryCache(1000, time.Hour)}
	ps := places(4)
	first, err := m.Matrix(context.Background(), ps)
	if err != nil {
		t.Fatal(err)
	}
	again, err := m.Matrix(context.Background(), ps)
	if err != nil {
		t.Fatal(err)
	}
	if n.Load() != 1 || again.Source != "mapbox-cache" {
		t.Errorf("requests %d, source %s: the second table must come from the cache", n.Load(), again.Source)
	}
	for i := range first.DistM {
		if first.DistM[i] != again.DistM[i] {
			t.Fatalf("cached distances differ at %d", i)
		}
	}
}

func TestCacheEntriesExpire(t *testing.T) {
	c := NewMemoryCache(10, time.Minute)
	clock := time.Now()
	c.now = func() time.Time { return clock }
	a, b := contracts.LatLng{Lat: -6.2, Lng: 106.8}, contracts.LatLng{Lat: -6.3, Lng: 106.9}
	c.Put(a, b, 1234)
	if d, ok := c.Get(a, b); !ok || d != 1234 {
		t.Fatalf("Get = %d, %v", d, ok)
	}
	clock = clock.Add(2 * time.Minute)
	if _, ok := c.Get(a, b); ok {
		t.Error("an entry past its TTL must be gone")
	}
}

func TestErrorsNeverLeakTheToken(t *testing.T) {
	var n atomic.Int32
	srv := fakeMapbox(t, &n, [2]int{-1, -1})
	defer srv.Close()
	m := &Mapbox{Token: "secret-token", BaseURL: srv.URL, Backoff: time.Millisecond}
	_, err := m.Matrix(context.Background(), places(3))
	if err == nil {
		t.Fatal("a rejected token must fail")
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Errorf("the error leaks the token: %v", err)
	}
	m.BaseURL = "http://127.0.0.1:1" // nothing listens here
	m.Token = "secret-token"
	if _, err := m.Matrix(context.Background(), places(3)); err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Errorf("a network error must not leak the token either: %v", err)
	}
}

func TestRateLimitingIsRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, `{"code":"Ok","distances":[[0,500],[500,0]]}`)
	}))
	defer srv.Close()
	m := &Mapbox{AllowTolls: true, Token: "tok", BaseURL: srv.URL, Backoff: time.Millisecond}
	got, err := m.Matrix(context.Background(), places(2))
	if err != nil || calls.Load() != 2 || got.DistM[1] != 500 {
		t.Errorf("err %v, calls %d, distances %v", err, calls.Load(), got.DistM)
	}
}

func TestEstimateNeedsNoNetwork(t *testing.T) {
	got, err := Estimate{}.Matrix(context.Background(), places(3))
	if err != nil || got.Source != "estimate" || got.DistM[1] <= 0 || got.DistM[0] != 0 {
		t.Errorf("%+v %v", got, err)
	}
}

func TestGeometryJoinsChunksOfLongRoutes(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		raw, _ := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/directions/v5/mapbox/driving/"))
		coords := strings.Split(raw, ";")
		var pts []string
		for _, c := range coords {
			pts = append(pts, "["+c+"]")
		}
		fmt.Fprintf(w, `{"code":"Ok","routes":[{"geometry":{"coordinates":[%s]}}]}`, strings.Join(pts, ","))
	}))
	defer srv.Close()
	m := &Mapbox{Token: "tok", BaseURL: srv.URL}
	path, err := m.Geometry(context.Background(), places(30))
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Errorf("%d requests, 30 waypoints need two chunks of at most 25", calls.Load())
	}
	if len(path) != 30 { // the shared joint is not repeated
		t.Errorf("path has %d points, want 30", len(path))
	}
}
