package analysis

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/mseptiaan/loong.lat/backend/pkg/engine/model"
)

// Causes of an unserved order: the stable codes of Unserved.Cause.
const (
	CauseNoSkill           = "no_vehicle_skill"     // no vehicle offers a required skill
	CauseTooHeavy          = "too_heavy"            // the load exceeds every eligible vehicle
	CauseWindowInBreak     = "window_in_break"      // the opening hours lie inside the drivers' fixed break
	CauseWindowUnreachable = "window_unreachable"   // no vehicle can arrive before the window closes
	CauseShiftTooShort     = "shift_too_short"      // no vehicle can serve it and be back before its shift ends
	CauseMaxWork           = "max_working_duration" // serving it alone exceeds the maximum working duration
	CauseDriverTimeFull    = "driver_time_full"     // the eligible drivers have no time left in their shifts
	CauseVehicleFull       = "vehicle_full"         // the eligible vehicles have no capacity left
	CauseScheduleConflict  = "schedule_conflict"    // every insertion breaks a time window
	CauseGlobalConstraint  = "global_constraint"    // a global constraint forbids it
	CauseNotPlaced         = "not_placed"           // it still fits; the search did not place it
	CauseUnknown           = "unknown"
)

// UnservedStop locates one stop of an unserved order.
type UnservedStop struct {
	ID         string  `json:"id"`
	Lat        float64 `json:"lat"`
	Lon        float64 `json:"lon"`
	ServiceMin float64 `json:"service_min"`
	// Demand is what the stop loads or unloads, per capacity dimension.
	Demand map[string]float64 `json:"demand,omitempty"`
	// StartWindows are the clock times between which service may start.
	StartWindows []string `json:"start_windows,omitempty"`
}

// Unserved explains why an order has no route.
type Unserved struct {
	Order string `json:"order"`
	// RequiredSkills are the capabilities a vehicle needs to serve the order.
	RequiredSkills []string       `json:"required_skills,omitempty"`
	Stops          []UnservedStop `json:"stops"`
	// Cause is a stable code (see the Cause constants) and Reason says the same
	// in a sentence, with the times, loads and vehicles that decide it.
	Cause  string `json:"cause"`
	Reason string `json:"reason"`
	// Reasons is the technical breakdown: why each group of vehicles cannot
	// serve the order even on a route of its own.
	Reasons []string `json:"reasons"`
}

// explainer finds why orders are unserved in one solution.
type explainer struct {
	p  *model.Problem
	s  *model.Solution
	ev *model.Evaluator
}

// explainUnserved finds, for every unassigned order, why it has no route.
func explainUnserved(p *model.Problem, s *model.Solution, ev *model.Evaluator) []Unserved {
	x := &explainer{p: p, s: s, ev: ev}
	var out []Unserved
	for _, o := range s.Unassigned {
		out = append(out, x.explain(o))
	}
	return out
}

func (x *explainer) explain(o int) Unserved {
	p := x.p
	u := Unserved{Order: p.Orders[o].ID, RequiredSkills: p.Orders[o].RequiredSkills}
	for _, n := range p.Orders[o].Nodes {
		node := &p.Nodes[n]
		u.Stops = append(u.Stops, UnservedStop{
			ID: node.ID, Lat: node.Pos.Lat, Lon: node.Pos.Lon, ServiceMin: round(float64(node.Service)/60, 1),
			Demand: Demand(p, node), StartWindows: x.windows(node),
		})
	}

	solo := soloRoute(p, o)
	evals := make([]model.RouteEval, len(p.Vehicles))
	// Count vehicles per failure reason: with many vehicles one line per
	// vehicle would drown the useful information.
	counts := map[string][]string{}
	for v := range p.Vehicles {
		evals[v] = x.ev.Eval(v, solo)
		if r := evals[v]; !r.OK {
			counts[r.Fail.String()] = append(counts[r.Fail.String()], p.Vehicles[v].ID)
		}
	}
	failed := 0
	for _, reason := range slices.Sorted(maps.Keys(counts)) {
		ids := counts[reason]
		failed += len(ids)
		sample := strings.Join(ids[:min(2, len(ids))], ", ")
		if len(ids) > 2 {
			sample += ", …"
		}
		u.Reasons = append(u.Reasons, fmt.Sprintf("%d vehicle(s) cannot serve it alone: %s (%s)", len(ids), reason, sample))
	}
	if failed < len(p.Vehicles) {
		u.Reasons = append(u.Reasons, "some vehicles could serve it alone, so the conflict is with the other orders (time, capacity or a global constraint)")
	}

	u.Cause, u.Reason = x.classify(o, solo, evals)
	return u
}

// classify names the main cause. Vehicles that lack a skill or the capacity
// are set aside first; what remains either cannot serve the order even alone
// (its own window, or the shift) or could, which makes it a conflict with the
// routes of the other orders.
func (x *explainer) classify(o int, solo []int, evals []model.RouteEval) (cause, reason string) {
	p := x.p
	var capable, alone []int
	skillful := 0
	for v, e := range evals {
		if e.Fail != model.FailSkill {
			skillful++
		}
		if e.Fail == model.FailSkill || e.Fail == model.FailCapacity {
			continue
		}
		capable = append(capable, v)
		if e.OK {
			alone = append(alone, v)
		}
	}
	switch {
	case skillful == 0:
		return CauseNoSkill, fmt.Sprintf("Tidak ada kendaraan yang memiliki skill %s.", quoteList(p.Orders[o].RequiredSkills))
	case len(capable) == 0:
		return x.tooHeavy(o, evals)
	case len(alone) == 0:
		return x.impossibleAlone(solo, capable, evals)
	default:
		return x.conflict(o, solo, alone)
	}
}

func (x *explainer) tooHeavy(o int, evals []model.RouteEval) (string, string) {
	p := x.p
	peak := x.peakLoad(o)
	best, worst := -1, 0.0
	var maxCap float64
	for k := range p.Dims {
		var top float64
		for v, e := range evals {
			if e.Fail != model.FailSkill {
				top = max(top, p.Vehicles[v].Cap[k])
			}
		}
		if over := peak[k] - top; over > worst {
			best, worst, maxCap = k, over, top
		}
	}
	if best < 0 {
		return CauseTooHeavy, "Muatannya tidak muat di kendaraan mana pun yang bisa mengangkutnya."
	}
	return CauseTooHeavy, fmt.Sprintf("Muatannya %s %s melebihi kapasitas terbesar kendaraan yang bisa mengangkutnya (%s %s).",
		num(peak[best]), p.Dims[best], num(maxCap), p.Dims[best])
}

// peakLoad is the most the order puts on a vehicle at any moment.
func (x *explainer) peakLoad(o int) []float64 {
	p := x.p
	load := make([]float64, len(p.Dims))
	for _, n := range p.Orders[o].Nodes {
		if node := &p.Nodes[n]; node.Demand != nil && (node.Kind == model.Pickup || node.Onboard) {
			for k := range load {
				load[k] += node.Demand[k]
			}
		}
	}
	return load
}

// impossibleAlone explains an order that no capable vehicle can serve even on
// a route of its own.
func (x *explainer) impossibleAlone(solo, capable []int, evals []model.RouteEval) (string, string) {
	var window, shift, maxWork []int
	for _, v := range capable {
		switch evals[v].Fail {
		case model.FailWindow:
			window = append(window, v)
		case model.FailShift:
			shift = append(shift, v)
		case model.FailMaxWork:
			maxWork = append(maxWork, v)
		}
	}
	switch {
	case len(window) > 0 && len(window) >= len(shift) && len(window) >= len(maxWork):
		return x.windowMissed(solo, window, evals)
	case len(shift) > 0 && len(shift) >= len(maxWork):
		return x.shiftMissed(solo, shift)
	case len(maxWork) > 0:
		veh := &x.p.Vehicles[maxWork[0]]
		return CauseMaxWork, fmt.Sprintf("Perjalanan dan layanannya saja sudah melebihi durasi kerja maksimum (%s) kendaraan yang bisa mengangkutnya.", jam(veh.MaxWork))
	}
	return CauseUnknown, fmt.Sprintf("Tidak ada kendaraan yang bisa melayaninya sendirian (%s).", evals[capable[0]].Fail)
}

// windowMissed covers an order whose stop window cannot be met by any vehicle.
func (x *explainer) windowMissed(solo, vehicles []int, evals []model.RouteEval) (string, string) {
	p := x.p
	blocked := true
	var bestVeh, bestNode int
	bestArrive, lastStart := int64(-1), int64(0)
	for _, v := range vehicles {
		n := solo[evals[v].FailAt]
		node := &p.Nodes[n]
		if fb := p.Vehicles[v].Fixed; fb == nil || !insideBreak(node, fb) {
			blocked = false
		}
		if arr := x.ev.EarliestArrival(v, n); bestArrive < 0 || arr < bestArrive {
			bestVeh, bestNode, bestArrive = v, n, arr
			lastStart = node.Auth[len(node.Auth)-1].End
		}
	}
	node := &p.Nodes[bestNode]
	win := strings.Join(x.windows(node), ", ")
	if blocked {
		fb := p.Vehicles[bestVeh].Fixed
		return CauseWindowInBreak, fmt.Sprintf("Jam bukanya (layanan harus mulai %s, lama layanan %s) jatuh seluruhnya di jam istirahat %s–%s, sehingga tidak ada pengemudi yang bisa melayaninya.",
			win, jam(node.Service), x.hm(fb.Begin), x.hm(fb.End))
	}
	if bestArrive > lastStart {
		return CauseWindowUnreachable, fmt.Sprintf("Jendela waktunya (layanan harus mulai %s) tidak terkejar: kendaraan tercepat (%s) berangkat pukul %s dan paling cepat baru tiba pukul %s, sedangkan layanan harus mulai paling lambat pukul %s.",
			win, p.Vehicles[bestVeh].ID, x.hm(p.Vehicles[bestVeh].Window.Begin), x.hm(bestArrive), x.hm(lastStart))
	}
	// The window is missed for a reason the arrival time alone does not show,
	// such as one that opens just before the break and is too short for the service.
	return CauseWindowUnreachable, fmt.Sprintf("Jendela waktunya (layanan harus mulai %s) tidak bisa dipenuhi kendaraan mana pun, dengan memperhitungkan lama layanan dan jam istirahat.", win)
}

// shiftMissed covers an order that cannot be served and left before the shift ends.
func (x *explainer) shiftMissed(solo, vehicles []int) (string, string) {
	p := x.p
	var bestVeh int
	bestArrive := int64(-1)
	for _, v := range vehicles {
		if arr, ok := x.ev.ShiftOverrun(v, solo); ok && (bestArrive < 0 || arr-p.Vehicles[v].Window.End < bestArrive-p.Vehicles[bestVeh].Window.End) {
			bestVeh, bestArrive = v, arr
		}
	}
	if bestArrive < 0 {
		return CauseShiftTooShort, "Tidak ada kendaraan yang bisa melayaninya dan kembali sebelum shift berakhir."
	}
	veh := &p.Vehicles[bestVeh]
	var wins string
	if w := x.windows(&p.Nodes[solo[0]]); len(w) > 0 {
		wins = fmt.Sprintf(" Layanan harus mulai %s.", strings.Join(w, ", "))
	}
	return CauseShiftTooShort, fmt.Sprintf("Shift tidak cukup.%s Dilayani secepat mungkin pun, kendaraan tercepat (%s) baru kembali ke gudang pukul %s, %s melewati akhir shift pukul %s.",
		wins, veh.ID, x.hm(bestArrive), jam(bestArrive-veh.Window.End), x.hm(veh.Window.End))
}

// attempt records what happened when an order was squeezed into one vehicle's route.
type attempt struct {
	fails   map[model.Fail]int // why the insertions failed, by reason
	overrun int64              // the smallest time by which a shift was exceeded, or -1
	endsAt  int64              // and when the route would have ended
}

// conflict explains an order that some vehicles could serve alone, by trying to
// squeeze it into the routes of those vehicles.
func (x *explainer) conflict(o int, solo, alone []int) (string, string) {
	p, s := x.p, x.s
	if len(solo) > 3 {
		return CauseUnknown, "Bisa dilayani sendirian, tetapi tidak muat bersama order lain."
	}

	attempts := make(map[int]*attempt, len(alone))
	for _, v := range alone {
		a := &attempt{fails: map[model.Fail]int{}, overrun: -1}
		attempts[v] = a
		end := p.Vehicles[v].Window.End
		var fits []int
		forEachInsertion(s.Routes[v], solo, func(cand []int) {
			if fits != nil {
				return
			}
			e := x.ev.Eval(v, cand)
			if e.OK {
				fits = slices.Clone(cand)
				return
			}
			a.fails[e.Fail]++
			if e.Fail == model.FailShift {
				if arr, ok := x.ev.ShiftOverrun(v, cand); ok && (a.overrun < 0 || arr-end < a.overrun) {
					a.overrun, a.endsAt = arr-end, arr
				}
			}
		})
		if fits != nil {
			return x.fitsButUnplaced(o, v, fits)
		}
	}

	// Each vehicle's main obstacle; then the obstacle most of them share.
	var timeFull, capFull, windowBound []int
	for _, v := range alone {
		switch dominant(attempts[v].fails) {
		case model.FailShift, model.FailMaxWork:
			timeFull = append(timeFull, v)
		case model.FailCapacity:
			capFull = append(capFull, v)
		default:
			windowBound = append(windowBound, v)
		}
	}
	switch {
	case len(timeFull) >= len(capFull) && len(timeFull) >= len(windowBound):
		return x.driverTimeFull(timeFull, len(alone), attempts)
	case len(capFull) >= len(windowBound):
		return x.vehicleFull(o, capFull, len(alone))
	}
	return CauseScheduleConflict, fmt.Sprintf("Di rute %s, setiap posisi penyisipan membuat order ini atau stop lain melewati jendela waktunya.", x.vehicleList(windowBound))
}

func dominant(fails map[model.Fail]int) model.Fail {
	best, n := model.FailNone, 0
	for _, f := range []model.Fail{model.FailShift, model.FailMaxWork, model.FailCapacity, model.FailWindow, model.FailBreak, model.FailOrder} {
		if fails[f] > n {
			best, n = f, fails[f]
		}
	}
	return best
}

// fitsButUnplaced handles an order that can be inserted without breaking any
// route: either a global constraint forbids the solution as a whole, or the
// search simply missed the spot.
func (x *explainer) fitsButUnplaced(o, v int, route []int) (string, string) {
	p := x.p
	c := x.s.Clone()
	c.Routes[v] = route
	c.Unassigned = slices.DeleteFunc(c.Unassigned, func(u int) bool { return u == o })
	c.Recompute(x.ev)
	if c.Feasible {
		return CauseNotPlaced, fmt.Sprintf("Sebenarnya masih muat di rute %s tanpa melanggar aturan, tetapi pencarian belum menempatkannya. Coba tambah iterasi atau jalankan ulang.", p.Vehicles[v].ID)
	}
	for ci, gc := range p.GC {
		var total float64
		for _, e := range c.Evals {
			if e.OK {
				total += e.GC[ci]
			}
		}
		if total > gc.Max {
			return CauseGlobalConstraint, fmt.Sprintf("Menambahkannya melanggar batasan global %q (%s melebihi maksimum %s).", gc.Name, num(total), num(gc.Max))
		}
	}
	return CauseGlobalConstraint, "Menambahkannya melanggar salah satu batasan global."
}

// driverTimeFull says that the vehicles able to carry the order have no time
// left, using the one that came closest as the example.
func (x *explainer) driverTimeFull(vehicles []int, eligible int, attempts map[int]*attempt) (string, string) {
	p := x.p
	best := vehicles[0]
	for _, v := range vehicles[1:] {
		if a, b := attempts[v], attempts[best]; a.overrun >= 0 && (b.overrun < 0 || a.overrun < b.overrun) {
			best = v
		}
	}
	veh, a := &p.Vehicles[best], attempts[best]
	e := x.s.Evals[best]
	reason := "Waktu pengemudi habis. " + x.eligibleText(vehicles, eligible) + " sudah penuh jadwalnya"
	if e.Used {
		who := "ia"
		if len(vehicles) > 1 {
			who = "yang paling longgar, " + veh.ID + ","
		}
		reason += fmt.Sprintf(": %s bekerja pukul %s–%s (%s kerja)", who, x.hm(e.Depart), x.hm(e.Arrive), jam(e.Work))
	}
	if a.overrun >= 0 {
		reason += fmt.Sprintf(", dan dengan order ini baru selesai paling cepat pukul %s, %s melewati akhir shift pukul %s", x.hm(a.endsAt), jam(a.overrun), x.hm(veh.Window.End))
	}
	return CauseDriverTimeFull, reason + "."
}

// eligibleText names the vehicles that could carry an order and that are
// blocked, out of all that could.
func (x *explainer) eligibleText(blocked []int, eligible int) string {
	list := x.vehicleList(blocked)
	switch {
	case eligible == 1:
		return fmt.Sprintf("Satu-satunya kendaraan yang mampu mengangkutnya (%s)", list)
	case len(blocked) == eligible:
		return fmt.Sprintf("Semua %d kendaraan yang mampu mengangkutnya (%s)", eligible, list)
	}
	return fmt.Sprintf("%d dari %d kendaraan yang mampu mengangkutnya (%s)", len(blocked), eligible, list)
}

// vehicleFull says that the vehicles able to carry the order are full, using
// the one with the most room as the example.
func (x *explainer) vehicleFull(o int, vehicles []int, eligible int) (string, string) {
	p := x.p
	need := x.peakLoad(o)
	var bestVeh, bestDim int
	var bestFree, bestUsed float64
	first := true
	for _, v := range vehicles {
		if p.Vehicles[v].MultiTrip {
			continue // its capacity limits one trip, which the route does not show
		}
		used := make([]float64, len(p.Dims))
		for _, n := range x.s.Routes[v] {
			if node := &p.Nodes[n]; node.Demand != nil && (node.Kind == model.Pickup || node.Onboard) {
				for k := range used {
					used[k] += node.Demand[k]
				}
			}
		}
		for k := range p.Dims {
			if free := p.Vehicles[v].Cap[k] - used[k]; need[k] > free && (first || free > bestFree) {
				bestVeh, bestDim, bestFree, bestUsed, first = v, k, free, used[k], false
			}
		}
	}
	if first {
		return CauseVehicleFull, "Kapasitas kendaraan yang mampu mengangkutnya sudah penuh."
	}
	dim, veh := p.Dims[bestDim], &p.Vehicles[bestVeh]
	who := "ia"
	if len(vehicles) > 1 {
		who = "yang paling longgar, " + veh.ID + ","
	}
	return CauseVehicleFull, fmt.Sprintf("Kapasitas penuh. %s sudah terisi: %s memuat %s dari %s %s, sedangkan order ini butuh %s %s.",
		x.eligibleText(vehicles, eligible), who, num(bestUsed), num(veh.Cap[bestDim]), dim, num(need[bestDim]), dim)
}

// vehicleList names up to three vehicles.
func (x *explainer) vehicleList(vehicles []int) string {
	names := make([]string, 0, 3)
	for _, v := range vehicles[:min(3, len(vehicles))] {
		names = append(names, x.p.Vehicles[v].ID)
	}
	out := strings.Join(names, ", ")
	if len(vehicles) > 3 {
		out += ", …"
	}
	return out
}

// forEachInsertion calls fn with every route that results from placing stops
// into route while keeping their relative order. fn must not keep cand.
func forEachInsertion(route, stops []int, fn func(cand []int)) {
	var rec func(cur []int, k, from int)
	rec = func(cur []int, k, from int) {
		if k == len(stops) {
			fn(cur)
			return
		}
		for i := from; i <= len(cur); i++ {
			rec(slices.Insert(slices.Clone(cur), i, stops[k]), k+1, i+1)
		}
	}
	rec(route, 0, 0)
}

// insideBreak reports whether no start time within the node's windows avoids
// the fixed break: a service that starts before the break must end by its
// beginning, and one that starts during it has to wait for its end.
func insideBreak(node *model.Node, fb *model.FixedBreak) bool {
	for _, w := range node.Auth {
		if w.Begin <= fb.Begin-node.Service || w.End >= fb.End {
			return false
		}
	}
	return true
}

// windows lists the clock times between which service at node may start.
func (x *explainer) windows(node *model.Node) []string { return WindowTexts(x.p, node.Auth) }

// WindowTexts renders time windows as clock times in the plan's time zone,
// such as "13:00–14:45". A window without limits on both sides says nothing and
// is left out, so an unrestricted stop yields no text at all.
func WindowTexts(p *model.Problem, ws []model.Window) []string {
	hm := func(sec int64) string { return p.Local(sec).Format("15:04") }
	var out []string
	for _, w := range ws {
		unboundedBegin, unboundedEnd := w.Begin <= -model.Inf/2, w.End >= model.Inf/2
		switch {
		case unboundedBegin && unboundedEnd:
		case unboundedBegin:
			out = append(out, "sampai "+hm(w.End))
		case unboundedEnd:
			out = append(out, "mulai "+hm(w.Begin))
		default:
			out = append(out, hm(w.Begin)+"–"+hm(w.End))
		}
	}
	return out
}

// Demand is what node loads or unloads per capacity dimension, without the
// floating-point residue and without the dimensions it does not use.
func Demand(p *model.Problem, node *model.Node) map[string]float64 {
	var out map[string]float64
	for k, dim := range p.Dims {
		if node.Demand == nil || node.Demand[k] == 0 {
			continue
		}
		if out == nil {
			out = map[string]float64{}
		}
		out[dim] = round(node.Demand[k], 6)
	}
	return out
}

func (x *explainer) hm(sec int64) string { return x.p.Local(sec).Format("15:04") }

// jam renders a duration in seconds in Indonesian.
func jam(sec int64) string {
	if sec < 3600 {
		return fmt.Sprintf("%d menit", (sec+30)/60)
	}
	return strings.Replace(fmt.Sprintf("%.1f", float64(sec)/3600), ".", ",", 1) + " jam"
}

// num renders a quantity with a decimal comma.
func num(v float64) string {
	return strings.Replace(strconv.FormatFloat(round(v, 1), 'f', -1, 64), ".", ",", 1)
}

func quoteList(items []string) string {
	q := make([]string, len(items))
	for i, it := range items {
		q[i] = strconv.Quote(it)
	}
	return strings.Join(q, ", ")
}
