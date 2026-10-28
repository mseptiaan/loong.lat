// Package roads measures how far apart places are by road.
//
// The optimizer needs a full table of road distances between the places of a
// quote. Where they come from is a detail behind Provider: Mapbox today, a
// self-hosted router later, and a straight-line estimate when neither answers.
package roads

import (
	"context"
	"math"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/routeplan"
)

// Matrix is the road distance in meters from place i to place j at DistM[i*N+j].
type Matrix struct {
	N     int
	DistM []int32
	// Source says where the numbers came from ("mapbox", "estimate"), for reporting.
	Source string
	// Estimated counts the pairs that had to be estimated because the provider could not route them.
	Estimated int
}

// Provider measures road distances.
type Provider interface {
	Matrix(ctx context.Context, places []contracts.LatLng) (Matrix, error)
}

// Router draws the road path through places, in order, as [lng, lat] pairs. It
// only decorates a route for the map; no number of a quote depends on it.
type Router interface {
	Geometry(ctx context.Context, waypoints []contracts.LatLng) ([]contracts.LngLat, error)
}

// EstimateMeters is the estimated road distance between two places.
func EstimateMeters(a, b contracts.LatLng) int32 {
	return int32(math.Round(routeplan.RoadKm(a, b) * 1000))
}

// Estimate answers every question from straight-line distances. It needs no
// network, so it is the development provider and the last resort.
type Estimate struct{}

// Matrix implements Provider.
func (Estimate) Matrix(_ context.Context, places []contracts.LatLng) (Matrix, error) {
	n := len(places)
	m := Matrix{N: n, DistM: make([]int32, n*n), Source: "estimate"}
	for i := range n {
		for j := range n {
			if i != j {
				m.DistM[i*n+j] = EstimateMeters(places[i], places[j])
			}
		}
	}
	m.Estimated = n * (n - 1)
	return m, nil
}
