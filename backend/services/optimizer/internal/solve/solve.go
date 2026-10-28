// Package solve turns a job.Job into routes with the engine. It is what runs in
// the sandbox (cmd/solver) and in-process for development and as a fallback.
//
// A job is solved once per preset, in parallel. Each preset is the same problem
// under a different priority of objectives, so the four candidates are the
// engine's best answers to "cheapest", "shortest", "fastest" and "balanced".
// Small jobs are also solved exhaustively, which proves the answer optimal.
//
// A job with a Search looks harder per preset (see search.go): an incumbent from an
// earlier job, clustered experiments and a warm-start polish compete, and the best
// valid result wins. Without one, a preset is solved once, the quick quote.
package solve

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/engine/analysis"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/exact"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/model"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/plan"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/solver"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/validate"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/fare"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/version"
)

// Defaults for limits a job leaves out.
const (
	defaultIterations = 1500
	defaultExactMax   = 5
	defaultExactEvals = 3_000_000
	shiftHours        = 24
	// blendScale turns the blend weights into integers: each weight is
	// blendScale * share / reference, where reference is the baseline total of that component.
	blendScale = 1e12
)

// Capacity dimensions and the way a shipment's size class becomes a skill.
const (
	dimWeight = "weight"
	dimVolume = "volume"
)

func sizeSkill(rank int) string { return fmt.Sprintf("size>=%d", rank) }

// Progress is called after each preset finishes.
type Progress func(done, total int)

// Run solves j and returns the result. Cancelling ctx ends the search early; the
// candidates then carry Truncated.
func Run(ctx context.Context, j *job.Job, progress Progress) (*job.Result, error) {
	if err := j.Validate(); err != nil {
		return nil, err
	}
	if j.Engine != "" && j.Engine != version.Engine {
		return nil, job.Fail(job.CodeEngine, "job wants engine %q, this is %q", j.Engine, version.Engine)
	}
	started := time.Now()
	if j.Limits.MaxSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(j.Limits.MaxSeconds)*time.Second)
		defer cancel()
	}

	b, err := build(j)
	if err != nil {
		return nil, job.Fail(job.CodeInvalid, "%v", err)
	}
	refs := b.baseline()
	b.experiments = b.planExperiments()

	res := &job.Result{
		Version: job.Version,
		Engine:  version.Engine,
		Stats:   job.Stats{Shipments: len(j.Shipments), Vehicles: len(b.p.Vehicles)},
	}
	for _, o := range b.unserved() {
		res.Unserved = append(res.Unserved, o)
	}

	// Presets run side by side; their order in the result is the order asked for.
	out := make([]*outcome, len(j.Presets))
	errs := make([]error, len(j.Presets))
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		done int
	)
	for i, preset := range j.Presets {
		wg.Go(func() {
			out[i], errs[i] = b.solvePreset(ctx, preset, refs)
			if progress != nil {
				mu.Lock()
				done++
				progress(done, len(j.Presets))
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			return nil, job.Fail(job.CodeInternal, "preset %s: %v", j.Presets[i], err)
		}
	}
	for _, o := range out {
		res.Candidates = append(res.Candidates, o.candidate)
		res.Stats.Iterations += o.iterations
		res.Stats.ExactRan = res.Stats.ExactRan || o.exactRan
		res.Stats.Trials += o.trials
		res.Stats.Polished = res.Stats.Polished || o.polished
	}
	res.Stats.ElapsedMs = time.Since(started).Milliseconds()
	return res, nil
}

// built is a job compiled into an engine problem.
type built struct {
	j *job.Job
	p *model.Problem
	// shipmentOf maps an order index to its shipment id (orders follow j.Shipments).
	shipmentOf []string
	// typeOf maps a vehicle index to its fleet type name.
	typeOf []string
	// byType lists the vehicle indexes of each fleet type, in vehicle order.
	byType map[string][]int
	// orderOf maps a shipment id to its order index; pickup and drop are the stops of an order.
	orderOf map[string]int
	pickup  []int
	drop    []int
	// experiments are the experiments of the job's Search, planned once and shared by the presets.
	experiments []*experiment
	// units are the orders as clusterable items, built once when the first clustering needs them.
	units func() *unitSet
}

func build(j *job.Job) (*built, error) {
	pl := &plan.Plan{
		TZ:         "UTC",
		Objectives: objectives(job.Cheapest),
		Properties: map[string]any{"name": j.ID},
	}

	// Fleet types ranked by the largest item they take: a shipment that does not
	// fit the type of rank r-1 needs the skill "size>=r", which only vehicles of
	// rank r and above offer.
	fleet := slices.Clone(j.Fleet)
	slices.SortStableFunc(fleet, func(a, b job.FleetType) int {
		if c := cmpFloat(a.Capacity.MaxDimensionCm, b.Capacity.MaxDimensionCm); c != 0 {
			return c
		}
		return cmpFloat(a.Capacity.WeightKg, b.Capacity.WeightKg)
	})

	shiftEnd := j.DepartAt.Add(shiftHours * time.Hour)
	maxCount := len(j.Shipments)
	var typeOf []string
	for rank, f := range fleet {
		fixed, perKm, perHour, perOrder := fare.Cost(f.Fare)
		if fixed < 0 {
			return nil, fmt.Errorf("fleet type %s: the base fare %g is below twice the extra-stop fare %g", f.Type, f.Fare.Base, f.Fare.PerExtraStop)
		}
		var skills []string
		for r := 1; r <= rank; r++ {
			skills = append(skills, sizeSkill(r))
		}
		start := pos(j.Points[f.Start])
		for k := range min(f.Count, maxCount) {
			pl.Resources = append(pl.Resources, plan.Resource{
				ID:             fmt.Sprintf("%s#%d", f.Type, k+1),
				VehicleProfile: plan.VehicleProfile{Type: "truck", AvgSpeedKmh: f.SpeedKmh},
				Departure:      &start, OpenRoute: true,
				WorkingTimeWindow: &plan.TimeWindow{Begin: j.DepartAt, End: shiftEnd},
				Capacities:        map[string]float64{dimWeight: f.Capacity.WeightKg, dimVolume: f.Capacity.VolumeM3},
				Skills:            skills,
				Cost: &plan.ResourceCost{
					Fixed: fixed, PerKm: perKm, PerHour: perHour, PerOrder: perOrder,
					Surcharge:    &plan.LoadSurcharge{Dimension: dimWeight, Above: f.Fare.SurchargeAboveKg, PerUnit: f.Fare.SurchargePerKg},
					FreeApproach: true,
				},
			})
			typeOf = append(typeOf, f.Type)
		}
	}

	shipmentOf := make([]string, len(j.Shipments))
	for i, s := range j.Shipments {
		load := map[string]float64{dimWeight: s.WeightKg, dimVolume: s.VolumeM3}
		pick := plan.Stop{Type: "single", ID: "p:" + s.ID, Kind: plan.KindPickup, Position: pos(j.Points[s.Pickup]),
			OperationDuration: plan.Duration(time.Duration(j.Dwell.Pickup) * time.Second), Capacities: load}
		drop := plan.Stop{Type: "single", ID: "d:" + s.ID, Kind: plan.KindDelivery, Position: pos(j.Points[s.Dropoff]),
			OperationDuration: plan.Duration(time.Duration(j.Dwell.Drop) * time.Second), Capacities: load}
		if s.Deadline != nil {
			// A deadline is soft: being late costs Delay, which every preset ranks right after serving the shipment.
			drop.PreferredTimeWindows = []plan.TimeWindow{{Begin: j.DepartAt, End: *s.Deadline}}
		}
		o := plan.Order{ID: s.ID, Stops: []plan.Stop{pick, drop}, SuccessiveStops: true}
		if need := fitRank(fleet, s); need > 0 {
			o.RequiredSkills = []string{sizeSkill(need)}
		}
		pl.Orders = append(pl.Orders, o)
		shipmentOf[i] = s.ID
	}
	if err := pl.Validate(); err != nil {
		return nil, err
	}

	p, err := model.Compile(pl, model.Options{})
	if err != nil {
		return nil, err
	}
	mats, err := matrices(j, p)
	if err != nil {
		return nil, err
	}
	if err := p.SetMatrices(mats); err != nil {
		return nil, err
	}
	b := &built{j: j, p: p, shipmentOf: shipmentOf, typeOf: typeOf, byType: map[string][]int{}, orderOf: map[string]int{}}
	for v, t := range typeOf {
		b.byType[t] = append(b.byType[t], v)
	}
	b.pickup, b.drop = make([]int, len(p.Orders)), make([]int, len(p.Orders))
	for o := range p.Orders {
		b.orderOf[shipmentOf[o]] = o
		for _, n := range p.Orders[o].Nodes {
			if p.Nodes[n].Kind == model.Pickup {
				b.pickup[o] = n
			} else {
				b.drop[o] = n
			}
		}
	}
	b.units = sync.OnceValue(b.buildUnits)
	return b, nil
}

// fitRank is the smallest rank of a fleet type whose largest item dimension
// takes the shipment's longest item; len(fleet) when no type does.
func fitRank(fleet []job.FleetType, s job.Shipment) int {
	for r, f := range fleet {
		if s.MaxDimensionCm <= f.Capacity.MaxDimensionCm {
			return r
		}
	}
	return len(fleet)
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func pos(p job.Point) plan.Position { return plan.Position{Lat: p.Lat, Lon: p.Lng} }

// locKey identifies a place the way the engine deduplicates them (1e-6 degrees).
func locKey(lat, lng float64) [2]int64 {
	return [2]int64{int64(math.Round(lat * 1e6)), int64(math.Round(lng * 1e6))}
}

// matrices builds the travel matrix of every profile of p. The job carries road
// distances; a profile's travel times are those distances at its speed.
func matrices(j *job.Job, p *model.Problem) ([]*model.Matrix, error) {
	index := make(map[[2]int64]int, len(j.Points))
	for i, pt := range j.Points {
		if _, ok := index[locKey(pt.Lat, pt.Lng)]; !ok {
			index[locKey(pt.Lat, pt.Lng)] = i
		}
	}
	n := len(p.Locs)
	src := make([]int, n)
	for l, ll := range p.Locs {
		i, ok := index[locKey(ll.Lat, ll.Lon)]
		if !ok {
			return nil, fmt.Errorf("location %d is not among the job's points", l)
		}
		src[l] = i
	}
	np := len(j.Points)
	dist := make([]float64, n*n)
	for a := range n {
		for b := range n {
			if a != b {
				dist[a*n+b] = float64(j.DistM[src[a]*np+src[b]])
			}
		}
	}
	mats := make([]*model.Matrix, len(p.Profiles))
	for i, prof := range p.Profiles {
		dur := make([]float64, n*n)
		for k, d := range dist {
			dur[k] = d / (prof.SpeedKmh / 3.6)
		}
		mats[i] = model.NewMatrix(n, dur, dist, "job")
	}
	return mats, nil
}

// objectives is the priority order of a preset. Serving every shipment comes
// first and then arriving by the deadlines; the preset decides the rest.
func objectives(preset string) []string {
	head := []string{"maximizeMandatoryStops", "minimizeDelay"}
	switch preset {
	case job.Shortest:
		return append(head, "minimizeDistance", "minimizeCosts", "minimizeSumArrival")
	case job.Fastest:
		return append(head, "minimizeLargestTourDuration", "minimizeSumArrival", "minimizeCosts")
	case job.Balanced:
		return append(head, "minimizeBlend")
	default:
		return append(head, "minimizeCosts", "minimizeDistance", "minimizeSumArrival")
	}
}

// engineObjectives is objectives(preset) in the engine's own terms.
func engineObjectives(preset string) []model.Objective {
	names := objectives(preset)
	out := make([]model.Objective, 0, len(names))
	for _, name := range names {
		for o := range model.NumObjectives {
			if o.PlanName() == name {
				out = append(out, o)
			}
		}
	}
	return out
}

// refs are the totals of the baseline, every shipment sent on its own. They are
// the yardstick that makes the components of the balanced preset comparable.
type refs struct{ cost, sumArrival, dist, makespan int64 }

// baseline prices every shipment alone on the cheapest vehicle type that takes it.
func (b *built) baseline() refs {
	p := b.p
	ev := model.NewEvaluator(p)
	class := p.VehicleClasses()
	var r refs
	for o := range p.Orders {
		best := model.RouteEval{Cost: math.MaxInt64}
		for v := range p.Vehicles {
			if class[v] != v {
				continue
			}
			if e := ev.Eval(v, p.Orders[o].Nodes); e.OK && e.Cost < best.Cost {
				best = e
			}
		}
		if best.Cost == math.MaxInt64 {
			continue // no vehicle takes it; it is reported as unserved
		}
		r.cost += best.Cost
		r.sumArrival += best.SumArrival
		r.dist += best.Dist
		r.makespan = max(r.makespan, best.Elapsed)
	}
	return r
}

// blend turns the balanced preset's shares (0.4 cost, 0.3 arrival, 0.15 distance,
// 0.15 longest route) into integer weights against the baseline totals.
func (r refs) blend() plan.Blend {
	w := func(share float64, ref int64) int64 {
		return max(1, int64(math.Round(blendScale*share/float64(max(1, ref)))))
	}
	return plan.Blend{Cost: w(0.4, r.cost), SumArrival: w(0.3, r.sumArrival), Dist: w(0.15, r.dist), Makespan: w(0.15, r.makespan)}
}

// unserved lists the shipments that no vehicle can carry alone, with the engine's cause.
func (b *built) unserved() []job.Unserved {
	p := b.p
	s := model.NewSolution(p)
	var lost []int
	ev := model.NewEvaluator(p)
	for o := range p.Orders {
		fits := false
		for v := range p.Vehicles {
			if e := ev.Eval(v, p.Orders[o].Nodes); e.OK {
				fits = true
				break
			}
		}
		if !fits {
			lost = append(lost, o)
		}
	}
	if len(lost) == 0 {
		return nil
	}
	s.Unassigned = lost
	causes := map[string]string{}
	for _, u := range analysis.Diagnose(p, s, nil).Unserved {
		causes[u.Order] = u.Cause
	}
	out := make([]job.Unserved, 0, len(lost))
	for _, o := range lost {
		out = append(out, job.Unserved{Shipment: b.shipmentOf[o], Cause: causes[b.shipmentOf[o]]})
	}
	return out
}

type outcome struct {
	candidate  job.Candidate
	iterations int
	exactRan   bool
	trials     int
	polished   bool
}

// problem is b.p ranked by a preset's objectives. The problem is immutable; the copy only changes how
// solutions are ranked, so every solution of the preset must be built on it for its metrics to be comparable.
func (b *built) problem(preset string, r refs) *model.Problem {
	q := *b.p
	q.Objectives = engineObjectives(preset)
	if preset == job.Balanced {
		q.Blend = r.blend()
	}
	return &q
}

// effortIterations are the iterations per worker of each effort level when the job leaves the base to the
// engine; the base itself (Limits.Iterations) is what EffortNormal means, so a job that tunes it keeps the
// plain solve and the normal experiment identical.
var effortIterations = map[string]int{job.EffortQuick: 400, job.EffortNormal: defaultIterations, job.EffortThorough: 5000}

// iterations is the improvement iterations per worker for an effort level, scaled to the size of the job.
func (b *built) iterations(effort string) int {
	it := b.j.Limits.Iterations
	if it <= 0 {
		it = defaultIterations
	}
	if effort != "" && effort != job.EffortNormal {
		it = max(1, it*effortIterations[effort]/defaultIterations)
	}
	return it * max(1, len(b.p.Orders)/25)
}

// score is the objective vector of m in the priority order of q: comparing two of them lexicographically
// gives the verdict of q.Compare, because Compare reads exactly these entries in exactly this order.
func score(q *model.Problem, m *model.Metrics) []int64 {
	out := make([]int64, len(q.Objectives))
	for i, o := range q.Objectives {
		out[i] = m[o]
	}
	return out
}

func metricsOf(m *model.Metrics) job.Metrics {
	return job.Metrics{
		CostMilli:     m[model.ObjCost],
		DistM:         m[model.ObjDist],
		SumArrivalSec: m[model.ObjSumArrival],
		MakespanSec:   m[model.ObjMakespan],
		LateSec:       m[model.ObjDelay],
	}
}

// solvePreset finds the best plan for one preset.
func (b *built) solvePreset(ctx context.Context, preset string, r refs) (*outcome, error) {
	q := b.problem(preset, r)
	n := len(q.Orders)

	var (
		best      *model.Solution
		truncated bool
		found     *searched // nil for a plain solve
		out       = &outcome{}
	)
	if b.j.Search == nil {
		sol, stats := solver.SolveContext(ctx, q, nil, solver.Options{Seed: 1, Iterations: b.iterations(job.EffortNormal), Workers: 1})
		best, truncated = sol, stats.Truncated
		out.iterations, out.trials = stats.Iterations, 1
	} else {
		var err error
		if found, err = b.search(ctx, q, preset); err != nil {
			return nil, err
		}
		best, truncated = found.best.sol, found.truncated
		out.iterations, out.trials, out.polished = found.iterations, found.solves, found.polished
	}
	proof := job.ProofHeuristic

	exactMax := b.j.Limits.ExactMaxOrders
	if exactMax == 0 {
		exactMax = defaultExactMax
	}
	if n <= exactMax && n <= exact.MaxOrders && ctx.Err() == nil {
		budget := b.j.Limits.ExactBudget
		if budget <= 0 {
			budget = defaultExactEvals
		}
		out.exactRan = true
		if res, err := exact.Solve(q, budget); err == nil {
			// The proof can only improve on the heuristic; if it ever did not, the
			// heuristic's validated answer stays and is not called optimal.
			if !q.Less(&best.M, &res.Solution.M) {
				best, proof = res.Solution, job.ProofOptimal
			}
		}
	}

	rep := validate.Check(q, best)
	if !rep.Valid {
		return nil, fmt.Errorf("the solution fails validation: %v", rep.Violations)
	}
	out.candidate = job.Candidate{
		Preset: preset, Routes: b.routes(best), Proof: proof, Truncated: truncated,
		Metrics: metricsOf(&best.M), Score: score(q, &best.M),
	}
	if found != nil {
		// Won names the trial that won among the heuristics. The exact step above can only tie it or, if the
		// heuristics all fell short, beat it; the candidate's own Score and Metrics say which.
		out.candidate.Trials = found.trials
		out.candidate.Facts = facts(q, best, found.best.orderCluster, rep)
	}
	return out, nil
}

// routes reads the visiting order of every used vehicle out of a solution.
func (b *built) routes(s *model.Solution) []job.Route {
	var out []job.Route
	for v, route := range s.Routes {
		if len(route) == 0 {
			continue
		}
		r := job.Route{Type: b.typeOf[v]}
		for _, n := range route {
			node := &b.p.Nodes[n]
			kind := job.Drop
			if node.Kind == model.Pickup {
				kind = job.Pickup
			}
			r.Stops = append(r.Stops, job.Stop{Shipment: b.shipmentOf[node.Order], Kind: kind})
		}
		out = append(out, r)
	}
	// Routes are listed by the first stop's shipment so that equal plans compare equal.
	slices.SortStableFunc(out, func(a, c job.Route) int { return b.rank(a) - b.rank(c) })
	return out
}

// rank orders routes by the position of their first shipment in the job.
func (b *built) rank(r job.Route) int {
	for i, id := range b.shipmentOf {
		if id == r.Stops[0].Shipment {
			return i
		}
	}
	return len(b.shipmentOf)
}
