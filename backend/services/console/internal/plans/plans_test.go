package plans

import (
	"encoding/json/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/optimizerapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/testkit"
)

func TestCreateDraftStartsAnEmptyPlanWithTheNextCode(t *testing.T) {
	w := newWorld(t)
	first, err := w.svc.CreateDraft(t.Context(), w.customer)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := w.svc.CreateDraft(t.Context(), w.customer)

	if first.Code != "PL-2001" || second.Code != "PL-2002" {
		t.Errorf("codes %s, %s; want PL-2001, PL-2002", first.Code, second.Code)
	}
	if !strings.HasPrefix(first.ID, "pln-") || first.UserID != w.customer || first.Status != contracts.PlanStatusDraft || first.DraftStep != 1 {
		t.Errorf("draft = %+v", first)
	}
	if first.Schedule.Mode != contracts.ScheduleModeNow || first.Priority.Preset != contracts.PresetBalanced {
		t.Errorf("defaults: schedule %+v priority %+v", first.Schedule, first.Priority)
	}
	if len(first.Events) != 1 || first.Events[0].Text.Key != "srv.api.event.draftCreated" {
		t.Errorf("events = %+v", first.Events)
	}
	if got := w.stored(first.ID); got.Plan.Code != "PL-2001" || got.Version != 0 {
		t.Errorf("stored = %+v", got)
	}
}

func TestCustomersCannotReachEachOthersPlans(t *testing.T) {
	w := newWorld(t)
	mine := w.ready()
	intruder := testkit.Customer(t, w.pool, "Raka")
	ctx := t.Context()

	calls := map[string]func() error{
		"get": func() error { _, err := w.svc.Get(ctx, intruder, mine.ID); return err },
		"save": func() error {
			_, err := w.svc.SaveDraft(ctx, intruder, mine.ID, DraftPatch{DraftStep: new(2)})
			return err
		},
		"compute": func() error {
			return w.svc.Compute(ctx, intruder, mine.ID, "", func(ComputeEvent) error { return nil })
		},
		"choose":  func() error { _, err := w.svc.ChooseOption(ctx, intruder, mine.ID, mine.SelectedOptionID); return err },
		"confirm": func() error { _, err := w.svc.Confirm(ctx, intruder, mine.ID, contracts.PaymentWallet); return err },
		"remove":  func() error { return w.svc.Remove(ctx, intruder, mine.ID) },
	}
	testkit.Fund(t, w.pool, intruder, 1_000_000)
	for name, call := range calls {
		if got := apiError(t, call()); got.Code != platform.CodeDenied || got.Status != 403 || got.Msg.Key != "srv.api.planNoAccess" {
			t.Errorf("%s: got %+v, want 403 ditolak planNoAccess", name, got)
		}
	}
	if list, err := w.svc.List(ctx, intruder, Filter{}); err != nil || len(list) != 0 {
		t.Errorf("the other customer lists %+v, %v", list, err)
	}
	if got := w.stored(mine.ID); got.Plan.Status != contracts.PlanStatusOptionsReady || got.Version != 0 || got.Plan.DraftStep != mine.DraftStep {
		t.Errorf("the plan changed under the intruder: %+v", got.Plan.Status)
	}
	if _, err := w.svc.Get(ctx, w.customer, "pln-missing"); apiError(t, err).Status != 404 {
		t.Errorf("unknown plan: %v", err)
	}
}

func TestSaveDraftChangesOnlyWhatWasSent(t *testing.T) {
	w := newWorld(t)
	p := w.draft("s1")
	window := &contracts.TimeWindow{From: contracts.At(w.clock.at.Add(time.Hour)), To: contracts.At(w.clock.at.Add(2 * time.Hour))}
	p, err := w.svc.SaveDraft(t.Context(), w.customer, p.ID, DraftPatch{
		Schedule: &contracts.Schedule{Mode: contracts.ScheduleModeScheduled, Window: window}, DraftStep: new(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Shipments) != 1 || p.Schedule.Mode != contracts.ScheduleModeScheduled || p.DraftStep != 2 || p.Priority.Preset != contracts.PresetBalanced {
		t.Errorf("plan = %+v", p)
	}
	// An empty list is a change too: the shipper removed everything.
	p, err = w.svc.SaveDraft(t.Context(), w.customer, p.ID, DraftPatch{Shipments: []contracts.Shipment{}})
	if err != nil || len(p.Shipments) != 0 {
		t.Errorf("after clearing: %+v, %v", p.Shipments, err)
	}
}

func TestSaveDraftRejectsValuesTheWizardCannotProduce(t *testing.T) {
	w := newWorld(t)
	p := w.draft()
	for name, patch := range map[string]DraftPatch{
		"schedule":  {Schedule: &contracts.Schedule{Mode: "kapan-saja"}},
		"priority":  {Priority: &contracts.Priority{Preset: "murah"}},
		"draftStep": {DraftStep: new(9)},
	} {
		_, err := w.svc.SaveDraft(t.Context(), w.customer, p.ID, patch)
		if got := apiError(t, err); got.Code != platform.CodeInvalid || got.Field != name {
			t.Errorf("%s: got %+v", name, got)
		}
	}
}

func TestComputeRelaysTheStagesAndStoresTheOptions(t *testing.T) {
	w := newWorld(t)
	p := w.draft("s1", "s2")
	events, err := w.compute(p.ID)
	if err != nil {
		t.Fatal(err)
	}

	wantLabels := []string{
		"Reading 4 locations",
		"Calculating distances and travel times between points",
		"Building vehicle combinations for 2 shipments",
		"Comparing cost, distance, and time",
	}
	if len(events) != 5 {
		t.Fatalf("%d events, want 4 progress and done: %+v", len(events), events)
	}
	for i, label := range wantLabels {
		if got := events[i]; got.Type != "progress" || got.Step != i+1 || got.Total != 4 || got.Label != label || got.OptionSet != nil {
			t.Errorf("event %d = %+v, want progress %d/4 %q", i, got, i+1, label)
		}
	}
	done := events[4]
	if done.Type != "done" || done.OptionSet == nil || len(done.OptionSet.Options) != 1 {
		t.Fatalf("last event = %+v", done)
	}

	rec := w.stored(p.ID)
	if rec.Plan.Status != contracts.PlanStatusOptionsReady || rec.Plan.OptionSet == nil || rec.Plan.OptionSet.Options[0].ID != done.OptionSet.Options[0].ID {
		t.Errorf("stored plan = %s, options %+v", rec.Plan.Status, rec.Plan.OptionSet)
	}
	if rec.Plan.DraftStep != 3 || rec.Plan.SelectedOptionID != "" {
		t.Errorf("draftStep %d selected %q, want step 3 and nothing chosen", rec.Plan.DraftStep, rec.Plan.SelectedOptionID)
	}
	if rec.DraftInputHash == "" || rec.DraftInputHash != inputHash(rec.Plan) {
		t.Errorf("stored hash %q is not the hash of the draft", rec.DraftInputHash)
	}
	if rec.Quote == nil || rec.Quote.Engine != "fake-1" || !rec.Quote.Proven || rec.Quote.RoadSource != "estimate" {
		t.Errorf("quote meta = %+v", rec.Quote)
	}

	reqs := w.optimizer.Requests()
	if len(reqs) != 1 {
		t.Fatalf("%d quotes asked, want 1", len(reqs))
	}
	req := reqs[0]
	if req.PlanID != p.ID || len(req.Shipments) != 2 || len(req.Vehicles) != 1 || req.Vehicles[0].ID != "veh-aaa" || req.Vehicles[0].Fare.Base != 6000 {
		t.Errorf("request = plan %s, %d shipments, vehicles %+v", req.PlanID, len(req.Shipments), req.Vehicles)
	}
	if req.Schedule.Mode != contracts.ScheduleModeNow || req.Priority.Preset != contracts.PresetBalanced || !req.Now.Equal(w.clock.at) {
		t.Errorf("request = schedule %+v priority %+v now %v", req.Schedule, req.Priority, req.Now)
	}
}

func TestStageLabelsAgreeInNumber(t *testing.T) {
	if got := stageLabel(1, 1); got != "Reading 2 locations" {
		t.Errorf("stage 1 for one shipment = %q", got)
	}
	if got := stageLabel(3, 1); got != "Building vehicle combinations for 1 shipment" {
		t.Errorf("stage 3 for one shipment = %q", got)
	}
	if got := stageLabel(9, 1); got == "" {
		t.Error("a stage the console does not know has no label")
	}
}

func TestEditingWhatDecidesTheOptionsDropsThemButEditingAContactDoesNot(t *testing.T) {
	w := newWorld(t)
	p := w.ready("s1")
	hash := w.stored(p.ID).DraftInputHash

	contact := slices.Clone(p.Shipments)
	contact[0].Dropoff.ContactName, contact[0].Dropoff.Note = "Someone else", "Ring twice"
	p, err := w.svc.SaveDraft(t.Context(), w.customer, p.ID, DraftPatch{Shipments: contact, DraftStep: new(4)})
	if err != nil {
		t.Fatal(err)
	}
	if p.OptionSet == nil || p.SelectedOptionID == "" || p.Status != contracts.PlanStatusOptionsReady {
		t.Fatalf("a contact edit threw the options away: %s, %+v", p.Status, p.OptionSet)
	}

	heavier := slices.Clone(p.Shipments)
	heavier[0].Items = slices.Clone(heavier[0].Items)
	heavier[0].Items[0].WeightKg = 40
	p, err = w.svc.SaveDraft(t.Context(), w.customer, p.ID, DraftPatch{Shipments: heavier})
	if err != nil {
		t.Fatal(err)
	}
	if p.OptionSet != nil || p.SelectedOptionID != "" || p.Status != contracts.PlanStatusDraft {
		t.Errorf("after a heavier item: %s, options %v, chosen %q; want a plain draft", p.Status, p.OptionSet != nil, p.SelectedOptionID)
	}
	if rec := w.stored(p.ID); rec.DraftInputHash != "" || rec.Quote != nil || hash == "" {
		t.Errorf("stored hash %q quote %+v should be cleared with the options", rec.DraftInputHash, rec.Quote)
	}
}

func TestComputeNeedsAnActiveVehicle(t *testing.T) {
	w := newWorld(t)
	if _, err := w.pool.Exec(t.Context(), `UPDATE fleet_vehicles SET status = 'servis'`); err != nil {
		t.Fatal(err)
	}
	p := w.draft("s1")
	events, err := w.compute(p.ID)
	wantError(t, err, platform.CodeService, "srv.engine.noVehicles")
	if len(events) != 0 || len(w.optimizer.Requests()) != 0 {
		t.Errorf("%d events streamed and %d quotes asked; an impossible request should stop before both", len(events), len(w.optimizer.Requests()))
	}
}

func TestComputeAnswersWhatTheOptimizerRefused(t *testing.T) {
	w := newWorld(t)
	p := w.draft("s1")

	w.optimizer.FailWith(&optimizerapi.Error{Code: "tidak_valid", Field: "shipments.0.items", Msg: contracts.Msg{Key: "srv.engine.itemsMissing", Params: map[string]any{"n": 1}}})
	events, err := w.compute(p.ID)
	got := wantError(t, err, platform.CodeInvalid, "srv.engine.itemsMissing")
	if got.Field != "shipments.0.items" || got.Status != 422 || got.Msg.Params["n"] != float64(1) {
		t.Errorf("got field %q status %d params %v", got.Field, got.Status, got.Msg.Params)
	}
	if len(events) != 4 {
		t.Errorf("%d events before the error, want the 4 progress lines", len(events))
	}

	w.optimizer.FailWith(&optimizerapi.Error{Code: "layanan", Msg: contracts.Msg{Key: "srv.engine.unavailable"}})
	_, err = w.compute(p.ID)
	wantError(t, err, platform.CodeService, "srv.engine.unavailable")

	if rec := w.stored(p.ID); rec.Plan.OptionSet != nil || rec.Plan.Status != contracts.PlanStatusDraft {
		t.Errorf("a failed quote changed the plan: %s", rec.Plan.Status)
	}
}

func TestComputeSurvivesAnUnreachableOptimizer(t *testing.T) {
	w := newWorld(t)
	w.svc.optimizer = &optimizerapi.Client{BaseURL: "http://127.0.0.1:1"} // nothing listens there
	p := w.draft("s1")
	_, err := w.compute(p.ID)
	got := wantError(t, err, platform.CodeService, "srv.engine.unavailable")
	if got.Unwrap() == nil {
		t.Error("the cause is not kept for the logs")
	}
}

func TestComputeThrowsAwayAResultTheDraftHasOutgrown(t *testing.T) {
	w := newWorld(t)
	p := w.draft("s1")
	heavier := slices.Clone(p.Shipments)
	heavier[0].Items = []contracts.Item{{ID: "i", Name: "Anvil", Qty: 1, WeightKg: 40, LengthCm: 30, WidthCm: 20, HeightCm: 10}}
	w.optimizer.BeforeResult(func() { // the shipper edits the draft while the optimizer works
		if _, err := w.svc.SaveDraft(t.Context(), w.customer, p.ID, DraftPatch{Shipments: heavier}); err != nil {
			t.Error(err)
		}
	})

	_, err := w.compute(p.ID)
	wantError(t, err, platform.CodeConflict, "srv.api.optionsStale")
	if rec := w.stored(p.ID); rec.Plan.OptionSet != nil || rec.Plan.Status != contracts.PlanStatusDraft || rec.Plan.Shipments[0].Items[0].WeightKg != 40 {
		t.Errorf("stored = %s, options %v; the stale options must not be kept and the edit must stay", rec.Plan.Status, rec.Plan.OptionSet != nil)
	}
}

func TestComputeStopsWhenTheClientHasGone(t *testing.T) {
	w := newWorld(t)
	p := w.draft("s1")
	gone := platform.Unavailable(contracts.Msg{Key: "client left"})
	err := w.svc.Compute(t.Context(), w.customer, p.ID, "", func(ComputeEvent) error { return gone })
	if err != gone {
		t.Errorf("Compute = %v, want the error emit returned", err)
	}
	if rec := w.stored(p.ID); rec.Plan.OptionSet != nil {
		t.Error("options were stored for a client that left")
	}
}

func TestChooseOptionTakesOnlyComputedOptions(t *testing.T) {
	w := newWorld(t)
	p := w.draft("s1")
	_, err := w.svc.ChooseOption(t.Context(), w.customer, p.ID, "anything")
	if got := wantError(t, err, platform.CodeInvalid, "srv.api.optionNotFound"); got.Field != "optionId" {
		t.Errorf("field = %q", got.Field)
	}

	p = w.ready("s1")
	if p.SelectedOptionID != p.ID+":termurah" {
		t.Errorf("selected = %q", p.SelectedOptionID)
	}
	if _, err := w.svc.ChooseOption(t.Context(), w.customer, p.ID, "pln-other:termurah"); apiError(t, err).Msg.Key != "srv.api.optionNotFound" {
		t.Errorf("an option of another plan was accepted: %v", err)
	}
}

func TestConfirmPaysAndHandsThePlanOver(t *testing.T) {
	w := newWorld(t)
	testkit.Fund(t, w.pool, w.customer, 100_000)
	p := w.ready("s1", "s2")
	w.clock.advance(5 * time.Minute)
	before := len(testkit.OutboxTypes(t, w.pool))

	got, err := w.svc.Confirm(t.Context(), w.customer, p.ID, contracts.PaymentWallet)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != contracts.PlanStatusAwaitingDriver || got.PaymentMethod != contracts.PaymentWallet || got.TotalCostRp != 25_000 || got.DraftStep != 4 {
		t.Errorf("plan = %s %s %d step %d", got.Status, got.PaymentMethod, got.TotalCostRp, got.DraftStep)
	}
	if !got.ConfirmedAt.Equal(w.clock.at) || got.OptionSet == nil || got.SelectedOptionID != p.SelectedOptionID {
		t.Errorf("confirmedAt %v, options kept %v, selected %q", got.ConfirmedAt, got.OptionSet != nil, got.SelectedOptionID)
	}

	if len(got.Routes) != 1 || got.Routes[0].ID != p.ID+":r1" || got.Routes[0].PlanID != p.ID || got.Routes[0].Status != contracts.RouteStatusAwaitingDriver {
		t.Fatalf("routes = %+v", got.Routes)
	}
	for i, s := range got.Routes[0].Stops {
		if want := got.Routes[0].ID + ":s" + string(rune('1'+i)); s.ID != want || s.Status != contracts.StopStatusWaiting {
			t.Errorf("stop %d = %s %s, want %s waiting", i, s.ID, s.Status, want)
		}
		if hasCode := len(s.DeliveryCode) == 4; hasCode != (s.Kind == contracts.StopKindDropoff) {
			t.Errorf("stop %s (%s) has delivery code %q: only drop-offs get one", s.ID, s.Kind, s.DeliveryCode)
		}
	}
	if n := len(got.Events); n != 2 || got.Events[1].Text.Key != "srv.api.event.confirmed" || got.Events[1].Text.Params["amount"] != int64(25_000) {
		t.Errorf("events = %+v", got.Events)
	}
	if opt, ok := got.Events[1].Text.Params["option"].(contracts.Msg); !ok || opt.Key != "srv.engine.label.best" {
		t.Errorf("the event names the option as %+v", got.Events[1].Text.Params["option"])
	}

	rec := w.stored(p.ID)
	if rec.Version != 1 || rec.Plan.Status != contracts.PlanStatusAwaitingDriver {
		t.Errorf("stored version %d status %s", rec.Version, rec.Plan.Status)
	}
	if w.balance() != 75_000 {
		t.Errorf("balance = %d, want 75000", w.balance())
	}

	// Two events, both from this transaction: the charge, then the plan handed over, carrying version 1.
	if after := testkit.OutboxTypes(t, w.pool); !slices.Equal(after[before:], []string{contracts.TypeWalletCharged, contracts.TypePlanConfirmed}) {
		t.Fatalf("published %v", after[before:])
	}
	env := testkit.Outbox(t, w.pool, contracts.TypePlanConfirmed)[0]
	if env.Key != p.ID || env.AggregateVersion != 1 {
		t.Errorf("plan.confirmed key %s version %d", env.Key, env.AggregateVersion)
	}
	ev, err := env.Payload[contracts.PlanConfirmed]()
	if err != nil {
		t.Fatal(err)
	}
	if ev.Plan.OptionSet != nil || ev.Plan.Status != contracts.PlanStatusAwaitingDriver || len(ev.Plan.Routes) != 1 || ev.Plan.UserID != w.customer {
		t.Errorf("snapshot = status %s, options %v, %d routes", ev.Plan.Status, ev.Plan.OptionSet != nil, len(ev.Plan.Routes))
	}
	// the alternatives are not shared, but what the chosen one saves is, so the back office can total it
	var chosen contracts.PlanOption
	for _, o := range p.OptionSet.Options {
		if o.ID == p.SelectedOptionID {
			chosen = o
		}
	}
	if ev.Plan.SavingsRp == nil || ev.Plan.SavingsPct == nil || *ev.Plan.SavingsRp != chosen.Savings.CostRp || *ev.Plan.SavingsPct != chosen.Savings.Pct {
		t.Errorf("shared savings = %v / %v, want the chosen option's %+v", ev.Plan.SavingsRp, ev.Plan.SavingsPct, chosen.Savings)
	}
	if ev.Payment.Method != contracts.PaymentWallet || ev.Payment.AmountRp != 25_000 || !ev.Payment.PaidAt.Equal(w.clock.at) {
		t.Errorf("payment = %+v", ev.Payment)
	}
	charged, _ := testkit.Outbox(t, w.pool, contracts.TypeWalletCharged)[0].Payload[contracts.WalletCharged]()
	if charged.PlanID != p.ID || charged.AmountRp != 25_000 || charged.BalanceAfterRp != 75_000 {
		t.Errorf("wallet.charged = %+v", charged)
	}
}

func TestConfirmWithTooLittleBalanceChangesNothingAndPublishesNothing(t *testing.T) {
	w := newWorld(t)
	testkit.Fund(t, w.pool, w.customer, 10_000) // the option costs 25 000
	p := w.ready("s1")
	eventsBefore := testkit.OutboxTypes(t, w.pool)
	entriesBefore := testkit.Count(t, w.pool, `SELECT count(*) FROM wallet_entries`)
	recBefore := w.stored(p.ID)

	_, err := w.svc.Confirm(t.Context(), w.customer, p.ID, contracts.PaymentWallet)
	got := wantError(t, err, platform.CodeInvalid, "srv.ops.err.insufficientBalanceDetail")
	if got.Field != "paymentMethod" || got.Msg.Params["balance"] != int64(10_000) || got.Msg.Params["cost"] != int64(25_000) {
		t.Errorf("field %q params %v", got.Field, got.Msg.Params)
	}

	rec := w.stored(p.ID)
	if rec.Plan.Status != contracts.PlanStatusOptionsReady || rec.Version != 0 || len(rec.Plan.Routes) != 0 || rec.Plan.PaymentMethod != "" ||
		rec.Plan.TotalCostRp != 0 || !rec.Plan.ConfirmedAt.IsZero() || len(rec.Plan.Events) != len(recBefore.Plan.Events) || !rec.Plan.UpdatedAt.Equal(recBefore.Plan.UpdatedAt.Time) {
		t.Errorf("the plan changed: %+v", rec)
	}
	if w.balance() != 10_000 || testkit.Count(t, w.pool, `SELECT count(*) FROM wallet_entries`) != entriesBefore {
		t.Errorf("the wallet changed: balance %d", w.balance())
	}
	if after := testkit.OutboxTypes(t, w.pool); !slices.Equal(after, eventsBefore) {
		t.Errorf("events published %v -> %v", eventsBefore, after)
	}

	// Topping up makes the very same plan confirmable.
	testkit.Fund(t, w.pool, w.customer, 20_000)
	if _, err := w.svc.Confirm(t.Context(), w.customer, p.ID, contracts.PaymentWallet); err != nil {
		t.Errorf("confirm after topping up: %v", err)
	}
}

func TestAStaleDraftCannotBeConfirmed(t *testing.T) {
	w := newWorld(t)
	testkit.Fund(t, w.pool, w.customer, 100_000)
	p := w.ready("s1")
	eventsBefore := testkit.OutboxTypes(t, w.pool)

	// Something changed the draft without clearing its options (SaveDraft would have): confirm must still notice.
	_, err := w.pool.Exec(t.Context(),
		`UPDATE plans SET snapshot = jsonb_set(snapshot, '{shipments,0,items,0,weightKg}', '99') WHERE id = $1`, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.svc.Confirm(t.Context(), w.customer, p.ID, contracts.PaymentWallet)
	wantError(t, err, platform.CodeConflict, "srv.api.optionsStale")
	if w.balance() != 100_000 || !slices.Equal(testkit.OutboxTypes(t, w.pool), eventsBefore) {
		t.Errorf("a refused confirm still charged (balance %d) or published", w.balance())
	}
	if rec := w.stored(p.ID); rec.Plan.Status != contracts.PlanStatusOptionsReady || rec.Version != 0 {
		t.Errorf("stored = %s v%d", rec.Plan.Status, rec.Version)
	}
}

func TestConfirmingTwiceChargesOnce(t *testing.T) {
	w := newWorld(t)
	testkit.Fund(t, w.pool, w.customer, 100_000)
	p := w.ready("s1")
	if _, err := w.svc.Confirm(t.Context(), w.customer, p.ID, contracts.PaymentWallet); err != nil {
		t.Fatal(err)
	}
	_, err := w.svc.Confirm(t.Context(), w.customer, p.ID, contracts.PaymentWallet)
	wantError(t, err, platform.CodeConflict, "srv.api.planNotReady")

	if w.balance() != 75_000 {
		t.Errorf("balance = %d, want one charge of 25000", w.balance())
	}
	if n := testkit.Count(t, w.pool, `SELECT count(*) FROM wallet_entries WHERE kind = 'pembayaran' AND reference = $1`, p.ID); n != 1 {
		t.Errorf("%d payment entries for the plan, want 1", n)
	}
	if n := len(testkit.Outbox(t, w.pool, contracts.TypePlanConfirmed)); n != 1 {
		t.Errorf("%d plan.confirmed events, want 1", n)
	}
	if rec := w.stored(p.ID); rec.Version != 1 {
		t.Errorf("version = %d, want 1", rec.Version)
	}
}

func TestConfirmNeedsAChosenOptionAndTheWallet(t *testing.T) {
	w := newWorld(t)
	testkit.Fund(t, w.pool, w.customer, 100_000)
	p := w.draft("s1")
	if _, err := w.svc.Confirm(t.Context(), w.customer, p.ID, contracts.PaymentWallet); apiError(t, err).Msg.Key != "srv.api.planNotReady" {
		t.Errorf("a draft without options: %v", err)
	}

	events, err := w.compute(p.ID)
	if err != nil || events[len(events)-1].OptionSet == nil {
		t.Fatal(err)
	}
	_, err = w.svc.Confirm(t.Context(), w.customer, p.ID, contracts.PaymentWallet)
	if got := wantError(t, err, platform.CodeInvalid, "srv.api.pickOption"); got.Field != "optionId" {
		t.Errorf("field = %q", got.Field)
	}

	for _, method := range []contracts.PaymentMethod{contracts.PaymentTransfer, contracts.PaymentCash, ""} {
		_, err := w.svc.Confirm(t.Context(), w.customer, p.ID, method)
		if got := wantError(t, err, platform.CodeInvalid, "srv.ops.err.chooseMethod"); got.Field != "paymentMethod" {
			t.Errorf("%q: field = %q", method, got.Field)
		}
	}
	if w.balance() != 100_000 {
		t.Errorf("balance = %d: nothing should have been charged", w.balance())
	}
}

func TestOnlyDraftsCanBeEditedOrRemoved(t *testing.T) {
	w := newWorld(t)
	testkit.Fund(t, w.pool, w.customer, 100_000)
	p := w.ready("s1")
	if _, err := w.svc.Confirm(t.Context(), w.customer, p.ID, contracts.PaymentWallet); err != nil {
		t.Fatal(err)
	}
	_, err := w.svc.SaveDraft(t.Context(), w.customer, p.ID, DraftPatch{DraftStep: new(1)})
	wantError(t, err, platform.CodeConflict, "srv.api.planLocked")
	_, err = w.svc.ChooseOption(t.Context(), w.customer, p.ID, p.SelectedOptionID)
	wantError(t, err, platform.CodeConflict, "srv.api.planLocked")
	wantError(t, w.svc.Remove(t.Context(), w.customer, p.ID), platform.CodeConflict, "srv.api.draftOnlyDelete")
	_, err = w.compute(p.ID)
	wantError(t, err, platform.CodeConflict, "srv.api.planAlreadyConfirmed")

	draft := w.draft("s9")
	if err := w.svc.Remove(t.Context(), w.customer, draft.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.Get(t.Context(), w.customer, draft.ID); apiError(t, err).Status != 404 {
		t.Errorf("a removed draft is still there: %v", err)
	}
}

func TestListShowsTheCustomersPlansNewestFirstAndFilters(t *testing.T) {
	w := newWorld(t)
	testkit.Fund(t, w.pool, w.customer, 100_000)
	other := testkit.Customer(t, w.pool, "Raka")
	if _, err := w.svc.CreateDraft(t.Context(), other); err != nil {
		t.Fatal(err)
	}

	empty := w.draft() // PL-2002 (the other customer's draft took 2001)
	w.clock.advance(time.Minute)
	confirmed := w.ready("s1", "s2")
	if _, err := w.svc.Confirm(t.Context(), w.customer, confirmed.ID, contracts.PaymentWallet); err != nil {
		t.Fatal(err)
	}
	w.clock.advance(time.Minute)
	options := w.ready("s3")

	all, err := w.svc.List(t.Context(), w.customer, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, s := range all {
		order = append(order, s.ID)
	}
	if want := []string{options.ID, confirmed.ID, empty.ID}; !slices.Equal(order, want) {
		t.Fatalf("order = %v, want newest first %v (and not the other customer's)", order, want)
	}
	byID := map[string]Summary{}
	for _, s := range all {
		byID[s.ID] = s
	}
	if s := byID[confirmed.ID]; s.Status != contracts.PlanStatusAwaitingDriver || s.RoutesWithoutDriver != 1 || s.Shipments != 2 || s.Stops != 4 || s.TotalCostRp == nil || *s.TotalCostRp != 25_000 || s.CustomerName != "Dina" {
		t.Errorf("confirmed summary = %+v", s)
	}

	count := func(f Filter) int {
		t.Helper()
		got, err := w.svc.List(t.Context(), w.customer, f)
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}
	cases := map[string]struct {
		filter Filter
		want   int
	}{
		"status":         {Filter{Status: []contracts.PlanStatus{contracts.PlanStatusDraft, contracts.PlanStatusAwaitingDriver}}, 2},
		"needs driver":   {Filter{NeedsDriver: true}, 1},
		"motorbike":      {Filter{VehicleType: contracts.VehicleTypeMotor}, 2},
		"blind van":      {Filter{VehicleType: contracts.VehicleTypeBlindvan}, 0},
		"by code":        {Filter{Q: " pl-" + strings.TrimPrefix(byID[confirmed.ID].Code, "PL-")}, 1},
		"by place":       {Filter{Q: "senayan s3"}, 1},
		"created from":   {Filter{From: contracts.At(w.clock.at.Add(-time.Hour))}, 3},
		"created to":     {Filter{To: contracts.At(w.clock.at.Add(-time.Hour))}, 0},
		"own id":         {Filter{UserID: w.customer}, 3},
		"someone else's": {Filter{UserID: other}, 0},
	}
	for name, c := range cases {
		if got := count(c.filter); got != c.want {
			t.Errorf("%s: %d plans, want %d", name, got, c.want)
		}
	}
}

// The wizard sends only what changed. Clearing the shipments is a change (`[]`), leaving them out is not.
func TestDraftPatchTellsAnEmptyListFromAnAbsentOne(t *testing.T) {
	decode := func(body string) DraftPatch {
		t.Helper()
		var p DraftPatch
		if err := json.Unmarshal([]byte(body), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if p := decode(`{"shipments":[]}`); p.Shipments == nil || len(p.Shipments) != 0 {
		t.Errorf("an empty list decoded as %#v", p.Shipments)
	}
	for _, body := range []string{`{}`, `{"shipments":null}`, `{"draftStep":2}`} {
		if p := decode(body); p.Shipments != nil {
			t.Errorf("%s: decoded as %#v, want the list left alone", body, p.Shipments)
		}
	}
	if p := decode(`{"schedule":{"mode":"sekarang"},"draftStep":3}`); p.Schedule == nil || p.DraftStep == nil || *p.DraftStep != 3 || p.Priority != nil {
		t.Errorf("patch = %+v", p)
	}
}
