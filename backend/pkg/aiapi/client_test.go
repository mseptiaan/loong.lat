package aiapi_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi/aiapitest"
	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

func gateway(t *testing.T, h http.HandlerFunc) *aiapi.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &aiapi.Client{BaseURL: srv.URL, Token: "s3cret"}
}

func jsonReply(w http.ResponseWriter, status int, v any) {
	body, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(body)
}

func failure(w http.ResponseWriter, status int, body aiapi.ErrorBody) {
	jsonReply(w, status, aiapi.ErrorEnvelope{Error: body})
}

func TestChatSendsTheRequestAndReturnsTheReply(t *testing.T) {
	var got aiapi.ChatRequest
	var auth, accept, traceparent string
	c := gateway(t, func(w http.ResponseWriter, r *http.Request) {
		auth, accept, traceparent = r.Header.Get("Authorization"), r.Header.Get("Accept"), r.Header.Get("Traceparent")
		if r.URL.Path != aiapi.ChatPath || r.Method != http.MethodPost {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		if err := json.UnmarshalRead(r.Body, &got); err != nil {
			t.Error(err)
		}
		jsonReply(w, 200, aiapi.ChatResponse{
			Message:      aiapi.Message{Role: aiapi.RoleAssistant, ToolCalls: []aiapi.ToolCall{{ID: "c1", Name: "get_plan", Arguments: `{"id":"PL-1"}`}}},
			FinishReason: aiapi.FinishToolCalls, Usage: aiapi.Usage{Prompt: 10, Completion: 5, Reasoning: 2, Total: 15}, Model: "m",
		})
	})
	ctx := platform.WithTraceID(t.Context(), "0af7651916cd43dd8448eb211c80319c")
	img := aiapi.Image{MIME: "image/png", Data: []byte{0x89, 'P', 'N', 'G'}}
	resp, err := c.Chat(ctx, aiapi.ChatRequest{
		Profile:    aiapi.ProfileVision,
		Messages:   []aiapi.Message{aiapi.SystemText("be brief"), {Role: aiapi.RoleUser, Content: "what is this", Images: []aiapi.Image{img}}},
		Tools:      []aiapi.Tool{{Name: "get_plan", Description: "d", Schema: []byte(`{"type":"object"}`)}},
		JSONSchema: &aiapi.JSONSchema{Name: "items", Schema: []byte(`{"type":"object"}`)},
		Stream:     true, // a plain Chat call never streams, whatever the request says
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.FinishReason != aiapi.FinishToolCalls || len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].Arguments != `{"id":"PL-1"}` || resp.Usage.Total != 15 || resp.Usage.Reasoning != 2 {
		t.Errorf("response = %+v", resp)
	}
	if auth != "Bearer s3cret" || accept != "application/json" {
		t.Errorf("auth %q accept %q", auth, accept)
	}
	if !strings.HasPrefix(traceparent, "00-0af7651916cd43dd8448eb211c80319c-") || !strings.HasSuffix(traceparent, "-01") || len(traceparent) != 55 {
		t.Errorf("traceparent = %q", traceparent)
	}
	if got.Profile != aiapi.ProfileVision || got.Stream || len(got.Messages) != 2 || !bytes.Equal(got.Messages[1].Images[0].Data, img.Data) || got.Messages[1].Images[0].MIME != "image/png" ||
		got.Tools[0].Name != "get_plan" || got.JSONSchema.Name != "items" {
		t.Errorf("the gateway received %+v", got)
	}
}

func TestWireFormatIsCamelCaseAndCompact(t *testing.T) {
	raw, err := json.Marshal(aiapi.ChatRequest{
		Profile:  aiapi.ProfileCopilot,
		Messages: []aiapi.Message{{Role: aiapi.RoleTool, ToolCallID: "c1", Content: "{}"}, {Role: aiapi.RoleAssistant, ToolCalls: []aiapi.ToolCall{{ID: "c1", Name: "n", Arguments: "{}"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"profile":"copilot","messages":[{"role":"tool","content":"{}","toolCallId":"c1"},{"role":"assistant","toolCalls":[{"id":"c1","name":"n","arguments":"{}"}]}]}`
	if string(raw) != want {
		t.Errorf("wire = %s\nwant   %s", raw, want)
	}
}

func TestChatFailuresMapToPlatformErrors(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       *aiapi.ErrorBody // nil = a body that is not JSON
		wantKind   *aiapi.Error
		wantStatus int
		wantCode   platform.ErrorCode
		wantKey    string
		wantField  string
	}{
		{"model failing", 502, &aiapi.ErrorBody{Kind: aiapi.KindUnavailable}, aiapi.ErrUnavailable, 500, platform.CodeService, "srv.engine.unavailable", ""},
		{"model too slow", 504, &aiapi.ErrorBody{Kind: aiapi.KindTimeout}, aiapi.ErrTimeout, 500, platform.CodeService, "srv.engine.unavailable", ""},
		{"gateway without a key", 503, &aiapi.ErrorBody{Kind: aiapi.KindNotConfigured}, aiapi.ErrNotConfigured, 500, platform.CodeService, "srv.engine.unavailable", ""},
		{"wrong token", 401, &aiapi.ErrorBody{Kind: aiapi.KindUnauthorized}, aiapi.ErrUnauthorized, 500, platform.CodeService, "srv.engine.unavailable", ""},
		{"cut by the token budget", 502, &aiapi.ErrorBody{Kind: aiapi.KindTruncated}, aiapi.ErrTruncated, 500, platform.CodeService, "srv.engine.unavailable", ""},
		{"rate limited", 429, &aiapi.ErrorBody{Kind: aiapi.KindRateLimited, RetryAfter: 7}, aiapi.ErrRateLimited, 429, platform.CodeDenied, platform.MsgTooManyRequest, ""},
		{"input refused", 422, &aiapi.ErrorBody{Kind: aiapi.KindInvalid, Field: "messages.0.images.0", Msg: msg("srv.api.imageTooLarge")}, aiapi.ErrInvalid, 422, platform.CodeInvalid, "srv.api.imageTooLarge", "messages.0.images.0"},
		{"plain 404 from a proxy", 404, nil, aiapi.ErrUnavailable, 500, platform.CodeService, "srv.engine.unavailable", ""},
		{"plain 413 from a proxy", 413, nil, aiapi.ErrInvalid, 422, platform.CodeInvalid, "srv.rpc.badRequest", ""},
		{"plain 504 from a proxy", 504, nil, aiapi.ErrTimeout, 500, platform.CodeService, "srv.engine.unavailable", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := gateway(t, func(w http.ResponseWriter, _ *http.Request) {
				if tc.body == nil {
					http.Error(w, "<html>nope</html>", tc.status)
					return
				}
				failure(w, tc.status, *tc.body)
			})
			_, err := c.Chat(t.Context(), aiapi.ChatRequest{Profile: aiapi.ProfileIntent, Messages: []aiapi.Message{aiapi.UserText("hi")}})
			if !errors.Is(err, tc.wantKind) {
				t.Fatalf("err = %v, want kind %s", err, tc.wantKind.Kind)
			}
			apiErr, ok := errors.AsType[*platform.APIError](err)
			if !ok {
				t.Fatalf("a handler returning %T could not answer with a platform error", err)
			}
			if apiErr.Status != tc.wantStatus || apiErr.Code != tc.wantCode || apiErr.Msg.Key != tc.wantKey || apiErr.Field != tc.wantField {
				t.Errorf("platform error = status %d code %s key %s field %q", apiErr.Status, apiErr.Code, apiErr.Msg.Key, apiErr.Field)
			}
			if tc.wantKind == aiapi.ErrRateLimited && (apiErr.RetryAfter != 7*time.Second || apiErr.Msg.Params["seconds"] != 7) {
				t.Errorf("rate limit = %+v", apiErr)
			}
		})
	}
}

func TestAnErrorBodyDoesNotLeakToClients(t *testing.T) {
	c := gateway(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "stack trace: password=hunter2 at db.go:42", http.StatusInternalServerError)
	})
	_, err := c.Chat(t.Context(), aiapi.ChatRequest{Profile: aiapi.ProfileIntent})
	apiErr, _ := errors.AsType[*platform.APIError](err)
	if apiErr == nil || strings.Contains(apiErr.Msg.Key, "hunter2") || len(apiErr.Msg.Params) != 0 {
		t.Errorf("platform error = %+v", apiErr)
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("the gateway's raw body should not be copied into errors: %v", err)
	}
}

func msg(key string) contracts.Msg { return contracts.Msg{Key: key} }

func TestGatewayDownIsUnavailable(t *testing.T) {
	c := &aiapi.Client{BaseURL: "http://127.0.0.1:1", Token: "x"}
	_, err := c.Chat(t.Context(), aiapi.ChatRequest{Profile: aiapi.ProfileCopilot})
	if !errors.Is(err, aiapi.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	apiErr, ok := errors.AsType[*platform.APIError](err)
	if !ok || apiErr.Code != platform.CodeService || apiErr.Msg.Key != "srv.engine.unavailable" {
		t.Errorf("platform error = %+v", apiErr)
	}
	if errors.Unwrap(apiErr) == nil {
		t.Error("the transport failure should be kept as the cause, for logs")
	}
}

func TestNoGatewayConfiguredNeverTouchesTheNetwork(t *testing.T) {
	for name, c := range map[string]*aiapi.Client{"nil client": nil, "empty address": {}} {
		if c.Configured() {
			t.Errorf("%s: Configured should be false", name)
		}
		_, err := c.Chat(t.Context(), aiapi.ChatRequest{})
		if !errors.Is(err, aiapi.ErrNotConfigured) {
			t.Errorf("%s: Chat err = %v", name, err)
		}
		_, err = c.ChatStream(t.Context(), aiapi.ChatRequest{}, nil)
		if !errors.Is(err, aiapi.ErrNotConfigured) {
			t.Errorf("%s: ChatStream err = %v", name, err)
		}
		_, err = c.Transcribe(t.Context(), []byte("x"), "a.wav", "")
		if !errors.Is(err, aiapi.ErrNotConfigured) {
			t.Errorf("%s: Transcribe err = %v", name, err)
		}
		_, err = c.Info(t.Context())
		if !errors.Is(err, aiapi.ErrNotConfigured) {
			t.Errorf("%s: Info err = %v", name, err)
		}
		if api, _ := errors.AsType[*platform.APIError](err); api == nil || api.Msg.Key != "srv.engine.unavailable" {
			t.Errorf("%s: platform error = %+v", name, api)
		}
	}
}

func TestCallerCancellationIsNotAGatewayFailure(t *testing.T) {
	started := make(chan struct{})
	c := gateway(t, func(_ http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body) // the server notices a disconnect only once the body has been read
		close(started)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		<-started
		cancel()
	}()
	_, err := c.Chat(ctx, aiapi.ChatRequest{Profile: aiapi.ProfileIntent})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if _, ok := errors.AsType[*aiapi.Error](err); ok {
		t.Errorf("the caller's own cancellation must come back as the context error: %v", err)
	}
}

func TestOwnDeadlineBecomesATimeoutAndDefaultTimeoutApplies(t *testing.T) {
	release := make(chan struct{})
	c := gateway(t, func(_ http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	t.Cleanup(func() { close(release) })
	c.Timeout = 80 * time.Millisecond
	start := time.Now()
	_, err := c.Chat(t.Context(), aiapi.ChatRequest{Profile: aiapi.ProfileIntent})
	if !errors.Is(err, aiapi.ErrTimeout) {
		t.Fatalf("err = %v, want a timeout", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("took %v", time.Since(start))
	}

	// A caller's own deadline is theirs: it comes back as the context error.
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err = c.Chat(ctx, aiapi.ChatRequest{Profile: aiapi.ProfileIntent})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v", err)
	}
	if _, ok := errors.AsType[*aiapi.Error](err); ok {
		t.Errorf("a caller's deadline must not be reported as a gateway failure: %v", err)
	}
}

func ndjson(w http.ResponseWriter, events ...aiapi.Event) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	f, _ := w.(http.Flusher)
	for _, ev := range events {
		b, _ := json.Marshal(ev)
		w.Write(append(b, '\n'))
		f.Flush()
	}
}

func TestChatStreamRelaysDeltasThenReturnsTheAssembledReply(t *testing.T) {
	var accept string
	var streamRequested bool
	c := gateway(t, func(w http.ResponseWriter, r *http.Request) {
		accept = r.Header.Get("Accept")
		var req aiapi.ChatRequest
		json.UnmarshalRead(r.Body, &req)
		streamRequested = req.Stream
		ndjson(w,
			aiapi.Event{Type: aiapi.EventDelta, Content: "Hel"},
			aiapi.Event{Type: aiapi.EventDelta, Content: "lo"},
			aiapi.Event{Type: aiapi.EventDelta, ToolCalls: []aiapi.ToolCallDelta{{Index: 0, ID: "c1", Name: "n", Arguments: "{}"}}},
			aiapi.Event{Type: aiapi.EventDone, Response: &aiapi.ChatResponse{Message: aiapi.Message{Role: aiapi.RoleAssistant, Content: "Hello"}, FinishReason: "stop", Usage: aiapi.Usage{Total: 9}}},
		)
	})
	var text string
	var fragments int
	resp, err := c.ChatStream(t.Context(), aiapi.ChatRequest{Profile: aiapi.ProfileCopilot}, func(d aiapi.Delta) error {
		text += d.Content
		fragments += len(d.ToolCalls)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if text != "Hello" || fragments != 1 || resp.Message.Content != "Hello" || resp.Usage.Total != 9 {
		t.Errorf("text %q, fragments %d, resp %+v", text, fragments, resp)
	}
	if accept != "application/x-ndjson" || !streamRequested {
		t.Errorf("accept %q, stream requested %v", accept, streamRequested)
	}
}

func TestChatStreamErrorEventAndFailuresBeforeTheStream(t *testing.T) {
	c := gateway(t, func(w http.ResponseWriter, _ *http.Request) {
		ndjson(w,
			aiapi.Event{Type: aiapi.EventDelta, Content: "par"},
			aiapi.Event{Type: aiapi.EventError, Error: &aiapi.ErrorBody{Kind: aiapi.KindTimeout}},
		)
	})
	var text string
	resp, err := c.ChatStream(t.Context(), aiapi.ChatRequest{Profile: aiapi.ProfileCopilot}, func(d aiapi.Delta) error { text += d.Content; return nil })
	if !errors.Is(err, aiapi.ErrTimeout) || text != "par" || resp.Message.Content != "par" {
		t.Errorf("err %v, text %q, partial %+v", err, text, resp)
	}
	if api, ok := errors.AsType[*platform.APIError](err); !ok || api.Msg.Key != "srv.engine.unavailable" {
		t.Errorf("platform error = %+v", api)
	}

	c = gateway(t, func(w http.ResponseWriter, _ *http.Request) {
		failure(w, 429, aiapi.ErrorBody{Kind: aiapi.KindRateLimited, RetryAfter: 3})
	})
	_, err = c.ChatStream(t.Context(), aiapi.ChatRequest{Profile: aiapi.ProfileCopilot}, nil)
	if !errors.Is(err, aiapi.ErrRateLimited) {
		t.Errorf("err = %v", err)
	}
}

func TestChatStreamCallbackErrorAndTruncatedStreams(t *testing.T) {
	c := gateway(t, func(w http.ResponseWriter, _ *http.Request) {
		ndjson(w, aiapi.Event{Type: aiapi.EventDelta, Content: "a"}, aiapi.Event{Type: aiapi.EventDelta, Content: "b"})
	})
	boom := errors.New("stop")
	resp, err := c.ChatStream(t.Context(), aiapi.ChatRequest{Profile: aiapi.ProfileCopilot}, func(aiapi.Delta) error { return boom })
	if err != boom || resp.Message.Content != "a" {
		t.Errorf("a callback's error comes back as is, with the text so far: %v %+v", err, resp)
	}
	_, err = c.ChatStream(t.Context(), aiapi.ChatRequest{Profile: aiapi.ProfileCopilot}, nil)
	if !errors.Is(err, aiapi.ErrUnavailable) {
		t.Errorf("a stream that ends without a result is a gateway failure: %v", err)
	}
}

func TestChatStreamCancellation(t *testing.T) {
	c := gateway(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		ndjson(w, aiapi.Event{Type: aiapi.EventDelta, Content: "a"})
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, err := c.ChatStream(ctx, aiapi.ChatRequest{Profile: aiapi.ProfileCopilot}, func(aiapi.Delta) error { cancel(); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestTranscribeSendsAMultipartRecording(t *testing.T) {
	var fields = map[string][]string{}
	var filename, partType string
	var audio []byte
	c := gateway(t, func(w http.ResponseWriter, r *http.Request) {
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			t.Error(err)
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Error(err)
				break
			}
			b, _ := io.ReadAll(p)
			if p.FormName() == aiapi.FieldAudio {
				filename, partType, audio = p.FileName(), p.Header.Get("Content-Type"), b
				continue
			}
			fields[p.FormName()] = append(fields[p.FormName()], string(b))
		}
		if r.URL.Path != aiapi.TranscribePath {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonReply(w, 200, aiapi.Transcript{Text: "dua kardus", DurationMs: 1500})
	})
	got, err := c.Transcribe(t.Context(), []byte("RIFFxxxxWAVE"), `../a "b".wav`, "audio/wav",
		aiapi.WithKeywords("Kemang", "Senayan"), aiapi.WithLanguageBias("Indonesian"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "dua kardus" || got.DurationMs != 1500 {
		t.Errorf("transcript = %+v", got)
	}
	if string(audio) != "RIFFxxxxWAVE" || partType != "audio/wav" || strings.ContainsAny(filename, `"/\`) {
		t.Errorf("audio part: %q type %q file %q", audio, partType, filename)
	}
	if strings.Join(fields[aiapi.FieldKeywords], ",") != "Kemang,Senayan" || strings.Join(fields[aiapi.FieldLanguageBias], ",") != "Indonesian" {
		t.Errorf("fields = %v", fields)
	}
}

func TestTranscribeFailureMapsLikeChat(t *testing.T) {
	c := gateway(t, func(w http.ResponseWriter, _ *http.Request) {
		failure(w, 422, aiapi.ErrorBody{Kind: aiapi.KindInvalid, Field: "audio", Msg: msg("srv.api.noSpeech")})
	})
	_, err := c.Transcribe(t.Context(), []byte("x"), "a.wav", "")
	api, ok := errors.AsType[*platform.APIError](err)
	if !errors.Is(err, aiapi.ErrInvalid) || !ok || api.Status != 422 || api.Field != "audio" || api.Msg.Key != "srv.api.noSpeech" {
		t.Errorf("err = %v, platform %+v", err, api)
	}
}

func TestInfo(t *testing.T) {
	c := gateway(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != aiapi.InfoPath || r.Header.Get("Authorization") != "Bearer s3cret" {
			t.Errorf("%s %s %q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		jsonReply(w, 200, aiapi.Info{Configured: true, Profiles: []aiapi.ProfileInfo{{Name: aiapi.ProfileCopilot, Model: "m", TimeoutMs: 60000}}})
	})
	info, err := c.Info(t.Context())
	if err != nil || !info.Configured || len(info.Profiles) != 1 || info.Profiles[0].Name != aiapi.ProfileCopilot {
		t.Errorf("info = %+v, %v", info, err)
	}
}

func TestFakeBehavesLikeTheRealClient(t *testing.T) {
	var m aiapi.Model = aitestFake()
	var text string
	resp, err := m.ChatStream(t.Context(), aiapi.ChatRequest{Profile: aiapi.ProfileCopilot}, func(d aiapi.Delta) error { text += d.Content; return nil })
	if err != nil || text != "ab" || resp.Message.Content != "ab" || resp.FinishReason != aiapi.FinishStop {
		t.Errorf("stream: %q %+v %v", text, resp, err)
	}
	if _, err := m.Chat(t.Context(), aiapi.ChatRequest{}); err == nil {
		t.Error("an exhausted script must fail loudly")
	}
}

func aitestFake() *aiapitest.Fake { return aiapitest.New(aiapitest.Say("ab", "a", "b")) }
