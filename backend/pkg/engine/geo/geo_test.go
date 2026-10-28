package geo

import (
	"math"
	"testing"
)

func TestHaversine(t *testing.T) {
	// Ottawa -> Toronto is roughly 351 km as the crow flies.
	got := Haversine(LatLon{45.4236, -75.7003}, LatLon{43.6532, -79.3832})
	if math.Abs(got-351_000) > 5_000 {
		t.Errorf("Haversine Ottawa-Toronto = %.0f m, want about 351000", got)
	}
	if d := Haversine(LatLon{1, 1}, LatLon{1, 1}); d != 0 {
		t.Errorf("distance to self = %v", d)
	}
}

func TestProjectorRoundTrip(t *testing.T) {
	pts := []LatLon{{45.42, -75.70}, {45.35, -75.59}, {45.54, -75.68}}
	pr := NewProjector(pts)
	for _, p := range pts {
		back := pr.Unproject(pr.Project(p))
		if math.Abs(back.Lat-p.Lat) > 1e-9 || math.Abs(back.Lon-p.Lon) > 1e-9 {
			t.Errorf("round trip %v -> %v", p, back)
		}
	}
	// Planar distance should track haversine closely at city scale.
	a, b := pr.Project(pts[0]), pr.Project(pts[1])
	if rel := math.Abs(a.Dist(b)-Haversine(pts[0], pts[1])) / Haversine(pts[0], pts[1]); rel > 0.005 {
		t.Errorf("projection distortion %.4f too large", rel)
	}
}

func TestConvexHullAndArea(t *testing.T) {
	pts := []XY{{0, 0}, {4, 0}, {4, 4}, {0, 4}, {2, 2}, {1, 3}} // interior points must vanish
	hull := ConvexHull(pts)
	if len(hull) != 4 {
		t.Fatalf("hull has %d vertices, want 4: %v", len(hull), hull)
	}
	if a := Area(hull); math.Abs(a-16) > 1e-9 {
		t.Errorf("area = %v, want 16", a)
	}
}

func TestConvexIntersectionArea(t *testing.T) {
	sq := func(x, y, s float64) []XY { return []XY{{x, y}, {x + s, y}, {x + s, y + s}, {x, y + s}} }
	tests := []struct {
		name string
		a, b []XY
		want float64
	}{
		{"partial overlap", sq(0, 0, 4), sq(2, 2, 4), 4},
		{"contained", sq(0, 0, 10), sq(2, 2, 2), 4},
		{"disjoint", sq(0, 0, 1), sq(5, 5, 1), 0},
		{"identical", sq(0, 0, 3), sq(0, 0, 3), 9},
	}
	for _, tc := range tests {
		if got := ConvexIntersectionArea(tc.a, tc.b); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestBufferedHullHandlesSinglePoint(t *testing.T) {
	h := BufferedHull([]XY{{10, 10}}, 100)
	if len(h) < 3 {
		t.Fatalf("a single point must still produce a polygon, got %d vertices", len(h))
	}
	// A 12-gon inscribed in radius 100 has area ~ 3*r^2.
	if a := Area(h); a < 2.9*100*100 || a > 3.2*100*100 {
		t.Errorf("unexpected disc area %v", a)
	}
}
