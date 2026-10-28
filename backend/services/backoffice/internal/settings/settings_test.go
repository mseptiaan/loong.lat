package settings

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/changes"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/testkit"
)

func TestPatchChangesOnlyWhatItCarries(t *testing.T) {
	cur := App{AI: contracts.AIFeatures{Vision: true, Voice: true, Intent: true, Copilot: true}, Dispatch: contracts.DispatchSettings{Auto: true}}

	got := Patch{AI: &AIPatch{Voice: new(false)}}.apply(cur)
	want := cur
	want.AI.Voice = false
	if got != want {
		t.Errorf("patched = %+v, want only voice switched off", got)
	}
	if got := (Patch{}).apply(cur); got != cur {
		t.Errorf("an empty patch changed the settings: %+v", got)
	}
	if got := (Patch{AI: &AIPatch{}, Dispatch: &DispatchPatch{}}).apply(cur); got != cur {
		t.Errorf("empty groups changed the settings: %+v", got)
	}
	both := Patch{AI: &AIPatch{Vision: new(false), Copilot: new(false)}, Dispatch: &DispatchPatch{Auto: new(false)}}.apply(cur)
	if both.AI.Vision || both.AI.Copilot || !both.AI.Voice || !both.AI.Intent || both.Dispatch.Auto {
		t.Errorf("patched = %+v", both)
	}
}

// These tests need a real Postgres (TEST_DATABASE_URL) and skip without one.

type rig struct {
	pool    *pgxpool.Pool
	svc     *Service
	enabled int
}

func newRig(t *testing.T) *rig {
	t.Helper()
	pool := testkit.Pool(t)
	r := &rig{pool: pool}
	r.svc = New(pool, testkit.Publisher(pool), changes.New(platform.NewHub(), time.Millisecond), func() { r.enabled++ })
	return r
}

func TestSettingsStartWithTheMocksDefaults(t *testing.T) {
	r := newRig(t)
	got, err := Read(t.Context(), r.pool)
	if err != nil {
		t.Fatal(err)
	}
	want := App{AI: contracts.AIFeatures{Vision: true, Voice: true, Intent: true, Copilot: true}, Dispatch: contracts.DispatchSettings{Auto: true}}
	if got != want {
		t.Errorf("settings = %+v, want %+v", got, want)
	}
	if auto, err := DispatchAuto(t.Context(), r.pool); err != nil || !auto {
		t.Errorf("DispatchAuto = %v, %v", auto, err)
	}
	if n := len(testkit.Outbox(t, r.pool, contracts.TypeSettingsUpdated)); n != 0 {
		t.Errorf("%d events before any change", n)
	}
}

func TestUpdateAnnouncesEachChangedGroupOnTheCompactedTopic(t *testing.T) {
	r := newRig(t)
	got, err := r.svc.Update(t.Context(), Patch{
		AI:       &AIPatch{Voice: new(false)},
		Dispatch: &DispatchPatch{Auto: new(false)},
	})
	if err != nil || got.AI.Voice || got.Dispatch.Auto || !got.AI.Vision {
		t.Fatalf("update = %+v, %v", got, err)
	}
	events := testkit.Outbox(t, r.pool, contracts.TypeSettingsUpdated)
	if len(events) != 2 {
		t.Fatalf("%d events, want one per changed group", len(events))
	}
	byKey := map[string]contracts.SettingsUpdated{}
	for _, env := range events {
		ev, err := env.Payload[contracts.SettingsUpdated]()
		if err != nil {
			t.Fatal(err)
		}
		if env.Key != ev.Key || env.AggregateVersion != 1 {
			t.Errorf("envelope key %q version %d for group %q, want the group as key at version 1", env.Key, env.AggregateVersion, ev.Key)
		}
		byKey[ev.Key] = ev
	}
	if ai := byKey[contracts.SettingsKeyAI]; ai.AI == nil || ai.Dispatch != nil || *ai.AI != got.AI {
		t.Errorf("ai event = %+v, want the full AI group", ai)
	}
	if d := byKey[contracts.SettingsKeyDispatch]; d.Dispatch == nil || d.AI != nil || d.Dispatch.Auto {
		t.Errorf("dispatch event = %+v, want the dispatch group with auto off", d)
	}
	var topic string
	if err := r.pool.QueryRow(t.Context(), `SELECT DISTINCT topic FROM outbox`).Scan(&topic); err != nil || topic != contracts.TopicSettings {
		t.Errorf("topic = %q, %v; want the settings topic", topic, err)
	}

	// the next change of a group is its next version; a change of nothing is not announced
	if _, err := r.svc.Update(t.Context(), Patch{Dispatch: &DispatchPatch{Auto: new(true)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Update(t.Context(), Patch{Dispatch: &DispatchPatch{Auto: new(true)}, AI: &AIPatch{Voice: new(false)}}); err != nil {
		t.Fatal(err)
	}
	events = testkit.Outbox(t, r.pool, contracts.TypeSettingsUpdated)
	if len(events) != 3 || events[2].Key != contracts.SettingsKeyDispatch || events[2].AggregateVersion != 2 {
		t.Errorf("events = %d, last %q at %d; want 3 with the dispatch group at version 2 last", len(events), events[len(events)-1].Key, events[len(events)-1].AggregateVersion)
	}
	if now, _ := Read(t.Context(), r.pool); !now.Dispatch.Auto || now.AI.Voice {
		t.Errorf("settings = %+v", now)
	}
}

func TestSwitchingAutomaticDispatchOnWakesTheWorkerOnlyThen(t *testing.T) {
	r := newRig(t)
	update := func(auto bool) {
		t.Helper()
		if _, err := r.svc.Update(t.Context(), Patch{Dispatch: &DispatchPatch{Auto: &auto}}); err != nil {
			t.Fatal(err)
		}
	}
	update(true) // already on
	update(false)
	if r.enabled != 0 {
		t.Fatalf("worker woken %d times by changes that did not switch dispatch on", r.enabled)
	}
	update(true)
	update(true)
	if r.enabled != 1 {
		t.Errorf("worker woken %d times, want once, by the switch on", r.enabled)
	}
	if _, err := r.svc.Update(t.Context(), Patch{AI: &AIPatch{Intent: new(false)}}); err != nil || r.enabled != 1 {
		t.Errorf("an AI change woke the worker: %d, %v", r.enabled, err)
	}
}
