package app

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/meta"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

const (
	testKey   = "meta-key-0123456789-DO-NOT-LEAK"
	testToken = "caller-token-DO-NOT-LEAK"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestConfigFromEnv(t *testing.T) {
	env := platform.NewEnvFrom(map[string]string{
		"AI_TOKEN":                 "console-token, backoffice-token, optimizer-token",
		"META_API_KEY":             testKey,
		"META_MODEL":               "muse-spark-test",
		"AI_COPILOT_EFFORT":        "low", // the default is minimal; an override still wins
		"AI_COPILOT_MAX_TOKENS":    "2500",
		"AI_COPILOT_TIMEOUT":       "90s",
		"AI_VISION_MODEL":          "muse-vision-test",
		"AI_TRANSCRIBE_TIMEOUT":    "12s",
		"AI_INTENT_MAX_BODY_BYTES": "32768",
		"AI_REVIEW_TIMEOUT":        "12s", // the optimizer's reviewer
		"AI_REVIEW_MAX_TOKENS":     "2500",
		"AI_REPORT_EFFORT":         "low", // the console's explanation of a quote
		"AI_REPORT_MODEL":          "muse-report-test",
		"AI_REPORT_MAX_BODY_BYTES": "131072",
		"AI_RATE_PER_MINUTE":       "30",
	})
	cfg := ConfigFromEnv(env)
	if err := env.Err(); err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":8091" || len(cfg.Tokens) != 3 || cfg.Tokens[1].Reveal() != "backoffice-token" || cfg.Tokens[2].Reveal() != "optimizer-token" || cfg.MetaAPIKey.Reveal() != testKey ||
		cfg.MetaBaseURL != meta.DefaultBaseURL || cfg.RatePerMinute != 30 || cfg.RateBurst != 30 || cfg.MaxInFlight != 16 {
		t.Errorf("config = %+v", cfg)
	}
	copilot := cfg.Profiles[aiapi.ProfileCopilot]
	if copilot.Model != "muse-spark-test" || copilot.ReasoningEffort != "low" || copilot.MaxTokens != 2500 || copilot.Timeout != 90*time.Second {
		t.Errorf("copilot = %+v", copilot)
	}
	if cfg.Profiles[aiapi.ProfileVision].Model != "muse-vision-test" || cfg.Profiles[aiapi.ProfileIntent].MaxBodyBytes != 32768 ||
		cfg.Profiles[aiapi.ProfileTranscribe].Timeout != 12*time.Second || cfg.Profiles[aiapi.ProfileTranscribe].Model != meta.DefaultTranscribeModel {
		t.Errorf("profiles = %+v", cfg.Profiles)
	}
	if cfg.Profiles[aiapi.ProfileInsights].Model != "muse-spark-test" || cfg.Profiles[aiapi.ProfileInsights].ReasoningEffort != "minimal" {
		t.Errorf("untouched profiles keep their defaults: %+v", cfg.Profiles[aiapi.ProfileInsights])
	}
	review, report := cfg.Profiles[aiapi.ProfileReview], cfg.Profiles[aiapi.ProfileReport]
	if review.Timeout != 12*time.Second || review.MaxTokens != 2500 || review.Model != "muse-spark-test" || review.ReasoningEffort != "minimal" || review.MaxBodyBytes != 64<<10 {
		t.Errorf("review = %+v: AI_REVIEW_* override it, the rest keeps its defaults", review)
	}
	if report.Model != "muse-report-test" || report.ReasoningEffort != "low" || report.MaxBodyBytes != 131072 || report.MaxTokens != 3000 || report.Timeout != 45*time.Second {
		t.Errorf("report = %+v: AI_REPORT_* override it, the rest keeps its defaults", report)
	}

	empty := ConfigFromEnv(platform.NewEnvFrom(nil))
	if empty.MetaAPIKey.Set() || len(empty.Tokens) != 0 || empty.MetaModel != meta.DefaultModel || empty.Profiles[aiapi.ProfileCopilot].Model != meta.DefaultModel {
		t.Errorf("defaults = %+v", empty)
	}
	for _, name := range []aiapi.Profile{aiapi.ProfileReview, aiapi.ProfileReport} {
		if empty.Profiles[name].Model != meta.DefaultModel || empty.Profiles[name].MaxTokens < 1500 {
			t.Errorf("%s without overrides = %+v", name, empty.Profiles[name])
		}
	}

	bad := platform.NewEnvFrom(map[string]string{"AI_COPILOT_MAX_TOKENS": "lots", "AI_VISION_TIMEOUT": "soon", "AI_REVIEW_TIMEOUT": "later", "AI_REPORT_MAX_TOKENS": "many"})
	ConfigFromEnv(bad)
	if err := bad.Err(); err == nil || !strings.Contains(err.Error(), "AI_COPILOT_MAX_TOKENS") || !strings.Contains(err.Error(), "AI_VISION_TIMEOUT") ||
		!strings.Contains(err.Error(), "AI_REVIEW_TIMEOUT") || !strings.Contains(err.Error(), "AI_REPORT_MAX_TOKENS") {
		t.Errorf("env errors = %v", err)
	}
}

func TestPrintingTheConfigNeverShowsASecret(t *testing.T) {
	cfg := ConfigFromEnv(platform.NewEnvFrom(map[string]string{"AI_TOKEN": testToken, "META_API_KEY": testKey}))
	var logs lockedBuffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("starting", "key", cfg.MetaAPIKey, "tokens", cfg.Tokens)
	for name, out := range map[string]string{
		"%v": fmt.Sprintf("%v", cfg), "%+v": fmt.Sprintf("%+v", cfg), "%#v": fmt.Sprintf("%#v", cfg), "slog": logs.String(),
	} {
		if strings.Contains(out, testKey) || strings.Contains(out, testToken) {
			t.Errorf("%s output leaks a secret", name)
		}
	}
}

func start(t *testing.T, cfg Config, logs io.Writer) (*App, string, context.CancelFunc) {
	t.Helper()
	cfg.HTTPAddr = "127.0.0.1:0"
	ctx, cancel := context.WithCancel(t.Context())
	a, err := Start(ctx, cfg, slog.New(slog.NewJSONHandler(logs, nil)))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		a.Wait()
	})
	return a, "http://" + a.Addr(), cancel
}

func TestStartNeedsATokenUnlessInDevMode(t *testing.T) {
	_, err := Start(t.Context(), Config{HTTPAddr: "127.0.0.1:0"}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err == nil || !strings.Contains(err.Error(), "AI_TOKEN") {
		t.Errorf("err = %v", err)
	}
	_, err = Start(t.Context(), Config{HTTPAddr: "127.0.0.1:0", Tokens: []meta.Secret{""}}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("an empty token must not open the gateway: %v", err)
	}
}

func TestStartRefusesUnsafeProfiles(t *testing.T) {
	env := platform.NewEnvFrom(map[string]string{"AI_DEV": "true", "AI_INTENT_MAX_TOKENS": "100"})
	cfg := ConfigFromEnv(env)
	cfg.HTTPAddr = "127.0.0.1:0"
	_, err := Start(t.Context(), cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err == nil || !strings.Contains(err.Error(), "intent") {
		t.Errorf("err = %v", err)
	}
}

func TestStartRefusesUnsafeReviewAndReportProfiles(t *testing.T) {
	for env, name := range map[string]string{"AI_REVIEW_MAX_TOKENS": "review", "AI_REPORT_MAX_TOKENS": "report"} {
		cfg := ConfigFromEnv(platform.NewEnvFrom(map[string]string{"AI_DEV": "true", env: "100"}))
		cfg.HTTPAddr = "127.0.0.1:0"
		_, err := Start(t.Context(), cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s=100: err = %v", env, err)
		}
	}
}

func TestWithoutAKeyTheServiceStartsAndSaysSo(t *testing.T) {
	var logs lockedBuffer
	_, url, cancel := start(t, Config{Dev: true}, &logs)

	resp, err := http.Get(url + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"configured":false`) {
		t.Errorf("readyz = %d %s", resp.StatusCode, body)
	}
	client := &aiapi.Client{BaseURL: url}
	_, err = client.Chat(t.Context(), aiapi.ChatRequest{Profile: aiapi.ProfileIntent, Messages: []aiapi.Message{aiapi.UserText("hi")}})
	if !errors.Is(err, aiapi.ErrNotConfigured) {
		t.Errorf("err = %v", err)
	}
	if api, ok := errors.AsType[*platform.APIError](err); !ok || api.Msg.Key != "srv.engine.unavailable" {
		t.Errorf("platform error = %+v", api)
	}
	if !strings.Contains(logs.String(), "META_API_KEY is not set") {
		t.Errorf("startup should warn about the missing key: %s", logs.String())
	}
	cancel()
}

func TestStartedGatewayServesChatThroughTheClientWithoutLeakingSecrets(t *testing.T) {
	var sawKey string
	fakeMeta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawKey = r.Header.Get("Authorization")
		io.Copy(io.Discard, r.Body)
		body, _ := json.Marshal(map[string]any{
			"id": "x", "model": "served",
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ready"}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 3, "completion_tokens": 1, "total_tokens": 4},
		})
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	defer fakeMeta.Close()

	var logs lockedBuffer
	_, url, cancel := start(t, Config{
		Tokens: []meta.Secret{testToken}, MetaAPIKey: testKey, MetaBaseURL: fakeMeta.URL + "/v1", RatePerMinute: 60, RateBurst: 10, MaxInFlight: 4,
	}, &logs)

	client := &aiapi.Client{BaseURL: url, Token: testToken}
	resp, err := client.Chat(t.Context(), aiapi.ChatRequest{Profile: aiapi.ProfileInsights, Messages: []aiapi.Message{aiapi.UserText("summarize")}})
	if err != nil || resp.Message.Content != "ready" || resp.Usage.Total != 4 {
		t.Fatalf("resp %+v, err %v", resp, err)
	}
	if sawKey != "Bearer "+testKey {
		t.Errorf("Meta saw %q: the gateway adds the key itself", sawKey)
	}
	info, err := client.Info(t.Context())
	if err != nil || !info.Configured {
		t.Errorf("info = %+v, %v", info, err)
	}
	if _, err := (&aiapi.Client{BaseURL: url}).Chat(t.Context(), aiapi.ChatRequest{Profile: aiapi.ProfileInsights}); !errors.Is(err, aiapi.ErrUnauthorized) {
		t.Errorf("without the token: %v", err)
	}
	for _, secret := range []string{testKey, testToken} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("the logs contain a secret: %s", logs.String())
		}
	}
	cancel()
}

func TestShutdownIsGraceful(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	a, err := Start(ctx, Config{HTTPAddr: "127.0.0.1:0", Dev: true}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	done := make(chan error, 1)
	go func() { done <- a.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Wait = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the service did not stop")
	}
}

// The optimizer calls the gateway as a third caller with a token of its own: the review profile for its reviewer, and
// every token is checked and limited separately.
func TestAThirdCallerWithItsOwnTokenReachesTheReviewAndReportProfiles(t *testing.T) {
	fakeMeta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"x","model":"served","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer fakeMeta.Close()
	tokens := []meta.Secret{"console-secret", "backoffice-secret", "optimizer-secret"}
	var logs lockedBuffer
	_, url, cancel := start(t, Config{Tokens: tokens, MetaAPIKey: testKey, MetaBaseURL: fakeMeta.URL + "/v1", RatePerMinute: 60, RateBurst: 1, MaxInFlight: 4}, &logs)
	defer cancel()

	for i, secret := range tokens {
		client := &aiapi.Client{BaseURL: url, Token: secret.Reveal()}
		// A burst of one: each caller gets its first call through whatever the others did.
		if _, err := client.Chat(t.Context(), aiapi.ChatRequest{Profile: aiapi.ProfileReview, Messages: []aiapi.Message{aiapi.UserText("facts")}}); err != nil {
			t.Errorf("caller %d (review): %v", i, err)
		}
		if _, err := client.Chat(t.Context(), aiapi.ChatRequest{Profile: aiapi.ProfileReport, Messages: []aiapi.Message{aiapi.UserText("facts")}}); !errors.Is(err, aiapi.ErrRateLimited) {
			t.Errorf("caller %d: a second call in the same instant should be limited for that caller alone: %v", i, err)
		}
	}
	if _, err := (&aiapi.Client{BaseURL: url, Token: "someone-else"}).Chat(t.Context(), aiapi.ChatRequest{Profile: aiapi.ProfileReview, Messages: []aiapi.Message{aiapi.UserText("facts")}}); !errors.Is(err, aiapi.ErrUnauthorized) {
		t.Errorf("a token that is not configured: %v", err)
	}
	info, err := (&aiapi.Client{BaseURL: url, Token: "optimizer-secret"}).Info(t.Context())
	if err != nil || !info.Configured {
		t.Fatalf("info = %+v, %v", info, err)
	}
	listed := map[aiapi.Profile]bool{}
	for _, p := range info.Profiles {
		listed[p.Name] = true
	}
	if !listed[aiapi.ProfileReview] || !listed[aiapi.ProfileReport] || len(info.Profiles) != 7 {
		t.Errorf("profiles offered = %+v", info.Profiles)
	}
	for _, secret := range tokens {
		if strings.Contains(logs.String(), secret.Reveal()) {
			t.Error("the logs contain a caller's token")
		}
	}
}
