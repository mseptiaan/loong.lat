// Package fare prices a route the way the frontend's engine did: base fare +
// distance + time + extra stops + a weight surcharge, rounded to Rp 500.
//
// The solver minimizes the same tariff before rounding (see solve.cost), so the
// numbers a customer sees are the numbers the optimizer worked to reduce.
package fare

import (
	"math"

	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
)

// Load is what a route is billed on. The way to the first stop is not billed,
// so Km and Min start at the first stop.
type Load struct {
	Km       float64
	Min      float64
	Stops    int
	WeightKg float64
}

// Line message keys; the frontend's catalog turns them into text.
const (
	KeyBase       = "srv.engine.fare.base"
	KeyDistance   = "srv.engine.fare.distance"
	KeyTime       = "srv.engine.fare.time"
	KeyExtraStops = "srv.engine.fare.extraStops"
	KeyWeight     = "srv.engine.fare.weight"
	KeyRounding   = "srv.engine.fare.rounding"
)

// Line is one row of the price breakdown.
type Line struct {
	Key      string
	Params   map[string]float64
	AmountRp int64
}

// Step is the rounding unit of a total, in rupiah.
const Step = 500

func roundTo(n float64, step float64) float64 { return math.Round(n/step) * step }

// Compute returns the breakdown and the total of one route. The lines always sum to the total.
func Compute(f job.Fare, l Load) (lines []Line, total int64) {
	lines = []Line{
		{KeyBase, nil, int64(math.Round(f.Base))},
		{KeyDistance, map[string]float64{"km": l.Km}, int64(math.Round(l.Km * f.PerKm))},
		{KeyTime, map[string]float64{"min": l.Min}, int64(math.Round(l.Min * f.PerMin))},
	}
	if extra := max(0, l.Stops-2); extra > 0 {
		lines = append(lines, Line{KeyExtraStops, map[string]float64{"count": float64(extra)}, int64(extra) * int64(math.Round(f.PerExtraStop))})
	}
	if l.WeightKg > f.SurchargeAboveKg && f.SurchargePerKg > 0 {
		lines = append(lines, Line{
			KeyWeight, map[string]float64{"kg": l.WeightKg, "over": f.SurchargeAboveKg},
			int64(math.Round((l.WeightKg - f.SurchargeAboveKg) * f.SurchargePerKg)),
		})
	}
	var raw int64
	for _, ln := range lines {
		raw += ln.AmountRp
	}
	total = max(int64(roundTo(float64(raw), Step)), Step)
	if rounding := total - raw; rounding != 0 {
		lines = append(lines, Line{KeyRounding, nil, rounding})
	}
	return lines, total
}

// Cost converts the tariff into the engine's cost model: a fixed part, a price
// per kilometre, per hour and per order, and a weight surcharge. A route with k
// orders has 2k stops, so (2k-2) extra stops cost 2*perExtraStop per order after
// the first, which the fixed part and the per-order part reproduce exactly.
func Cost(f job.Fare) (fixed, perKm, perHour, perOrder float64) {
	perOrder = 2 * f.PerExtraStop
	return f.Base - perOrder, f.PerKm, f.PerMin * 60, perOrder
}
