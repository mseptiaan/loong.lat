package ai

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/agent"
	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/dispatch"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/fleet"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/plans"
)

// The Copilot's tools. All of them read the backoffice's own projection, and nothing else; none changes anything
// (propose_action only records a proposal for the screen). What a tool returns is for the model: every figure
// comes with its text already formatted in the admin's language, so that the model quotes rather than calculates.
// What it shows the admin (title, target, detail) is composed here too, in the same language.

const (
	// maxListed bounds the plans, drivers or vehicles one tool result lists; the rest is counted ("more").
	maxListed = 10
	// maxCandidates is how many drivers suggest_drivers lists per route.
	maxCandidates = 5
	// maxStops is how many stops of a route get_plan lists.
	maxStops = 12
	// recommendMinScore is the score from which the best candidate counts as a suggestion (the frontend's
	// RECOMMEND_MIN_SCORE): below it, even the best driver is a weak fit and is not proposed.
	recommendMinScore = 40
	// lateMin is the delay from which a plan needs attention (plans.anomalyDelayMin); onTimeMin the delay up to which
	// a route still counts as on time (the threshold at which driver-api starts to announce route.delayed).
	lateMin   = 10
	onTimeMin = 5
)

// openStatuses are the statuses of a plan that is being served: not finished, not cancelled.
var openStatuses = []contracts.PlanStatus{contracts.PlanStatusAwaitingDriver, contracts.PlanStatusAssigned, contracts.PlanStatusRunning}

const planArgDescription = "The plan: its code (like PL-2041) or id. Optional when the question is about one plan."

// suggestPlanArgDescription is the plan argument of suggest_drivers, whose absence means "all that wait".
const suggestPlanArgDescription = "The plan: its code (like PL-2041) or id. Leave it out to rate every plan that waits for a driver."

func (r *copilotRun) tools() []agent.Tool {
	return []agent.Tool{
		{
			Name:        "get_queue",
			Description: "Reads the queue of plans being served and what needs the admin: the plans waiting for a driver (the longest waiting first), the plans that are late (10 minutes or more) or have a problem stop, how many are on the way or assigned, and how many drivers are ready. Start here for questions like what to do first, what is stuck, or whether anything is wrong.",
			Preview:     r.preview("Reading the queue", "Membaca antrean", ""),
			Run:         r.guard("get_queue", r.queue),
		},
		{
			Name:        "get_plan",
			Description: "Reads one plan: status, customer, priority, schedule, cost and each route with its vehicle, driver, departure, delay and stops (status and times), including problem stops with the reason the driver gave and who to contact there. Use it for questions about a particular plan.",
			Schema: jsontext.Value(`{"type":"object","properties":{
				"plan":{"type":"string","description":"` + planArgDescription + `"}
			},"additionalProperties":false}`),
			Preview: r.previewPlan("Reading the plan", "Membaca plan"),
			Run:     r.guard("get_plan", r.plan),
		},
		{
			Name:        "list_drivers",
			Description: "Lists the drivers with their availability (online and idle, on duty, offline), vehicle, rating, last known position and workload (the routes assigned to them). Use it for questions about who is available, who is busy, or where the drivers are.",
			Schema: jsonObject(`"status":{"type":"string","enum":["all","idle","on_duty","offline"],"description":"Which drivers to list; default all."},
				"limit":{"type":"integer","minimum":1,"maximum":30,"description":"At most this many drivers (default 10)."}`),
			Preview: r.preview("Checking driver availability", "Memeriksa ketersediaan driver", ""),
			Run:     r.guard("list_drivers", r.drivers),
		},
		{
			Name:        "list_vehicles",
			Description: "Lists the vehicles of the fleet: type, plate, model, capacity, status (active, in service, inactive) and the driver who drives each. Use it for questions about the fleet or capacity.",
			Preview:     r.preview("Reading the fleet", "Membaca armada", ""),
			Run:         r.guard("list_vehicles", r.vehicles),
		},
		{
			Name:        "suggest_drivers",
			Description: "Rates the drivers who could take the routes that still wait for a driver: for each route the candidates best first, each with score (0 to 100), distance and time to the pickup, rating and whether the load fits the vehicle. The server computes the scores with the dispatcher's own rules. It also says which candidate automatic assignment would pick. For one plan, name it; without a plan (and when the question is not about one plan) it rates every plan that waits for a driver, the longest waiting first (five at most), so you need not call get_queue first. Use it for questions about who is the best driver.",
			Schema: jsontext.Value(`{"type":"object","properties":{
				"plan":{"type":"string","description":"` + suggestPlanArgDescription + `"},
				"route":{"type":"integer","minimum":1,"maximum":20,"description":"Only this route of the plan (1 is the first); default all routes that wait for a driver."}
			},"additionalProperties":false}`),
			Preview: r.previewPlan("Scoring driver candidates", "Menilai kandidat driver"),
			Run:     r.guard("suggest_drivers", r.suggest),
		},
		{
			Name:        "today_numbers",
			Description: "Today's numbers (Jakarta time): plans created, completed and cancelled, order value, how many routes finished on time, the state of the queue and the drivers right now, and the top-ups paid today. Use it for questions about how today is going.",
			Preview:     r.preview("Reading today's numbers", "Membaca angka hari ini", ""),
			Run:         r.guard("today_numbers", r.today),
		},
		{
			Name:        "propose_action",
			Description: "Proposes a button for the admin to press; it changes nothing by itself. Kinds: tugaskan_terbaik (give the plan's routes that have no driver the best available driver; only a plan that is open, not being cancelled, with such routes and a driver who fits) and buka_plan (open the plan's page). The tool checks the proposal against the real data and says whether it was accepted.",
			Schema: jsontext.Value(`{"type":"object","properties":{
				"kind":{"type":"string","enum":["tugaskan_terbaik","buka_plan"]},
				"plan":{"type":"string","description":"` + planArgDescription + `"}
			},"required":["kind"],"additionalProperties":false}`),
			Preview: r.previewPlan("Checking the suggested action", "Memeriksa tindakan yang disarankan"),
			Run:     r.guard("propose_action", r.proposeTool),
		},
	}
}

func jsonObject(properties string) jsontext.Value {
	return jsontext.Value(`{"type":"object","properties":{` + properties + `},"additionalProperties":false}`)
}

// preview is the line shown while a tool runs: its title now, and what it is about when that is known.
func (r *copilotRun) preview(en, id, target string) func(context.Context, jsontext.Value) agent.Trace {
	return func(context.Context, jsontext.Value) agent.Trace {
		return agent.Trace{Title: r.l.pick(en, id), Target: target}
	}
}

// previewPlan is preview for a tool about a plan: the plan the model named (a code, as far as the screen knows)
// or else the plan of the question.
func (r *copilotRun) previewPlan(en, id string) func(context.Context, jsontext.Value) agent.Trace {
	return func(_ context.Context, args jsontext.Value) agent.Trace {
		var in struct {
			Plan string `json:"plan"`
		}
		_ = json.Unmarshal(args, &in)
		target := strings.TrimSpace(in.Plan)
		if target == "" {
			target = r.scopeCode
		}
		return agent.Trace{Title: r.l.pick(en, id), Target: target}
	}
}

// guard keeps a tool's database failures out of the model's sight (it learns only "the tool failed") and puts
// them in the log.
func (r *copilotRun) guard(name string, fn func(context.Context, jsontext.Value) (agent.Result, error)) func(context.Context, jsontext.Value) (agent.Result, error) {
	return func(ctx context.Context, args jsontext.Value) (agent.Result, error) {
		res, err := fn(ctx, args)
		if _, readable := errors.AsType[*agent.ToolError](err); err != nil && !readable && ctx.Err() == nil {
			r.s.log.WarnContext(ctx, "ai tool failed", "tool", name, "error", err)
		}
		return res, err
	}
}

// lines joins the lines of a trace's detail.
func lines(parts ...string) string { return strings.Join(parts, "\n") }

// failed is a tool failure the model may read and the admin sees as a failed line, with the reason as its detail.
func (r *copilotRun) failed(en, id, target string, reason string) (agent.Result, error) {
	return agent.Result{Trace: agent.Trace{Title: r.l.pick(en, id), Target: target, Detail: reason}}, agent.Failure("%s", reason)
}

// planRef is the plan a call is about: the plan argument (a code or an id), else the plan of the question.
func (r *copilotRun) planRef(args jsontext.Value) string {
	var in struct {
		Plan string `json:"plan"`
	}
	_ = json.Unmarshal(args, &in)
	if ref := strings.TrimSpace(in.Plan); ref != "" {
		return ref
	}
	return r.scopePlan
}

// resolvePlan finds the plan of a call. A plan that is not there is a failure the model can read.
func (r *copilotRun) resolvePlan(ctx context.Context, ref string) (*plans.Aggregate, error) {
	if ref == "" {
		return nil, agent.Failure("say which plan: pass its code (like PL-2041) in the plan argument; get_queue lists the plans that need attention")
	}
	id, err := r.s.plans.IDOf(ctx, ref)
	if api, ok := errors.AsType[*platform.APIError](err); ok && api.Code == platform.CodeNotFound {
		return nil, agent.Failure("there is no plan %q; check the code with get_queue", ref)
	}
	if err != nil {
		return nil, err
	}
	a, err := plans.Load(ctx, r.s.pool, id)
	if api, ok := errors.AsType[*platform.APIError](err); ok && api.Code == platform.CodeNotFound {
		return nil, agent.Failure("there is no plan %q; check the code with get_queue", ref)
	}
	return a, err
}

func driverNames(drivers []fleet.Driver) map[string]string {
	names := make(map[string]string, len(drivers))
	for _, d := range drivers {
		names[d.ID] = d.Name
	}
	return names
}

/* ---------------------------------- get_queue ---------------------------------- */

type planRow struct {
	Plan                string     `json:"plan"`
	ID                  string     `json:"id"`
	Customer            string     `json:"customer"`
	Status              string     `json:"status"`
	Priority            string     `json:"priority"`
	Schedule            string     `json:"schedule"`
	Routes              int        `json:"routes"`
	RoutesWithoutDriver int        `json:"routesWithoutDriver,omitzero"`
	Vehicles            []string   `json:"vehicles"`
	From                string     `json:"from"`
	To                  string     `json:"to"`
	WaitingFor          *quantity  `json:"waitingFor,omitempty"`
	Delay               *quantity  `json:"delay,omitempty"`
	ProblemStops        int        `json:"problemStops,omitzero"`
	Why                 []string   `json:"why,omitempty"`
	Drivers             []string   `json:"drivers,omitempty"`
	CancelPending       bool       `json:"cancelPending,omitzero"`
	sort                [2]float64 // ordering inside a list: scheduled after "now", then the longest waiting or latest first
}

type planList struct {
	Count int       `json:"count"`
	Plans []planRow `json:"plans"`
	More  int       `json:"more,omitzero"`
}

func newPlanList(rows []planRow) planList {
	l := planList{Count: len(rows), Plans: rows}
	if len(rows) > maxListed {
		l.Plans, l.More = rows[:maxListed], len(rows)-maxListed
	}
	if l.Plans == nil {
		l.Plans = []planRow{}
	}
	return l
}

// row describes a plan for a list.
func (r *copilotRun) row(p plans.Row, sum plans.Summary, now time.Time) planRow {
	row := planRow{
		Plan: p.Plan.Code, ID: p.Plan.ID, Customer: p.Customer, Status: r.l.planStatus(p.Plan.Status),
		Priority: r.l.preset(p.Plan.Priority.Preset), Schedule: r.schedule(p.Plan, sum),
		Routes: len(p.Plan.Routes), RoutesWithoutDriver: sum.RoutesWithoutDriver, Vehicles: []string{},
		From: sum.FromLabel, To: sum.ToLabel, ProblemStops: sum.ProblemStops, Drivers: sum.DriverNames,
		CancelPending: p.Plan.PendingAction != "",
	}
	for _, v := range sum.VehicleTypes {
		row.Vehicles = append(row.Vehicles, r.l.vehicle(v))
	}
	if sum.RoutesWithoutDriver > 0 && p.Plan.Schedule.Mode != contracts.ScheduleModeScheduled {
		since := p.Plan.ConfirmedAt
		if since.IsZero() {
			since = p.Plan.CreatedAt
		}
		waited := max(0, now.Sub(since.Time).Minutes())
		q := r.l.mins(waited)
		row.WaitingFor = &q
		row.sort[1] = -waited
	} else {
		row.sort = [2]float64{1, float64(sum.StartAt.Unix())}
	}
	if sum.DelayMin > 0 {
		q := r.l.mins(float64(sum.DelayMin))
		row.Delay = &q
	}
	if sum.DelayMin >= lateMin {
		row.Why = append(row.Why, r.l.pick(fmt.Sprintf("%s late", r.l.duration(float64(sum.DelayMin))), fmt.Sprintf("terlambat %s", r.l.duration(float64(sum.DelayMin)))))
	}
	if sum.ProblemStops > 0 {
		row.Why = append(row.Why, r.l.pick(fmt.Sprintf("%d problem stop(s)", sum.ProblemStops), fmt.Sprintf("%d stop bermasalah", sum.ProblemStops)))
	}
	return row
}

// schedule says when a plan is to run: as soon as possible, or at the time the customer chose.
func (r *copilotRun) schedule(p contracts.Plan, sum plans.Summary) string {
	if p.Schedule.Mode != contracts.ScheduleModeScheduled {
		return r.l.pick("as soon as possible", "secepatnya")
	}
	return r.l.pick("scheduled for ", "dijadwalkan ") + r.l.when(sum.StartAt.Time, r.s.now())
}

type queueResult struct {
	Now                string   `json:"now"`
	ActivePlans        int      `json:"activePlans"`
	Running            int      `json:"running"`
	AssignedNotStarted int      `json:"assignedNotStarted"`
	Cancelling         int      `json:"cancelling,omitzero"`
	WaitingForDriver   planList `json:"waitingForDriver"`
	NeedAttention      planList `json:"needAttention"`
	DriversReady       int      `json:"driversReady"`
}

func (r *copilotRun) queue(ctx context.Context, _ jsontext.Value) (agent.Result, error) {
	now := r.s.now()
	rows, err := r.s.plans.Rows(ctx, plans.Filter{Status: openStatuses})
	if err != nil {
		return agent.Result{}, err
	}
	drivers, err := r.s.fleet.Drivers(ctx)
	if err != nil {
		return agent.Result{}, err
	}
	names := driverNames(drivers)
	out := queueResult{Now: r.l.dateTime(now), ActivePlans: len(rows)}
	for _, d := range drivers {
		if d.Status == fleet.StatusOnline {
			out.DriversReady++
		}
	}
	var waiting, attention []planRow
	for _, p := range rows {
		sum := plans.Summarize(p.Plan, p.Customer, names)
		switch p.Plan.Status {
		case contracts.PlanStatusRunning:
			out.Running++
		case contracts.PlanStatusAssigned:
			out.AssignedNotStarted++
		}
		if p.Plan.PendingAction != "" {
			out.Cancelling++ // dispatch leaves it alone: it is neither waiting nor an anomaly
			continue
		}
		row := r.row(p, sum, now)
		if sum.RoutesWithoutDriver > 0 {
			waiting = append(waiting, row)
		}
		if sum.HasAnomaly {
			row.sort = [2]float64{-float64(sum.DelayMin), float64(sum.StartAt.Unix())}
			attention = append(attention, row)
		}
	}
	byOrder := func(a, b planRow) int {
		return cmp.Or(cmp.Compare(a.sort[0], b.sort[0]), cmp.Compare(a.sort[1], b.sort[1]), cmp.Compare(a.Plan, b.Plan))
	}
	slices.SortFunc(waiting, byOrder)
	slices.SortFunc(attention, byOrder)
	out.WaitingForDriver, out.NeedAttention = newPlanList(waiting), newPlanList(attention)

	detail := lines(
		r.l.pick("active plans: ", "plan aktif: ")+fmt.Sprint(out.ActivePlans),
		r.l.pick("waiting for a driver: ", "menunggu driver: ")+fmt.Sprint(out.WaitingForDriver.Count),
		r.l.pick("anomalies: ", "anomali: ")+fmt.Sprint(out.NeedAttention.Count),
		r.l.pick("drivers ready: ", "driver siap: ")+fmt.Sprint(out.DriversReady),
	)
	for _, row := range out.NeedAttention.Plans {
		detail += "\n" + row.Plan + ": " + strings.Join(row.Why, ", ")
	}
	return agent.Result{Content: out, Trace: agent.Trace{
		Title: r.l.pick("Reading the queue", "Membaca antrean"), Target: r.l.count(out.ActivePlans, "active plan", "active plans", "plan aktif"),
		Detail: detail,
	}}, nil
}

/* ---------------------------------- get_plan ---------------------------------- */

type personRef struct {
	Name  string `json:"name"`
	Phone string `json:"phone,omitempty"`
}

type stopRow struct {
	Seq    int    `json:"seq"`
	Kind   string `json:"kind"`
	Place  string `json:"place"`
	Status string `json:"status"`
	Eta    string `json:"eta"`
}

type problemRow struct {
	Seq     int        `json:"seq"`
	Place   string     `json:"place"`
	Reason  string     `json:"reason,omitempty"`
	Note    string     `json:"driverNote,omitempty"`
	Contact *personRef `json:"contact,omitempty"`
}

type routeRow struct {
	N            int          `json:"n"`
	Vehicle      string       `json:"vehicle"`
	Status       string       `json:"status"`
	Driver       *personRef   `json:"driver,omitempty"`
	DriverVeh    string       `json:"driverVehicle,omitempty"`
	Departs      string       `json:"departs,omitempty"`
	Distance     quantity     `json:"distance"`
	Duration     quantity     `json:"duration"`
	Cost         quantity     `json:"cost"`
	Delay        *quantity    `json:"delay,omitempty"`
	StopsDone    string       `json:"stopsDone"`
	Stops        []stopRow    `json:"stops"`
	MoreStops    int          `json:"moreStops,omitzero"`
	ProblemStops []problemRow `json:"problemStops,omitempty"`
}

type planDetail struct {
	Plan          string     `json:"plan"`
	ID            string     `json:"id"`
	Customer      string     `json:"customer"`
	Status        string     `json:"status"`
	Priority      string     `json:"priority"`
	Schedule      string     `json:"schedule"`
	CreatedAt     string     `json:"createdAt"`
	Cost          quantity   `json:"cost"`
	Shipments     int        `json:"shipments"`
	CancelPending bool       `json:"cancelPending,omitzero"`
	CancelReason  string     `json:"cancelReason,omitempty"`
	Routes        []routeRow `json:"routes"`
}

func (r *copilotRun) plan(ctx context.Context, args jsontext.Value) (agent.Result, error) {
	ref := r.planRef(args)
	a, err := r.resolvePlan(ctx, ref)
	if err != nil {
		if _, readable := errors.AsType[*agent.ToolError](err); readable {
			return r.failed("Reading the plan", "Membaca plan", ref, err.Error())
		}
		return agent.Result{}, err
	}
	now := r.s.now()
	drivers, err := r.s.fleet.Drivers(ctx)
	if err != nil {
		return agent.Result{}, err
	}
	vehicles, err := r.s.fleet.Vehicles(ctx)
	if err != nil {
		return agent.Result{}, err
	}
	byDriver := map[string]fleet.Driver{}
	for _, d := range drivers {
		byDriver[d.ID] = d
	}
	byVehicle := map[string]contracts.Vehicle{}
	for _, v := range vehicles {
		byVehicle[v.ID] = v
	}
	customer, err := plans.CustomerName(ctx, r.s.pool, a.Plan.UserID)
	if err != nil {
		return agent.Result{}, err
	}

	plan := a.Snapshot()
	sum := plans.Summarize(plan, customer, driverNames(drivers))
	out := planDetail{
		Plan: plan.Code, ID: plan.ID, Customer: customer, Status: r.l.planStatus(plan.Status),
		Priority: r.l.preset(plan.Priority.Preset), Schedule: r.schedule(plan, sum), CreatedAt: r.l.when(plan.CreatedAt.Time, now),
		Cost: r.l.rp(plan.TotalCostRp), Shipments: len(plan.Shipments), CancelPending: plan.PendingAction != "",
		CancelReason: plan.CancelReason, Routes: []routeRow{},
	}
	for i, rt := range plan.Routes {
		out.Routes = append(out.Routes, r.routeRow(i+1, rt, byDriver, byVehicle, now))
	}

	detail := lines(
		"plan: "+plan.Code,
		"status: "+out.Status,
		r.l.pick("routes: ", "rute: ")+fmt.Sprint(len(plan.Routes)),
	)
	return agent.Result{Content: out, Trace: agent.Trace{Title: r.l.pick("Reading the plan", "Membaca plan"), Target: plan.Code, Detail: detail}}, nil
}

func (r *copilotRun) routeRow(n int, rt contracts.Route, drivers map[string]fleet.Driver, vehicles map[string]contracts.Vehicle, now time.Time) routeRow {
	row := routeRow{
		N: n, Vehicle: r.l.vehicle(rt.VehicleType), Status: r.l.routeStatus(rt.Status),
		Distance: r.l.kmq(rt.DistanceKm), Duration: r.l.mins(rt.DurationMin), Cost: r.l.rp(rt.CostRp),
		Stops: []stopRow{},
	}
	if d, ok := drivers[rt.DriverID]; ok {
		row.Driver = &personRef{Name: d.Name, Phone: d.Phone}
		if v, ok := vehicles[rt.VehicleID]; ok {
			row.DriverVeh = fmt.Sprintf("%s · %s", v.Model, v.Plate)
		}
	}
	if rt.Status == contracts.RouteStatusAssigned || rt.Status == contracts.RouteStatusAwaitingDriver {
		row.Departs = r.l.when(rt.StartAt.Time, now)
	}
	if rt.DelayMin >= 1 { // a finished route shows how late it ran
		q := r.l.mins(rt.DelayMin)
		row.Delay = &q
	}
	done := 0
	for _, s := range rt.Stops {
		if s.Status == contracts.StopStatusDone || s.Status == contracts.StopStatusProblem {
			done++
		}
		if len(row.Stops) < maxStops {
			row.Stops = append(row.Stops, stopRow{Seq: s.Seq, Kind: r.l.stopKind(s.Kind), Place: s.Waypoint.Name, Status: r.l.stopStatus(s.Status), Eta: r.l.when(s.ArriveAt.Time, now)})
		}
		if s.Status == contracts.StopStatusProblem {
			reason, note := r.l.issue(s.Issue)
			p := problemRow{Seq: s.Seq, Place: s.Waypoint.Name, Reason: reason, Note: note}
			if s.Waypoint.ContactName != "" || s.Waypoint.ContactPhone != "" {
				p.Contact = &personRef{Name: s.Waypoint.ContactName, Phone: s.Waypoint.ContactPhone}
			}
			row.ProblemStops = append(row.ProblemStops, p)
		}
	}
	row.StopsDone = fmt.Sprintf("%d/%d", done, len(rt.Stops))
	row.MoreStops = max(0, len(rt.Stops)-maxStops)
	return row
}

/* ---------------------------------- list_drivers ---------------------------------- */

type driverWork struct {
	Plan    string `json:"plan"`
	Status  string `json:"status"`
	Departs string `json:"departs"`
}

type driverRow struct {
	Name           string       `json:"name"`
	ID             string       `json:"id"`
	Status         string       `json:"status"`
	Rating         float64      `json:"rating"`
	CompletedTrips int          `json:"completedTrips"`
	Vehicle        string       `json:"vehicle,omitempty"`
	VehicleType    string       `json:"vehicleType,omitempty"`
	Position       *positionRow `json:"position,omitempty"`
	LastSignal     string       `json:"lastSignal,omitempty"`
	Routes         []driverWork `json:"routes,omitempty"`
	rank           int
}

type positionRow struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type driverCounts struct {
	Idle    int `json:"onlineAndIdle"`
	OnDuty  int `json:"onDuty"`
	Offline int `json:"offline"`
	Total   int `json:"total"`
}

type driversResult struct {
	Counts  driverCounts `json:"counts"`
	Drivers []driverRow  `json:"drivers"`
	More    int          `json:"more,omitzero"`
}

func countDrivers(drivers []fleet.Driver) driverCounts {
	c := driverCounts{Total: len(drivers)}
	for _, d := range drivers {
		switch d.Status {
		case fleet.StatusOnline:
			c.Idle++
		case fleet.StatusOnDuty:
			c.OnDuty++
		default:
			c.Offline++
		}
	}
	return c
}

func (r *copilotRun) drivers(ctx context.Context, args jsontext.Value) (agent.Result, error) {
	var in struct {
		Status string `json:"status"`
		Limit  int    `json:"limit"`
	}
	_ = json.Unmarshal(args, &in)
	limit := cmp.Or(in.Limit, maxListed)
	now := r.s.now()

	drivers, err := r.s.fleet.Drivers(ctx)
	if err != nil {
		return agent.Result{}, err
	}
	vehicles, err := r.s.fleet.Vehicles(ctx)
	if err != nil {
		return agent.Result{}, err
	}
	live, err := r.s.plans.LiveRoutes(ctx)
	if err != nil {
		return agent.Result{}, err
	}
	byVehicle := map[string]contracts.Vehicle{}
	for _, v := range vehicles {
		byVehicle[v.ID] = v
	}
	work := map[string][]driverWork{}
	for _, lr := range live {
		work[lr.Route.DriverID] = append(work[lr.Route.DriverID], driverWork{
			Plan: lr.PlanCode, Status: r.l.routeStatus(lr.Route.Status), Departs: r.l.when(lr.Route.StartAt.Time, now),
		})
	}

	var rows []driverRow
	for _, d := range drivers {
		switch in.Status {
		case "idle":
			if d.Status != fleet.StatusOnline {
				continue
			}
		case "on_duty":
			if d.Status != fleet.StatusOnDuty {
				continue
			}
		case "offline":
			if d.Status != fleet.StatusOffline {
				continue
			}
		}
		row := driverRow{
			Name: d.Name, ID: d.ID, Status: r.l.driverStatus(d.Status), Rating: d.Rating, CompletedTrips: d.CompletedTrips,
			LastSignal: r.l.when(d.LastSeen.Time, now), Routes: work[d.ID],
			rank: map[fleet.Status]int{fleet.StatusOnline: 0, fleet.StatusOnDuty: 1, fleet.StatusOffline: 2}[d.Status],
		}
		if v, ok := byVehicle[d.VehicleID]; ok {
			row.Vehicle, row.VehicleType = fmt.Sprintf("%s · %s", v.Model, v.Plate), r.l.vehicle(v.Type)
		}
		if d.Position != nil {
			row.Position = &positionRow{Lat: math.Round(d.Position.Lat*10000) / 10000, Lng: math.Round(d.Position.Lng*10000) / 10000}
		}
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(a, b driverRow) int {
		return cmp.Or(cmp.Compare(a.rank, b.rank), cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
	})
	out := driversResult{Counts: countDrivers(drivers), Drivers: rows}
	if len(rows) > limit {
		out.Drivers, out.More = rows[:limit], len(rows)-limit
	}
	if out.Drivers == nil {
		out.Drivers = []driverRow{}
	}

	c := out.Counts
	return agent.Result{Content: out, Trace: agent.Trace{
		Title:  r.l.pick("Checking driver availability", "Memeriksa ketersediaan driver"),
		Target: r.l.pick(fmt.Sprintf("%s ready", r.l.drivers(c.Idle)), fmt.Sprintf("%s siap", r.l.drivers(c.Idle))),
		Detail: lines(
			r.l.pick("online and idle: ", "online tanpa tugas: ")+fmt.Sprint(c.Idle),
			r.l.pick("on duty: ", "bertugas: ")+fmt.Sprint(c.OnDuty),
			"offline: "+fmt.Sprint(c.Offline),
		),
	}}, nil
}

/* ---------------------------------- list_vehicles ---------------------------------- */

type vehicleRow struct {
	ID          string  `json:"id"`
	Type        string  `json:"type"`
	Plate       string  `json:"plate"`
	Model       string  `json:"model"`
	Status      string  `json:"status"`
	Driver      string  `json:"driver,omitempty"`
	MaxWeightKg float64 `json:"maxWeightKg"`
	MaxVolumeM3 float64 `json:"maxVolumeM3"`
	MaxDimCm    float64 `json:"maxDimensionCm"`
}

type vehiclesResult struct {
	Total    int            `json:"total"`
	ByType   map[string]int `json:"byType"`
	ByStatus map[string]int `json:"byStatus"`
	Vehicles []vehicleRow   `json:"vehicles"`
	More     int            `json:"more,omitzero"`
}

func (r *copilotRun) vehicles(ctx context.Context, _ jsontext.Value) (agent.Result, error) {
	vehicles, err := r.s.fleet.Vehicles(ctx)
	if err != nil {
		return agent.Result{}, err
	}
	drivers, err := r.s.fleet.Drivers(ctx)
	if err != nil {
		return agent.Result{}, err
	}
	names := driverNames(drivers)
	out := vehiclesResult{Total: len(vehicles), ByType: map[string]int{}, ByStatus: map[string]int{}, Vehicles: []vehicleRow{}}
	for _, v := range vehicles {
		out.ByType[r.l.vehicle(v.Type)]++
		out.ByStatus[r.l.vehicleStatus(v.Status)]++
		if len(out.Vehicles) < 20 {
			out.Vehicles = append(out.Vehicles, vehicleRow{
				ID: v.ID, Type: r.l.vehicle(v.Type), Plate: v.Plate, Model: v.Model, Status: r.l.vehicleStatus(v.Status),
				Driver: names[v.DriverID], MaxWeightKg: v.MaxWeightKg, MaxVolumeM3: v.MaxVolumeM3, MaxDimCm: v.MaxDimensionCm,
			})
		}
	}
	out.More = max(0, len(vehicles)-len(out.Vehicles))

	var detail []string
	for _, key := range slices.Sorted(maps.Keys(out.ByStatus)) {
		detail = append(detail, fmt.Sprintf("%s: %d", key, out.ByStatus[key]))
	}
	return agent.Result{Content: out, Trace: agent.Trace{
		Title: r.l.pick("Reading the fleet", "Membaca armada"), Target: r.l.count(len(vehicles), "vehicle", "vehicles", "kendaraan"),
		Detail: lines(detail...),
	}}, nil
}

/* ---------------------------------- suggest_drivers ---------------------------------- */

type candidateRow struct {
	Driver         string   `json:"driver"`
	DriverID       string   `json:"driverId"`
	Vehicle        string   `json:"vehicle"`
	Rating         float64  `json:"rating"`
	Score          int      `json:"score"`
	DistanceKm     quantity `json:"distanceToPickup"`
	Eta            quantity `json:"timeToPickup"`
	Fits           bool     `json:"loadFits"`
	AutoAssignPick bool     `json:"automaticAssignmentPicksThis,omitzero"`
}

type routeSuggestions struct {
	Route      int            `json:"route"`
	Vehicle    string         `json:"vehicleNeeded"`
	Pickup     string         `json:"pickup"`
	AssignedTo string         `json:"alreadyAssignedTo,omitempty"`
	Candidates []candidateRow `json:"candidates"`
	// Best is the driver of the highest score whose load fits; Strong says whether that score is high enough to be a
	// suggestion (not just the only one available).
	Best   string `json:"bestDriver,omitempty"`
	Strong bool   `json:"bestIsAStrongFit,omitzero"`
	Note   string `json:"note,omitempty"`
}

type planSuggestions struct {
	Plan   string             `json:"plan"`
	Routes []routeSuggestions `json:"routes"`
}

type suggestResult struct {
	Plans []planSuggestions `json:"plans"`
	// More is how many further plans wait for a driver: a call without a plan rates the first five.
	More int `json:"morePlansWaiting,omitzero"`
}

// suggestions rates the drivers for one route that waits for one: the server's own suggestion list, fitting
// drivers first, with the one automatic assignment would pick marked (the nearest that fits).
func (r *copilotRun) suggestions(ctx context.Context, a *plans.Aggregate, n int, rt *plans.Route) (routeSuggestions, error) {
	row := routeSuggestions{Route: n, Vehicle: r.l.vehicle(rt.VehicleType), Candidates: []candidateRow{}}
	if len(rt.Stops) > 0 {
		row.Pickup = rt.Stops[0].Waypoint.Name
	}
	sugs, err := r.s.dispatch.Suggest(ctx, a.Plan.ID, rt.ID)
	if err != nil {
		return row, err
	}
	slices.SortStableFunc(sugs, func(x, y dispatch.Suggestion) int {
		if x.Fits != y.Fits {
			if x.Fits {
				return -1
			}
			return 1
		}
		return cmp.Compare(y.Score, x.Score)
	})
	// Automatic assignment gives the route to the nearest driver whose load fits, then the better rated, then by id.
	nearest := -1
	for i, s := range sugs {
		if !s.Fits {
			continue
		}
		if nearest < 0 {
			nearest = i
			continue
		}
		o := sugs[nearest]
		if cmp.Or(cmp.Compare(s.DistanceToPickupKm, o.DistanceToPickupKm), cmp.Compare(o.Rating, s.Rating), cmp.Compare(s.DriverID, o.DriverID)) < 0 {
			nearest = i
		}
	}
	for i, s := range sugs {
		if i >= maxCandidates && i != nearest {
			continue
		}
		row.Candidates = append(row.Candidates, candidateRow{
			Driver: s.DriverName, DriverID: s.DriverID, Vehicle: s.Plate, Rating: s.Rating, Score: s.Score,
			DistanceKm: r.l.kmq(s.DistanceToPickupKm), Eta: r.l.mins(float64(s.EtaToPickupMin)), Fits: s.Fits, AutoAssignPick: i == nearest,
		})
	}
	if len(sugs) > 0 && sugs[0].Fits {
		row.Best, row.Strong = sugs[0].DriverName, sugs[0].Score >= recommendMinScore
	}
	switch {
	case a.Frozen():
		row.Note = "a cancellation of this plan is under way: nobody can be assigned to it now"
	case len(sugs) == 0:
		row.Note = "no driver can take this route now: none is online, free, in the right vehicle and in range"
	case row.Best == "":
		row.Note = "every available driver's vehicle is too small for the load"
	}
	return row, nil
}

// maxRatedPlans is how many waiting plans a call of suggest_drivers without a plan rates.
const maxRatedPlans = 5

// waitingPlans are the plans of the open ones that wait for a driver and may be given one (not being cancelled),
// in the order the queue lists them: "now" plans the longest waiting first, then scheduled plans by departure.
func waitingPlans(rows []plans.Row, names map[string]string) []plans.Row {
	var out []plans.Row
	for _, p := range rows {
		if p.Plan.PendingAction == "" && plans.Summarize(p.Plan, p.Customer, names).RoutesWithoutDriver > 0 {
			out = append(out, p)
		}
	}
	scheduled := func(p plans.Row) int {
		if p.Plan.Schedule.Mode == contracts.ScheduleModeScheduled {
			return 1
		}
		return 0
	}
	at := func(p plans.Row) int64 {
		if scheduled(p) == 1 && len(p.Plan.Routes) > 0 {
			return p.Plan.Routes[0].StartAt.Unix()
		}
		if !p.Plan.ConfirmedAt.IsZero() {
			return p.Plan.ConfirmedAt.Unix()
		}
		return p.Plan.CreatedAt.Unix()
	}
	slices.SortFunc(out, func(a, b plans.Row) int {
		return cmp.Or(cmp.Compare(scheduled(a), scheduled(b)), cmp.Compare(at(a), at(b)), cmp.Compare(a.Plan.Code, b.Plan.Code))
	})
	return out
}

// ratePlan rates the drivers for the routes of a plan that wait for one (only route onlyRoute when it is not 0),
// and says what it did as the lines of a trace and how many candidates it found.
func (r *copilotRun) ratePlan(ctx context.Context, a *plans.Aggregate, onlyRoute int, names map[string]string) (planSuggestions, []string, int, error) {
	out := planSuggestions{Plan: a.Plan.Code, Routes: []routeSuggestions{}}
	var detail []string
	candidates := 0
	for i := range a.Routes {
		rt := &a.Routes[i]
		if onlyRoute != 0 && onlyRoute != i+1 {
			continue
		}
		if rt.Status != contracts.RouteStatusAwaitingDriver || rt.DriverID != "" {
			out.Routes = append(out.Routes, routeSuggestions{
				Route: i + 1, Vehicle: r.l.vehicle(rt.VehicleType), AssignedTo: cmp.Or(names[rt.DriverID], r.l.routeStatus(rt.Status)), Candidates: []candidateRow{},
			})
			continue
		}
		row, err := r.suggestions(ctx, a, i+1, rt)
		if err != nil {
			return out, nil, 0, err
		}
		out.Routes = append(out.Routes, row)
		candidates += len(row.Candidates)
		if len(a.Routes) > 1 {
			detail = append(detail, r.l.pick("route ", "rute ")+fmt.Sprint(i+1)+":")
		}
		for _, c := range row.Candidates {
			detail = append(detail, r.l.pick(fmt.Sprintf("%s: score %d, %s", c.Driver, c.Score, c.DistanceKm.Text), fmt.Sprintf("%s: skor %d, %s", c.Driver, c.Score, c.DistanceKm.Text)))
		}
		if len(row.Candidates) == 0 {
			detail = append(detail, r.l.pick("no candidates", "tidak ada kandidat"))
		}
	}
	return out, detail, candidates, nil
}

func (r *copilotRun) suggest(ctx context.Context, args jsontext.Value) (agent.Result, error) {
	var in struct {
		Route int `json:"route"`
	}
	_ = json.Unmarshal(args, &in)
	title := r.l.pick("Scoring driver candidates", "Menilai kandidat driver")
	drivers, err := r.s.fleet.Drivers(ctx)
	if err != nil {
		return agent.Result{}, err
	}
	names := driverNames(drivers)

	out := suggestResult{Plans: []planSuggestions{}}
	var detail []string
	candidates := 0
	target := ""
	if ref := r.planRef(args); ref != "" {
		a, err := r.resolvePlan(ctx, ref)
		if err != nil {
			if _, readable := errors.AsType[*agent.ToolError](err); readable {
				return r.failed("Scoring driver candidates", "Menilai kandidat driver", ref, err.Error())
			}
			return agent.Result{}, err
		}
		target = a.Plan.Code
		if a.Plan.Status == contracts.PlanStatusDone || a.Plan.Status == contracts.PlanStatusCancelled {
			return r.failed("Scoring driver candidates", "Menilai kandidat driver", target,
				fmt.Sprintf("plan %s is %s: there is nothing left to assign", target, strings.ToLower(r.l.planStatus(a.Plan.Status))))
		}
		if in.Route > len(a.Routes) {
			return r.failed("Scoring driver candidates", "Menilai kandidat driver", target, fmt.Sprintf("plan %s has only %d route(s)", target, len(a.Routes)))
		}
		row, lines, n, err := r.ratePlan(ctx, a, in.Route, names)
		if err != nil {
			return agent.Result{}, err
		}
		out.Plans, detail, candidates = append(out.Plans, row), lines, n
		target += " · "
	} else {
		// No plan named, and the question is not about one: every plan that waits for a driver, the longest waiting first.
		rows, err := r.s.plans.Rows(ctx, plans.Filter{Status: openStatuses})
		if err != nil {
			return agent.Result{}, err
		}
		waiting := waitingPlans(rows, names)
		out.More = max(0, len(waiting)-maxRatedPlans)
		for _, p := range waiting[:min(len(waiting), maxRatedPlans)] {
			a, err := plans.Load(ctx, r.s.pool, p.Plan.ID)
			if err != nil {
				return agent.Result{}, err
			}
			row, lines, n, err := r.ratePlan(ctx, a, 0, names)
			if err != nil {
				return agent.Result{}, err
			}
			out.Plans = append(out.Plans, row)
			detail = append(detail, p.Plan.Code+":")
			detail = append(detail, lines...)
			candidates += n
		}
		target = r.l.plans(len(out.Plans)) + " · "
	}
	if len(detail) == 0 {
		detail = append(detail, r.l.pick("no plan waits for a driver", "tidak ada plan yang menunggu driver"))
	}
	return agent.Result{Content: out, Trace: agent.Trace{
		Title: title, Target: target + r.l.count(candidates, "candidate", "candidates", "kandidat"), Detail: lines(detail...),
	}}, nil
}

/* ---------------------------------- today_numbers ---------------------------------- */

type todayResult struct {
	Date  string `json:"date"`
	Plans struct {
		Created    int      `json:"createdToday"`
		Completed  int      `json:"completedToday"`
		Cancelled  int      `json:"cancelledToday"`
		OrderValue quantity `json:"orderValueOfNewPlans"`
	} `json:"plans"`
	Routes struct {
		FinishedToday int       `json:"finishedToday"`
		OnTime        int       `json:"finishedOnTime"`
		OnTimeRate    *quantity `json:"onTimeRate,omitempty"`
		OnTimeNote    string    `json:"onTimeMeans"`
	} `json:"routes"`
	QueueNow struct {
		Active           int `json:"activePlans"`
		Running          int `json:"onTheWay"`
		WaitingForDriver int `json:"plansWaitingForADriver"`
		NeedAttention    int `json:"plansNeedingAttention"`
		ProblemStops     int `json:"problemStopsInOpenPlans"`
	} `json:"queueNow"`
	Drivers driverCounts `json:"drivers"`
	TopUps  struct {
		PaidToday    quantity `json:"paidToday"`
		PaidCount    int      `json:"paidTodayCount"`
		PendingCount int      `json:"pending"`
	} `json:"topUps"`
}

func (r *copilotRun) today(ctx context.Context, _ jsontext.Value) (agent.Result, error) {
	now := r.s.now()
	start := startOfDay(now)
	// A plan that finishes today was created at most a couple of weeks ago; the open ones are read separately.
	recent, err := r.s.plans.Rows(ctx, plans.Filter{From: contracts.At(now.Add(-14 * 24 * time.Hour))})
	if err != nil {
		return agent.Result{}, err
	}
	open, err := r.s.plans.Rows(ctx, plans.Filter{Status: openStatuses})
	if err != nil {
		return agent.Result{}, err
	}
	drivers, err := r.s.fleet.Drivers(ctx)
	if err != nil {
		return agent.Result{}, err
	}
	overview, err := r.s.finance.Overview(ctx)
	if err != nil {
		return agent.Result{}, err
	}
	names := driverNames(drivers)

	var out todayResult
	out.Date = r.l.date(start)
	var value int64
	for _, p := range recent {
		isCancelled := p.Plan.Status == contracts.PlanStatusCancelled
		if !p.Plan.CreatedAt.Before(start) && !isCancelled {
			out.Plans.Created++
			value += p.Plan.TotalCostRp
		}
		if isCancelled && !p.Plan.UpdatedAt.Before(start) {
			out.Plans.Cancelled++
		}
		if p.Plan.Status == contracts.PlanStatusDone && !p.Plan.FinishedAt.IsZero() && !p.Plan.FinishedAt.Before(start) {
			out.Plans.Completed++
		}
		for _, rt := range p.Plan.Routes {
			if rt.Status == contracts.RouteStatusDone && !rt.FinishedAt.IsZero() && !rt.FinishedAt.Before(start) {
				out.Routes.FinishedToday++
				if rt.DelayMin < onTimeMin {
					out.Routes.OnTime++
				}
			}
		}
	}
	out.Plans.OrderValue = r.l.rp(value)
	out.Routes.OnTimeNote = fmt.Sprintf("a finished route that ran less than %d minutes late", onTimeMin)
	if out.Routes.FinishedToday > 0 {
		q := r.l.pctq(100 * float64(out.Routes.OnTime) / float64(out.Routes.FinishedToday))
		out.Routes.OnTimeRate = &q
	}
	out.QueueNow.Active = len(open)
	for _, p := range open {
		sum := plans.Summarize(p.Plan, p.Customer, names)
		if p.Plan.Status == contracts.PlanStatusRunning {
			out.QueueNow.Running++
		}
		out.QueueNow.ProblemStops += sum.ProblemStops
		if p.Plan.PendingAction != "" {
			continue
		}
		if sum.RoutesWithoutDriver > 0 {
			out.QueueNow.WaitingForDriver++
		}
		if sum.HasAnomaly {
			out.QueueNow.NeedAttention++
		}
	}
	out.Drivers = countDrivers(drivers)
	out.TopUps.PaidToday, out.TopUps.PaidCount, out.TopUps.PendingCount = r.l.rp(overview.PaidTodayRp), overview.PaidTodayCount, overview.PendingCount

	rate := "—"
	if out.Routes.OnTimeRate != nil {
		rate = out.Routes.OnTimeRate.Text
	}
	return agent.Result{Content: out, Trace: agent.Trace{
		Title: r.l.pick("Reading today's numbers", "Membaca angka hari ini"), Target: r.l.plans(out.Plans.Created),
		Detail: lines(
			r.l.pick("created today: ", "dibuat hari ini: ")+fmt.Sprint(out.Plans.Created),
			r.l.pick("completed today: ", "selesai hari ini: ")+fmt.Sprint(out.Plans.Completed),
			r.l.pick("order value: ", "nilai pesanan: ")+out.Plans.OrderValue.Text,
			r.l.pick("on time: ", "tepat waktu: ")+rate,
		),
	}}, nil
}

/* ---------------------------------- propose_action ---------------------------------- */

func (r *copilotRun) proposeTool(ctx context.Context, args jsontext.Value) (agent.Result, error) {
	var in struct {
		Kind string `json:"kind"`
	}
	_ = json.Unmarshal(args, &in)
	ref := r.planRef(args)
	const en, id = "Checking the suggested action", "Memeriksa tindakan yang disarankan"
	refuse := func(target, reason string) (agent.Result, error) {
		return r.failed(en, id, target, "not proposed: "+reason)
	}

	a, err := r.resolvePlan(ctx, ref)
	if err != nil {
		if te, readable := errors.AsType[*agent.ToolError](err); readable {
			return refuse(ref, te.Message)
		}
		return agent.Result{}, err
	}
	code := a.Plan.Code

	var action Action
	switch in.Kind {
	case actionOpenPlan:
		action = Action{ID: "buka:" + a.Plan.ID, Label: r.l.pick("Open ", "Buka ") + code, Kind: actionOpenPlan, PlanID: a.Plan.ID}
	case actionAssignBest:
		if reason, err := r.canAssign(ctx, a); err != nil {
			return agent.Result{}, err
		} else if reason != "" {
			return refuse(code, reason)
		}
		action = Action{ID: "tugaskan:" + a.Plan.ID, Label: r.l.pick("Apply the best driver suggestion", "Terapkan saran driver terbaik"), Kind: actionAssignBest, PlanID: a.Plan.ID}
	default:
		return refuse(code, fmt.Sprintf("unknown kind %q", in.Kind))
	}
	r.propose(action)
	return agent.Result{
		Content: map[string]any{
			"accepted": true, "kind": action.Kind, "plan": code, "label": action.Label,
			"note": "this is shown to the admin as a button under your answer; nothing has been changed",
		},
		Trace: agent.Trace{Title: r.l.pick(en, id), Target: code, Detail: r.l.pick("proposed: ", "diusulkan: ") + action.Label},
	}, nil
}

// canAssign says why "assign the best driver" cannot be offered for the plan, or "" when pressing it would work:
// the plan is open and not being cancelled, it has routes without a driver, and each of them has a driver who fits
// well enough to be a suggestion. It is the same suggestion list the dispatcher's screen shows.
func (r *copilotRun) canAssign(ctx context.Context, a *plans.Aggregate) (string, error) {
	code := a.Plan.Code
	switch {
	case a.Plan.Status == contracts.PlanStatusDone || a.Plan.Status == contracts.PlanStatusCancelled:
		return fmt.Sprintf("plan %s is %s", code, strings.ToLower(r.l.planStatus(a.Plan.Status))), nil
	case a.Frozen():
		return fmt.Sprintf("a cancellation of plan %s is under way, nobody can be assigned to it now", code), nil
	}
	waiting := 0
	for i := range a.Routes {
		rt := &a.Routes[i]
		if rt.Status != contracts.RouteStatusAwaitingDriver || rt.DriverID != "" {
			continue
		}
		waiting++
		row, err := r.suggestions(ctx, a, i+1, rt)
		if err != nil {
			return "", err
		}
		switch {
		case row.Best == "":
			return fmt.Sprintf("no available driver fits route %d of plan %s (%s)", i+1, code, strings.ToLower(row.Vehicle)), nil
		case !row.Strong:
			best := slices.IndexFunc(row.Candidates, func(c candidateRow) bool { return c.Driver == row.Best })
			return fmt.Sprintf("the best candidate for route %d of plan %s scores only %d, a weak fit; the admin can still pick a driver by hand", i+1, code, row.Candidates[best].Score), nil
		}
	}
	if waiting == 0 {
		return fmt.Sprintf("every route of plan %s already has a driver", code), nil
	}
	return "", nil
}
