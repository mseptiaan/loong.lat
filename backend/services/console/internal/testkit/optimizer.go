package testkit

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/optimizerapi"
)

// Optimizer is a fake of the optimizer service. It speaks the same wire contract (optimizerapi): a request in,
// four progress events (and the steps of a thorough search, when set) and then either one option set or an error
// out, as NDJSON. It does no optimizing: the
// single option visits every pickup and then every drop-off, at a fixed price (OptionCostRp).
type Optimizer struct {
	// URL is where to point optimizerapi.Client.
	URL string

	mu           sync.Mutex
	requests     []optimizerapi.QuoteRequest
	failure      *optimizerapi.Error
	beforeResult func()
	method       *contracts.Method
	report       *contracts.Report
	steps        []contracts.Msg
}

// OptionCostRp is the price of the option the fake answers with.
const OptionCostRp = 25_000

// NewOptimizer starts the fake.
func NewOptimizer(t testing.TB) *Optimizer {
	t.Helper()
	o := &Optimizer{}
	srv := httptest.NewServer(http.HandlerFunc(o.serve))
	t.Cleanup(srv.Close)
	o.URL = srv.URL
	return o
}

// Requests are the quotes asked for so far.
func (o *Optimizer) Requests() []optimizerapi.QuoteRequest {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]optimizerapi.QuoteRequest(nil), o.requests...)
}

// FailWith makes every quote end with this error, after the progress events.
func (o *Optimizer) FailWith(e *optimizerapi.Error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.failure = e
}

// SetMethod makes every option set the fake answers with say how it was found, as the optimizer does.
func (o *Optimizer) SetMethod(m *contracts.Method) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.method = m
}

// SetReport makes every option set the fake answers with carry a report, which the optimizer never does: the console
// must not keep it.
func (o *Optimizer) SetReport(r *contracts.Report) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.report = r
}

// SetSteps makes every quote report these steps of a thorough search after the third stage begins, each as a
// progress event of that stage carrying the message.
func (o *Optimizer) SetSteps(steps ...contracts.Msg) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.steps = steps
}

// BeforeResult runs fn after the progress events and before the result is sent: the moment a shipper can
// change the draft while the quote is being computed.
func (o *Optimizer) BeforeResult(fn func()) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.beforeResult = fn
}

func (o *Optimizer) serve(w http.ResponseWriter, r *http.Request) {
	var req optimizerapi.QuoteRequest
	if err := json.UnmarshalRead(r.Body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	o.mu.Lock()
	o.requests = append(o.requests, req)
	failure, before, method, report, steps := o.failure, o.beforeResult, o.method, o.report, o.steps
	o.mu.Unlock()

	w.Header().Set("Content-Type", "application/x-ndjson")
	send := func(ev optimizerapi.Event) {
		line, _ := json.Marshal(ev)
		_, _ = w.Write(append(line, '\n'))
		w.(http.Flusher).Flush()
	}
	for stage := 1; stage <= 4; stage++ {
		send(optimizerapi.Event{Type: optimizerapi.EventProgress, Stage: stage, Total: 4})
		if stage == 3 {
			for _, step := range steps {
				send(optimizerapi.Event{Type: optimizerapi.EventProgress, Stage: stage, Total: 4, Msg: &step})
			}
		}
	}
	if before != nil {
		before()
	}
	if failure != nil {
		send(optimizerapi.Event{Type: optimizerapi.EventError, Error: failure})
		return
	}
	set := OptionSetFor(req, OptionCostRp)
	set.Method, set.Report = method, report
	send(optimizerapi.Event{Type: optimizerapi.EventDone, Result: &optimizerapi.QuoteResult{
		OptionSet: set, Proven: true, RoadSource: "estimate", Engine: "fake-1",
	}})
}

// OptionSetFor is the single option the fake answers with: one motorbike route that picks everything up and
// then drops everything off, five minutes and a kilometre per leg, costing costRp.
func OptionSetFor(req optimizerapi.QuoteRequest, costRp int64) contracts.OptionSet {
	now := req.Now.Time
	if now.IsZero() {
		now = time.Now()
	}
	var stops []contracts.RouteStop
	add := func(kind contracts.StopKind, s contracts.Shipment, w contracts.Waypoint) {
		seq := len(stops) + 1
		eta := float64(seq * 5)
		stops = append(stops, contracts.RouteStop{
			ID: fmt.Sprintf("stop-%d", seq), Seq: seq, Kind: kind, ShipmentID: s.ID, Waypoint: w,
			LegKm: 1, LegMin: 5, EtaMin: eta, ArriveAt: contracts.At(now.Add(time.Duration(eta) * time.Minute)),
			Status: contracts.StopStatusWaiting, ItemsSummary: contracts.Msg{Key: "srv.engine.itemsSummary_one"},
		})
	}
	for _, s := range req.Shipments {
		add(contracts.StopKindPickup, s, s.Pickup)
	}
	for _, s := range req.Shipments {
		add(contracts.StopKindDropoff, s, s.Dropoff)
	}
	km, minutes := float64(len(stops)), float64(len(stops)*5)
	route := contracts.Route{
		ID: "r1", PlanID: req.PlanID, VehicleType: contracts.VehicleTypeMotor, Status: contracts.RouteStatusAwaitingDriver,
		StartPosition: stops[0].Waypoint.Position, StartAt: contracts.At(now), Stops: stops,
		DistanceKm: km, DurationMin: minutes, CostRp: costRp, TotalWeightKg: 1,
		Fare: []contracts.FareLine{{Label: contracts.Msg{Key: "srv.engine.fare.base"}, AmountRp: costRp}},
	}
	totals := contracts.Totals{CostRp: costRp, DistanceKm: km, DurationMin: minutes, AvgDeliveryMin: minutes, Stops: len(stops), Motor: 1}
	return contracts.OptionSet{
		ComputedAt: contracts.At(now), InputHash: "fake", Baseline: contracts.Totals{CostRp: costRp + 1000, DistanceKm: km + 1, Stops: len(stops), Motor: 1},
		Options: []contracts.PlanOption{{
			ID: req.PlanID + ":" + string(contracts.PresetCheapest), Preset: contracts.PresetCheapest, Recommended: true,
			Label: contracts.Msg{Key: "srv.engine.label.best"}, Headline: contracts.Msg{Key: "srv.engine.headline_other"},
			Routes: []contracts.Route{route}, Totals: totals,
			Savings: contracts.Savings{CostRp: 1000, Pct: 4, DistanceKm: 1}, Reasons: []contracts.Msg{},
		}},
	}
}

// Shipment is a shipment between two places in Jakarta with one small item.
func Shipment(id string) contracts.Shipment {
	place := func(name string, lat, lng float64) contracts.Waypoint {
		return contracts.Waypoint{
			PlaceRef:    contracts.PlaceRef{Name: name, Address: name + " street", Position: contracts.LatLng{Lat: lat, Lng: lng}},
			ContactName: "Contact", ContactPhone: "081200000000",
		}
	}
	return contracts.Shipment{
		ID: id, Pickup: place("Kemang "+id, -6.2608, 106.8133), Dropoff: place("Senayan "+id, -6.2275, 106.8029),
		Items: []contracts.Item{{ID: "i-" + id, Name: "Parcel", Qty: 1, WeightKg: 3, LengthCm: 30, WidthCm: 20, HeightCm: 10, Source: contracts.ItemSourceManual}},
	}
}
