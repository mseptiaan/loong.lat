// Package analysis turns problems and solutions into compact, human- and
// LLM-readable facts: instance summaries, per-route statistics, territory
// overlap, cluster integrity and rule-based improvement hints.
package analysis

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/mseptiaan/loong.lat/backend/pkg/engine/geo"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/model"
)

// ObjectiveValue is one objective in human units.
type ObjectiveValue struct {
	Objective string  `json:"objective"`
	Value     float64 `json:"value"`
	Unit      string  `json:"unit"`
}

// ObjectiveValues lists the metrics in the plan's priority order, converted to
// units people think in (kilometres, minutes, cost units).
func ObjectiveValues(p *model.Problem, m model.Metrics) []ObjectiveValue {
	out := make([]ObjectiveValue, 0, len(p.Objectives))
	for _, o := range p.Objectives {
		v, unit := float64(m[o]), o.Unit()
		switch o {
		case model.ObjUnserved:
			unit = "stops unserved"
		case model.ObjUnsatPref:
			unit = "unmet preferences"
		case model.ObjDelay:
			v, unit = v/60, "min late"
		case model.ObjCost:
			v, unit = v/1000, "cost units"
		case model.ObjDist:
			v, unit = v/1000, "km"
		case model.ObjMakespan:
			v, unit = v/60, "min (longest tour)"
		case model.ObjWork:
			v, unit = v/60, "min (total work)"
		case model.ObjSumArrival:
			v, unit = v/60, "min (sum of arrival times)"
		case model.ObjBlend:
			unit = "blend"
		}
		out = append(out, ObjectiveValue{Objective: o.PlanName(), Value: round(v, 2), Unit: unit})
	}
	return out
}

func round(v float64, places int) float64 {
	f := math.Pow(10, float64(places))
	return math.Round(v*f) / f
}

// RouteStat summarizes one vehicle's route.
type RouteStat struct {
	Vehicle      string  `json:"vehicle"`
	Used         bool    `json:"used"`
	Orders       int     `json:"orders"`
	Stops        int     `json:"stops"`
	DistanceKm   float64 `json:"distance_km"`
	WorkMin      float64 `json:"work_min"`
	ElapsedMin   float64 `json:"elapsed_min"`
	WaitMin      float64 `json:"wait_min"`
	BreakMin     float64 `json:"break_min"`
	Cost         float64 `json:"cost"`
	Depart       string  `json:"depart_local,omitempty"`
	Arrive       string  `json:"arrive_local,omitempty"`
	LongestLegKm float64 `json:"longest_leg_km"`
	HullKm2      float64 `json:"territory_km2"`
	WorkShare    float64 `json:"work_vs_max_pct,omitempty"` // work as a percentage of the maximum, when one is set
	Trips        int     `json:"trips,omitempty"`           // more than one when the vehicle returns to its depot to unload
}

// Overlap says how much two vehicles' territories intersect.
type Overlap struct {
	A     string  `json:"a"`
	B     string  `json:"b"`
	Ratio float64 `json:"ratio"` // intersection area over the smaller territory
}

// Integrity measures how well clusters survived as units of assignment.
type Integrity struct {
	Clusters      int     `json:"clusters"`
	SplitClusters int     `json:"split_clusters"` // clusters served by more than one vehicle
	Purity        float64 `json:"purity"`         // share of orders served by their cluster's main vehicle
}

// Diagnostics is the analysis of one solution.
type Diagnostics struct {
	Routes     []RouteStat `json:"routes"`
	Overlaps   []Overlap   `json:"overlaps,omitempty"`
	MaxOverlap float64     `json:"max_overlap"`
	Integrity  *Integrity  `json:"cluster_integrity,omitempty"`
	Unserved   []Unserved  `json:"unserved,omitempty"`
	Hints      []string    `json:"hints,omitempty"`
}

// Diagnose analyzes s. orderCluster (order -> cluster) may be nil.
func Diagnose(p *model.Problem, s *model.Solution, orderCluster []int) *Diagnostics {
	d := &Diagnostics{}
	ev := model.NewEvaluator(p)

	proj := projector(p)
	hulls := make([][]geo.XY, len(s.Routes))
	for v, route := range s.Routes {
		st := RouteStat{Vehicle: p.Vehicles[v].ID}
		if len(route) > 0 {
			det := ev.Detail(v, route)
			st.Used = true
			st.Stops = len(route)
			orders := map[int]bool{}
			var pts []geo.XY
			for _, n := range route {
				orders[p.Nodes[n].Order] = true
				if l := p.Nodes[n].Loc; !p.IsDepot[l] {
					pts = append(pts, proj.Project(p.Locs[l]))
				}
			}
			st.Orders = len(orders)
			if det.Eval.OK {
				st.DistanceKm = round(float64(det.Eval.Dist)/1000, 2)
				st.WorkMin = round(float64(det.Eval.Work)/60, 1)
				st.ElapsedMin = round(float64(det.Eval.Elapsed)/60, 1)
				st.WaitMin = round(float64(det.Eval.Wait)/60, 1)
				st.Cost = round(float64(det.Eval.Cost)/1000, 2)
				if det.Trips > 1 {
					st.Trips = det.Trips
				}
				st.Depart = p.Local(det.Depart).Format("15:04")
				st.Arrive = p.Local(det.Arrive).Format("15:04")
				var brk, longest int64
				for _, stop := range det.Stops {
					brk += stop.BreakBefore
					longest = max(longest, stop.TravelM)
				}
				brk += det.EndBreak
				longest = max(longest, det.EndTravelM)
				st.BreakMin = round(float64(brk)/60, 1)
				st.LongestLegKm = round(float64(longest)/1000, 2)
				if mw := p.Vehicles[v].MaxWork; mw > 0 {
					st.WorkShare = round(100*float64(det.Eval.Work)/float64(mw), 1)
				}
			}
			if len(pts) > 0 {
				hulls[v] = geo.BufferedHull(pts, 250)
				st.HullKm2 = round(geo.Area(hulls[v])/1e6, 2)
			}
		}
		d.Routes = append(d.Routes, st)
	}

	for a := range hulls {
		for b := a + 1; b < len(hulls); b++ {
			if hulls[a] == nil || hulls[b] == nil {
				continue
			}
			inter := geo.ConvexIntersectionArea(hulls[a], hulls[b])
			small := math.Min(geo.Area(hulls[a]), geo.Area(hulls[b]))
			if small <= 0 {
				continue
			}
			ratio := round(inter/small, 3)
			d.MaxOverlap = max(d.MaxOverlap, ratio)
			if ratio > 0 {
				d.Overlaps = append(d.Overlaps, Overlap{A: p.Vehicles[a].ID, B: p.Vehicles[b].ID, Ratio: ratio})
			}
		}
	}

	if orderCluster != nil {
		d.Integrity = clusterIntegrity(s, orderCluster)
	}
	d.Unserved = explainUnserved(p, s, ev)
	d.Hints = hints(p, s, d)
	return d
}

func projector(p *model.Problem) geo.Projector {
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

func clusterIntegrity(s *model.Solution, orderCluster []int) *Integrity {
	ov := s.OrderVehicle()
	k := 0
	for _, c := range orderCluster {
		k = max(k, c+1)
	}
	count := make([]map[int]int, k)
	for o, c := range orderCluster {
		if ov[o] < 0 {
			continue
		}
		if count[c] == nil {
			count[c] = map[int]int{}
		}
		count[c][ov[o]]++
	}
	in := &Integrity{}
	var served, majority int
	for _, m := range count {
		if len(m) == 0 {
			continue
		}
		in.Clusters++
		if len(m) > 1 {
			in.SplitClusters++
		}
		top := 0
		for _, n := range m {
			served += n
			top = max(top, n)
		}
		majority += top
	}
	if served > 0 {
		in.Purity = round(float64(majority)/float64(served), 3)
	}
	return in
}

// soloRoute lists an order's stops in a valid sequence for a route of its own.
func soloRoute(p *model.Problem, o int) []int {
	nodes := slices.Clone(p.Orders[o].Nodes)
	if !p.Orders[o].Sequential {
		slices.SortStableFunc(nodes, func(a, b int) int { return int(p.Nodes[a].Kind) - int(p.Nodes[b].Kind) })
	}
	return nodes
}

// Eligibility returns, per order, the vehicles that could serve it alone.
func Eligibility(p *model.Problem) [][]int {
	ev := model.NewEvaluator(p)
	out := make([][]int, len(p.Orders))
	for o := range p.Orders {
		for v := range p.Vehicles {
			if ev.Eval(v, soloRoute(p, o)).OK {
				out[o] = append(out[o], v)
			}
		}
	}
	return out
}

func hints(p *model.Problem, s *model.Solution, d *Diagnostics) []string {
	var h []string
	if len(s.Unassigned) > 0 {
		h = append(h, fmt.Sprintf("%d order(s) are unserved; see the reasons. More iterations or a different clustering may find room for them.", len(s.Unassigned)))
	}
	used := s.VehiclesUsed()
	if used > 0 && used < len(p.Vehicles) {
		var idle []int
		for v, r := range s.Routes {
			if len(r) == 0 {
				idle = append(idle, v)
			}
		}
		sample := make([]string, 0, 3)
		for _, v := range idle[:min(3, len(idle))] {
			sample = append(sample, p.Vehicles[v].ID)
		}
		more := ""
		if len(idle) > len(sample) {
			more = fmt.Sprintf(" and %d more", len(idle)-len(sample))
		}
		h = append(h, fmt.Sprintf("Only %d of %d vehicles are used (idle: %s%s). Under the plan's objective priorities consolidating work is preferred over balancing; if balance matters, list minimizeLargestTourDuration before minimizeDistance/minimizeCosts.", used, len(p.Vehicles), strings.Join(sample, ", "), more))
	}
	if d.MaxOverlap > 0.15 {
		h = append(h, fmt.Sprintf("Vehicle territories overlap by up to %.0f%%: routes criss-cross. A different clustering (kmedoids or agglomerative, or another k) may separate them.", 100*d.MaxOverlap))
	}
	if in := d.Integrity; in != nil && in.SplitClusters > 0 && in.Purity < 0.85 {
		h = append(h, fmt.Sprintf("%d cluster(s) are split over several vehicles (purity %.0f%%): the clustering was not respected.", in.SplitClusters, 100*in.Purity))
	}
	for _, r := range d.Routes {
		if r.WorkShare > 90 {
			h = append(h, fmt.Sprintf("%s uses %.0f%% of its maximum working duration: little slack.", r.Vehicle, r.WorkShare))
		}
		if r.WaitMin > 30 {
			h = append(h, fmt.Sprintf("%s idles %.0f min waiting for time windows.", r.Vehicle, r.WaitMin))
		}
	}
	if s.M[model.ObjUnsatPref] > 0 {
		h = append(h, fmt.Sprintf("%d preference(s) are not met.", s.M[model.ObjUnsatPref]))
	}
	return h
}
