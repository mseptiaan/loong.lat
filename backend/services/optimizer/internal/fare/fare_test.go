package fare

import (
	"testing"

	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
)

var motor = job.Fare{Base: 6000, PerKm: 1800, PerMin: 150, PerExtraStop: 2500, SurchargeAboveKg: 10, SurchargePerKg: 800}

func TestComputeAddsUpAndRounds(t *testing.T) {
	lines, total := Compute(motor, Load{Km: 4.2, Min: 17.5, Stops: 2, WeightKg: 3})
	// 6000 + 4.2*1800 + 17.5*150 = 6000 + 7560 + 2625 = 16185 -> 16000 with a -185 rounding line.
	if total != 16000 {
		t.Fatalf("total = %d, want 16000", total)
	}
	var sum int64
	for _, l := range lines {
		sum += l.AmountRp
	}
	if sum != total {
		t.Errorf("lines sum to %d, total is %d", sum, total)
	}
	if last := lines[len(lines)-1]; last.Key != KeyRounding || last.AmountRp != -185 {
		t.Errorf("last line = %+v, want the rounding line of -185", last)
	}
}

func TestComputeAddsExtraStopsAndWeightSurcharge(t *testing.T) {
	lines, total := Compute(motor, Load{Km: 10, Min: 30, Stops: 6, WeightKg: 12})
	// 6000 + 18000 + 4500 + 4*2500 + 2*800 = 40100 -> 40000.
	if total != 40000 {
		t.Fatalf("total = %d, want 40000", total)
	}
	keys := map[string]int64{}
	for _, l := range lines {
		keys[l.Key] = l.AmountRp
	}
	if keys[KeyExtraStops] != 10000 || keys[KeyWeight] != 1600 {
		t.Errorf("extra stops = %d, weight = %d, want 10000 and 1600", keys[KeyExtraStops], keys[KeyWeight])
	}
}

func TestComputeHasAMinimum(t *testing.T) {
	if _, total := Compute(job.Fare{}, Load{Stops: 2}); total != Step {
		t.Errorf("an empty tariff must still cost %d, got %d", Step, total)
	}
}

// The engine prices a route as fixed + per km + per hour + per order; for any
// number of orders that must equal the tariff before rounding.
func TestEngineCostModelReproducesTheTariff(t *testing.T) {
	fixed, perKm, perHour, perOrder := Cost(motor)
	for orders := 1; orders <= 6; orders++ {
		km, min := 7.5, 42.0
		want := Compute // keep the comparison honest: same inputs, no weight surcharge
		lines, _ := want(motor, Load{Km: km, Min: min, Stops: 2 * orders})
		var raw float64
		for _, l := range lines {
			if l.Key != KeyRounding {
				raw += float64(l.AmountRp)
			}
		}
		engine := fixed + perKm*km + perHour*(min/60) + perOrder*float64(orders)
		if diff := engine - raw; diff < -2 || diff > 2 { // each line rounds to a whole rupiah
			t.Errorf("%d orders: engine cost %.1f, tariff %.1f", orders, engine, raw)
		}
	}
}
