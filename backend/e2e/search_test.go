//go:build e2e

package e2e

import (
	"encoding/json/v2"
	"slices"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

// The three search levels of a quote (docs/optimizer-search.md) through the console, the optimizer and the solver.

type searchEvent struct {
	Type      string              `json:"type"`
	Step      int                 `json:"step"`
	Total     int                 `json:"total"`
	Msg       *contracts.Msg      `json:"msg"`
	OptionSet contracts.OptionSet `json:"optionSet"`
}

// quoteAt asks for a quote of the plan at an effort and returns the events of the stream.
func (w *world) quoteAt(c *session, planID string, effort string) (contracts.OptionSet, []searchEvent) {
	w.t.Helper()
	var (
		set    contracts.OptionSet
		events []searchEvent
	)
	// The vehicles arrive in console-api over Kafka; quoting works as soon as they have.
	eventually(w.t, "a "+effort+" quote", 120*time.Second, func() (bool, string) {
		raws, err := c.stream("plans.computeOptions", map[string]any{"id": planID, "effort": effort})
		if err != nil {
			return false, err.Error()
		}
		events = events[:0]
		for _, raw := range raws {
			var ev searchEvent
			if err := json.Unmarshal(raw, &ev); err != nil {
				w.t.Fatalf("bad event %s: %v", raw, err)
			}
			events = append(events, ev)
			switch ev.Type {
			case "done":
				set = ev.OptionSet
				return true, ""
			case "error":
				return false, string(raw)
			}
		}
		return false, "no done event"
	})
	return set, events
}

func cheapest(set contracts.OptionSet) (cost int64, distKm float64) {
	cost, distKm = 1<<62, 1e18
	for _, o := range set.Options {
		cost = min(cost, o.Totals.CostRp)
		distKm = min(distKm, o.Totals.DistanceKm)
	}
	return cost, distKm
}

func TestADeeperSearchNeverQuotesWorseThanTheQuickOne(t *testing.T) {
	w := newWorld(t)
	t.Cleanup(func() {
		if t.Failed() {
			w.dump()
		}
	})
	admin := w.admin()
	w.provisionFleet(admin)
	c := w.customer()

	var plan contracts.Plan
	c.must("plans.createDraft", nil, &plan)
	c.must("plans.saveDraft", map[string]any{"id": plan.ID, "patch": map[string]any{
		"shipments": shipments(t),
		"schedule":  map[string]any{"mode": "sekarang"},
		"priority":  map[string]any{"preset": "termurah"},
		"draftStep": 3,
	}}, &plan)

	fast, events := w.quoteAt(c, plan.ID, "fast")
	var steps int
	for _, ev := range events {
		if ev.Type == "progress" {
			steps++
			if ev.Msg != nil {
				t.Errorf("a fast quote reports a sub-step: %+v", ev.Msg)
			}
		}
	}
	if steps != 4 {
		t.Errorf("a fast quote streams %d progress events, the wizard shows exactly 4", steps)
	}
	if fast.Method == nil || fast.Method.Effort != contracts.EffortFast || fast.Method.Trials < 1 || fast.Method.Polished {
		t.Fatalf("fast method = %+v", fast.Method)
	}
	fastCost, fastDist := cheapest(fast)

	// An invalid effort is refused before any work starts.
	if _, err := c.stream("plans.computeOptions", map[string]any{"id": plan.ID, "effort": "extreme"}); err == nil {
		t.Error("an unknown effort must be refused")
	}

	for _, effort := range []contracts.Effort{contracts.EffortThorough, contracts.EffortReviewed} {
		t.Run(string(effort), func(t *testing.T) {
			set, events := w.quoteAt(c, plan.ID, string(effort))
			m := set.Method
			if m == nil || m.Effort != effort || !m.Polished || m.Trials <= fast.Method.Trials {
				t.Fatalf("method = %+v, want effort %s, polished, with more trials than the %d of a fast quote", m, effort, fast.Method.Trials)
			}
			cost, dist := cheapest(set)
			if cost > fastCost || dist > fastDist+1e-9 {
				t.Errorf("a %s quote costs %d / %.2f km at best, the fast one %d / %.2f km", effort, cost, dist, fastCost, fastDist)
			}

			var keys []string
			for _, ev := range events {
				if ev.Type != "progress" {
					continue
				}
				if ev.Step < 1 || ev.Step > ev.Total || ev.Total != 4 {
					t.Errorf("progress step %d of %d", ev.Step, ev.Total)
				}
				if ev.Msg != nil {
					keys = append(keys, ev.Msg.Key)
					if ev.Step != 3 {
						t.Errorf("sub-step %s reported in stage %d, they belong to stage 3", ev.Msg.Key, ev.Step)
					}
				}
			}
			if !slices.Contains(keys, "srv.engine.search.polish") {
				t.Errorf("no polish step among %v", keys)
			}
			if effort == contracts.EffortReviewed {
				// The e2e stack runs without the AI gateway: the built-in rules review.
				if m.Reviewer != contracts.ReviewerRules || !slices.Contains(keys, "srv.engine.search.reviewRules") {
					t.Errorf("reviewer %q, steps %v: want the rules reviewer", m.Reviewer, keys)
				}
			} else if m.Reviewer != "" || m.Rounds != 0 {
				t.Errorf("a thorough quote has no review: %+v", m)
			}

			// The quote replaces the earlier options and the choice is open again.
			got := w.plan(c, plan.ID)
			if got.OptionSet == nil || got.OptionSet.Method == nil || got.OptionSet.Method.Effort != effort || got.SelectedOptionID != "" {
				t.Errorf("stored plan: options %+v, selected %q", got.OptionSet, got.SelectedOptionID)
			}
		})
	}
}
