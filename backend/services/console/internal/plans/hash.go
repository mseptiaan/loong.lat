package plans

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"math"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

// inputHash identifies what decides a draft's options: where each shipment goes, what it weighs and when it
// is due, and the schedule and preset. Names, contacts, notes and the wizard step are left out, so editing them
// does not throw away the options. It is the console's own hash, independent of the one the optimizer puts in
// the option set.
func inputHash(p contracts.Plan) string {
	type item struct {
		Qty                                   int
		WeightKg, LengthCm, WidthCm, HeightCm float64
		Fragile                               bool
	}
	type shipment struct {
		Pickup, Dropoff [2]float64
		Items           []item
		Deadline        contracts.Time
	}
	type input struct {
		Shipments []shipment
		Mode      contracts.ScheduleMode
		From, To  contracts.Time
		Preset    contracts.Preset
	}
	// Coordinates to about a metre: the map pin's jitter must not count as a change.
	round5 := func(x float64) float64 { return math.Round(x*1e5) / 1e5 }
	point := func(w contracts.Waypoint) [2]float64 {
		return [2]float64{round5(w.Position.Lat), round5(w.Position.Lng)}
	}

	in := input{Mode: p.Schedule.Mode, Preset: p.Priority.Preset}
	if w := p.Schedule.Window; w != nil {
		in.From, in.To = w.From, w.To
	}
	for _, s := range p.Shipments {
		sh := shipment{Pickup: point(s.Pickup), Dropoff: point(s.Dropoff), Deadline: s.Deadline}
		for _, it := range s.Items {
			sh.Items = append(sh.Items, item{it.Qty, it.WeightKg, it.LengthCm, it.WidthCm, it.HeightCm, it.Fragile})
		}
		in.Shipments = append(in.Shipments, sh)
	}
	b, err := json.Marshal(in, json.Deterministic(true))
	if err != nil {
		panic("plans: hash input: " + err.Error()) // plain numbers and strings always encode
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
