package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/routeplan"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/testkit"
)

var deliveryPhoto = []byte("\xff\xd8\xff\xe0 the photo the driver took")

func deliveryPhotoURL() string {
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(deliveryPhoto)
}

// proofGateway is an AI gateway that answers every photo with the same analysis and keeps what it was asked.
type proofGateway struct {
	*httptest.Server
	mu       sync.Mutex
	requests []aiapi.ChatRequest
	// bad are the calls that so far get a gateway error instead of an answer.
	bad int
}

func newProofGateway(t *testing.T) *proofGateway {
	t.Helper()
	g := &proofGateway{}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req aiapi.ChatRequest
		if r.URL.Path != aiapi.ChatPath || r.Header.Get("Authorization") != "Bearer gateway-token" || json.UnmarshalRead(r.Body, &req) != nil {
			http.Error(w, "unexpected call", http.StatusBadRequest)
			return
		}
		g.mu.Lock()
		g.requests = append(g.requests, req)
		fail := g.bad > 0
		if fail {
			g.bad--
		}
		g.mu.Unlock()
		if fail {
			http.Error(w, "the model is having a moment", http.StatusServiceUnavailable)
			return
		}
		text := func(en, id string) map[string]string { return map[string]string{"en": en, "id": id} }
		answer, _ := json.Marshal(map[string]any{
			"summary": text("A courier hands a brown box to a person at a gate.", "Kurir menyerahkan kotak cokelat kepada seseorang di gerbang."),
			"checks": []any{
				map[string]any{"key": "recipient", "result": "ok", "note": text("A person appears to receive the box.", "Seseorang tampak menerima kotak.")},
				map[string]any{"key": "parcel", "result": "ok", "note": text("A box is visible.", "Sebuah kotak terlihat.")},
				map[string]any{"key": "quality", "result": "unclear", "note": text("The photo is a little dark.", "Foto agak gelap.")},
			},
			"confidence": 0.84,
		})
		body, _ := json.Marshal(aiapi.ChatResponse{Message: aiapi.Message{Role: aiapi.RoleAssistant, Content: string(answer)}, FinishReason: aiapi.FinishStop, Model: "muse-test"})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(g.Close)
	return g
}

func (g *proofGateway) asked() []aiapi.ChatRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]aiapi.ChatRequest(nil), g.requests...)
}

func (g *proofGateway) failNext(n int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.bad = n
}

// deliver has a driver run the plan's only route: the first drop-off is proved with a photo, every other one with the
// OTP.
func (s *service) deliver(c caller, plan contracts.Plan) {
	s.t.Helper()
	route := plan.Routes[0]
	now := time.Now().UTC()
	assigned := routeplan.Assign(routeplan.RebaseStart(route, contracts.LatLng{Lat: -6.27, Lng: 106.80}, now.Add(2*time.Minute)), "drv-1", "veh-aaa", now)
	s.publish(contracts.TypeRouteAssigned, route.ID, 1, contracts.RouteAssigned{
		PlanID: plan.ID, PlanCode: plan.Code, CustomerName: "Dina Pramesti", DriverID: "drv-1", VehicleID: "veh-aaa", StartAt: assigned.StartAt, Route: assigned,
	})
	s.waitFor("the assignment", func() bool { return c.plan(plan.ID).Status == contracts.PlanStatusAssigned })
	s.publish(contracts.TypeRouteStarted, route.ID, 1, contracts.RouteStarted{PlanID: plan.ID, RouteID: route.ID, DriverID: "drv-1", StartedAt: contracts.At(now.Add(3 * time.Minute))})
	version, photographed := int64(1), false
	for i, stop := range route.Stops {
		minute := time.Duration(10*(i+1)) * time.Minute
		version++
		s.publish(contracts.TypeStopArrived, route.ID, version, contracts.StopArrived{PlanID: plan.ID, RouteID: route.ID, StopID: stop.ID, ArrivedAt: contracts.At(now.Add(minute))})
		version++
		done := contracts.StopCompleted{PlanID: plan.ID, RouteID: route.ID, StopID: stop.ID, CompletedAt: contracts.At(now.Add(minute + 3*time.Minute))}
		if stop.Kind == contracts.StopKindDropoff {
			done.Proof = &contracts.DeliveryProof{Kind: contracts.ProofKindOTP, At: done.CompletedAt}
			if !photographed {
				photographed = true
				done.Proof = &contracts.DeliveryProof{Kind: contracts.ProofKindPhoto, At: done.CompletedAt, Photo: deliveryPhotoURL()}
			}
		}
		s.publish(contracts.TypeStopCompleted, route.ID, version, done)
	}
	s.publish(contracts.TypeRouteCompleted, route.ID, version+1, contracts.RouteCompleted{PlanID: plan.ID, RouteID: route.ID, DriverID: "drv-1", FinishedAt: contracts.At(now.Add(50 * time.Minute))})
}

// proofsOf reads the proofs of a plan's drop-offs as the customer sees them, in route order.
func (c caller) proofsOf(planID string) []*contracts.DeliveryProof {
	c.svc.t.Helper()
	var proofs []*contracts.DeliveryProof
	for _, stop := range c.plan(planID).Routes[0].Stops {
		if stop.Kind == contracts.StopKindDropoff {
			proofs = append(proofs, stop.Proof)
		}
	}
	return proofs
}

func (s *service) waitForProofs(c caller, planID string) {
	s.t.Helper()
	s.waitFor("the drop-offs to be done", func() bool {
		proofs := c.proofsOf(planID)
		return len(proofs) == 2 && proofs[0] != nil && proofs[1] != nil
	})
}

func (s *service) jobState() string {
	s.t.Helper()
	var state string
	if err := s.pool.QueryRow(s.t.Context(), `SELECT state FROM proof_jobs`).Scan(&state); err != nil {
		s.t.Fatal(err)
	}
	return state
}

func TestADriversPhotoProofGetsItsAIAnalysisThroughTheWholeStack(t *testing.T) {
	gateway := newProofGateway(t)
	svc := startService(t, func(c *Config) {
		c.AIURL, c.AIToken = gateway.URL, "gateway-token"
		c.ProofAIPoll = 20 * time.Millisecond
	})
	dina, _ := svc.register("dina@contoh.id")
	plan := svc.confirmedPlan(dina)
	versionBefore := testkit.Count(t, svc.pool, `SELECT version FROM plans`)

	svc.deliver(dina, plan)
	svc.waitFor("the analysis to appear on the photo", func() bool {
		proofs := dina.proofsOf(plan.ID)
		return len(proofs) == 2 && proofs[0] != nil && proofs[0].Analysis != nil
	})

	proofs := dina.proofsOf(plan.ID)
	photo, otp := proofs[0], proofs[1]
	if photo.Kind != contracts.ProofKindPhoto || photo.Photo != deliveryPhotoURL() {
		t.Errorf("the photo proof = %+v", photo)
	}
	a := photo.Analysis
	if a.Model != "muse-test" || a.Confidence != 0.84 || a.AnalyzedAt.IsZero() || len(a.Checks) != 3 {
		t.Fatalf("analysis = %+v", a)
	}
	if a.Summary.Key != "srv.ai.text" || a.Summary.Params["text"] == "" || a.Checks[0].Key != contracts.ProofCheckRecipient || a.Checks[0].Result != contracts.ProofResultOK ||
		a.Checks[2].Result != contracts.ProofResultUnclear || a.Checks[2].Note.Key != "srv.ai.text" {
		t.Errorf("analysis = %+v", a)
	}
	var shown map[string]string
	if err := json.Unmarshal([]byte(a.Summary.Params["text"].(string)), &shown); err != nil || shown["en"] == "" || shown["id"] == "" {
		t.Errorf("the summary text is not the JSON {en, id}: %v %v", a.Summary.Params, err)
	}
	if otp.Kind != contracts.ProofKindOTP || otp.Analysis != nil {
		t.Errorf("the OTP proof = %+v: there is nothing to analyse", otp)
	}

	// The model was asked about this photo and nothing else, through the vision profile with the gateway's token.
	asked := gateway.asked()
	if len(asked) != 1 || asked[0].Profile != aiapi.ProfileVision || len(asked[0].Messages) != 2 {
		t.Fatalf("the gateway was asked %d times: %+v", len(asked), asked)
	}
	if imgs := asked[0].Messages[1].Images; len(imgs) != 1 || imgs[0].MIME != "image/jpeg" || !bytes.Equal(imgs[0].Data, deliveryPhoto) {
		t.Errorf("images = %+v", imgs)
	}

	// The commercial side of the plan is as the delivery left it: the analysis is not a new version.
	svc.waitFor("the plan to be done", func() bool { return dina.plan(plan.ID).Status == contracts.PlanStatusDone })
	if got := testkit.Count(t, svc.pool, `SELECT version FROM plans`); got != versionBefore {
		t.Errorf("the plan's version went from %d to %d", versionBefore, got)
	}
	if state := svc.jobState(); state != "done" {
		t.Errorf("job = %s", state)
	}
}

func TestAPhotoIsAnalysedAfterTheGatewayRecovers(t *testing.T) {
	gateway := newProofGateway(t)
	gateway.failNext(1)
	svc := startService(t, func(c *Config) {
		c.AIURL, c.AIToken = gateway.URL, "gateway-token"
		c.ProofAIPoll = 20 * time.Millisecond
	})
	dina, _ := svc.register("dina@contoh.id")
	plan := svc.confirmedPlan(dina)
	svc.deliver(dina, plan)

	svc.waitFor("the first attempt to fail", func() bool { return len(gateway.asked()) >= 1 })
	svc.waitFor("the job to be waiting for its retry", func() bool {
		return testkit.Count(t, svc.pool, `SELECT count(*) FROM proof_jobs WHERE state = 'pending' AND attempts = 1 AND next_at > now() + interval '20 seconds'`) == 1
	})
	if proofs := dina.proofsOf(plan.ID); len(proofs) == 2 && proofs[0] != nil && proofs[0].Analysis != nil {
		t.Fatal("an analysis appeared although the gateway failed")
	}
	// The wait is a backoff of seconds; the test does not sit through it.
	if _, err := svc.pool.Exec(t.Context(), `UPDATE proof_jobs SET next_at = now()`); err != nil {
		t.Fatal(err)
	}
	svc.waitFor("the analysis after the retry", func() bool {
		proofs := dina.proofsOf(plan.ID)
		return len(proofs) == 2 && proofs[0] != nil && proofs[0].Analysis != nil
	})
	if got := len(gateway.asked()); got != 2 {
		t.Errorf("%d gateway calls, want the failed one and the good one", got)
	}
}

func TestWithTheVisionSwitchOffAPhotoIsNeverAnalysed(t *testing.T) {
	gateway := newProofGateway(t)
	svc := startService(t, func(c *Config) {
		c.AIURL, c.AIToken = gateway.URL, "gateway-token"
		c.ProofAIPoll = 20 * time.Millisecond
	})
	dina, _ := svc.register("dina@contoh.id")
	plan := svc.confirmedPlan(dina)
	svc.publish(contracts.TypeSettingsUpdated, contracts.SettingsKeyAI, 1, contracts.SettingsUpdated{
		Key: contracts.SettingsKeyAI, AI: &contracts.AIFeatures{Vision: false, Voice: true, Intent: true, Copilot: true},
	})
	svc.waitFor("the switch to reach the console", func() bool { return !aiFlags(t, dina).Vision })

	svc.deliver(dina, plan)
	svc.waitForProofs(dina, plan.ID)
	svc.waitFor("the job to be skipped", func() bool { return svc.jobState() == "skipped" })

	if got := len(gateway.asked()); got != 0 {
		t.Errorf("the gateway was asked %d times with the switch off", got)
	}
	proofs := dina.proofsOf(plan.ID)
	if proofs[0].Photo != deliveryPhotoURL() || proofs[0].Analysis != nil {
		t.Errorf("the photo proof = %+v: the photo stays, the analysis never comes", proofs[0])
	}

	// Off means off: switching it on again does not read the photos taken meanwhile.
	svc.publish(contracts.TypeSettingsUpdated, contracts.SettingsKeyAI, 2, contracts.SettingsUpdated{
		Key: contracts.SettingsKeyAI, AI: &contracts.AIFeatures{Vision: true, Voice: true, Intent: true, Copilot: true},
	})
	svc.waitFor("the switch to come on", func() bool { return aiFlags(t, dina).Vision })
	time.Sleep(300 * time.Millisecond)
	if got := len(gateway.asked()); got != 0 || dina.proofsOf(plan.ID)[0].Analysis != nil {
		t.Errorf("a photo from before was analysed after the switch came on (%d calls)", got)
	}
}

func TestWithoutAGatewayPhotoJobsWaitAndNothingReadsThem(t *testing.T) {
	// A short poll, so that a worker that wrongly ran would have acted long before the test looks.
	svc := startService(t, func(c *Config) { c.ProofAIPoll = 10 * time.Millisecond })
	dina, _ := svc.register("dina@contoh.id")
	plan := svc.confirmedPlan(dina)

	svc.deliver(dina, plan)
	svc.waitForProofs(dina, plan.ID)
	svc.waitFor("the job to be queued", func() bool { return testkit.Count(t, svc.pool, `SELECT count(*) FROM proof_jobs`) == 1 })
	time.Sleep(300 * time.Millisecond)

	if state := svc.jobState(); state != "pending" {
		t.Errorf("job = %s: no worker may run without a gateway", state)
	}
	if testkit.Count(t, svc.pool, `SELECT attempts FROM proof_jobs`) != 0 || dina.proofsOf(plan.ID)[0].Analysis != nil {
		t.Error("the photo was worked on without a gateway")
	}
}

func TestConfigFromEnvReadsTheProofAnalysisPoll(t *testing.T) {
	env := platformEnv(map[string]string{"DATABASE_URL": "postgres://x", "KAFKA_BROKERS": "a:9092"})
	cfg := ConfigFromEnv(env)
	if err := env.Err(); err != nil || cfg.ProofAIPoll != 2*time.Second {
		t.Errorf("default = %v (%v), want 2s", cfg.ProofAIPoll, err)
	}
	if got := (Config{}).withDefaults().ProofAIPoll; got != 2*time.Second {
		t.Errorf("withDefaults = %v", got)
	}
	if got := (Config{ProofAIPoll: time.Second}).withDefaults().ProofAIPoll; got != time.Second {
		t.Errorf("an explicit poll became %v", got)
	}
	cfg = ConfigFromEnv(platformEnv(map[string]string{"DATABASE_URL": "postgres://x", "KAFKA_BROKERS": "a:9092", "PROOF_AI_POLL": "500ms"}))
	if cfg.ProofAIPoll != 500*time.Millisecond {
		t.Errorf("PROOF_AI_POLL=500ms gave %v", cfg.ProofAIPoll)
	}
	bad := platformEnv(map[string]string{"DATABASE_URL": "postgres://x", "KAFKA_BROKERS": "a:9092", "PROOF_AI_POLL": "often"})
	ConfigFromEnv(bad)
	if err := bad.Err(); err == nil || !strings.Contains(err.Error(), "PROOF_AI_POLL") {
		t.Errorf("a bad poll was not reported: %v", err)
	}
}
