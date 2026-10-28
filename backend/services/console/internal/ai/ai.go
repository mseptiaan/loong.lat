// Package ai is the console's AI features for the shipper: the Copilot that answers questions about one of their
// plans, the explanation of the route options they have to choose between, reading a photo or a recording into
// shipment items, and turning a sentence into the priority and deadline chips of the wizard. It also describes the
// photo a driver took as proof of delivery (AnalyzeProof, called by the proofs worker rather than by a request).
// The model is Meta's, reached only through the AI gateway (pkg/aiapi); this package decides
// what the model may see and what becomes of what it says.
//
// Principles, all of them enforced here and covered by tests:
//
//   - The model only reads and proposes. The Copilot's tools read the caller's own plan and nothing else (a plan id
//     of someone else is NotFound before any model call), and an action is only ever a proposal the screen offers
//     as a button.
//   - Numbers come from the server. Option comparisons are computed in Go; the model is told to quote them, and
//     every figure a tool returns carries its text already formatted in the caller's language. The explanation of the
//     options (ai.explainOptions) goes further: every number in the text must be one of the facts it was written from,
//     or the text is not shown.
//   - Model output is data, not trust: everything it returns (items, weights, chips, deadlines) is validated,
//     clamped or dropped before it reaches a screen, and a bad answer degrades to "fewer chips", not to an error.
//   - The admin's switches (copilot, vision, voice, intent) are respected on every call, and each customer is
//     rate limited so one person cannot spend the Meta budget.
//   - Nothing the customer sent (a photo, a recording, a question) is logged; only sizes, counts and timings.
package ai

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/auth"
)

// Message keys of the frontend catalog the AI methods answer with.
const (
	msgAIDisabled   = "srv.api.aiDisabled"
	msgAIError      = "srv.api.aiError"
	msgFileNotImage = "srv.api.fileNotImage"
	msgImageTooBig  = "srv.api.imageTooLarge"
	msgFileEmpty    = "srv.api.fileEmpty"
	msgNoSpeech     = "srv.api.noSpeech"
	msgIntentEmpty  = "srv.api.intentEmpty"
	msgQuestionNone = "srv.api.questionEmpty"
	msgPlanNotFound = "srv.ops.err.planNotFound"
	msgRoleDenied   = platform.MsgRoleDenied
)

// feature is one of the admin's AI switches, with the name of the feature in the messages that mention it.
type feature struct {
	name  string // for logs
	label string // catalog key of the feature's name (srv.api.feature.*)
	on    func(contracts.AIFeatures) bool
}

var (
	featCopilot = feature{"copilot", "srv.api.feature.copilot", func(f contracts.AIFeatures) bool { return f.Copilot }}
	featVision  = feature{"vision", "srv.api.feature.vision", func(f contracts.AIFeatures) bool { return f.Vision }}
	featVoice   = feature{"voice", "srv.api.feature.voice", func(f contracts.AIFeatures) bool { return f.Voice }}
	featIntent  = feature{"intent", "srv.api.feature.intent", func(f contracts.AIFeatures) bool { return f.Intent }}
)

func (f feature) msg(key string) contracts.Msg {
	return contracts.Msg{Key: key, Params: map[string]any{"feature": contracts.Msg{Key: f.label}}}
}

// Defaults of the per-customer rate limit: a burst of Burst requests, then RatePerMinute a minute. A Copilot
// question is one request however many tools it uses.
const (
	DefaultRatePerMinute = 20
	DefaultBurst         = 10
)

// Options wires a Service.
type Options struct {
	// Gateway is the AI gateway; nil (no AI_URL) makes every method answer as if the feature were unavailable.
	Gateway aiapi.Gateway
	Pool    *pgxpool.Pool
	// Flags reports the admin's switches as the settings projection serves them (already false when there is no
	// gateway).
	Flags func(ctx context.Context) (contracts.AIFeatures, error)
	// BalanceRp reads a customer's wallet balance.
	BalanceRp func(ctx context.Context, customerID string) (int64, error)
	Log       *slog.Logger
	// RatePerMinute and Burst bound each customer's AI requests; zero takes the defaults.
	RatePerMinute int
	Burst         int
	// Now is the clock (tests).
	Now func() time.Time
}

// Service serves the ai.* methods.
type Service struct {
	gw      aiapi.Gateway
	pool    *pgxpool.Pool
	flags   func(context.Context) (contracts.AIFeatures, error)
	balance func(context.Context, string) (int64, error)
	limiter *platform.LoginLimiter
	log     *slog.Logger
	now     func() time.Time
}

// New returns a Service.
func New(o Options) *Service {
	s := &Service{gw: o.Gateway, pool: o.Pool, flags: o.Flags, balance: o.BalanceRp, log: o.Log, now: o.Now}
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

// enter is the door of every method: the caller must be a signed-in customer who is not blocked, the admin must
// not have switched the feature off, and the customer must be within their rate. The order matters: a refused
// feature does not spend the customer's rate.
func (s *Service) enter(ctx context.Context, f feature) (customerID string, err error) {
	if customerID, err = auth.Customer(ctx); err != nil {
		return "", err
	}
	flags, err := s.flags(ctx)
	if err != nil {
		return "", err
	}
	if !f.on(flags) {
		return "", platform.Denied(f.msg(msgAIDisabled))
	}
	if err := auth.EnsureActive(ctx, s.pool, customerID); err != nil {
		return "", err
	}
	if ok, wait := s.limiter.Allow(customerID); !ok {
		return "", platform.TooManyRequests(wait)
	}
	if s.gw == nil {
		return "", platform.Unavailable(f.msg(msgAIError)).WithCause(errors.New("no AI gateway is configured (AI_URL)"))
	}
	return customerID, nil
}

// fail turns what went wrong with a feature into the error to answer with. The caller's own cancellation stays
// as it is; the gateway's refusal of input (invalidField names the field of the request that carried it, empty
// when the caller could not have caused it) becomes a validation error; a gateway that is down, slow or cut off, a
// model that misbehaved, anything else: "this feature is having problems", with the cause kept for the logs.
func (s *Service) fail(ctx context.Context, f feature, err error, invalidField string) error {
	if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return err
	}
	if _, isAPI := errors.AsType[*platform.APIError](err); isAPI && !isGateway(err) {
		return err // one of ours, already shaped for the caller
	}
	if ge, ok := errors.AsType[*aiapi.Error](err); ok {
		switch {
		case errors.Is(ge, aiapi.ErrRateLimited):
			return ge // 429 with the wait, as the gateway said it
		case errors.Is(ge, aiapi.ErrInvalid) && invalidField != "":
			return platform.Invalid(invalidField, ge.Msg).WithCause(ge)
		}
	}
	return platform.Unavailable(f.msg(msgAIError)).WithCause(err)
}

func isGateway(err error) bool {
	_, ok := errors.AsType[*aiapi.Error](err)
	return ok
}

// requestID names one AI answer in the screen and in the logs.
func requestID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "req-" + hex.EncodeToString(b[:])
}

// Mount adds the ai.* methods this service answers; bodies are named after the frontend's parameters.
func (s *Service) Mount(r *platform.RPC) {
	r.Stream("ai.copilot", s.copilot)
	r.Stream("ai.explainOptions", s.explainOptions)
	r.Stream("ai.readImage", s.readImage)
	r.Handle("ai.transcribe", s.transcribe)
	r.Handle("ai.parseSpeech", s.parseSpeech)
	r.Handle("ai.parseIntent", s.parseIntent)
}
