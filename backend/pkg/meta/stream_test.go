package meta

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// sseWriter writes a stream the way a real server does: in arbitrary pieces, flushed between them.
type sseWriter struct {
	w http.ResponseWriter
	f http.Flusher
}

func newSSE(w http.ResponseWriter) *sseWriter {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	f, _ := w.(http.Flusher)
	return &sseWriter{w: w, f: f}
}

func (s *sseWriter) raw(piece string) {
	s.w.Write([]byte(piece))
	s.f.Flush()
	time.Sleep(2 * time.Millisecond) // let the client see the piece alone
}

// event writes one data event whole.
func (s *sseWriter) event(data string) { s.raw("data: " + data + "\n\n") }

func chunk(content string) string {
	return `{"id":"c1","model":"m","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":` + quote(content) + `},"finish_reason":null}]}`
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func finishChunk(reason string) string {
	return `{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"` + reason + `"}]}`
}

const usageChunk = `{"id":"c1","choices":[],"usage":{"prompt_tokens":9,"completion_tokens":5,"total_tokens":14,"completion_tokens_details":{"reasoning_tokens":2}}}`

func TestChatStreamAssemblesChunksSplitMidLine(t *testing.T) {
	c, api, _ := newClient(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) {
		s := newSSE(w)
		s.event(`{"id":"c1","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`)
		// One event cut into pieces in the middle of the field name, the JSON and the terminator.
		full := "data: " + chunk("Hel") + "\n\n"
		prev := 0
		for _, cut := range []int{3, 9, 40, len(full) - 1} {
			s.raw(full[prev:cut])
			prev = cut
		}
		s.raw(full[prev:])
		// CRLF line ends, a comment keep-alive, and a data field split over two lines.
		s.raw(": keep-alive\r\n\r\n")
		s.raw("data: " + chunk("lo, ") + "\r\n\r\n")
		s.raw("event: message\ndata: {\"id\":\"c1\",\"choices\":[{\"index\":0,\ndata: \"delta\":{\"content\":\"world\"},\"finish_reason\":null}]}\n\n")
		s.event(finishChunk("stop"))
		s.event(usageChunk)
		s.raw("data: [DONE]\n\n")
	})

	var deltas []string
	var finish string
	got, err := c.ChatStream(t.Context(), ChatRequest{Messages: userMsg("hi"), ReasoningEffort: EffortLow, MaxCompletionTokens: 1500}, func(d Delta) error {
		if d.Content != "" {
			deltas = append(deltas, d.Content)
		}
		if d.FinishReason != "" {
			finish = d.FinishReason
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(deltas, "|") != "Hel|lo, |world" {
		t.Errorf("deltas = %q", deltas)
	}
	if got.Message.Content != "Hello, world" || got.FinishReason != "stop" || finish != "stop" || got.ID != "c1" || got.Model != "m" {
		t.Errorf("response = %+v (finish seen by callback: %q)", got, finish)
	}
	if got.Usage.PromptTokens != 9 || got.Usage.CompletionTokens != 5 || got.Usage.ReasoningTokens != 2 {
		t.Errorf("usage = %+v", got.Usage)
	}

	body := decodeMap(t, api.request(0).Body)
	if body["stream"] != true || body["stream_options"].(map[string]any)["include_usage"] != true || body["reasoning_effort"] != "low" {
		t.Errorf("body = %v", body)
	}
	if api.request(0).Header.Get("Accept") != "text/event-stream" {
		t.Errorf("accept = %q", api.request(0).Header.Get("Accept"))
	}
}

func TestChatStreamAssemblesToolCallFragments(t *testing.T) {
	c, _, _ := newClient(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) {
		s := newSSE(w)
		tc := func(fragment string) string {
			return `{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[` + fragment + `]},"finish_reason":null}]}`
		}
		// Two calls interleaved by index; arguments arrive in pieces; the second server repeats the whole name.
		s.event(tc(`{"index":0,"id":"call_a","type":"function","function":{"name":"get_plan","arguments":""}}`))
		s.event(tc(`{"index":0,"function":{"arguments":"{\"id\":"}}`))
		s.event(tc(`{"index":1,"id":"call_b","type":"function","function":{"name":"list_","arguments":""}}`))
		s.event(tc(`{"index":1,"function":{"name":"drivers","arguments":"{}"}}`))
		s.event(tc(`{"index":0,"function":{"name":"get_plan","arguments":"\"PL-1\"}"}}`))
		s.event(finishChunk("tool_calls"))
		s.event(usageChunk)
		s.raw("data: [DONE]\n\n")
	})

	var fragments int
	got, err := c.ChatStream(t.Context(), ChatRequest{Messages: userMsg("hi"), Tools: []Tool{{Name: "get_plan"}}}, func(d Delta) error {
		fragments += len(d.ToolCalls)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.FinishReason != FinishToolCalls || got.Message.Content != "" || len(got.Message.ToolCalls) != 2 {
		t.Fatalf("response = %+v", got)
	}
	if want := (ToolCall{ID: "call_a", Name: "get_plan", Arguments: `{"id":"PL-1"}`}); got.Message.ToolCalls[0] != want {
		t.Errorf("call 0 = %+v, want %+v", got.Message.ToolCalls[0], want)
	}
	if want := (ToolCall{ID: "call_b", Name: "list_drivers", Arguments: `{}`}); got.Message.ToolCalls[1] != want {
		t.Errorf("call 1 = %+v, want %+v", got.Message.ToolCalls[1], want)
	}
	if fragments != 5 {
		t.Errorf("the callback should see every fragment, saw %d", fragments)
	}
}

func TestChatStreamCallbackErrorStopsTheStream(t *testing.T) {
	stopped := make(chan struct{})
	c, _, _ := newClient(t, func(_ int, w http.ResponseWriter, r *http.Request, _ []byte) {
		s := newSSE(w)
		defer close(stopped)
		for range 200 {
			s.event(chunk("x"))
			if r.Context().Err() != nil {
				return
			}
		}
	})
	boom := errors.New("client went away")
	var seen int
	got, err := c.ChatStream(t.Context(), ChatRequest{Messages: userMsg("hi")}, func(Delta) error {
		seen++
		if seen == 3 {
			return boom
		}
		return nil
	})
	if err != boom {
		t.Fatalf("err = %v, want the callback's own error", err)
	}
	if seen != 3 || got.Message.Content != "xxx" {
		t.Errorf("seen %d, partial %q", seen, got.Message.Content)
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Error("the request was not abandoned after the callback failed")
	}
}

func TestChatStreamErrorEventMidStream(t *testing.T) {
	c, _, _ := newClient(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) {
		s := newSSE(w)
		s.event(chunk("par"))
		s.event(`{"error":{"message":"the backend dropped the request","type":"server_error","code":"backend_unavailable"}}`)
	})
	var text string
	got, err := c.ChatStream(t.Context(), ChatRequest{Messages: userMsg("hi")}, func(d Delta) error { text += d.Content; return nil })
	e := wantKind(t, err, ErrServer)
	if e.Code != "backend_unavailable" || !strings.Contains(e.Message, "dropped") || text != "par" || got.Message.Content != "par" {
		t.Errorf("error %+v, text %q, partial %+v", e, text, got)
	}
}

func TestChatStreamEndingEarly(t *testing.T) {
	t.Run("finish reason without DONE is complete", func(t *testing.T) {
		c, _, _ := newClient(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) {
			s := newSSE(w)
			s.event(chunk("ok"))
			s.raw("data: " + finishChunk("stop")) // not even a blank line after it
		})
		got, err := c.ChatStream(t.Context(), ChatRequest{Messages: userMsg("hi")}, nil)
		if err != nil || got.Message.Content != "ok" || got.FinishReason != "stop" {
			t.Errorf("got %+v, %v", got, err)
		}
	})
	t.Run("cut off before the end is malformed", func(t *testing.T) {
		c, _, _ := newClient(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) {
			s := newSSE(w)
			s.event(chunk("half an ans"))
		})
		got, err := c.ChatStream(t.Context(), ChatRequest{Messages: userMsg("hi")}, nil)
		wantKind(t, err, ErrMalformed)
		if got.Message.Content != "half an ans" {
			t.Errorf("partial = %q", got.Message.Content)
		}
	})
}

func TestChatStreamMalformedChunk(t *testing.T) {
	c, _, _ := newClient(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) {
		s := newSSE(w)
		s.event(chunk("fine"))
		s.event(`{"choices":[{"delta":`)
	})
	_, err := c.ChatStream(t.Context(), ChatRequest{Messages: userMsg("hi")}, nil)
	wantKind(t, err, ErrMalformed)
}

func TestChatStreamLengthWithoutContent(t *testing.T) {
	c, _, _ := newClient(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) {
		s := newSSE(w)
		s.event(finishChunk("length"))
		s.event(usageChunk)
		s.raw("data: [DONE]\n\n")
	})
	got, err := c.ChatStream(t.Context(), ChatRequest{Messages: userMsg("hi"), MaxCompletionTokens: 20}, nil)
	wantKind(t, err, ErrLength)
	if got.Usage.CompletionTokens != 5 {
		t.Errorf("usage should survive: %+v", got.Usage)
	}
}

func TestChatStreamFailuresBeforeTheStreamAreNeverRetried(t *testing.T) {
	c, api, waits := newClient(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.Header().Set("Retry-After", "1")
		apiError(w, 429, "rate_limit_error", "rate_limit_exceeded", "slow down")
	})
	_, err := c.ChatStream(t.Context(), ChatRequest{Messages: userMsg("hi")}, nil)
	e := wantKind(t, err, ErrRateLimit)
	if api.count() != 1 || len(*waits) != 0 || e.RetryAfter != time.Second {
		t.Errorf("requests %d, waits %v, error %+v", api.count(), *waits, e)
	}
}

func TestChatStreamCancellationMidStream(t *testing.T) {
	c, _, _ := newClient(t, func(_ int, w http.ResponseWriter, r *http.Request, _ []byte) {
		s := newSSE(w)
		s.event(chunk("a"))
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, err := c.ChatStream(ctx, ChatRequest{Messages: userMsg("hi")}, func(Delta) error {
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestChatStreamTimeoutWhenTheServerStalls(t *testing.T) {
	c, _, _ := newClient(t, func(_ int, w http.ResponseWriter, r *http.Request, _ []byte) {
		s := newSSE(w)
		s.event(chunk("a"))
		<-r.Context().Done()
	})
	got, err := c.ChatStream(t.Context(), ChatRequest{Messages: userMsg("hi"), Timeout: 150 * time.Millisecond}, nil)
	wantKind(t, err, ErrTimeout)
	if got.Message.Content != "a" {
		t.Errorf("partial = %q", got.Message.Content)
	}
}

func TestChatStreamOversizedEventIsRejected(t *testing.T) {
	c, _, _ := newClient(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) {
		s := newSSE(w)
		s.raw("data: " + strings.Repeat("x", maxEventBytes+10) + "\n\n")
	})
	_, err := c.ChatStream(t.Context(), ChatRequest{Messages: userMsg("hi")}, nil)
	wantKind(t, err, ErrMalformed)
}
