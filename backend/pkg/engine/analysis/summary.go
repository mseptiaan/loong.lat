package analysis

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/mseptiaan/loong.lat/backend/pkg/engine/geo"
	"github.com/mseptiaan/loong.lat/backend/pkg/engine/model"
)

// VehicleSummary describes a group of identical vehicles (same road profile,
// working window, capacities, limits and skills).
type VehicleSummary struct {
	Count        int                `json:"count"`
	IDs          []string           `json:"ids"`
	Profile      string             `json:"road_profile"`
	WindowLocal  string             `json:"working_window_local"`
	Capacities   map[string]float64 `json:"capacities,omitempty"`
	Skills       []string           `json:"skills,omitempty"`
	MaxWorkHours float64            `json:"max_work_hours,omitempty"`
	Break        string             `json:"break,omitempty"`
	// MultiTrip vehicles return to the depot when full, unload, and set out
	// again, so their capacity limits one trip and not the whole day.
	MultiTrip      bool     `json:"multi_trip,omitempty"`
	TurnaroundMin  float64  `json:"turnaround_min,omitempty"`
	CostModel      string   `json:"cost,omitempty"`
	CanCarryGoods  bool     `json:"can_carry_goods"`
	PreferredTags  []string `json:"preferred_stop_tags,omitempty"`
	EligibleOrders int      `json:"orders_each_can_serve_alone"`
}

// Summary is what the analyst agent sees: enough to choose a strategy, small
// enough to read at a glance.
type Summary struct {
	Plan             string           `json:"plan"`
	Timezone         string           `json:"timezone"`
	Vehicles         []VehicleSummary `json:"vehicles"`
	Orders           int              `json:"orders"`
	Stops            int              `json:"stops"`
	Locations        int              `json:"unique_locations"`
	Objectives       []string         `json:"objectives_in_priority_order"`
	Geography        map[string]any   `json:"geography"`
	Workload         map[string]any   `json:"workload"`
	Constraints      []string         `json:"constraints"`
	MustStayTogether [][]string       `json:"must_share_a_vehicle,omitempty"`
	RestrictedOrders []string         `json:"orders_only_some_vehicles_can_serve,omitempty"`
	Notes            []string         `json:"notes,omitempty"`
}

// Summarize describes the problem. The travel matrices must be installed.
func Summarize(p *model.Problem) *Summary {
	s := &Summary{
		Plan: p.Name, Timezone: p.TZ.String(),
		Orders: len(p.Orders), Stops: len(p.Nodes), Locations: len(p.Locs),
		Objectives: p.ObjectiveOrder(), Notes: slices.Clone(p.Warnings),
	}
	elig := Eligibility(p)

	// Vehicles that are identical in every respect that matters are described once.
	groupOf := map[string]int{}
	var first []int // first vehicle of each group
	eligible := make([]int, len(p.Vehicles))
	for _, vs := range elig {
		for _, v := range vs {
			eligible[v]++
		}
	}
	for v := range p.Vehicles {
		vs := describeVehicle(p, v)
		key := fmt.Sprint(vs.Profile, vs.WindowLocal, vs.Capacities, vs.Skills, vs.MaxWorkHours, vs.Break, vs.MultiTrip, vs.TurnaroundMin, vs.CostModel, vs.PreferredTags, p.Vehicles[v].Start, p.Vehicles[v].End)
		gi, ok := groupOf[key]
		if !ok {
			gi = len(s.Vehicles)
			groupOf[key] = gi
			s.Vehicles = append(s.Vehicles, vs)
			first = append(first, v)
		}
		g := &s.Vehicles[gi]
		g.Count++
		if len(g.IDs) < 3 {
			g.IDs = append(g.IDs, p.Vehicles[v].ID)
		}
	}
	for gi, v := range first {
		s.Vehicles[gi].EligibleOrders = eligible[v]
	}

	var service int64
	var pickups, deliveries, authorized, preferred int
	demand := make([]float64, len(p.Dims))
	for _, n := range p.Nodes {
		service += n.Service
		if n.Kind == model.Pickup {
			pickups++
		} else {
			deliveries++
		}
		if len(n.Auth) > 0 && n.Auth[0].Begin > -model.Inf {
			authorized++
		}
		if len(n.Pref) > 0 {
			preferred++
		}
		if n.Demand != nil && n.Kind == model.Delivery {
			for k := range demand {
				demand[k] += n.Demand[k]
			}
		}
	}
	proj := projector(p)
	var pts []geo.XY
	var maxFrom float64
	for l, c := range p.Locs {
		if p.IsDepot[l] {
			continue
		}
		pts = append(pts, proj.Project(c))
		maxFrom = max(maxFrom, geo.Haversine(p.Locs[p.Vehicles[0].Start], c))
	}
	var diameter float64
	for i := range pts {
		for j := i + 1; j < len(pts); j++ {
			diameter = max(diameter, pts[i].Dist(pts[j]))
		}
	}
	s.Geography = map[string]any{
		"farthest_stop_from_depot_km": round(maxFrom/1000, 1),
		"diameter_of_stops_km":        round(diameter/1000, 1),
		"depots":                      depotCount(p),
	}

	nnWork := nearestNeighbourWork(p)
	need := map[string]float64{}
	for k, d := range p.Dims {
		if demand[k] > 0 {
			need[d] = demand[k]
		}
	}
	s.Workload = map[string]any{
		"pickups":                         pickups,
		"deliveries":                      deliveries,
		"service_hours":                   round(float64(service)/3600, 2),
		"one_vehicle_work_hours_estimate": round(float64(nnWork+service)/3600, 2),
		"total_delivery_demand":           need,
	}

	if authorized > 0 {
		s.Constraints = append(s.Constraints, fmt.Sprintf("%d stop(s) have hard (authorized) time windows", authorized))
	}
	if preferred > 0 {
		s.Constraints = append(s.Constraints, fmt.Sprintf("%d stop(s) have soft (preferred) time windows", preferred))
	}
	for _, gc := range p.GC {
		s.Constraints = append(s.Constraints, fmt.Sprintf("global constraint %q (max cumulated cost %.0f over tags %s)", gc.Name, gc.Max, strings.Join(gc.Tags, ", ")))
	}
	for _, g := range p.Affinity {
		ids := make([]string, len(g))
		for i, o := range g {
			ids[i] = p.Orders[o].ID
		}
		s.MustStayTogether = append(s.MustStayTogether, ids)
	}
	// Group orders that not every vehicle can serve by the set that can.
	byEligible := map[string][]string{}
	for o, vs := range elig {
		if len(vs) == len(p.Vehicles) {
			continue
		}
		key := vehicleSet(p, vs)
		byEligible[key] = append(byEligible[key], p.Orders[o].ID)
	}
	for _, key := range slices.Sorted(maps.Keys(byEligible)) {
		ids := byEligible[key]
		shown := ids[:min(3, len(ids))]
		more := ""
		if len(ids) > len(shown) {
			more = fmt.Sprintf(", … (+%d more)", len(ids)-len(shown))
		}
		s.RestrictedOrders = append(s.RestrictedOrders, fmt.Sprintf("%d order(s) can only be served by %s: %s%s", len(ids), key, strings.Join(shown, ", "), more))
	}
	return s
}

// describeVehicle summarizes vehicle v (without count and ids).
func describeVehicle(p *model.Problem, v int) VehicleSummary {
	veh := &p.Vehicles[v]
	vs := VehicleSummary{
		Profile:     p.Profiles[veh.Profile].String(),
		WindowLocal: fmt.Sprintf("%s-%s", p.Local(veh.Window.Begin).Format("15:04"), p.Local(veh.Window.End).Format("15:04")),
		Skills:      veh.SkillNames, MaxWorkHours: float64(veh.MaxWork) / 3600, PreferredTags: sortedKeys(veh.PrefTags),
	}
	if veh.Break != nil {
		vs.Break = fmt.Sprintf("%.0f min after every %.1f h of work", float64(veh.Break.Min)/60, float64(veh.Break.MaxInter)/3600)
	}
	if veh.MultiTrip {
		vs.MultiTrip, vs.TurnaroundMin = true, round(float64(veh.Turnaround)/60, 1)
	}
	if fb := veh.Fixed; fb != nil {
		vs.Break = fmt.Sprintf("rest %s-%s", p.Local(fb.Begin).Format("15:04"), p.Local(fb.End).Format("15:04"))
	}
	if c := veh.Cost; c != nil {
		var parts []string
		if c.Fixed > 0 {
			parts = append(parts, fmt.Sprintf("%g per vehicle used", c.Fixed))
		}
		if c.PerKm > 0 {
			parts = append(parts, fmt.Sprintf("%g per km", c.PerKm))
		}
		if c.PerHour > 0 {
			parts = append(parts, fmt.Sprintf("%g per working hour", c.PerHour))
		}
		if c.PerOrder > 0 {
			parts = append(parts, fmt.Sprintf("%g per order", c.PerOrder))
		}
		if sc := c.Surcharge; sc != nil {
			parts = append(parts, fmt.Sprintf("%g per %s above %g", sc.PerUnit, sc.Dimension, sc.Above))
		}
		if w := c.WorkedHours; w != nil {
			parts = append(parts, fmt.Sprintf("at least %.0f h billed, ×%.2f beyond %.0f h", w.CostFloor, w.OvercostCoeff, w.OvercostFloor))
		}
		vs.CostModel = strings.Join(parts, " + ")
	}
	for k, c := range veh.Cap {
		if c > 0 {
			vs.CanCarryGoods = true
			if vs.Capacities == nil {
				vs.Capacities = map[string]float64{}
			}
			vs.Capacities[p.Dims[k]] = c
		}
	}
	return vs
}

// vehicleSet labels a set of vehicles compactly: every id when there are few,
// otherwise the count and a sample.
func vehicleSet(p *model.Problem, vs []int) string {
	if len(vs) == 0 {
		return "nobody"
	}
	names := make([]string, 0, 4)
	for _, v := range vs[:min(3, len(vs))] {
		names = append(names, p.Vehicles[v].ID)
	}
	if len(vs) <= 3 {
		return "[" + strings.Join(names, ", ") + "]"
	}
	return fmt.Sprintf("[%d vehicles, e.g. %s]", len(vs), strings.Join(names, ", "))
}

func depotCount(p *model.Problem) int {
	n := 0
	for _, d := range p.IsDepot {
		if d {
			n++
		}
	}
	return n
}

// nearestNeighbourWork estimates the travel seconds of one tour through every
// non-depot location, a rough yardstick for whether one vehicle could do it all.
func nearestNeighbourWork(p *model.Problem) int64 {
	mat := p.Mats[0]
	cur := p.Vehicles[0].Start
	var todo []int
	for l := range p.Locs {
		if !p.IsDepot[l] {
			todo = append(todo, l)
		}
	}
	var total int64
	for len(todo) > 0 {
		bi, bd := 0, model.Inf
		for i, l := range todo {
			if d := mat.TravelTime(cur, l); d < bd {
				bi, bd = i, d
			}
		}
		total += bd
		cur = todo[bi]
		todo = slices.Delete(todo, bi, bi+1)
	}
	if p.Vehicles[0].Open {
		return total
	}
	return total + mat.TravelTime(cur, p.Vehicles[0].End)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
