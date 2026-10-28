package roads

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

// Mapbox limits: a driving matrix request takes at most 25 coordinates; larger
// tables are fetched block by block, each pair of blocks within that limit.
const (
	maxCoords    = 25
	blockSize    = 12
	defaultBase  = "https://api.mapbox.com"
	defaultPace  = 1100 * time.Millisecond // the Matrix API allows about 60 requests a minute
	httpTimeout  = 20 * time.Second
	maxRetries   = 3
	maxBodyBytes = 8 << 20
)

// Mapbox measures roads with the Directions API, and with the Matrix API when tolls are allowed. Mapbox is a helper:
// it supplies road data only. All optimization happens in our own engine.
type Mapbox struct {
	// Token is a Mapbox access token. It stays on the server and is never put in a log line.
	Token string
	// BaseURL defaults to https://api.mapbox.com; tests point it elsewhere.
	BaseURL string
	Client  *http.Client
	// Cache remembers pairs already measured, so a repeated quote costs no request.
	Cache PairCache
	// AllowTolls lets routes use toll roads, and measures the table with the Matrix API, which is faster but cannot
	// leave tolls out. By default no route uses a toll road: our vehicles may not (see mapbox_tollfree.go).
	AllowTolls bool
	// Pace is the pause between the requests of one large table.
	Pace time.Duration
	// Backoff is the first wait before retrying a request; it doubles each time.
	Backoff time.Duration
	Log     *slog.Logger
}

// PairCache stores measured road distances by pair of places.
type PairCache interface {
	Get(from, to contracts.LatLng) (meters int32, ok bool)
	Put(from, to contracts.LatLng, meters int32)
}

func (m *Mapbox) base() string {
	if m.BaseURL != "" {
		return m.BaseURL
	}
	return defaultBase
}

func (m *Mapbox) client() *http.Client {
	if m.Client != nil {
		return m.Client
	}
	return &http.Client{Timeout: httpTimeout}
}

func (m *Mapbox) pace() time.Duration {
	if m.Pace > 0 {
		return m.Pace
	}
	return defaultPace
}

func (m *Mapbox) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.Default()
}

func coord(p contracts.LatLng) string {
	return strconv.FormatFloat(p.Lng, 'f', 6, 64) + "," + strconv.FormatFloat(p.Lat, 'f', 6, 64)
}

func coordList(ps []contracts.LatLng) string {
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = coord(p)
	}
	return strings.Join(parts, ";")
}

// Matrix implements Provider.
func (m *Mapbox) Matrix(ctx context.Context, places []contracts.LatLng) (Matrix, error) {
	n := len(places)
	out := Matrix{N: n, DistM: make([]int32, n*n), Source: "mapbox"}
	if n <= 1 {
		return out, nil
	}
	if m.Token == "" {
		return Matrix{}, errors.New("mapbox: no access token")
	}

	if !m.AllowTolls {
		return m.matrixAvoidingTolls(ctx, places)
	}

	// A table whose every pair is known costs nothing.
	if m.Cache != nil {
		complete := true
	scan:
		for i := range n {
			for j := range n {
				if i == j {
					continue
				}
				d, ok := m.Cache.Get(places[i], places[j])
				if !ok {
					complete = false
					break scan
				}
				out.DistM[i*n+j] = d
			}
		}
		if complete {
			out.Source = "mapbox-cache"
			return out, nil
		}
	}

	if n <= maxCoords {
		d, err := m.fetchBlock(ctx, places, nil, nil)
		if err != nil {
			return Matrix{}, err
		}
		m.fill(&out, places, d, nil, nil)
	} else {
		var blocks [][]int
		for lo := 0; lo < n; lo += blockSize {
			var b []int
			for i := lo; i < min(lo+blockSize, n); i++ {
				b = append(b, i)
			}
			blocks = append(blocks, b)
		}
		first := true
		for _, rows := range blocks {
			for _, cols := range blocks {
				if !first {
					select {
					case <-time.After(m.pace()):
					case <-ctx.Done():
						return Matrix{}, ctx.Err()
					}
				}
				first = false
				coords, src, dst := blockCoords(places, rows, cols)
				d, err := m.fetchBlock(ctx, coords, src, dst)
				if err != nil {
					return Matrix{}, err
				}
				m.fill(&out, places, d, rows, cols)
			}
		}
	}
	return out, nil
}

// blockCoords lists the coordinates of two blocks once each, with the positions of the sources and destinations among them.
func blockCoords(places []contracts.LatLng, rows, cols []int) (coords []contracts.LatLng, src, dst []int) {
	pos := map[int]int{}
	add := func(i int) int {
		if p, ok := pos[i]; ok {
			return p
		}
		pos[i] = len(coords)
		coords = append(coords, places[i])
		return pos[i]
	}
	for _, i := range rows {
		src = append(src, add(i))
	}
	for _, j := range cols {
		dst = append(dst, add(j))
	}
	return coords, src, dst
}

type matrixResponse struct {
	Code      string       `json:"code"`
	Message   string       `json:"message"`
	Distances [][]*float64 `json:"distances"`
}

// fetchBlock asks for the distances from the sources to the destinations among coords.
// With no sources and destinations it asks for the full table.
func (m *Mapbox) fetchBlock(ctx context.Context, coords []contracts.LatLng, src, dst []int) ([][]*float64, error) {
	q := url.Values{"annotations": {"distance"}, "access_token": {m.Token}}
	if src != nil {
		q.Set("sources", joinInts(src))
		q.Set("destinations", joinInts(dst))
	}
	endpoint := fmt.Sprintf("%s/directions-matrix/v1/mapbox/driving/%s?%s", m.base(), url.PathEscape(coordList(coords)), q.Encode())
	body, err := m.get(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	var res matrixResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("mapbox: decode matrix: %w", err)
	}
	if res.Code != "Ok" {
		return nil, fmt.Errorf("mapbox: matrix %s: %s", res.Code, res.Message)
	}
	return res.Distances, nil
}

func joinInts(v []int) string {
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = strconv.Itoa(x)
	}
	return strings.Join(parts, ";")
}

// fill copies a block's distances into the table and the cache. An unroutable pair
// (null) is estimated from the straight line and counted. With nil index lists the
// block is the full table over the same places.
func (m *Mapbox) fill(out *Matrix, places []contracts.LatLng, d [][]*float64, rows, cols []int) {
	n := out.N
	rowIdx := func(i int) int { return i }
	colIdx := func(j int) int { return j }
	nr, nc := len(d), 0
	if len(d) > 0 {
		nc = len(d[0])
	}
	if rows != nil {
		rowIdx = func(i int) int { return rows[i] }
		colIdx = func(j int) int { return cols[j] }
	}
	for i := range nr {
		for j := range nc {
			a, b := rowIdx(i), colIdx(j)
			if a == b {
				continue
			}
			var meters int32
			if v := d[i][j]; v != nil {
				meters = int32(math.Round(*v))
				if m.Cache != nil {
					m.Cache.Put(places[a], places[b], meters)
				}
			} else {
				meters = EstimateMeters(places[a], places[b])
				out.Estimated++
			}
			out.DistM[a*n+b] = meters
		}
	}
}

// get performs a GET with retries on rate limiting and server errors. Errors never
// contain the request URL, which carries the token.
func (m *Mapbox) get(ctx context.Context, endpoint string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			wait := time.Duration(1<<(attempt-1)) * cmpDuration(m.Backoff, time.Second)
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, errors.New("mapbox: build request")
		}
		resp, err := m.client().Do(req)
		if err != nil {
			var uerr *url.Error
			if errors.As(err, &uerr) {
				err = uerr.Err // drop the URL, keep the cause
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = fmt.Errorf("mapbox: %w", err)
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
			lastErr = fmt.Errorf("mapbox: http %d", resp.StatusCode)
			continue
		case resp.StatusCode != http.StatusOK:
			return nil, newAPIError(resp.StatusCode, body)
		case readErr != nil:
			lastErr = fmt.Errorf("mapbox: read: %w", readErr)
			continue
		}
		return body, nil
	}
	return nil, lastErr
}

// apiError is a refusal by Mapbox: the HTTP status and, when the body has them, Mapbox's own code and message. Its
// text never contains the request URL, which carries the token.
type apiError struct {
	Status        int
	Code, Message string
}

func newAPIError(status int, body []byte) *apiError {
	var b struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &b) // a body that is not JSON just leaves the detail out
	return &apiError{Status: status, Code: b.Code, Message: b.Message}
}

func (e *apiError) Error() string {
	detail := e.Message
	if detail == "" {
		detail = "no detail"
	}
	return fmt.Sprintf("mapbox: http %d: %s", e.Status, detail)
}

// noRoute says the refusal is about the places, not about us: there is no road between them or none near one.
func (e *apiError) noRoute() bool { return e.Code == "NoRoute" || e.Code == "NoSegment" }

type directionsResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Routes  []struct {
		Geometry struct {
			Coordinates []contracts.LngLat `json:"coordinates"`
		} `json:"geometry"`
	} `json:"routes"`
}

// Geometry is the road path through the waypoints in order, as [lng, lat] pairs.
// Long routes are cut into chunks of at most 24 legs that share their joints.
func (m *Mapbox) Geometry(ctx context.Context, waypoints []contracts.LatLng) ([]contracts.LngLat, error) {
	if len(waypoints) < 2 {
		return nil, nil
	}
	if m.Token == "" {
		return nil, errors.New("mapbox: no access token")
	}
	var path []contracts.LngLat
	for lo := 0; lo < len(waypoints)-1; lo += maxCoords - 1 {
		chunk := waypoints[lo:min(lo+maxCoords, len(waypoints))]
		q := url.Values{"geometries": {"geojson"}, "overview": {"full"}, "access_token": {m.Token}}
		if !m.AllowTolls {
			// the same rules as the table, so that the line drawn is the road the distances were measured on
			q.Set("exclude", "toll")
			q.Set("continue_straight", "false")
		}
		endpoint := fmt.Sprintf("%s/directions/v5/mapbox/driving/%s?%s", m.base(), url.PathEscape(coordList(chunk)), q.Encode())
		body, err := m.get(ctx, endpoint)
		if err != nil {
			return nil, err
		}
		var res directionsResponse
		if err := json.Unmarshal(body, &res); err != nil {
			return nil, fmt.Errorf("mapbox: decode directions: %w", err)
		}
		if res.Code != "Ok" || len(res.Routes) == 0 {
			return nil, fmt.Errorf("mapbox: directions %s: %s", res.Code, res.Message)
		}
		coords := res.Routes[0].Geometry.Coordinates
		if len(path) > 0 && len(coords) > 0 {
			coords = coords[1:] // the joint is already the end of the previous chunk
		}
		path = append(path, coords...)
	}
	return path, nil
}

func cmpDuration(d, fallback time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return fallback
}
