package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi/aiapitest"
	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/plans"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/testkit"
)

// now is the fixed clock of the tests: Wednesday 21 October 2026, 14:05 in Jakarta.
var now = time.Date(2026, 10, 21, 7, 5, 0, 0, time.UTC)

// syncBuffer is a log sink that handler goroutines and the test can share.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// speechGateway adds Transcribe to the scripted model.
type speechGateway struct {
	*aiapitest.Fake
	transcribe func(ctx context.Context, audio []byte) (aiapi.Transcript, error)
	audios     atomic.Int32
}

func (g *speechGateway) Transcribe(ctx context.Context, audio []byte, _, _ string, _ ...aiapi.TranscribeOption) (aiapi.Transcript, error) {
	g.audios.Add(1)
	if g.transcribe == nil {
		return aiapi.Transcript{}, &aiapi.Error{Kind: aiapi.KindUnavailable}
	}
	return g.transcribe(ctx, audio)
}

// flagSet is the admin's switches, changeable while a test runs.
type flagSet struct {
	mu sync.Mutex
	f  contracts.AIFeatures
}

func (f *flagSet) get(context.Context) (contracts.AIFeatures, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.f, nil
}

func (f *flagSet) set(v contracts.AIFeatures) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.f = v
}

var allOn = contracts.AIFeatures{Vision: true, Voice: true, Intent: true, Copilot: true}

// rig is the AI service on a fresh database behind the real RPC layer.
type rig struct {
	t     *testing.T
	pool  *pgxpool.Pool
	svc   *Service
	gw    *speechGateway
	flags *flagSet
	logs  *syncBuffer
	srv   *httptest.Server
	plans int
}

type rigOptions struct {
	steps []aiapitest.Step
	// gateway replaces the scripted one (a real client against a fake gateway, say); nil keeps the script.
	gateway       aiapi.Gateway
	noGateway     bool
	ratePerMinute int
	burst         int
	// clock replaces the fixed clock.
	clock func() time.Time
}

func newRig(t *testing.T, mod ...func(*rigOptions)) *rig {
	t.Helper()
	var o rigOptions
	for _, m := range mod {
		m(&o)
	}
	pool := testkit.DB(t)
	r := &rig{t: t, pool: pool, flags: &flagSet{f: allOn}, logs: &syncBuffer{}}
	r.gw = &speechGateway{Fake: aiapitest.New(o.steps...)}
	var gateway aiapi.Gateway = r.gw
	if o.gateway != nil {
		gateway = o.gateway
	}
	if o.noGateway {
		gateway = nil
	}
	clock := func() time.Time { return now }
	if o.clock != nil {
		clock = o.clock
	}
	r.svc = New(Options{
		Gateway: gateway, Pool: pool, Flags: r.flags.get, Now: clock,
		Log:           slog.New(slog.NewJSONHandler(r.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		RatePerMinute: o.ratePerMinute, Burst: o.burst,
		BalanceRp: func(ctx context.Context, customerID string) (int64, error) {
			var balance int64
			err := pool.QueryRow(ctx, `SELECT balance_rp FROM wallets WHERE customer_id = $1`, customerID).Scan(&balance)
			return balance, err
		},
	})
	rpc := platform.NewRPC(slog.New(slog.NewJSONHandler(r.logs, nil)))
	r.svc.Mount(rpc)
	mux := http.NewServeMux()
	rpc.Mount(mux)
	r.srv = httptest.NewServer(platform.Chain(mux,
		platform.LocaleMiddleware,
		func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if id := req.Header.Get("X-Customer"); id != "" {
					role := contracts.Role(req.Header.Get("X-Role"))
					if role == "" {
						role = contracts.RoleCustomer
					}
					req = req.WithContext(platform.WithPrincipal(req.Context(), platform.Principal{Role: role, UserID: id, Name: "Test"}))
				}
				next.ServeHTTP(w, req)
			})
		}))
	t.Cleanup(r.srv.Close)
	return r
}

// response is what one RPC call answered: for a plain method its JSON body; for a stream the events in order.
type response struct {
	Status int
	Body   map[string]any
	Events []map[string]any
	Raw    string
}

// call posts an RPC method as a customer ("" = anonymous) in a language ("" = none).
func (r *rig) call(customerID, lang, method string, body any) response {
	r.t.Helper()
	return r.callCtx(r.t.Context(), customerID, lang, method, body)
}

func (r *rig) callCtx(ctx context.Context, customerID, lang, method string, body any) response {
	r.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		r.t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, r.srv.URL+"/v1/rpc/"+method, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if customerID != "" {
		req.Header.Set("X-Customer", customerID)
	}
	if lang != "" {
		req.Header.Set("Accept-Language", lang)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := response{Status: resp.StatusCode}
	data, _ := io.ReadAll(resp.Body)
	out.Raw = string(data)
	if strings.Contains(resp.Header.Get("Content-Type"), "ndjson") {
		sc := bufio.NewScanner(bytes.NewReader(data))
		sc.Buffer(nil, 16<<20)
		for sc.Scan() {
			var ev map[string]any
			if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
				r.t.Fatalf("bad stream line %q: %v", sc.Text(), err)
			}
			out.Events = append(out.Events, ev)
		}
		return out
	}
	if len(data) > 0 {
		_ = json.Unmarshal(data, &out.Body)
	}
	return out
}

// errKey is the message key of an error answer, and its code.
func (res response) errKey() (code, key string) {
	msg, _ := res.Body["msg"].(map[string]any)
	code, _ = res.Body["code"].(string)
	key, _ = msg["key"].(string)
	return code, key
}

// wantError checks that a plain answer is the API error with this status and message key.
func (res response) wantError(t *testing.T, status int, key string) {
	t.Helper()
	if _, got := res.errKey(); res.Status != status || got != key {
		t.Fatalf("answered %d %q (%s), want %d %q", res.Status, got, res.Raw, status, key)
	}
}

// types lists the event types of a stream.
func (res response) types() []string {
	var out []string
	for _, ev := range res.Events {
		out = append(out, ev["type"].(string))
	}
	return out
}

func (res response) event(t *testing.T, i int) map[string]any {
	t.Helper()
	if i < 0 {
		i += len(res.Events)
	}
	if i < 0 || i >= len(res.Events) {
		t.Fatalf("no event %d in %d events: %s", i, len(res.Events), res.Raw)
	}
	return res.Events[i]
}

func (r *rig) customer(name string) string {
	r.t.Helper()
	return testkit.Customer(r.t, r.pool, name)
}

/* ---------------------------------- a plan to ask about ---------------------------------- */

func msg(key string, params map[string]any) contracts.Msg {
	return contracts.Msg{Key: key, Params: params}
}

// option builds a route option: cost, distance, total time and average arrival, with the label and reasons the
// optimizer would give it.
func option(id string, preset contracts.Preset, cost int64, km, total, avg float64, motor, van int, recommended bool) contracts.PlanOption {
	routes := []contracts.Route{}
	for i := range motor + van {
		typ := contracts.VehicleTypeMotor
		if i >= motor {
			typ = contracts.VehicleTypeBlindvan
		}
		routes = append(routes, contracts.Route{
			ID: id + ":r" + string(rune('1'+i)), VehicleType: typ, CostRp: cost / int64(motor+van),
			Stops: []contracts.RouteStop{{ID: "s1"}, {ID: "s2"}},
			Fare: []contracts.FareLine{
				{Label: msg("srv.engine.fare.base", nil), AmountRp: 6000},
				{Label: msg("srv.engine.fare.distance", map[string]any{"km": km}), AmountRp: cost/int64(motor+van) - 6000},
			},
		})
	}
	return contracts.PlanOption{
		ID: id, Preset: preset, Recommended: recommended,
		Label: msg("srv.engine.label.two", map[string]any{
			"a": msg("dom.preset."+string(preset), nil),
			"b": msg("srv.engine.fleet.motor", map[string]any{"count": float64(motor + van)}),
		}),
		Headline: msg("srv.engine.headline", map[string]any{"fleet": msg("srv.engine.fleet.motor", map[string]any{"count": float64(motor)}), "count": float64(4), "time": "2026-10-21T08:20:00.000Z"}),
		Routes:   routes,
		Totals:   contracts.Totals{CostRp: cost, DistanceKm: km, DurationMin: total, AvgDeliveryMin: avg, Stops: 4, Motor: motor, Blindvan: van},
		Savings:  contracts.Savings{CostRp: 120000 - cost, Pct: math.Round(float64(120000-cost) / 1200), DistanceKm: 20 - km},
		Reasons: []contracts.Msg{
			msg("srv.engine.reason.costSaves", map[string]any{"cost": float64(cost), "saving": float64(120000 - cost), "pct": math.Round(float64(120000-cost) / 1200)}),
			msg("srv.engine.reason.single", map[string]any{"duration": total}),
		},
	}
}

// Option ids of the seeded plan.
const (
	optCheap    = "opt-cheap"
	optFast     = "opt-fast"
	optBalanced = "opt-balanced"
)

// optionSet is three options: the cheapest (Rp 46.500), the fastest (Rp 68.000, two vehicles) and the
// recommended balanced one (Rp 54.000).
func optionSet() *contracts.OptionSet {
	return &contracts.OptionSet{
		ComputedAt: contracts.At(now),
		Baseline:   contracts.Totals{CostRp: 120000, DistanceKm: 20.4, DurationMin: 95},
		Options: []contracts.PlanOption{
			option(optCheap, contracts.PresetCheapest, 46500, 12.3, 65, 41, 1, 0, false),
			option(optFast, contracts.PresetFastest, 68000, 15.8, 38, 24, 2, 0, false),
			option(optBalanced, contracts.PresetBalanced, 54000, 13.1, 52, 33, 1, 0, true),
		},
		InputHash: "h1",
	}
}

func waypoint(name, address string) contracts.Waypoint {
	return contracts.Waypoint{
		PlaceRef:    contracts.PlaceRef{Name: name, Address: address},
		ContactName: "Contact " + name, ContactPhone: "0812-SECRET-PHONE", Note: "note for " + name,
	}
}

// seedPlan stores a plan owned by the customer: with the three options computed and (by default) not yet
// confirmed. Change the plan before it is stored with the mods.
func (r *rig) seedPlan(customerID string, mods ...func(*contracts.Plan)) contracts.Plan {
	r.t.Helper()
	id := "pln-" + customerID[len(customerID)-8:] + "-" + time.Now().Format("150405.000000000")
	r.plans++
	p := contracts.Plan{
		ID: id, Code: "PL-" + strconv.Itoa(2040+r.plans), UserID: customerID, Status: contracts.PlanStatusOptionsReady,
		Schedule: contracts.Schedule{Mode: contracts.ScheduleModeNow}, Priority: contracts.Priority{Preset: contracts.PresetBalanced},
		Shipments: []contracts.Shipment{
			{ID: "s1", Pickup: waypoint("Kemang Village", "Jl. Pangeran Antasari"), Dropoff: waypoint("Senayan City", "Jl. Asia Afrika"),
				Items: []contracts.Item{{ID: "i1", Name: "Laptop (boxed)", Qty: 1, WeightKg: 2.8, Fragile: true}}},
			{ID: "s2", Pickup: waypoint("Blok M Square", "Jl. Melawai"), Dropoff: waypoint("Pondok Indah Mall", "Jl. Metro Pondok Indah"),
				Items: []contracts.Item{{ID: "i2", Name: "Medium cardboard box", Qty: 2, WeightKg: 4}}},
		},
		OptionSet: optionSet(), SelectedOptionID: optBalanced,
		CreatedAt: contracts.At(now), UpdatedAt: contracts.At(now),
		Routes: []contracts.Route{}, Events: []contracts.PlanEvent{},
	}
	for _, m := range mods {
		m(&p)
	}
	if err := plans.Insert(r.t.Context(), r.pool, plans.Record{Plan: p, Version: 1, RouteVersions: map[string]plans.RouteVersions{}}); err != nil {
		r.t.Fatal(err)
	}
	return p
}

// confirmed turns the seeded plan into a confirmed one with a route that is on its way.
func confirmed(p *contracts.Plan) {
	p.Status = contracts.PlanStatusRunning
	p.TotalCostRp = 54000
	p.ConfirmedAt = contracts.At(now.Add(-30 * time.Minute))
	p.Routes = []contracts.Route{{
		ID: p.ID + ":r1", PlanID: p.ID, VehicleType: contracts.VehicleTypeMotor, VehicleID: "veh-aaa", DriverID: "drv-1",
		Status: contracts.RouteStatusRunning, StartAt: contracts.At(now.Add(-20 * time.Minute)), CostRp: 54000, DelayMin: 7,
		Stops: []contracts.RouteStop{
			{ID: "st1", Seq: 1, Kind: contracts.StopKindPickup, Waypoint: waypoint("Kemang Village", "Jl. Pangeran Antasari"), Status: contracts.StopStatusDone, ArriveAt: contracts.At(now.Add(-10 * time.Minute)), DeliveryCode: "SECRET-OTP-1234"},
			{ID: "st2", Seq: 2, Kind: contracts.StopKindDropoff, Waypoint: waypoint("Senayan City", "Jl. Asia Afrika"), Status: contracts.StopStatusEnRoute, ArriveAt: contracts.At(now.Add(25 * time.Minute)), DeliveryCode: "SECRET-OTP-5678"},
		},
	}}
}

// textOf flattens everything the model was sent in one request, for "this must not appear" checks.
func textOf(req aiapi.ChatRequest) string {
	raw, _ := json.Marshal(req)
	return string(raw)
}

// allText is the text of every request the scripted model received.
func allText(g *speechGateway) string {
	var b strings.Builder
	for _, req := range g.Requests() {
		b.WriteString(textOf(req))
	}
	return b.String()
}

// platformCustomer is a context that carries a signed-in customer, for calling a method directly.
func platformCustomer(customerID string) context.Context {
	return platform.WithPrincipal(context.Background(), platform.Principal{Role: contracts.RoleCustomer, UserID: customerID})
}

// apiErr is the code and message key of the platform error in err.
func apiErr(t *testing.T, err error) (code, key string) {
	t.Helper()
	e, ok := errors.AsType[*platform.APIError](err)
	if !ok {
		t.Fatalf("not a platform error: %v", err)
	}
	return string(e.Code), e.Msg.Key
}

// callCtxQuiet is callCtx for a request the test expects to be cut short: transport errors are not failures.
func (r *rig) callCtxQuiet(ctx context.Context, customerID, lang, method string, body any) response {
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, r.srv.URL+"/v1/rpc/"+method, bytes.NewReader(raw))
	req.Header.Set("X-Customer", customerID)
	req.Header.Set("Accept-Language", lang)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return response{}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return response{Status: resp.StatusCode}
}
