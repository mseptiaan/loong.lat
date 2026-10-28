// Package geo contains the small amount of geometry the optimizer needs:
// distances, a local planar projection, convex hulls and polygon overlap.
package geo

import (
	"cmp"
	"math"
	"slices"
)

const earthRadiusM = 6371008.8

// LatLon is a WGS84 coordinate in degrees.
type LatLon struct {
	Lat, Lon float64
}

// XY is a planar point in meters.
type XY struct {
	X, Y float64
}

// Haversine returns the great-circle distance in meters.
func Haversine(a, b LatLon) float64 {
	const rad = math.Pi / 180
	dLat := (b.Lat - a.Lat) * rad
	dLon := (b.Lon - a.Lon) * rad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(a.Lat*rad)*math.Cos(b.Lat*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusM * math.Asin(math.Min(1, math.Sqrt(h)))
}

// Projector maps coordinates to a local equirectangular plane (meters) around
// a reference latitude. It is accurate enough for city and regional scale
// clustering, where distortion stays well below one percent.
type Projector struct {
	lat0, lon0, cosLat float64
}

// NewProjector centers a projector on the centroid of pts.
func NewProjector(pts []LatLon) Projector {
	var lat, lon float64
	for _, p := range pts {
		lat += p.Lat
		lon += p.Lon
	}
	if n := float64(len(pts)); n > 0 {
		lat, lon = lat/n, lon/n
	}
	return Projector{lat0: lat, lon0: lon, cosLat: math.Cos(lat * math.Pi / 180)}
}

// Project converts a coordinate to planar meters.
func (p Projector) Project(c LatLon) XY {
	const rad = math.Pi / 180
	return XY{
		X: (c.Lon - p.lon0) * rad * earthRadiusM * p.cosLat,
		Y: (c.Lat - p.lat0) * rad * earthRadiusM,
	}
}

// Unproject converts planar meters back to a coordinate.
func (p Projector) Unproject(q XY) LatLon {
	const deg = 180 / math.Pi
	return LatLon{
		Lat: p.lat0 + q.Y/earthRadiusM*deg,
		Lon: p.lon0 + q.X/(earthRadiusM*p.cosLat)*deg,
	}
}

// Dist is the Euclidean distance between two planar points.
func (a XY) Dist(b XY) float64 { return math.Hypot(a.X-b.X, a.Y-b.Y) }

// Centroid is the arithmetic mean of pts; it returns the zero value for none.
func Centroid(pts []XY) XY {
	if len(pts) == 0 {
		return XY{}
	}
	var c XY
	for _, p := range pts {
		c.X += p.X
		c.Y += p.Y
	}
	n := float64(len(pts))
	return XY{c.X / n, c.Y / n}
}

// ConvexHull returns the hull in counter-clockwise order (Andrew's monotone
// chain). Fewer than three distinct points are returned as they are.
func ConvexHull(pts []XY) []XY {
	p := slices.Clone(pts)
	slices.SortFunc(p, func(a, b XY) int {
		if c := cmp.Compare(a.X, b.X); c != 0 {
			return c
		}
		return cmp.Compare(a.Y, b.Y)
	})
	p = slices.CompactFunc(p, func(a, b XY) bool { return a == b })
	if len(p) < 3 {
		return p
	}
	cross := func(o, a, b XY) float64 { return (a.X-o.X)*(b.Y-o.Y) - (a.Y-o.Y)*(b.X-o.X) }
	hull := make([]XY, 0, 2*len(p))
	for _, q := range p {
		for len(hull) >= 2 && cross(hull[len(hull)-2], hull[len(hull)-1], q) <= 0 {
			hull = hull[:len(hull)-1]
		}
		hull = append(hull, q)
	}
	lower := len(hull) + 1
	for i := len(p) - 2; i >= 0; i-- {
		q := p[i]
		for len(hull) >= lower && cross(hull[len(hull)-2], hull[len(hull)-1], q) <= 0 {
			hull = hull[:len(hull)-1]
		}
		hull = append(hull, q)
	}
	return hull[:len(hull)-1]
}

// BufferedHull returns a convex polygon that encloses every point with a
// margin of radius meters. Because each point is replaced by a small ring
// before hulling, one or two points still yield a proper polygon (a disc or a
// capsule), which keeps single-stop clusters visible on a map.
func BufferedHull(pts []XY, radius float64) []XY {
	const segments = 12
	ring := make([]XY, 0, len(pts)*segments)
	for _, p := range pts {
		for i := range segments {
			a := 2 * math.Pi * float64(i) / segments
			ring = append(ring, XY{p.X + radius*math.Cos(a), p.Y + radius*math.Sin(a)})
		}
	}
	return ConvexHull(ring)
}

// Area is the absolute area of a simple polygon.
func Area(poly []XY) float64 {
	var s float64
	for i := range poly {
		j := (i + 1) % len(poly)
		s += poly[i].X*poly[j].Y - poly[j].X*poly[i].Y
	}
	return math.Abs(s) / 2
}

// ConvexIntersectionArea returns the overlap area of two convex polygons given
// in counter-clockwise order (Sutherland-Hodgman clipping).
func ConvexIntersectionArea(a, b []XY) float64 {
	if len(a) < 3 || len(b) < 3 {
		return 0
	}
	out := slices.Clone(a)
	for i := range b {
		if len(out) == 0 {
			return 0
		}
		c1, c2 := b[i], b[(i+1)%len(b)]
		inside := func(p XY) bool {
			return (c2.X-c1.X)*(p.Y-c1.Y)-(c2.Y-c1.Y)*(p.X-c1.X) >= 0
		}
		intersect := func(p, q XY) XY {
			dx1, dy1 := q.X-p.X, q.Y-p.Y
			dx2, dy2 := c2.X-c1.X, c2.Y-c1.Y
			den := dx1*dy2 - dy1*dx2
			if den == 0 {
				return p
			}
			t := ((c1.X-p.X)*dy2 - (c1.Y-p.Y)*dx2) / den
			return XY{p.X + t*dx1, p.Y + t*dy1}
		}
		in := out
		out = nil
		for j := range in {
			cur, prev := in[j], in[(j+len(in)-1)%len(in)]
			switch {
			case inside(cur) && inside(prev):
				out = append(out, cur)
			case inside(cur):
				out = append(out, intersect(prev, cur), cur)
			case inside(prev):
				out = append(out, intersect(prev, cur))
			}
		}
	}
	if len(out) < 3 {
		return 0
	}
	return Area(out)
}
