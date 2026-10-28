package aiapitest

import (
	"context"
	"errors"
	"testing"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
)

// The fake answers whatever profile a request names, the optimizer's reviewer and the console's report included,
// and records the profile, the messages and the schema so that a test can check what was asked.
func TestFakeServesEveryChatProfile(t *testing.T) {
	for _, profile := range aiapi.ChatProfiles {
		t.Run(string(profile), func(t *testing.T) {
			f := New(Say("one"), Say("two", "t", "wo"))
			req := aiapi.ChatRequest{
				Profile: profile, Messages: []aiapi.Message{aiapi.SystemText("rules"), aiapi.UserText("facts")},
				JSONSchema: &aiapi.JSONSchema{Name: "answer", Schema: []byte(`{"type":"object"}`)},
			}
			resp, err := f.Chat(t.Context(), req)
			if err != nil || resp.Message.Content != "one" || resp.FinishReason != aiapi.FinishStop {
				t.Fatalf("Chat = %+v, %v", resp, err)
			}
			var streamed string
			resp, err = f.ChatStream(t.Context(), req, func(d aiapi.Delta) error { streamed += d.Content; return nil })
			if err != nil || resp.Message.Content != "two" || streamed != "two" {
				t.Fatalf("ChatStream = %+v, streamed %q, %v", resp, streamed, err)
			}
			got := f.RequestsFor(profile)
			if len(got) != 2 || got[0].Profile != profile || got[0].JSONSchema == nil || got[0].Messages[1].Content != "facts" {
				t.Errorf("requests for %s = %+v", profile, got)
			}
			if other := f.RequestsFor("nobody"); len(other) != 0 {
				t.Errorf("requests for another profile = %+v", other)
			}
		})
	}
}

func TestFakeKeepsProfilesApartAndScriptsFailures(t *testing.T) {
	boom := errors.New("gateway down")
	f := New(Say("review answer"), Fail(boom), Say("report answer"))
	ask := func(profile aiapi.Profile) (string, error) {
		resp, err := f.Chat(context.Background(), aiapi.ChatRequest{Profile: profile, Messages: []aiapi.Message{aiapi.UserText("x")}})
		return resp.Message.Content, err
	}
	if text, err := ask(aiapi.ProfileReview); err != nil || text != "review answer" {
		t.Fatalf("review: %q, %v", text, err)
	}
	if _, err := ask(aiapi.ProfileReport); !errors.Is(err, boom) {
		t.Fatalf("report: %v", err)
	}
	if text, err := ask(aiapi.ProfileReport); err != nil || text != "report answer" {
		t.Fatalf("report again: %q, %v", text, err)
	}
	if len(f.RequestsFor(aiapi.ProfileReview)) != 1 || len(f.RequestsFor(aiapi.ProfileReport)) != 2 || f.Calls() != 3 || f.Remaining() != 0 {
		t.Errorf("review %d, report %d, calls %d, remaining %d", len(f.RequestsFor(aiapi.ProfileReview)), len(f.RequestsFor(aiapi.ProfileReport)), f.Calls(), f.Remaining())
	}
	if _, err := ask(aiapi.ProfileReport); err == nil {
		t.Error("a call beyond the script should fail loudly")
	}
}
