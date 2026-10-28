// Package quote turns a customer's shipments into the options the wizard shows.
//
// It is the optimizer's face to the rest of the platform: it checks the input,
// measures the roads, asks a runner to search for routes, and then rebuilds each
// route with times, prices and plain-language reasons in the shape of
// contracts.OptionSet. The search itself (internal/solve) only ever sees
// coordinates, weights and distances.
package quote

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

// Error codes, as the frontend's ApiError knows them.
const (
	CodeInvalid = "tidak_valid"
	CodeService = "layanan"
)

// Error is a quote that cannot be computed. Msg is language-neutral; Field names
// the input at fault, as a path such as "shipments.1.pickup".
type Error struct {
	Code  string
	Field string
	Msg   contracts.Msg
}

func (e *Error) Error() string { return e.Code + ": " + e.Msg.Key }

func invalid(field, key string, params map[string]any) *Error {
	return &Error{Code: CodeInvalid, Field: field, Msg: contracts.Msg{Key: key, Params: params}}
}

func msg(key string, params map[string]any) contracts.Msg {
	return contracts.Msg{Key: key, Params: params}
}

// Request is one quote to compute.
type Request struct {
	PlanID    string
	Shipments []contracts.Shipment
	Schedule  contracts.Schedule
	Priority  contracts.Priority
	// Vehicles is the fleet as configured by the admin; only active vehicles count.
	Vehicles []contracts.Vehicle
	Now      time.Time
	// Effort is how hard to search; empty means fast.
	Effort contracts.Effort
	// AIReview lets the model review a reviewed quote, when the planner has one; otherwise the rules review.
	AIReview bool
}

// Aggregate is the load of some items: totals the vehicle must carry.
type Aggregate struct {
	WeightKg  float64
	VolumeM3  float64
	MaxDimCm  float64 // the longest side of any item
	ItemCount int
}

func aggregateItems(items []contracts.Item) Aggregate {
	var a Aggregate
	for _, it := range items {
		q := float64(it.Qty)
		a.WeightKg += q * it.WeightKg
		a.VolumeM3 += q * it.LengthCm * it.WidthCm * it.HeightCm / 1e6
		a.MaxDimCm = max(a.MaxDimCm, it.LengthCm, it.WidthCm, it.HeightCm)
		a.ItemCount += it.Qty
	}
	return a
}

func aggregateShipments(ss []contracts.Shipment) Aggregate {
	var items []contracts.Item
	for _, s := range ss {
		items = append(items, s.Items...)
	}
	return aggregateItems(items)
}

func fits(a Aggregate, v contracts.Vehicle) bool {
	return a.WeightKg <= v.MaxWeightKg && a.VolumeM3 <= v.MaxVolumeM3 && a.MaxDimCm <= v.MaxDimensionCm
}

// fleetProfile picks one representative vehicle per type: the active one with the
// lowest base fare. It sets the tariff and the capacity quotes are made against.
type fleetProfile map[contracts.VehicleType]contracts.Vehicle

func profileOf(vehicles []contracts.Vehicle) fleetProfile {
	p := fleetProfile{}
	for _, v := range vehicles {
		if v.Status != contracts.VehicleStatusActive {
			continue
		}
		if cur, ok := p[v.Type]; !ok || v.Fare.Base < cur.Fare.Base {
			p[v.Type] = v
		}
	}
	return p
}

// smallestFit is the smallest vehicle type that takes the load, if any.
func (p fleetProfile) smallestFit(a Aggregate) (contracts.VehicleType, bool) {
	for _, t := range []contracts.VehicleType{contracts.VehicleTypeMotor, contracts.VehicleTypeBlindvan} {
		if v, ok := p[t]; ok && fits(a, v) {
			return t, true
		}
	}
	return "", false
}

// itemName is how an item is named in an error: catalog items follow the
// reader's language, typed names are shown as they are.
func itemName(it contracts.Item) any {
	switch {
	case it.CatalogKey != "":
		return contracts.Msg{Key: "cat." + it.CatalogKey}
	case strings.TrimSpace(it.Name) != "":
		return it.Name
	}
	return contracts.Msg{Key: "srv.engine.itemFallback"}
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// Validate reports the first reason the shipments cannot be quoted, or nil.
func Validate(shipments []contracts.Shipment) *Error {
	if len(shipments) == 0 {
		return invalid("shipments", "srv.engine.needShipment", nil)
	}
	for i, s := range shipments {
		n := i + 1
		for _, w := range []struct {
			label string
			wp    contracts.Waypoint
			key   string
		}{
			{"pickup", s.Pickup, "srv.engine.pickupMissing"},
			{"tujuan", s.Dropoff, "srv.engine.dropoffMissing"},
		} {
			if w.wp.Name == "" || !finite(w.wp.Position.Lat) || !finite(w.wp.Position.Lng) {
				return invalid(fmt.Sprintf("shipments.%d.%s", i, w.label), w.key, map[string]any{"n": n})
			}
		}
		if len(s.Items) == 0 {
			return invalid(fmt.Sprintf("shipments.%d.items", i), "srv.engine.itemsMissing", map[string]any{"n": n})
		}
		for _, it := range s.Items {
			if strings.TrimSpace(it.Name) == "" || it.Qty < 1 || !(it.WeightKg > 0) || !(it.LengthCm > 0) || !(it.WidthCm > 0) || !(it.HeightCm > 0) {
				return invalid(fmt.Sprintf("shipments.%d.items", i), "srv.engine.itemIncomplete", map[string]any{"n": n, "item": itemName(it)})
			}
		}
	}
	return nil
}

// inputHash identifies everything a quote depends on, so that an old quote is
// recognised as stale when anything it was computed from has changed.
func inputHash(r *Request, fleet fleetProfile, engine string) string {
	type item struct {
		Qty         int
		Kg, L, W, H float64
		Fragile     bool
	}
	type ship struct {
		P, D     [2]string
		Items    []item
		Deadline *string
	}
	core := struct {
		S      []ship
		Sched  []string
		Preset string
		Fleet  map[string]contracts.FareConfig
		Engine string
		// Effort is left out for a fast quote, so that the hash of a fast quote is what it always was.
		Effort string `json:",omitempty"`
	}{Preset: string(r.Priority.Preset), Fleet: map[string]contracts.FareConfig{}, Engine: engine}
	if r.Effort != "" && r.Effort != contracts.EffortFast {
		core.Effort = string(r.Effort)
	}
	for _, s := range r.Shipments {
		sh := ship{
			P: [2]string{fmt.Sprintf("%.5f", s.Pickup.Position.Lat), fmt.Sprintf("%.5f", s.Pickup.Position.Lng)},
			D: [2]string{fmt.Sprintf("%.5f", s.Dropoff.Position.Lat), fmt.Sprintf("%.5f", s.Dropoff.Position.Lng)},
		}
		for _, it := range s.Items {
			sh.Items = append(sh.Items, item{it.Qty, it.WeightKg, it.LengthCm, it.WidthCm, it.HeightCm, it.Fragile})
		}
		if !s.Deadline.IsZero() {
			d := s.Deadline.UTC().Format(time.RFC3339)
			sh.Deadline = &d
		}
		core.S = append(core.S, sh)
	}
	core.Sched = []string{string(r.Schedule.Mode)}
	if w := r.Schedule.Window; w != nil {
		core.Sched = append(core.Sched, w.From.UTC().Format(time.RFC3339), w.To.UTC().Format(time.RFC3339))
	}
	for t, v := range fleet {
		core.Fleet[string(t)] = v.Fare
	}
	data, _ := json.Marshal(core, json.Deterministic(true))
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

func round1(n float64) float64 { return math.Round(n*10) / 10 }
