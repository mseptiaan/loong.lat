package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi/aiapitest"
	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/optimizerapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/orchestrate/orchestratetest"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/quote"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/roads"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/runner"
)

func vehicles() []contracts.Vehicle {
	return []contracts.Vehicle{
		{ID: "m", Type: contracts.VehicleTypeMotor, Status: contracts.VehicleStatusActive, MaxWeightKg: 20, MaxVolumeM3: 0.12, MaxDimensionCm: 55,
			Fare: contracts.FareConfig{Base: 6000, PerKm: 1800, PerMin: 150, PerExtraStop: 2500, SurchargePerKg: 800, SurchargeAboveKg: 10}},
		{ID: "v", Type: contracts.VehicleTypeBlindvan, Status: contracts.VehicleStatusActive, MaxWeightKg: 500, MaxVolumeM3: 3.2, MaxDimensionCm: 190,
			Fare: contracts.FareConfig{Base: 45000, PerKm: 4500, PerMin: 500, PerExtraStop: 8000, SurchargePerKg: 150, SurchargeAboveKg: 300}},
	}
}

func wp(name string, lat, lng float64) contracts.Waypoint {
	return contracts.Waypoint{PlaceRef: contracts.PlaceRef{Name: name, Address: name, Position: contracts.LatLng{Lat: lat, Lng: lng}}}
}

func request() optimizerapi.QuoteRequest {
	item := contracts.Item{ID: "i", Name: "Paket", Qty: 1, WeightKg: 3, LengthCm: 30, WidthCm: 20, HeightCm: 10, Source: contracts.ItemSourceManual}
	return optimizerapi.QuoteRequest{
		PlanID: "PL-1",
		Shipments: []contracts.Shipment{
			{ID: "s1", Pickup: wp("Kemang", -6.2608, 106.8133), Dropoff: wp("Senayan", -6.2275, 106.8029), Items: []contracts.Item{item}},
			{ID: "s2", Pickup: wp("Blok M", -6.2441, 106.7993), Dropoff: wp("Pondok Indah", -6.2653, 106.7836), Items: []contracts.Item{item}},
		},
		Schedule: contracts.Schedule{Mode: contracts.ScheduleModeNow}, Priority: contracts.Priority{Preset: contracts.PresetBalanced},
		Vehicles: vehicles(), Now: contracts.At(time.Date(2026, 10, 21, 2, 50, 0, 0, time.UTC)),
	}
}

func server(t *testing.T, token string) (*httptest.Server, *optimizerapi.Client) {
	t.Helper()
	h := &Handler{Planner: &quote.Planner{Roads: roads.Estimate{}, Runner: runner.Local{}}, Token: token, Timeout: 30 * time.Second}
	srv := httptest.NewServer(h.Routes())
	t.Cleanup(srv.Close)
	return srv, &optimizerapi.Client{BaseURL: srv.URL, Token: token}
}

func TestQuoteStreamsProgressThenTheOptions(t *testing.T) {
	_, client := server(t, "s3cret")
	var stages []int
	res, err := client.Quote(context.Background(), request(), func(pr optimizerapi.Progress) {
		stage, total := pr.Stage, pr.Total
		if pr.Msg != nil {
			t.Errorf("a fast quote reported a step inside a stage: %+v", pr.Msg)
		}
		if total != quote.NumStages {
			t.Errorf("total = %d, want %d", total, quote.NumStages)
		}
		stages = append(stages, stage)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(stages) != quote.NumStages || stages[0] != 1 || stages[len(stages)-1] != quote.NumStages {
		t.Errorf("stages = %v, want 1..%d", stages, quote.NumStages)
	}
	if len(res.OptionSet.Options) == 0 || res.Engine == "" || res.RoadSource != "estimate" {
		t.Errorf("unexpected result %+v", res)
	}
	var recommended int
	for _, o := range res.OptionSet.Options {
		if o.Recommended {
			recommended++
		}
	}
	if recommended != 1 {
		t.Errorf("%d recommended options", recommended)
	}
}

func TestACustomerMistakeComesBackAsATypedError(t *testing.T) {
	_, client := server(t, "")
	req := request()
	req.Shipments[1].Items = nil
	_, err := client.Quote(context.Background(), req, nil)
	var oe *optimizerapi.Error
	if !errors.As(err, &oe) {
		t.Fatalf("want *optimizerapi.Error, got %T %v", err, err)
	}
	if oe.Code != quote.CodeInvalid || oe.Field != "shipments.1.items" || oe.Msg.Key != "srv.engine.itemsMissing" {
		t.Errorf("unexpected error %+v", oe)
	}
}

func TestRequestsNeedTheSharedSecret(t *testing.T) {
	srv, _ := server(t, "s3cret")
	for name, auth := range map[string]string{"none": "", "wrong": "Bearer nope"} {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+optimizerapi.QuotePath, nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, resp.StatusCode)
		}
	}
}

func TestBadBodiesAreRejected(t *testing.T) {
	srv, _ := server(t, "")
	resp, err := http.Post(srv.URL+optimizerapi.QuotePath, "application/json", stringsReader("{not json"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status %d, want 400", resp.StatusCode)
	}
}

// searching serves quotes with a runner that answers a Search the way a searching solver does, over the real plain solve.
func searching(t *testing.T, ai *aiapitest.Fake) (*httptest.Server, *optimizerapi.Client) {
	t.Helper()
	planner := &quote.Planner{Roads: roads.Estimate{}, Runner: orchestratetest.Searching(runner.Local{}), Limits: job.Limits{Iterations: 40}}
	info := optimizerapi.Info{Engine: "test", Runner: "local", Efforts: contracts.Efforts}
	if ai != nil {
		planner.AI = ai
		info.AIReview = true
	}
	h := &Handler{Planner: planner, Timeout: 30 * time.Second, DeepTimeout: time.Minute, Info: info}
	srv := httptest.NewServer(h.Routes())
	t.Cleanup(srv.Close)
	return srv, &optimizerapi.Client{BaseURL: srv.URL}
}

func TestAThoroughQuoteStreamsItsStepsAndEndsWithItsMethod(t *testing.T) {
	_, client := searching(t, nil)
	req := request()
	req.Effort = contracts.EffortThorough
	var events []optimizerapi.Progress
	res, err := client.Quote(context.Background(), req, func(pr optimizerapi.Progress) { events = append(events, pr) })
	if err != nil {
		t.Fatal(err)
	}

	var order []string
	for _, e := range events {
		if e.Total != quote.NumStages {
			t.Errorf("event %+v: total = %d, want %d", e, e.Total, quote.NumStages)
		}
		if e.Msg == nil {
			order = append(order, string(rune('0'+e.Stage)))
			continue
		}
		if e.Stage != int(quote.StageCombine) {
			t.Errorf("step %s reported in stage %d, want the combine stage", e.Msg.Key, e.Stage)
		}
		order = append(order, "3:"+strings.TrimPrefix(e.Msg.Key, "srv.engine.search."))
	}
	if want := []string{"1", "2", "3", "3:experiments", "3:experiments", "3:polish", "4"}; !slices.Equal(order, want) {
		t.Errorf("events = %v, want %v", order, want)
	}
	for _, e := range events {
		if e.Msg != nil && e.Msg.Key == "srv.engine.search.experiments" {
			if n, ok := e.Msg.Params["n"].(float64); !ok || n < 1 {
				t.Errorf("experiments carries n = %v", e.Msg.Params["n"])
			}
		}
	}

	m := res.OptionSet.Method
	if m == nil || m.Effort != contracts.EffortThorough || !m.Polished || m.Trials == 0 || m.RoadSource != "estimate" {
		t.Errorf("done carries method %+v", m)
	}
}

func TestAReviewedQuoteStreamsEachRoundAndNamesItsReviewer(t *testing.T) {
	ai := aiapitest.New(
		aiapitest.Say(`{"satisfied":false,"experiments":[{"algorithm":"kmedoids","k":2,"effort":"normal"}],"note":"more"}`),
		aiapitest.Say(`{"satisfied":true,"experiments":[],"note":"ok"}`),
	)
	_, client := searching(t, ai)
	req := request()
	req.Effort, req.AIReview = contracts.EffortReviewed, true
	var keys []string
	var rounds []any
	res, err := client.Quote(context.Background(), req, func(pr optimizerapi.Progress) {
		if pr.Msg != nil {
			keys = append(keys, strings.TrimPrefix(pr.Msg.Key, "srv.engine.search."))
			if pr.Msg.Key == "srv.engine.search.round" {
				rounds = append(rounds, pr.Msg.Params["round"])
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"experiments", "round", "reviewAI", "more", "round", "reviewAI", "satisfied", "polish"}; !slices.Equal(keys, want) {
		t.Errorf("steps = %v, want %v", keys, want)
	}
	if len(rounds) != 2 || rounds[0] != 1.0 || rounds[1] != 2.0 {
		t.Errorf("rounds = %v", rounds)
	}
	if m := res.OptionSet.Method; m == nil || m.Reviewer != contracts.ReviewerAI || m.Rounds != 1 {
		t.Errorf("method = %+v", m)
	}

	// The caller says no AI: the same quote is reviewed by the rules and the model is not asked.
	asked := ai.Calls()
	req.AIReview = false
	res, err = client.Quote(context.Background(), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.OptionSet.Method.Reviewer != contracts.ReviewerRules || ai.Calls() != asked {
		t.Errorf("reviewer %q, %d new model calls", res.OptionSet.Method.Reviewer, ai.Calls()-asked)
	}
}

func TestAFastQuoteStreamsExactlyTheFourStagesAndNothingElse(t *testing.T) {
	srv, _ := searching(t, nil)
	body, _ := json.Marshal(request())
	resp, err := http.Post(srv.URL+optimizerapi.QuotePath, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var types []string
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for sc.Scan() {
		var raw map[string]any
		if err := json.Unmarshal(sc.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		if _, has := raw["msg"]; has {
			t.Errorf("a fast quote streamed a msg: %s", sc.Bytes())
		}
		types = append(types, raw["type"].(string))
	}
	if want := []string{"progress", "progress", "progress", "progress", "done"}; !slices.Equal(types, want) {
		t.Errorf("events = %v, want %v", types, want)
	}
}

func TestAnUnknownEffortIsRefusedAsAnInputError(t *testing.T) {
	_, client := searching(t, nil)
	req := request()
	req.Effort = "extreme"
	_, err := client.Quote(context.Background(), req, nil)
	var oe *optimizerapi.Error
	if !errors.As(err, &oe) || oe.Code != quote.CodeInvalid || oe.Field != "effort" {
		t.Errorf("err = %v", err)
	}
}

func TestDeepQuotesGetTheDeepTimeout(t *testing.T) {
	h := &Handler{Timeout: 45 * time.Second, DeepTimeout: 150 * time.Second}
	for effort, want := range map[contracts.Effort]time.Duration{
		"": 45 * time.Second, contracts.EffortFast: 45 * time.Second,
		contracts.EffortThorough: 150 * time.Second, contracts.EffortReviewed: 150 * time.Second,
	} {
		if got := h.timeout(effort); got != want {
			t.Errorf("timeout(%q) = %v, want %v", effort, got, want)
		}
	}
	if got := (&Handler{Timeout: 45 * time.Second}).timeout(contracts.EffortReviewed); got != 45*time.Second {
		t.Errorf("without a deep timeout a deep quote takes the ordinary one, got %v", got)
	}
}

func TestInfoReportsTheEffortsAndWhetherTheModelCanReview(t *testing.T) {
	for _, withAI := range []bool{false, true} {
		var ai *aiapitest.Fake
		if withAI {
			ai = aiapitest.New()
		}
		srv, _ := searching(t, ai)
		resp, err := http.Get(srv.URL + optimizerapi.InfoPath)
		if err != nil {
			t.Fatal(err)
		}
		var info optimizerapi.Info
		err = json.UnmarshalRead(resp.Body, &info)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(info.Efforts, contracts.Efforts) || info.AIReview != withAI || info.Engine != "test" || info.Runner != "local" {
			t.Errorf("info = %+v (AI configured: %v)", info, withAI)
		}
	}
}

func stringsReader(s string) *strings.Reader { return strings.NewReader(s) }

// The service runs behind the platform's access log, which wraps the response writer. The stream must still reach
// the client while the solver works (the first event is what lets the wizard show its progress), not all at once at
// the end: the runner here waits for the client to have seen an event before it solves.
func TestProgressReachesTheClientWhileTheSolverIsStillWorking(t *testing.T) {
	seen := make(chan struct{})
	var once sync.Once
	waiting := runner.Func(func(ctx context.Context, j *job.Job, _ func(job.Progress)) (*job.Result, error) {
		select {
		case <-seen:
		case <-time.After(5 * time.Second):
			return nil, errors.New("no progress event reached the client while the solver was busy")
		}
		return runner.Local{}.Run(ctx, j, nil)
	})
	h := &Handler{Planner: &quote.Planner{Roads: roads.Estimate{}, Runner: waiting}, Timeout: 30 * time.Second}
	srv := httptest.NewServer(platform.Chain(h.Routes(), platform.AccessLog(slog.New(slog.DiscardHandler))))
	t.Cleanup(srv.Close)

	client := &optimizerapi.Client{BaseURL: srv.URL}
	if _, err := client.Quote(context.Background(), request(), func(optimizerapi.Progress) { once.Do(func() { close(seen) }) }); err != nil {
		t.Fatal(err)
	}
}
