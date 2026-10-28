package solver

import (
	"cmp"
	"math"
	"slices"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/engine/model"
)

// construct builds the starting solution cluster by cluster. Clusters are
// processed farthest-first; each is offered whole to every vehicle, and the
// vehicle that yields the best solution keeps it. A cluster that no single
// vehicle can absorb is split and inserted order by order.
func (w *worker) construct(orderCluster []int) *model.Solution {
	p := w.p
	s := model.NewSolution(p)

	k := 1
	cl := make([]int, len(p.Orders))
	if orderCluster != nil {
		copy(cl, orderCluster)
		for _, c := range cl {
			k = max(k, c+1)
		}
	}
	members := make([][]int, k)
	for o, c := range cl {
		members[c] = append(members[c], o)
	}

	farness := func(orders []int) float64 {
		var sum float64
		for _, o := range orders {
			sum += w.depotDist[o]
		}
		return sum / float64(max(1, len(orders)))
	}
	ids := make([]int, 0, k)
	for c := range members {
		if len(members[c]) > 0 {
			ids = append(ids, c)
		}
	}
	slices.SortStableFunc(ids, func(a, b int) int { return cmp.Compare(farness(members[b]), farness(members[a])) })

	for _, c := range ids {
		orders := slices.Clone(members[c])
		slices.SortStableFunc(orders, func(a, b int) int { return cmp.Compare(w.depotDist[b], w.depotDist[a]) })

		// Offer the whole cluster to each vehicle. An order a vehicle cannot
		// take is skipped (it may fit elsewhere, or nowhere at all), and the
		// vehicle that ends up serving the most, then best by the plan's
		// objectives, wins.
		var best *model.Solution
		tried := make([]bool, len(p.Vehicles))
		for v := range p.Vehicles {
			if w.cancelled() {
				break
			}
			if len(s.Routes[v]) == 0 {
				if tried[w.class[v]] {
					continue // an identical empty vehicle was already tried
				}
				tried[w.class[v]] = true
			}
			trial := s.Clone()
			for _, o := range orders {
				if pl, found := w.bestInsertion(trial, o, v); found {
					w.apply(trial, o, pl)
				}
			}
			if best == nil || p.Less(&trial.M, &best.M) {
				best = trial
			}
		}
		if best != nil {
			s = best
		}
		// Whatever the chosen vehicle could not take may fit another one.
		for _, o := range orders {
			if _, open := slices.BinarySearch(s.Unassigned, o); !open {
				continue
			}
			if pl, found := w.bestInsertion(s, o, -1); found {
				w.apply(s, o, pl)
			}
		}
	}
	return s
}

// improve runs a large neighbourhood search from start and returns the best
// solution it found together with the number of iterations performed. start is
// left untouched.
func (w *worker) improve(start *model.Solution, iters int, deadline time.Time) (*model.Solution, int) {
	p := w.p
	cur := start.Clone()
	w.descend(cur, w.passes(3))
	best := cur.Clone()

	stagnation, it := 0, 0
	for ; it < iters; it++ {
		if it%8 == 0 && (w.cancelled() || (!deadline.IsZero() && time.Now().After(deadline))) {
			break
		}
		cand := cur.Clone()
		changed := w.ruin(cand)
		if changed == nil {
			break
		}
		switch mode := w.rng.IntN(3); {
		case mode == 0 && len(changed) <= greedyLimit:
			w.recreate(cand, cand.Unassigned, true)
		case mode == 2:
			w.recreateGroup(cand)
		default:
			w.recreate(cand, cand.Unassigned, false)
		}

		// Accept sideways moves too, and slightly worse ones while the
		// threshold is still wide: plateaus are common when high-priority
		// objectives are flat, and crossing them is how the search escapes.
		thr := 0.0
		if w.threshold > 0 {
			thr = w.threshold * (1 - float64(it)/float64(max(1, iters)))
		}
		if cand.Feasible && w.accept(&cur.M, &cand.M, thr) {
			cur = cand
		}
		if p.Less(&cur.M, &best.M) {
			// Only the orders just moved can have found a better place, so
			// polish those; a full local search on every new best would
			// dominate the running time on big problems.
			w.polish(cur, changed)
			best = cur.Clone()
			stagnation = 0
		} else if stagnation++; stagnation >= 150 {
			// Return to the best known solution, or on small problems half the
			// time start over from scratch to leave its basin.
			if w.diversify() && w.rng.IntN(2) == 0 {
				cur = w.randomStart()
			} else {
				cur = best.Clone()
			}
			stagnation = 0
		}
	}
	w.descend(best, w.passes(4))
	if w.resequence(best) {
		w.descend(best, 1)
	}
	return best, it
}

// accept decides whether to move from cur to cand. Anything not worse is
// accepted. A worse candidate is accepted only if the first objective in which
// it differs is a soft one, and the worsening is within thr of the current value.
func (w *worker) accept(cur, cand *model.Metrics, thr float64) bool {
	for _, o := range w.p.Objectives {
		a, b := cand[o], cur[o]
		switch {
		case a == b:
			continue
		case a < b:
			return true
		case o == model.ObjUnserved || o == model.ObjUnsatPref || thr <= 0:
			return false
		default:
			return float64(a-b) <= thr*math.Max(1, math.Abs(float64(b)))
		}
	}
	return true
}

// ruin removes a group of orders chosen by one of four strategies and returns
// them, or nil when there was nothing to remove.
func (w *worker) ruin(s *model.Solution) []int {
	assigned := assignedOrders(s)
	n := len(assigned)
	if n == 0 {
		return nil
	}
	// Big problems remove fewer orders per step: reinserting each costs more.
	maxK := min(max(4, n/3), 12)
	if w.big() {
		maxK = min(maxK, 8)
	}
	k := min(1+w.rng.IntN(maxK), n)

	var picked []int
	switch w.rng.IntN(4) {
	case 0:
		w.rng.Shuffle(n, func(i, j int) { assigned[i], assigned[j] = assigned[j], assigned[i] })
		picked = assigned[:k]
	case 1:
		picked = w.relatedRemoval(assigned, k)
	case 2:
		picked = w.worstRemoval(s, assigned, k)
	default:
		picked = w.routeRemoval(s, assigned, k)
	}
	w.removeOrders(s, picked)
	return picked
}

// greedyLimit is the largest group reinserted best-first; beyond it the cost
// grows with the square of the group and random order is used instead.
const greedyLimit = 6

// passes is the number of local search passes: big problems get one fewer
// full sweep, since each sweep touches every order.
func (w *worker) passes(n int) int {
	if !w.big() {
		return n
	}
	return max(1, n/3)
}

// polish improves the placement of just the given orders: each is moved to its
// best position, then the routes that changed get 2-opt.
func (w *worker) polish(s *model.Solution, orders []int) {
	w.relocateSome(s, orders, -1)
	seen := map[int]bool{}
	var routes []int
	for _, o := range orders {
		if r := vehicleOf(s, o); r >= 0 && !seen[r] {
			seen[r] = true
			routes = append(routes, r)
		}
	}
	w.twoOpt(s, routes)
}

// skewedPick returns an index in [0,n) biased toward 0; power controls the bias.
func (w *worker) skewedPick(n int, power float64) int {
	return min(n-1, int(math.Pow(w.rng.Float64(), power)*float64(n)))
}

// relatedRemoval removes a random order and the orders closest to it.
func (w *worker) relatedRemoval(assigned []int, k int) []int {
	seed := assigned[w.rng.IntN(len(assigned))]
	rest := slices.DeleteFunc(slices.Clone(assigned), func(o int) bool { return o == seed })
	slices.SortFunc(rest, func(a, b int) int {
		return cmp.Compare(w.relate[seed*w.n+a], w.relate[seed*w.n+b])
	})
	picked := []int{seed}
	for len(picked) < k && len(rest) > 0 {
		i := w.skewedPick(len(rest), 4)
		picked = append(picked, rest[i])
		rest = slices.Delete(rest, i, i+1)
	}
	return picked
}

// worstRemoval removes orders whose absence improves the solution the most.
func (w *worker) worstRemoval(s *model.Solution, assigned []int, k int) []int {
	p := w.p
	type scored struct {
		order int
		m     model.Metrics
	}
	ov := s.OrderVehicle()
	list := make([]scored, 0, len(assigned))
	for _, o := range assigned {
		r := ov[o]
		w.tmp = w.tmp[:0]
		for _, n := range s.Routes[r] {
			if p.Nodes[n].Order != o {
				w.tmp = append(w.tmp, n)
			}
		}
		copy(w.evbuf, s.Evals)
		w.evbuf[r] = w.ev.Eval(r, w.tmp)
		m, _ := p.Aggregate(w.evbuf, s.M[model.ObjUnserved]+int64(len(p.Orders[o].Nodes)))
		list = append(list, scored{o, m})
	}
	slices.SortFunc(list, func(a, b scored) int { return p.Compare(&a.m, &b.m) })
	var picked []int
	for len(picked) < k && len(list) > 0 {
		i := w.skewedPick(len(list), 3)
		picked = append(picked, list[i].order)
		list = slices.Delete(list, i, i+1)
	}
	return picked
}

// routeRemoval empties a random used route, which lets the search consolidate
// or redistribute whole vehicles. Falls back to random removal for huge routes.
func (w *worker) routeRemoval(s *model.Solution, assigned []int, k int) []int {
	var used []int
	for r, route := range s.Routes {
		if len(route) > 0 {
			used = append(used, r)
		}
	}
	// Sometimes two routes at once, so their orders can be merged into one.
	chosen := []int{used[w.rng.IntN(len(used))]}
	if len(used) > 1 && w.rng.IntN(2) == 0 {
		for {
			if r := used[w.rng.IntN(len(used))]; r != chosen[0] {
				chosen = append(chosen, r)
				break
			}
		}
	}
	ov := s.OrderVehicle()
	var picked []int
	for _, o := range assigned {
		if slices.Contains(chosen, ov[o]) {
			picked = append(picked, o)
		}
	}
	if len(picked) > 3*k+6 {
		w.rng.Shuffle(len(picked), func(i, j int) { picked[i], picked[j] = picked[j], picked[i] })
		picked = picked[:k]
	}
	return picked
}

// descend applies relocate and 2-opt moves until no move improves the solution.
func (w *worker) descend(s *model.Solution, maxPasses int) {
	for range maxPasses {
		if w.cancelled() {
			return
		}
		improved := w.relocateAll(s)
		if w.twoOptAll(s) {
			improved = true
		}
		if w.swapRoutes(s) {
			improved = true
		}
		if w.exchangeAll(s) {
			improved = true
		}
		if !improved {
			return
		}
	}
}

// maxResequence is the longest route, in stops, whose visiting order is tried exhaustively.
const maxResequence = 8

// resequence puts every short route in its best visiting order by trying all the
// orders the stops allow. Local search reaches such an order only through a run of
// moves that each look worse; on a route this short the exhaustive check is cheap
// and leaves no better order of the same stops behind.
func (w *worker) resequence(s *model.Solution) bool {
	improved := false
	for r, route := range s.Routes {
		if len(route) >= 3 && len(route) <= maxResequence && !w.cancelled() && w.resequenceRoute(s, r) {
			improved = true
		}
	}
	return improved
}

func (w *worker) resequenceRoute(s *model.Solution, r int) bool {
	p := w.p
	nodes := slices.Clone(s.Routes[r])
	seq := make([]int, 0, len(nodes))
	used := make([]bool, len(nodes))
	placed := map[int]int{}  // order -> stops of it already in seq
	pickups := map[int]int{} // order -> pickups of it already in seq
	improved := false
	bestRoute, bestEval, bestM := s.Routes[r], s.Evals[r], s.M

	// allowed says whether node n may come next given what is already placed.
	allowed := func(n int) bool {
		node := &p.Nodes[n]
		ord := &p.Orders[node.Order]
		if ord.Sequential {
			return node.Seq == placed[node.Order]
		}
		return node.Kind == model.Pickup || pickups[node.Order] >= ord.Pickups
	}

	var walk func()
	walk = func() {
		if len(seq) == len(nodes) {
			ev := w.ev.Eval(r, seq)
			if !ev.OK {
				return
			}
			copy(w.evbuf, s.Evals)
			w.evbuf[r] = ev
			if m, ok := p.Aggregate(w.evbuf, s.M[model.ObjUnserved]); ok && p.Less(&m, &bestM) {
				bestRoute, bestEval, bestM = slices.Clone(seq), ev, m
				improved = true
			}
			return
		}
		for i, n := range nodes {
			if used[i] || !allowed(n) {
				continue
			}
			node := &p.Nodes[n]
			used[i] = true
			placed[node.Order]++
			if node.Kind == model.Pickup {
				pickups[node.Order]++
			}
			seq = append(seq, n)
			walk()
			seq = seq[:len(seq)-1]
			if node.Kind == model.Pickup {
				pickups[node.Order]--
			}
			placed[node.Order]--
			used[i] = false
		}
	}
	walk()
	if improved {
		s.Routes[r], s.Evals[r], s.M = bestRoute, bestEval, bestM
	}
	return improved
}

// relocateAll tries to move every order to its best position anywhere.
func (w *worker) relocateAll(s *model.Solution) bool {
	orders := assignedOrders(s)
	w.rng.Shuffle(len(orders), func(i, j int) { orders[i], orders[j] = orders[j], orders[i] })
	return w.relocateSome(s, orders, -1)
}

// relocateSome tries to move each of the given orders to its best position,
// anywhere or, when only >= 0, only within that route.
func (w *worker) relocateSome(s *model.Solution, orders []int, only int) bool {
	p := w.p
	improved := false
	for _, o := range orders {
		before, feasible := s.M, s.Feasible
		r0 := vehicleOf(s, o)
		if r0 < 0 || (only >= 0 && r0 != only) {
			continue
		}
		oldRoute, oldEval := s.Routes[r0], s.Evals[r0]

		w.removeOrders(s, []int{o})
		if pl, ok := w.bestInsertion(s, o, only); ok && p.Less(&pl.m, &before) {
			w.apply(s, o, pl)
			improved = true
			continue
		}
		// Not better: put everything back exactly as it was.
		s.Routes[r0], s.Evals[r0] = oldRoute, oldEval
		if i, ok := slices.BinarySearch(s.Unassigned, o); ok {
			s.Unassigned = slices.Delete(s.Unassigned, i, i+1)
		}
		s.M, s.Feasible = before, feasible
	}
	return improved
}

func vehicleOf(s *model.Solution, o int) int {
	for r, route := range s.Routes {
		for _, n := range route {
			if s.P.Nodes[n].Order == o {
				return r
			}
		}
	}
	return -1
}

// twoOptAll reverses route segments wherever that improves the solution.
func (w *worker) twoOptAll(s *model.Solution) bool {
	all := make([]int, len(s.Routes))
	for r := range all {
		all[r] = r
	}
	return w.twoOpt(s, all)
}

// twoOpt reverses segments of the given routes wherever that improves the solution.
func (w *worker) twoOpt(s *model.Solution, routes []int) bool {
	p := w.p
	improved := false
	for _, r := range routes {
		for again := true; again; {
			again = false
			route := s.Routes[r]
		search:
			for i := 0; i < len(route)-1; i++ {
				for j := i + 1; j < len(route); j++ {
					w.tmp = append(w.tmp[:0], route...)
					slices.Reverse(w.tmp[i : j+1])
					ev := w.ev.Eval(r, w.tmp)
					if !ev.OK {
						continue
					}
					copy(w.evbuf, s.Evals)
					w.evbuf[r] = ev
					m, ok := p.Aggregate(w.evbuf, s.M[model.ObjUnserved])
					if ok && p.Less(&m, &s.M) {
						s.Routes[r], s.Evals[r], s.M = slices.Clone(w.tmp), ev, m
						improved, again = true, true
						break search
					}
				}
			}
		}
	}
	return improved
}

// swapRoutes hands the route of one vehicle to another, which matters when
// vehicles differ in speed, cost, capacity or working hours. An idle vehicle
// counts: a whole route may move to a faster or cheaper one.
func (w *worker) swapRoutes(s *model.Solution) bool {
	p := w.p
	improved := false
	for a := range s.Routes {
		for b := a + 1; b < len(s.Routes); b++ {
			if w.class[a] == w.class[b] || (len(s.Routes[a]) == 0 && len(s.Routes[b]) == 0) {
				continue
			}
			ra, rb := s.Routes[a], s.Routes[b]
			ea, eb := w.ev.Eval(a, rb), w.ev.Eval(b, ra)
			if !ea.OK || !eb.OK {
				continue
			}
			copy(w.evbuf, s.Evals)
			w.evbuf[a], w.evbuf[b] = ea, eb
			m, ok := p.Aggregate(w.evbuf, s.M[model.ObjUnserved])
			if ok && p.Less(&m, &s.M) {
				s.Routes[a], s.Routes[b] = rb, ra
				s.Evals[a], s.Evals[b], s.M = ea, eb, m
				improved = true
			}
		}
	}
	return improved
}

// exchangeAll swaps pairs of orders that sit in different routes, each into
// the other's route at its best position. Large problems sample the pairs.
func (w *worker) exchangeAll(s *model.Solution) bool {
	p := w.p
	orders := assignedOrders(s)
	type pair struct{ a, b int }
	var pairs []pair
	for i, a := range orders {
		for _, b := range orders[i+1:] {
			pairs = append(pairs, pair{a, b})
		}
	}
	w.rng.Shuffle(len(pairs), func(i, j int) { pairs[i], pairs[j] = pairs[j], pairs[i] })
	limit := 300
	if w.big() {
		limit = 60
	}
	if len(pairs) > limit {
		pairs = pairs[:limit]
	}

	improved := false
	for _, pr := range pairs {
		ra, rb := vehicleOf(s, pr.a), vehicleOf(s, pr.b)
		if ra < 0 || rb < 0 || ra == rb {
			continue
		}
		before, feasible := s.M, s.Feasible
		oldA, oldB, evA, evB := s.Routes[ra], s.Routes[rb], s.Evals[ra], s.Evals[rb]

		w.removeOrders(s, []int{pr.a, pr.b})
		if pl, ok := w.bestInsertion(s, pr.a, rb); ok {
			w.apply(s, pr.a, pl)
			if pl, ok := w.bestInsertion(s, pr.b, ra); ok {
				w.apply(s, pr.b, pl)
				if p.Less(&s.M, &before) {
					improved = true
					continue
				}
			}
		}
		// Not better: restore both routes exactly.
		s.Routes[ra], s.Routes[rb], s.Evals[ra], s.Evals[rb] = oldA, oldB, evA, evB
		s.Unassigned = slices.DeleteFunc(s.Unassigned, func(o int) bool { return o == pr.a || o == pr.b })
		s.M, s.Feasible = before, feasible
	}
	return improved
}
