package plans

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/testkit"
)

func (w *world) report(text string) contracts.Report {
	return contracts.Report{Text: text, Locale: "en", At: contracts.At(w.clock.at)}
}

func TestStoreReportKeepsTheReportWithItsOptionSetAndChangesNothingElse(t *testing.T) {
	w := newWorld(t)
	p := w.ready("s1", "s2")
	before := w.stored(p.ID)
	w.clock.advance(time.Hour)

	stored, err := StoreReport(t.Context(), w.pool, w.customer, p.ID, RefOf(before.Plan.OptionSet), w.report("Take the cheapest."))
	if err != nil || !stored {
		t.Fatalf("StoreReport = %v, %v", stored, err)
	}
	after := w.stored(p.ID)
	if r := after.Plan.OptionSet.Report; r == nil || r.Text != "Take the cheapest." || r.Locale != "en" || !r.At.Equal(w.clock.at) {
		t.Fatalf("report = %+v", r)
	}
	// Only the report changed: not the version, the time of the last change, the status or the choice.
	if after.Version != before.Version || !after.Plan.UpdatedAt.Equal(before.Plan.UpdatedAt.Time) || after.Plan.Status != before.Plan.Status ||
		after.Plan.SelectedOptionID != before.Plan.SelectedOptionID || after.DraftInputHash != before.DraftInputHash {
		t.Errorf("version %d→%d, updatedAt %v→%v, status %s→%s, selected %q→%q", before.Version, after.Version,
			before.Plan.UpdatedAt, after.Plan.UpdatedAt, before.Plan.Status, after.Plan.Status, before.Plan.SelectedOptionID, after.Plan.SelectedOptionID)
	}
	if !slices.Equal(optionIDs(after.Plan), optionIDs(before.Plan)) {
		t.Error("the options changed")
	}
	got, err := w.svc.Get(t.Context(), w.customer, p.ID)
	if err != nil || got.OptionSet.Report == nil || got.OptionSet.Report.Text != "Take the cheapest." {
		t.Errorf("plans.get = %+v, %v", got.OptionSet, err)
	}

	// Explaining again replaces the report.
	if stored, err := StoreReport(t.Context(), w.pool, w.customer, p.ID, RefOf(after.Plan.OptionSet), w.report("A second opinion.")); err != nil || !stored {
		t.Fatalf("second StoreReport = %v, %v", stored, err)
	}
	if r := w.stored(p.ID).Plan.OptionSet.Report; r == nil || r.Text != "A second opinion." {
		t.Errorf("report = %+v", r)
	}
}

func optionIDs(p contracts.Plan) []string {
	var ids []string
	for _, o := range p.OptionSet.Options {
		ids = append(ids, o.ID)
	}
	return ids
}

// A report is slow to write. If the options were computed again, or dropped by an edit, while it was being written,
// it describes options that are gone and is not kept.
func TestStoreReportRefusesAnOptionSetThatIsNoLongerTheOneExplained(t *testing.T) {
	w := newWorld(t)
	p := w.ready("s1", "s2")
	explained := RefOf(w.stored(p.ID).Plan.OptionSet)

	cases := map[string]func(t *testing.T){
		"computed again": func(t *testing.T) {
			w.clock.advance(time.Minute)
			if _, err := w.compute(p.ID); err != nil {
				t.Fatal(err)
			}
		},
		"dropped by an edit": func(t *testing.T) {
			heavier := slices.Clone(w.stored(p.ID).Plan.Shipments)
			heavier[0].Items = slices.Clone(heavier[0].Items)
			heavier[0].Items[0].WeightKg = 40
			if _, err := w.svc.SaveDraft(t.Context(), w.customer, p.ID, DraftPatch{Shipments: heavier}); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			change(t)
			stored, err := StoreReport(t.Context(), w.pool, w.customer, p.ID, explained, w.report("about the old options"))
			if err != nil || stored {
				t.Errorf("StoreReport = %v, %v; want it refused without an error", stored, err)
			}
			if set := w.stored(p.ID).Plan.OptionSet; set != nil && set.Report != nil {
				t.Errorf("a report of other options was kept: %+v", set.Report)
			}
		})
	}
}

// Same computation time but another input hash (and the reverse) is another option set too.
func TestStoreReportComparesBothTheInputHashAndTheComputationTime(t *testing.T) {
	w := newWorld(t)
	p := w.ready("s1")
	ref := RefOf(w.stored(p.ID).Plan.OptionSet)
	for name, wrong := range map[string]OptionSetRef{
		"other hash": {InputHash: ref.InputHash + "x", ComputedAt: ref.ComputedAt},
		"other time": {InputHash: ref.InputHash, ComputedAt: contracts.At(ref.ComputedAt.Add(time.Millisecond))},
	} {
		if stored, err := StoreReport(t.Context(), w.pool, w.customer, p.ID, wrong, w.report("x")); err != nil || stored {
			t.Errorf("%s: StoreReport = %v, %v", name, stored, err)
		}
	}
	if stored, err := StoreReport(t.Context(), w.pool, w.customer, p.ID, ref, w.report("x")); err != nil || !stored {
		t.Errorf("the right reference: %v, %v", stored, err)
	}
}

func TestStoreReportIsForTheOwnerOfThePlan(t *testing.T) {
	w := newWorld(t)
	p := w.ready("s1")
	intruder := testkit.Customer(t, w.pool, "Raka")
	_, err := StoreReport(t.Context(), w.pool, intruder, p.ID, RefOf(w.stored(p.ID).Plan.OptionSet), w.report("not yours"))
	if got := apiError(t, err); got.Code != platform.CodeDenied || got.Msg.Key != "srv.api.planNoAccess" {
		t.Errorf("got %+v", got)
	}
	_, err = StoreReport(t.Context(), w.pool, w.customer, "pln-missing", OptionSetRef{}, w.report("x"))
	wantError(t, err, platform.CodeNotFound, "srv.ops.err.planNotFound")
	if set := w.stored(p.ID).Plan.OptionSet; set.Report != nil {
		t.Errorf("an intruder's report was kept: %+v", set.Report)
	}
	if !strings.HasPrefix(p.ID, "pln-") {
		t.Fatal("setup")
	}
}
