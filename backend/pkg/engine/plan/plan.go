// Package plan defines the JSON schema of a routing plan (resources, orders,
// objectives, global constraints) and loads it from disk.
package plan

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Plan is the root document describing what has to be optimized.
type Plan struct {
	TZ                string             `json:"tz"`
	Resources         []Resource         `json:"resources"`
	Orders            []Order            `json:"orders"`
	Objectives        []string           `json:"objectives"`
	GlobalConstraints []GlobalConstraint `json:"globalConstraints,omitzero"`
	// Blend gives the weights of the minimizeBlend objective. Extension of the base schema.
	Blend      *Blend         `json:"blend,omitzero"`
	Properties map[string]any `json:"properties,omitzero"`
}

// Blend is the integer weighting behind the minimizeBlend objective: the blend
// of a solution is Cost*cost(milli units) + SumArrival*s + Dist*m + Makespan*s.
// Integers keep the comparison exact and identical on every platform; callers
// scale each weight by the inverse of a reference value for that component.
type Blend struct {
	Cost       int64 `json:"cost,omitzero"`
	SumArrival int64 `json:"sumArrival,omitzero"`
	Dist       int64 `json:"dist,omitzero"`
	Makespan   int64 `json:"makespan,omitzero"`
}

// Name returns the human readable plan name, if any.
func (p *Plan) Name() string {
	if s, ok := p.Properties["name"].(string); ok {
		return s
	}
	return "unnamed plan"
}

// Position is a WGS84 coordinate.
type Position struct {
	Lon float64 `json:"lon"`
	Lat float64 `json:"lat"`
}

// TimeWindow is a closed interval of absolute time.
type TimeWindow struct {
	Begin time.Time `json:"begin"`
	End   time.Time `json:"end"`
}

// Resource is a vehicle together with its driver.
type Resource struct {
	ID                string             `json:"id"`
	VehicleProfile    VehicleProfile     `json:"vehicleProfile"`
	WorkingTimeWindow *TimeWindow        `json:"workingTimeWindow,omitzero"`
	Departure         *Position          `json:"departure,omitzero"`
	Arrival           *Position          `json:"arrival,omitzero"`
	Capacities        map[string]float64 `json:"capacities,omitzero"`
	PreferredStopTags []string           `json:"preferredStopTags,omitzero"`
	Tags              []string           `json:"tags,omitzero"`
	// Skills are the capabilities this resource offers; an order may only be
	// served by a resource that has every skill it requires. Extension of the base schema.
	Skills             []string      `json:"skills,omitzero"`
	Breaks             []Break       `json:"breaks,omitzero"`
	MaxWorkingDuration Duration      `json:"maxWorkingDuration,omitzero"`
	Cost               *ResourceCost `json:"cost,omitzero"`
	// MultiTrip lets the resource return to its arrival location during the
	// day, unload what it has collected, and set out again, so that its capacity
	// limits each trip instead of the whole day. Extension of the base schema.
	MultiTrip bool `json:"multiTrip,omitzero"`
	// TurnaroundDuration is the time spent at the arrival location between two
	// trips. It only matters with MultiTrip. Extension of the base schema.
	TurnaroundDuration Duration `json:"turnaroundDuration,omitzero"`
	// OpenRoute ends the route at its last stop instead of driving back to the
	// arrival location (a courier who finishes where the last parcel is dropped).
	// The final leg then costs no distance, time or money. Extension of the base schema.
	OpenRoute bool `json:"openRoute,omitzero"`
}

// VehicleProfile selects the road network profile used for travel estimates.
type VehicleProfile struct {
	Type        string `json:"type"`
	WithTraffic bool   `json:"withTraffic,omitzero"`
	// AvgSpeedKmh, when set, replaces the road network's travel times: a leg
	// takes its road distance divided by this speed. Extension of the base schema.
	AvgSpeedKmh float64 `json:"avgSpeedKmh,omitzero"`
}

// Break types.
const (
	// BreakSliding is a rest of MinBreakDuration after at most
	// MaxInterBreakDuration of work, taken whenever it is needed.
	BreakSliding = "workingDurationSlidingBreak"
	// BreakFixed is a rest at a fixed time of day, such as a lunch hour:
	// from Begin to End the resource neither drives nor serves. Extension of
	// the base schema.
	BreakFixed = "fixedBreak"
)

// Break is a mandatory rest rule. A resource follows one break rule.
type Break struct {
	Type                  string   `json:"type"`
	MinBreakDuration      Duration `json:"minBreakDuration,omitzero"`
	MaxInterBreakDuration Duration `json:"maxInterBreakDuration,omitzero"`
	// Begin and End bound a fixedBreak.
	Begin time.Time `json:"begin,omitzero"`
	End   time.Time `json:"end,omitzero"`
}

// ResourceCost describes how using a resource is priced. All parts add up and
// apply only to resources that are used.
type ResourceCost struct {
	WorkedHours *WorkedHoursCost `json:"workedHours,omitzero"`
	// Fixed is charged once per used resource. Extension of the base schema.
	Fixed float64 `json:"fixed,omitzero"`
	// PerKm is charged per kilometre driven. Extension of the base schema.
	PerKm float64 `json:"perKm,omitzero"`
	// PerHour is charged per hour of working time, waiting included.
	// Extension of the base schema.
	PerHour float64 `json:"perHour,omitzero"`
	// PerOrder is charged for every order the route serves. Extension of the base schema.
	PerOrder float64 `json:"perOrder,omitzero"`
	// Surcharge prices the goods the route carries. Extension of the base schema.
	Surcharge *LoadSurcharge `json:"surcharge,omitzero"`
	// FreeApproach leaves the leg to the first stop out of the distance and the
	// working time that are billed: the customer does not pay for the courier's
	// way to the pickup. The distance objective follows the bill and does not
	// count that leg either. Extension of the base schema.
	FreeApproach bool `json:"freeApproach,omitzero"`
}

// LoadSurcharge charges PerUnit for every unit of a capacity dimension that the
// goods loaded on a route weigh beyond Above (for example per kg above 10 kg).
type LoadSurcharge struct {
	Dimension string  `json:"dimension"`
	Above     float64 `json:"above"`
	PerUnit   float64 `json:"perUnit"`
}

// Billing is what a route is priced on. The evaluator and the validator both
// derive it from their own timelines and price it with Milli.
type Billing struct {
	DistM   int64   // billable meters
	WorkSec int64   // billable working seconds
	Orders  int     // orders served by the route
	Load    float64 // goods loaded on the route, in the unit of the surcharge dimension
}

// Milli prices a route in thousandths of a cost unit. It is the single
// definition of cost shared by the evaluator and the validator, so that both
// round alike.
func (c *ResourceCost) Milli(b Billing) int64 {
	hours := float64(b.WorkSec) / 3600
	v := c.Fixed*1000 + c.PerKm*float64(b.DistM) + c.PerHour*1000*hours + c.PerOrder*1000*float64(b.Orders)
	if s := c.Surcharge; s != nil {
		v += s.PerUnit * 1000 * math.Max(0, b.Load-s.Above)
	}
	if w := c.WorkedHours; w != nil {
		billed := math.Max(w.CostFloor, hours)
		if w.OvercostCoeff > 1 && hours > w.OvercostFloor {
			billed += (w.OvercostCoeff - 1) * (hours - w.OvercostFloor)
		}
		v += billed * 1000
	}
	return int64(math.Round(v))
}

// WorkedHoursCost prices worked hours with a minimum paid amount and an
// overtime multiplier beyond a threshold.
type WorkedHoursCost struct {
	CostFloor     float64 `json:"costFloor"`
	OvercostFloor float64 `json:"overcostFloor"`
	OvercostCoeff float64 `json:"overcostCoeff"`
}

// Order groups the stops that must be handled by the same resource.
type Order struct {
	ID              string `json:"id"`
	Stops           []Stop `json:"stops"`
	SuccessiveStops bool   `json:"successiveStops,omitzero"`
	// RequiredSkills lists skills a resource needs to serve this order.
	// Extension of the base schema.
	RequiredSkills []string `json:"requiredSkills,omitzero"`
}

// Stop is a single location to visit.
type Stop struct {
	Type                  string             `json:"type"`
	ID                    string             `json:"id"`
	Position              Position           `json:"position"`
	Kind                  string             `json:"kind"`
	OperationDuration     Duration           `json:"operationDuration,omitzero"`
	Capacities            map[string]float64 `json:"capacities,omitzero"`
	Tags                  []string           `json:"tags,omitzero"`
	AuthorizedTimeWindows []TimeWindow       `json:"authorizedTimeWindows,omitzero"`
	PreferredTimeWindows  []TimeWindow       `json:"preferredTimeWindows,omitzero"`
}

// GlobalConstraint limits an aggregated quantity over the whole solution.
type GlobalConstraint struct {
	Type               string                      `json:"type"`
	Maximum            float64                     `json:"maximum"`
	Name               string                      `json:"name,omitzero"`
	CostsByResourceTag map[string]ResourceTagCosts `json:"costsByResourceTag,omitzero"`
}

// ResourceTagCosts is the cost schedule that applies to resources carrying a tag.
type ResourceTagCosts struct {
	CostsByStopTag map[string]StopTagCost `json:"costsByStopTag,omitzero"`
}

// StopTagCost is charged to a resource that visits stops carrying the tag.
type StopTagCost struct {
	Using float64 `json:"using"`
}

// Stop kinds.
const (
	KindPickup   = "pickup"
	KindDelivery = "delivery"
)

// Load reads and validates a plan file. The returned warnings list problems
// that do not prevent solving but may change the interpretation of the plan,
// for instance fields the solver does not understand.
func Load(path string) (*Plan, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read plan: %w", err)
	}
	return Parse(data)
}

// Parse decodes and validates a plan document.
func Parse(data []byte) (*Plan, []string, error) {
	var p Plan
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, nil, fmt.Errorf("decode plan: %w", err)
	}
	var warnings []string
	var strict Plan
	if err := json.Unmarshal(data, &strict, json.RejectUnknownMembers(true)); err != nil {
		warnings = append(warnings, "plan has fields the solver does not understand and will ignore: "+err.Error())
	}
	if err := p.Validate(); err != nil {
		return nil, nil, err
	}
	return &p, warnings, nil
}

// Validate checks structural invariants that the solver relies on.
func (p *Plan) Validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if len(p.Resources) == 0 {
		add("plan has no resources")
	}
	if len(p.Orders) == 0 {
		add("plan has no orders")
	}
	if p.TZ != "" {
		if _, err := time.LoadLocation(p.TZ); err != nil {
			add("invalid tz %q: %v", p.TZ, err)
		}
	}

	seen := map[string]bool{}
	for i := range p.Resources {
		r := &p.Resources[i]
		if r.ID == "" {
			add("resource #%d has no id", i)
		}
		if seen["r:"+r.ID] {
			add("duplicate resource id %q", r.ID)
		}
		seen["r:"+r.ID] = true
		if r.Departure == nil && r.Arrival == nil {
			add("resource %q needs a departure or an arrival position", r.ID)
		}
		for _, pos := range []*Position{r.Departure, r.Arrival} {
			if pos != nil && !validPosition(*pos) {
				add("resource %q has an invalid position %+v", r.ID, *pos)
			}
		}
		if w := r.WorkingTimeWindow; w != nil && !w.End.After(w.Begin) {
			add("resource %q has an empty working time window", r.ID)
		}
		for dim, v := range r.Capacities {
			if v < 0 || math.IsNaN(v) {
				add("resource %q has invalid capacity %q=%v", r.ID, dim, v)
			}
		}
		if v := r.VehicleProfile.AvgSpeedKmh; v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			add("resource %q has an invalid avgSpeedKmh %v", r.ID, v)
		}
		if c := r.Cost; c != nil {
			if c.Fixed < 0 || c.PerKm < 0 || c.PerHour < 0 || c.PerOrder < 0 {
				add("resource %q has a negative cost", r.ID)
			}
			if sc := c.Surcharge; sc != nil && (sc.Dimension == "" || sc.Above < 0 || sc.PerUnit < 0) {
				add("resource %q has an invalid load surcharge", r.ID)
			}
		}
	}

	stopIDs := map[string]bool{}
	for i := range p.Orders {
		o := &p.Orders[i]
		if o.ID == "" {
			add("order #%d has no id", i)
		}
		if seen["o:"+o.ID] {
			add("duplicate order id %q", o.ID)
		}
		seen["o:"+o.ID] = true
		if len(o.Stops) == 0 {
			add("order %q has no stops", o.ID)
		}
		for j := range o.Stops {
			s := &o.Stops[j]
			if s.ID == "" {
				add("order %q stop #%d has no id", o.ID, j)
			}
			if stopIDs[s.ID] {
				add("duplicate stop id %q", s.ID)
			}
			stopIDs[s.ID] = true
			if s.Type != "" && s.Type != "single" {
				add("stop %q has unsupported type %q (only \"single\" is supported)", s.ID, s.Type)
			}
			if s.Kind != KindPickup && s.Kind != KindDelivery {
				add("stop %q has unsupported kind %q", s.ID, s.Kind)
			}
			if !validPosition(s.Position) {
				add("stop %q has an invalid position %+v", s.ID, s.Position)
			}
			for _, w := range slices.Concat(s.AuthorizedTimeWindows, s.PreferredTimeWindows) {
				if w.End.Before(w.Begin) {
					add("stop %q has a time window that ends before it begins", s.ID)
				}
			}
			for dim, v := range s.Capacities {
				if v < 0 || math.IsNaN(v) {
					add("stop %q has invalid demand %q=%v", s.ID, dim, v)
				}
			}
		}
	}
	return errors.Join(errs...)
}

func validPosition(p Position) bool {
	return p.Lat >= -90 && p.Lat <= 90 && p.Lon >= -180 && p.Lon <= 180 && (p.Lat != 0 || p.Lon != 0)
}

// Marshal renders the plan as indented JSON with a stable key order.
func (p *Plan) Marshal() ([]byte, error) {
	return json.Marshal(p, json.Deterministic(true), jsontext.WithIndent("  "))
}

// WriteFile writes the plan as JSON, creating the file if needed.
func (p *Plan) WriteFile(path string) error {
	data, err := p.Marshal()
	if err != nil {
		return fmt.Errorf("encode plan: %w", err)
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// Duration is an ISO-8601 duration such as PT1H30M, decoded into a time.Duration.
type Duration time.Duration

// Std converts to the standard library type.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// UnmarshalJSON implements json.Unmarshaler.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duration must be an ISO-8601 string: %w", err)
	}
	v, err := ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// MarshalJSON implements json.Marshaler.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(FormatDuration(time.Duration(d)))
}

// ParseDuration parses the ISO-8601 duration subset P[nD][T[nH][nM][nS]].
// Years and months are rejected because their length is ambiguous.
func ParseDuration(s string) (time.Duration, error) {
	orig := s
	rest, ok := strings.CutPrefix(s, "P")
	if !ok {
		return 0, fmt.Errorf("invalid ISO-8601 duration %q", orig)
	}
	datePart, timePart, hasT := strings.Cut(rest, "T")
	if rest == "" || (hasT && timePart == "") {
		return 0, fmt.Errorf("invalid ISO-8601 duration %q", orig)
	}
	var total float64
	parse := func(part string, units map[byte]float64) error {
		for part != "" {
			i := 0
			for i < len(part) && (part[i] >= '0' && part[i] <= '9' || part[i] == '.') {
				i++
			}
			if i == 0 || i == len(part) {
				return fmt.Errorf("invalid ISO-8601 duration %q", orig)
			}
			n, err := strconv.ParseFloat(part[:i], 64)
			if err != nil {
				return fmt.Errorf("invalid ISO-8601 duration %q: %w", orig, err)
			}
			mult, ok := units[part[i]]
			if !ok {
				return fmt.Errorf("unsupported unit %q in ISO-8601 duration %q", part[i], orig)
			}
			total += n * mult
			part = part[i+1:]
		}
		return nil
	}
	if err := parse(datePart, map[byte]float64{'D': 86400, 'W': 7 * 86400}); err != nil {
		return 0, err
	}
	if err := parse(timePart, map[byte]float64{'H': 3600, 'M': 60, 'S': 1}); err != nil {
		return 0, err
	}
	return time.Duration(total * float64(time.Second)), nil
}

// FormatDuration renders a duration as ISO-8601 (PT#H#M#S).
func FormatDuration(d time.Duration) string {
	if d <= 0 {
		return "PT0S"
	}
	secs := int64(d.Round(time.Second) / time.Second)
	h, m, s := secs/3600, secs%3600/60, secs%60
	var b strings.Builder
	b.WriteString("PT")
	if h > 0 {
		fmt.Fprintf(&b, "%dH", h)
	}
	if m > 0 {
		fmt.Fprintf(&b, "%dM", m)
	}
	if s > 0 || (h == 0 && m == 0) {
		fmt.Fprintf(&b, "%dS", s)
	}
	return b.String()
}
