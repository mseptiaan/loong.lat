package meta

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"os"
	"testing"
	"time"
)

// TestLive talks to the real Model API. It is skipped unless LOONG_LIVE_META=1 and costs a handful of calls:
//
//	cd backend && set -a && . ./.env && set +a && LOONG_LIVE_META=1 go test -count=1 -run Live -v ./pkg/meta
//
// The key comes from META_API_KEY (and META_BASE_URL / META_MODEL when set). It is never printed: only latency,
// finish reasons and token counts are logged.
func TestLive(t *testing.T) {
	if os.Getenv("LOONG_LIVE_META") != "1" {
		t.Skip("set LOONG_LIVE_META=1 (and META_API_KEY) to call the real Model API")
	}
	key := os.Getenv("META_API_KEY")
	if key == "" {
		t.Fatal("LOONG_LIVE_META=1 needs META_API_KEY in the environment")
	}
	c := New(Config{
		BaseURL: os.Getenv("META_BASE_URL"), APIKey: Secret(key),
		Model: os.Getenv("META_MODEL"), TranscribeModel: os.Getenv("META_TRANSCRIBE_MODEL"),
		Timeout: 45 * time.Second,
	})

	t.Run("chat_minimal_reasoning", func(t *testing.T) {
		start := time.Now()
		got, err := c.Chat(t.Context(), ChatRequest{
			Messages: []Message{
				{Role: RoleSystem, Content: "Answer in one short sentence."},
				{Role: RoleUser, Content: "Say hello to the loong.lat team."},
			},
			ReasoningEffort: EffortMinimal, MaxCompletionTokens: 1500,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("latency %v, finish %q, tokens %+v", time.Since(start).Round(time.Millisecond), got.FinishReason, got.Usage)
		if got.Message.Content == "" || got.FinishReason != FinishStop || got.Usage.TotalTokens == 0 {
			t.Errorf("unexpected reply: finish %q, %d chars, usage %+v", got.FinishReason, len(got.Message.Content), got.Usage)
		}
	})

	t.Run("chat_stream", func(t *testing.T) {
		start := time.Now()
		var first time.Duration
		var fragments int
		got, err := c.ChatStream(t.Context(), ChatRequest{
			Messages:        []Message{{Role: RoleUser, Content: "Count from one to five in words, separated by commas."}},
			ReasoningEffort: EffortMinimal, MaxCompletionTokens: 1500,
		}, func(d Delta) error {
			if d.Content != "" {
				if fragments == 0 {
					first = time.Since(start)
				}
				fragments++
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("latency %v (first text after %v), %d fragments, finish %q, tokens %+v",
			time.Since(start).Round(time.Millisecond), first.Round(time.Millisecond), fragments, got.FinishReason, got.Usage)
		if got.Message.Content == "" || fragments == 0 || got.Usage.TotalTokens == 0 {
			t.Errorf("unexpected stream: %d fragments, %d chars, usage %+v", fragments, len(got.Message.Content), got.Usage)
		}
	})

	t.Run("tool_call_round_trip", func(t *testing.T) {
		tools := []Tool{{
			Name:        "get_plan_status",
			Description: "Returns the delivery status of a plan by its code.",
			Parameters:  jsontext.Value(`{"type":"object","properties":{"plan_id":{"type":"string","description":"Plan code such as PL-2041"}},"required":["plan_id"]}`),
		}}
		history := []Message{
			{Role: RoleSystem, Content: "You are a dispatch assistant. Use the tools to look things up; never guess."},
			{Role: RoleUser, Content: "What is the status of plan PL-2041? Use the tool."},
		}
		start := time.Now()
		first, err := c.Chat(t.Context(), ChatRequest{Messages: history, Tools: tools, ReasoningEffort: EffortMinimal, MaxCompletionTokens: 1500})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("call 1: latency %v, finish %q, %d tool calls, tokens %+v", time.Since(start).Round(time.Millisecond), first.FinishReason, len(first.Message.ToolCalls), first.Usage)
		if len(first.Message.ToolCalls) != 1 {
			t.Fatalf("want one tool call, got %d (finish %q)", len(first.Message.ToolCalls), first.FinishReason)
		}
		call := first.Message.ToolCalls[0]
		var args struct {
			PlanID string `json:"plan_id"`
		}
		if call.Name != "get_plan_status" || call.ID == "" || json.Unmarshal([]byte(call.Arguments), &args) != nil || args.PlanID != "PL-2041" {
			t.Fatalf("unexpected call: name %q, id set %v, arguments %q", call.Name, call.ID != "", call.Arguments)
		}

		// Echo the assistant turn and answer the call. The API supports only tool_choice "auto", so a last step is
		// steered with a system note instead (what pkg/agent does).
		history = append(history, first.Message, Message{Role: RoleTool, ToolCallID: call.ID, Content: `{"planId":"PL-2041","status":"berjalan","etaMinutes":12}`},
			Message{Role: RoleSystem, Content: "This is your last step: answer the user now from the tool results. Do not call any tool."})
		start = time.Now()
		final, err := c.Chat(t.Context(), ChatRequest{Messages: history, Tools: tools, ReasoningEffort: EffortMinimal, MaxCompletionTokens: 1500})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("call 2: latency %v, finish %q, tokens %+v", time.Since(start).Round(time.Millisecond), final.FinishReason, final.Usage)
		if final.Message.Content == "" || len(final.Message.ToolCalls) != 0 {
			t.Errorf("want a final text answer with no further calls: %d chars, %d calls", len(final.Message.Content), len(final.Message.ToolCalls))
		}
	})

	t.Run("json_schema", func(t *testing.T) {
		schema := jsontext.Value(`{
			"type":"object",
			"properties":{"items":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},"qty":{"type":"integer"}},"required":["name","qty"],"additionalProperties":false}}},
			"required":["items"],"additionalProperties":false}`)
		start := time.Now()
		got, err := c.Chat(t.Context(), ChatRequest{
			Messages: []Message{
				{Role: RoleSystem, Content: "Extract the shipment items from the text."},
				{Role: RoleUser, Content: "I am sending 2 boxes of books and 1 laptop."},
			},
			JSONSchema:      &JSONSchema{Name: "shipment_items", Schema: schema, Strict: true},
			ReasoningEffort: EffortMinimal, MaxCompletionTokens: 1500,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("latency %v, finish %q, tokens %+v", time.Since(start).Round(time.Millisecond), got.FinishReason, got.Usage)
		var out struct {
			Items []struct {
				Name string `json:"name"`
				Qty  int    `json:"qty"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(got.Message.Content), &out); err != nil || len(out.Items) == 0 {
			t.Fatalf("the reply is not the requested document: %v (%d chars)", err, len(got.Message.Content))
		}
	})

	// A second of silence checks that the transcription endpoint accepts our multipart request. What it answers
	// is informational: a key without voice access, or "no speech", must not fail the smoke test.
	t.Run("transcribe_silence", func(t *testing.T) {
		start := time.Now()
		got, err := c.Transcribe(t.Context(), wav(1, 1, 16000, 16, 32000), "silence.wav", "audio/wav", WithLanguageBias("Indonesian"))
		if err != nil {
			if e, ok := errors.AsType[*Error](err); ok {
				t.Logf("informational: latency %v, kind %s, http %d, code %q", time.Since(start).Round(time.Millisecond), e.Kind, e.Status, e.Code)
				return
			}
			t.Fatal(err)
		}
		t.Logf("informational: latency %v, %d ms of audio, %d chars", time.Since(start).Round(time.Millisecond), got.DurationMs, len(got.Text))
	})
}
