// Package model compiles a plan into an indexed, integer-time routing problem
// and provides the route evaluator shared by the solver and the validator.
package model

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/engine/geo"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/plan"
)

// Inf is a time/duration larger than anything in a real plan that still leaves
// headroom against int64 overflow when added to other values.
const Inf = int64(math.MaxInt64 / 4)

// MaxGlobalConstraints bounds the number of global constraints, which are
// tracked in a fixed-size array on the evaluator hot path.
const MaxGlobalConstraints = 4

// Kind distinguishes loading from unloading stops.
type Kind uint8

// Stop kinds.
const (
	Pickup Kind = iota
	Delivery
)

// Window is a closed interval in seconds relative to Problem.Epoch.
type Window struct{ Begin, End int64 }

// Node is a single stop, the smallest schedulable unit.
type Node struct {
	ID      string
	Order   int
	Seq     int // position within its order
	Kind    Kind
	Loc     int
	Pos     geo.LatLon
	Service int64
	Demand  []float64 // per capacity dimension; nil when the stop carries nothing
	// Onboard marks a delivery whose order has no pickup: the goods are loaded
	// when the vehicle leaves its start location.
	Onboard bool
	Auth    []Window // hard windows, sorted; never empty (unbounded if none)
	Pref    []Window // soft windows, sorted
	Target  []Window // Auth intersected with Pref: windows the scheduler aims for
	Tags    []string
	GCMask  [MaxGlobalConstraints]uint64
	Need    uint64 // required skills, as a bitmask over Problem.SkillNames
}

// Order is a group of stops served by the same vehicle.
type Order struct {
	ID    string
	Nodes []int
	// Sequential requires the stops in their listed order. Otherwise only
	// pickups must precede deliveries.
	Sequential bool
	Pickups    int
	// RequiredSkills names the skills a vehicle needs to serve this order.
	RequiredSkills []string
}

// BreakRule is a sliding break: after MaxInter seconds of work a break of
// Min seconds must be taken.
type BreakRule struct{ Min, MaxInter int64 }

// FixedBreak is a rest at a fixed time of day: from Begin to End the driver
// neither drives nor serves. Driving is suspended when the break starts and
// resumes when it ends; a service that would overlap the break waits for it to
// end instead of being interrupted.
type FixedBreak struct{ Begin, End int64 }

// advance returns when a trip of d seconds that sets off at t arrives, and how
// much of the trip's span is spent resting.
func (f *FixedBreak) advance(t, d int64) (arrive, rest int64) {
	switch {
	case d == 0 || t >= f.End || t+d <= f.Begin:
		return t + d, 0
	case t < f.Begin:
		return t + d + f.End - f.Begin, f.End - f.Begin
	default:
		return f.End + d, f.End - t
	}
}

// Overlap is how much of [from, to) lies inside the break.
func (f *FixedBreak) Overlap(from, to int64) int64 {
	return max(0, min(to, f.End)-max(from, f.Begin))
}

// Vehicle is a resource compiled for fast evaluation.
type Vehicle struct {
	ID       string
	Profile  int // index into Problem.Profiles
	Start    int // location index
	End      int
	Window   Window
	Cap      []float64
	PrefTags map[string]bool
	Break    *BreakRule
	Fixed    *FixedBreak
	MaxWork  int64 // seconds, 0 means unlimited
	// MultiTrip lets the vehicle return to End during the day to unload and set
	// out again; Turnaround is the time that takes, in seconds.
	MultiTrip  bool
	Turnaround int64
	Cost       *plan.ResourceCost
	// Open ends the route at the last stop: the leg back to End is free.
	Open bool
	// SurchargeDim is the capacity dimension the cost surcharge applies to, -1 for none.
	SurchargeDim int
	Tags         []string
	Skills       uint64 // offered skills as a bitmask over Problem.SkillNames
	// SkillNames lists the offered skills by name.
	SkillNames []string
}

// GlobalConstraint is a compiled maxCumulatedCost constraint.
type GlobalConstraint struct {
	Name    string
	Max     float64
	Tags    []string    // stop tags tracked by this constraint, at most 64
	TagCost [][]float64 // [vehicle][tag]: charged once when the vehicle visits the tag
}

// Profile selects how travel between two locations is measured: which Mapbox
// road network to use and, optionally, a fixed average speed that replaces the
// network's own travel times (duration = road distance / speed).
type Profile struct {
	Road     string
	SpeedKmh float64
}

func (p Profile) String() string {
	if p.SpeedKmh > 0 {
		return fmt.Sprintf("%s@%gkm/h", p.Road, p.SpeedKmh)
	}
	return p.Road
}

// Objective is one component of the solution quality vector.
type Objective int

// Objectives, in the vocabulary of the plan.
const (
	ObjUnserved   Objective = iota // maximizeMandatoryStops
	ObjUnsatPref                   // maximizePreferredStops
	ObjDelay                       // minimizeDelay
	ObjCost                        // minimizeCosts
	ObjDist                        // minimizeDistance
	ObjMakespan                    // minimizeLargestTourDuration
	ObjWork                        // minimizeWorkingDuration
	ObjSumArrival                  // minimizeSumArrival: total of the delivery arrival times (the average delivery time)
	ObjBlend                       // minimizeBlend: weighted sum of cost, arrival times, distance and longest tour
	NumObjectives
)

var objectiveNames = [NumObjectives]string{
	"maximizeMandatoryStops",
	"maximizePreferredStops",
	"minimizeDelay",
	"minimizeCosts",
	"minimizeDistance",
	"minimizeLargestTourDuration",
	"minimizeWorkingDuration",
	"minimizeSumArrival",
	"minimizeBlend",
}

// PlanName is the identifier used for the objective in plan files.
func (o Objective) PlanName() string { return objectiveNames[o] }

// Unit describes the unit in which Metrics stores the objective.
func (o Objective) Unit() string {
	return [NumObjectives]string{"stops", "preferences", "s", "cost×1000", "m", "s", "s", "s", "blend"}[o]
}

// Metrics holds every objective value of a (partial) solution as integers so
// that comparisons are exact: stops, preferences, seconds, milli-cost, meters.
type Metrics [NumObjectives]int64

// Options tune how a plan is interpreted.
type Options struct {
	// UnlimitedUndeclaredCapacity treats a resource that declares no
	// capacities at all as unconstrained. By default it can carry nothing.
	UnlimitedUndeclaredCapacity bool
}

// Problem is the compiled, immutable routing problem.
type Problem struct {
	Name       string
	TZ         *time.Location
	Epoch      time.Time
	Locs       []geo.LatLon
	Dims       []string
	Vehicles   []Vehicle
	Orders     []Order
	Nodes      []Node
	Profiles   []Profile // distinct travel profiles used by the vehicles
	SkillNames []string  // every skill offered or required, indexed by bit
	GC         []GlobalConstraint
	Objectives []Objective // priority order, most important first
	// Blend weights the components of the ObjBlend metric; see plan.Blend.
	Blend plan.Blend

	// Affinity lists groups of orders that a global constraint forces onto a
	// single vehicle; clustering keeps them together.
	Affinity [][]int

	// PrefMatch[v*len(Nodes)+n] is true when vehicle v prefers node n's tags;
	// PrefPossible[n] when some vehicle does.
	PrefMatch    []bool
	PrefPossible []bool

	// IsDepot[l] marks locations used as a vehicle start or end.
	IsDepot []bool

	Mats     []*Matrix // one per profile; set with SetMatrices
	Warnings []string

	skillBit map[string]uint64
}

// Matrix holds integer travel times (s) and distances (m), row-major.
type Matrix struct {
	N      int
	Dur    []int32
	Dist   []int32
	Source string // where the numbers came from, for reporting
}

// NewMatrix converts floating point measurements to a Matrix.
func NewMatrix(n int, dur, dist []float64, source string) *Matrix {
	m := &Matrix{N: n, Dur: make([]int32, n*n), Dist: make([]int32, n*n), Source: source}
	for i := range dur {
		m.Dur[i] = int32(math.Round(dur[i]))
		m.Dist[i] = int32(math.Round(dist[i]))
	}
	return m
}

// TravelTime returns the travel time in seconds between two locations.
func (m *Matrix) TravelTime(a, b int) int64 { return int64(m.Dur[a*m.N+b]) }

// TravelDist returns the travel distance in meters between two locations.
func (m *Matrix) TravelDist(a, b int) int64 { return int64(m.Dist[a*m.N+b]) }

// SetMatrices installs one matrix per profile (same order as Problem.Profiles).
func (p *Problem) SetMatrices(mats []*Matrix) error {
	if len(mats) != len(p.Profiles) {
		return fmt.Errorf("got %d matrices for %d profiles", len(mats), len(p.Profiles))
	}
	for i, m := range mats {
		if m == nil || m.N != len(p.Locs) {
			return fmt.Errorf("matrix %d (%s) does not match %d locations", i, p.Profiles[i], len(p.Locs))
		}
	}
	p.Mats = mats
	return nil
}

// VehicleClasses groups vehicles that are identical in everything but their id.
// The result maps each vehicle to its class, named by the index of the first
// vehicle of the class. Empty routes of vehicles in the same class are
// interchangeable, which lets searches try only one of them.
func (p *Problem) VehicleClasses() []int {
	class := make([]int, len(p.Vehicles))
	var reps []int
	for v := range p.Vehicles {
		found := -1
		for _, r := range reps {
			if sameVehicle(&p.Vehicles[r], &p.Vehicles[v]) {
				found = r
				break
			}
		}
		if found < 0 {
			found = v
			reps = append(reps, v)
		}
		class[v] = found
	}
	return class
}

func sameVehicle(a, b *Vehicle) bool {
	x, y := *a, *b
	x.ID, y.ID = "", ""
	return reflect.DeepEqual(x, y)
}

// Seconds converts an absolute time to the solver's integer time axis.
func (p *Problem) Seconds(t time.Time) int64 { return int64(t.Sub(p.Epoch) / time.Second) }

// Time converts solver seconds back to an absolute UTC time.
func (p *Problem) Time(sec int64) time.Time {
	return p.Epoch.Add(time.Duration(sec) * time.Second).UTC()
}

// Local converts solver seconds to the plan's time zone.
func (p *Problem) Local(sec int64) time.Time { return p.Time(sec).In(p.TZ) }

// Less reports whether a is strictly better than b under the plan's
// objective priorities.
func (p *Problem) Less(a, b *Metrics) bool {
	for _, o := range p.Objectives {
		if a[o] != b[o] {
			return a[o] < b[o]
		}
	}
	return false
}

// Compare orders two metric vectors by the objective priorities.
func (p *Problem) Compare(a, b *Metrics) int {
	for _, o := range p.Objectives {
		if c := cmp.Compare(a[o], b[o]); c != 0 {
			return c
		}
	}
	return 0
}

// OrderAnchors returns the planar-projection inputs of an order: positions of
// its stops that are not at a depot (a pickup at the depot says nothing about
// where the order belongs geographically).
func (p *Problem) OrderAnchors(o int) []int {
	var locs []int
	for _, n := range p.Orders[o].Nodes {
		if l := p.Nodes[n].Loc; !p.IsDepot[l] && !slices.Contains(locs, l) {
			locs = append(locs, l)
		}
	}
	return locs
}

// ObjectiveOrder lists the plan names of the objectives in priority order.
func (p *Problem) ObjectiveOrder() []string {
	names := make([]string, len(p.Objectives))
	for i, o := range p.Objectives {
		names[i] = o.PlanName()
	}
	return names
}

// Compile validates the plan semantics and builds the indexed problem. Matrices
// are not included; fetch them for Problem.Locs and call SetMatrices.
func Compile(pl *plan.Plan, opt Options) (*Problem, error) {
	p := &Problem{Name: pl.Name(), TZ: time.UTC}
	if pl.TZ != "" {
		loc, err := time.LoadLocation(pl.TZ)
		if err != nil {
			return nil, fmt.Errorf("load tz: %w", err)
		}
		p.TZ = loc
	}
	p.Epoch = epochOf(pl)

	locIndex := map[[2]int64]int{}
	addLoc := func(pos plan.Position) int {
		key := [2]int64{int64(math.Round(pos.Lat * 1e6)), int64(math.Round(pos.Lon * 1e6))}
		if i, ok := locIndex[key]; ok {
			return i
		}
		locIndex[key] = len(p.Locs)
		p.Locs = append(p.Locs, geo.LatLon{Lat: pos.Lat, Lon: pos.Lon})
		return len(p.Locs) - 1
	}

	// Capacity dimensions are the union of everything vehicles offer or stops need.
	dimSet := map[string]bool{}
	for _, r := range pl.Resources {
		for d := range r.Capacities {
			dimSet[d] = true
		}
	}
	for _, o := range pl.Orders {
		for _, s := range o.Stops {
			for d := range s.Capacities {
				dimSet[d] = true
			}
		}
	}
	for d := range dimSet {
		p.Dims = append(p.Dims, d)
	}
	slices.Sort(p.Dims)
	dimIdx := make(map[string]int, len(p.Dims))
	for i, d := range p.Dims {
		dimIdx[d] = i
	}

	if err := p.indexSkills(pl); err != nil {
		return nil, err
	}
	if err := p.compileVehicles(pl, opt, addLoc, dimIdx); err != nil {
		return nil, err
	}
	if err := p.compileOrders(pl, addLoc, dimIdx); err != nil {
		return nil, err
	}
	p.IsDepot = make([]bool, len(p.Locs))
	for _, v := range p.Vehicles {
		p.IsDepot[v.Start], p.IsDepot[v.End] = true, true
	}
	if err := p.compileConstraints(pl); err != nil {
		return nil, err
	}
	p.compilePreferences()
	p.compileObjectives(pl)
	if b := pl.Blend; b != nil {
		if b.Cost < 0 || b.SumArrival < 0 || b.Dist < 0 || b.Makespan < 0 {
			return nil, errors.New("blend weights must not be negative")
		}
		p.Blend = *b
	}
	if slices.Contains(p.Objectives, ObjBlend) && p.Blend == (plan.Blend{}) {
		return nil, errors.New("the minimizeBlend objective needs blend weights")
	}
	return p, nil
}

// epochOf picks the earliest instant mentioned in the plan as time zero.
func epochOf(pl *plan.Plan) time.Time {
	var epoch time.Time
	note := func(t time.Time) {
		if !t.IsZero() && (epoch.IsZero() || t.Before(epoch)) {
			epoch = t
		}
	}
	for _, r := range pl.Resources {
		if r.WorkingTimeWindow != nil {
			note(r.WorkingTimeWindow.Begin)
		}
	}
	for _, o := range pl.Orders {
		for _, s := range o.Stops {
			for _, w := range slices.Concat(s.AuthorizedTimeWindows, s.PreferredTimeWindows) {
				note(w.Begin)
			}
		}
	}
	if epoch.IsZero() {
		now := time.Now().UTC()
		epoch = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	}
	return epoch.UTC()
}

// indexSkills assigns a bit to every skill mentioned anywhere in the plan.
func (p *Problem) indexSkills(pl *plan.Plan) error {
	set := map[string]bool{}
	for _, r := range pl.Resources {
		for _, sk := range r.Skills {
			set[sk] = true
		}
	}
	for _, o := range pl.Orders {
		for _, sk := range o.RequiredSkills {
			set[sk] = true
		}
	}
	if len(set) > 64 {
		return fmt.Errorf("at most 64 distinct skills are supported, got %d", len(set))
	}
	p.SkillNames = slices.Sorted(maps.Keys(set))
	p.skillBit = make(map[string]uint64, len(p.SkillNames))
	for i, sk := range p.SkillNames {
		p.skillBit[sk] = 1 << i
	}
	return nil
}

func (p *Problem) compileVehicles(pl *plan.Plan, opt Options, addLoc func(plan.Position) int, dimIdx map[string]int) error {
	profIdx := map[Profile]int{}
	noProfile := map[string][]string{} // vehicle type without a road profile -> resource ids
	horizon := p.Seconds(p.Epoch.Add(24 * time.Hour))
	// Vehicles without a working window may run until the latest stop window ends.
	for _, o := range pl.Orders {
		for _, s := range o.Stops {
			for _, w := range slices.Concat(s.AuthorizedTimeWindows, s.PreferredTimeWindows) {
				horizon = max(horizon, p.Seconds(w.End)+24*3600)
			}
		}
	}

	for i := range pl.Resources {
		r := &pl.Resources[i]
		v := Vehicle{ID: r.ID, Tags: r.Tags, PrefTags: map[string]bool{}}

		start, end := r.Departure, r.Arrival
		if start == nil {
			start = end
		}
		if end == nil {
			end = start
		}
		v.Start, v.End = addLoc(*start), addLoc(*end)
		v.Open = r.OpenRoute

		v.Window = Window{Begin: 0, End: horizon}
		if w := r.WorkingTimeWindow; w != nil {
			v.Window = Window{Begin: p.Seconds(w.Begin), End: p.Seconds(w.End)}
		}

		prof := Profile{Road: mapboxProfile(r.VehicleProfile), SpeedKmh: r.VehicleProfile.AvgSpeedKmh}
		if r.VehicleProfile.Type != "" && !slices.Contains([]string{"truck", "car", "van", "driving"}, strings.ToLower(r.VehicleProfile.Type)) {
			noProfile[r.VehicleProfile.Type] = append(noProfile[r.VehicleProfile.Type], r.ID)
		}
		pi, ok := profIdx[prof]
		if !ok {
			pi = len(p.Profiles)
			profIdx[prof] = pi
			p.Profiles = append(p.Profiles, prof)
		}
		v.Profile = pi

		v.Cap = make([]float64, len(p.Dims))
		unlimited := opt.UnlimitedUndeclaredCapacity && len(r.Capacities) == 0
		for d, i := range dimIdx {
			switch c, declared := r.Capacities[d]; {
			case declared:
				v.Cap[i] = c
			case unlimited:
				v.Cap[i] = math.Inf(1)
			}
		}
		for _, t := range r.PreferredStopTags {
			v.PrefTags[t] = true
		}
		v.SkillNames = r.Skills
		for _, sk := range r.Skills {
			v.Skills |= p.skillBit[sk]
		}

		for _, b := range r.Breaks {
			if b.Type != plan.BreakSliding && b.Type != plan.BreakFixed {
				p.Warnings = append(p.Warnings, fmt.Sprintf("resource %q: break type %q is not supported and is ignored", r.ID, b.Type))
				continue
			}
			if v.Break != nil || v.Fixed != nil {
				p.Warnings = append(p.Warnings, fmt.Sprintf("resource %q: only the first break rule is applied", r.ID))
				continue
			}
			if b.Type == plan.BreakFixed {
				v.Fixed = &FixedBreak{Begin: p.Seconds(b.Begin), End: p.Seconds(b.End)}
				continue
			}
			if b.MaxInterBreakDuration <= 0 {
				return fmt.Errorf("resource %q: sliding break needs a positive maxInterBreakDuration", r.ID)
			}
			v.Break = &BreakRule{Min: int64(b.MinBreakDuration.Std() / time.Second), MaxInter: int64(b.MaxInterBreakDuration.Std() / time.Second)}
		}
		v.MultiTrip = r.MultiTrip
		if r.MultiTrip {
			v.Turnaround = int64(r.TurnaroundDuration.Std() / time.Second)
		} else if r.TurnaroundDuration > 0 {
			p.Warnings = append(p.Warnings, fmt.Sprintf("resource %q: turnaroundDuration has no effect without multiTrip", r.ID))
		}
		v.MaxWork = int64(r.MaxWorkingDuration.Std() / time.Second)
		v.Cost = r.Cost
		v.SurchargeDim = -1
		if r.Cost != nil && r.Cost.Surcharge != nil {
			d, ok := dimIdx[r.Cost.Surcharge.Dimension]
			if !ok {
				return fmt.Errorf("resource %q: the surcharge dimension %q is not a capacity dimension of the plan", r.ID, r.Cost.Surcharge.Dimension)
			}
			v.SurchargeDim = d
		}
		p.Vehicles = append(p.Vehicles, v)
	}
	for _, typ := range slices.Sorted(maps.Keys(noProfile)) {
		ids := noProfile[typ]
		sample := strings.Join(ids[:min(3, len(ids))], ", ")
		if len(ids) > 3 {
			sample += ", …"
		}
		p.Warnings = append(p.Warnings, fmt.Sprintf("%d resource(s) (%s) have vehicle type %q, which has no dedicated road profile; the car network is used (driving, or driving-traffic with withTraffic)", len(ids), sample, typ))
	}
	return nil
}

// mapboxProfile maps a plan vehicle profile to a Matrix API profile.
func mapboxProfile(vp plan.VehicleProfile) string {
	if vp.WithTraffic {
		return "driving-traffic"
	}
	return "driving"
}

func (p *Problem) compileOrders(pl *plan.Plan, addLoc func(plan.Position) int, dimIdx map[string]int) error {
	sortWindows := func(ws []Window) {
		slices.SortFunc(ws, func(a, b Window) int { return cmp.Compare(a.Begin, b.Begin) })
	}
	toWindows := func(in []plan.TimeWindow) []Window {
		out := make([]Window, len(in))
		for i, w := range in {
			out[i] = Window{Begin: p.Seconds(w.Begin), End: p.Seconds(w.End)}
		}
		sortWindows(out)
		return out
	}

	for oi := range pl.Orders {
		po := &pl.Orders[oi]
		ord := Order{ID: po.ID, Sequential: po.SuccessiveStops, RequiredSkills: po.RequiredSkills}
		var need uint64
		for _, sk := range po.RequiredSkills {
			need |= p.skillBit[sk]
		}
		for _, s := range po.Stops {
			if s.Kind == plan.KindPickup {
				ord.Pickups++
			}
		}
		for si := range po.Stops {
			s := &po.Stops[si]
			n := Node{
				ID:      s.ID,
				Order:   oi,
				Seq:     si,
				Kind:    Delivery,
				Loc:     addLoc(s.Position),
				Pos:     geo.LatLon{Lat: s.Position.Lat, Lon: s.Position.Lon},
				Service: int64(s.OperationDuration.Std() / time.Second),
				Tags:    s.Tags,
				Auth:    []Window{{Begin: -Inf, End: Inf}},
				Need:    need,
			}
			if s.Kind == plan.KindPickup {
				n.Kind = Pickup
			}
			n.Onboard = n.Kind == Delivery && ord.Pickups == 0
			if len(s.Capacities) > 0 {
				n.Demand = make([]float64, len(p.Dims))
				for d, q := range s.Capacities {
					n.Demand[dimIdx[d]] = q
				}
			}
			if len(s.AuthorizedTimeWindows) > 0 {
				n.Auth = toWindows(s.AuthorizedTimeWindows)
			}
			if len(s.PreferredTimeWindows) > 0 {
				n.Pref = toWindows(s.PreferredTimeWindows)
				for _, a := range n.Auth {
					for _, w := range n.Pref {
						if lo, hi := max(a.Begin, w.Begin), min(a.End, w.End); lo <= hi {
							n.Target = append(n.Target, Window{lo, hi})
						}
					}
				}
				sortWindows(n.Target)
			}
			ord.Nodes = append(ord.Nodes, len(p.Nodes))
			p.Nodes = append(p.Nodes, n)
		}
		p.Orders = append(p.Orders, ord)
	}
	p.Locs = slices.Clip(p.Locs)
	return nil
}

func (p *Problem) compileConstraints(pl *plan.Plan) error {
	var errs []error
	affinity := newUnionFind(len(p.Orders))
	grouped := false

	for _, gc := range pl.GlobalConstraints {
		if gc.Type != "maxCumulatedCost" {
			p.Warnings = append(p.Warnings, fmt.Sprintf("global constraint %q has unsupported type %q and is ignored", gc.Name, gc.Type))
			continue
		}
		if len(p.GC) >= MaxGlobalConstraints {
			errs = append(errs, fmt.Errorf("at most %d global constraints are supported", MaxGlobalConstraints))
			break
		}
		c := GlobalConstraint{Name: gc.Name, Max: gc.Maximum}

		tagSet := map[string]bool{}
		for _, rt := range gc.CostsByResourceTag {
			for tag := range rt.CostsByStopTag {
				tagSet[tag] = true
			}
		}
		for tag := range tagSet {
			c.Tags = append(c.Tags, tag)
		}
		slices.Sort(c.Tags)
		if len(c.Tags) > 64 {
			errs = append(errs, fmt.Errorf("constraint %q tracks more than 64 stop tags", gc.Name))
			continue
		}

		c.TagCost = make([][]float64, len(p.Vehicles))
		for vi, v := range p.Vehicles {
			c.TagCost[vi] = make([]float64, len(c.Tags))
			for _, rtag := range v.Tags {
				for stopTag, cost := range gc.CostsByResourceTag[rtag].CostsByStopTag {
					c.TagCost[vi][slices.Index(c.Tags, stopTag)] += cost.Using
				}
			}
		}

		ci := len(p.GC)
		for ni := range p.Nodes {
			for ti, tag := range c.Tags {
				if slices.Contains(p.Nodes[ni].Tags, tag) {
					p.Nodes[ni].GCMask[ci] |= 1 << ti
				}
			}
		}

		// A tag that at most one vehicle can afford to touch binds all of its
		// orders to that single vehicle: clustering must keep them together.
		for ti := range c.Tags {
			costs := make([]float64, len(p.Vehicles))
			for vi := range p.Vehicles {
				costs[vi] = c.TagCost[vi][ti]
			}
			if maxVehiclesAffordable(costs, c.Max) != 1 {
				continue
			}
			first := -1
			for ni := range p.Nodes {
				if p.Nodes[ni].GCMask[ci]&(1<<ti) == 0 {
					continue
				}
				if first < 0 {
					first = p.Nodes[ni].Order
				} else {
					affinity.union(first, p.Nodes[ni].Order)
					grouped = true
				}
			}
		}
		p.GC = append(p.GC, c)
	}
	if grouped {
		p.Affinity = affinity.groups(2)
	}
	return errors.Join(errs...)
}

// maxVehiclesAffordable returns how many vehicles can each be charged their
// cost before the budget is exhausted, or len(costs) when any vehicle is free.
func maxVehiclesAffordable(costs []float64, budget float64) int {
	sorted := slices.Clone(costs)
	slices.Sort(sorted)
	if len(sorted) == 0 || sorted[0] <= 0 {
		return len(costs)
	}
	sum, k := 0.0, 0
	for _, c := range sorted {
		if sum+c > budget+1e-9 {
			break
		}
		sum += c
		k++
	}
	return k
}

func (p *Problem) compilePreferences() {
	n := len(p.Nodes)
	p.PrefMatch = make([]bool, len(p.Vehicles)*n)
	p.PrefPossible = make([]bool, n)
	for vi, v := range p.Vehicles {
		for ni := range p.Nodes {
			for _, tag := range p.Nodes[ni].Tags {
				if v.PrefTags[tag] {
					p.PrefMatch[vi*n+ni] = true
					p.PrefPossible[ni] = true
				}
			}
		}
	}
}

func (p *Problem) compileObjectives(pl *plan.Plan) {
	seen := map[Objective]bool{}
	for _, name := range pl.Objectives {
		idx := slices.Index(objectiveNames[:], name)
		if idx < 0 {
			p.Warnings = append(p.Warnings, fmt.Sprintf("objective %q is not supported and is ignored", name))
			continue
		}
		if o := Objective(idx); !seen[o] {
			seen[o] = true
			p.Objectives = append(p.Objectives, o)
		}
	}
	// Serving every stop always outranks everything else, listed or not.
	if !seen[ObjUnserved] {
		p.Objectives = slices.Insert(p.Objectives, 0, ObjUnserved)
		p.Warnings = append(p.Warnings, "maximizeMandatoryStops was not listed; it is applied as the top priority")
	}
	if len(p.Objectives) == 1 {
		p.Objectives = append(p.Objectives, ObjDist)
		p.Warnings = append(p.Warnings, "no objective besides serving stops was given; minimizing distance")
	}
}

type unionFind []int

func newUnionFind(n int) unionFind {
	u := make(unionFind, n)
	for i := range u {
		u[i] = i
	}
	return u
}

func (u unionFind) find(x int) int {
	for u[x] != x {
		u[x] = u[u[x]]
		x = u[x]
	}
	return x
}

func (u unionFind) union(a, b int) { u[u.find(a)] = u.find(b) }

// groups returns the sets with at least minSize members, in a stable order.
func (u unionFind) groups(minSize int) [][]int {
	byRoot := map[int][]int{}
	for i := range u {
		r := u.find(i)
		byRoot[r] = append(byRoot[r], i)
	}
	var out [][]int
	for _, g := range byRoot {
		if len(g) >= minSize {
			out = append(out, g)
		}
	}
	slices.SortFunc(out, func(a, b []int) int { return cmp.Compare(a[0], b[0]) })
	return out
}
