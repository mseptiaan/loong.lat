package plans

import (
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/routeplan"
)

// anomalyDelayMin is the delay from which a running route counts as an anomaly.
const anomalyDelayMin = 10

// notSelected stands in for a place that is not known yet (a draft without shipments). The frontend's
// PlanSummary labels are plain strings, so this is the one text the console writes in a language: English,
// the frontend's default.
const notSelected = "Not selected yet"

// Progress counts finished stops of the plan's routes.
type Progress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// Summary is the frontend's PlanSummary: one row of the plan list.
type Summary struct {
	ID           string                 `json:"id"`
	Code         string                 `json:"code"`
	UserID       string                 `json:"userId"`
	CustomerName string                 `json:"customerName"`
	Status       contracts.PlanStatus   `json:"status"`
	CreatedAt    contracts.Time         `json:"createdAt"`
	UpdatedAt    contracts.Time         `json:"updatedAt"`
	ScheduleMode contracts.ScheduleMode `json:"scheduleMode"`
	StartAt      contracts.Time         `json:"startAt,omitzero"`
	Preset       contracts.Preset       `json:"preset"`
	Shipments    int                    `json:"shipments"`
	Stops        int                    `json:"stops"`
	FromLabel    string                 `json:"fromLabel"`
	ToLabel      string                 `json:"toLabel"`
	TotalCostRp  *int64                 `json:"totalCostRp,omitempty"`
	DistanceKm   *float64               `json:"distanceKm,omitempty"`
	DurationMin  *float64               `json:"durationMin,omitempty"`
	// VehicleTypes has one entry per route: two motorbike routes are ["motor", "motor"].
	VehicleTypes []contracts.VehicleType `json:"vehicleTypes"`
	// DriverNames is always empty: driver master data is not part of what the console follows.
	DriverNames         []string `json:"driverNames"`
	RoutesWithoutDriver int      `json:"routesWithoutDriver"`
	Progress            Progress `json:"progress"`
	SavingsPct          *float64 `json:"savingsPct,omitempty"`
	SavingsRp           *int64   `json:"savingsRp,omitempty"`
	DraftStep           int      `json:"draftStep"`
	// DelayMin is the largest delay among routes that are not finished.
	DelayMin     float64 `json:"delayMin"`
	ProblemStops int     `json:"problemStops"`
	HasAnomaly   bool    `json:"hasAnomaly"`
	// PendingAction mirrors Plan.PendingAction ("cancel" while the drivers' votes are awaited) so list rows can say
	// "Cancelling…"; absent otherwise.
	PendingAction string `json:"pendingAction,omitempty"`
}

// Summarize derives a plan's list row from the plan itself.
func Summarize(p contracts.Plan, customerName string) Summary {
	var selected *contracts.PlanOption
	if p.OptionSet != nil {
		if i := slices.IndexFunc(p.OptionSet.Options, func(o contracts.PlanOption) bool { return o.ID == p.SelectedOptionID }); i >= 0 {
			selected = &p.OptionSet.Options[i]
		}
	}
	var stops []contracts.RouteStop
	for _, r := range p.Routes {
		stops = append(stops, r.Stops...)
	}

	s := Summary{
		ID: p.ID, Code: p.Code, UserID: p.UserID, CustomerName: customerName, Status: p.Status,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, ScheduleMode: p.Schedule.Mode, Preset: p.Priority.Preset,
		Shipments: len(p.Shipments), Stops: len(stops), FromLabel: notSelected, ToLabel: notSelected,
		VehicleTypes: []contracts.VehicleType{}, DriverNames: []string{}, DraftStep: p.DraftStep,
		Progress: Progress{Total: len(stops)}, PendingAction: p.PendingAction,
	}
	if s.Stops == 0 {
		s.Stops = len(p.Shipments) * 2
	}

	switch {
	case len(p.Routes) > 0:
		s.StartAt = p.Routes[0].StartAt
	case p.Schedule.Window != nil:
		s.StartAt = p.Schedule.Window.From
	}
	if len(stops) > 0 {
		s.FromLabel = p.Routes[0].Stops[0].Waypoint.Name
		last := p.Routes[len(p.Routes)-1]
		s.ToLabel = last.Stops[len(last.Stops)-1].Waypoint.Name
	} else if len(p.Shipments) > 0 {
		s.FromLabel = p.Shipments[0].Pickup.Name
		s.ToLabel = p.Shipments[len(p.Shipments)-1].Dropoff.Name
	}
	if len(p.Shipments) > 1 {
		s.ToLabel += " +" + strconv.Itoa(len(p.Shipments)-1)
	}

	routes := p.Routes
	if len(routes) == 0 && selected != nil {
		routes = selected.Routes
	}
	for _, r := range routes {
		s.VehicleTypes = append(s.VehicleTypes, r.VehicleType)
	}
	switch {
	case len(p.Routes) > 0:
		var km, minutes float64
		for _, r := range p.Routes {
			km += r.DistanceKm
			minutes = max(minutes, r.DurationMin)
		}
		s.DistanceKm, s.DurationMin = new(routeplan.Round1(km)), new(minutes)
	case selected != nil:
		s.DistanceKm, s.DurationMin = new(selected.Totals.DistanceKm), new(selected.Totals.DurationMin)
	}
	switch {
	case p.TotalCostRp != 0:
		s.TotalCostRp = new(p.TotalCostRp)
	case selected != nil:
		s.TotalCostRp = new(selected.Totals.CostRp)
	}
	if selected != nil {
		s.SavingsPct, s.SavingsRp = new(selected.Savings.Pct), new(selected.Savings.CostRp)
	}

	switch p.Status {
	case contracts.PlanStatusAwaitingDriver, contracts.PlanStatusAssigned, contracts.PlanStatusRunning:
		for _, r := range p.Routes {
			if r.DriverID == "" && r.Status != contracts.RouteStatusDone {
				s.RoutesWithoutDriver++
			}
		}
	}
	for _, st := range stops {
		switch st.Status {
		case contracts.StopStatusDone:
			s.Progress.Done++
		case contracts.StopStatusProblem:
			s.Progress.Done++
			s.ProblemStops++
		}
	}
	var delay float64
	for _, r := range p.Routes {
		if r.Status != contracts.RouteStatusDone {
			delay = max(delay, r.DelayMin)
		}
	}
	s.DelayMin = math.Round(delay)
	s.HasAnomaly = delay >= anomalyDelayMin || s.ProblemStops > 0
	return s
}

// matches reports whether the row passes the frontend's PlanFilter (userId is checked by the caller).
func (s Summary) matches(f Filter) bool {
	switch {
	case len(f.Status) > 0 && !slices.Contains(f.Status, s.Status):
		return false
	case !f.From.IsZero() && s.CreatedAt.Before(f.From.Time):
		return false
	case !f.To.IsZero() && s.CreatedAt.After(f.To.Time):
		return false
	case f.NeedsDriver && s.RoutesWithoutDriver == 0:
		return false
	case f.VehicleType != "" && !slices.Contains(s.VehicleTypes, f.VehicleType):
		return false
	}
	if q := strings.ToLower(strings.TrimSpace(f.Q)); q != "" {
		fields := append([]string{s.Code, s.CustomerName, s.FromLabel, s.ToLabel}, s.DriverNames...)
		return slices.ContainsFunc(fields, func(field string) bool { return strings.Contains(strings.ToLower(field), q) })
	}
	return true
}
