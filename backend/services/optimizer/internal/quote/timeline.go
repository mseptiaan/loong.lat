package quote

import (
	"fmt"
	"math"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/routeplan"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/fare"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
)

// roadNet is the road distance between the places of a quote.
type roadNet struct {
	pts   []contracts.LatLng
	distM []int32
}

func (n roadNet) km(from, to int) float64 {
	if from == to {
		return 0
	}
	return float64(n.distM[from*len(n.pts)+to]) / 1000
}

// seqItem is a visit in a route: the shipment (an index into the route's own
// list), whether it is the pickup or the drop, and the place it happens at.
type seqItem struct {
	s    int
	kind contracts.StopKind
	at   int
}

type routeArgs struct {
	id, planID string
	vehicle    contracts.Vehicle
	shipments  []contracts.Shipment
	seq        []seqItem
	start      int // place index
	startAt    time.Time
	net        roadNet
}

func fareConfig(c contracts.FareConfig) job.Fare {
	return job.Fare{
		Base: float64(c.Base), PerKm: float64(c.PerKm), PerMin: float64(c.PerMin), PerExtraStop: float64(c.PerExtraStop),
		SurchargeAboveKg: c.SurchargeAboveKg, SurchargePerKg: float64(c.SurchargePerKg),
	}
}

func fareLines(lines []fare.Line) []contracts.FareLine {
	out := make([]contracts.FareLine, len(lines))
	for i, l := range lines {
		var params map[string]any
		if len(l.Params) > 0 {
			params = make(map[string]any, len(l.Params))
			for k, v := range l.Params {
				params[k] = v
			}
		}
		out[i] = contracts.FareLine{Label: msg(l.Key, params), AmountRp: l.AmountRp}
	}
	return out
}

// buildRoute lays out a visiting order in time and prices it. Each leg's speed
// follows the clock, so a route that runs into the evening rush shows it. The
// way from the start to the first stop is part of the timeline but not of the bill.
func buildRoute(a routeArgs) contracts.Route {
	stops := make([]contracts.RouteStop, 0, len(a.seq))
	prev := a.start
	var etaMin, billableKm, firstArrive float64

	for i, it := range a.seq {
		sh := a.shipments[it.s]
		wp := sh.Pickup
		if it.kind == contracts.StopKindDropoff {
			wp = sh.Dropoff
		}
		legKm := a.net.km(prev, it.at)
		speed := routeplan.SpeedKmh(a.vehicle.Type, routeplan.HourWIB(a.startAt.Add(time.Duration(etaMin*60_000)*time.Millisecond)))
		legMin := math.Max(1, round1(legKm/speed*60))
		etaMin += legMin
		if i == 0 {
			firstArrive = etaMin
		} else {
			billableKm += legKm
		}

		agg := aggregateItems(sh.Items)
		items := make([]contracts.StopItem, len(sh.Items))
		for k, item := range sh.Items {
			items[k] = contracts.StopItem{
				Name: item.Name, CatalogKey: item.CatalogKey, Qty: item.Qty, Fragile: item.Fragile,
				Detail: msg("dom.stopItem.detail", map[string]any{"kg": item.WeightKg * float64(item.Qty), "l": item.LengthCm, "w": item.WidthCm, "h": item.HeightCm}),
			}
		}
		stops = append(stops, contracts.RouteStop{
			ID:         fmt.Sprintf("%s:s%d", a.id, i+1),
			Seq:        i + 1,
			Kind:       it.kind,
			ShipmentID: sh.ID,
			Waypoint:   wp,
			LegKm:      round1(legKm),
			LegMin:     legMin,
			EtaMin:     round1(etaMin),
			ArriveAt:   contracts.At(a.startAt.Add(time.Duration(etaMin*60_000) * time.Millisecond)),
			Status:     contracts.StopStatusWaiting,
			ItemsSummary: msg("srv.engine.itemsSummary", map[string]any{
				"count": agg.ItemCount, "kg": agg.WeightKg,
			}),
			Items: items,
		})
		etaMin += routeplan.DwellMin(it.kind)
		prev = it.at
	}

	totalMin := etaMin
	billableMin := math.Max(1, totalMin-firstArrive)
	weight := aggregateShipments(a.shipments).WeightKg
	lines, total := fare.Compute(fareConfig(a.vehicle.Fare), fare.Load{Km: billableKm, Min: billableMin, Stops: len(a.seq), WeightKg: weight})

	return contracts.Route{
		ID:            a.id,
		PlanID:        a.planID,
		VehicleType:   a.vehicle.Type,
		Status:        contracts.RouteStatusAwaitingDriver,
		StartPosition: a.net.pts[a.start],
		StartAt:       contracts.At(a.startAt),
		Stops:         stops,
		DistanceKm:    round1(billableKm),
		DurationMin:   round1(totalMin),
		CostRp:        total,
		Fare:          fareLines(lines),
		TotalWeightKg: round1(weight),
	}
}

// totalsOf sums routes into the figures an option is compared on.
func totalsOf(routes []contracts.Route) contracts.Totals {
	var t contracts.Totals
	var km, longest, dropMin float64
	var drops int
	for _, r := range routes {
		t.CostRp += r.CostRp
		km += r.DistanceKm
		longest = max(longest, r.DurationMin)
		t.Stops += len(r.Stops)
		switch r.VehicleType {
		case contracts.VehicleTypeMotor:
			t.Motor++
		case contracts.VehicleTypeBlindvan:
			t.Blindvan++
		}
		for _, s := range r.Stops {
			if s.Kind == contracts.StopKindDropoff {
				dropMin += s.EtaMin
				drops++
			}
		}
	}
	t.DistanceKm = round1(km)
	t.DurationMin = round1(longest)
	t.AvgDeliveryMin = round1(dropMin / float64(max(1, drops)))
	return t
}

// lastArrival is when the last stop of any route is reached.
func lastArrival(routes []contracts.Route) time.Time {
	var last time.Time
	for _, r := range routes {
		if end := r.Stops[len(r.Stops)-1].ArriveAt.Time; end.After(last) {
			last = end
		}
	}
	return last
}
