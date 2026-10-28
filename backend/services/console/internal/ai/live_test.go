package ai

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/testkit"
)

// gatewayToken is the bearer secret between the live test and the gateway it starts; it protects nothing.
const gatewayToken = "live-test-gateway-token"

// startGateway builds the real AI gateway and runs it as a process on a free local port, against Meta. It is a
// process and not an import because a service may not import another (boundaries_test.go). The Meta key is
// inherited from the environment and never printed: the gateway does not log it, and neither does this test.
func startGateway(t *testing.T) *aiapi.Client {
	t.Helper()
	if os.Getenv("META_API_KEY") == "" {
		t.Fatal("LOONG_LIVE_META=1 needs META_API_KEY in the environment")
	}
	bin := filepath.Join(t.TempDir(), "ai-gateway")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", bin, "./services/ai/cmd/ai")
	build.Dir = "../../../.." // the module root: services/console/internal/ai -> backend
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the gateway: %v\n%s", err, out)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	ctx, cancel := context.WithCancel(t.Context())
	cmd := exec.CommandContext(ctx, bin)
	cmd.Dir = t.TempDir() // no .env to pick up
	cmd.Env = append(os.Environ(), "AI_ADDR="+addr, "AI_TOKEN="+gatewayToken, "AI_DEV=", "LOG_LEVEL=info")
	// LOONG_LIVE_GATEWAY_ENV="AI_COPILOT_EFFORT=minimal AI_INSIGHTS_EFFORT=minimal" runs the same questions against a
	// gateway with other settings, to compare them.
	cmd.Env = append(cmd.Env, strings.Fields(os.Getenv("LOONG_LIVE_GATEWAY_ENV"))...)
	var logs syncBuffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
	})
	url := "http://" + addr
	for deadline := time.Now().Add(15 * time.Second); ; {
		if resp, err := http.Get(url + "/readyz"); err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the gateway did not start:\n%s", logs.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	client := &aiapi.Client{BaseURL: url, Token: gatewayToken}
	if info, err := client.Info(t.Context()); err != nil || !info.Configured {
		t.Fatalf("the gateway reports no Meta key: %+v, %v", info, err)
	}
	return client
}

// recorder passes calls to the real gateway and keeps what the model was sent, so that the test can check the
// answer against the very tool results the model saw.
type recorder struct {
	aiapi.Gateway
	mu       sync.Mutex
	requests []aiapi.ChatRequest
}

func (r *recorder) record(req aiapi.ChatRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	req.Messages = slices.Clone(req.Messages)
	r.requests = append(r.requests, req)
}

func (r *recorder) Chat(ctx context.Context, req aiapi.ChatRequest) (aiapi.ChatResponse, error) {
	r.record(req)
	return r.Gateway.Chat(ctx, req)
}

func (r *recorder) ChatStream(ctx context.Context, req aiapi.ChatRequest, fn func(aiapi.Delta) error) (aiapi.ChatResponse, error) {
	r.record(req)
	return r.Gateway.ChatStream(ctx, req, fn)
}

// turns is how many tools the model asked for in each of its turns in the longest conversation so far: [4 1] is
// four tools in one step and then one more.
func (r *recorder) turns() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	var best []int
	for _, req := range r.requests {
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

func (r *recorder) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

// last is the most recent request the model was sent.
func (r *recorder) last() aiapi.ChatRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.requests[len(r.requests)-1]
}

func (r *recorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = nil
}

// style counts what the Copilot's prompt asks it not to do: quotation marks, and bold beyond a handful of figures.
func style(text string) (quotes, bold int) {
	return strings.Count(text, `"`) + strings.Count(text, "“") + strings.Count(text, "”"), strings.Count(text, "**") / 2
}

// quotedFigure matches a figure inside quotation marks: "3 min", “Rp 46,500”.
var quotedFigure = regexp.MustCompile(`["“'‘]\s*(?:Rp\s*)?\d`)

// numberWords finds numbers written as words, which a check of the digits in an answer cannot see. The prompts ask for
// digits, so any of these is a figure that slipped past the grounding check.
var numberWords = regexp.MustCompile(`(?i)\b(two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|twenty|thirty|fifty|hundred|thousand|million|half|twice|dua|tiga|empat|lima|enam|tujuh|delapan|sembilan|sepuluh|sebelas|setengah|ratus|ribu|juta)\b`)

// liveReps is how many times each question is asked (LOONG_LIVE_REPS, default 1), to see the spread of the latency.
func liveReps() int {
	if n, err := strconv.Atoi(os.Getenv("LOONG_LIVE_REPS")); err == nil && n > 0 {
		return n
	}
	return 1
}

// toolResults is the text of every tool result the model was sent so far.
func (r *recorder) toolResults() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b strings.Builder
	for _, req := range r.requests {
		for _, m := range req.Messages {
			if m.Role == aiapi.RoleTool {
				b.WriteString(m.Content + "\n")
			}
		}
	}
	return b.String()
}

// boxPNG draws a cardboard box on a plain background: enough picture for the model to have something to say.
func boxPNG() []byte {
	const w, h = 320, 240
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	fill := func(r image.Rectangle, c color.Color) {
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				img.Set(x, y, c)
			}
		}
	}
	fill(img.Bounds(), color.RGBA{238, 238, 238, 255})                  // floor
	fill(image.Rect(60, 70, 260, 200), color.RGBA{176, 124, 72, 255})   // front of the box
	fill(image.Rect(60, 70, 260, 90), color.RGBA{204, 150, 94, 255})    // lid
	fill(image.Rect(150, 70, 170, 200), color.RGBA{222, 205, 160, 255}) // tape
	fill(image.Rect(60, 198, 260, 202), color.RGBA{120, 80, 44, 255})   // shadow edge
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

var numberRE = regexp.MustCompile(`\d[\d.,]*`)

// digits keeps only the digits of a number as written: "Rp 46,500" and "46.500" both become 46500.
func digits(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}

// TestLive runs the AI features against the real gateway and Meta. It is skipped unless LOONG_LIVE_META=1:
//
//	cd backend && set -a && . ./.env && set +a && export TEST_DATABASE_URL=... \
//	  && LOONG_LIVE_META=1 go test -count=1 -run Live -v ./services/console/internal/ai
//
// Only latencies and pass/fail are logged, never what was asked or answered.
func TestLive(t *testing.T) {
	if os.Getenv("LOONG_LIVE_META") != "1" {
		t.Skip("set LOONG_LIVE_META=1 (and META_API_KEY) to call the real Meta API through the real gateway")
	}
	gateway := &recorder{Gateway: startGateway(t)}
	r := newRig(t, func(o *rigOptions) {
		o.gateway = gateway
		o.clock = time.Now
		o.burst = 50
	})

	for rep := range liveReps() {
		t.Run(fmt.Sprintf("copilot_cheapest_option_%d", rep+1), func(t *testing.T) {
			gateway.reset()
			customer := r.customer("Dina")
			plan := r.seedPlan(customer) // the balanced option (Rp 54,000) is chosen; the cheapest costs Rp 46,500
			start := time.Now()
			res := r.call(customer, "en", "ai.copilot", askBody(plan.ID,
				"Which route option is the cheapest, how much would I save by switching to it from the one I chose, and can you suggest switching?"))
			t.Logf("latency %v, %d events", time.Since(start).Round(time.Millisecond), len(res.Events))
			if res.Status != 200 {
				t.Fatalf("status %d: %s", res.Status, res.Raw)
			}
			var answer strings.Builder
			var tools []string
			for _, ev := range res.Events {
				switch ev["type"] {
				case "token":
					answer.WriteString(ev["text"].(string))
				case "tool":
					tr := ev["trace"].(map[string]any)
					if tr["status"] == "done" {
						tools = append(tools, tr["title"].(string))
					}
					if tr["status"] == "failed" {
						t.Errorf("a tool failed: %v", tr["title"])
					}
				case "error":
					t.Fatalf("the stream ended in an error: %v", ev["error"])
				}
			}
			quotes, bold := style(answer.String())
			t.Logf("tools used: %v; %d model calls, tools per turn %v; answer is %d characters, %d quotation marks, %d bold spans, quoted figures %v",
				tools, gateway.calls(), gateway.turns(), answer.Len(), quotes, bold, quotedFigure.FindAllString(answer.String(), -1))
			t.Logf("answer:\n%s", answer.String())
			if len(tools) == 0 || answer.Len() == 0 {
				t.Fatalf("the Copilot used %d tools and wrote %d characters", len(tools), answer.Len())
			}

			// No invented numbers: every number in the answer is one of the numbers in the tool results the model was sent.
			known := map[string]bool{}
			results := gateway.toolResults()
			for _, n := range numberRE.FindAllString(results, -1) {
				known[digits(n)] = true
			}
			for _, n := range numberRE.FindAllString(answer.String(), -1) {
				if d := digits(n); d != "" && !known[d] {
					t.Errorf("the answer contains the number %q, which no tool returned", n)
				}
			}
			if results == "" {
				t.Error("the model was sent no tool results")
			}
			text := answer.String()
			if !strings.Contains(text, "46,500") {
				t.Errorf("the answer should name the cheapest cost (Rp 46,500)")
			}
			if !strings.Contains(text, "7,500") {
				t.Errorf("the answer should say the saving (Rp 7,500)")
			}

			done := res.event(t, -1)
			if done["type"] != "done" || !strings.HasPrefix(done["requestId"].(string), "req-") {
				t.Fatalf("last event = %v", done)
			}
			actions := done["actions"].([]any)
			if len(actions) != 1 {
				t.Fatalf("actions = %v", actions)
			}
			if a := actions[0].(map[string]any); a["kind"] != "pilih_opsi" || a["optionId"] != optCheap || a["planId"] != plan.ID {
				t.Errorf("done carries the wrong action: %v", a)
			}
			if quotedFigure.MatchString(text) {
				t.Errorf("a figure is in quotation marks")
			}
		})
	}

	// ask puts a question to the Copilot about a plan and holds the answer to what the prompts promise: it ends in done,
	// no tool failed, every number is one a tool returned (and written as digits), and no figure is in quotation marks.
	ask := func(t *testing.T, customer, planID, question string) (text string, done map[string]any) {
		t.Helper()
		gateway.reset()
		start := time.Now()
		res := r.call(customer, "en", "ai.copilot", askBody(planID, question))
		if res.Status != 200 {
			t.Fatalf("status %d: %s", res.Status, res.Raw)
		}
		var answer strings.Builder
		for _, ev := range res.Events {
			switch ev["type"] {
			case "token":
				answer.WriteString(ev["text"].(string))
			case "tool":
				if tr := ev["trace"].(map[string]any); tr["status"] == "failed" {
					t.Errorf("a tool failed: %v", tr["title"])
				}
			case "error":
				t.Fatalf("the stream ended in an error: %v", ev["error"])
			}
		}
		text = answer.String()
		quotes, bold := style(text)
		t.Logf("latency %v, %d model calls, tools per turn %v, %d characters, %d quotation marks, %d bold spans",
			time.Since(start).Round(time.Millisecond), gateway.calls(), gateway.turns(), len(text), quotes, bold)
		t.Logf("answer:\n%s", text)
		known := map[string]bool{}
		for _, n := range numberRE.FindAllString(gateway.toolResults(), -1) {
			known[digits(n)] = true
		}
		for _, n := range numberRE.FindAllString(text, -1) {
			if d := digits(n); d != "" && !known[d] {
				t.Errorf("the answer contains the number %q, which no tool returned", n)
			}
		}
		if words := numberWords.FindAllString(text, -1); len(words) > 0 {
			t.Errorf("numbers written as words (they escape the grounding check): %v", words)
		}
		if quotedFigure.MatchString(text) {
			t.Errorf("a figure is in quotation marks")
		}
		done = res.event(t, -1)
		if done["type"] != "done" || len(text) == 0 {
			t.Fatalf("the answer did not end in done: %v", done)
		}
		return text, done
	}

	for rep := range liveReps() {
		t.Run(fmt.Sprintf("copilot_compare_all_%d", rep+1), func(t *testing.T) {
			customer := r.customer("Gita")
			plan := r.seedPlan(customer)
			text, done := ask(t, customer, plan.ID, "Compare all three options for me.")
			for _, cost := range []string{"46,500", "54,000", "68,000"} {
				if !strings.Contains(text, cost) {
					t.Errorf("the comparison lacks the cost %s", cost)
				}
			}
			if actions := done["actions"].([]any); len(actions) != 0 {
				t.Errorf("a comparison proposes nothing, got %v", actions)
			}
		})

		t.Run(fmt.Sprintf("copilot_afford_fastest_%d", rep+1), func(t *testing.T) {
			customer := r.customer("Hadi")
			testkit.Fund(t, r.pool, customer, 50000)
			plan := r.seedPlan(customer) // the fastest option costs Rp 68,000
			text, _ := ask(t, customer, plan.ID, "Do I have enough in my wallet to pay for the fastest option?")
			if !strings.Contains(text, "18,000") {
				t.Errorf("the answer should say the shortfall (Rp 18,000)")
			}
			if lower := strings.ToLower(text); strings.Contains(lower, "you have enough") || strings.Contains(lower, "yes,") {
				t.Errorf("the wallet is Rp 18,000 short, but the answer says it is enough")
			}
		})

		t.Run(fmt.Sprintf("copilot_where_is_it_%d", rep+1), func(t *testing.T) {
			customer := r.customer("Indah")
			testkit.ProjectVehicle(t, r.pool, testkit.Vehicle(fmt.Sprintf("veh-live%d", rep), contracts.VehicleTypeMotor))
			plan := r.seedPlan(customer, confirmed)
			text, _ := ask(t, customer, plan.ID, "Where is my delivery, and is it running late?")
			if !strings.Contains(text, "7 min") || !strings.Contains(text, "Senayan City") {
				t.Errorf("the answer should give the delay (7 min) and the next stop (Senayan City)")
			}
		})
	}

	t.Run("parse_intent", func(t *testing.T) {
		start := time.Now()
		res := r.call(r.customer("Eko"), "en", "ai.parseIntent", map[string]any{"text": "cheapest please, but it has to arrive before 5 pm"})
		t.Logf("latency %v", time.Since(start).Round(time.Millisecond))
		chips := chipsOf(t, res)
		var priority, deadline map[string]any
		for _, c := range chips {
			switch c["kind"] {
			case "prioritas":
				priority = c
			case "batas_waktu":
				deadline = c
			}
		}
		if priority == nil || priority["preset"] != "termurah" || priority["label"] != "Priority: Cheapest" {
			t.Errorf("priority chip = %v (chips %v)", priority, chips)
		}
		if deadline == nil {
			t.Fatalf("no deadline chip in %v", chips)
		}
		at, err := time.Parse(time.RFC3339, deadline["deadline"].(string))
		if err != nil || at.In(wib).Format("15:04") != "17:00" || !at.After(time.Now()) || at.After(time.Now().Add(25*time.Hour)) || deadline["label"] != "Arrive before 17:00" {
			t.Errorf("deadline chip = %v (parsed %v, %v)", deadline, at.In(wib), err)
		}
	})

	t.Run("read_image", func(t *testing.T) {
		start := time.Now()
		res := r.call(r.customer("Fajar"), "en", "ai.readImage", imageBody(boxPNG(), "image/png"))
		t.Logf("latency %v, events %v", time.Since(start).Round(time.Millisecond), res.types())
		if res.Status != 200 {
			t.Fatalf("status %d: %s", res.Status, res.Raw)
		}
		if got := strings.Join(res.types(), ","); got != "progress,progress,progress,progress,done" {
			t.Fatalf("events = %s", got)
		}
		done := res.event(t, -1)
		if done["summary"] == "" {
			t.Error("no summary")
		}
		items := done["items"].([]any)
		t.Logf("%d items detected", len(items))
		for _, raw := range items {
			it := raw.(map[string]any)
			qty, weight, conf := it["qty"].(float64), it["weightKg"].(float64), it["confidence"].(float64)
			if qty < 1 || qty > 99 || weight < 0.05 || weight > 500 || conf < 0 || conf > 0.99 || it["name"] == "" {
				t.Errorf("item out of bounds: %v", it)
			}
		}
	})
}

// internalTerms are words of the engine's own that the report must not use: the prompt asks for "attempts"
// and "review rounds" instead.
var internalTerms = regexp.MustCompile(`(?i)\b(presets?|clusters?|clustering|solver|trials?|k-?means|k-?medoids|seeds?|warm-?start)\b`)

// TestLiveExplainOptions asks the real model, through the real gateway, to explain a reviewed option set, in both
// languages, and prints what it wrote. It is skipped unless LOONG_LIVE_META=1 (see TestLive for how to run it):
//
//	cd backend && set -a && . ./.env && set +a && export TEST_DATABASE_URL=... \
//	  && LOONG_LIVE_META=1 go test -count=1 -run LiveExplainOptions -v ./services/console/internal/ai
//
// LOONG_LIVE_GATEWAY_ENV="AI_REPORT_EFFORT=low" runs it against a gateway with other settings, and LOONG_LIVE_REPS=3
// asks three times to see the spread. The server has already refused any text with a number that is not in its facts;
// the test checks that again from the outside, with the facts the model was really sent, and holds the text to the
// rest of what the prompt promises. Only the report and latencies are logged, never anything else of the customer's.
func TestLiveExplainOptions(t *testing.T) {
	if os.Getenv("LOONG_LIVE_META") != "1" {
		t.Skip("set LOONG_LIVE_META=1 (and META_API_KEY) to call the real Meta API through the real gateway")
	}
	gateway := &recorder{Gateway: startGateway(t)}
	r := newRig(t, func(o *rigOptions) {
		o.gateway = gateway
		o.clock = time.Now
		o.burst = 50
	})
	for _, lang := range []string{"en", "id"} {
		for rep := range liveReps() {
			t.Run(fmt.Sprintf("explain_options_%s_%d", lang, rep+1), func(t *testing.T) {
				gateway.reset()
				customer := r.customer("Dina")
				plan := r.seedPlan(customer, withMethod)
				start := time.Now()
				res := r.call(customer, lang, "ai.explainOptions", explainBodyFor(plan.ID))
				t.Logf("latency %v, %d model calls", time.Since(start).Round(time.Millisecond), gateway.calls())
				if res.Status != 200 {
					t.Fatalf("status %d: %s", res.Status, res.Raw)
				}
				if got := strings.Join(res.types(), ","); got != "token,done" {
					t.Fatalf("events = %s", got)
				}
				text, _ := res.event(t, 0)["text"].(string)
				t.Logf("report (%d words):\n%s", len(strings.Fields(text)), text)

				// The numbers: every one is in the facts the model was sent (the last request is the one that was kept).
				facts := gateway.last().Messages[1].Content
				if invented := ungroundedFigures(text, facts); len(invented) > 0 {
					t.Errorf("the report contains numbers that are not in the facts: %v", invented)
				}
				if words := numberWords.FindAllString(text, -1); len(words) > 0 {
					t.Errorf("numbers written as words (they escape the grounding check): %v", words)
				}
				if quotedFigure.MatchString(text) {
					t.Errorf("a figure is in quotation marks")
				}

				// The shape the prompt asks for.
				if n := len(strings.Fields(text)); n == 0 || n > 230 {
					t.Errorf("the report has %d words; the prompt asks for at most 180", n)
				}
				if terms := internalTerms.FindAllString(text, -1); len(terms) > 0 {
					t.Errorf("internal terms in the report: %v", terms)
				}
				if bold := strings.Count(text, "**"); bold > 0 {
					t.Errorf("%d bold markers; the prompt asks for none", bold)
				}

				report := r.storedReport(plan.ID)
				if report == nil || report.Text != text || report.Locale != lang {
					t.Errorf("stored report = %+v", report)
				}
				done := res.event(t, 1)
				if done["type"] != "done" || !strings.HasPrefix(done["requestId"].(string), "req-") {
					t.Errorf("last event = %v", done)
				}
			})
		}
	}
}

// TestLiveAnalyzeProof asks the real model, through the real gateway, what a delivery photo shows, and prints the
// analysis the dashboard would show. It needs a photo of your own, given by path, so it is skipped without
// LOONG_LIVE_PHOTO as well as without LOONG_LIVE_META=1 (see TestLive for how to run it):
//
//	cd backend && set -a && . ./.env && set +a \
//	  && LOONG_LIVE_META=1 LOONG_LIVE_PHOTO=/path/to/handover.jpg go test -count=1 -run LiveAnalyzeProof -v ./services/console/internal/ai
//
// LOONG_LIVE_PARCELS="2 x Medium box,1 x Lamp" says what the photo should show (comma separated; empty by default),
// and LOONG_LIVE_REPS=3 asks three times to see the spread. No database is needed. The photo itself is never logged;
// what the model said about it is, which is the point of the test.
func TestLiveAnalyzeProof(t *testing.T) {
	if os.Getenv("LOONG_LIVE_META") != "1" {
		t.Skip("set LOONG_LIVE_META=1 (and META_API_KEY) to call the real Meta API through the real gateway")
	}
	path := os.Getenv("LOONG_LIVE_PHOTO")
	if path == "" {
		t.Skip("set LOONG_LIVE_PHOTO to the path of a JPEG, PNG or WebP photo to analyse")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mime, ok := ProofImageType(http.DetectContentType(data))
	if !ok {
		t.Fatalf("%s is %s: the analysis reads JPEG, PNG and WebP", filepath.Base(path), http.DetectContentType(data))
	}
	var parcels []string
	for _, p := range strings.Split(os.Getenv("LOONG_LIVE_PARCELS"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			parcels = append(parcels, p)
		}
	}

	gateway := &recorder{Gateway: startGateway(t)}
	svc := New(Options{Gateway: gateway, Now: time.Now})
	for rep := range liveReps() {
		t.Run(fmt.Sprintf("analyze_proof_%d", rep+1), func(t *testing.T) {
			gateway.reset()
			start := time.Now()
			got, err := svc.AnalyzeProof(t.Context(), ProofInput{MIME: mime, Data: data, Parcels: parcels})
			t.Logf("latency %v, %s of %d bytes, %d expected items", time.Since(start).Round(time.Millisecond), mime, len(data), len(parcels))
			if err != nil {
				t.Fatalf("AnalyzeProof: %v", err)
			}

			en, id := shownText(t, got.Summary)
			t.Logf("model %s, confidence %.2f\nsummary (en): %s\nsummary (id): %s", got.Model, got.Confidence, en, id)
			if en == "" || id == "" || got.Model == "" || got.Confidence < 0 || got.Confidence > 1 {
				t.Errorf("the analysis is incomplete: %+v", got)
			}
			if len(got.Checks) != len(proofChecks) {
				t.Fatalf("%d checks", len(got.Checks))
			}
			for i, c := range got.Checks {
				if c.Key != proofChecks[i] {
					t.Errorf("check %d is %s, want %s", i, c.Key, proofChecks[i])
				}
				note := "(no note: " + c.Note.Key + ")"
				if c.Note.Key == msgProofText {
					noteEN, noteID := shownText(t, c.Note)
					note = noteEN + " / " + noteID
				}
				t.Logf("%-9s %-8s %s", c.Key, c.Result, note)
			}

			req := gateway.last()
			if req.Profile != aiapi.ProfileVision || req.JSONSchema == nil || len(req.Messages) != 2 || len(req.Messages[1].Images) != 1 {
				t.Errorf("the request was %+v", req)
			}
		})
	}
}
