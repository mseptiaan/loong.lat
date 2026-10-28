// Package profile holds the fixed settings of each use of the model. A caller names a profile; model, reasoning
// effort, token budget, time limit and the largest request are the gateway's to decide, so that one feature cannot
// spend another's budget or send a megabyte where a sentence is expected.
package profile

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/meta"
)

// MinMaxTokens is the smallest token budget accepted. Reasoning tokens count against it: a smaller one can leave
// no room for an answer (the call then fails with finish_reason "length" and no content).
const MinMaxTokens = 1500

// Profile is the fixed settings of one use.
type Profile struct {
	Name  aiapi.Profile
	Model string
	// ReasoningEffort is minimal, low, medium or high ("none" is refused by the model). Empty for transcribe.
	ReasoningEffort string
	// MaxTokens is max_completion_tokens: visible output plus reasoning.
	MaxTokens int
	// Timeout bounds the whole call to Meta, retries included.
	Timeout time.Duration
	// MaxBodyBytes bounds the request body as received.
	MaxBodyBytes int64
	// Tools allows tool declarations and tool-call messages; Images allows image parts.
	Tools  bool
	Images bool
}

// Image limits of a vision request.
const (
	MaxImages     = 4
	MaxImageBytes = 4 << 20
)

// Set is every profile by name.
type Set map[aiapi.Profile]Profile

// Defaults are the built-in profiles. model is the chat model, transcribeModel the speech model.
//
// Reasoning effort is what an answer costs in time: each model call at "low" took 5 to 7 s and at "minimal" 2 to 3 s.
// The copilot and the insights narration run on "minimal" because, measured against Meta's model with the real
// prompts (the live tests of the console and back office, LOONG_LIVE_GATEWAY_ENV=AI_COPILOT_EFFORT=low ...), the
// answers stayed as good: every number was still one a tool returned, the claims were right, the proposed actions
// were the right ones, and an answer took 4 to 16 s instead of 8 to 34 s, a narration 5 to 10 s instead of 21 to 31 s.
// Vision reads a photo and estimates weights, which repays some thinking, so it stays on "low". The optimizer's
// reviewer (review) picks the next experiments from a handful of facts and answers as one small JSON document, and the
// report narrates figures the server already formatted, so both run on "minimal" as well; their tokens are budgeted
// for the answer plus a little reasoning. Every effort can be changed with AI_<PROFILE>_EFFORT.
func Defaults(model, transcribeModel string) Set {
	return Set{
		aiapi.ProfileCopilot: {
			Name: aiapi.ProfileCopilot, Model: model, ReasoningEffort: meta.EffortMinimal, MaxTokens: 3000,
			Timeout: 60 * time.Second, MaxBodyBytes: 512 << 10, Tools: true,
		},
		aiapi.ProfileVision: {
			Name: aiapi.ProfileVision, Model: model, ReasoningEffort: meta.EffortLow, MaxTokens: 4000,
			// A 4 MiB image is 5.4 MiB as base64, plus the prompt.
			Timeout: 60 * time.Second, MaxBodyBytes: 6 << 20, Images: true,
		},
		aiapi.ProfileIntent: {
			Name: aiapi.ProfileIntent, Model: model, ReasoningEffort: meta.EffortMinimal, MaxTokens: MinMaxTokens,
			Timeout: 20 * time.Second, MaxBodyBytes: 64 << 10,
		},
		aiapi.ProfileInsights: {
			Name: aiapi.ProfileInsights, Model: model, ReasoningEffort: meta.EffortMinimal, MaxTokens: 3000,
			Timeout: 45 * time.Second, MaxBodyBytes: 256 << 10,
		},
		// The optimizer's reviewer: facts about the candidates in, the next experiments out as JSON (the schema is the
		// caller's). It runs once per review round of a quote, so it has to be quick.
		aiapi.ProfileReview: {
			Name: aiapi.ProfileReview, Model: model, ReasoningEffort: meta.EffortMinimal, MaxTokens: 2000,
			Timeout: 20 * time.Second, MaxBodyBytes: 64 << 10,
		},
		// The console's explanation of a quote's options, written from figures the server already formatted.
		aiapi.ProfileReport: {
			Name: aiapi.ProfileReport, Model: model, ReasoningEffort: meta.EffortMinimal, MaxTokens: 3000,
			Timeout: 45 * time.Second, MaxBodyBytes: 256 << 10,
		},
		aiapi.ProfileTranscribe: {
			Name: aiapi.ProfileTranscribe, Model: transcribeModel,
			Timeout: 30 * time.Second, MaxBodyBytes: 8 << 20,
		},
	}
}

// Names lists every profile in a stable order.
func Names() []aiapi.Profile {
	return append(slices.Clone(aiapi.ChatProfiles), aiapi.ProfileTranscribe)
}

// EnvPrefix is the prefix of a profile's overrides: AI_COPILOT_MODEL, AI_COPILOT_EFFORT, AI_COPILOT_MAX_TOKENS,
// AI_COPILOT_TIMEOUT, AI_COPILOT_MAX_BODY_BYTES.
func EnvPrefix(name aiapi.Profile) string { return "AI_" + strings.ToUpper(string(name)) + "_" }

// Validate checks one profile's settings.
func (p Profile) Validate() error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("profile %s: %s", p.Name, fmt.Sprintf(format, args...))
	}
	if p.Model == "" {
		return fail("no model")
	}
	if p.Timeout <= 0 {
		return fail("timeout must be positive")
	}
	if p.MaxBodyBytes <= 0 {
		return fail("max body bytes must be positive")
	}
	if p.Name == aiapi.ProfileTranscribe {
		return nil
	}
	switch p.ReasoningEffort {
	case meta.EffortMinimal, meta.EffortLow, meta.EffortMedium, meta.EffortHigh:
	default:
		return fail("reasoning effort %q is not minimal, low, medium or high (the model refuses none)", p.ReasoningEffort)
	}
	if p.MaxTokens < MinMaxTokens {
		return fail("max tokens %d is below %d: reasoning tokens count against it, so less can leave no room for an answer", p.MaxTokens, MinMaxTokens)
	}
	return nil
}

// Validate checks every profile, and that none is missing.
func (s Set) Validate() error {
	for _, name := range Names() {
		p, ok := s[name]
		if !ok {
			return fmt.Errorf("profile %s is missing", name)
		}
		if p.Name != name {
			return fmt.Errorf("profile %s is filed under the name %q", p.Name, name)
		}
		if err := p.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Infos describes the profiles for GET /v1/info.
func (s Set) Infos() []aiapi.ProfileInfo {
	var out []aiapi.ProfileInfo
	for _, name := range Names() {
		p := s[name]
		out = append(out, aiapi.ProfileInfo{
			Name: p.Name, Model: p.Model, ReasoningEffort: p.ReasoningEffort, MaxTokens: p.MaxTokens,
			TimeoutMs: p.Timeout.Milliseconds(), MaxRequestBytes: p.MaxBodyBytes,
		})
	}
	return out
}

// MaxChatBody is the largest body any chat profile accepts.
func (s Set) MaxChatBody() int64 {
	var most int64
	for _, name := range aiapi.ChatProfiles {
		most = max(most, s[name].MaxBodyBytes)
	}
	return most
}
