package plans

import (
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

// anomalyDelayMin is the delay from which a route counts as an anomaly for the admin console.
const anomalyDelayMin = 10

// Summary is a row of the admin's plan table: the frontend's PlanSummary.
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
	// FromLabel and ToLabel name the first and last place for the table row ("Kemang Village ... Plaza Senayan +1").
	FromLabel           string                  `json:"fromLabel"`
	ToLabel             string                  `json:"toLabel"`
	TotalCostRp         int64                   `json:"totalCostRp,omitzero"`
	DistanceKm          float64                 `json:"distanceKm,omitzero"`
	DurationMin         float64                 `json:"durationMin,omitzero"`
	VehicleTypes        []contracts.VehicleType `json:"vehicleTypes"`
	DriverNames         []string                `json:"driverNames"`
	RoutesWithoutDriver int                     `json:"routesWithoutDriver"`
	Progress            Progress                `json:"progress"`
	DraftStep           int                     `json:"draftStep"`
	DelayMin            int                     `json:"delayMin"`
	ProblemStops        int                     `json:"problemStops"`
	HasAnomaly          bool                    `json:"hasAnomaly"`
	// SavingsRp and SavingsPct are what the chosen option saved against sending the shipments one by one (negative
	// when it costs more but is faster); absent for plans confirmed before they were shared.
	SavingsPct *float64 `json:"savingsPct,omitempty"`
	SavingsRp  *int64   `json:"savingsRp,omitempty"`
	// PendingAction is "cancel" while a cancel waits for the drivers' votes (an addition to the frontend's
	// PlanSummary, like Plan.PendingAction).
	PendingAction string `json:"pendingAction,omitempty"`
}

// Progress counts the finished stops of a plan.
type Progress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// Summarize builds the table row of a plan (routes included). driverNames maps driver ids to names; a driver
// missing from it is left out of the row.
func Summarize(plan contracts.Plan, customerName string, driverNames map[string]string) Summary {
	var stops []contracts.RouteStop
	var distanceKm, durationMin float64
	vehicleTypes := make([]contracts.VehicleType, 0, len(plan.Routes))
	drivers := []string{}
	withoutDriver, delay := 0, 0.0
	var start contracts.Time
	for i, r := range plan.Routes {
		stops = append(stops, r.Stops...)
		distanceKm += r.DistanceKm
		durationMin = math.Max(durationMin, r.DurationMin)
		vehicleTypes = append(vehicleTypes, r.VehicleType)
		if name, ok := driverNames[r.DriverID]; ok {
			drivers = append(drivers, name)
		}
		if r.DriverID == "" && r.Status != contracts.RouteStatusDone {
			withoutDriver++
		}
		if r.Status != contracts.RouteStatusDone {
			delay = math.Max(delay, r.DelayMin)
		}
		if i == 0 {
			start = r.StartAt
		}
	}
	if start.IsZero() && plan.Schedule.Window != nil {
		start = plan.Schedule.Window.From
	}

	open := plan.Status == contracts.PlanStatusAwaitingDriver || plan.Status == contracts.PlanStatusAssigned || plan.Status == contracts.PlanStatusRunning
	if !open {
		withoutDriver = 0
	}
	done, problems := 0, 0
	for _, s := range stops {
		switch s.Status {
		case contracts.StopStatusDone:
			done++
		case contracts.StopStatusProblem:
			done++
			problems++
		}
	}
	totalStops := len(stops)
	if totalStops == 0 {
		totalStops = len(plan.Shipments) * 2
	}
	from, to := endpoints(plan, stops)

	anomaly := problems > 0
	for _, r := range plan.Routes {
		anomaly = anomaly || (r.DelayMin >= anomalyDelayMin && r.Status != contracts.RouteStatusDone)
	}

	return Summary{
		ID: plan.ID, Code: plan.Code, UserID: plan.UserID, CustomerName: customerName, Status: plan.Status,
		CreatedAt: plan.CreatedAt, UpdatedAt: plan.UpdatedAt, ScheduleMode: plan.Schedule.Mode, StartAt: start,
		Preset: plan.Priority.Preset, Shipments: len(plan.Shipments), Stops: totalStops,
		FromLabel: from, ToLabel: to,
		TotalCostRp: plan.TotalCostRp, DistanceKm: math.Round(distanceKm*10) / 10, DurationMin: durationMin,
		VehicleTypes: vehicleTypes, DriverNames: drivers, RoutesWithoutDriver: withoutDriver,
		Progress: Progress{Done: done, Total: len(stops)}, DraftStep: plan.DraftStep,
		DelayMin: int(math.Round(delay)), ProblemStops: problems, HasAnomaly: anomaly, PendingAction: plan.PendingAction, SavingsRp: plan.SavingsRp, SavingsPct: plan.SavingsPct,
	}
}

// endpoints names the first and last place of a plan, with "+n" for the shipments beyond the first.
func endpoints(plan contracts.Plan, stops []contracts.RouteStop) (from, to string) {
	switch {
	case len(stops) > 0:
		from, to = stops[0].Waypoint.Name, stops[len(stops)-1].Waypoint.Name
	case len(plan.Shipments) > 0:
		from, to = plan.Shipments[0].Pickup.Name, plan.Shipments[len(plan.Shipments)-1].Dropoff.Name
	}
	if n := len(plan.Shipments); n > 1 {
		to += " +" + strconv.Itoa(n-1)
	}
	return from, to
}

// Matches reports whether the row contains q (case-insensitive) in its code, customer, places or drivers.
func (s Summary) Matches(q string) bool {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return true
	}
	fields := slices.Concat([]string{s.Code, s.CustomerName, s.FromLabel, s.ToLabel}, s.DriverNames)
	return slices.ContainsFunc(fields, func(f string) bool { return strings.Contains(strings.ToLower(f), q) })
}
