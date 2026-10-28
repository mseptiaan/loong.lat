package app

import (
	"context"
	"encoding/json/v2"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/optimizerapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

func TestTheDeepSettingsHaveDefaultsAndCanBeSet(t *testing.T) {
	env := platform.NewEnvFrom(map[string]string{})
	cfg := ConfigFromEnv(env)
	if cfg.Timeout != 45*time.Second || cfg.DeepTimeout != 150*time.Second || cfg.Rounds != 3 || cfg.AIURL != "" || cfg.AIToken != "" {
		t.Errorf("defaults = timeout %v deep %v rounds %d ai %q/%q", cfg.Timeout, cfg.DeepTimeout, cfg.Rounds, cfg.AIURL, cfg.AIToken)
	}

	env = platform.NewEnvFrom(map[string]string{
		"OPTIMIZER_TIMEOUT": "20s", "OPTIMIZER_TIMEOUT_DEEP": "3m", "OPTIMIZER_ROUNDS": "2", "AI_URL": "http://ai:8091", "AI_TOKEN": "optimizer-token",
	})
	cfg = ConfigFromEnv(env)
	if cfg.Timeout != 20*time.Second || cfg.DeepTimeout != 3*time.Minute || cfg.Rounds != 2 || cfg.AIURL != "http://ai:8091" || cfg.AIToken != "optimizer-token" {
		t.Errorf("config = %+v", cfg)
	}
	if err := env.Err(); err != nil {
		t.Error(err)
	}
}

func TestNoRouteMayUseATollRoadUnlessAllowed(t *testing.T) {
	if cfg := ConfigFromEnv(platform.NewEnvFrom(map[string]string{})); cfg.RoadAllowTolls {
		t.Error("toll roads must be off by default: none of our vehicles may use them")
	}
	if cfg := ConfigFromEnv(platform.NewEnvFrom(map[string]string{"ROAD_ALLOW_TOLLS": "true"})); !cfg.RoadAllowTolls {
		t.Error("ROAD_ALLOW_TOLLS=true must allow them")
	}
}

func TestInfoSaysWhichEffortsAreOfferedAndWhetherTheModelCanReview(t *testing.T) {
	for name, aiURL := range map[string]string{"no gateway": "", "a gateway": "http://127.0.0.1:1"} {
		ctx, cancel := context.WithCancel(context.Background())
		a, err := Start(ctx, Config{HTTPAddr: "127.0.0.1:0", Dev: true, Runner: "local", AIURL: aiURL, Timeout: time.Second, DeepTimeout: 2 * time.Second},
			slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		resp, err := http.Get("http://" + a.Addr() + optimizerapi.InfoPath)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		var info optimizerapi.Info
		err = json.UnmarshalRead(resp.Body, &info)
		resp.Body.Close()
		cancel()
		a.Wait()
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(info.Efforts, contracts.Efforts) || info.AIReview != (aiURL != "") || info.Runner != "local" || info.Engine == "" {
			t.Errorf("%s: info = %+v", name, info)
		}
	}
}
