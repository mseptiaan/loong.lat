package quote

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/pkg/routeplan"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/orchestrate"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/roads"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/runner"
)

// Stage is a step of computing a quote, reported as it begins. The wizard shows
// them as the honest progress of the work ("reading the points, measuring the
// roads, combining routes, comparing options").
type Stage int

// The four stages, in order.
const (
	StageReadPoints Stage = iota + 1
	StageMeasure
	StageCombine
	StageCompare
	NumStages = int(StageCompare)
)

// Update is a progress report of Compute: a stage that has begun, or, with Msg set, a step of a thorough search
// inside the combine stage (Stage is then StageCombine).
type Update struct {
	Stage Stage
	Msg   *contracts.Msg
}

// Planner computes quotes.
type Planner struct {
	Roads  roads.Provider
	Runner runner.Runner
	// Router, when set, draws each route on the road network after the options are computed.
	Router roads.Router
	Log    *slog.Logger
	// Limits bound each job; the zero value takes the solver's defaults. A deep quote also gives each job a
	// MaxSeconds of its own from the time left to it (Limits.MaxSeconds, when set, caps it).
	Limits job.Limits
	// AI, when set, lets the model review a reviewed quote whose request allows it; without it the rules review.
	AI aiapi.Model
	// Rounds caps the review rounds of a reviewed quote; zero takes orchestrate.DefaultRounds.
	Rounds int
	// RoadTimeout bounds measuring the roads; on a miss the quote falls back to estimates.
	RoadTimeout time.Duration
	// GeometryTimeout bounds drawing the routes; on a miss they are simply left undrawn.
	GeometryTimeout time.Duration
}

// Result is a computed quote.
type Result struct {
	Set contracts.OptionSet
	// Truncated means a time limit cut a search short, so the quote could differ
	// on a faster machine; such a quote should not be cached as canonical.
	Truncated bool
	// Proven means every candidate was confirmed optimal by an exhaustive search.
	Proven bool
	// RoadSource says whether distances are measured ("mapbox") or estimated.
	RoadSource string
	Engine     string
}

// Compute works out the options for r. progress, which may be nil, is called as each stage begins and, for a
// thorough or reviewed quote, as the search reports its steps inside the combine stage. A fast quote reports the
// four stages and nothing else.
func (p *Planner) Compute(ctx context.Context, r Request, progress func(Update)) (*Result, error) {
	log := p.Log
	if log == nil {
		log = slog.Default()
	}
	report := func(u Update) {
		if progress != nil {
			progress(u)
		}
	}
	step := func(s Stage) { report(Update{Stage: s}) }

	step(StageReadPoints)
	if err := Validate(r.Shipments); err != nil {
		return nil, err
	}
	if !slices.Contains(effortsOf, r.Effort) {
		return nil, invalid("effort", platform.MsgBadRequest, nil)
	}
	fleet := profileOf(r.Vehicles)
	if len(fleet) == 0 {
		return nil, &Error{Code: CodeService, Msg: msg("srv.engine.noVehicles", nil)}
	}
	for _, s := range r.Shipments {
		if _, ok := fleet.smallestFit(aggregateItems(s.Items)); !ok {
			return nil, invalid("shipments", "srv.engine.tooBig", nil)
		}
	}
	sh := newShape(&r, fleet)

	step(StageMeasure)
	matrix, err := p.measure(ctx, sh.net.pts, log)
	if err != nil {
		return nil, err
	}
	sh.net.distM = matrix.DistM

	step(StageCombine)
	out, err := p.search(ctx, &r, sh.job(p.Limits), func(m contracts.Msg) { report(Update{Stage: StageCombine, Msg: &m}) }, log)
	if err != nil {
		return nil, err
	}
	res := out.Result
	if len(res.Unserved) > 0 {
		return nil, invalid("shipments", "srv.engine.tooBig", nil)
	}

	step(StageCompare)
	method := out.Method
	method.RoadSource = matrix.Source
	set, proven, err := sh.assemble(res, method)
	if err != nil {
		return nil, err
	}
	p.draw(ctx, set, log)
	return &Result{Set: *set, Truncated: out.Truncated, Proven: proven, RoadSource: matrix.Source, Engine: res.Engine}, nil
}

// effortsOf lists the efforts a request may name; empty means fast.
var effortsOf = append([]contracts.Effort{""}, contracts.Efforts...)

// search runs the jobs of the quote's effort. A failure of the first job is a quote that cannot be computed; a
// failure later only cuts the search short, and the best answers found by then are the quote.
func (p *Planner) search(ctx context.Context, r *Request, base *job.Job, notify func(contracts.Msg), log *slog.Logger) (*orchestrate.Outcome, error) {
	o := &orchestrate.Orchestrator{Runner: p.Runner, Rounds: p.Rounds, Notify: notify, Log: log}
	if p.Router != nil {
		// Leave the drawing of the routes the time it needs, whatever the search does with its own.
		o.Reserve = p.GeometryTimeout + time.Second
	}
	if r.Effort == contracts.EffortReviewed && r.AIReview && p.AI != nil {
		o.Reviewer = &orchestrate.AIReviewer{Model: p.AI}
	}
	out, err := o.Run(ctx, r.Effort, base)
	if err != nil {
		var rejected *job.Error
		if errors.As(err, &rejected) && rejected.Code == job.CodeInvalid {
			return nil, &Error{Code: CodeInvalid, Msg: msg("srv.engine.tooBig", nil), Field: "shipments"}
		}
		return nil, &Error{Code: CodeService, Msg: msg("srv.engine.unavailable", nil)}
	}
	for _, rv := range out.Reviews {
		// The note is the reviewer's own words, a model's perhaps: it is for the log and nothing else.
		log.Info("review round", "plan", r.PlanID, "round", rv.Round, "by", rv.By, "satisfied", rv.Satisfied, "more", len(rv.Experiments), "fallback", rv.Fallback, "note", rv.Note)
	}
	return out, nil
}

// draw attaches road geometry to every route. It runs the routes side by side
// and gives up quietly: a map without lines is still a correct quote.
func (p *Planner) draw(ctx context.Context, set *contracts.OptionSet, log *slog.Logger) {
	if p.Router == nil {
		return
	}
	if p.GeometryTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.GeometryTimeout)
		defer cancel()
	}
	var wg sync.WaitGroup
	for oi := range set.Options {
		for ri := range set.Options[oi].Routes {
			r := &set.Options[oi].Routes[ri]
			wg.Go(func() {
				way := make([]contracts.LatLng, 0, len(r.Stops)+1)
				way = append(way, r.StartPosition)
				for _, st := range r.Stops {
					way = append(way, st.Waypoint.Position)
				}
				path, err := p.Router.Geometry(ctx, way)
				if err != nil {
					log.Debug("route geometry unavailable", "route", r.ID, "err", err)
					return
				}
				r.Geometry = path
			})
		}
	}
	wg.Wait()
}

// measure gets road distances, and settles for estimates rather than fail a quote.
func (p *Planner) measure(ctx context.Context, pts []contracts.LatLng, log *slog.Logger) (roads.Matrix, error) {
	if p.Roads != nil {
		mctx := ctx
		if p.RoadTimeout > 0 {
			var cancel context.CancelFunc
			mctx, cancel = context.WithTimeout(ctx, p.RoadTimeout)
			defer cancel()
		}
		m, err := p.Roads.Matrix(mctx, pts)
		if err == nil {
			return m, nil
		}
		if ctx.Err() != nil {
			return roads.Matrix{}, ctx.Err()
		}
		log.Warn("road matrix failed, using estimates", "err", err, "places", len(pts))
	}
	return roads.Estimate{}.Matrix(ctx, pts)
}

// newShape places the shipments: distinct places are numbered once, and the
// notional start is the pickup nearest to the middle of the pickups.
func newShape(r *Request, fleet fleetProfile) *shape {
	s := &shape{req: r, fleet: fleet}
	index := map[[2]int64]int{}
	place := func(ll contracts.LatLng) int {
		key := [2]int64{int64(math.Round(ll.Lat * 1e6)), int64(math.Round(ll.Lng * 1e6))}
		if i, ok := index[key]; ok {
			return i
		}
		index[key] = len(s.net.pts)
		s.net.pts = append(s.net.pts, ll)
		return len(s.net.pts) - 1
	}
	for _, sh := range r.Shipments {
		s.pickup = append(s.pickup, place(sh.Pickup.Position))
		s.drop = append(s.drop, place(sh.Dropoff.Position))
	}

	var lat, lng float64
	for _, i := range s.pickup {
		lat += s.net.pts[i].Lat
		lng += s.net.pts[i].Lng
	}
	mid := contracts.LatLng{Lat: lat / float64(len(s.pickup)), Lng: lng / float64(len(s.pickup))}
	s.start = s.pickup[0]
	for _, i := range s.pickup[1:] {
		if routeplan.HaversineKm(s.net.pts[i], mid) < routeplan.HaversineKm(s.net.pts[s.start], mid) {
			s.start = i
		}
	}
	s.startAt = departure(r)
	return s
}

// departure is when the couriers set off: soon for a delivery now, at the start of the window for a scheduled one.
func departure(r *Request) time.Time {
	if r.Schedule.Mode == contracts.ScheduleModeScheduled && r.Schedule.Window != nil {
		return maxTime(r.Now.Add(10*time.Minute), r.Schedule.Window.From.Time)
	}
	return r.Now.Add(6 * time.Minute)
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// job builds what the solver needs: places, distances, the fleet at its speed
// for the hour of departure, and the shipments.
func (s *shape) job(limits job.Limits) *job.Job {
	points := make([]job.Point, len(s.net.pts))
	for i, p := range s.net.pts {
		points[i] = job.Point{Lat: p.Lat, Lng: p.Lng}
	}
	j := &job.Job{
		Version:  job.Version,
		ID:       fmt.Sprintf("%s-%d", s.req.PlanID, s.req.Now.UnixMilli()),
		DepartAt: s.startAt,
		Points:   points,
		DistM:    s.net.distM,
		Presets:  job.Presets,
		Dwell:    job.Dwell{Pickup: routeplan.DwellPickupMin * 60, Drop: routeplan.DwellDropMin * 60},
		Limits:   limits,
	}
	hour := routeplan.HourWIB(s.startAt)
	for _, t := range []contracts.VehicleType{contracts.VehicleTypeMotor, contracts.VehicleTypeBlindvan} {
		v, ok := s.fleet[t]
		if !ok {
			continue
		}
		j.Fleet = append(j.Fleet, job.FleetType{
			Type: string(t), Count: len(s.req.Shipments), SpeedKmh: routeplan.SpeedKmh(t, hour), Start: s.start,
			Capacity: job.Capacity{WeightKg: v.MaxWeightKg, VolumeM3: v.MaxVolumeM3, MaxDimensionCm: v.MaxDimensionCm},
			Fare:     fareConfig(v.Fare),
		})
	}
	for i, sh := range s.req.Shipments {
		agg := aggregateItems(sh.Items)
		js := job.Shipment{ID: sh.ID, Pickup: s.pickup[i], Dropoff: s.drop[i], WeightKg: agg.WeightKg, VolumeM3: agg.VolumeM3, MaxDimensionCm: agg.MaxDimCm}
		if !sh.Deadline.IsZero() {
			d := sh.Deadline.Time
			js.Deadline = &d
		}
		j.Shipments = append(j.Shipments, js)
	}
	return j
}
