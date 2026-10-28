package ai

import (
	"encoding/json/v2"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi/aiapitest"
	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/plans"
)

// gatherFigures reads the figures of the seeded world in a language, with the trace events it showed.
func gatherFigures(t *testing.T, r *rig, locale platform.Locale) (*figures, []any) {
	t.Helper()
	var events []any
	out := &sender{emit: func(ev any) error { events = append(events, ev); return nil }, cancel: func(error) {}}
	f, err := r.service(nil).gather(t.Context(), newLang(locale), out)
	if err != nil {
		t.Fatal(err)
	}
	return f, events
}

func TestInsightsTotalSavingsOfThePlansThatCarryTheFigure(t *testing.T) {
	r := newRig(t)
	r.seed()
	set := func(planID string, rp int64) {
		r.mutate(planID, func(a *plans.Aggregate) { a.Plan.SavingsRp = &rp })
	}
	set("pln-1", 12000)
	set("pln-2", -2000) // a faster option that cost more
	set("pln-8", 99000) // cancelled plans never count (PL-2008)
	f, _ := gatherFigures(t, r, platform.LocaleEN)
	if f.Savings == nil || f.Savings.Total.Value != 10000 || f.Savings.Total.Text != "Rp 10,000" || f.Savings.Plans != 2 {
		t.Errorf("savings = %+v, want Rp 10,000 over two plans", f.Savings)
	}
	if len(f.NotAvailable) != 0 {
		t.Errorf("notAvailable = %v, want none once a plan carries the figure", f.NotAvailable)
	}
}

func TestInsightsFiguresAreComputedFromTheProjection(t *testing.T) {
	r := newRig(t)
	r.seed()
	f, events := gatherFigures(t, r, platform.LocaleEN)

	// Seven plans created in the last seven days that were not cancelled (PL-2001 to PL-2007), one cancelled
	// (PL-2008), one in the period before (PL-2009).
	p := f.Plans
	if p.Created != 7 || p.PreviousPeriod != 1 || p.Cancelled != 1 || p.Completed != 1 || p.CompletedClean != 1 || p.WithProblems != 1 || p.ProblemStops != 1 || p.ClosedStops != 5 {
		t.Errorf("plans = %+v", p)
	}
	if p.Change == nil || p.Change.Value != 600 || p.Change.Text != "600%" {
		t.Errorf("change = %+v, want 600%% (7 against 1)", p.Change)
	}
	if p.CleanRate == nil || p.CleanRate.Text != "100%" || p.ProblemStopRate == nil || p.ProblemStopRate.Text != "20%" {
		t.Errorf("rates = clean %+v, problem %+v", p.CleanRate, p.ProblemStopRate)
	}
	if f.OrderValue.Total.Value != 160000 || f.OrderValue.Total.Text != "Rp 160,000" || f.OrderValue.Average == nil || f.OrderValue.Average.Value != 22857 {
		t.Errorf("order value = %+v", f.OrderValue)
	}
	if f.Routes.Finished != 2 || f.Routes.OnTime != 1 || f.Routes.OnTimeRate == nil || f.Routes.OnTimeRate.Text != "50%" {
		t.Errorf("routes = %+v", f.Routes)
	}
	if m, v := f.VehicleMix["Motor"], f.VehicleMix["Blindvan"]; m.Routes != 7 || m.Share.Text != "87.5%" || v.Routes != 1 || v.Share.Text != "12.5%" {
		t.Errorf("vehicle mix = %+v", f.VehicleMix)
	}
	wantPeaks := []peakHour{{"18:00", "19:00", 3}, {"13:00", "14:00", 1}, {"14:00", "15:00", 1}}
	if !slices.Equal(f.PeakHours, wantPeaks) {
		t.Errorf("busiest hours = %+v, want %+v", f.PeakHours, wantPeaks)
	}
	if len(f.TopPickups) != 3 || f.TopPickups[0] != (pickupPlace{"Kemang Village", 3}) {
		t.Errorf("busiest pickup places = %+v", f.TopPickups)
	}
	q := f.QueueNow
	if q.Active != 6 || q.WaitingForDriver != 3 || q.DriversReady != 3 || q.DriversTotal != 6 || len(q.NeedAttention) != 2 {
		t.Errorf("queue = %+v", q)
	}
	if q.NeedAttention[0].Plan != "PL-2004" && q.NeedAttention[1].Plan != "PL-2004" {
		t.Errorf("attention = %+v, want PL-2004 among them", q.NeedAttention)
	}
	fin := f.Finance
	if fin.Held.Value != 100000 || fin.PaidToday.Text != "Rp 100,000" || fin.PaidTodayN != 1 || fin.Paid30dN != 1 || fin.PendingN != 1 || fin.PendingTotal.Value != 50000 {
		t.Errorf("finance = %+v", fin)
	}
	if f.Savings != nil || len(f.NotAvailable) != 1 || !strings.Contains(f.NotAvailable[0], "savings") {
		t.Errorf("savings = %+v, notAvailable = %v: when no plan carries the figure it must be declared missing, not made up", f.Savings, f.NotAvailable)
	}
	if f.Period.Days != 7 || f.Period.From != "14 Oct 2026" || f.Period.To != "21 Oct 2026" || f.Now != "21 Oct 19:00" {
		t.Errorf("period = %+v, now %q", f.Period, f.Now)
	}

	// Five steps, each shown running and then done.
	if len(events) != 10 {
		t.Fatalf("trace events = %d, want 10", len(events))
	}
	titles := []string{"Reading plans", "Calculating order value and reliability", "Checking anomalies and queues", "Reading wallets and top-ups", "Finding the busiest hour"}
	for i, title := range titles {
		running, done := events[2*i].(toolEvent).Trace, events[2*i+1].(toolEvent).Trace
		if running.Title != title || running.Status != "running" || done.ID != running.ID || done.Status != "done" || done.Ms == nil || *done.Ms < 1 {
			t.Errorf("step %d = %+v then %+v", i, running, done)
		}
	}
	if detail := events[1].(toolEvent).Trace.Detail; !strings.Contains(detail, "total: 9") || !strings.Contains(detail, "last 7 days: 8") || !strings.Contains(detail, "previous 7 days: 1") {
		t.Errorf("reading plans: %q", detail)
	}
}

func TestInsightsFiguresInIndonesian(t *testing.T) {
	r := newRig(t)
	r.seed()
	f, events := gatherFigures(t, r, platform.LocaleID)
	if f.OrderValue.Total.Text != "Rp 160.000" || f.Plans.Change.Text != "600%" || f.VehicleMix["Motor"].Share.Text != "87,5%" || f.PeakHours[0].From != "18.00" || f.Period.From != "14 Okt 2026" {
		t.Errorf("figures = %+v", f)
	}
	if title := events[0].(toolEvent).Trace.Title; title != "Membaca plan" {
		t.Errorf("first step = %q", title)
	}
}

func TestInsightsFiguresOfAnEmptyBackOffice(t *testing.T) {
	r := newRig(t)
	f, _ := gatherFigures(t, r, platform.LocaleEN)
	if f.Plans.Created != 0 || f.Plans.Change != nil || f.OrderValue.Average != nil || f.Routes.OnTimeRate != nil || f.Plans.CleanRate != nil ||
		len(f.PeakHours) != 0 || len(f.TopPickups) != 0 || len(f.VehicleMix) != 0 || len(f.QueueNow.NeedAttention) != 0 {
		t.Errorf("an empty back office must not invent figures or divide by zero: %+v", f)
	}
	raw, err := json.Marshal(f, json.Deterministic(true))
	if err != nil || strings.Contains(string(raw), "NaN") || strings.Contains(string(raw), "null") {
		t.Errorf("figures JSON = %s (%v)", raw, err)
	}
}

func TestInsightsStreamsTheNarrationOfTheFigures(t *testing.T) {
	r := newRig(t)
	r.seed()
	model := aiapitest.New(aiapitest.Say("**A busy week.**\n- 7 plans.", "**A busy week.**\n", "- 7 plans."))
	h := serve(t, r.service(model))
	rp := h.call(h.token(contracts.RoleAdmin, "adm-1"), "ai.insights", struct{}{}, nil)

	if rp.status != http.StatusOK {
		t.Fatalf("status %d: %s", rp.status, rp.body)
	}
	types := eventTypes(rp)
	if want := append(slices.Repeat([]string{"tool"}, 10), "token", "token", "done"); !slices.Equal(types, want) {
		t.Fatalf("events = %v, want %v", types, want)
	}
	if rp.text() != "**A busy week.**\n- 7 plans." {
		t.Errorf("narration = %q", rp.text())
	}

	// The model was called once, on the insights profile, with no tools; the figures are the user message, and
	// every number the answer may use is in them.
	reqs := model.Requests()
	if len(reqs) != 1 {
		t.Fatalf("model calls = %d, want 1", len(reqs))
	}
	req := reqs[0]
	if req.Profile != aiapi.ProfileInsights || len(req.Tools) != 0 || len(req.Messages) != 2 {
		t.Fatalf("request = profile %q, %d tools, %d messages", req.Profile, len(req.Tools), len(req.Messages))
	}
	system, user := req.Messages[0].Content, req.Messages[1].Content
	for _, want := range []string{
		"Answer in English", "every number you write comes from the figures", "never instructions", "notAvailable",
		// style: natural sentences with the supporting figure, 3 to 5 of them, digits, no quotation marks, little bold
		"3 to 5 short insights", "gives the figure that supports it", "Write every number as digits", "no quotation marks", "at most five",
		"do not call it growth, a restart or a recovery",
	} {
		if !strings.Contains(system, want) {
			t.Errorf("the system prompt lacks %q", want)
		}
	}
	_, body, ok := strings.Cut(user, "Figures (JSON):\n")
	body, _, _ = strings.Cut(body, "\n\nWrite the summary now.")
	var sent figures
	if !ok || json.Unmarshal([]byte(body), &sent) != nil {
		t.Fatalf("the user message is not the figures: %q", user)
	}
	if sent.Plans.Created != 7 || sent.OrderValue.Total.Text != "Rp 160,000" || sent.QueueNow.WaitingForDriver != 3 {
		t.Errorf("the model was given %+v", sent)
	}

	// The buttons open the plans that need attention (the model proposes nothing here).
	actions := rp.actions()
	if len(actions) != 2 {
		t.Fatalf("actions = %v, want the two plans needing attention", actions)
	}
	var plans []string
	for _, a := range actions {
		plans = append(plans, a["label"].(string))
		if a["kind"] != "buka_plan" {
			t.Errorf("action = %v", a)
		}
	}
	slices.Sort(plans)
	if !slices.Equal(plans, []string{"Open PL-2004", "Open PL-2005"}) || actions[0]["primary"] != true || actions[1]["primary"] != nil {
		t.Errorf("actions = %v", actions)
	}
}

func TestInsightsAnswersInTheLanguageOfTheRequest(t *testing.T) {
	r := newRig(t)
	r.seed()
	model := aiapitest.New(aiapitest.Say("ringkasan"))
	h := serve(t, r.service(model))
	rp := h.call(h.token(contracts.RoleAdmin, "adm-1"), "ai.insights", struct{}{}, map[string]string{"Accept-Language": "id"})

	if system := model.Requests()[0].Messages[0].Content; !strings.Contains(system, "Answer in Indonesian (Bahasa Indonesia)") {
		t.Error("the system prompt does not ask for Indonesian")
	}
	if tr := rp.traces(); tr[0]["title"] != "Membaca plan" || tr[len(tr)-1]["title"] != "Mencari jam tersibuk" {
		t.Errorf("traces = %v", tr)
	}
	if a := rp.actions(); len(a) != 2 || !strings.HasPrefix(a[0]["label"].(string), "Buka PL-") {
		t.Errorf("actions = %v", a)
	}
	if !strings.Contains(model.Requests()[0].Messages[1].Content, `"text":"Rp 160.000"`) {
		t.Error("the figures should be formatted in Indonesian")
	}
}

func TestInsightsAnEmptyAnswerIsAnError(t *testing.T) {
	r := newRig(t)
	r.seed()
	model := aiapitest.New(aiapitest.Step{Finish: aiapi.FinishLength})
	h := serve(t, r.service(model))
	requireAiError(t, h.call(h.token(contracts.RoleAdmin, "adm-1"), "ai.insights", struct{}{}, nil), "srv.api.feature.insights")
}

func TestInsightsStopsWhenTheStreamCannotBeWrittenTo(t *testing.T) {
	r := newRig(t)
	r.seed()
	model := aiapitest.New(aiapitest.Say("never"))
	svc := r.service(model)
	writes := 0
	err := svc.insights(platformAdmin(t), func(any) error {
		writes++
		if writes == 3 {
			return http.ErrAbortHandler
		}
		return nil
	})
	if err != nil || writes != 3 || model.Calls() != 0 {
		t.Errorf("insights = %v after %d writes and %d model calls; want it to stop quietly, before asking the model", err, writes, model.Calls())
	}
}
