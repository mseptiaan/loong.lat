package ai

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform/platformtest"
)

// TestLive asks the Copilot and the analytics summary real questions over seeded data, through the real AI gateway
// (services/ai, built and run as a child process: a service may not import another service's code) against Meta's
// Model API. It is skipped unless LOONG_LIVE_META=1 and costs a handful of calls:
//
//	cd backend && set -a && . ./.env && set +a && LOONG_LIVE_META=1 \
//	  TEST_DATABASE_URL=... go test -count=1 -run Live -v ./services/backoffice/internal/ai
//
// The key comes from META_API_KEY in the environment (the child inherits it). Nothing about it is printed or
// logged, and the child's own output is discarded: the test reports only what passed, and the latencies.
func TestLive(t *testing.T) {
	if os.Getenv("LOONG_LIVE_META") != "1" {
		t.Skip("set LOONG_LIVE_META=1 (and META_API_KEY, TEST_DATABASE_URL) to call the real Model API")
	}
	if os.Getenv("META_API_KEY") == "" {
		t.Fatal("LOONG_LIVE_META=1 needs META_API_KEY in the environment")
	}
	if os.Getenv(platformtest.EnvDatabaseURL) == "" {
		t.Fatal("LOONG_LIVE_META=1 needs TEST_DATABASE_URL: the questions are asked over seeded data")
	}

	gateway := startGateway(t)
	r := newRig(t)
	r.seed()
	rec := &recorder{inner: gateway}
	h := serve(t, r.service(rec))
	admin := h.token(contracts.RoleAdmin, "adm-1")

	for rep := range liveReps() {
		t.Run(fmt.Sprintf("copilot_%d", rep+1), func(t *testing.T) {
			rec.reset()
			started := time.Now()
			rp := h.call(admin, "ai.copilot", askBody("Which plans need a driver right now, and who is the best driver for each? Should I assign them?"), nil)
			t.Logf("copilot: latency %v, %d tool lines, %d answer chars, %d model calls", time.Since(started).Round(time.Millisecond), len(rp.traces()), len(rp.text()), rec.calls())

			if rp.status != 200 || rp.done() == nil {
				t.Fatalf("the answer did not end with done: status %d, events %v", rp.status, eventTypes(rp))
			}
			tools := map[string]bool{}
			for _, tr := range rp.traces() {
				if tr["status"] == "failed" {
					t.Errorf("a tool line failed: %v", tr)
				}
				if tr["status"] == "done" {
					tools[tr["title"].(string)] = true
				}
			}
			if len(tools) == 0 {
				t.Error("the model answered without reading anything")
			}
			text := rp.text()
			if !strings.Contains(text, "PL-2001") {
				t.Errorf("the answer does not mention the plan that waits longest, PL-2001:\n%s", text)
			}
			if bad := ungrounded(text, rec.toolResults()); len(bad) > 0 {
				t.Errorf("numbers in the answer that no tool returned: %v\n%s", bad, text)
			}
			for _, a := range rp.actions() {
				if a["planId"] == "" || (a["kind"] != actionAssignBest && a["kind"] != actionOpenPlan) {
					t.Errorf("odd action %v", a)
				}
			}
			quotes, bold := style(text)
			t.Logf("copilot answered %d chars after reading %d things, with %d proposed actions; %d model calls, tools per turn %v, %d quotation marks, %d bold spans, quoted figures %v",
				len(text), len(tools), len(rp.actions()), rec.calls(), rec.turns(), quotes, bold, quotedFigure.FindAllString(text, -1))
			t.Logf("answer:\n%s", text)
			if quotedFigure.MatchString(text) {
				t.Errorf("a figure is in quotation marks")
			}
			if words := numberWords.FindAllString(text, -1); len(words) > 0 {
				t.Errorf("numbers written as words (they escape the grounding check): %v", words)
			}
		})
	}

	for rep := range liveReps() {
		t.Run(fmt.Sprintf("insights_%d", rep+1), func(t *testing.T) {
			rec.reset()
			started := time.Now()
			rp := h.call(admin, "ai.insights", struct{}{}, nil)
			t.Logf("insights: latency %v, %d answer chars", time.Since(started).Round(time.Millisecond), len(rp.text()))

			if rp.status != 200 || rp.done() == nil {
				t.Fatalf("the answer did not end with done: status %d, events %v", rp.status, eventTypes(rp))
			}
			text := rp.text()
			if strings.TrimSpace(text) == "" {
				t.Fatal("the narration is empty")
			}
			sent := rec.userText()
			if bad := ungrounded(text, []string{sent}); len(bad) > 0 {
				t.Errorf("numbers in the narration that are not among the figures: %v\n%s", bad, text)
			}
			if lower := strings.ToLower(text); strings.Contains(lower, "savings") || strings.Contains(lower, "hemat") {
				t.Errorf("the narration talks about savings, which are not available:\n%s", text)
			}
			quotes, bold := style(text)
			t.Logf("insights narration has %d bullet lines, %d quotation marks, %d bold spans, quoted figures %v",
				strings.Count(text, "\n- ")+strings.Count(text, "\n* "), quotes, bold, quotedFigure.FindAllString(text, -1))
			t.Logf("narration:\n%s", text)
			if quotedFigure.MatchString(text) {
				t.Errorf("a figure is in quotation marks")
			}
			if words := numberWords.FindAllString(text, -1); len(words) > 0 {
				t.Errorf("numbers written as words (they escape the grounding check): %v", words)
			}
		})
	}

	// more asks a further question of the Copilot and holds the answer to what the prompts promise: it ends in done,
	// no tool failed, every number is one a tool returned (written as digits), and no figure is in quotation marks.
	more := func(t *testing.T, body map[string]any) (text string, rp reply) {
		t.Helper()
		rec.reset()
		started := time.Now()
		rp = h.call(admin, "ai.copilot", body, nil)
		if rp.status != 200 || rp.done() == nil {
			t.Fatalf("the answer did not end with done: status %d, events %v", rp.status, eventTypes(rp))
		}
		text = rp.text()
		for _, tr := range rp.traces() {
			if tr["status"] == "failed" {
				t.Errorf("a tool line failed: %v", tr)
			}
		}
		quotes, bold := style(text)
		t.Logf("latency %v, %d model calls, tools per turn %v, %d characters, %d quotation marks, %d bold spans",
			time.Since(started).Round(time.Millisecond), rec.calls(), rec.turns(), len(text), quotes, bold)
		t.Logf("answer:\n%s", text)
		if bad := ungrounded(text, rec.toolResults()); len(bad) > 0 {
			t.Errorf("numbers in the answer that no tool returned: %v", bad)
		}
		if words := numberWords.FindAllString(text, -1); len(words) > 0 {
			t.Errorf("numbers written as words (they escape the grounding check): %v", words)
		}
		if quotedFigure.MatchString(text) {
			t.Errorf("a figure is in quotation marks")
		}
		return text, rp
	}
	for rep := range liveReps() {
		t.Run(fmt.Sprintf("copilot_today_%d", rep+1), func(t *testing.T) {
			text, _ := more(t, askBody("How is today going, and what is the most urgent thing to deal with?"))
			if !strings.Contains(text, "PL-200") {
				t.Errorf("the answer should name a plan that needs attention")
			}
		})
		t.Run(fmt.Sprintf("copilot_plan_scope_%d", rep+1), func(t *testing.T) {
			text, rp := more(t, map[string]any{"scope": map[string]any{"kind": "plan", "planId": "PL-2001"}, "question": "Who is the best driver for this plan, and should I assign them?"})
			if !strings.Contains(text, "Budi") {
				t.Errorf("the best driver for PL-2001 is Budi")
			}
			if len(rp.actions()) == 0 {
				t.Errorf("the answer should offer the assignment")
			}
		})
	}
}

// style counts what the prompts ask the model not to do: quotation marks, and bold beyond a handful of figures.
func style(text string) (quotes, bold int) {
	return strings.Count(text, `"`) + strings.Count(text, "“") + strings.Count(text, "”"), strings.Count(text, "**") / 2
}

// quotedFigure matches a figure inside quotation marks: "3 min", “Rp 46,500”.
// numberWords finds numbers written as words, which a check of the digits in an answer cannot see. The prompts ask for
// digits, so any of these is a figure that slipped past the grounding check.
var numberWords = regexp.MustCompile(`(?i)\b(two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|twenty|thirty|fifty|hundred|thousand|million|half|twice|dua|tiga|empat|lima|enam|tujuh|delapan|sembilan|sepuluh|sebelas|setengah|ratus|ribu|juta)\b`)

var quotedFigure = regexp.MustCompile(`["“'‘]\s*(?:Rp\s*)?\d`)

// liveReps is how many times each question is asked (LOONG_LIVE_REPS, default 1), to see the spread of the latency.
func liveReps() int {
	if n, err := strconv.Atoi(os.Getenv("LOONG_LIVE_REPS")); err == nil && n > 0 {
		return n
	}
	return 1
}

// turns is how many tools the model asked for in each of its turns in the longest conversation so far: [4 1] is
// four tools in one step and then one more.
func (r *recorder) turns() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	var best []int
	for _, req := range r.reqs {
		var turns []int
		for _, m := range req.Messages {
			if m.Role == aiapi.RoleAssistant && len(m.ToolCalls) > 0 {
				turns = append(turns, len(m.ToolCalls))
			}
		}
		if len(turns) > len(best) {
			best = turns
		}
	}
	return best
}

// recorder wraps the model and keeps what it was sent: the tool results the Copilot's model saw, and the figures the
// narration was given.
type recorder struct {
	inner aiapi.Model

	mu   sync.Mutex
	reqs []aiapi.ChatRequest
}

func (r *recorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = nil
}

func (r *recorder) note(req aiapi.ChatRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	req.Messages = append([]aiapi.Message(nil), req.Messages...)
	r.reqs = append(r.reqs, req)
}

func (r *recorder) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.reqs)
}

func (r *recorder) Chat(ctx context.Context, req aiapi.ChatRequest) (aiapi.ChatResponse, error) {
	r.note(req)
	return r.inner.Chat(ctx, req)
}

func (r *recorder) ChatStream(ctx context.Context, req aiapi.ChatRequest, fn func(aiapi.Delta) error) (aiapi.ChatResponse, error) {
	r.note(req)
	return r.inner.ChatStream(ctx, req, fn)
}

// toolResults are the tool messages of the longest conversation, which holds all of them.
func (r *recorder) toolResults() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, req := range r.reqs {
		var results []string
		for _, m := range req.Messages {
			if m.Role == aiapi.RoleTool {
				results = append(results, m.Content)
			}
		}
		if len(results) > len(out) {
			out = results
		}
	}
	return out
}

// userText is the user message of the last request.
func (r *recorder) userText() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.reqs) == 0 {
		return ""
	}
	msgs := r.reqs[len(r.reqs)-1].Messages
	return msgs[len(msgs)-1].Content
}

var numberRE = regexp.MustCompile(`\d+(?:[.,]\d+)*`)

// forms are the ways a number token may be written: as is, without grouping or decimal separators, and with a
// decimal point (1.250.000 and 1,250,000 are 1250000; 92,3 is 92.3).
func forms(tok string) []string {
	plain := strings.NewReplacer(".", "", ",", "").Replace(tok)
	out := []string{tok, plain, strings.ReplaceAll(tok, ",", ".")}
	if whole, frac, ok := strings.Cut(strings.ReplaceAll(tok, ",", "."), "."); ok {
		out = append(out, whole, frac)
	}
	return out
}

// ungrounded returns the numbers of the answer that none of the sources has. The numbering of a list is not a
// figure, and neither is a digit of a plan code that a source spells out (those are in the sources anyway).
func ungrounded(answer string, sources []string) []string {
	known := map[string]bool{}
	for _, src := range sources {
		for _, tok := range numberRE.FindAllString(src, -1) {
			for _, f := range forms(tok) {
				known[f] = true
			}
		}
	}
	var bad []string
	for _, line := range strings.Split(answer, "\n") {
		line = regexp.MustCompile(`^\s*(?:[-*]\s+)?\d+[.)]\s+`).ReplaceAllString(line, "")
		for _, tok := range numberRE.FindAllString(line, -1) {
			ok := false
			for _, f := range forms(tok) {
				ok = ok || known[f]
			}
			if !ok {
				bad = append(bad, tok)
			}
		}
	}
	return bad
}

// startGateway builds services/ai, runs it on a free port with a token of its own, waits until it reports a Meta
// key, and returns a client for it. The child's output goes nowhere.
func startGateway(t *testing.T) *aiapi.Client {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..", "..", "..") // backend/
	bin := filepath.Join(t.TempDir(), "ai")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", bin, "./services/ai/cmd/ai")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the gateway: %v\n%s", err, out)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	token := hex.EncodeToString(raw[:])

	cmd := exec.CommandContext(t.Context(), bin)
	cmd.Dir = t.TempDir() // no .env here: the environment is what the shell loaded
	cmd.Env = append(os.Environ(), "AI_ADDR="+addr, "AI_TOKEN="+token, "AI_DEV=false", "LOG_LEVEL=error")
	// LOONG_LIVE_GATEWAY_ENV="AI_COPILOT_EFFORT=minimal AI_INSIGHTS_EFFORT=minimal" runs the same questions against a
	// gateway with other settings, to compare them.
	cmd.Env = append(cmd.Env, strings.Fields(os.Getenv("LOONG_LIVE_GATEWAY_ENV"))...)
	cmd.Stdout, cmd.Stderr = nil, nil // discarded
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the gateway: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	client := &aiapi.Client{BaseURL: "http://" + addr, Token: token}
	deadline := time.Now().Add(20 * time.Second)
	for {
		info, err := client.Info(t.Context())
		if err == nil {
			if !info.Configured {
				t.Fatal("the gateway runs without a Meta key: load backend/.env into the environment")
			}
			return client
		}
		if time.Now().After(deadline) {
			t.Fatalf("the gateway did not come up: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
