package solve

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/mseptiaan/loong.lat/backend/pkg/engine/analysis"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/cluster"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/geo"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/model"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/solver"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/validate"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
)

// maxClusters is the most clusters an experiment may ask for; beyond it routes get too short to be worth a cluster.
const maxClusters = 8

// experiment is a job.Experiment after normalization, with the seed it runs under and its clustering,
// which every preset shares: clustering looks at places and travel times only, never at the objectives.
type experiment struct {
	job.Experiment
	seed uint64
	once sync.Once
	cl   clustered
}

// clustered is the outcome of an experiment's clustering. orderCluster is nil when nothing was clustered.
type clustered struct {
	orderCluster []int
	k            int
	silhouette   float64
	err          error
}

// planExperiments normalizes the job's experiments: defaults filled in, parameters an algorithm ignores
// cleared, impossible ones dropped, repeats merged into the first. Seeds follow the position in the job's
// list, so adding an experiment at the end never changes what the earlier ones find. The one exception is
// the baseline (no clustering, normal effort): it always runs with the seed of the plain solve, so the search
// contains the quick quote wherever the caller lists it, and it runs first, so a search the clock cuts short
// has the quick quote in hand before anything else.
func (b *built) planExperiments() []*experiment {
	sch := b.j.Search
	if sch == nil {
		return nil
	}
	var out []*experiment
	seen := map[job.Experiment]bool{}
	for i, e := range sch.Experiments {
		if e.Effort == "" {
			e.Effort = job.EffortNormal
		}
		if e.Algorithm == job.AlgNone || e.Algorithm == job.AlgDBSCAN {
			e.K = 0 // dbscan finds its own number of clusters
		}
		if e.Algorithm != job.AlgDBSCAN {
			e.EpsMinutes = 0
		}
		if e.K < 0 || e.K > len(b.p.Orders) {
			continue // more clusters than orders cannot be asked for, and a negative k is no k
		}
		e.K = min(e.K, maxClusters)
		if seen[e] {
			continue
		}
		seen[e] = true
		seed := uint64(1 + i)
		if isBaseline(e) {
			seed = 1
		}
		out = append(out, &experiment{Experiment: e, seed: seed})
	}
	if i := slices.IndexFunc(out, func(e *experiment) bool { return isBaseline(e.Experiment) }); i > 0 {
		base := out[i]
		copy(out[1:i+1], out[:i])
		out[0] = base
	}
	return out
}

// isBaseline is the experiment that equals the plain solve of a job without a Search.
func isBaseline(e job.Experiment) bool {
	return e.Algorithm == job.AlgNone && e.Effort == job.EffortNormal
}

// cluster runs an experiment's clustering once, however many presets ask for it.
func (b *built) cluster(e *experiment) *clustered {
	e.once.Do(func() { e.cl = b.runClustering(e) })
	return &e.cl
}

func (b *built) runClustering(e *experiment) clustered {
	if e.Algorithm == job.AlgNone {
		return clustered{}
	}
	us := b.units()
	res := &cluster.Result{Algorithm: e.Algorithm, K: 1} // nothing to group: every order is loose
	if len(us.units) > 0 {
		var err error
		params := cluster.Params{Algorithm: e.Algorithm, K: e.K, Seed: e.seed, Eps: e.EpsMinutes * 60}
		if res, err = cluster.Run(cluster.Input{Units: us.units, Time: us.time}, params); err != nil {
			return clustered{err: err}
		}
	}
	orderCluster := make([]int, len(b.p.Orders))
	for o, u := range us.unitOf {
		if u >= 0 {
			orderCluster[o] = res.Assign[u]
		}
	}
	return clustered{orderCluster: orderCluster, k: res.K, silhouette: res.Silhouette}
}

// unitSet is the orders of a job as items to cluster. Every order is a unit (the problems built here have no
// global constraint that would bind several orders to one vehicle), anchored at its pickup and its drop.
type unitSet struct {
	units []cluster.Unit
	locs  [][]int // anchor locations per unit
	mat   *model.Matrix
	// unitOf maps an order to its unit, -1 for an order with no anchor away from the depots, which joins cluster 0.
	unitOf []int
}

// time is the symmetric mean road travel time between two units in seconds. It reads the first profile's
// matrix: clusters follow where places are, and the profiles differ only in speed.
func (u *unitSet) time(a, b int) float64 {
	var sum float64
	var n int
	for _, la := range u.locs[a] {
		for _, lb := range u.locs[b] {
			sum += float64(u.mat.TravelTime(la, lb)+u.mat.TravelTime(lb, la)) / 2
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

func (b *built) buildUnits() *unitSet {
	p := b.p
	proj := projectorFor(p)
	us := &unitSet{mat: p.Mats[0], unitOf: make([]int, len(p.Orders))}
	for o := range p.Orders {
		anchors := p.OrderAnchors(o)
		if len(anchors) == 0 {
			us.unitOf[o] = -1
			continue
		}
		us.unitOf[o] = len(us.units)
		unit := cluster.Unit{Orders: []int{o}}
		for _, l := range anchors {
			unit.Pts = append(unit.Pts, proj.Project(p.Locs[l]))
		}
		us.units = append(us.units, unit)
		us.locs = append(us.locs, anchors)
	}
	return us
}

// projectorFor flattens the job's places around the centre of the non-depot ones.
func projectorFor(p *model.Problem) geo.Projector {
	var pts []geo.LatLon
	for l, c := range p.Locs {
		if !p.IsDepot[l] {
			pts = append(pts, c)
		}
	}
	if len(pts) == 0 {
		pts = p.Locs
	}
	return geo.NewProjector(pts)
}

// solution turns the routes of an earlier job into a solution of q: the i-th route of a fleet type goes to the
// i-th vehicle of that type, which is a faithful mapping because the vehicles of a type are identical. It
// refuses what cannot be that job's own output: an unknown type or shipment, more routes of a type than
// vehicles, a stop twice, or a shipment with only one of its two stops.
func (b *built) solution(q *model.Problem, routes []job.Route) (*model.Solution, error) {
	s := model.NewSolution(q)
	taken := map[string]int{}
	placed := make([]int, len(q.Orders)) // stops of each order that are in a route
	visited := make([]bool, len(q.Nodes))
	for i, r := range routes {
		if len(r.Stops) == 0 {
			continue
		}
		pool := b.byType[r.Type]
		if taken[r.Type] >= len(pool) {
			return nil, fmt.Errorf("route %d: no free vehicle of type %q", i, r.Type)
		}
		v := pool[taken[r.Type]]
		taken[r.Type]++
		nodes := make([]int, 0, len(r.Stops))
		for _, st := range r.Stops {
			o, ok := b.orderOf[st.Shipment]
			if !ok {
				return nil, fmt.Errorf("route %d: unknown shipment %q", i, st.Shipment)
			}
			var n int
			switch st.Kind {
			case job.Pickup:
				n = b.pickup[o]
			case job.Drop:
				n = b.drop[o]
			default:
				return nil, fmt.Errorf("route %d: unknown stop kind %q", i, st.Kind)
			}
			if visited[n] {
				return nil, fmt.Errorf("route %d: shipment %q is visited twice for the same stop", i, st.Shipment)
			}
			visited[n] = true
			placed[o]++
			nodes = append(nodes, n)
		}
		s.Routes[v] = nodes
	}
	s.Unassigned = nil
	for o := range q.Orders {
		switch placed[o] {
		case 0:
			s.Unassigned = append(s.Unassigned, o)
		case len(q.Orders[o].Nodes):
		default:
			return nil, fmt.Errorf("shipment %q has only some of its stops in the routes", b.shipmentOf[o])
		}
	}
	s.Recompute(model.NewEvaluator(q))
	if !s.Feasible {
		return nil, fmt.Errorf("the routes are not feasible for this job")
	}
	return s, nil
}

// entry is a solution the search of a preset holds, with the clustering it came from and the trial that made it.
type entry struct {
	sol          *model.Solution
	orderCluster []int
	trial        int // index into searched.trials
	overlap      float64
	hasOverlap   bool
}

// maxOverlap is the largest territory overlap of the solution, worked out only when a tie needs it.
func (e *entry) maxOverlap(q *model.Problem) float64 {
	if !e.hasOverlap {
		e.overlap, e.hasOverlap = analysis.Diagnose(q, e.sol, e.orderCluster).MaxOverlap, true
	}
	return e.overlap
}

// better reports whether a beats c: first by the preset's objectives, then, when they tie, by how cleanly the
// vehicles' territories are separated.
func better(q *model.Problem, a, c *entry) bool {
	if cmp := q.Compare(&a.sol.M, &c.sol.M); cmp != 0 {
		return cmp < 0
	}
	return a.maxOverlap(q) < c.maxOverlap(q)-1e-9
}

// searched is what the search of one preset leaves behind.
type searched struct {
	best       *entry
	trials     []job.Trial
	solves     int // solves run, polishes included
	iterations int
	truncated  bool
	polished   bool
}

// search looks for the best plan of one preset: the incumbent, every experiment in turn, then a polish of
// the best so far. Everything runs one after the other on one worker with fixed seeds, so the result is a
// function of the job alone, unless the context cuts the search short.
func (b *built) search(ctx context.Context, q *model.Problem, preset string) (*searched, error) {
	sch := b.j.Search
	s := &searched{}
	lastFailure := "nothing was solved"

	// admit records a finished solution as a trial and lets it compete when it is valid.
	admit := func(t job.Trial, sol *model.Solution, orderCluster []int) {
		rep := validate.Check(q, sol)
		t.Valid, t.Unserved, t.Metrics, t.Score = rep.Valid, len(sol.Unassigned), metricsOf(&sol.M), score(q, &sol.M)
		s.trials = append(s.trials, t)
		if !rep.Valid {
			lastFailure = fmt.Sprint(rep.Violations)
			return
		}
		e := &entry{sol: sol, orderCluster: orderCluster, trial: len(s.trials) - 1}
		if s.best == nil || better(q, e, s.best) {
			s.best = e
		}
	}
	solved := func(st solver.Stats) {
		s.solves++
		s.iterations += st.Iterations
		s.truncated = s.truncated || st.Truncated
	}

	if routes := sch.From[preset]; len(routes) > 0 {
		if sol, err := b.solution(q, routes); err == nil {
			admit(job.Trial{Kind: job.TrialIncumbent}, sol, nil)
		} else {
			// Kept in the record so a caller sees its routes were turned down, but it cannot compete.
			s.trials = append(s.trials, job.Trial{Kind: job.TrialIncumbent})
			lastFailure = err.Error()
		}
	}

	baseline := false
	for _, e := range b.experiments {
		if ctx.Err() != nil {
			s.truncated = true
			break
		}
		cl := b.cluster(e)
		if cl.err != nil {
			continue
		}
		sol, st := solver.SolveContext(ctx, q, cl.orderCluster, solver.Options{Seed: e.seed, Iterations: b.iterations(e.Effort), Workers: 1})
		solved(st)
		baseline = baseline || isBaseline(e.Experiment)
		admit(job.Trial{Kind: job.TrialExperiment, Algorithm: e.Algorithm, K: cl.k, Effort: e.Effort, Silhouette: cl.silhouette, Iterations: st.Iterations}, sol, cl.orderCluster)
	}
	if s.best == nil && !baseline {
		// Nothing to build on (no incumbent, no experiment that produced a valid plan, even none at all):
		// the plain solve, which also answers a cancelled context with the plan it constructed.
		sol, st := solver.SolveContext(ctx, q, nil, solver.Options{Seed: 1, Iterations: b.iterations(job.EffortNormal), Workers: 1})
		solved(st)
		admit(job.Trial{Kind: job.TrialExperiment, Algorithm: job.AlgNone, Effort: job.EffortNormal, Iterations: st.Iterations}, sol, nil)
	}
	if s.best == nil {
		return nil, fmt.Errorf("no solution passes validation: %s", lastFailure)
	}

	if sch.Polish != "" {
		if ctx.Err() != nil {
			s.truncated = true
		} else {
			src := s.best
			seed := 1 + uint64(len(sch.Experiments))
			sol, st := solver.RefineContext(ctx, q, src.sol, solver.Options{Seed: seed, Iterations: b.iterations(sch.Polish), Workers: 1})
			solved(st)
			s.polished = true
			// It continues the clustering of the solution it started from, so the facts about the winner stay meaningful.
			admit(job.Trial{Kind: job.TrialPolish, Effort: sch.Polish, Iterations: st.Iterations}, sol, src.orderCluster)
		}
	}
	s.trials[s.best.trial].Won = true
	return s, nil
}

// facts audits a candidate for a reviewer with the engine's own diagnosis. rep is the validator's verdict on sol.
func facts(q *model.Problem, sol *model.Solution, orderCluster []int, rep *validate.Report) *job.Facts {
	d := analysis.Diagnose(q, sol, orderCluster)
	f := &job.Facts{Valid: rep.Valid, MaxOverlap: d.MaxOverlap, Hints: relevantHints(d.Hints)}
	if d.Integrity != nil {
		f.ClusterPurity = d.Integrity.Purity
	}
	for _, u := range d.Unserved {
		f.Unserved = append(f.Unserved, job.Unserved{Shipment: u.Order, Cause: u.Cause})
	}
	return f
}

// relevantHints drops the engine's remark that only some vehicles are used. Our fleet is a pool with one virtual
// vehicle per shipment, so the remark is always true, and it advises changing the objective order, which a
// reviewer cannot do: all it would do is distract it.
func relevantHints(hints []string) []string {
	return slices.DeleteFunc(slices.Clone(hints), func(h string) bool {
		return strings.HasPrefix(h, "Only ") && strings.Contains(h, " vehicles are used")
	})
}
