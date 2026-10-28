package settings_test

import (
	"errors"
	"testing"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/drivertest"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/settings"
)

// These tests need Postgres (TEST_DATABASE_URL) and skip without it.

func TestWithNothingAnnouncedTheDefaultsApply(t *testing.T) {
	pool := drivertest.Pool(t)
	got, err := settings.Read(t.Context(), pool)
	if err != nil || got != settings.Defaults {
		t.Fatalf("settings = %+v, %v; want the defaults", got, err)
	}
	if !got.AI.Vision || !got.AI.Voice || !got.AI.Intent || !got.AI.Copilot || !got.Dispatch.Auto {
		t.Errorf("the defaults must be all on (DEFAULT_SETTINGS of the frontend): %+v", got)
	}
}

func TestEachGroupKeepsItsNewestVersion(t *testing.T) {
	pool := drivertest.Pool(t)
	ctx := t.Context()
	put := func(key string, version int64, value any) bool {
		t.Helper()
		stored, err := settings.Put(ctx, pool, key, version, value, drivertest.Epoch)
		if err != nil {
			t.Fatal(err)
		}
		return stored
	}

	if !put(contracts.SettingsKeyAI, 2, contracts.AIFeatures{Vision: true, Copilot: true}) {
		t.Fatal("the first value was not stored")
	}
	if put(contracts.SettingsKeyAI, 1, contracts.AIFeatures{Voice: true}) || put(contracts.SettingsKeyAI, 2, contracts.AIFeatures{Intent: true}) {
		t.Error("an older or repeated version was stored")
	}
	got, _ := settings.Read(ctx, pool)
	if want := (contracts.AIFeatures{Vision: true, Copilot: true}); got.AI != want {
		t.Errorf("ai = %+v, want %+v", got.AI, want)
	}
	if !got.Dispatch.Auto {
		t.Error("a group nobody announced lost its default")
	}

	if !put(contracts.SettingsKeyAI, 3, contracts.AIFeatures{}) || !put(contracts.SettingsKeyDispatch, 1, contracts.DispatchSettings{Auto: false}) {
		t.Fatal("newer versions were not stored")
	}
	got, _ = settings.Read(ctx, pool)
	if got.AI != (contracts.AIFeatures{}) || got.Dispatch.Auto {
		t.Errorf("settings = %+v, want everything off", got)
	}
}

func TestTheAIFlagsAreTheAdminsFlagsAndAGatewayThatExists(t *testing.T) {
	pool := drivertest.Pool(t)
	ctx := t.Context()
	admin := contracts.AIFeatures{Vision: true, Voice: false, Intent: true, Copilot: false}
	if _, err := settings.Put(ctx, pool, contracts.SettingsKeyAI, 1, admin, drivertest.Epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.Put(ctx, pool, contracts.SettingsKeyDispatch, 1, contracts.DispatchSettings{Auto: false}, drivertest.Epoch); err != nil {
		t.Fatal(err)
	}

	with, err := settings.NewService(pool, true).Get(ctx)
	if err != nil || with.AI != admin || with.Dispatch.Auto {
		t.Errorf("with a gateway: %+v, %v; want the admin's flags %+v and dispatch off", with, err, admin)
	}
	without, err := settings.NewService(pool, false).Get(ctx)
	if err != nil || without.AI != (contracts.AIFeatures{}) || without.Dispatch.Auto {
		t.Errorf("without a gateway: %+v, %v; want every AI flag off and the rest as announced", without, err)
	}

	// and with nothing announced at all: the defaults are on only where a gateway exists
	empty := drivertest.Pool(t)
	on, _ := settings.NewService(empty, true).Get(ctx)
	off, _ := settings.NewService(empty, false).Get(ctx)
	if on.AI != settings.Defaults.AI || off.AI != (contracts.AIFeatures{}) || !on.Dispatch.Auto || !off.Dispatch.Auto {
		t.Errorf("defaults with a gateway %+v, without %+v", on, off)
	}
}

func TestGroupReadsTheValueOfTheEventsKey(t *testing.T) {
	ai, dispatch := contracts.AIFeatures{Vision: true}, contracts.DispatchSettings{Auto: true}
	for name, tc := range map[string]struct {
		in      contracts.SettingsUpdated
		want    any
		wantErr error
	}{
		"ai":              {in: contracts.SettingsUpdated{Key: "ai", AI: &ai}, want: ai},
		"dispatch":        {in: contracts.SettingsUpdated{Key: "dispatch", Dispatch: &dispatch}, want: dispatch},
		"the other value": {in: contracts.SettingsUpdated{Key: "ai", Dispatch: &dispatch}, wantErr: errors.New("missing")},
		"no value":        {in: contracts.SettingsUpdated{Key: "dispatch"}, wantErr: errors.New("missing")},
		"a future group":  {in: contracts.SettingsUpdated{Key: "payments"}, wantErr: settings.ErrUnknownKey},
		"no key":          {in: contracts.SettingsUpdated{AI: &ai}, wantErr: settings.ErrUnknownKey},
	} {
		got, err := settings.Group(tc.in)
		switch {
		case tc.wantErr == nil && (err != nil || got != tc.want):
			t.Errorf("%s: %v, %v; want %v", name, got, err, tc.want)
		case tc.wantErr == settings.ErrUnknownKey && !errors.Is(err, settings.ErrUnknownKey):
			t.Errorf("%s: error %v, want the unknown-key error", name, err)
		case tc.wantErr != nil && tc.wantErr != settings.ErrUnknownKey && (err == nil || errors.Is(err, settings.ErrUnknownKey)):
			t.Errorf("%s: error %v, want a missing-value error", name, err)
		}
	}
}
