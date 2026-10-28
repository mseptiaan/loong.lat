package settings

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/testkit"
)

func apply(t *testing.T, p *Projection, version int64, ev contracts.SettingsUpdated) error {
	t.Helper()
	env, err := contracts.NewEnvelope("backoffice-api", contracts.TypeSettingsUpdated, ev.Key, version, ev)
	if err != nil {
		t.Fatal(err)
	}
	env.OccurredAt = contracts.At(time.Date(2026, 10, 21, 3, 0, 0, 0, time.UTC))
	return platform.WithTx(t.Context(), p.pool, func(tx pgx.Tx) error { return update(t.Context(), tx, env) })
}

func get(t *testing.T, p *Projection) App {
	t.Helper()
	app, err := p.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func ai(vision, voice, intent, copilot bool) *contracts.AIFeatures {
	return &contracts.AIFeatures{Vision: vision, Voice: voice, Intent: intent, Copilot: copilot}
}

func TestBeforeAnythingArrivesTheDefaultsApplyAndAIIsOffWithoutAGateway(t *testing.T) {
	pool := testkit.DB(t)
	withGateway, without := New(pool, true), New(pool, false)

	if got := get(t, withGateway); got != Default || !got.AI.Vision || !got.AI.Voice || !got.AI.Intent || !got.AI.Copilot || !got.Dispatch.Auto {
		t.Errorf("defaults with a gateway = %+v, want everything on", got)
	}
	if got := get(t, without); got.AI != (contracts.AIFeatures{}) || !got.Dispatch.Auto {
		t.Errorf("defaults without a gateway = %+v: no AI feature may be offered, dispatch is unaffected", got)
	}
}

func TestSettingsFollowBackOfficeAndAIIsOfferedOnlyWithAGateway(t *testing.T) {
	pool := testkit.DB(t)
	withGateway, without := New(pool, true), New(pool, false)

	if err := apply(t, withGateway, 1, contracts.SettingsUpdated{Key: contracts.SettingsKeyAI, AI: ai(false, true, false, true)}); err != nil {
		t.Fatal(err)
	}
	if err := apply(t, withGateway, 1, contracts.SettingsUpdated{Key: contracts.SettingsKeyDispatch, Dispatch: &contracts.DispatchSettings{Auto: false}}); err != nil {
		t.Fatal(err)
	}
	if got := get(t, withGateway); got.AI != *ai(false, true, false, true) || got.Dispatch.Auto {
		t.Errorf("settings = %+v, want what the admin chose", got)
	}
	if got := get(t, without); got.AI != (contracts.AIFeatures{}) || got.Dispatch.Auto {
		t.Errorf("without a gateway = %+v: the admin's AI flags AND the gateway; dispatch as stored", got)
	}

	// One key arriving leaves the other as it was.
	if err := apply(t, withGateway, 2, contracts.SettingsUpdated{Key: contracts.SettingsKeyAI, AI: ai(true, true, true, false)}); err != nil {
		t.Fatal(err)
	}
	if got := get(t, withGateway); got.AI != *ai(true, true, true, false) || got.Dispatch.Auto {
		t.Errorf("after the second ai record: %+v", got)
	}
}

func TestAnOlderRecordCannotReplaceANewerOne(t *testing.T) {
	pool := testkit.DB(t)
	p := New(pool, true)
	if err := apply(t, p, 5, contracts.SettingsUpdated{Key: contracts.SettingsKeyAI, AI: ai(false, false, false, false)}); err != nil {
		t.Fatal(err)
	}
	if err := apply(t, p, 4, contracts.SettingsUpdated{Key: contracts.SettingsKeyAI, AI: ai(true, true, true, true)}); err != nil {
		t.Fatalf("an old record must be dropped quietly: %v", err)
	}
	if got := get(t, p); got.AI != (contracts.AIFeatures{}) {
		t.Errorf("an older record won: %+v", got.AI)
	}
	// A repeat of the newest is harmless; a newer one wins.
	if err := apply(t, p, 5, contracts.SettingsUpdated{Key: contracts.SettingsKeyAI, AI: ai(false, false, false, false)}); err != nil {
		t.Fatal(err)
	}
	if err := apply(t, p, 6, contracts.SettingsUpdated{Key: contracts.SettingsKeyAI, AI: ai(true, false, false, false)}); err != nil {
		t.Fatal(err)
	}
	if got := get(t, p); got.AI != *ai(true, false, false, false) {
		t.Errorf("settings = %+v", got.AI)
	}
}

func TestAnUnknownKeyIsIgnoredAndAKeyWithoutAValueIsRefused(t *testing.T) {
	pool := testkit.DB(t)
	p := New(pool, true)
	if err := apply(t, p, 1, contracts.SettingsUpdated{Key: "theme"}); err != nil {
		t.Errorf("a key from a newer back office: %v", err)
	}
	if n := testkit.Count(t, pool, `SELECT count(*) FROM settings`); n != 0 {
		t.Errorf("%d rows stored for an unknown key", n)
	}
	for _, key := range []string{contracts.SettingsKeyAI, contracts.SettingsKeyDispatch} {
		if err := apply(t, p, 1, contracts.SettingsUpdated{Key: key}); err == nil {
			t.Errorf("%s without a value was accepted", key)
		}
	}
	if got := get(t, p); got != Default {
		t.Errorf("settings = %+v: nothing valid arrived", got)
	}
}
