// Package validate is an independent referee for solutions.
//
// The solver's evaluator schedules routes and reports a score. Validate does
// not trust it: it takes the finished timeline and re-derives every rule from
// the raw problem data (matrices, windows, demands, tags) and recomputes the
// objective values, so a scheduling bug cannot hide behind its own arithmetic.
package validate

import (
	"fmt"
	"slices"

	"github.com/mseptiaan/loong.lat/backend/pkg/engine/model"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/plan"
)

// Report is the outcome of checking a solution.
type Report struct {
	Valid      bool
	Violations []string
	// Recomputed holds the objective values derived from scratch.
	Recomputed model.Metrics
}

// Check verifies s against every rule of p.
func Check(p *model.Problem, s *model.Solution) *Report {
	r := &Report{}
	fail := func(format string, args ...any) {
		r.Violations = append(r.Violations, fmt.Sprintf(format, args...))
	}

	checkCoverage(p, s, fail)

	ev := model.NewEvaluator(p)
	var m model.Metrics
	gcUsed := make([][]map[string]bool, len(p.GC)) // [constraint][vehicle] -> tags touched
	for c := range gcUsed {
		gcUsed[c] = make([]map[string]bool, len(p.Vehicles))
	}

	for v, route := range s.Routes {
		if len(route) == 0 {
			continue
		}
		veh := &p.Vehicles[v]
		d := ev.Detail(v, route)
		if !d.Eval.OK {
			fail("%s: route is infeasible (%v)", veh.ID, d.Eval.Fail)
			continue
		}
		mat := p.Mats[veh.Profile]
		checkOrdering(p, v, route, fail)
		checkSkills(p, v, route, fail)

		t := d.Depart
		if t < veh.Window.Begin {
			fail("%s: departs at %s before its working window opens", veh.ID, p.Local(t).Format("15:04"))
		}
		pos := veh.Start
		var dist, active, rest, workSince, sumArrival int64
		var load float64
		orders := map[int]bool{}
		for _, st := range d.Stops {
			node := &p.Nodes[st.Node]
			tr, trDist, turn := mat.TravelTime(pos, node.Loc), mat.TravelDist(pos, node.Loc), int64(0)
			if st.Reload {
				// A new trip: the vehicle went back to its end location, unloaded, and set out again.
				if !veh.MultiTrip {
					fail("%s: returns to its depot before %s but is not allowed several trips", veh.ID, node.ID)
				}
				tr = mat.TravelTime(pos, veh.End) + mat.TravelTime(veh.End, node.Loc)
				trDist = mat.TravelDist(pos, veh.End) + mat.TravelDist(veh.End, node.Loc)
				turn = veh.Turnaround
			}
			if st.TravelSec != tr {
				fail("%s: leg to %s takes %ds in the timeline but %ds in the matrix", veh.ID, node.ID, st.TravelSec, tr)
			}
			waitRest := int64(0) // part of the wait that falls inside a fixed break
			if fb := veh.Fixed; fb != nil {
				// Driving stops for the fixed break, so the leg lasts its travel
				// time plus whatever part of the leg's span lies inside the break.
				legRest := overlap(fb, t, st.Arrival)
				waitRest = overlap(fb, st.Arrival, st.Begin)
				if st.Arrival != t+tr+turn+legRest {
					fail("%s: leg to %s does not pause exactly for the fixed break (%d vs %d)", veh.ID, node.ID, st.Arrival, t+tr+turn+legRest)
				}
				if st.BreakBefore != legRest+waitRest {
					fail("%s: rest before %s is %ds but the fixed break covers %ds of it", veh.ID, node.ID, st.BreakBefore, legRest+waitRest)
				}
				if overlap(fb, st.Begin, st.End) > 0 {
					fail("%s: the service at %s overlaps the fixed break", veh.ID, node.ID)
				}
				rest += st.BreakBefore
			} else {
				if st.BreakBefore > 0 {
					if veh.Break == nil || st.BreakBefore < veh.Break.Min {
						fail("%s: break before %s lasts %ds, below the minimum", veh.ID, node.ID, st.BreakBefore)
					}
					workSince = 0
				}
				t += st.BreakBefore
				rest += st.BreakBefore
				if st.Arrival != t+tr+turn {
					fail("%s: arrival at %s is inconsistent with the previous stop", veh.ID, node.ID)
				}
			}
			// Waiting is working time: it counts toward the break rule too.
			leg := tr + turn + st.Wait + node.Service
			workSince += leg
			if veh.Break != nil && workSince > veh.Break.MaxInter {
				fail("%s: worked %ds without a break by %s (limit %ds)", veh.ID, workSince, node.ID, veh.Break.MaxInter)
			}
			if st.Begin < st.Arrival {
				fail("%s: service at %s starts before arrival", veh.ID, node.ID)
			}
			if st.Wait != st.Begin-st.Arrival-waitRest {
				fail("%s: waiting at %s (%ds) does not match arrival and service start", veh.ID, node.ID, st.Wait)
			}
			if !within(node.Auth, st.Begin) {
				fail("%s: service at %s starts at %s outside its authorized time windows", veh.ID, node.ID, p.Local(st.Begin).Format("15:04"))
			}
			if st.End != st.Begin+node.Service {
				fail("%s: service duration at %s is wrong", veh.ID, node.ID)
			}
			dist += trDist
			active += tr + turn + node.Service
			t, pos = st.End, node.Loc

			orders[node.Order] = true
			if node.Kind == model.Delivery {
				sumArrival += st.Arrival
			}
			if node.Demand != nil && veh.SurchargeDim >= 0 && (node.Kind == model.Pickup || node.Onboard) {
				load += node.Demand[veh.SurchargeDim]
			}

			if len(node.Pref) > 0 && !within(node.Pref, st.Begin) {
				m[model.ObjUnsatPref]++
				m[model.ObjDelay] += latenessOf(node.Pref, st.Begin)
			}
			if p.PrefPossible[st.Node] && !p.PrefMatch[v*len(p.Nodes)+st.Node] {
				m[model.ObjUnsatPref]++
			}
		}
		trEnd, trEndDist := mat.TravelTime(pos, veh.End), mat.TravelDist(pos, veh.End)
		if veh.Open {
			trEnd, trEndDist = 0, 0 // the route ends at its last stop
		}
		if fb := veh.Fixed; fb != nil {
			legRest := overlap(fb, t, d.Arrive)
			if d.EndBreak != legRest {
				fail("%s: rest before returning is %ds but the fixed break covers %ds of it", veh.ID, d.EndBreak, legRest)
			}
			t += trEnd + legRest
			rest += d.EndBreak
			if want := overlap(fb, d.Depart, d.Arrive); rest != want {
				fail("%s: rests %ds in total but the fixed break covers %ds of the tour", veh.ID, rest, want)
			}
		} else {
			if d.EndBreak > 0 {
				workSince = 0
			}
			workSince += trEnd
			if veh.Break != nil && workSince > veh.Break.MaxInter {
				fail("%s: worked %ds without a break before returning (limit %ds)", veh.ID, workSince, veh.Break.MaxInter)
			}
			t += d.EndBreak + trEnd
			rest += d.EndBreak
		}
		dist += trEndDist
		active += trEnd
		if d.Arrive != t {
			fail("%s: arrival at the end location is inconsistent (%d vs %d)", veh.ID, d.Arrive, t)
		}
		if d.Arrive > veh.Window.End {
			fail("%s: returns at %s after its working window closes", veh.ID, p.Local(d.Arrive).Format("15:04"))
		}
		// Working duration is the whole tour minus breaks, waiting included.
		work := d.Arrive - d.Depart - rest
		if veh.MaxWork > 0 && work > veh.MaxWork {
			fail("%s: works %ds, above the maximum of %ds", veh.ID, work, veh.MaxWork)
		}
		if work < active {
			fail("%s: working duration %ds is below its driving and service time %ds", veh.ID, work, active)
		}

		reload := make([]bool, len(d.Stops))
		for i, st := range d.Stops {
			reload[i] = st.Reload && i > 0
		}
		checkCapacity(p, v, route, reload, fail)

		billed := dist
		if c := veh.Cost; c != nil && c.FreeApproach {
			billed -= d.Stops[0].TravelM
		}
		m[model.ObjDist] += billed
		m[model.ObjWork] += work
		m[model.ObjSumArrival] += sumArrival
		m[model.ObjMakespan] = max(m[model.ObjMakespan], d.Arrive-d.Depart)
		if c := veh.Cost; c != nil {
			b := plan.Billing{DistM: billed, WorkSec: work, Orders: len(orders), Load: load}
			if c.FreeApproach {
				b.WorkSec = max(0, work-d.Stops[0].TravelSec)
			}
			m[model.ObjCost] += c.Milli(b)
		}

		for c, gc := range p.GC {
			for _, n := range route {
				for _, tag := range p.Nodes[n].Tags {
					if slices.Contains(gc.Tags, tag) {
						if gcUsed[c][v] == nil {
							gcUsed[c][v] = map[string]bool{}
						}
						gcUsed[c][v][tag] = true
					}
				}
			}
		}
	}

	for c, gc := range p.GC {
		var total float64
		for v, tags := range gcUsed[c] {
			for tag := range tags {
				total += gc.TagCost[v][slices.Index(gc.Tags, tag)]
			}
		}
		if total > gc.Max+1e-9 {
			fail("global constraint %q: cumulated cost %.2f exceeds the maximum %.2f", gc.Name, total, gc.Max)
		}
	}

	for _, o := range s.Unassigned {
		m[model.ObjUnserved] += int64(len(p.Orders[o].Nodes))
	}
	m[model.ObjBlend] = p.BlendOf(&m)
	r.Recomputed = m
	if m != s.M {
		fail("objective values drifted: solver says %v, recomputed %v", s.M, m)
	}
	r.Valid = len(r.Violations) == 0
	return r
}

// checkCoverage ensures every stop is served exactly once, by one vehicle per
// order, or belongs to an order reported as unassigned.
func checkCoverage(p *model.Problem, s *model.Solution, fail func(string, ...any)) {
	where := make([]int, len(p.Nodes)) // vehicle index + 1, -1 for unassigned, 0 for unseen
	for v, route := range s.Routes {
		for _, n := range route {
			if n < 0 || n >= len(p.Nodes) {
				fail("route of %s contains the unknown node %d", p.Vehicles[v].ID, n)
				continue
			}
			if where[n] != 0 {
				fail("stop %s is visited more than once", p.Nodes[n].ID)
			}
			where[n] = v + 1
		}
	}
	for _, o := range s.Unassigned {
		for _, n := range p.Orders[o].Nodes {
			if where[n] != 0 {
				fail("stop %s is both routed and reported as unassigned", p.Nodes[n].ID)
			}
			where[n] = -1
		}
	}
	for o := range p.Orders {
		vehicle := 0
		for _, n := range p.Orders[o].Nodes {
			if where[n] == 0 {
				fail("stop %s is neither routed nor reported as unassigned", p.Nodes[n].ID)
				continue
			}
			if vehicle == 0 {
				vehicle = where[n]
			} else if where[n] != vehicle {
				fail("order %s is split across vehicles", p.Orders[o].ID)
				break
			}
		}
	}
}

// checkOrdering verifies pickup-before-delivery and listed sequences from the
// final positions in the route.
func checkOrdering(p *model.Problem, v int, route []int, fail func(string, ...any)) {
	pos := map[int]int{}
	for i, n := range route {
		pos[n] = i
	}
	for _, n := range route {
		node := &p.Nodes[n]
		ord := &p.Orders[node.Order]
		for _, other := range ord.Nodes {
			po, ok := pos[other]
			if !ok || other == n {
				continue
			}
			on := &p.Nodes[other]
			switch {
			case ord.Sequential && on.Seq < node.Seq && po > pos[n]:
				fail("%s: order %s is visited out of its listed sequence (%s after %s)", p.Vehicles[v].ID, ord.ID, on.ID, node.ID)
			case !ord.Sequential && on.Kind == model.Pickup && node.Kind == model.Delivery && po > pos[n]:
				fail("%s: %s is delivered before %s is picked up", p.Vehicles[v].ID, node.ID, on.ID)
			}
		}
	}
}

// checkSkills verifies, by name, that the vehicle offers every skill the orders
// in its route require.
func checkSkills(p *model.Problem, v int, route []int, fail func(string, ...any)) {
	veh := &p.Vehicles[v]
	seen := map[int]bool{}
	for _, n := range route {
		o := p.Nodes[n].Order
		if seen[o] {
			continue
		}
		seen[o] = true
		for _, sk := range p.Orders[o].RequiredSkills {
			if !slices.Contains(veh.SkillNames, sk) {
				fail("%s lacks the skill %q required by order %s", veh.ID, sk, p.Orders[o].ID)
			}
		}
	}
}

// checkCapacity verifies the load trip by trip: a vehicle that may make several
// trips unloads at its depot between them, so only what rides together counts.
// A trip is the stretch of the route between two returns to the depot, and an
// order's goods may not change trips.
func checkCapacity(p *model.Problem, v int, route []int, reload []bool, fail func(string, ...any)) {
	veh := &p.Vehicles[v]
	tripOf := map[int]int{} // order -> trip of its first stop
	for from, trip := 0, 0; from < len(route); trip++ {
		to := from + 1
		for to < len(route) && !reload[to] {
			to++
		}
		for _, n := range route[from:to] {
			o := p.Nodes[n].Order
			if first, seen := tripOf[o]; seen && first != trip {
				fail("%s: order %s is split over two trips", veh.ID, p.Orders[o].ID)
			}
			tripOf[o] = trip
		}
		checkTripLoad(p, v, route[from:to], fail)
		from = to
	}
}

func checkTripLoad(p *model.Problem, v int, route []int, fail func(string, ...any)) {
	veh := &p.Vehicles[v]
	load := make([]float64, len(p.Dims))
	for _, n := range route {
		if node := &p.Nodes[n]; node.Onboard && node.Demand != nil {
			for k := range load {
				load[k] += node.Demand[k]
			}
		}
	}
	over := func(where string) {
		for k, l := range load {
			if l > veh.Cap[k]+1e-9 {
				fail("%s: %s load %.1f exceeds the capacity %.1f %s", veh.ID, p.Dims[k], l, veh.Cap[k], where)
			}
		}
	}
	over("when leaving the start")
	for _, n := range route {
		node := &p.Nodes[n]
		if node.Demand == nil {
			continue
		}
		sign := -1.0
		if node.Kind == model.Pickup {
			sign = 1
		}
		for k := range load {
			load[k] += sign * node.Demand[k]
		}
		if node.Kind == model.Pickup {
			over("after " + node.ID)
		}
	}
}

// overlap is how much of [from, to) lies inside the fixed break.
func overlap(fb *model.FixedBreak, from, to int64) int64 {
	return max(0, min(to, fb.End)-max(from, fb.Begin))
}

func within(ws []model.Window, t int64) bool {
	for _, w := range ws {
		if t >= w.Begin && t <= w.End {
			return true
		}
	}
	return false
}

func latenessOf(ws []model.Window, t int64) int64 {
	var late int64
	for _, w := range ws {
		if t > w.End {
			late = t - w.End
		}
	}
	return late
}
