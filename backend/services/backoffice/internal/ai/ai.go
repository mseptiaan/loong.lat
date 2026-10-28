// Package ai is the back office's AI features for the admin: the Copilot that answers questions about the queue, the
// drivers and a plan, and the narrated summary of the analytics. The model is Meta's, reached only through the AI
// gateway (pkg/aiapi); this package decides what the model may see and what becomes of what it says.
//
// Principles, all of them enforced here and covered by tests:
//
//   - The model only reads and proposes. The Copilot's tools read the backoffice's own projection (plans, drivers,
//     vehicles, dispatch suggestions, today's numbers) and change nothing; an action is only ever a proposal the
//     screen offers as a button, validated against the real data before it is offered.
//   - Numbers come from the server. Every figure a tool returns, and every figure of the analytics, is computed in
//     Go and carries its text formatted in the admin's language; the model is told to quote them.
//   - The admin's copilot switch is respected on every call (settings.get shows the admin's stored truth; the gate is
//     here), and each admin is rate limited so one session cannot spend the Meta budget.
//   - Nothing the admin asked and nothing a model answered is logged; only sizes, counts and timings.
package ai

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/agent"
	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/dispatch"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/finance"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/fleet"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/plans"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/settings"
)

// Message keys of the frontend catalog the AI methods answer with.
const (
	msgAIDisabled = "srv.api.aiDisabled"
	msgAIError    = "srv.api.aiError"
	msgQuestion   = "srv.api.questionEmpty"
)

// feature is one AI method, with the name of the feature in the messages that mention it. Both methods are
// governed by the admin's one copilot switch.
type feature struct {
	name  string // for logs
	label string // catalog key of the feature's name (srv.api.feature.*)
}

var (
	featCopilot  = feature{"copilot", "srv.api.feature.copilot"}
	featInsights = feature{"insights", "srv.api.feature.insights"}
)

func (f feature) msg(key string) contracts.Msg {
	return contracts.Msg{Key: key, Params: map[string]any{"feature": contracts.Msg{Key: f.label}}}
}

// Defaults of the per-admin rate limit: a burst of Burst requests, then RatePerMinute a minute. A Copilot question
// or an insights run is one request however many tools or model calls it takes.
const (
	DefaultRatePerMinute = 20
	DefaultBurst         = 10
)

// Options wires a Service.
type Options struct {
	// Model is the AI gateway; nil (no AI_URL) makes every method answer that the feature is having problems.
	Model aiapi.Model
	Pool  *pgxpool.Pool
	// The services the tools read through; none of them is written to.
	Plans    *plans.Service
	Fleet    *fleet.Service
	Dispatch *dispatch.Service
	Finance  *finance.Service
	Log      *slog.Logger
	// RatePerMinute and Burst bound each admin's AI requests; zero takes the defaults.
	RatePerMinute int
	Burst         int
	// Now is the clock (tests).
	Now func() time.Time
}

// Service serves ai.copilot and ai.insights.
type Service struct {
	model    aiapi.Model
	pool     *pgxpool.Pool
	plans    *plans.Service
	fleet    *fleet.Service
	dispatch *dispatch.Service
	finance  *finance.Service
	limiter  *platform.LoginLimiter
	log      *slog.Logger
	now      func() time.Time
}

// New returns a Service.
func New(o Options) *Service {
	s := &Service{
		model: o.Model, pool: o.Pool, plans: o.Plans, fleet: o.Fleet, dispatch: o.Dispatch, finance: o.Finance,
		log: o.Log, now: o.Now,
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	rate, burst := o.RatePerMinute, o.Burst
	if rate <= 0 {
		rate = DefaultRatePerMinute
	}
	if burst <= 0 {
		burst = DefaultBurst
	}
	s.limiter = platform.NewLoginLimiter(time.Minute/time.Duration(rate), burst)
	return s
}

// Mount adds ai.copilot and ai.insights; bodies are named after the frontend's parameters.
func (s *Service) Mount(rpc *platform.RPC) {
	rpc.Stream("ai.copilot", s.copilot)
	rpc.Stream("ai.insights", func(ctx context.Context, _ struct{}, emit func(any) error) error {
		return s.insights(ctx, emit)
	})
}

// enter is the door of both methods: the caller must be a signed-in admin, the admin's copilot switch must be
// on, and there must be a gateway. A refused feature costs the admin none of the rate (see allow).
func (s *Service) enter(ctx context.Context, f feature) (platform.Principal, error) {
	admin, err := platform.RequireRole(ctx, contracts.RoleAdmin)
	if err != nil {
		return admin, err
	}
	current, err := settings.Read(ctx, s.pool)
	if err != nil {
		return admin, err
	}
	if !current.AI.Copilot {
		return admin, platform.Denied(f.msg(msgAIDisabled))
	}
	if s.model == nil {
		return admin, platform.Unavailable(f.msg(msgAIError)).WithCause(errors.New("no AI gateway is configured (AI_URL)"))
	}
	return admin, nil
}

// allow spends one request of the admin's rate; the answer is the one a rate limiter gives.
func (s *Service) allow(admin platform.Principal) error {
	if ok, wait := s.limiter.Allow(admin.UserID); !ok {
		return platform.TooManyRequests(wait)
	}
	return nil
}

// fail turns what went wrong with a feature into the error to answer with. The caller's own cancellation stays as
// it is; a gateway that is down, slow, limited or cut off, a model that misbehaved, anything else: "this feature is
// having problems", with the cause kept for the logs (the *aiapi.Error stays in the chain, errors.AsType finds it).
func (s *Service) fail(ctx context.Context, f feature, err error) error {
	if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return err
	}
	return platform.Unavailable(f.msg(msgAIError)).WithCause(err)
}

// requestID names one AI answer in the screen and in the logs.
func requestID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "req-" + hex.EncodeToString(b[:])
}

/* ---------- the stream's wire types (the frontend's CopilotEvent) ---------- */

// ToolTrace is one tool call as the screen shows it: a running line that becomes done or failed.
type ToolTrace struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Target string `json:"target,omitempty"`
	Status string `json:"status"`
	Ms     *int64 `json:"ms,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// Action is the frontend's CopilotAction: a proposal the screen offers as a button; nothing happens until the
// admin presses it.
type Action struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Kind     string `json:"kind"`
	PlanID   string `json:"planId,omitempty"`
	OptionID string `json:"optionId,omitempty"`
	Primary  bool   `json:"primary,omitzero"`
}

// Kinds of actions the admin's Copilot proposes (pilih_opsi belongs to the customer's).
const (
	actionAssignBest = "tugaskan_terbaik"
	actionOpenPlan   = "buka_plan"
)

// maxActions is how many buttons an answer offers.
const maxActions = 3

// The events of the stream: tool traces, pieces of the answer, then done with the proposals (always a list, never
// null: the screen reads its length).
type (
	toolEvent struct {
		Type  string    `json:"type"`
		Trace ToolTrace `json:"trace"`
	}
	tokenEvent struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	doneEvent struct {
		Type      string   `json:"type"`
		Actions   []Action `json:"actions"`
		RequestID string   `json:"requestId"`
	}
)

// sender writes events to the stream and remembers the first failed write: the client has gone, and whoever
// feeds it should stop. The context it cancels is the run's own.
type sender struct {
	emit   func(any) error
	cancel context.CancelCauseFunc
	err    error
	n      int // traces started so far; ids are t0, t1, ...
}

func (o *sender) send(ev any) {
	if o.err != nil {
		return
	}
	if err := o.emit(ev); err != nil {
		o.err = err
		o.cancel(err)
	}
}

// begin opens a trace line and returns the id its end must carry.
func (o *sender) begin(title, target string) string {
	id := "t" + strconv.Itoa(o.n)
	o.n++
	o.send(toolEvent{Type: "tool", Trace: ToolTrace{ID: id, Title: title, Target: target, Status: "running"}})
	return id
}

// end closes a trace line.
func (o *sender) end(id string, tr agent.Trace, failed bool, ms int64) {
	status := "done"
	if failed {
		status = "failed"
	}
	o.send(toolEvent{Type: "tool", Trace: ToolTrace{ID: id, Title: tr.Title, Target: tr.Target, Status: status, Ms: &ms, Detail: tr.Detail}})
}

func (o *sender) token(text string) { o.send(tokenEvent{Type: "token", Text: text}) }

// done ends the stream; with no actions the list is empty, not absent.
func (o *sender) done(actions []Action, id string) error {
	if actions == nil {
		actions = []Action{}
	}
	return o.emit(doneEvent{Type: "done", Actions: actions, RequestID: id})
}

// cleanQuestion trims the question and cuts it to maxQuestionRunes; empty is a validation error on "question".
func cleanQuestion(q string) (string, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return "", platform.Invalid("question", contracts.Msg{Key: msgQuestion})
	}
	if utf8.RuneCountInString(q) > maxQuestionRunes {
		q = string([]rune(q)[:maxQuestionRunes])
	}
	return q, nil
}

const maxQuestionRunes = 1000
