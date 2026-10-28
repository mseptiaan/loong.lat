package model

import "github.com/mseptiaan/loong.lat/backend/pkg/engine/plan"

// Fail says why a route is infeasible.
type Fail uint8

// Failure reasons.
const (
	FailNone     Fail = iota
	FailOrder         // pickup/delivery precedence or listed sequence violated
	FailCapacity      // vehicle capacity exceeded
	FailMaxWork       // maximum working duration exceeded
	FailBreak         // one leg alone exceeds the maximum inter-break duration
	FailWindow        // a stop cannot be reached before its time window closes
	FailShift         // route does not fit in the vehicle's working time window
	FailSkill         // the vehicle lacks a skill an order requires
)

func (f Fail) String() string {
	return [...]string{
		"ok", "stop order violated", "capacity exceeded", "maximum working duration exceeded",
		"break rule cannot be met", "stop time window missed", "working time window exceeded",
		"vehicle lacks a required skill",
	}[f]
}

const eps = 1e-9

// RouteEval is the cheap, allocation-free summary of one vehicle's route.
type RouteEval struct {
	OK     bool
	Used   bool
	Fail   Fail
	FailAt int // index of the offending stop in the route, when known

	Depart, Arrive int64 // solver seconds
	Dist           int64 // meters driven
	// Billed is the part of Dist that is paid for and ranked by the distance
	// objective: all of it, except that a vehicle whose cost frees the approach
	// does not count the way to its first stop.
	Billed     int64
	Active     int64 // travel + service seconds
	Work       int64 // working duration: elapsed time minus breaks, waiting included
	Wait       int64 // idle seconds spent waiting for time windows
	Elapsed    int64 // Arrive - Depart, including waiting and breaks
	Cost       int64 // milli cost units
	UnsatPref  int64 // unsatisfied preferences
	Delay      int64 // seconds beyond preferred windows
	SumArrival int64 // total arrival time (solver seconds) over the delivery stops
	GC         [MaxGlobalConstraints]float64
}

// emptyEval is the evaluation of an unused vehicle.
var emptyEval = RouteEval{OK: true}

// EmptyEval returns the evaluation of a route without stops.
func EmptyEval() RouteEval { return emptyEval }

// StopTiming is the full timeline of one stop, used for reporting and validation.
type StopTiming struct {
	Node      int
	Arrival   int64
	Begin     int64 // service start
	End       int64 // service end
	Wait      int64
	TravelSec int64 // travel from the previous stop, by way of the depot when Reload is set
	TravelM   int64
	// Reload marks that the vehicle returned to its end location before this
	// stop to unload, which starts a new trip; ReloadArrive and ReloadDepart are
	// when it got there and left again.
	Reload                     bool
	ReloadArrive, ReloadDepart int64
	BreakBefore                int64 // rest taken before reaching this stop (a fixed break also counts the part spent waiting)
	Load                       []float64
	// PrefTW is -1 without preferred windows, otherwise 1 when met and 0 when not.
	PrefTW int8
	// PrefTag is -1 when no vehicle prefers the stop's tags, otherwise 1 when
	// the serving vehicle does and 0 when it does not.
	PrefTag int8
	Delay   int64
}

// RouteDetail is the complete timeline of a feasible route.
type RouteDetail struct {
	Vehicle     int
	Eval        RouteEval
	Depart      int64
	Arrive      int64
	InitialLoad []float64
	Trips       int // 1 unless the vehicle returns to its depot to start another trip
	Stops       []StopTiming
	EndTravel   int64 // seconds of the final leg back to the end location
	EndTravelM  int64
	EndBreak    int64 // break taken before the final leg
}

// Evaluator computes route metrics. It owns scratch buffers, so each goroutine
// needs its own instance; the Problem it reads is shared and immutable.
type Evaluator struct {
	P *Problem

	arr, begin, wait, wend []int64
	trv, trvM, brk         []int64
	trvA, tun              []int64 // multi-trip: travel to the depot before a stop, and the turnaround there
	reload, cut            []bool  // multi-trip: return to the depot before a stop; may a trip end before it
	last                   []int32 // multi-trip: last position of each order in the route, plus one
	onb, pref, maxp        []float64
	target                 []bool
	load                   []float64
	maxSeq, pick           []int32
	touched                []int
	depart, arrive         int64
}

// NewEvaluator creates an evaluator for p.
func NewEvaluator(p *Problem) *Evaluator {
	return &Evaluator{
		P:      p,
		load:   make([]float64, len(p.Dims)),
		maxSeq: make([]int32, len(p.Orders)),
		pick:   make([]int32, len(p.Orders)),
		last:   make([]int32, len(p.Orders)),
		onb:    make([]float64, len(p.Dims)),
		pref:   make([]float64, len(p.Dims)),
		maxp:   make([]float64, len(p.Dims)),
	}
}

func (e *Evaluator) ensure(m int) {
	if cap(e.arr) >= m+1 {
		e.arr, e.begin, e.wait, e.wend = e.arr[:m+1], e.begin[:m+1], e.wait[:m+1], e.wend[:m+1]
		e.trv, e.trvM, e.brk, e.target = e.trv[:m+1], e.trvM[:m+1], e.brk[:m+1], e.target[:m+1]
		e.trvA, e.tun, e.reload, e.cut = e.trvA[:m+1], e.tun[:m+1], e.reload[:m+1], e.cut[:m+1]
		return
	}
	n := 2 * (m + 1)
	e.arr, e.begin, e.wait, e.wend = make([]int64, m+1, n), make([]int64, m+1, n), make([]int64, m+1, n), make([]int64, m+1, n)
	e.trv, e.trvM, e.brk = make([]int64, m+1, n), make([]int64, m+1, n), make([]int64, m+1, n)
	e.target = make([]bool, m+1, n)
	e.trvA, e.tun = make([]int64, m+1, n), make([]int64, m+1, n)
	e.reload, e.cut = make([]bool, m+1, n), make([]bool, m+1, n)
}

// Eval evaluates the route of vehicle v. A route with no stops is feasible and
// costs nothing.
func (e *Evaluator) Eval(v int, route []int) RouteEval { return e.run(v, route) }

// Detail evaluates a route and returns its full timeline. For an infeasible
// route only Eval is meaningful.
func (e *Evaluator) Detail(v int, route []int) RouteDetail {
	ev := e.run(v, route)
	d := RouteDetail{Vehicle: v, Eval: ev}
	if !ev.OK || len(route) == 0 {
		return d
	}
	p := e.P
	m := len(route)
	d.Depart, d.Arrive = ev.Depart, ev.Arrive
	d.EndTravel, d.EndTravelM, d.EndBreak = e.trv[m], e.trvM[m], e.brk[m]

	multi := p.Vehicles[v].MultiTrip
	nDims := len(p.Dims)
	load := make([]float64, nDims)
	// startLoad sets load to what the trip beginning at stop from carries when it
	// leaves the depot: the goods of the deliveries that have no pickup.
	startLoad := func(from int) {
		clear(load)
		for j := from; j < m && (j == from || !(multi && e.reload[j])); j++ {
			if node := &p.Nodes[route[j]]; node.Onboard && node.Demand != nil {
				for k := range nDims {
					load[k] += node.Demand[k]
				}
			}
		}
	}
	startLoad(0)
	d.InitialLoad = append([]float64(nil), load...)
	d.Trips = 1

	d.Stops = make([]StopTiming, m)
	for i, n := range route {
		node := &p.Nodes[n]
		reload := multi && e.reload[i]
		if reload {
			startLoad(i)
			d.Trips++
		}
		if node.Demand != nil {
			sign := -1.0
			if node.Kind == Pickup {
				sign = 1
			}
			for k := range nDims {
				load[k] += sign * node.Demand[k]
			}
		}
		st := StopTiming{
			Node: n, Arrival: e.arr[i], Begin: e.begin[i], End: e.begin[i] + node.Service,
			Wait: e.wait[i], TravelSec: e.trv[i], TravelM: e.trvM[i], BreakBefore: e.brk[i],
			Load: append([]float64(nil), load...), PrefTW: -1, PrefTag: -1,
		}
		if reload {
			st.Reload = true
			st.ReloadArrive, st.ReloadDepart = e.depotVisit(&p.Vehicles[v], e.begin[i-1]+p.Nodes[route[i-1]].Service, i)
		}
		if len(node.Pref) > 0 {
			st.PrefTW = 0
			if inWindows(node.Pref, st.Begin) {
				st.PrefTW = 1
			} else {
				st.Delay = lateness(node.Pref, st.Begin)
			}
		}
		if p.PrefPossible[n] {
			st.PrefTag = 0
			if p.PrefMatch[v*len(p.Nodes)+n] {
				st.PrefTag = 1
			}
		}
		d.Stops[i] = st
	}
	return d
}

// ShiftOverrun evaluates a route that fails only because it does not fit in the
// vehicle's working window, and returns when it would end. It reports false when
// the route fits or fails for another reason.
func (e *Evaluator) ShiftOverrun(v int, route []int) (arrive int64, ok bool) {
	if ev := e.run(v, route); ev.Fail != FailShift {
		return 0, false
	}
	return e.arrive, true
}

// EarliestArrival is when vehicle v, leaving its start when its working window
// opens, first reaches the location of node n, with the fixed break respected.
func (e *Evaluator) EarliestArrival(v, n int) int64 {
	veh := &e.P.Vehicles[v]
	t, d := veh.Window.Begin, e.P.Mats[veh.Profile].TravelTime(veh.Start, e.P.Nodes[n].Loc)
	if veh.Fixed != nil {
		arrive, _ := veh.Fixed.advance(t, d)
		return arrive
	}
	return t + d
}

func (e *Evaluator) run(v int, route []int) RouteEval {
	p := e.P
	m := len(route)
	if m == 0 {
		return emptyEval
	}
	veh := &p.Vehicles[v]
	ev := RouteEval{Used: true}
	e.ensure(m)

	if fail, at := e.checkStatic(veh, route); fail != FailNone {
		ev.Fail, ev.FailAt = fail, at
		return ev
	}
	multi := veh.MultiTrip
	if multi && !e.splitTrips(veh, route) {
		ev.Fail = FailCapacity
		return ev
	}

	// Travel and distance depend only on the sequence, not on the clock.
	mat := p.Mats[veh.Profile]
	pos := veh.Start
	var active, dist int64
	for i, n := range route {
		node := &p.Nodes[n]
		if multi && e.reload[i] {
			// Back to the depot to unload, then on to this stop.
			a := mat.TravelTime(pos, veh.End)
			e.trvA[i], e.tun[i] = a, veh.Turnaround
			e.trv[i] = a + mat.TravelTime(veh.End, node.Loc)
			e.trvM[i] = mat.TravelDist(pos, veh.End) + mat.TravelDist(veh.End, node.Loc)
			active += e.trv[i] + veh.Turnaround + node.Service
		} else {
			e.trv[i], e.trvM[i] = mat.TravelTime(pos, node.Loc), mat.TravelDist(pos, node.Loc)
			e.tun[i] = 0
			active += e.trv[i] + node.Service
		}
		dist += e.trvM[i]
		pos = node.Loc
	}
	if veh.Open {
		e.trv[m], e.trvM[m] = 0, 0 // the route ends at its last stop
	} else {
		e.trv[m], e.trvM[m] = mat.TravelTime(pos, veh.End), mat.TravelDist(pos, veh.End)
	}
	active += e.trv[m]
	dist += e.trvM[m]
	// Working time can only grow with waiting, so driving and serving alone
	// already rule a route out when they exceed the limit.
	if veh.MaxWork > 0 && active > veh.MaxWork {
		ev.Fail = FailMaxWork
		return ev
	}

	// Aim for every preferred window; whenever that makes the route
	// infeasible, give up the preference responsible and try again.
	for i, n := range route {
		e.target[i] = len(p.Nodes[n].Target) > 0
	}
	for {
		fail, at := e.schedule(veh, route)
		if fail == FailNone {
			break
		}
		c := e.culprit(fail, at, m)
		if c < 0 {
			ev.Fail, ev.FailAt = fail, min(at, m-1)
			return ev
		}
		e.target[c] = false
	}

	ev.OK = true
	ev.Depart, ev.Arrive = e.depart, e.arrive
	ev.Dist, ev.Active, ev.Elapsed = dist, active, e.arrive-e.depart
	var rest int64
	for i := 0; i <= m; i++ {
		rest += e.brk[i]
	}
	ev.Work = ev.Elapsed - rest
	for i, n := range route {
		node := &p.Nodes[n]
		ev.Wait += e.wait[i]
		if node.Kind == Delivery {
			ev.SumArrival += e.arr[i]
		}
		if len(node.Pref) > 0 && !inWindows(node.Pref, e.begin[i]) {
			ev.UnsatPref++
			ev.Delay += lateness(node.Pref, e.begin[i])
		}
		if p.PrefPossible[n] && !p.PrefMatch[v*len(p.Nodes)+n] {
			ev.UnsatPref++
		}
	}
	ev.Billed = dist
	if c := veh.Cost; c != nil {
		// e.touched still lists the distinct orders of this route (see checkStatic).
		b := plan.Billing{DistM: dist, WorkSec: ev.Work, Orders: len(e.touched)}
		if c.FreeApproach {
			ev.Billed -= e.trvM[0]
			b.DistM = ev.Billed
			b.WorkSec = max(0, b.WorkSec-e.trv[0])
		}
		if veh.SurchargeDim >= 0 {
			for _, n := range route {
				if node := &p.Nodes[n]; node.Demand != nil && (node.Kind == Pickup || node.Onboard) {
					b.Load += node.Demand[veh.SurchargeDim]
				}
			}
		}
		ev.Cost = c.Milli(b)
	}
	for c := range p.GC {
		var mask uint64
		for _, n := range route {
			mask |= p.Nodes[n].GCMask[c]
		}
		for t := range p.GC[c].Tags {
			if mask&(1<<t) != 0 {
				ev.GC[c] += p.GC[c].TagCost[v][t]
			}
		}
	}
	return ev
}

// schedule finds the timeline of the route for the current set of targeted
// preferences: the earliest schedule, then a later departure that removes
// waiting at the start (Savelsbergh). The result is left in the scratch
// buffers.
func (e *Evaluator) schedule(veh *Vehicle, route []int) (Fail, int) {
	m := len(route)
	start := veh.Window.Begin
	if fail, at := e.forward(veh, route, start); fail != FailNone {
		return fail, at
	}
	if fail, at := e.checkWork(veh, m); fail != FailNone {
		// Leaving later may still absorb enough waiting to fit.
		if !e.shiftDeparture(veh, route, start) {
			return fail, at
		}
		return e.checkWork(veh, m)
	}
	e.shiftDeparture(veh, route, start)
	return e.checkWork(veh, m)
}

// shiftDeparture delays the departure by the largest amount that keeps every
// window satisfied and that removes waiting, then reschedules. It keeps the new
// schedule only if it is feasible and does not increase working time; otherwise
// the original schedule is restored. It reports whether a shift was applied.
func (e *Evaluator) shiftDeparture(veh *Vehicle, route []int, start int64) bool {
	m := len(route)
	var totalWait, cum int64
	deltaMax := Inf
	for i := range m {
		totalWait += e.wait[i]
		cum += e.wait[i]
		deltaMax = min(deltaMax, cum+e.wend[i]-e.begin[i])
	}
	deltaMax = min(deltaMax, cum+veh.Window.End-e.arrive)
	delta := min(deltaMax, totalWait)
	if delta <= 0 {
		return false
	}
	before := e.workOf(m)
	if fail, _ := e.forward(veh, route, start+delta); fail == FailNone && e.workOf(m) <= before {
		return true
	}
	e.forward(veh, route, start) // cannot fail: it succeeded before
	return false
}

// workOf is the working duration of the schedule in the scratch buffers: the
// elapsed time minus breaks, waiting included.
func (e *Evaluator) workOf(m int) int64 {
	w := e.arrive - e.depart
	for i := 0; i <= m; i++ {
		w -= e.brk[i]
	}
	return w
}

func (e *Evaluator) checkWork(veh *Vehicle, m int) (Fail, int) {
	if veh.MaxWork > 0 && e.workOf(m) > veh.MaxWork {
		return FailMaxWork, m
	}
	return FailNone, 0
}

// forward schedules the route as early as possible starting at start, taking
// sliding breaks when working time since the last break would otherwise exceed
// the limit, or pausing for the fixed break. Waiting counts as working time. It
// fills the scratch timeline and reports the first violated window.
func (e *Evaluator) forward(veh *Vehicle, route []int, start int64) (Fail, int) {
	p := e.P
	t := start
	m := len(route)
	e.depart = start
	fb := veh.Fixed
	multi := veh.MultiTrip
	var since int64 // working time since the last break
	for i := range m {
		node := &p.Nodes[route[i]]
		ws := node.Auth
		if e.target[i] {
			ws = node.Target
		}
		e.brk[i] = 0
		// The leg is the drive from the previous stop, plus the detour through the
		// depot and the turnaround there when a new trip starts before this stop.
		leg := e.trv[i]
		if multi {
			leg += e.tun[i]
		}
		arr := t + leg
		if fb != nil {
			arr, e.brk[i] = fb.advance(t, leg)
		}
		b, wend, ok := firstFit(ws, arr)
		if !ok {
			return FailWindow, i
		}
		if br := veh.Break; br != nil {
			if since+(b-t)+node.Service > br.MaxInter {
				// Rest before this leg: it also shortens the wait that follows.
				e.brk[i] = br.Min
				t += br.Min
				since = 0
				arr = t + leg
				if b, wend, ok = firstFit(ws, arr); !ok {
					return FailWindow, i
				}
				if (b-t)+node.Service > br.MaxInter {
					return FailBreak, i
				}
			}
			since += (b - t) + node.Service
		}
		wait := b - arr
		if fb != nil {
			if b < fb.End && b+node.Service > fb.Begin {
				// A service is not interrupted: it starts after the break.
				if b, wend, ok = firstFit(ws, fb.End); !ok {
					return FailWindow, i
				}
			}
			// Time spent waiting during the break is rest, not idleness.
			rest := fb.Overlap(arr, b)
			e.brk[i] += rest
			wait = b - arr - rest
		}
		e.arr[i], e.begin[i], e.wait[i], e.wend[i] = arr, b, wait, wend
		t = b + node.Service
	}
	e.brk[m] = 0
	if br := veh.Break; br != nil && since+e.trv[m] > br.MaxInter {
		if e.trv[m] > br.MaxInter {
			return FailBreak, m
		}
		e.brk[m] = br.Min
		t += br.Min
	}
	e.arrive = t + e.trv[m]
	if fb != nil {
		e.arrive, e.brk[m] = fb.advance(t, e.trv[m])
	}
	if e.arrive > veh.Window.End {
		return FailShift, m
	}
	return FailNone, 0
}

// depotVisit says when a vehicle that left the stop before stop i at prevEnd
// reaches its end location and when it sets out again.
func (e *Evaluator) depotVisit(veh *Vehicle, prevEnd int64, i int) (arrive, depart int64) {
	if fb := veh.Fixed; fb != nil {
		arrive, _ = fb.advance(prevEnd, e.trvA[i])
		depart, _ = fb.advance(arrive, veh.Turnaround)
		return arrive, depart
	}
	arrive = prevEnd + e.brk[i] + e.trvA[i]
	return arrive, arrive + veh.Turnaround
}

// splitTrips decides where a multi-trip vehicle returns to its depot: as late
// as its capacity allows, and never in the middle of an order, whose stops stay
// on one trip. It sets e.reload (true before the first stop of every trip but
// the first) and reports whether the route can be driven at all.
func (e *Evaluator) splitTrips(veh *Vehicle, route []int) bool {
	p := e.P
	m := len(route)
	nd := len(p.Dims)
	clear(e.reload[:m])

	// A trip may end before stop i only when no order has stops on both sides.
	for i, n := range route {
		e.last[p.Nodes[n].Order] = int32(i) + 1
	}
	maxLast := -1 // last position of any order that has started
	for i, n := range route {
		e.cut[i] = i > 0 && maxLast < i
		maxLast = max(maxLast, int(e.last[p.Nodes[n].Order])-1)
	}
	for _, n := range route {
		e.last[p.Nodes[n].Order] = 0
	}

	// reset starts an empty trip; add puts stop j on the trip and reports
	// whether the load still fits.
	reset := func() {
		clear(e.onb[:nd])
		clear(e.pref[:nd])
		clear(e.maxp[:nd])
	}
	add := func(j int) bool {
		node := &p.Nodes[route[j]]
		if node.Demand == nil {
			return true
		}
		for k := range nd {
			d := node.Demand[k]
			switch {
			case node.Kind == Pickup:
				e.pref[k] += d
				e.maxp[k] = max(e.maxp[k], e.pref[k])
			default:
				if node.Onboard {
					e.onb[k] += d
				}
				e.pref[k] -= d
			}
		}
		for k := range nd {
			if e.onb[k]+e.maxp[k] > veh.Cap[k]+eps {
				return false
			}
		}
		return true
	}

	start := 0
	reset()
	for i := range m {
		// If the stop does not fit on the current trip, end that trip at the latest
		// allowed point and rebuild the new one. add leaves the state dirty when it
		// fails, which the rebuild discards.
		for !add(i) {
			cut := -1
			for j := i; j > start; j-- {
				if e.cut[j] {
					cut = j
					break
				}
			}
			if cut < 0 {
				return false
			}
			e.reload[cut], start = true, cut
			reset()
			for j := start; j < i; j++ {
				if !add(j) {
					return false
				}
			}
		}
	}
	return true
}

// culprit chooses which targeted preference to drop after a failed schedule,
// or -1 when the failure does not stem from waiting for a preferred window.
func (e *Evaluator) culprit(fail Fail, at, m int) int {
	switch fail {
	case FailWindow, FailBreak:
		if at < m && e.target[at] {
			return at
		}
		for j := min(at, m) - 1; j >= 0; j-- {
			if e.target[j] && e.wait[j] > 0 {
				return j
			}
		}
	case FailShift, FailMaxWork:
		for j := m - 1; j >= 0; j-- {
			if e.target[j] && e.wait[j] > 0 {
				return j
			}
		}
	}
	return -1
}

// firstFit returns the earliest service start within one of the sorted
// windows for an arrival at arr, and the end of the window used.
func firstFit(ws []Window, arr int64) (begin, end int64, ok bool) {
	for _, w := range ws {
		if arr <= w.End {
			return max(arr, w.Begin), w.End, true
		}
	}
	return 0, 0, false
}

func inWindows(ws []Window, t int64) bool {
	for _, w := range ws {
		if t >= w.Begin && t <= w.End {
			return true
		}
	}
	return false
}

// lateness is how far t lies beyond the last window that ended before it;
// being early costs nothing.
func lateness(ws []Window, t int64) int64 {
	var late int64
	for _, w := range ws {
		if t > w.End {
			late = t - w.End
		}
	}
	return late
}

// checkStatic validates everything about a route that does not depend on the
// clock, in a single pass: stop order within each order, required skills, and
// capacity. Capacity has a fast path: loads never exceed the total of all
// pickups and onboard goods, so when that total fits there is nothing to check.
func (e *Evaluator) checkStatic(veh *Vehicle, route []int) (Fail, int) {
	p := e.P
	nd := len(p.Dims)
	total := e.load[:nd]
	clear(total)

	orderOK := true
	skillAt := -1
	e.touched = e.touched[:0]
	for i, n := range route {
		node := &p.Nodes[n]
		o := node.Order
		if e.maxSeq[o] == 0 {
			e.touched = append(e.touched, o)
		}
		if p.Orders[o].Sequential {
			if int32(node.Seq)+1 <= e.maxSeq[o] {
				orderOK = false
			}
			e.maxSeq[o] = int32(node.Seq) + 1
		} else {
			e.maxSeq[o] = 1
			if node.Kind == Pickup {
				e.pick[o]++
			} else if int(e.pick[o]) < p.Orders[o].Pickups {
				orderOK = false
			}
		}
		if skillAt < 0 && node.Need&^veh.Skills != 0 {
			skillAt = i
		}
		if node.Demand != nil && (node.Kind == Pickup || node.Onboard) {
			for k := range nd {
				total[k] += node.Demand[k]
			}
		}
	}
	for _, o := range e.touched {
		e.maxSeq[o], e.pick[o] = 0, 0
	}

	switch {
	case !orderOK:
		return FailOrder, 0
	case skillAt >= 0:
		return FailSkill, skillAt
	case veh.MultiTrip:
		return FailNone, 0 // capacity limits each trip; splitTrips checks it
	}
	fits := true
	for k := range nd {
		if total[k] > veh.Cap[k]+eps {
			fits = false
			break
		}
	}
	if !fits && !e.checkCapacity(veh, route) {
		return FailCapacity, 0
	}
	return FailNone, 0
}

func (e *Evaluator) checkCapacity(veh *Vehicle, route []int) bool {
	p := e.P
	nd := len(p.Dims)
	if nd == 0 {
		return true
	}
	load := e.load[:nd]
	clear(load)
	for _, n := range route {
		if node := &p.Nodes[n]; node.Onboard && node.Demand != nil {
			for k := range nd {
				load[k] += node.Demand[k]
			}
		}
	}
	for k := range nd {
		if load[k] > veh.Cap[k]+eps {
			return false
		}
	}
	for _, n := range route {
		node := &p.Nodes[n]
		if node.Demand == nil {
			continue
		}
		if node.Kind == Pickup {
			for k := range nd {
				load[k] += node.Demand[k]
				if load[k] > veh.Cap[k]+eps {
					return false
				}
			}
			continue
		}
		for k := range nd {
			load[k] -= node.Demand[k]
		}
	}
	return true
}
