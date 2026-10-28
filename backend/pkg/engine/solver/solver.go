// Package solver builds and improves vehicle routes.
//
// The method is cluster-first, route-second. Orders are grouped into
// geographic clusters, each cluster is assigned as a whole to the vehicle that
// serves it best, and routes are built by cheapest insertion. A large
// neighbourhood search (ruin and recreate, run on several workers) and a local
// search (relocate, 2-opt) then refine the result across cluster borders.
//
// Solutions are compared lexicographically in the priority order the plan
// lists its objectives, via model.Problem.Less.
package solver

import (
	"cmp"
	"context"
	"math/rand/v2"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/engine/model"
)

// Options control a solve.
type Options struct {
	Seed       uint64
	Iterations int           // improvement iterations per worker; defaults to 1500
	Workers    int           // parallel searches; defaults to min(4, CPUs)
	TimeLimit  time.Duration // optional wall-clock cap on the improvement phase
	// Threshold is the relative worsening of a soft objective (cost, distance,
	// durations) accepted at the start of the search, shrinking linearly to
	// zero. It lets the search leave local optima. Default 0.2%; negative
	// disables it.
	Threshold float64
	// SkipFleetReduction disables the attempts to drop a whole route; it exists
	// for experiments that measure what that step contributes.
	SkipFleetReduction bool
}

func (o Options) withDefaults() Options {
	if o.Iterations <= 0 {
		o.Iterations = 1500
	}
	if o.Workers <= 0 {
		o.Workers = min(4, runtime.NumCPU())
	}
	if o.Threshold == 0 {
		o.Threshold = 0.002
	}
	return o
}

// Stats describes how a solve went.
type Stats struct {
	Iterations int
	Workers    int
	Elapsed    time.Duration
	Initial    model.Metrics // after construction, before improvement
	Final      model.Metrics
	// Truncated reports that the search was cut short by cancellation or by the
	// time limit, so the result depends on the machine's speed and is not
	// reproducible from the seed alone.
	Truncated bool
}

// shared holds read-only data derived once per problem.
type shared struct {
	p *model.Problem
	// relate[a*n+b] is the road travel time in seconds between the closest
	// anchor locations of orders a and b; it drives related-order removal.
	relate []int32
	n      int
	// depotDist[o] is the mean travel time from the first vehicle's start to
	// the anchors of order o; it orders clusters and orders far-first.
	depotDist []float64
	// threshold is the starting relative worsening of a soft objective that the
	// search still accepts; it shrinks to zero over the run.
	threshold float64
	// near[node] lists the nodes closest to it by travel time. On long routes
	// only positions next to these are tried when inserting, which turns the
	// cubic cost of exhaustive insertion into something close to linear.
	near [][]int32
	// class[v] names the class of identical vehicles that v belongs to.
	class []int
	// done is closed when the caller gives up; nil when it never will.
	done <-chan struct{}
}

// cancelled reports whether the caller has given up on the solve.
func (sh *shared) cancelled() bool {
	select {
	case <-sh.done:
		return true
	default:
		return false
	}
}

const (
	// pruneAbove is the route length from which insertion positions are pruned.
	pruneAbove = 14
	// nearCount is how many neighbours define the allowed positions.
	nearCount = 8
)

func newShared(ctx context.Context, p *model.Problem, opt Options) *shared {
	n := len(p.Orders)
	sh := &shared{p: p, n: n, threshold: opt.Threshold, relate: make([]int32, n*n), depotDist: make([]float64, n), class: p.VehicleClasses(), done: ctx.Done()}
	mat := p.Mats[0]
	sh.near = nearestNodes(p, mat)
	anchors := make([][]int, n)
	for o := range n {
		anchors[o] = p.OrderAnchors(o)
		if len(anchors[o]) == 0 {
			continue
		}
		var sum float64
		for _, l := range anchors[o] {
			sum += float64(mat.TravelTime(p.Vehicles[0].Start, l))
		}
		sh.depotDist[o] = sum / float64(len(anchors[o]))
	}
	for a := range n {
		for b := a + 1; b < n; b++ {
			best := int64(0)
			if len(anchors[a]) > 0 && len(anchors[b]) > 0 {
				best = model.Inf
				for _, la := range anchors[a] {
					for _, lb := range anchors[b] {
						best = min(best, mat.TravelTime(la, lb), mat.TravelTime(lb, la))
					}
				}
			}
			sh.relate[a*n+b], sh.relate[b*n+a] = int32(best), int32(best)
		}
	}
	return sh
}

// big reports whether the problem is large enough that per-move cost matters
// more than thoroughness: smaller neighbourhoods and fewer full sweeps.
func (sh *shared) big() bool { return sh.n > 60 }

// diversify reports whether workers explore other basins from random starts
// and restarts. A search from the cluster-based start gets stuck in one basin;
// the extra workers are what escape it.
func (sh *shared) diversify() bool { return sh.n <= 60 }

// nearestNodes computes, for every stop, the closest other stops by travel time.
func nearestNodes(p *model.Problem, mat *model.Matrix) [][]int32 {
	type cand struct {
		node int32
		t    int64
	}
	nn := len(p.Nodes)
	out := make([][]int32, nn)
	cands := make([]cand, 0, nn)
	for a := range nn {
		cands = cands[:0]
		for b := range nn {
			if a == b {
				continue
			}
			la, lb := p.Nodes[a].Loc, p.Nodes[b].Loc
			cands = append(cands, cand{int32(b), min(mat.TravelTime(la, lb), mat.TravelTime(lb, la))})
		}
		slices.SortFunc(cands, func(x, y cand) int { return cmp.Compare(x.t, y.t) })
		k := min(nearCount, len(cands))
		out[a] = make([]int32, k)
		for i := range k {
			out[a][i] = cands[i].node
		}
	}
	return out
}

// Solve builds a solution from the given clustering (orderCluster[o] is the
// cluster of order o; nil means one big cluster) and improves it.
func Solve(p *model.Problem, orderCluster []int, opt Options) (*model.Solution, Stats) {
	return SolveContext(context.Background(), p, orderCluster, opt)
}

// SolveContext is Solve that stops early, keeping the best solution found so
// far, once ctx is done. The result is always a complete, evaluated solution.
func SolveContext(ctx context.Context, p *model.Problem, orderCluster []int, opt Options) (*model.Solution, Stats) {
	opt = opt.withDefaults()
	start := time.Now()
	sh := newShared(ctx, p, opt)
	initial := sh.newWorker(opt.Seed).construct(orderCluster)
	return sh.run(initial, opt, start, false)
}

// Refine continues the search from an existing solution instead of building a
// new one: every worker starts from it, so the result is never worse. It is
// how a good solution is polished further, for instance after several
// independent attempts have shown which one is best.
func Refine(p *model.Problem, from *model.Solution, opt Options) (*model.Solution, Stats) {
	return RefineContext(context.Background(), p, from, opt)
}

// RefineContext is Refine that stops early once ctx is done.
func RefineContext(ctx context.Context, p *model.Problem, from *model.Solution, opt Options) (*model.Solution, Stats) {
	opt = opt.withDefaults()
	start := time.Now()
	sh := newShared(ctx, p, opt)
	return sh.run(from.Clone(), opt, start, true)
}

// run improves initial on parallel workers. With warm set, every worker
// starts from initial; otherwise the extra workers on small and medium
// problems start from random solutions to explore other basins.
func (sh *shared) run(initial *model.Solution, opt Options, start time.Time, warm bool) (*model.Solution, Stats) {
	p := sh.p
	stats := Stats{Workers: opt.Workers, Initial: initial.M}

	// With a time limit, the main search gets two fifths of it and the rest is
	// kept for fleet reduction, which would otherwise be starved.
	var deadline, mainDeadline time.Time
	if opt.TimeLimit > 0 {
		deadline = start.Add(opt.TimeLimit)
		mainDeadline = start.Add(opt.TimeLimit * 2 / 5)
	}

	results := make([]*model.Solution, opt.Workers)
	iters := make([]int, opt.Workers)
	var wg sync.WaitGroup
	for i := range opt.Workers {
		wg.Go(func() {
			w := sh.newWorker(opt.Seed*1000003 + uint64(i) + 1)
			from := initial
			// Big problems keep the given start: a short search could not
			// rebuild what it was handed, and the cluster-based start is good.
			if !warm && i > 0 && sh.diversify() {
				from = w.randomStart()
			}
			results[i], iters[i] = w.improve(from, opt.Iterations, mainDeadline)
			// When fixed costs or distance make every vehicle expensive, the
			// big win is dropping one altogether, which small moves cannot do.
			if !opt.SkipFleetReduction {
				results[i] = w.reduceFleet(results[i], max(200, opt.Iterations/3), deadline)
			}
		})
	}
	wg.Wait()

	best := initial
	for _, r := range results {
		if r != nil && p.Less(&r.M, &best.M) {
			best = r
		}
	}
	for _, n := range iters {
		stats.Iterations += n
	}
	stats.Elapsed = time.Since(start)
	stats.Final = best.M
	stats.Truncated = sh.cancelled() || (!deadline.IsZero() && time.Now().After(deadline))
	return best, stats
}

// reduceFleet tries to serve everything with one vehicle fewer. It empties a
// small route into the pool of unserved orders and lets the large neighbourhood
// search work that pool down: the plan's first objective is serving every
// stop, so the search is pulled toward placing them all again, and if it
// succeeds the result has one route less. The attempt is kept only if it is
// better under the plan's objectives; otherwise the best solution so far stays.
// It stops at the first failed round of attempts.
func (w *worker) reduceFleet(best *model.Solution, iters int, deadline time.Time) *model.Solution {
	p := w.p
	const attemptsPerRound = 3
	for {
		if w.cancelled() || (!deadline.IsZero() && time.Now().After(deadline)) {
			return best
		}
		// Used routes, smallest first: the cheapest ones to give up.
		type used struct{ route, orders int }
		var routes []used
		for r, route := range best.Routes {
			if len(route) == 0 {
				continue
			}
			seen := map[int]bool{}
			for _, n := range route {
				seen[p.Nodes[n].Order] = true
			}
			routes = append(routes, used{r, len(seen)})
		}
		if len(routes) < 2 {
			return best
		}
		slices.SortStableFunc(routes, func(a, b used) int { return cmp.Compare(a.orders, b.orders) })

		improved := false
		for _, u := range routes[:min(attemptsPerRound, len(routes))] {
			start := best.Clone()
			var orders []int
			seen := map[int]bool{}
			for _, n := range start.Routes[u.route] {
				if o := p.Nodes[n].Order; !seen[o] {
					seen[o] = true
					orders = append(orders, o)
				}
			}
			w.removeOrders(start, orders)
			r, _ := w.improve(start, iters, deadline)
			if len(r.Unassigned) == 0 && r.Feasible && p.Less(&r.M, &best.M) {
				best, improved = r, true
				break
			}
		}
		if !improved {
			return best
		}
	}
}

// worker owns the mutable scratch state of one search thread.
type worker struct {
	*shared
	ev    *model.Evaluator
	rng   *rand.Rand
	tmp   []int
	evbuf []model.RouteEval
	pos   []int32 // node -> index in the route being extended, -1 when absent
	// triedEmpty marks vehicle classes whose empty route was already tried.
	triedEmpty []bool
}

func (sh *shared) newWorker(seed uint64) *worker {
	return &worker{
		shared:     sh,
		ev:         model.NewEvaluator(sh.p),
		rng:        rand.New(rand.NewPCG(seed, 0x9e3779b97f4a7c15)),
		evbuf:      make([]model.RouteEval, len(sh.p.Vehicles)),
		pos:        slices.Repeat([]int32{-1}, len(sh.p.Nodes)),
		triedEmpty: make([]bool, len(sh.p.Vehicles)),
	}
}

// positions lists the insertion indexes worth trying for node n in base: every
// index when the route is short, otherwise the two route ends and the slots
// next to the stops closest to n.
func (w *worker) positions(n int, base []int) []int {
	if len(base) <= pruneAbove {
		all := make([]int, len(base)+1)
		for i := range all {
			all[i] = i
		}
		return all
	}
	for i, b := range base {
		w.pos[b] = int32(i)
	}
	out := []int{0, len(base)}
	for _, nb := range w.near[n] {
		if i := w.pos[nb]; i >= 0 {
			out = append(out, int(i), int(i)+1)
		}
	}
	for _, b := range base {
		w.pos[b] = -1
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// placement is the best way found to put an order into a route.
type placement struct {
	route int
	nodes []int // the resulting route
	ev    model.RouteEval
	m     model.Metrics // metrics of the whole solution after insertion
}

// insertionVariants lists the node sequences in which an order's stops may be
// inserted: pickups before deliveries, or the listed order when sequential.
func insertionVariants(ord *model.Order, nodes []model.Node) [][]int {
	seq := slices.Clone(ord.Nodes)
	if !ord.Sequential {
		slices.SortStableFunc(seq, func(a, b int) int { return cmp.Compare(nodes[a].Kind, nodes[b].Kind) })
	}
	out := [][]int{seq}
	if !ord.Sequential && len(seq) == 2 && nodes[seq[0]].Kind == nodes[seq[1]].Kind {
		out = append(out, []int{seq[1], seq[0]})
	}
	return out
}

// bestInsertion finds the lexicographically best feasible way to insert the
// currently unassigned order o into any route (or only route `only` when >= 0).
func (w *worker) bestInsertion(s *model.Solution, o, only int) (placement, bool) {
	p := w.p
	ord := &p.Orders[o]
	unserved := s.M[model.ObjUnserved] - int64(len(ord.Nodes))
	var best placement
	found := false

	consider := func(r int, route []int) {
		ev := w.ev.Eval(r, route)
		if !ev.OK {
			return
		}
		copy(w.evbuf, s.Evals)
		w.evbuf[r] = ev
		m, ok := p.Aggregate(w.evbuf, unserved)
		if !ok {
			return
		}
		if !found || p.Less(&m, &best.m) {
			best.route, best.ev, best.m = r, ev, m
			best.nodes = append(best.nodes[:0], route...)
			found = true
		}
	}

	clear(w.triedEmpty)
	for r := range s.Routes {
		if only >= 0 && r != only {
			continue
		}
		base := s.Routes[r]
		// Empty routes of identical vehicles are interchangeable: try one.
		if only < 0 && len(base) == 0 {
			if w.triedEmpty[w.class[r]] {
				continue
			}
			w.triedEmpty[w.class[r]] = true
		}
		for _, seq := range insertionVariants(ord, p.Nodes) {
			switch len(seq) {
			case 1:
				for _, i := range w.positions(seq[0], base) {
					w.tmp = append(append(append(w.tmp[:0], base[:i]...), seq[0]), base[i:]...)
					consider(r, w.tmp)
				}
			case 2:
				first := w.positions(seq[0], base)
				second := w.positions(seq[1], base)
				try := func(i, j int) {
					w.tmp = append(w.tmp[:0], base[:i]...)
					w.tmp = append(w.tmp, seq[0])
					w.tmp = append(w.tmp, base[i:j]...)
					w.tmp = append(w.tmp, seq[1])
					w.tmp = append(w.tmp, base[j:]...)
					consider(r, w.tmp)
				}
				for _, i := range first {
					// The second stop may always directly follow the first.
					if _, ok := slices.BinarySearch(second, i); !ok {
						try(i, i)
					}
					for _, j := range second {
						if j >= i {
							try(i, j)
						}
					}
				}
			default:
				w.insertGreedy(s, r, seq, base, consider)
			}
		}
	}
	return best, found
}

// insertGreedy places the nodes of a long order one after another, each at its
// best position after the previous one. It is an approximation for orders with
// more than two stops, where trying every combination is too expensive.
func (w *worker) insertGreedy(s *model.Solution, r int, seq, base []int, consider func(int, []int)) {
	cur := slices.Clone(base)
	minPos := 0
	for k, n := range seq {
		bestPos, found := -1, false
		var bestM model.Metrics
		for i := minPos; i <= len(cur); i++ {
			w.tmp = append(append(append(w.tmp[:0], cur[:i]...), n), cur[i:]...)
			ev := w.ev.Eval(r, w.tmp)
			if !ev.OK {
				continue
			}
			copy(w.evbuf, s.Evals)
			w.evbuf[r] = ev
			m, _ := w.p.Aggregate(w.evbuf, 0)
			if !found || w.p.Less(&m, &bestM) {
				bestPos, bestM, found = i, m, true
			}
		}
		if !found {
			return
		}
		cur = slices.Insert(cur, bestPos, n)
		minPos = bestPos + 1
		if k == len(seq)-1 {
			consider(r, cur)
		}
	}
}

// apply commits a placement for order o into s.
func (w *worker) apply(s *model.Solution, o int, pl placement) {
	s.Routes[pl.route] = slices.Clone(pl.nodes)
	s.Evals[pl.route] = pl.ev
	if i, ok := slices.BinarySearch(s.Unassigned, o); ok {
		s.Unassigned = slices.Delete(s.Unassigned, i, i+1)
	}
	s.M, s.Feasible = pl.m, true
}

// removeOrders takes the given orders out of their routes and marks them unassigned.
func (w *worker) removeOrders(s *model.Solution, orders []int) {
	drop := make([]bool, len(w.p.Orders))
	for _, o := range orders {
		drop[o] = true
	}
	for r, route := range s.Routes {
		touched := false
		for _, n := range route {
			if drop[w.p.Nodes[n].Order] {
				touched = true
				break
			}
		}
		if !touched {
			continue
		}
		kept := make([]int, 0, len(route))
		for _, n := range route {
			if !drop[w.p.Nodes[n].Order] {
				kept = append(kept, n)
			}
		}
		s.Routes[r] = kept
		s.Evals[r] = w.ev.Eval(r, kept)
	}
	for _, o := range orders {
		if _, ok := slices.BinarySearch(s.Unassigned, o); !ok {
			s.Unassigned = append(s.Unassigned, o)
		}
	}
	slices.Sort(s.Unassigned)
	s.RefreshMetrics()
}

// recreate reinserts the pending orders, greedily best-first or in random order.
func (w *worker) recreate(s *model.Solution, pending []int, greedy bool) {
	pending = slices.Clone(pending)
	if !greedy {
		w.rng.Shuffle(len(pending), func(i, j int) { pending[i], pending[j] = pending[j], pending[i] })
		for _, o := range pending {
			if pl, ok := w.bestInsertion(s, o, -1); ok {
				w.apply(s, o, pl)
			}
		}
		return
	}
	for len(pending) > 0 {
		bestIdx := -1
		var bestPl placement
		for i, o := range pending {
			pl, ok := w.bestInsertion(s, o, -1)
			if ok && (bestIdx < 0 || w.p.Less(&pl.m, &bestPl.m)) {
				bestIdx, bestPl = i, pl
			}
		}
		if bestIdx < 0 {
			return
		}
		w.apply(s, pending[bestIdx], bestPl)
		pending = slices.Delete(pending, bestIdx, bestIdx+1)
	}
}

// randomStart builds a solution by inserting all orders in random order, each
// at its best position. It is a cheap way to land in a different basin.
func (w *worker) randomStart() *model.Solution {
	s := model.NewSolution(w.p)
	pending := slices.Clone(s.Unassigned)
	w.recreate(s, pending, false)
	return s
}

// recreateGroup puts the pending orders into one route together, trying every
// route (one empty route per vehicle class), and keeps the best result. One at
// a time, an order may look cheaper in a busy route, while the whole group
// would be better off in a route of its own; this is the move that finds that.
// An order that no route can take (for instance one nobody has the skills for)
// is skipped rather than spoiling the attempt; orders a route could not absorb
// are afterwards offered to the other routes.
func (w *worker) recreateGroup(s *model.Solution) {
	pending := slices.Clone(s.Unassigned)
	slices.SortStableFunc(pending, func(a, b int) int { return cmp.Compare(w.depotDist[b], w.depotDist[a]) })

	var best *model.Solution
	tried := make([]bool, len(w.p.Vehicles))
	candidates := w.groupCandidates(s, pending)
	for r := range s.Routes {
		if candidates != nil && !candidates[r] {
			continue
		}
		if len(s.Routes[r]) == 0 {
			if tried[w.class[r]] {
				continue
			}
			tried[w.class[r]] = true
		}
		trial := s.Clone()
		for _, o := range pending {
			if pl, found := w.bestInsertion(trial, o, r); found {
				w.apply(trial, o, pl)
			}
		}
		// Inserting one order at a time is myopic: the first fixes positions
		// that block the next. Letting the group's orders re-pick their places
		// within this route repairs that.
		w.relocateSome(trial, pending, r)
		if best == nil || w.p.Less(&trial.M, &best.M) {
			best = trial
		}
	}
	if best == nil {
		return
	}
	if len(best.Unassigned) > 0 {
		w.recreate(best, best.Unassigned, true)
	}
	*s = *best
}

// groupCandidates limits, on big problems, the routes a group is tried in to
// those serving the orders closest to the group's first member (plus every
// empty route). It returns nil, meaning "all routes", for small problems.
func (w *worker) groupCandidates(s *model.Solution, pending []int) []bool {
	if !w.big() || len(pending) == 0 {
		return nil
	}
	seed := pending[0]
	type near struct {
		order int
		t     int32
	}
	var list []near
	ov := s.OrderVehicle()
	for o, v := range ov {
		if v >= 0 {
			list = append(list, near{o, w.relate[seed*w.n+o]})
		}
	}
	slices.SortFunc(list, func(a, b near) int { return cmp.Compare(a.t, b.t) })
	ok := make([]bool, len(s.Routes))
	for _, nr := range list[:min(4, len(list))] {
		ok[ov[nr.order]] = true
	}
	for r := range s.Routes {
		if len(s.Routes[r]) == 0 {
			ok[r] = true
		}
	}
	return ok
}

// assignedOrders lists orders currently in some route.
func assignedOrders(s *model.Solution) []int {
	out := make([]int, 0, len(s.P.Orders))
	for o, v := range s.OrderVehicle() {
		if v >= 0 {
			out = append(out, o)
		}
	}
	return out
}
