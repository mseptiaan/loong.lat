package app

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

func platformEnv(vars map[string]string) *platform.Env { return platform.NewEnvFrom(vars) }

// settingsOf reads what settings.get tells the dashboard about the AI features.
func aiFlags(t *testing.T, c caller) contracts.AIFeatures {
	t.Helper()
	var got struct {
		AI contracts.AIFeatures `json:"ai"`
	}
	c.rpc("settings.get", nil).into(&got)
	return got.AI
}

func TestWithoutAGatewayEveryAIFeatureIsOffAndRefused(t *testing.T) {
	svc := startService(t)
	dina, _ := svc.register("dina@contoh.id")
	if flags := aiFlags(t, dina); flags != (contracts.AIFeatures{}) {
		t.Fatalf("flags = %+v: without AI_URL nothing may be offered", flags)
	}
	for method, body := range map[string]any{
		"ai.parseIntent": map[string]any{"text": "cheapest"},
		"ai.parseSpeech": map[string]any{"transcript": "two boxes"},
		"ai.transcribe":  map[string]any{"input": map[string]any{"type": "audio/wav", "size": 1, "data": "AA=="}},
		"ai.readImage":   map[string]any{"input": map[string]any{"name": "a.png", "type": "image/png", "size": 1, "data": "AA=="}},
		"ai.copilot":     map[string]any{"scope": map[string]any{"kind": "plan", "planId": "pln-x"}, "question": "hi"},
	} {
		r := dina.rpc(method, body)
		if f := r.failure(); r.status != http.StatusForbidden || f.Code != "ditolak" || f.Msg.Key != "srv.api.aiDisabled" {
			t.Errorf("%s: %d %+v, want 403 ditolak srv.api.aiDisabled", method, r.status, f)
		}
	}
	anon := svc.anonymous().rpc("ai.parseIntent", map[string]any{"text": "cheapest"})
	if anon.status != http.StatusUnauthorized {
		t.Errorf("anonymous: %d", anon.status)
	}
}

func TestWithAGatewayTheAIMethodsAreServedThroughTheWholeStack(t *testing.T) {
	var calls int
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req aiapi.ChatRequest
		if r.URL.Path != aiapi.ChatPath || r.Header.Get("Authorization") != "Bearer gateway-token" || json.UnmarshalRead(r.Body, &req) != nil || req.Profile != aiapi.ProfileIntent {
			http.Error(w, "unexpected call", http.StatusBadRequest)
			return
		}
		reply := `{"priority":{"preset":"termurah","contradictory":false,"words":"cheapest"},"deadline":{"present":false,"local_datetime":"","words":""},"leftover":""}`
		body, _ := json.Marshal(aiapi.ChatResponse{Message: aiapi.Message{Role: aiapi.RoleAssistant, Content: reply}, FinishReason: aiapi.FinishStop})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer gateway.Close()

	svc := startService(t, func(c *Config) {
		c.AIURL, c.AIToken = gateway.URL, "gateway-token"
		c.AIRateBurst, c.AIRatePerMinute = 2, 1
	})
	dina, _ := svc.register("dina@contoh.id")
	if flags := aiFlags(t, dina); flags != (contracts.AIFeatures{Vision: true, Voice: true, Intent: true, Copilot: true}) {
		t.Fatalf("flags = %+v: with a gateway and nothing switched off, everything is offered", flags)
	}

	var parsed struct {
		Chips []struct {
			Kind, Preset string
		} `json:"chips"`
	}
	r := dina.rpc("ai.parseIntent", map[string]any{"text": "cheapest please"})
	r.into(&parsed)
	if len(parsed.Chips) != 1 || calls != 1 {
		t.Fatalf("chips %+v after %d gateway calls: %s", parsed.Chips, calls, r.body)
	}

	// The per-customer limit comes from the configuration.
	dina.rpc("ai.parseIntent", map[string]any{"text": "cheapest please"}).into(&parsed)
	if r := dina.rpc("ai.parseIntent", map[string]any{"text": "cheapest please"}); r.status != http.StatusTooManyRequests || r.failure().Msg.Key != "srv.rpc.tooManyRequests" {
		t.Errorf("third request: %d %s", r.status, r.body)
	}
	other, _ := svc.register("eko@contoh.id")
	other.rpc("ai.parseIntent", map[string]any{"text": "cheapest please"}).into(&parsed)

	// A plan that is not the caller's is not found, whoever asks.
	if r := other.rpc("ai.copilot", map[string]any{"scope": map[string]any{"kind": "plan", "planId": "pln-not-mine"}, "question": "hi"}); r.status != http.StatusNotFound {
		t.Errorf("a plan that is not theirs: %d %s", r.status, r.body)
	}
}

func TestConfigFromEnvReadsTheAIRateLimit(t *testing.T) {
	env := platformEnv(map[string]string{"DATABASE_URL": "postgres://x", "KAFKA_BROKERS": "a:9092"})
	cfg := ConfigFromEnv(env)
	if cfg.AIRatePerMinute != 20 || cfg.AIRateBurst != 10 {
		t.Errorf("defaults = %d/%d, want 20 a minute and a burst of 10", cfg.AIRatePerMinute, cfg.AIRateBurst)
	}
	cfg = ConfigFromEnv(platformEnv(map[string]string{"DATABASE_URL": "postgres://x", "KAFKA_BROKERS": "a:9092", "AI_USER_RATE_PER_MINUTE": "5", "AI_USER_RATE_BURST": "3"}))
	if cfg.AIRatePerMinute != 5 || cfg.AIRateBurst != 3 {
		t.Errorf("cfg = %d/%d", cfg.AIRatePerMinute, cfg.AIRateBurst)
	}
}
