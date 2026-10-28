package ai

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"math"
	"slices"
	"strings"

	"github.com/mseptiaan/loong.lat/backend/pkg/agent"
	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/fleet"
)

// The Copilot's tools. All of them read the one plan the question is about, and nothing else; none changes
// anything (propose_action only records a proposal for the screen). What a tool returns is for the model: every
// figure comes with its text already formatted in the customer's language, so that the model quotes rather than
// calculates. What it shows the customer (title, target, detail) is composed here too, in the same language.

// quantity is a figure with the text a screen would show for it.
type quantity struct {
	Value float64 `json:"value"`
	Text  string  `json:"text"`
}

func (c *copilotRun) rp(n int64) quantity { return quantity{float64(n), c.sp.rupiah(float64(n))} }
func (c *copilotRun) km(v float64) quantity {
	return quantity{math.Round(v*10) / 10, c.sp.km(v)}
}
func (c *copilotRun) mins(v float64) quantity {
	return quantity{math.Round(v), c.sp.duration(v)}
}

// when is a moment as a short text: the clock time today, the date and time otherwise (Jakarta time).
func (c *copilotRun) when(t contracts.Time, now contracts.Time) string { return c.sp.when(t, now.Time) }

func (c *copilotRun) tools() []agent.Tool {
	return []agent.Tool{
		{
			Name:        "get_plan_overview",
			Description: "Reads the delivery plan: its status, priority, schedule, shipments (places and items), and, once confirmed, its routes with vehicle, assigned driver, stops, estimated times and delays. Use it for questions about status, arrival, delays, who is delivering, or what is being sent.",
			Preview:     c.preview("ai.tool.overview"),
			Run:         c.overview,
		},
		{
			Name:        "get_options",
			Description: "Reads the route options computed for this plan: for each option its label, cost, distance, durations, vehicles, the fare breakdown per route, the saving against sending every shipment separately, and the reasons why the option looks as it does. Use it for questions about options, costs, why one was recommended, or the cost breakdown.",
			Preview:     c.preview("ai.tool.options"),
			Run:         c.options,
		},
		{
			Name:        "compare_options",
			Description: "Compares route options exactly: the difference in cost, distance, total time and average arrival time of each option against a reference option (the chosen one by default), and which option is the cheapest, shortest and fastest. The server computes every difference; use it instead of subtracting numbers yourself. Each difference has its amount without a sign and a direction word: say it as 'Rp 7.500 cheaper', never with a minus sign.",
			Schema: jsontext.Value(`{"type":"object","properties":{
				"reference_option_id":{"type":"string","description":"Option to compare against; defaults to the chosen option, else the recommended one."},
				"option_ids":{"type":"array","items":{"type":"string"},"maxItems":8,"description":"Options to compare; defaults to all the others."}
			},"additionalProperties":false}`),
			Preview: c.preview("ai.tool.compare"),
			Run:     c.compare,
		},
		{
			Name:        "check_wallet",
			Description: "Compares the customer's wallet balance with what the plan costs: the plan's total once it is confirmed, otherwise the cost of an option (the chosen one by default). Says whether the balance is enough and by how much it is short or left over.",
			Schema: jsontext.Value(`{"type":"object","properties":{
				"option_id":{"type":"string","description":"Option whose cost to check; defaults to the chosen option, else the recommended one."}
			},"additionalProperties":false}`),
			Preview: c.preview("ai.tool.wallet"),
			Run:     c.wallet,
		},
		{
			Name:        "propose_action",
			Description: "Proposes a button for the customer to press; it changes nothing by itself. Kinds: pilih_opsi (choose a route option; only while the options can still be chosen) and buka_plan (open this plan's page; only once it is confirmed). Name the option for pilih_opsi either by option_id (from get_options) or, without waiting for get_options, by pick: cheapest, shortest, fastest (soonest average arrival) or recommended, which the server resolves. The tool checks the proposal against the real plan and says whether it was accepted.",
			Schema: jsontext.Value(`{"type":"object","properties":{
				"kind":{"type":"string","enum":["pilih_opsi","buka_plan"]},
				"option_id":{"type":"string","description":"For pilih_opsi: the id of the option, from get_options."},
				"pick":{"type":"string","enum":["cheapest","shortest","fastest","recommended"],"description":"For pilih_opsi, instead of option_id: which option to propose."}
			},"required":["kind"],"additionalProperties":false}`),
			Preview: c.preview("ai.tool.propose"),
			Run:     c.propose,
		},
	}
}

// preview is the line shown while a tool runs: its title now, and the plan it is about once that is known.
func (c *copilotRun) preview(titleKey string) func(context.Context, jsontext.Value) agent.Trace {
	return func(ctx context.Context, _ jsontext.Value) agent.Trace {
		return agent.Trace{Title: c.sp.t(titleKey)}
	}
}

// jsonValue renders a tool's content; encoding is left to the agent, which does it deterministically.

/* ---------------------------------- get_plan_overview ---------------------------------- */

type placeJSON struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

type itemJSON struct {
	Name     string  `json:"name"`
	Qty      int     `json:"qty"`
	WeightKg float64 `json:"weightKg"`
	Fragile  bool    `json:"fragile,omitzero"`
}

type shipmentJSON struct {
	N       int        `json:"n"`
	Pickup  placeJSON  `json:"pickup"`
	Dropoff placeJSON  `json:"dropoff"`
	Items   []itemJSON `json:"items"`
}

type stopJSON struct {
	N      int    `json:"n"`
	Kind   string `json:"kind"` // pickup | dropoff
	Place  string `json:"place"`
	Status string `json:"status"`
	// Planned is when the stop was planned for; Issue is what the driver reported, as text.
	Planned string `json:"planned,omitempty"`
	Issue   string `json:"issue,omitempty"`
}

type routeJSON struct {
	N              int        `json:"n"`
	Status         string     `json:"status"`
	Vehicle        string     `json:"vehicle"`
	VehiclePlate   string     `json:"vehiclePlate,omitempty"`
	DriverAssigned bool       `json:"driverAssigned"`
	StartsAt       string     `json:"startsAt,omitempty"`
	StopsDone      int        `json:"stopsDone"`
	StopsTotal     int        `json:"stopsTotal"`
	NextStop       *stopJSON  `json:"nextStop,omitempty"`
	Stops          []stopJSON `json:"stops"`
	Delay          *quantity  `json:"delay,omitempty"`
	Cost           quantity   `json:"cost"`
	FinishedAt     string     `json:"finishedAt,omitempty"`
}

type overviewJSON struct {
	Code            string         `json:"code"`
	Status          string         `json:"status"`
	StatusText      string         `json:"statusText"`
	Priority        string         `json:"priority"`
	Schedule        string         `json:"schedule"`
	Shipments       []shipmentJSON `json:"shipments"`
	OptionsComputed bool           `json:"optionsComputed"`
	ChosenOptionID  string         `json:"chosenOptionId,omitempty"`
	Confirmed       bool           `json:"confirmed"`
	PendingAction   string         `json:"pendingAction,omitempty"`
	TotalCost       *quantity      `json:"totalCost,omitempty"`
	Routes          []routeJSON    `json:"routes,omitempty"`
	CancelReason    string         `json:"cancelReason,omitempty"`
	NowInJakarta    string         `json:"nowInJakarta"`
}

// stopStatus names a stop's state for the model.
var stopStatus = map[contracts.StopStatus]string{
	contracts.StopStatusWaiting: "waiting", contracts.StopStatusEnRoute: "on the way", contracts.StopStatusArrived: "arrived",
	contracts.StopStatusDone: "done", contracts.StopStatusProblem: "problem",
}

var routeStatus = map[contracts.RouteStatus]string{
	contracts.RouteStatusAwaitingDriver: "waiting for a driver", contracts.RouteStatusAssigned: "driver assigned, not started",
	contracts.RouteStatusRunning: "running", contracts.RouteStatusDone: "finished",
}

func (c *copilotRun) overview(ctx context.Context, _ jsontext.Value) (agent.Result, error) {
	p, err := c.plan(ctx)
	if err != nil {
		return agent.Result{}, agent.Failure("the plan is not available")
	}
	now := contracts.At(c.s.now())
	out := overviewJSON{
		Code: p.Code, Status: string(p.Status), StatusText: c.sp.planStatus(p.Status), Priority: c.sp.preset(p.Priority.Preset),
		Schedule: c.schedule(p, now), OptionsComputed: p.OptionSet != nil && len(p.OptionSet.Options) > 0,
		ChosenOptionID: p.SelectedOptionID, Confirmed: len(p.Routes) > 0, PendingAction: p.PendingAction,
		NowInJakarta: c.sp.dateTime(now.Time), Shipments: []shipmentJSON{},
	}
	if p.Status == contracts.PlanStatusCancelled {
		out.CancelReason = clip(p.CancelReason, 200)
	}
	for i, sh := range p.Shipments {
		js := shipmentJSON{N: i + 1, Pickup: place(sh.Pickup), Dropoff: place(sh.Dropoff), Items: []itemJSON{}}
		for _, it := range sh.Items {
			js.Items = append(js.Items, itemJSON{Name: clip(it.Name, 80), Qty: it.Qty, WeightKg: it.WeightKg, Fragile: it.Fragile})
		}
		out.Shipments = append(out.Shipments, js)
	}
	if out.Confirmed {
		total := c.rp(p.TotalCostRp)
		out.TotalCost = &total
	}
	for i, r := range p.Routes {
		out.Routes = append(out.Routes, c.route(ctx, i+1, r, now))
	}

	detail := c.sp.t("ai.detail.overview", "code", p.Code, "status", out.StatusText, "priority", out.Priority, "shipments", len(p.Shipments), "routes", len(p.Routes))
	return agent.Result{Content: out, Trace: agent.Trace{Title: c.sp.t("ai.tool.overview"), Target: p.Code, Detail: detail}}, nil
}

func (c *copilotRun) schedule(p contracts.Plan, now contracts.Time) string {
	if p.Schedule.Mode == contracts.ScheduleModeScheduled && p.Schedule.Window != nil {
		return c.when(p.Schedule.Window.From, now) + " - " + c.when(p.Schedule.Window.To, now)
	}
	return "now"
}

func place(w contracts.Waypoint) placeJSON {
	return placeJSON{Name: clip(w.Name, 80), Address: clip(w.Address, 160)}
}

func (c *copilotRun) route(ctx context.Context, n int, r contracts.Route, now contracts.Time) routeJSON {
	out := routeJSON{
		N: n, Status: routeStatus[r.Status], Vehicle: c.sp.vehicle(r.VehicleType), DriverAssigned: r.DriverID != "",
		StartsAt: c.when(r.StartAt, now), StopsTotal: len(r.Stops), Cost: c.rp(r.CostRp), Stops: []stopJSON{},
		FinishedAt: c.when(r.FinishedAt, now),
	}
	if r.VehicleID != "" {
		if plate, ok, err := fleet.Plate(ctx, c.s.pool, r.VehicleID); err == nil && ok {
			out.VehiclePlate = plate
		}
	}
	if r.DelayMin >= 1 && r.Status != contracts.RouteStatusDone {
		d := c.mins(r.DelayMin)
		out.Delay = &d
	}
	for _, st := range r.Stops {
		js := stopJSON{N: st.Seq, Kind: "dropoff", Place: clip(st.Waypoint.Name, 80), Status: stopStatus[st.Status], Planned: c.when(st.ArriveAt, now)}
		if st.Kind == contracts.StopKindPickup {
			js.Kind = "pickup"
		}
		if st.Issue != nil {
			js.Issue = c.sp.msg(*st.Issue)
		}
		if st.Status == contracts.StopStatusDone || st.Status == contracts.StopStatusProblem {
			out.StopsDone++
		} else if out.NextStop == nil {
			next := js
			out.NextStop = &next
		}
		out.Stops = append(out.Stops, js)
	}
	return out
}

/* ---------------------------------- get_options ---------------------------------- */

type fareJSON struct {
	Label  string   `json:"label"`
	Amount quantity `json:"amount"`
}

type optionRouteJSON struct {
	N       int        `json:"n"`
	Vehicle string     `json:"vehicle"`
	Stops   int        `json:"stops"`
	Cost    quantity   `json:"cost"`
	Fare    []fareJSON `json:"fare"`
}

type savingsJSON struct {
	Cost     quantity `json:"cost"`
	Percent  string   `json:"percent"`
	Distance quantity `json:"distance"`
}

type optionJSON struct {
	ID          string            `json:"id"`
	Label       string            `json:"label"`
	Preset      string            `json:"preset"`
	Recommended bool              `json:"recommended"`
	Chosen      bool              `json:"chosen"`
	Headline    string            `json:"headline"`
	Cost        quantity          `json:"cost"`
	Distance    quantity          `json:"distance"`
	Duration    quantity          `json:"totalTime"`
	AvgArrival  quantity          `json:"averageArrival"`
	Vehicles    string            `json:"vehicles"`
	Stops       int               `json:"stops"`
	Saving      savingsJSON       `json:"savingVsSeparate"`
	Reasons     []string          `json:"reasons"`
	Routes      []optionRouteJSON `json:"routes"`
}

type baselineJSON struct {
	Cost     quantity `json:"cost"`
	Distance quantity `json:"distance"`
}

type optionsJSON struct {
	Computed bool          `json:"computed"`
	Baseline *baselineJSON `json:"separateShipmentsBaseline,omitempty"`
	Options  []optionJSON  `json:"options"`
	Note     string        `json:"note,omitempty"`
}

// chosen is the option the customer picked, else the recommended one, else the first.
func chosen(p contracts.Plan) (contracts.PlanOption, bool) {
	if p.OptionSet == nil || len(p.OptionSet.Options) == 0 {
		return contracts.PlanOption{}, false
	}
	opts := p.OptionSet.Options
	if i := slices.IndexFunc(opts, func(o contracts.PlanOption) bool { return o.ID == p.SelectedOptionID }); i >= 0 {
		return opts[i], true
	}
	if i := slices.IndexFunc(opts, func(o contracts.PlanOption) bool { return o.Recommended }); i >= 0 {
		return opts[i], true
	}
	return opts[0], true
}

func (c *copilotRun) optionJSON(p contracts.Plan, o contracts.PlanOption) optionJSON {
	out := optionJSON{
		ID: o.ID, Label: c.sp.msg(o.Label), Preset: c.sp.preset(o.Preset), Recommended: o.Recommended, Chosen: o.ID == p.SelectedOptionID,
		Headline: c.sp.msg(o.Headline), Cost: c.rp(o.Totals.CostRp), Distance: c.km(o.Totals.DistanceKm), Duration: c.mins(o.Totals.DurationMin),
		AvgArrival: c.mins(o.Totals.AvgDeliveryMin), Vehicles: c.sp.fleet(o.Totals), Stops: o.Totals.Stops,
		Saving:  savingsJSON{Cost: c.rp(o.Savings.CostRp), Percent: c.sp.format("pct", o.Savings.Pct), Distance: c.km(o.Savings.DistanceKm)},
		Reasons: []string{}, Routes: []optionRouteJSON{},
	}
	for _, r := range o.Reasons {
		out.Reasons = append(out.Reasons, c.sp.msg(r))
	}
	for i, r := range o.Routes {
		jr := optionRouteJSON{N: i + 1, Vehicle: c.sp.vehicle(r.VehicleType), Stops: len(r.Stops), Cost: c.rp(r.CostRp), Fare: []fareJSON{}}
		for _, l := range r.Fare {
			jr.Fare = append(jr.Fare, fareJSON{Label: c.sp.msg(l.Label), Amount: c.rp(l.AmountRp)})
		}
		out.Routes = append(out.Routes, jr)
	}
	return out
}

func (c *copilotRun) options(ctx context.Context, _ jsontext.Value) (agent.Result, error) {
	p, err := c.plan(ctx)
	if err != nil {
		return agent.Result{}, agent.Failure("the plan is not available")
	}
	if p.OptionSet == nil || len(p.OptionSet.Options) == 0 {
		return agent.Result{
			Content: optionsJSON{Options: []optionJSON{}, Note: "the route options have not been calculated yet"},
			Trace:   agent.Trace{Title: c.sp.t("ai.tool.options"), Target: p.Code, Detail: c.sp.t("ai.detail.noOptions")},
		}, nil
	}
	set := p.OptionSet
	out := optionsJSON{Computed: true, Options: []optionJSON{}, Baseline: &baselineJSON{Cost: c.rp(set.Baseline.CostRp), Distance: c.km(set.Baseline.DistanceKm)}}
	var labels, lines []string
	for _, o := range set.Options {
		out.Options = append(out.Options, c.optionJSON(p, o))
		labels = append(labels, c.sp.msg(o.Label))
		lines = append(lines, c.sp.t("ai.detail.optionLine", "label", c.sp.msg(o.Label), "cost", o.Totals.CostRp, "distance", o.Totals.DistanceKm, "duration", o.Totals.DurationMin))
	}
	detail := c.sp.t("ai.detail.options", "options", strings.Join(labels, ", "), "cost", set.Baseline.CostRp, "distance", set.Baseline.DistanceKm) + "\n" + strings.Join(lines, "\n")
	return agent.Result{
		Content: out,
		Trace:   agent.Trace{Title: c.sp.t("ai.tool.options"), Target: c.sp.t("ai.optionsTarget", "count", len(set.Options)), Detail: detail},
	}, nil
}

/* ---------------------------------- compare_options ---------------------------------- */

type diffJSON struct {
	Value float64 `json:"value"` // the option minus the reference
	Text  string  `json:"text"`  // signed and formatted: +Rp 5.000
	// Amount is the size of the difference without a sign, to be said with its direction: "Rp 5.000 cheaper".
	Amount    string `json:"amount"`
	Direction string `json:"direction"`
}

type comparisonJSON struct {
	ID         string   `json:"id"`
	Label      string   `json:"label"`
	Cost       diffJSON `json:"cost"`
	Distance   diffJSON `json:"distance"`
	TotalTime  diffJSON `json:"totalTime"`
	AvgArrival diffJSON `json:"averageArrival"`
}

type bestJSON struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Value string `json:"value"`
}

type compareJSON struct {
	Reference   bestJSON         `json:"reference"`
	Comparisons []comparisonJSON `json:"comparisons"`
	Cheapest    bestJSON         `json:"cheapest"`
	Shortest    bestJSON         `json:"shortest"`
	Fastest     bestJSON         `json:"fastestArrival"`
}

// diff states the difference of two values: signed text, and in words whether it is better, worse or nothing.
// less and more are the directions for a lower and a higher value; fmt formats an absolute value; the epsilon is
// what rounds to nothing in that unit.
func diff(v, epsilon float64, format func(float64) string, less, more string) diffJSON {
	switch {
	case math.Abs(v) < epsilon:
		return diffJSON{Value: 0, Text: format(0), Amount: format(0), Direction: "same"}
	case v > 0:
		return diffJSON{Value: v, Text: "+" + format(v), Amount: format(v), Direction: more}
	}
	return diffJSON{Value: v, Text: "-" + format(-v), Amount: format(-v), Direction: less}
}

func (c *copilotRun) compare(ctx context.Context, args jsontext.Value) (agent.Result, error) {
	var in struct {
		Reference string   `json:"reference_option_id"`
		IDs       []string `json:"option_ids"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return agent.Result{}, agent.Failure("the arguments could not be read")
	}
	p, err := c.plan(ctx)
	if err != nil {
		return agent.Result{}, agent.Failure("the plan is not available")
	}
	if p.OptionSet == nil || len(p.OptionSet.Options) == 0 {
		return agent.Result{}, agent.Failure("the route options have not been calculated yet")
	}
	opts := p.OptionSet.Options
	find := func(id string) (contracts.PlanOption, bool) {
		i := slices.IndexFunc(opts, func(o contracts.PlanOption) bool { return o.ID == id })
		if i < 0 {
			return contracts.PlanOption{}, false
		}
		return opts[i], true
	}
	known := func() string {
		ids := make([]string, len(opts))
		for i, o := range opts {
			ids[i] = o.ID
		}
		return strings.Join(ids, ", ")
	}
	ref, _ := chosen(p)
	if in.Reference != "" {
		var ok bool
		if ref, ok = find(in.Reference); !ok {
			return agent.Result{}, agent.Failure("there is no option %q; the options are: %s", in.Reference, known())
		}
	}
	targets := opts
	if len(in.IDs) > 0 {
		targets = nil
		for _, id := range in.IDs {
			o, ok := find(id)
			if !ok {
				return agent.Result{}, agent.Failure("there is no option %q; the options are: %s", id, known())
			}
			targets = append(targets, o)
		}
	}

	out := compareJSON{Reference: bestJSON{ID: ref.ID, Label: c.sp.msg(ref.Label), Value: c.sp.rupiah(float64(ref.Totals.CostRp))}, Comparisons: []comparisonJSON{}}
	rp := func(v float64) string { return c.sp.rupiah(v) }
	km := func(v float64) string { return c.sp.km(v) }
	dur := func(v float64) string { return c.sp.duration(v) }
	var lines []string
	for _, o := range targets {
		if o.ID == ref.ID {
			continue
		}
		cmp := comparisonJSON{
			ID: o.ID, Label: c.sp.msg(o.Label),
			Cost:       diff(float64(o.Totals.CostRp-ref.Totals.CostRp), 0.5, rp, "cheaper", "pricier"),
			Distance:   diff(o.Totals.DistanceKm-ref.Totals.DistanceKm, 0.05, km, "shorter", "longer"),
			TotalTime:  diff(o.Totals.DurationMin-ref.Totals.DurationMin, 0.5, dur, "faster", "slower"),
			AvgArrival: diff(o.Totals.AvgDeliveryMin-ref.Totals.AvgDeliveryMin, 0.5, dur, "sooner", "later"),
		}
		out.Comparisons = append(out.Comparisons, cmp)
		lines = append(lines, c.sp.t("ai.detail.compareLine", "label", cmp.Label, "cost", cmp.Cost.Text, "distance", cmp.Distance.Text, "arrival", cmp.AvgArrival.Text))
	}
	best := func(value func(contracts.PlanOption) float64, text func(contracts.PlanOption) string) bestJSON {
		b := opts[0]
		for _, o := range opts[1:] {
			if value(o) < value(b) {
				b = o
			}
		}
		return bestJSON{ID: b.ID, Label: c.sp.msg(b.Label), Value: text(b)}
	}
	out.Cheapest = best(func(o contracts.PlanOption) float64 { return float64(o.Totals.CostRp) }, func(o contracts.PlanOption) string { return rp(float64(o.Totals.CostRp)) })
	out.Shortest = best(func(o contracts.PlanOption) float64 { return o.Totals.DistanceKm }, func(o contracts.PlanOption) string { return km(o.Totals.DistanceKm) })
	out.Fastest = best(func(o contracts.PlanOption) float64 { return o.Totals.AvgDeliveryMin }, func(o contracts.PlanOption) string { return dur(o.Totals.AvgDeliveryMin) })

	detail := c.sp.t("ai.detail.compare", "label", out.Reference.Label) + "\n" + strings.Join(lines, "\n") + "\n" +
		c.sp.t("ai.detail.compareBest", "cheapest", out.Cheapest.Label, "shortest", out.Shortest.Label, "fastest", out.Fastest.Label)
	return agent.Result{
		Content: out,
		Trace:   agent.Trace{Title: c.sp.t("ai.tool.compare"), Target: c.sp.t("ai.optionsTarget", "count", len(opts)), Detail: strings.TrimSpace(detail)},
	}, nil
}

/* ---------------------------------- check_wallet ---------------------------------- */

type walletJSON struct {
	Balance   quantity  `json:"balance"`
	Cost      quantity  `json:"cost"`
	CostOf    string    `json:"costIs"` // what the cost is the cost of
	Enough    bool      `json:"enough"`
	Shortfall *quantity `json:"shortBy,omitempty"`
	Left      *quantity `json:"leftAfterPaying,omitempty"`
	Paid      bool      `json:"alreadyPaid"`
}

func (c *copilotRun) wallet(ctx context.Context, args jsontext.Value) (agent.Result, error) {
	var in struct {
		OptionID string `json:"option_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return agent.Result{}, agent.Failure("the arguments could not be read")
	}
	p, err := c.plan(ctx)
	if err != nil {
		return agent.Result{}, agent.Failure("the plan is not available")
	}
	var cost int64
	var costOf string
	paid := len(p.Routes) > 0
	switch {
	case paid && in.OptionID == "":
		cost, costOf = p.TotalCostRp, "the total of the confirmed plan"
	default:
		o, ok := chosen(p)
		if in.OptionID != "" && p.OptionSet != nil {
			i := slices.IndexFunc(p.OptionSet.Options, func(o contracts.PlanOption) bool { return o.ID == in.OptionID })
			if i < 0 {
				return agent.Result{}, agent.Failure("there is no option %q for this plan", in.OptionID)
			}
			o, ok = p.OptionSet.Options[i], true
		}
		if !ok {
			return agent.Result{}, agent.Failure("the route options have not been calculated yet, so there is no cost to check")
		}
		cost, costOf = o.Totals.CostRp, "the option "+c.sp.msg(o.Label)
	}
	balance, err := c.s.balance(ctx, c.customerID)
	if err != nil {
		return agent.Result{}, err
	}
	out := walletJSON{Balance: c.rp(balance), Cost: c.rp(cost), CostOf: costOf, Enough: balance >= cost, Paid: paid && in.OptionID == ""}
	detail := c.sp.t("ai.detail.wallet", "balance", balance, "cost", cost)
	if balance >= cost {
		left := c.rp(balance - cost)
		out.Left = &left
		detail += "\n" + c.sp.t("ai.detail.walletLeft", "amount", balance-cost)
	} else {
		short := c.rp(cost - balance)
		out.Shortfall = &short
		detail += "\n" + c.sp.t("ai.detail.walletShort", "amount", cost-balance)
	}
	return agent.Result{Content: out, Trace: agent.Trace{Title: c.sp.t("ai.tool.wallet"), Target: p.Code, Detail: detail}}, nil
}

/* ---------------------------------- propose_action ---------------------------------- */

type proposalJSON struct {
	Accepted bool   `json:"accepted"`
	Action   string `json:"action,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

func (c *copilotRun) propose(ctx context.Context, args jsontext.Value) (agent.Result, error) {
	var in struct {
		Kind     string `json:"kind"`
		OptionID string `json:"option_id"`
		Pick     string `json:"pick"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return agent.Result{}, agent.Failure("the arguments could not be read")
	}
	p, err := c.plan(ctx)
	if err != nil {
		return agent.Result{}, agent.Failure("the plan is not available")
	}
	title := c.sp.t("ai.tool.propose")
	reject := func(key string) (agent.Result, error) {
		reason := c.sp.t(key)
		return agent.Result{
			Content: proposalJSON{Reason: reason},
			Trace:   agent.Trace{Title: title, Target: p.Code, Detail: c.sp.t("ai.detail.proposeNo", "reason", reason)},
		}, nil
	}
	accept := func(a CopilotAction, detail string) (agent.Result, error) {
		c.recordAction(a)
		return agent.Result{
			Content: proposalJSON{Accepted: true, Action: a.Label},
			Trace:   agent.Trace{Title: title, Target: p.Code, Detail: detail},
		}, nil
	}

	switch in.Kind {
	case actionChooseOption:
		if p.PendingAction != "" {
			return reject("ai.reject.pending")
		}
		if p.Status != contracts.PlanStatusDraft && p.Status != contracts.PlanStatusOptionsReady {
			return reject("ai.reject.locked")
		}
		if p.OptionSet == nil {
			return reject("ai.reject.noOption")
		}
		optionID := in.OptionID
		if optionID == "" {
			if in.Pick == "" {
				return agent.Result{}, agent.Failure("name the option: option_id (from get_options) or pick (cheapest, shortest, fastest or recommended)")
			}
			id, ok := pickOption(p.OptionSet.Options, in.Pick)
			if !ok {
				return agent.Result{}, agent.Failure("unknown pick %q; use cheapest, shortest, fastest or recommended", in.Pick)
			}
			optionID = id
		}
		i := slices.IndexFunc(p.OptionSet.Options, func(o contracts.PlanOption) bool { return o.ID == optionID })
		if i < 0 {
			return reject("ai.reject.noOption")
		}
		o := p.OptionSet.Options[i]
		if o.ID == p.SelectedOptionID {
			return reject("ai.reject.chosen")
		}
		label := c.sp.msg(o.Label)
		return accept(CopilotAction{
			ID: "pilih:" + o.ID, Label: c.sp.t("ai.action.chooseOption", "label", label), Kind: actionChooseOption,
			PlanID: p.ID, OptionID: o.ID, Primary: true,
		}, c.sp.t("ai.detail.proposeChoose", "label", label))
	case actionOpenPlan:
		if len(p.Routes) == 0 {
			return reject("ai.reject.notConfirmed")
		}
		return accept(CopilotAction{
			ID: "buka:" + p.ID, Label: c.sp.t("ai.action.open", "code", p.Code), Kind: actionOpenPlan, PlanID: p.ID,
		}, c.sp.t("ai.detail.proposeOpen", "code", p.Code))
	}
	return agent.Result{}, agent.Failure("unknown kind %q; use %s or %s", in.Kind, actionChooseOption, actionOpenPlan)
}

// pickOption resolves "the cheapest" and its kin to an option of the plan, in the server's own terms: the lowest
// cost, the shortest distance, the soonest average arrival, the one the optimizer recommended. A tie goes to the
// first option.
func pickOption(opts []contracts.PlanOption, pick string) (id string, ok bool) {
	if len(opts) == 0 {
		return "", false
	}
	best := func(value func(contracts.PlanOption) float64) string {
		b := opts[0]
		for _, o := range opts[1:] {
			if value(o) < value(b) {
				b = o
			}
		}
		return b.ID
	}
	switch pick {
	case "cheapest":
		return best(func(o contracts.PlanOption) float64 { return float64(o.Totals.CostRp) }), true
	case "shortest":
		return best(func(o contracts.PlanOption) float64 { return o.Totals.DistanceKm }), true
	case "fastest":
		return best(func(o contracts.PlanOption) float64 { return o.Totals.AvgDeliveryMin }), true
	case "recommended":
		if i := slices.IndexFunc(opts, func(o contracts.PlanOption) bool { return o.Recommended }); i >= 0 {
			return opts[i].ID, true
		}
	}
	return "", false
}

// clip shortens user-written text and drops its control characters before it goes to a model.
func clip(s string, limit int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 && r != ' ' || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return s
}
