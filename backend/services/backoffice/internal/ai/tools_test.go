package ai

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/agent"
	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/dispatch"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/plans"
)

// tool runs one of the Copilot's tools directly, as the agent loop would after checking the arguments.
func (r *rig) tool(t *testing.T, name, args string, locale platform.Locale) (agent.Result, error) {
	t.Helper()
	return r.toolIn(t, &copilotRun{s: r.service(nil), l: newLang(locale)}, name, args)
}

func (r *rig) toolIn(t *testing.T, rn *copilotRun, name, args string) (agent.Result, error) {
	t.Helper()
	for _, tool := range rn.tools() {
		if tool.Name == name {
			return tool.Run(context.Background(), jsontext.Value(args))
		}
	}
	t.Fatalf("no tool %q", name)
	return agent.Result{}, nil
}

// mustTool is tool for a call that must succeed.
func mustTool[T any](t *testing.T, r *rig, name, args string, locale platform.Locale) (T, agent.Trace) {
	t.Helper()
	res, err := r.tool(t, name, args, locale)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	got, ok := res.Content.(T)
	if !ok {
		t.Fatalf("%s returned %T", name, res.Content)
	}
	return got, res.Trace
}

func planCodes(rows []planRow) []string {
	var out []string
	for _, row := range rows {
		out = append(out, row.Plan)
	}
	return out
}

func TestQueueToolReadsWhatWaitsAndWhatIsWrong(t *testing.T) {
	r := newRig(t)
	r.seed()
	q, trace := mustTool[queueResult](t, r, "get_queue", `{}`, platform.LocaleEN)

	if q.ActivePlans != 6 || q.Running != 2 || q.AssignedNotStarted != 1 || q.DriversReady != 3 || q.Cancelling != 0 {
		t.Errorf("counts = active %d running %d assigned %d ready %d cancelling %d, want 6 2 1 3 0", q.ActivePlans, q.Running, q.AssignedNotStarted, q.DriversReady, q.Cancelling)
	}
	// The longest waiting "now" plan first, the scheduled one after.
	if got, want := planCodes(q.WaitingForDriver.Plans), []string{"PL-2001", "PL-2002", "PL-2003"}; !slices.Equal(got, want) || q.WaitingForDriver.Count != 3 {
		t.Errorf("waiting = %v (count %d), want %v", got, q.WaitingForDriver.Count, want)
	}
	first := q.WaitingForDriver.Plans[0]
	if first.WaitingFor == nil || first.WaitingFor.Value != 45 || first.WaitingFor.Text != "45 min" {
		t.Errorf("PL-2001 waiting for = %+v, want 45 min", first.WaitingFor)
	}
	if first.Customer != "Dina Pramesti" || first.Status != "Finding a driver" || first.Priority != "Balanced" || first.Schedule != "as soon as possible" ||
		first.RoutesWithoutDriver != 1 || !slices.Equal(first.Vehicles, []string{"Motor"}) || first.From != "Kemang Village" {
		t.Errorf("PL-2001 row = %+v", first)
	}
	scheduled := q.WaitingForDriver.Plans[2]
	if scheduled.Schedule != "scheduled for 22 Oct 09:00" || scheduled.WaitingFor != nil {
		t.Errorf("PL-2003 schedule = %q, waiting for %+v; want scheduled for 22 Oct 09:00 and no waiting time", scheduled.Schedule, scheduled.WaitingFor)
	}

	// What needs attention: the late plan first (12 min), then the one with a problem stop.
	if got, want := planCodes(q.NeedAttention.Plans), []string{"PL-2004", "PL-2005"}; !slices.Equal(got, want) {
		t.Fatalf("attention = %v, want %v", got, want)
	}
	late, problem := q.NeedAttention.Plans[0], q.NeedAttention.Plans[1]
	if late.Delay == nil || late.Delay.Value != 12 || !slices.Equal(late.Why, []string{"12 min late"}) || !slices.Equal(late.Drivers, []string{"Eko"}) {
		t.Errorf("PL-2004 row = %+v", late)
	}
	if problem.ProblemStops != 1 || !slices.Equal(problem.Why, []string{"1 problem stop(s)"}) {
		t.Errorf("PL-2005 row = %+v", problem)
	}

	if trace.Title != "Reading the queue" || trace.Target != "6 active plans" ||
		!strings.Contains(trace.Detail, "waiting for a driver: 3") || !strings.Contains(trace.Detail, "anomalies: 2") || !strings.Contains(trace.Detail, "PL-2004: 12 min late") {
		t.Errorf("trace = %+v", trace)
	}
}

func TestQueueToolSpeaksIndonesian(t *testing.T) {
	r := newRig(t)
	r.seed()
	q, trace := mustTool[queueResult](t, r, "get_queue", `{}`, platform.LocaleID)
	if trace.Title != "Membaca antrean" || trace.Target != "6 plan aktif" || !strings.Contains(trace.Detail, "menunggu driver: 3") {
		t.Errorf("trace = %+v", trace)
	}
	if first := q.WaitingForDriver.Plans[0]; first.Status != "Mencari driver" || first.Priority != "Seimbang" || first.Schedule != "secepatnya" || first.WaitingFor.Text != "45 mnt" {
		t.Errorf("PL-2001 row = %+v", first)
	}
	if sched := q.WaitingForDriver.Plans[2]; sched.Schedule != "dijadwalkan 22 Okt 09.00" {
		t.Errorf("PL-2003 schedule = %q, want dijadwalkan 22 Okt 09.00", sched.Schedule)
	}
	if late := q.NeedAttention.Plans[0]; !slices.Equal(late.Why, []string{"terlambat 12 mnt"}) {
		t.Errorf("PL-2004 why = %v", late.Why)
	}
}

func TestQueueToolSetsAPlanBeingCancelledApart(t *testing.T) {
	r := newRig(t)
	r.seed()
	r.mutate("pln-1", func(a *plans.Aggregate) { a.Freeze("req-1") })
	q, _ := mustTool[queueResult](t, r, "get_queue", `{}`, platform.LocaleEN)
	if q.Cancelling != 1 || slices.Contains(planCodes(q.WaitingForDriver.Plans), "PL-2001") {
		t.Errorf("a plan with a cancel under way must be counted apart and not listed as waiting: cancelling %d, waiting %v", q.Cancelling, planCodes(q.WaitingForDriver.Plans))
	}
}

func TestPlanToolReadsRoutesStopsAndProblems(t *testing.T) {
	r := newRig(t)
	r.seed()

	// By code in any case, and by id.
	for _, ref := range []string{"PL-2005", "pl-2005", "pln-5"} {
		p, trace := mustTool[planDetail](t, r, "get_plan", `{"plan":"`+ref+`"}`, platform.LocaleEN)
		if p.Plan != "PL-2005" || p.ID != "pln-5" || p.Customer != "Agus Salim" || p.Status != "On the way" || p.Cost.Value != 20000 || p.Cost.Text != "Rp 20,000" {
			t.Errorf("get_plan(%q) = %+v", ref, p)
		}
		if trace.Title != "Reading the plan" || trace.Target != "PL-2005" {
			t.Errorf("trace = %+v", trace)
		}
	}
	p, _ := mustTool[planDetail](t, r, "get_plan", `{"plan":"PL-2005"}`, platform.LocaleEN)
	if len(p.Routes) != 1 {
		t.Fatalf("routes = %d, want 1", len(p.Routes))
	}
	route := p.Routes[0]
	if route.Driver == nil || route.Driver.Name != "Fani" || route.Status != "on the way" || route.StopsDone != "1/2" || len(route.Stops) != 2 {
		t.Errorf("route = %+v", route)
	}
	if len(route.ProblemStops) != 1 {
		t.Fatalf("problem stops = %+v, want one", route.ProblemStops)
	}
	if ps := route.ProblemStops[0]; ps.Seq != 1 || ps.Reason != "Nobody at the location" || ps.Note != "Pintu terkunci" || ps.Place != "Pickup pln-5:r1" {
		t.Errorf("problem stop = %+v", ps)
	}

	// The same stop in Indonesian.
	pid, _ := mustTool[planDetail](t, r, "get_plan", `{"plan":"PL-2005"}`, platform.LocaleID)
	if ps := pid.Routes[0].ProblemStops[0]; ps.Reason != "Tidak ada orang di lokasi" || pid.Status != "Dalam perjalanan" || pid.Cost.Text != "Rp 20.000" {
		t.Errorf("indonesian plan = %+v, problem %+v", pid, ps)
	}

	// A finished two-route plan, and the cancelled one with its reason.
	done, _ := mustTool[planDetail](t, r, "get_plan", `{"plan":"PL-2007"}`, platform.LocaleEN)
	if done.Status != "Done" || len(done.Routes) != 2 || done.Routes[0].StopsDone != "2/2" || done.Routes[1].Delay == nil || done.Routes[1].Delay.Value != 8 {
		t.Errorf("PL-2007 = %+v", done)
	}
	cancelled, _ := mustTool[planDetail](t, r, "get_plan", `{"plan":"PL-2008"}`, platform.LocaleEN)
	if cancelled.Status != "Cancelled" || cancelled.CancelReason != "Dibatalkan oleh pengirim" {
		t.Errorf("PL-2008 = %+v", cancelled)
	}
}

func TestPlanToolRefusesAPlanThatDoesNotExist(t *testing.T) {
	r := newRig(t)
	r.seed()
	for _, args := range []string{`{"plan":"PL-9999"}`, `{"plan":"pln-invented"}`, `{}`} {
		res, err := r.tool(t, "get_plan", args, platform.LocaleEN)
		if _, readable := errors.AsType[*agent.ToolError](err); !readable {
			t.Fatalf("get_plan(%s) error = %v, want a failure the model can read", args, err)
		}
		if res.Trace.Title != "Reading the plan" || res.Trace.Detail == "" {
			t.Errorf("get_plan(%s) trace = %+v, want a titled line with the reason", args, res.Trace)
		}
	}
}

func TestDriversToolShowsAvailabilityPositionAndWorkload(t *testing.T) {
	r := newRig(t)
	w := r.seed()
	d, trace := mustTool[driversResult](t, r, "list_drivers", `{}`, platform.LocaleEN)

	if want := (driverCounts{Idle: 3, OnDuty: 2, Offline: 1, Total: 6}); d.Counts != want {
		t.Errorf("counts = %+v, want %+v", d.Counts, want)
	}
	var names []string
	for _, row := range d.Drivers {
		names = append(names, row.Name+"/"+row.Status)
	}
	// Idle first, then on duty, then offline; by name inside.
	want := []string{"Budi/online and idle", "Citra/online and idle", "Dodi/online and idle", "Eko/on duty", "Fani/on duty", "Gita/offline"}
	if !slices.Equal(names, want) {
		t.Errorf("drivers = %v, want %v", names, want)
	}
	byName := map[string]driverRow{}
	for _, row := range d.Drivers {
		byName[row.Name] = row
	}
	if b := byName["Budi"]; b.ID != w.budi || b.Position == nil || b.Vehicle != "Model motor · B 1001 AB" || b.VehicleType != "Motor" || b.Rating != 5 || len(b.Routes) != 0 {
		t.Errorf("Budi = %+v", b)
	}
	if f := byName["Fani"]; len(f.Routes) != 2 {
		t.Errorf("Fani's workload = %+v, want his running route and the one for tomorrow", f.Routes)
	} else if f.Routes[0].Plan != "PL-2005" && f.Routes[1].Plan != "PL-2005" {
		t.Errorf("Fani's workload = %+v, want PL-2005 among them", f.Routes)
	}
	if byName["Gita"].Position != nil {
		t.Errorf("Gita never reported a position, got %+v", byName["Gita"].Position)
	}
	if trace.Title != "Checking driver availability" || trace.Target != "3 drivers ready" || !strings.Contains(trace.Detail, "online and idle: 3") || !strings.Contains(trace.Detail, "on duty: 2") {
		t.Errorf("trace = %+v", trace)
	}

	idle, _ := mustTool[driversResult](t, r, "list_drivers", `{"status":"idle","limit":2}`, platform.LocaleEN)
	if len(idle.Drivers) != 2 || idle.More != 1 || idle.Counts.Total != 6 {
		t.Errorf("idle drivers = %d listed, more %d, total %d; want 2, 1, 6", len(idle.Drivers), idle.More, idle.Counts.Total)
	}
	off, _ := mustTool[driversResult](t, r, "list_drivers", `{"status":"offline"}`, platform.LocaleID)
	if len(off.Drivers) != 1 || off.Drivers[0].Name != "Gita" || off.Drivers[0].Status != "offline" {
		t.Errorf("offline drivers = %+v", off.Drivers)
	}
}

func TestVehiclesToolListsTheFleet(t *testing.T) {
	r := newRig(t)
	r.seed()
	v, trace := mustTool[vehiclesResult](t, r, "list_vehicles", `{}`, platform.LocaleEN)
	if v.Total != 6 || v.ByType["Motor"] != 5 || v.ByType["Blindvan"] != 1 || v.ByStatus["active"] != 6 || len(v.Vehicles) != 6 {
		t.Errorf("fleet = %+v", v)
	}
	var dodi vehicleRow
	for _, row := range v.Vehicles {
		if row.Plate == "B 1003 AB" {
			dodi = row
		}
	}
	if dodi.Type != "Blindvan" || dodi.Driver != "Dodi" || dodi.MaxWeightKg != 500 {
		t.Errorf("Dodi's van = %+v", dodi)
	}
	if trace.Title != "Reading the fleet" || trace.Target != "6 vehicles" {
		t.Errorf("trace = %+v", trace)
	}
}

func TestSuggestToolReusesTheDispatchersSuggestions(t *testing.T) {
	r := newRig(t)
	w := r.seed()
	s, trace := mustTool[suggestResult](t, r, "suggest_drivers", `{"plan":"PL-2001"}`, platform.LocaleEN)

	// Exactly the drivers (and numbers) the dispatcher's own suggestion list gives.
	want, err := r.dispatch.Suggest(t.Context(), "pln-1", "pln-1:r1")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Plans) != 1 || s.Plans[0].Plan != "PL-2001" || len(s.Plans[0].Routes) != 1 || len(s.Plans[0].Routes[0].Candidates) != len(want) || len(want) != 2 {
		t.Fatalf("suggestions = %+v, dispatcher has %d", s, len(want))
	}
	row := s.Plans[0].Routes[0]
	for i, c := range row.Candidates {
		var sug dispatch.Suggestion
		for _, x := range want {
			if x.DriverID == c.DriverID {
				sug = x
			}
		}
		if sug.DriverID == "" || c.Score != sug.Score || c.DistanceKm.Value != sug.DistanceToPickupKm || c.Eta.Value != float64(sug.EtaToPickupMin) || c.Fits != sug.Fits {
			t.Errorf("candidate %d = %+v, dispatcher says %+v", i, c, sug)
		}
	}
	if row.Candidates[0].Driver != "Budi" || row.Candidates[0].DriverID != w.budi || row.Candidates[1].Driver != "Citra" {
		t.Errorf("candidates = %+v, want Budi (1 km) before Citra (3 km)", row.Candidates)
	}
	if row.Best != "Budi" || !row.Strong || !row.Candidates[0].AutoAssignPick || row.Candidates[1].AutoAssignPick {
		t.Errorf("best = %q strong %v, picks %v %v; want Budi, strong, picked by automatic assignment", row.Best, row.Strong, row.Candidates[0].AutoAssignPick, row.Candidates[1].AutoAssignPick)
	}
	if row.Vehicle != "Motor" || row.Pickup != "Kemang Village" || row.Candidates[0].DistanceKm.Text != "1.3 km" {
		t.Errorf("route row = %+v, distance %+v", row, row.Candidates[0].DistanceKm)
	}
	if trace.Title != "Scoring driver candidates" || trace.Target != "PL-2001 · 2 candidates" || !strings.Contains(trace.Detail, "Budi: score ") {
		t.Errorf("trace = %+v", trace)
	}

	// The blind van plan has one candidate; a route that has its driver is not rated; a plan already done is refused.
	van, _ := mustTool[suggestResult](t, r, "suggest_drivers", `{"plan":"PL-2002"}`, platform.LocaleID)
	if len(van.Plans) != 1 || len(van.Plans[0].Routes[0].Candidates) != 1 || van.Plans[0].Routes[0].Candidates[0].Driver != "Dodi" {
		t.Errorf("van suggestions = %+v, want Dodi only", van)
	}
	assigned, _ := mustTool[suggestResult](t, r, "suggest_drivers", `{"plan":"PL-2004"}`, platform.LocaleEN)
	if routes := assigned.Plans[0].Routes; len(routes) != 1 || routes[0].AssignedTo != "Eko" || len(routes[0].Candidates) != 0 {
		t.Errorf("assigned route = %+v, want it reported as Eko's", routes)
	}
	if _, err := r.tool(t, "suggest_drivers", `{"plan":"PL-2007"}`, platform.LocaleEN); err == nil {
		t.Error("a finished plan has nothing to assign: want a failure")
	}
	if _, err := r.tool(t, "suggest_drivers", `{"plan":"PL-2001","route":3}`, platform.LocaleEN); err == nil {
		t.Error("route 3 of a one-route plan: want a failure")
	}
}

func TestSuggestToolWithoutAPlanRatesEveryPlanThatWaits(t *testing.T) {
	r := newRig(t)
	r.seed()
	s, trace := mustTool[suggestResult](t, r, "suggest_drivers", `{}`, platform.LocaleEN)

	var got []string
	for _, p := range s.Plans {
		got = append(got, p.Plan)
	}
	// The queue's order: the longest waiting "now" plan, then the other, then the scheduled one; not the plans that
	// have their driver or are being served.
	if want := []string{"PL-2001", "PL-2002", "PL-2003"}; !slices.Equal(got, want) || s.More != 0 {
		t.Fatalf("plans rated = %v (more %d), want %v", got, s.More, want)
	}
	if b := s.Plans[0].Routes[0].Best; b != "Budi" {
		t.Errorf("PL-2001 best = %q", b)
	}
	if b := s.Plans[1].Routes[0].Best; b != "Dodi" {
		t.Errorf("PL-2002 best = %q, want the blind van driver", b)
	}
	if trace.Target != "3 plans · 5 candidates" || !strings.Contains(trace.Detail, "PL-2002:\nDodi: score ") {
		t.Errorf("trace = %+v", trace)
	}

	// In the scope of one plan, no plan argument means that plan.
	rn := newRun(r, platform.LocaleEN)
	rn.scopePlan, rn.scopeCode = "pln-2", "PL-2002"
	res, err := r.toolIn(t, rn, "suggest_drivers", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if one := res.Content.(suggestResult); len(one.Plans) != 1 || one.Plans[0].Plan != "PL-2002" {
		t.Errorf("scoped call = %+v, want PL-2002 only", one)
	}

	// Nothing waits: say so, rate nothing.
	for _, id := range []string{"pln-1", "pln-2", "pln-3"} {
		r.assign(id, 1, r.firstDriver(t), false, 0)
	}
	none, trace := mustTool[suggestResult](t, r, "suggest_drivers", `{}`, platform.LocaleEN)
	if len(none.Plans) != 0 || trace.Detail != "no plan waits for a driver" {
		t.Errorf("with nothing waiting: %+v, %+v", none, trace)
	}
}

func TestSuggestToolSaysWhyThereIsNobody(t *testing.T) {
	r := newRig(t)
	r.seed()
	r.mutate("pln-1", func(a *plans.Aggregate) { a.Freeze("req-1") })
	s, _ := mustTool[suggestResult](t, r, "suggest_drivers", `{"plan":"PL-2001"}`, platform.LocaleEN)
	if row := s.Plans[0].Routes[0]; len(row.Candidates) != 0 || !strings.Contains(row.Note, "cancellation") {
		t.Errorf("frozen plan row = %+v, want no candidates and a note about the cancellation", row)
	}
}

func TestTodayToolCountsTheDay(t *testing.T) {
	r := newRig(t)
	r.seed()
	n, trace := mustTool[todayResult](t, r, "today_numbers", `{}`, platform.LocaleEN)

	if n.Date != "21 Oct 2026" {
		t.Errorf("date = %q", n.Date)
	}
	// Created today and not cancelled: PL-2001 to PL-2006 (PL-2007 is 30 h old, PL-2008 cancelled).
	if n.Plans.Created != 6 || n.Plans.Completed != 1 || n.Plans.Cancelled != 1 || n.Plans.OrderValue.Value != 120000 || n.Plans.OrderValue.Text != "Rp 120,000" {
		t.Errorf("plans = %+v", n.Plans)
	}
	if n.Routes.FinishedToday != 2 || n.Routes.OnTime != 1 || n.Routes.OnTimeRate == nil || n.Routes.OnTimeRate.Text != "50%" {
		t.Errorf("routes = %+v", n.Routes)
	}
	q := n.QueueNow
	if q.Active != 6 || q.Running != 2 || q.WaitingForDriver != 3 || q.NeedAttention != 2 || q.ProblemStops != 1 {
		t.Errorf("queue = %+v", q)
	}
	if n.Drivers.Idle != 3 || n.Drivers.OnDuty != 2 || n.Drivers.Offline != 1 {
		t.Errorf("drivers = %+v", n.Drivers)
	}
	if n.TopUps.PaidToday.Value != 100000 || n.TopUps.PaidToday.Text != "Rp 100,000" || n.TopUps.PaidCount != 1 || n.TopUps.PendingCount != 1 {
		t.Errorf("top-ups = %+v", n.TopUps)
	}
	if trace.Title != "Reading today's numbers" || trace.Target != "6 plans" || !strings.Contains(trace.Detail, "on time: 50%") {
		t.Errorf("trace = %+v", trace)
	}

	id, trace := mustTool[todayResult](t, r, "today_numbers", `{}`, platform.LocaleID)
	if id.Date != "21 Okt 2026" || id.Plans.OrderValue.Text != "Rp 120.000" || trace.Title != "Membaca angka hari ini" {
		t.Errorf("indonesian numbers = %q %q %q", id.Date, id.Plans.OrderValue.Text, trace.Title)
	}
}

func TestTodayToolWithNothingFinishedHasNoRate(t *testing.T) {
	r := newRig(t)
	n, _ := mustTool[todayResult](t, r, "today_numbers", `{}`, platform.LocaleEN)
	if n.Routes.OnTimeRate != nil || n.Plans.Created != 0 || n.Plans.OrderValue.Value != 0 {
		t.Errorf("an empty day = %+v, want no rate (not 0%%) and nothing counted", n)
	}
}

/* ---------------------------------- propose_action ---------------------------------- */

func proposeIn(t *testing.T, r *rig, rn *copilotRun, args string) (agent.Result, error) {
	t.Helper()
	return r.toolIn(t, rn, "propose_action", args)
}

func newRun(r *rig, locale platform.Locale) *copilotRun {
	return &copilotRun{s: r.service(nil), l: newLang(locale)}
}

func TestProposeActionAcceptsOnlyWhatIsTrue(t *testing.T) {
	r := newRig(t)
	r.seed()
	rn := newRun(r, platform.LocaleEN)

	res, err := proposeIn(t, r, rn, `{"kind":"tugaskan_terbaik","plan":"PL-2001"}`)
	if err != nil {
		t.Fatalf("assign PL-2001: %v", err)
	}
	if got := res.Content.(map[string]any); got["accepted"] != true || got["plan"] != "PL-2001" {
		t.Errorf("result = %v", got)
	}
	if _, err := proposeIn(t, r, rn, `{"kind":"buka_plan","plan":"pln-5"}`); err != nil {
		t.Fatalf("open PL-2005: %v", err)
	}
	if _, err := proposeIn(t, r, rn, `{"kind":"buka_plan","plan":"PL-2005"}`); err != nil {
		t.Fatalf("open PL-2005 again: %v", err)
	}
	want := []Action{
		{ID: "tugaskan:pln-1", Label: "Apply the best driver suggestion", Kind: "tugaskan_terbaik", PlanID: "pln-1", Primary: true},
		{ID: "buka:pln-5", Label: "Open PL-2005", Kind: "buka_plan", PlanID: "pln-5"},
	}
	if got := rn.proposed(); !slices.Equal(got, want) {
		t.Errorf("actions = %+v, want %+v", got, want)
	}
}

func TestProposeActionRefusesWhatIsNotTrue(t *testing.T) {
	r := newRig(t)
	r.seed()
	// A plan nobody can carry (25 kg on a motorbike; the van is the wrong vehicle) and one only a far driver could
	// take (13 km south of the pickup of Budi, 14 km from him).
	r.confirm(planSpec{id: "pln-heavy", code: "PL-3001", created: ago(5 * time.Minute), routes: []routeSpec{{contracts.VehicleTypeMotor, 25, 0}}})
	r.confirm(planSpec{id: "pln-far", code: "PL-3002", created: ago(5 * time.Minute), routes: []routeSpec{{contracts.VehicleTypeMotor, 2, -13}}})
	r.mutate("pln-3", func(a *plans.Aggregate) { a.Freeze("req-9") })

	tests := []struct {
		name, args, reason string
	}{
		{"a plan that was invented", `{"kind":"buka_plan","plan":"PL-9999"}`, "there is no plan"},
		{"a plan id that was invented", `{"kind":"tugaskan_terbaik","plan":"pln-nope"}`, "there is no plan"},
		{"no plan at all", `{"kind":"buka_plan"}`, "say which plan"},
		{"a plan whose routes all have a driver", `{"kind":"tugaskan_terbaik","plan":"PL-2004"}`, "already has a driver"},
		{"a finished plan", `{"kind":"tugaskan_terbaik","plan":"PL-2007"}`, "is done"},
		{"a cancelled plan", `{"kind":"tugaskan_terbaik","plan":"PL-2008"}`, "is cancelled"},
		{"a plan being cancelled", `{"kind":"tugaskan_terbaik","plan":"PL-2003"}`, "cancellation"},
		{"a load no vehicle available can carry", `{"kind":"tugaskan_terbaik","plan":"PL-3001"}`, "no available driver fits route 1"},
		{"a best candidate that is a weak fit", `{"kind":"tugaskan_terbaik","plan":"PL-3002"}`, "a weak fit"},
		{"a kind that does not exist", `{"kind":"pilih_opsi","plan":"PL-2001"}`, "unknown kind"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rn := newRun(r, platform.LocaleEN)
			res, err := proposeIn(t, r, rn, tt.args)
			te, readable := errors.AsType[*agent.ToolError](err)
			if !readable || !strings.Contains(te.Message, tt.reason) || !strings.HasPrefix(te.Message, "not proposed: ") {
				t.Fatalf("error = %v, want a failure mentioning %q", err, tt.reason)
			}
			if res.Trace.Title != "Checking the suggested action" || !strings.Contains(res.Trace.Detail, tt.reason) {
				t.Errorf("trace = %+v, want the reason as its detail", res.Trace)
			}
			if got := rn.proposed(); len(got) != 0 {
				t.Errorf("a refused proposal reached the screen: %+v", got)
			}
		})
	}
}

func TestProposeActionDefaultsToThePlanOfTheQuestion(t *testing.T) {
	r := newRig(t)
	r.seed()
	rn := newRun(r, platform.LocaleID)
	rn.scopePlan, rn.scopeCode = "pln-1", "PL-2001"
	if _, err := proposeIn(t, r, rn, `{"kind":"buka_plan"}`); err != nil {
		t.Fatal(err)
	}
	if got := rn.proposed(); len(got) != 1 || got[0].PlanID != "pln-1" || got[0].Label != "Buka PL-2001" || !got[0].Primary {
		t.Errorf("actions = %+v", got)
	}
}

func TestRankOrdersAssignmentsFirstAndCapsThem(t *testing.T) {
	open := func(n string) Action { return Action{ID: "buka:" + n, Kind: actionOpenPlan, PlanID: n} }
	assign := func(n string) Action { return Action{ID: "tugaskan:" + n, Kind: actionAssignBest, PlanID: n} }

	got := rank([]Action{open("a"), open("b"), assign("c"), open("d")})
	var ids []string
	for _, a := range got {
		ids = append(ids, a.ID)
	}
	if !slices.Equal(ids, []string{"tugaskan:c", "buka:a", "buka:b"}) {
		t.Errorf("order = %v, want the assignment first and three buttons at most", ids)
	}
	if !got[0].Primary || got[1].Primary || got[2].Primary {
		t.Errorf("primary = %v %v %v, want only the assignment", got[0].Primary, got[1].Primary, got[2].Primary)
	}
	if got := rank([]Action{open("a"), open("b")}); !got[0].Primary || got[1].Primary {
		t.Errorf("with nothing to assign the first plan to open is the primary button, got %v %v", got[0].Primary, got[1].Primary)
	}
	if got := rank(nil); len(got) != 0 {
		t.Errorf("rank(nil) = %v", got)
	}
}

func TestToolResultsAreJSONTheModelCanRead(t *testing.T) {
	r := newRig(t)
	r.seed()
	for _, name := range []string{"get_queue", "list_drivers", "list_vehicles", "today_numbers"} {
		res, err := r.tool(t, name, `{}`, platform.LocaleEN)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		raw, err := json.Marshal(res.Content, json.Deterministic(true))
		if err != nil || !jsontext.Value(raw).IsValid() {
			t.Errorf("%s: result is not JSON: %v", name, err)
		}
		if strings.Contains(string(raw), "null") && name != "today_numbers" {
			t.Errorf("%s: result has a null the model has to interpret: %s", name, raw)
		}
	}
}

func TestNoToolOrFigureShowsADeliveryCode(t *testing.T) {
	r := newRig(t)
	r.seed()
	var stored int
	if err := r.pool.QueryRow(t.Context(), `SELECT count(*) FROM routes WHERE route::text LIKE '%' || $1 || '%'`, deliveryCode).Scan(&stored); err != nil || stored == 0 {
		t.Fatalf("the seeded routes carry no delivery code (%d, %v): the check below would prove nothing", stored, err)
	}
	calls := map[string]string{
		"get_queue": `{}`, "list_drivers": `{}`, "list_vehicles": `{}`, "today_numbers": `{}`, "suggest_drivers": `{}`,
	}
	for _, code := range []string{"PL-2001", "PL-2004", "PL-2005", "PL-2007"} {
		calls["get_plan:"+code] = `{"plan":"` + code + `"}`
	}
	for name, args := range calls {
		res, err := r.tool(t, strings.SplitN(name, ":", 2)[0], args, platform.LocaleEN)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		raw, _ := json.Marshal(res.Content)
		if strings.Contains(string(raw), deliveryCode) || strings.Contains(res.Trace.Detail, deliveryCode) {
			t.Errorf("%s shows the delivery code: %s", name, raw)
		}
	}
	f, events := gatherFigures(t, r, platform.LocaleEN)
	raw, _ := json.Marshal(f)
	if strings.Contains(string(raw), deliveryCode) || strings.Contains(fmt.Sprint(events), deliveryCode) {
		t.Errorf("the figures show the delivery code: %s", raw)
	}
}
