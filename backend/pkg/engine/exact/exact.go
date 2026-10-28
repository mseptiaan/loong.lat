// Package exact finds provably optimal solutions of small problems by
// exhaustive search. It exists to referee the heuristic solver: for a handful
// of orders it can state whether the heuristic's answer is the true optimum.
//
// The search is complete, not clever. Orders are split into blocks, every
// block goes to a class of identical vehicles, and every valid visiting
// sequence of a block is evaluated with the same evaluator the heuristic uses.
// Routes of one block and vehicle class that another route beats in every
// component are discarded, which keeps the combination step small without
// ever losing the optimum, because every objective is monotone in those
// components.
package exact

import (
	"errors"
	"fmt"
	"math/bits"
	"slices"

	"github.com/mseptiaan/loong.lat/backend/pkg/engine/model"
)

// MaxOrders is the largest problem Solve accepts.
const MaxOrders = 6

// DefaultBudget caps the number of route evaluations.
const DefaultBudget = 30_000_000

// Result is a proven optimum.
type Result struct {
	Solution    *model.Solution
	Evaluations int64 // routes evaluated
	Classes     int   // groups of identical vehicles
}

// ErrTooLarge reports a problem beyond the exhaustive search's reach.
var ErrTooLarge = errors.New("problem too large for exhaustive search")

// Solve returns an optimal solution under the problem's objective priorities.
// budget bounds the work; zero means DefaultBudget.
func Solve(p *model.Problem, budget int64) (*Result, error) {
	n := len(p.Orders)
	if n > MaxOrders {
		return nil, fmt.Errorf("%w: %d orders, at most %d", ErrTooLarge, n, MaxOrders)
	}
	if budget <= 0 {
		budget = DefaultBudget
	}
	s := &searcher{p: p, ev: model.NewEvaluator(p), budget: budget, n: n}
	s.cache = map[[2]int][]profile{}
	s.groupVehicles()

	// First look for a solution that serves every order. Only if there is none
	// is it worth also exploring which orders to leave out.
	for _, allowUnserved := range []bool{false, true} {
		s.allowUnserved = allowUnserved
		s.found = false
		if err := s.combine(); err != nil {
			return nil, err
		}
		if s.found {
			break
		}
	}
	if !s.found {
		return nil, errors.New("no solution exists")
	}
	return s.build(), nil
}

// class is a group of vehicles that are identical in every respect.
type class struct {
	members []int
}

// profile is one distinct way to serve a block of orders with one vehicle
// class: the sequence and the numbers that matter for the objectives.
type profile struct {
	seq []int
	ev  model.RouteEval
	vec [vecLen]int64
}

// vecLen is the number of components a route is compared on: the additive and
// maximum metrics (everything Aggregate reads from a route) plus the global
// constraint costs.
const vecLen = 7 + model.MaxGlobalConstraints

type pick struct {
	mask, class, prof int
}

type searcher struct {
	p      *model.Problem
	ev     *model.Evaluator
	n      int
	budget int64
	evals  int64

	classes []class
	cache   map[[2]int][]profile // (block mask, class) -> Pareto-minimal profiles

	allowUnserved bool
	found         bool
	best          model.Metrics
	bestPicks     []pick
	bestUnserved  []int
}

func (s *searcher) groupVehicles() {
	index := map[int]int{} // representative vehicle -> class index
	for v, rep := range s.p.VehicleClasses() {
		idx, ok := index[rep]
		if !ok {
			idx = len(s.classes)
			index[rep] = idx
			s.classes = append(s.classes, class{})
		}
		s.classes[idx].members = append(s.classes[idx].members, v)
	}
}

// blockProfiles returns the Pareto-minimal feasible routes serving exactly the
// orders in mask with a vehicle of class c.
func (s *searcher) blockProfiles(mask, c int) ([]profile, error) {
	key := [2]int{mask, c}
	if ps, ok := s.cache[key]; ok {
		return ps, nil
	}
	var nodes []int
	for o := range s.n {
		if mask&(1<<o) != 0 {
			nodes = append(nodes, s.p.Orders[o].Nodes...)
		}
	}
	v := s.classes[c].members[0]

	// Depth-first over sequences that respect each order's own precedence.
	placed := make([]bool, len(nodes))
	seq := make([]int, 0, len(nodes))
	var out []profile
	var overrun bool
	var dfs func()
	dfs = func() {
		if overrun {
			return
		}
		if len(seq) == len(nodes) {
			if s.evals++; s.evals > s.budget {
				overrun = true
				return
			}
			ev := s.ev.Eval(v, seq)
			if ev.OK {
				out = addProfile(out, profile{seq: slices.Clone(seq), ev: ev, vec: vector(ev)})
			}
			return
		}
		for i, n := range nodes {
			if placed[i] || !s.ready(n, nodes, placed) {
				continue
			}
			placed[i] = true
			seq = append(seq, n)
			dfs()
			seq = seq[:len(seq)-1]
			placed[i] = false
		}
	}
	dfs()
	if overrun {
		return nil, fmt.Errorf("%w: more than %d route evaluations", ErrTooLarge, s.budget)
	}
	s.cache[key] = out
	return out, nil
}

// ready reports whether node n may be visited next given what is placed: a
// delivery needs all pickups of its order done, and a sequential order must be
// visited in its listed order.
func (s *searcher) ready(n int, nodes []int, placed []bool) bool {
	node := &s.p.Nodes[n]
	ord := &s.p.Orders[node.Order]
	for i, m := range nodes {
		if m == n || s.p.Nodes[m].Order != node.Order || placed[i] {
			continue
		}
		other := &s.p.Nodes[m]
		switch {
		case ord.Sequential && other.Seq < node.Seq:
			return false // an earlier stop of the order is still open
		case !ord.Sequential && node.Kind == model.Delivery && other.Kind == model.Pickup:
			return false
		}
	}
	return true
}

func vector(ev model.RouteEval) [vecLen]int64 {
	v := [vecLen]int64{ev.UnsatPref, ev.Delay, ev.Cost, ev.Billed, ev.Elapsed, ev.Work, ev.SumArrival}
	for i, g := range ev.GC {
		v[7+i] = int64(g*1000 + 0.5)
	}
	return v
}

// dominates reports whether a is at least as good as b everywhere.
func dominates(a, b profile) bool {
	for i := range a.vec {
		if a.vec[i] > b.vec[i] {
			return false
		}
	}
	return true
}

// addProfile keeps only Pareto-minimal profiles.
func addProfile(set []profile, p profile) []profile {
	for _, q := range set {
		if dominates(q, p) {
			return set
		}
	}
	set = slices.DeleteFunc(set, func(q profile) bool { return dominates(p, q) })
	return append(set, p)
}

// combine searches every way of splitting the orders into blocks served by
// vehicle classes and keeps the lexicographically best solution.
func (s *searcher) combine() error {
	used := make([]int, len(s.classes))
	var picks []pick
	var unserved []int
	var evals []model.RouteEval
	full := 1<<s.n - 1

	var rec func(remaining int) error
	rec = func(remaining int) error {
		if remaining == 0 {
			var stops int64
			for _, o := range unserved {
				stops += int64(len(s.p.Orders[o].Nodes))
			}
			m, ok := s.p.Aggregate(evals, stops)
			if ok && (!s.found || s.p.Less(&m, &s.best)) {
				s.found, s.best = true, m
				s.bestPicks = slices.Clone(picks)
				s.bestUnserved = slices.Clone(unserved)
			}
			return nil
		}
		low := remaining & -remaining
		rest := remaining &^ low
		// Every block containing the lowest remaining order.
		for sub := rest; ; sub = (sub - 1) & rest {
			block := low | sub
			for c := range s.classes {
				if used[c] >= len(s.classes[c].members) {
					continue
				}
				profs, err := s.blockProfiles(block, c)
				if err != nil {
					return err
				}
				for i, pr := range profs {
					used[c]++
					picks = append(picks, pick{block, c, i})
					evals = append(evals, pr.ev)
					err := rec(remaining &^ block)
					evals = evals[:len(evals)-1]
					picks = picks[:len(picks)-1]
					used[c]--
					if err != nil {
						return err
					}
				}
			}
			if sub == 0 {
				break
			}
		}
		if s.allowUnserved {
			o := bits.TrailingZeros(uint(low))
			unserved = append(unserved, o)
			err := rec(rest)
			unserved = unserved[:len(unserved)-1]
			return err
		}
		return nil
	}
	return rec(full)
}

// build turns the best picks into a solution.
func (s *searcher) build() *Result {
	sol := model.NewSolution(s.p)
	next := make([]int, len(s.classes))
	for _, pk := range s.bestPicks {
		v := s.classes[pk.class].members[next[pk.class]]
		next[pk.class]++
		sol.Routes[v] = slices.Clone(s.cache[[2]int{pk.mask, pk.class}][pk.prof].seq)
	}
	sol.Unassigned = slices.Clone(s.bestUnserved)
	slices.Sort(sol.Unassigned)
	sol.Recompute(s.ev)
	return &Result{Solution: sol, Evaluations: s.evals, Classes: len(s.classes)}
}
