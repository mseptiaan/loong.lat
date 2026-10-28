package profile

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
)

func TestDefaultsAreValidAndSafeAgainstTheReasoningBudgetTrap(t *testing.T) {
	set := Defaults("chat-model", "voice-model")
	if err := set.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, name := range Names() {
		p := set[name]
		if p.Name != name || p.Timeout <= 0 || p.MaxBodyBytes <= 0 {
			t.Errorf("%s: %+v", name, p)
		}
		if name == aiapi.ProfileTranscribe {
			if p.Model != "voice-model" {
				t.Errorf("transcribe model = %q", p.Model)
			}
			continue
		}
		if p.Model != "chat-model" {
			t.Errorf("%s model = %q", name, p.Model)
		}
		// Reasoning tokens count against max_completion_tokens: a small budget returns no content at all.
		if p.MaxTokens < MinMaxTokens || (p.ReasoningEffort != "minimal" && p.ReasoningEffort != "low") {
			t.Errorf("%s: effort %q, max tokens %d", name, p.ReasoningEffort, p.MaxTokens)
		}
	}
	if !set[aiapi.ProfileCopilot].Tools || set[aiapi.ProfileCopilot].Images || !set[aiapi.ProfileVision].Images || set[aiapi.ProfileVision].Tools {
		t.Error("only copilot takes tools and only vision takes images")
	}
	if got := set.MaxChatBody(); got != 6<<20 {
		t.Errorf("MaxChatBody = %d: the vision profile fits a 4 MiB photo as base64", got)
	}
	infos := set.Infos()
	if len(infos) != 7 || infos[0].Name != aiapi.ProfileCopilot || infos[6].Name != aiapi.ProfileTranscribe || infos[0].TimeoutMs != 60000 {
		t.Errorf("infos = %+v", infos)
	}
	var listed []aiapi.Profile
	for _, in := range infos {
		listed = append(listed, in.Name)
	}
	if want := []aiapi.Profile{aiapi.ProfileCopilot, aiapi.ProfileVision, aiapi.ProfileIntent, aiapi.ProfileInsights, aiapi.ProfileReview, aiapi.ProfileReport, aiapi.ProfileTranscribe}; !slices.Equal(listed, want) {
		t.Errorf("profiles listed = %v, want %v", listed, want)
	}
	if EnvPrefix(aiapi.ProfileCopilot) != "AI_COPILOT_" {
		t.Errorf("prefix = %q", EnvPrefix(aiapi.ProfileCopilot))
	}
}

// The efforts are a measured decision (see Defaults): the Copilot and the narration are quick enough on "minimal" to be
// used, vision keeps "low". A change here should come with new measurements.
func TestDefaultEffortsAreTheMeasuredOnes(t *testing.T) {
	set := Defaults("m", "v")
	for name, want := range map[aiapi.Profile]string{
		aiapi.ProfileCopilot: "minimal", aiapi.ProfileInsights: "minimal", aiapi.ProfileIntent: "minimal", aiapi.ProfileVision: "low",
	} {
		if got := set[name].ReasoningEffort; got != want {
			t.Errorf("%s effort = %q, want %q", name, got, want)
		}
	}
}

func TestValidateRejectsUnsafeSettings(t *testing.T) {
	cases := map[string]struct {
		mod  func(*Profile)
		want string
	}{
		"no model":        {func(p *Profile) { p.Model = "" }, "no model"},
		"effort none":     {func(p *Profile) { p.ReasoningEffort = "none" }, "reasoning effort"},
		"effort empty":    {func(p *Profile) { p.ReasoningEffort = "" }, "reasoning effort"},
		"effort typo":     {func(p *Profile) { p.ReasoningEffort = "lo" }, "reasoning effort"},
		"tiny budget":     {func(p *Profile) { p.MaxTokens = 200 }, "max tokens"},
		"no timeout":      {func(p *Profile) { p.Timeout = 0 }, "timeout"},
		"no body limit":   {func(p *Profile) { p.MaxBodyBytes = 0 }, "max body"},
		"negative budget": {func(p *Profile) { p.MaxTokens = -1 }, "max tokens"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			set := Defaults("m", "v")
			p := set[aiapi.ProfileIntent]
			tc.mod(&p)
			set[aiapi.ProfileIntent] = p
			err := set.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "intent") {
				t.Errorf("err = %v, want it to name the intent profile and %q", err, tc.want)
			}
		})
	}

	set := Defaults("m", "v")
	delete(set, aiapi.ProfileVision)
	if err := set.Validate(); err == nil || !strings.Contains(err.Error(), "vision is missing") {
		t.Errorf("a missing profile: %v", err)
	}
	set = Defaults("m", "v")
	p := set[aiapi.ProfileTranscribe]
	p.ReasoningEffort, p.MaxTokens, p.Timeout = "", 0, 5*time.Second
	set[aiapi.ProfileTranscribe] = p
	if err := set.Validate(); err != nil {
		t.Errorf("transcribe has no reasoning settings: %v", err)
	}
}

// The optimizer's reviewer and the console's report are chat profiles with a fixed shape: no tools, no images, a
// small budget and a short time limit for the reviewer (it runs inside a quote), a narration-sized one for the report.
func TestReviewAndReportProfiles(t *testing.T) {
	set := Defaults("chat-model", "voice-model")
	for name, want := range map[aiapi.Profile]Profile{
		aiapi.ProfileReview: {MaxTokens: 2000, Timeout: 20 * time.Second, MaxBodyBytes: 64 << 10},
		aiapi.ProfileReport: {MaxTokens: 3000, Timeout: 45 * time.Second, MaxBodyBytes: 256 << 10},
	} {
		p, ok := set[name]
		if !ok {
			t.Fatalf("no %s profile", name)
		}
		if p.Tools || p.Images || p.ReasoningEffort != "minimal" || p.Model != "chat-model" ||
			p.MaxTokens != want.MaxTokens || p.Timeout != want.Timeout || p.MaxBodyBytes != want.MaxBodyBytes {
			t.Errorf("%s = %+v, want %+v without tools or images, on minimal", name, p, want)
		}
		if !slices.Contains(aiapi.ChatProfiles, name) || !slices.Contains(Names(), name) {
			t.Errorf("%s is not a chat profile served by /v1/chat", name)
		}
		if EnvPrefix(name) != "AI_"+strings.ToUpper(string(name))+"_" {
			t.Errorf("%s: prefix %q", name, EnvPrefix(name))
		}
	}
	if EnvPrefix(aiapi.ProfileReview) != "AI_REVIEW_" || EnvPrefix(aiapi.ProfileReport) != "AI_REPORT_" {
		t.Errorf("prefixes = %q, %q", EnvPrefix(aiapi.ProfileReview), EnvPrefix(aiapi.ProfileReport))
	}

	// A set that lacks one of them is refused, like any missing profile, and so is one with an unsafe setting.
	for _, name := range []aiapi.Profile{aiapi.ProfileReview, aiapi.ProfileReport} {
		broken := Defaults("m", "v")
		delete(broken, name)
		if err := broken.Validate(); err == nil || !strings.Contains(err.Error(), string(name)+" is missing") {
			t.Errorf("without %s: %v", name, err)
		}
		unsafe := Defaults("m", "v")
		p := unsafe[name]
		p.MaxTokens = 500
		unsafe[name] = p
		if err := unsafe.Validate(); err == nil || !strings.Contains(err.Error(), string(name)) || !strings.Contains(err.Error(), "max tokens") {
			t.Errorf("%s with a tiny budget: %v", name, err)
		}
	}
}
