package quote

import (
	"cmp"
	"encoding/json/v2"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/routeplan"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
)

var presetOrder = []contracts.Preset{contracts.PresetCheapest, contracts.PresetShortest, contracts.PresetFastest, contracts.PresetBalanced}

// jsRound rounds half up like JavaScript's Math.round, which the frontend's percentages were built on.
func jsRound(x float64) float64 { return math.Floor(x + 0.5) }

// shape is a quote laid out for assembly: where each place is, which shipment
// sits where, and the fleet the quote is made against.
type shape struct {
	req    *Request
	fleet  fleetProfile
	net    roadNet
	start  int // place index of the notional start
	pickup []int
	drop   []int
	// at the moment the quote is for
	startAt time.Time
}

// stopAt is the place of a stop.
func (s *shape) stopAt(ship int, kind contracts.StopKind) int {
	if kind == contracts.StopKindPickup {
		return s.pickup[ship]
	}
	return s.drop[ship]
}

// candidate is one distinct plan: the routes of one or more presets.
type candidate struct {
	presets   []contracts.Preset
	routes    []contracts.Route
	members   [][]int // shipment indexes per route
	totals    contracts.Totals
	last      time.Time
	late      time.Duration // how late the worst deadline is; negative when all are met early
	hasDue    bool
	proof     string
	signature string
}

// build lays the solver's routes out in time. Ids are placeholders until the
// candidate is named after its option.
func (s *shape) build(c job.Candidate, index map[string]int) (*candidate, error) {
	out := &candidate{proof: c.Proof}
	for i, r := range c.Routes {
		var members []int
		local := map[int]int{} // shipment index -> position in members
		var seq []seqItem
		for _, st := range r.Stops {
			si, ok := index[st.Shipment]
			if !ok {
				return nil, fmt.Errorf("solver returned an unknown shipment %q", st.Shipment)
			}
			li, seen := local[si]
			if !seen {
				li = len(members)
				local[si] = li
				members = append(members, si)
			}
			kind := contracts.StopKindDropoff
			if st.Kind == job.Pickup {
				kind = contracts.StopKindPickup
			}
			seq = append(seq, seqItem{s: li, kind: kind, at: s.stopAt(si, kind)})
		}
		vehicle, ok := s.fleet[contracts.VehicleType(r.Type)]
		if !ok {
			return nil, fmt.Errorf("solver returned an unknown vehicle type %q", r.Type)
		}
		ships := make([]contracts.Shipment, len(members))
		for k, si := range members {
			ships[k] = s.req.Shipments[si]
		}
		route := buildRoute(routeArgs{
			id: fmt.Sprintf("%s:tmp:r%d", s.req.PlanID, i+1), planID: s.req.PlanID, vehicle: vehicle,
			shipments: ships, seq: seq, start: s.start, startAt: s.startAt, net: s.net,
		})
		out.routes = append(out.routes, route)
		out.members = append(out.members, members)
	}
	out.totals = totalsOf(out.routes)
	out.last = lastArrival(out.routes)
	out.signature = signatureOf(c.Routes)
	out.late, out.hasDue = s.lateness(out.routes)
	return out, nil
}

// signatureOf identifies a plan by its routes, so equal plans of different presets merge.
func signatureOf(routes []job.Route) string {
	keys := make([]string, 0, len(routes))
	for _, r := range routes {
		data, _ := json.Marshal(r)
		keys = append(keys, string(data))
	}
	slices.Sort(keys)
	data, _ := json.Marshal(keys)
	return string(data)
}

type deadlineRow struct {
	name   string
	arrive time.Time
	due    time.Time
}

// deadlineRows says when each shipment with a deadline reaches its drop.
func deadlineRows(routes []contracts.Route, ships []contracts.Shipment) []deadlineRow {
	var rows []deadlineRow
	for _, s := range ships {
		if s.Deadline.IsZero() {
			continue
		}
		for _, r := range routes {
			found := false
			for _, st := range r.Stops {
				if st.Kind == contracts.StopKindDropoff && st.ShipmentID == s.ID {
					rows = append(rows, deadlineRow{s.Dropoff.Name, st.ArriveAt.Time, s.Deadline.Time})
					found = true
					break
				}
			}
			if found {
				break
			}
		}
	}
	return rows
}

// lateness is how far past its deadline the worst shipment arrives.
func (s *shape) lateness(routes []contracts.Route) (worst time.Duration, ok bool) {
	rows := deadlineRows(routes, s.req.Shipments)
	for i, r := range rows {
		if d := r.arrive.Sub(r.due); i == 0 || d > worst {
			worst = d
		}
	}
	return worst, len(rows) > 0
}

// nameRoutes gives a candidate's routes and stops their final ids.
func (c *candidate) nameRoutes(planID string, primary contracts.Preset) []contracts.Route {
	out := make([]contracts.Route, len(c.routes))
	for i, r := range c.routes {
		id := fmt.Sprintf("%s:%s:r%d", planID, primary, i+1)
		r.ID = id
		stops := make([]contracts.RouteStop, len(r.Stops))
		for k, st := range r.Stops {
			st.ID = fmt.Sprintf("%s:s%d", id, k+1)
			stops[k] = st
		}
		r.Stops = stops
		out[i] = r
	}
	return out
}

/* ---------------- labels and headlines ---------------- */

func presetMsg(p contracts.Preset) contracts.Msg { return msg("dom.preset."+string(p), nil) }

// optionLabel names an option after the presets it serves: one, several ("Seimbang · Termurah"), or "Best route" when all agree.
func optionLabel(presets []contracts.Preset) contracts.Msg {
	switch len(presets) {
	case len(presetOrder):
		return msg("srv.engine.label.best", nil)
	case 3:
		return msg("srv.engine.label.three", map[string]any{"a": presetMsg(presets[0]), "b": presetMsg(presets[1]), "c": presetMsg(presets[2])})
	case 2:
		return msg("srv.engine.label.two", map[string]any{"a": presetMsg(presets[0]), "b": presetMsg(presets[1])})
	}
	return presetMsg(presets[0])
}

// describeFleet is "1 motorbike + 1 blindvan" as a nested message, so that word order and plurals follow the reader's language.
func describeFleet(t contracts.Totals) contracts.Msg {
	var motor, van *contracts.Msg
	if t.Motor > 0 {
		m := msg("srv.engine.fleet.motor", map[string]any{"count": t.Motor})
		motor = &m
	}
	if t.Blindvan > 0 {
		m := msg("srv.engine.fleet.blindvan", map[string]any{"count": t.Blindvan})
		van = &m
	}
	switch {
	case motor != nil && van != nil:
		return msg("srv.engine.fleet.both", map[string]any{"motor": *motor, "van": *van})
	case motor != nil:
		return *motor
	case van != nil:
		return *van
	}
	return msg("srv.engine.fleet.motor", map[string]any{"count": 0})
}

// chain joins messages into a right-nested chain, join(a, join(b, c)), without ever joining translated text.
func chain(items []contracts.Msg, join func(a, b contracts.Msg) contracts.Msg) contracts.Msg {
	acc := items[len(items)-1]
	for i := len(items) - 2; i >= 0; i-- {
		acc = join(items[i], acc)
	}
	return acc
}

func ms(t time.Time) int64 { return t.UnixMilli() }

// wib is Jakarta time, the customer's calendar for deciding whether two moments fall on the same day.
var wib = time.FixedZone("WIB", 7*3600)

func deadlineReasons(routes []contracts.Route, ships []contracts.Shipment) []contracts.Msg {
	rows := deadlineRows(routes, ships)
	if len(rows) == 0 {
		return nil
	}
	show := func(r deadlineRow) contracts.Msg {
		key := "srv.engine.deadline.row"
		if r.arrive.In(wib).Format(time.DateOnly) != r.due.In(wib).Format(time.DateOnly) {
			// different days: a time alone would read "arrives 22:05 (deadline 17:00)" for a deadline tomorrow
			key = "srv.engine.deadline.rowDated"
		}
		return msg(key, map[string]any{"place": r.name, "arrive": ms(r.arrive), "due": ms(r.due)})
	}
	list := func(items []deadlineRow) contracts.Msg {
		msgs := make([]contracts.Msg, len(items))
		for i, r := range items {
			msgs[i] = show(r)
		}
		return chain(msgs, func(a, b contracts.Msg) contracts.Msg {
			return msg("srv.engine.deadline.join", map[string]any{"a": a, "b": b})
		})
	}
	var late []deadlineRow
	for _, r := range rows {
		if r.arrive.After(r.due) {
			late = append(late, r)
		}
	}
	if len(late) > 0 {
		return []contracts.Msg{msg("srv.engine.reason.deadlineLate", map[string]any{"count": len(late), "rows": list(late)})}
	}
	return []contracts.Msg{msg("srv.engine.reason.deadlineMet", map[string]any{"count": len(rows), "rows": list(rows)})}
}

/* ---------------- reasons that can be traced ---------------- */

// reasons explains an option in terms of numbers the customer can check.
func (s *shape) reasons(c *candidate, baseline contracts.Totals, single *candidate, proven bool) []contracts.Msg {
	n := len(s.req.Shipments)
	var out []contracts.Msg
	motor, hasMotor := s.fleet[contracts.VehicleTypeMotor]

	for i, r := range c.routes {
		members := make([]contracts.Shipment, len(c.members[i]))
		for k, si := range c.members[i] {
			members[k] = s.req.Shipments[si]
		}
		agg := aggregateShipments(members)
		var text contracts.Msg
		switch {
		case r.VehicleType == contracts.VehicleTypeBlindvan && hasMotor:
			switch {
			case agg.MaxDimCm > motor.MaxDimensionCm:
				text = msg("srv.engine.reason.vanDim", map[string]any{"dim": agg.MaxDimCm, "max": motor.MaxDimensionCm})
			case agg.WeightKg > motor.MaxWeightKg:
				text = msg("srv.engine.reason.vanWeight", map[string]any{"kg": agg.WeightKg, "max": motor.MaxWeightKg})
			default:
				text = msg("srv.engine.reason.vanVolume", map[string]any{"m3": agg.VolumeM3})
			}
		case r.VehicleType == contracts.VehicleTypeMotor:
			text = msg("srv.engine.reason.fitsMotor", map[string]any{"count": agg.ItemCount, "kg": agg.WeightKg})
		default:
			text = msg("srv.engine.reason.fitsVan", map[string]any{"count": agg.ItemCount, "kg": agg.WeightKg})
		}
		if len(c.routes) > 1 {
			text = msg("srv.engine.reason.route", map[string]any{"n": i + 1, "text": text})
		}
		out = append(out, text)
	}

	if saveKm := baseline.DistanceKm - c.totals.DistanceKm; len(c.routes) < n && saveKm > 0.2 {
		out = append(out, msg("srv.engine.reason.merge", map[string]any{
			"shipments": n, "count": len(c.routes), "km": c.totals.DistanceKm, "base": baseline.DistanceKm,
			"pct": jsRound(saveKm / baseline.DistanceKm * 100),
		}))
	}

	switch saving := baseline.CostRp - c.totals.CostRp; {
	case saving > 0:
		out = append(out, msg("srv.engine.reason.costSaves", map[string]any{"cost": c.totals.CostRp, "saving": saving, "pct": jsRound(float64(saving) / float64(baseline.CostRp) * 100)}))
	case saving < 0:
		out = append(out, msg("srv.engine.reason.costMore", map[string]any{"cost": c.totals.CostRp, "extra": -saving}))
	default:
		out = append(out, msg("srv.engine.reason.costSame", map[string]any{"cost": c.totals.CostRp}))
	}
	if proven {
		out = append(out, msg("srv.engine.reason.provenOptimal", map[string]any{"count": n}))
	}

	if count := len(c.routes); count > 1 {
		if single != nil {
			out = append(out, msg("srv.engine.reason.splitVs", map[string]any{"count": count, "duration": c.totals.DurationMin, "single": single.totals.DurationMin}))
		} else {
			out = append(out, msg("srv.engine.reason.split", map[string]any{"count": count, "duration": c.totals.DurationMin}))
		}
	} else {
		out = append(out, msg("srv.engine.reason.single", map[string]any{"duration": c.totals.DurationMin}))
	}

	if n > 1 {
		pickupsFirst := true
		for _, r := range c.routes {
			firstDrop := slices.IndexFunc(r.Stops, func(st contracts.RouteStop) bool { return st.Kind == contracts.StopKindDropoff })
			if firstDrop >= 0 && slices.ContainsFunc(r.Stops[firstDrop:], func(st contracts.RouteStop) bool { return st.Kind != contracts.StopKindDropoff }) {
				pickupsFirst = false
			}
		}
		key := "srv.engine.reason.interleaved"
		if pickupsFirst {
			key = "srv.engine.reason.pickupsFirst"
		}
		out = append(out, msg(key, map[string]any{"avg": c.totals.AvgDeliveryMin}))
	}

	if f := routeplan.TrafficFactor(routeplan.HourWIB(s.startAt)); f < 0.7 {
		out = append(out, msg("srv.engine.reason.rush", map[string]any{"time": ms(s.startAt), "pct": jsRound((1 - f) * 100)}))
	}

	first := c.routes[0]
	steps := make([]contracts.Msg, 0, 6)
	for _, st := range first.Stops[:min(6, len(first.Stops))] {
		key := "srv.engine.seq.dropoff"
		if st.Kind == contracts.StopKindPickup {
			key = "srv.engine.seq.pickup"
		}
		steps = append(steps, msg(key, map[string]any{"place": st.Waypoint.Name}))
	}
	joined := chain(steps, func(a, b contracts.Msg) contracts.Msg {
		return msg("srv.engine.seq.then", map[string]any{"a": a, "b": b})
	})
	if len(first.Stops) > 6 {
		joined = msg("srv.engine.seq.more", map[string]any{"a": joined})
	}
	key := "srv.engine.reason.order"
	if len(c.routes) > 1 {
		key = "srv.engine.reason.orderRoute1"
	}
	out = append(out, msg(key, map[string]any{"steps": joined}))

	return append(out, deadlineReasons(c.routes, s.req.Shipments)...)
}

/* ---------------- the option set ---------------- */

// baselineOf prices sending every shipment on its own, each on the smallest vehicle that takes it.
func (s *shape) baselineOf() (contracts.Totals, error) {
	var routes []contracts.Route
	for i, ship := range s.req.Shipments {
		typ, ok := s.fleet.smallestFit(aggregateItems(ship.Items))
		if !ok {
			return contracts.Totals{}, invalid("shipments", "srv.engine.tooBig", nil)
		}
		routes = append(routes, buildRoute(routeArgs{
			id: fmt.Sprintf("%s:base:r%d", s.req.PlanID, i+1), planID: s.req.PlanID, vehicle: s.fleet[typ],
			shipments: []contracts.Shipment{ship},
			seq: []seqItem{
				{s: 0, kind: contracts.StopKindPickup, at: s.pickup[i]},
				{s: 0, kind: contracts.StopKindDropoff, at: s.drop[i]},
			},
			start: s.start, startAt: s.startAt, net: s.net,
		}))
	}
	return totalsOf(routes), nil
}

// assemble turns the solver's candidates into the options of the wizard. method says how they were found; assemble
// completes it with whether every option is proven optimal, which only the candidates can tell.
func (s *shape) assemble(res *job.Result, method contracts.Method) (*contracts.OptionSet, bool, error) {
	baseline, err := s.baselineOf()
	if err != nil {
		return nil, false, err
	}
	index := make(map[string]int, len(s.req.Shipments))
	for i, sh := range s.req.Shipments {
		index[sh.ID] = i
	}

	// Presets whose plans come out identical are one option.
	var cands []*candidate
	bySig := map[string]*candidate{}
	chosen := map[contracts.Preset]*candidate{}
	proven := true
	for _, jc := range res.Candidates {
		c, err := s.build(jc, index)
		if err != nil {
			return nil, false, err
		}
		if jc.Proof != job.ProofOptimal {
			proven = false
		}
		if prev, ok := bySig[c.signature]; ok {
			c = prev
		} else {
			bySig[c.signature] = c
			cands = append(cands, c)
		}
		chosen[contracts.Preset(jc.Preset)] = c
	}
	if len(cands) == 0 {
		return nil, false, &Error{Code: CodeService, Msg: msg("srv.engine.noVehicles", nil)}
	}

	// When some plan meets every deadline, only such plans are offered; when none
	// does, all stay and each says which shipments run late.
	if slices.ContainsFunc(cands, func(c *candidate) bool { return c.hasDue }) {
		var onTime []*candidate
		for _, c := range cands {
			if c.late <= 0 {
				onTime = append(onTime, c)
			}
		}
		if len(onTime) > 0 && len(onTime) < len(cands) {
			for p, c := range chosen {
				if c.late > 0 {
					chosen[p] = nearest(c, onTime)
				}
			}
		}
	}

	merged := map[*candidate][]contracts.Preset{}
	var order []*candidate
	for _, p := range presetOrder {
		c, ok := chosen[p]
		if !ok {
			continue
		}
		if _, seen := merged[c]; !seen {
			order = append(order, c)
		}
		merged[c] = append(merged[c], p)
	}

	var single *candidate
	for _, c := range order {
		if len(c.routes) == 1 {
			single = c
			break
		}
	}

	wanted := s.req.Priority.Preset
	options := make([]contracts.PlanOption, 0, len(order))
	for _, c := range order {
		presets := merged[c]
		recommended := slices.Contains(presets, wanted)
		primary := presets[0]
		if recommended {
			primary = wanted
		}
		saving := baseline.CostRp - c.totals.CostRp
		pct := 0.0
		if baseline.CostRp != 0 {
			pct = jsRound(float64(saving) / float64(baseline.CostRp) * 100)
		}
		options = append(options, contracts.PlanOption{
			ID:          fmt.Sprintf("%s:%s", s.req.PlanID, primary),
			Preset:      primary,
			Label:       optionLabel(presets),
			Recommended: recommended,
			Headline: msg("srv.engine.headline", map[string]any{
				"fleet": describeFleet(c.totals), "count": c.totals.Stops, "time": ms(c.last),
			}),
			Routes:  c.nameRoutes(s.req.PlanID, primary),
			Totals:  c.totals,
			Savings: contracts.Savings{CostRp: saving, Pct: pct, DistanceKm: round1(baseline.DistanceKm - c.totals.DistanceKm)},
			Reasons: s.reasons(c, baseline, single, c.proof == job.ProofOptimal),
		})
	}
	// The recommended option first, the rest in preset order.
	slices.SortStableFunc(options, func(a, b contracts.PlanOption) int {
		if a.Recommended != b.Recommended {
			if a.Recommended {
				return -1
			}
			return 1
		}
		return cmp.Compare(slices.Index(presetOrder, a.Preset), slices.Index(presetOrder, b.Preset))
	})

	method.Proven = proven
	return &contracts.OptionSet{
		ComputedAt: contracts.At(s.req.Now),
		Baseline:   baseline,
		Options:    options,
		InputHash:  inputHash(s.req, s.fleet, res.Engine),
		Method:     &method,
	}, proven, nil
}

// nearest is the on-time candidate closest in cost to c, the plan a late preset gives way to.
func nearest(c *candidate, onTime []*candidate) *candidate {
	best := onTime[0]
	for _, o := range onTime[1:] {
		if abs(o.totals.CostRp-c.totals.CostRp) < abs(best.totals.CostRp-c.totals.CostRp) {
			best = o
		}
	}
	return best
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
