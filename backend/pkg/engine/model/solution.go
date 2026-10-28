package model

import (
	"fmt"
	"slices"
	"strings"
)

// Solution assigns orders to vehicle routes. Orders that no route serves are
// listed in Unassigned. Evals and M are caches that Recompute rebuilds.
type Solution struct {
	P          *Problem
	Routes     [][]int // per vehicle, node indices in visiting order
	Unassigned []int   // order indices, ascending
	Evals      []RouteEval
	M          Metrics
	Feasible   bool // every route is feasible and the global constraints hold
}

// NewSolution returns a solution in which every vehicle is idle and every
// order is unassigned.
func NewSolution(p *Problem) *Solution {
	s := &Solution{
		P:          p,
		Routes:     make([][]int, len(p.Vehicles)),
		Unassigned: make([]int, len(p.Orders)),
		Evals:      make([]RouteEval, len(p.Vehicles)),
	}
	for i := range s.Unassigned {
		s.Unassigned[i] = i
	}
	for i := range s.Evals {
		s.Evals[i] = emptyEval
	}
	s.M, s.Feasible = p.Aggregate(s.Evals, s.unservedStops())
	return s
}

// Clone returns a deep copy.
func (s *Solution) Clone() *Solution {
	c := *s
	c.Routes = make([][]int, len(s.Routes))
	for i, r := range s.Routes {
		c.Routes[i] = slices.Clone(r)
	}
	c.Unassigned = slices.Clone(s.Unassigned)
	c.Evals = slices.Clone(s.Evals)
	return &c
}

func (s *Solution) unservedStops() int64 {
	var n int64
	for _, o := range s.Unassigned {
		n += int64(len(s.P.Orders[o].Nodes))
	}
	return n
}

// Recompute re-evaluates every route and rebuilds the aggregate metrics.
func (s *Solution) Recompute(ev *Evaluator) {
	for v, r := range s.Routes {
		s.Evals[v] = ev.Eval(v, r)
	}
	s.M, s.Feasible = s.P.Aggregate(s.Evals, s.unservedStops())
}

// RefreshMetrics rebuilds the aggregate from the cached route evaluations.
func (s *Solution) RefreshMetrics() {
	s.M, s.Feasible = s.P.Aggregate(s.Evals, s.unservedStops())
}

// OrderVehicle maps each order to the vehicle serving it, or -1.
func (s *Solution) OrderVehicle() []int {
	ov := make([]int, len(s.P.Orders))
	for i := range ov {
		ov[i] = -1
	}
	for v, r := range s.Routes {
		for _, n := range r {
			ov[s.P.Nodes[n].Order] = v
		}
	}
	return ov
}

// VehiclesUsed counts the vehicles that have at least one stop.
func (s *Solution) VehiclesUsed() int {
	n := 0
	for _, r := range s.Routes {
		if len(r) > 0 {
			n++
		}
	}
	return n
}

// Aggregate combines per-route evaluations into solution-level metrics and
// reports whether all routes and global constraints are feasible.
func (p *Problem) Aggregate(evals []RouteEval, unservedStops int64) (Metrics, bool) {
	var m Metrics
	ok := true
	var gc [MaxGlobalConstraints]float64
	for i := range evals {
		e := &evals[i]
		if !e.OK {
			ok = false
			continue
		}
		m[ObjUnsatPref] += e.UnsatPref
		m[ObjDelay] += e.Delay
		m[ObjCost] += e.Cost
		m[ObjDist] += e.Billed
		m[ObjWork] += e.Work
		m[ObjSumArrival] += e.SumArrival
		m[ObjMakespan] = max(m[ObjMakespan], e.Elapsed)
		for c := range p.GC {
			gc[c] += e.GC[c]
		}
	}
	m[ObjUnserved] = unservedStops
	m[ObjBlend] = p.BlendOf(&m)
	for c := range p.GC {
		if gc[c] > p.GC[c].Max+eps {
			ok = false
		}
	}
	return m, ok
}

// BlendOf is the weighted sum behind the ObjBlend metric, computed from the
// other components of m.
func (p *Problem) BlendOf(m *Metrics) int64 {
	b := p.Blend
	return b.Cost*m[ObjCost] + b.SumArrival*m[ObjSumArrival] + b.Dist*m[ObjDist] + b.Makespan*m[ObjMakespan]
}

// Describe returns a compact one-line-per-vehicle description for logs.
func (s *Solution) Describe() string {
	var b strings.Builder
	for v, r := range s.Routes {
		fmt.Fprintf(&b, "%s:", s.P.Vehicles[v].ID)
		for _, n := range r {
			b.WriteString(" " + s.P.Nodes[n].ID)
		}
		b.WriteByte('\n')
	}
	return b.String()
}
