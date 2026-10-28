package quote

import (
	"context"
	"encoding/json/v2"
	"errors"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/roads"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/runner"
)

var (
	// 10:00 WIB on a Wednesday: free-flowing traffic.
	now = time.Date(2026, 10, 21, 2, 50, 0, 0, time.UTC)

	motorVehicle = contracts.Vehicle{
		ID: "v-motor", Type: contracts.VehicleTypeMotor, Status: contracts.VehicleStatusActive,
		MaxWeightKg: 20, MaxVolumeM3: 0.12, MaxDimensionCm: 55,
		Fare: contracts.FareConfig{Base: 6000, PerKm: 1800, PerMin: 150, PerExtraStop: 2500, SurchargePerKg: 800, SurchargeAboveKg: 10},
	}
	vanVehicle = contracts.Vehicle{
		ID: "v-van", Type: contracts.VehicleTypeBlindvan, Status: contracts.VehicleStatusActive,
		MaxWeightKg: 500, MaxVolumeM3: 3.2, MaxDimensionCm: 190,
		Fare: contracts.FareConfig{Base: 45000, PerKm: 4500, PerMin: 500, PerExtraStop: 8000, SurchargePerKg: 150, SurchargeAboveKg: 300},
	}
)

func place(name string, lat, lng float64) contracts.Waypoint {
	return contracts.Waypoint{
		PlaceRef:    contracts.PlaceRef{Name: name, Address: name + ", Jakarta", Position: contracts.LatLng{Lat: lat, Lng: lng}},
		ContactName: "Kontak", ContactPhone: "0812",
	}
}

func parcel(id string, from, to contracts.Waypoint, kg float64) contracts.Shipment {
	return contracts.Shipment{ID: id, Pickup: from, Dropoff: to, Items: []contracts.Item{
		{ID: id + "-i", Name: "Paket", Qty: 1, WeightKg: kg, LengthCm: 30, WidthCm: 20, HeightCm: 10, Source: contracts.ItemSourceManual},
	}}
}

var (
	kemang   = place("Kemang Village", -6.2608, 106.8133)
	senayan  = place("Senayan City", -6.2275, 106.8029)
	blokM    = place("Blok M Square", -6.2441, 106.7993)
	pondok   = place("Pondok Indah Mall", -6.2653, 106.7836)
	senopati = place("Senopati", -6.2297, 106.8137)
	monas    = place("Monas", -6.2088, 106.8456)
)

func request(preset contracts.Preset, ships ...contracts.Shipment) Request {
	return Request{
		PlanID: "PL-2041", Shipments: ships, Schedule: contracts.Schedule{Mode: contracts.ScheduleModeNow},
		Priority: contracts.Priority{Preset: preset}, Vehicles: []contracts.Vehicle{motorVehicle, vanVehicle}, Now: now,
	}
}

func planner() *Planner { return &Planner{Roads: roads.Estimate{}, Runner: runner.Local{}} }

func compute(t *testing.T, r Request) *Result {
	t.Helper()
	res, err := planner().Compute(context.Background(), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// checkOptionSet verifies the rules the wizard relies on, whichever engine produced the set.
func checkOptionSet(t *testing.T, set contracts.OptionSet, r Request) {
	t.Helper()
	if len(set.Options) == 0 {
		t.Fatal("no options")
	}
	recommended := 0
	for _, o := range set.Options {
		if o.Recommended {
			recommended++
		}
		var cost int64
		picked := map[string]bool{}
		for _, rt := range o.Routes {
			var sum int64
			for _, l := range rt.Fare {
				sum += l.AmountRp
			}
			if sum != rt.CostRp {
				t.Errorf("%s %s: fare lines sum to %d, cost is %d", o.ID, rt.ID, sum, rt.CostRp)
			}
			cost += rt.CostRp
			var prev time.Time
			for i, st := range rt.Stops {
				if st.Seq != i+1 {
					t.Errorf("%s: stop %d has seq %d", rt.ID, i, st.Seq)
				}
				if st.ArriveAt.Before(prev) {
					t.Errorf("%s: arrival times go backwards at stop %d", rt.ID, st.Seq)
				}
				prev = st.ArriveAt.Time
				switch st.Kind {
				case contracts.StopKindPickup:
					picked[st.ShipmentID] = true
				case contracts.StopKindDropoff:
					if !picked[st.ShipmentID] {
						t.Errorf("%s: %s is dropped before its pickup", rt.ID, st.ShipmentID)
					}
				}
			}
		}
		if len(picked) != len(r.Shipments) {
			t.Errorf("%s serves %d of %d shipments", o.ID, len(picked), len(r.Shipments))
		}
		if o.Totals.CostRp != cost {
			t.Errorf("%s: totals say Rp %d, routes cost Rp %d", o.ID, o.Totals.CostRp, cost)
		}
		if want := set.Baseline.CostRp - o.Totals.CostRp; o.Savings.CostRp != want {
			t.Errorf("%s: savings %d, want %d", o.ID, o.Savings.CostRp, want)
		}
		if len(o.Reasons) == 0 {
			t.Errorf("%s has no reasons", o.ID)
		}
	}
	if recommended != 1 {
		t.Errorf("%d recommended options, want exactly one", recommended)
	}
	if !set.Options[0].Recommended {
		t.Error("the recommended option must come first")
	}
}

func TestOneShipmentGivesOneOptionAndNoSavings(t *testing.T) {
	r := request(contracts.PresetCheapest, parcel("s1", kemang, senayan, 3))
	res := compute(t, r)
	checkOptionSet(t, res.Set, r)
	if len(res.Set.Options) != 1 {
		t.Fatalf("%d options, a single shipment has one plan", len(res.Set.Options))
	}
	o := res.Set.Options[0]
	if o.Savings.CostRp != 0 || o.Label.Key != "srv.engine.label.best" || o.ID != "PL-2041:termurah" {
		t.Errorf("unexpected option %+v", o)
	}
	if len(o.Routes) != 1 || o.Routes[0].VehicleType != contracts.VehicleTypeMotor || len(o.Routes[0].Stops) != 2 {
		t.Errorf("want one motorbike route of two stops, got %+v", o.Routes)
	}
	if !res.Proven {
		t.Error("one shipment is solved exhaustively and must be proven optimal")
	}
}

func TestSeveralShipmentsAreOptimisedAgainstSendingThemSeparately(t *testing.T) {
	r := request(contracts.PresetBalanced,
		parcel("s1", kemang, senayan, 3), parcel("s2", kemang, blokM, 4),
		parcel("s3", senopati, monas, 2), parcel("s4", senayan, pondok, 5))
	res := compute(t, r)
	checkOptionSet(t, res.Set, r)

	var cheapest, wanted *contracts.PlanOption
	for i := range res.Set.Options {
		o := &res.Set.Options[i]
		if slices.Contains([]string{"srv.engine.label.best"}, o.Label.Key) || o.Preset == contracts.PresetCheapest {
			cheapest = o
		}
		if o.Recommended {
			wanted = o
		}
	}
	if wanted == nil || wanted.Preset != contracts.PresetBalanced {
		t.Errorf("the balanced option must be the recommended one, got %+v", wanted)
	}
	if cheapest != nil && cheapest.Savings.CostRp <= 0 {
		t.Errorf("batching four shipments must beat sending them apart, savings = %d", cheapest.Savings.CostRp)
	}
}

func TestEveryPresetSpeaksForItsOwnObjective(t *testing.T) {
	r := request(contracts.PresetCheapest,
		parcel("s1", kemang, senayan, 3), parcel("s2", kemang, blokM, 4),
		parcel("s3", senopati, monas, 2), parcel("s4", senayan, pondok, 5))
	res := compute(t, r)
	byPreset := map[contracts.Preset]contracts.Totals{}
	for _, o := range res.Set.Options {
		// An option merges the presets that agree; each of them is a preset of the plan it shows.
		byPreset[o.Preset] = o.Totals
	}
	if cheap, ok := byPreset[contracts.PresetCheapest]; ok {
		for p, tot := range byPreset {
			if tot.CostRp < cheap.CostRp {
				t.Errorf("%s costs Rp %d, less than the cheapest option's Rp %d", p, tot.CostRp, cheap.CostRp)
			}
		}
	}
}

func TestHeavyAndBulkyShipmentsRideTheVanAndSayWhy(t *testing.T) {
	heavy := parcel("s1", kemang, senayan, 80)
	r := request(contracts.PresetCheapest, heavy)
	res := compute(t, r)
	checkOptionSet(t, res.Set, r)
	o := res.Set.Options[0]
	if o.Routes[0].VehicleType != contracts.VehicleTypeBlindvan {
		t.Fatalf("80 kg does not fit a motorbike, got %s", o.Routes[0].VehicleType)
	}
	if !slices.ContainsFunc(o.Reasons, func(m contracts.Msg) bool { return m.Key == "srv.engine.reason.vanWeight" }) {
		t.Errorf("the reasons must explain the van by weight: %+v", o.Reasons)
	}
	// Over the surcharge threshold? 80 kg is below the van's 300 kg, so no weight line.
	for _, l := range o.Routes[0].Fare {
		if l.Label.Key == "srv.engine.fare.weight" {
			t.Errorf("unexpected weight surcharge %+v", l)
		}
	}
}

func TestAMotorbikeParcelOverTheThresholdPaysTheWeightSurcharge(t *testing.T) {
	r := request(contracts.PresetCheapest, parcel("s1", kemang, senayan, 12))
	o := compute(t, r).Set.Options[0]
	var found bool
	for _, l := range o.Routes[0].Fare {
		if l.Label.Key == "srv.engine.fare.weight" {
			found = true
			if l.AmountRp != 1600 { // (12-10) kg * Rp 800
				t.Errorf("surcharge = %d, want 1600", l.AmountRp)
			}
		}
	}
	if !found {
		t.Error("12 kg on a motorbike must carry the weight surcharge")
	}
}

func TestDeadlinesAreMetOrReportedLate(t *testing.T) {
	a, b := parcel("s1", kemang, senayan, 2), parcel("s2", senopati, monas, 2)
	b.Deadline = contracts.At(now.Add(40 * time.Minute))
	met := compute(t, request(contracts.PresetCheapest, a, b))
	if !hasReason(met.Set, "srv.engine.reason.deadlineMet") {
		t.Errorf("a reachable deadline must be reported as met: %+v", met.Set.Options[0].Reasons)
	}

	b.Deadline = contracts.At(now.Add(8 * time.Minute)) // before the courier can even set off
	late := compute(t, request(contracts.PresetCheapest, a, b))
	if !hasReason(late.Set, "srv.engine.reason.deadlineLate") {
		t.Errorf("an impossible deadline must be reported as late: %+v", late.Set.Options[0].Reasons)
	}
	checkOptionSet(t, late.Set, request(contracts.PresetCheapest, a, b)) // late, but still served
}

func TestADeadlineOnAnotherDayIsShownWithItsDate(t *testing.T) {
	a, b := parcel("s1", kemang, senayan, 2), parcel("s2", senopati, monas, 2)
	b.Deadline = contracts.At(now.Add(26 * time.Hour)) // tomorrow: "arrives 14:30 (deadline 16:30)" would read as today
	res := compute(t, request(contracts.PresetCheapest, a, b))
	if !hasNestedKey(res.Set, "srv.engine.deadline.rowDated") || hasNestedKey(res.Set, "srv.engine.deadline.row") {
		t.Errorf("a deadline on another day must name its date: %+v", res.Set.Options[0].Reasons)
	}

	b.Deadline = contracts.At(now.Add(40 * time.Minute)) // same day: the time alone is clear
	res = compute(t, request(contracts.PresetCheapest, a, b))
	if !hasNestedKey(res.Set, "srv.engine.deadline.row") || hasNestedKey(res.Set, "srv.engine.deadline.rowDated") {
		t.Errorf("a deadline today needs no date: %+v", res.Set.Options[0].Reasons)
	}
}

// hasNestedKey looks for a message key anywhere in the options' reasons, parameters included.
func hasNestedKey(set contracts.OptionSet, key string) bool {
	var in func(m contracts.Msg) bool
	in = func(m contracts.Msg) bool {
		if m.Key == key {
			return true
		}
		for _, v := range m.Params {
			if sub, ok := v.(contracts.Msg); ok && in(sub) {
				return true
			}
		}
		return false
	}
	for _, o := range set.Options {
		if slices.ContainsFunc(o.Reasons, in) {
			return true
		}
	}
	return false
}

func hasReason(set contracts.OptionSet, key string) bool {
	for _, o := range set.Options {
		if slices.ContainsFunc(o.Reasons, func(m contracts.Msg) bool { return m.Key == key }) {
			return true
		}
	}
	return false
}

func TestRushHourSlowsTheClockAndSaysSo(t *testing.T) {
	r := request(contracts.PresetCheapest, parcel("s1", kemang, senayan, 3))
	free := compute(t, r).Set.Options[0].Routes[0].DurationMin

	r.Now = time.Date(2026, 10, 21, 0, 50, 0, 0, time.UTC) // 07:50 WIB, departing 07:56
	res := compute(t, r)
	rush := res.Set.Options[0]
	if rush.Routes[0].DurationMin <= free {
		t.Errorf("a morning-rush trip (%.1f min) must take longer than a midday one (%.1f min)", rush.Routes[0].DurationMin, free)
	}
	if !hasReason(res.Set, "srv.engine.reason.rush") {
		t.Error("the reasons must mention the rush hour")
	}
}

func TestSameRequestSameQuote(t *testing.T) {
	r := request(contracts.PresetFastest, parcel("s1", kemang, senayan, 3), parcel("s2", blokM, pondok, 4), parcel("s3", senopati, monas, 2))
	// Message params are maps, so encoding must be deterministic to compare bytes.
	enc := func(set contracts.OptionSet) string {
		data, err := json.Marshal(set, json.Deterministic(true))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	a, b := enc(compute(t, r).Set), enc(compute(t, r).Set)
	if a != b {
		t.Error("the same request produced different quotes")
	}
	r.Shipments[0].Items[0].WeightKg = 9
	if c := enc(compute(t, r).Set); c == a {
		t.Error("a changed parcel must change the quote")
	}
	h1, h2 := compute(t, request(contracts.PresetCheapest, parcel("s1", kemang, senayan, 3))).Set.InputHash,
		compute(t, request(contracts.PresetShortest, parcel("s1", kemang, senayan, 3))).Set.InputHash
	if h1 == h2 {
		t.Error("the preset is part of what a quote depends on")
	}
}

func TestStagesArriveInOrder(t *testing.T) {
	var stages []Stage
	_, err := planner().Compute(context.Background(), request(contracts.PresetCheapest, parcel("s1", kemang, senayan, 3)), func(u Update) {
		if u.Msg != nil {
			t.Errorf("a fast quote reported a step inside a stage: %+v", u.Msg)
		}
		stages = append(stages, u.Stage)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(stages, []Stage{StageReadPoints, StageMeasure, StageCombine, StageCompare}) {
		t.Errorf("stages = %v", stages)
	}
}

func TestInvalidInputIsRefusedWithTheFieldAtFault(t *testing.T) {
	good := func() Request { return request(contracts.PresetCheapest, parcel("s1", kemang, senayan, 3)) }
	for name, tc := range map[string]struct {
		mutate func(*Request)
		field  string
		key    string
	}{
		"no shipments": {func(r *Request) { r.Shipments = nil }, "shipments", "srv.engine.needShipment"},
		"no pickup":    {func(r *Request) { r.Shipments[0].Pickup.Name = "" }, "shipments.0.pickup", "srv.engine.pickupMissing"},
		"no dropoff":   {func(r *Request) { r.Shipments[0].Dropoff.Position.Lat = math.NaN() }, "shipments.0.tujuan", "srv.engine.dropoffMissing"},
		"no items":     {func(r *Request) { r.Shipments[0].Items = nil }, "shipments.0.items", "srv.engine.itemsMissing"},
		"bad item":     {func(r *Request) { r.Shipments[0].Items[0].WeightKg = 0 }, "shipments.0.items", "srv.engine.itemIncomplete"},
		"too big":      {func(r *Request) { r.Shipments[0].Items[0].WeightKg = 900 }, "shipments", "srv.engine.tooBig"},
		"no vehicles":  {func(r *Request) { r.Vehicles = nil }, "", "srv.engine.noVehicles"},
		"all in service": {func(r *Request) {
			for i := range r.Vehicles {
				r.Vehicles[i].Status = contracts.VehicleStatusService
			}
		}, "", "srv.engine.noVehicles"},
	} {
		r := good()
		tc.mutate(&r)
		_, err := planner().Compute(context.Background(), r, nil)
		var qe *Error
		if !errors.As(err, &qe) {
			t.Errorf("%s: want a *quote.Error, got %v", name, err)
			continue
		}
		if qe.Field != tc.field || qe.Msg.Key != tc.key {
			t.Errorf("%s: field %q key %q, want %q %q", name, qe.Field, qe.Msg.Key, tc.field, tc.key)
		}
	}
}

func TestRunnerFailuresBecomeServiceErrors(t *testing.T) {
	down := runner.Func(func(context.Context, *job.Job, func(job.Progress)) (*job.Result, error) {
		return nil, errors.New("sandbox exploded")
	})
	p := &Planner{Roads: roads.Estimate{}, Runner: down}
	_, err := p.Compute(context.Background(), request(contracts.PresetCheapest, parcel("s1", kemang, senayan, 3)), nil)
	var qe *Error
	if !errors.As(err, &qe) || qe.Code != CodeService {
		t.Errorf("want a service error, got %v", err)
	}
}

type failingRoads struct{}

func (failingRoads) Matrix(context.Context, []contracts.LatLng) (roads.Matrix, error) {
	return roads.Matrix{}, errors.New("mapbox is down")
}

func TestAnUnreachableRoadServiceFallsBackToEstimates(t *testing.T) {
	p := &Planner{Roads: failingRoads{}, Runner: runner.Local{}}
	res, err := p.Compute(context.Background(), request(contracts.PresetCheapest, parcel("s1", kemang, senayan, 3)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.RoadSource != "estimate" {
		t.Errorf("road source = %q, want estimate", res.RoadSource)
	}
}

// pathRouter draws a straight line through the waypoints.
type pathRouter struct{ fail bool }

func (r pathRouter) Geometry(_ context.Context, way []contracts.LatLng) ([]contracts.LngLat, error) {
	if r.fail {
		return nil, errors.New("directions down")
	}
	out := make([]contracts.LngLat, len(way))
	for i, w := range way {
		out[i] = contracts.LngLat{w.Lng, w.Lat}
	}
	return out, nil
}

func TestRoutesAreDrawnFromTheStartThroughEveryStop(t *testing.T) {
	r := request(contracts.PresetCheapest, parcel("s1", kemang, senayan, 3), parcel("s2", blokM, pondok, 4))
	p := planner()
	p.Router = pathRouter{}
	res, err := p.Compute(context.Background(), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range res.Set.Options {
		for _, rt := range o.Routes {
			if len(rt.Geometry) != len(rt.Stops)+1 {
				t.Fatalf("%s: %d geometry points for %d stops", rt.ID, len(rt.Geometry), len(rt.Stops))
			}
			if rt.Geometry[0] != (contracts.LngLat{rt.StartPosition.Lng, rt.StartPosition.Lat}) {
				t.Errorf("%s: the path must begin at the start position", rt.ID)
			}
		}
	}

	p.Router = pathRouter{fail: true}
	res, err = p.Compute(context.Background(), r, nil)
	if err != nil {
		t.Fatalf("a failing router must not fail the quote: %v", err)
	}
	if res.Set.Options[0].Routes[0].Geometry != nil {
		t.Error("without a path the route stays undrawn")
	}
}
