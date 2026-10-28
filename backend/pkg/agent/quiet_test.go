package agent_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mseptiaan/loong.lat/backend/pkg/agent"
	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi/aiapitest"
)

// stream runs one model call and returns the text the callback saw, piece by piece.
func stream(t *testing.T, m aiapi.Model, req aiapi.ChatRequest) (pieces []string, resp aiapi.ChatResponse, err error) {
	t.Helper()
	resp, err = m.ChatStream(t.Context(), req, func(d aiapi.Delta) error {
		if d.Content != "" {
			pieces = append(pieces, d.Content)
		}
		return nil
	})
	return pieces, resp, err
}

var withTools = aiapi.ChatRequest{Profile: aiapi.ProfileCopilot, Messages: ask("hi"), Tools: []aiapi.Tool{{Name: "get_plan"}}}

func TestQuietChatterDropsWhatIsSaidWhileAskingForToolsAndReleasesTheAnswer(t *testing.T) {
	model := agent.QuietChatter(aiapitest.New(
		aiapitest.Step{Chunks: []string{"I'll pull ", "your plan."}, Calls: []aiapi.ToolCall{aiapitest.Call("c1", "get_plan", `{}`)}},
		aiapitest.Say("The cheapest option is X.", "The cheapest ", "option is X."),
	))
	pieces, resp, err := stream(t, model, withTools)
	if err != nil || len(pieces) != 0 {
		t.Fatalf("a turn that asks for tools must show nothing: %q, %v", pieces, err)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.Content != "I'll pull your plan." {
		t.Errorf("the reply is still whole: %+v", resp)
	}
	pieces, resp, err = stream(t, model, withTools)
	if err != nil || strings.Join(pieces, "|") != "The cheapest |option is X." || resp.Message.Content != "The cheapest option is X." {
		t.Errorf("the answer comes out in the pieces it came in: %q %+v %v", pieces, resp, err)
	}
}

func TestQuietChatterHoldsTheAnswerUntilItsTurnEnds(t *testing.T) {
	var sentBeforeEnd atomic.Int32
	var delivered atomic.Int32
	model := agent.QuietChatter(aiapitest.New(aiapitest.Step{
		Chunks: []string{"one ", "two"},
		Hold:   func(context.Context) { sentBeforeEnd.Store(delivered.Load()) },
	}))
	if _, err := model.ChatStream(t.Context(), withTools, func(aiapi.Delta) error { delivered.Add(1); return nil }); err != nil {
		t.Fatal(err)
	}
	if sentBeforeEnd.Load() != 0 || delivered.Load() != 2 {
		t.Errorf("%d pieces were delivered before the turn ended, %d in all", sentBeforeEnd.Load(), delivered.Load())
	}
}

func TestQuietChatterLeavesCallsWithoutToolsAlone(t *testing.T) {
	var sentBeforeEnd, delivered atomic.Int32
	model := agent.QuietChatter(aiapitest.New(aiapitest.Step{
		Chunks: []string{"one ", "two"},
		Hold:   func(context.Context) { sentBeforeEnd.Store(delivered.Load()) },
	}))
	req := withTools
	req.Tools = nil
	if _, err := model.ChatStream(t.Context(), req, func(aiapi.Delta) error { delivered.Add(1); return nil }); err != nil {
		t.Fatal(err)
	}
	if sentBeforeEnd.Load() != 2 {
		t.Errorf("a call without tools should stream as it comes: %d pieces before the end", sentBeforeEnd.Load())
	}
}

func TestQuietChatterPassesErrorsAndCancellationOn(t *testing.T) {
	boom := errors.New("gateway down")
	pieces, _, err := stream(t, agent.QuietChatter(aiapitest.New(aiapitest.Fail(boom))), withTools)
	if !errors.Is(err, boom) || len(pieces) != 0 {
		t.Errorf("err %v, pieces %q", err, pieces)
	}

	// A caller that stops while the model is still talking ends the call.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	model := agent.QuietChatter(aiapitest.New(aiapitest.Step{Chunks: []string{"a", "b", "c"}}))
	calls := 0
	_, err = model.ChatStream(ctx, withTools, func(aiapi.Delta) error { calls++; return nil })
	if err != nil || calls != 3 {
		t.Fatalf("baseline: %v, %d pieces", err, calls)
	}
	model = agent.QuietChatter(aiapitest.New(aiapitest.Step{Chunks: []string{"a", "b"}, Hold: func(context.Context) { cancel() }}))
	if _, err := model.ChatStream(ctx, withTools, func(aiapi.Delta) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}

	// A failing callback stops the release and is returned as it is.
	stop := errors.New("screen gone")
	n := 0
	_, err = agent.QuietChatter(aiapitest.New(aiapitest.Say("abc", "a", "b", "c"))).ChatStream(t.Context(), withTools, func(aiapi.Delta) error {
		n++
		return stop
	})
	if err != stop || n != 1 {
		t.Errorf("err %v after %d pieces", err, n)
	}
}

func TestQuietChatterKeepsChatterOutOfAnAgentRunsAnswer(t *testing.T) {
	var runs atomic.Int32
	script := func() *aiapitest.Fake {
		return aiapitest.New(
			aiapitest.Step{Chunks: []string{"I'll pull your plan ", "and compare the options."}, Calls: []aiapi.ToolCall{aiapitest.Call("c1", "get_plan", `{"id":"PL-1"}`)}},
			aiapitest.Step{Chunks: []string{"That fits your priority, "}, Calls: []aiapi.ToolCall{aiapitest.Call("c2", "get_plan", `{"id":"PL-1"}`)}},
			aiapitest.Say("PL-1 is on its way.", "PL-1 ", "is on its way."),
		)
	}
	cfg := agent.Config{Tools: []agent.Tool{planTool(&runs)}}

	// Without the wrapper the chatter reaches the observer; with it only the answer does.
	loud := &events{}
	if _, err := agent.Run(t.Context(), script(), cfg, ask("where?"), loud); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loud.joined(), "I'll pull your plan") {
		t.Fatalf("expected the unwrapped model to leak its chatter: %s", loud.joined())
	}
	quietly := &events{}
	out, err := agent.Run(t.Context(), agent.QuietChatter(script()), cfg, ask("where?"), quietly)
	if err != nil {
		t.Fatal(err)
	}
	var tokens []string
	for _, e := range quietly.log {
		if text, ok := strings.CutPrefix(e, "token:"); ok {
			tokens = append(tokens, text)
		}
	}
	if !slices.Equal(tokens, []string{"PL-1 ", "is on its way."}) || out.Text != "PL-1 is on its way." {
		t.Errorf("tokens %q, answer %q", tokens, out.Text)
	}
	if strings.Contains(quietly.joined(), "I'll") || strings.Contains(quietly.joined(), "fits your priority") {
		t.Errorf("chatter leaked: %s", quietly.joined())
	}
	if want := "start:get_plan finish:get_plan start:get_plan finish:get_plan token:PL-1  token:is on its way. done"; quietly.joined() != want {
		t.Errorf("events = %s\nwant     %s", quietly.joined(), want)
	}
}
