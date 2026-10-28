package aiapi

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

// Model is what an agent loop needs from the gateway. *Client implements it; tests use aiapitest.Fake.
type Model interface {
	// Chat runs one call and returns the whole reply.
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
	// ChatStream runs one call, handing increments to fn as they arrive (fn's error stops the call and is returned
	// as is), and returns the assembled reply.
	ChatStream(ctx context.Context, req ChatRequest, fn func(Delta) error) (ChatResponse, error)
}

// Speech is the transcription side of the gateway.
type Speech interface {
	Transcribe(ctx context.Context, audio []byte, filename, contentType string, opts ...TranscribeOption) (Transcript, error)
}

// Gateway is the whole gateway.
type Gateway interface {
	Model
	Speech
}

var _ Gateway = (*Client)(nil)

// Client calls the AI gateway. The zero Timeout takes the defaults below; a nil *Client, or one without a BaseURL,
// is "no gateway": every call fails with ErrNotConfigured and nothing goes over the network, which is how a
// service that was started without AI_URL turns the features off.
type Client struct {
	// BaseURL is the gateway's address, e.g. http://ai:8091.
	BaseURL string
	// Token is the shared secret of service-to-service calls.
	Token string
	HTTP  *http.Client
	// Timeout bounds a call whose context has no deadline: 90s for Chat and Transcribe, three times that for a
	// stream. Zero takes those defaults.
	Timeout time.Duration
}

const defaultTimeout = 90 * time.Second

// Configured reports whether there is a gateway to call.
func (c *Client) Configured() bool { return c != nil && c.BaseURL != "" }

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// notConfigured is the error of a call without a gateway.
func notConfigured() error {
	return newError(ErrorBody{Kind: KindNotConfigured, Msg: contracts.Msg{Key: MsgUnavailable}}, 0, errors.New("no AI gateway is configured (AI_URL is empty)"))
}

// bound applies the default timeout when the caller set none.
func (c *Client) bound(ctx context.Context, factor time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	d := c.Timeout
	if d <= 0 {
		d = defaultTimeout
	}
	return context.WithTimeout(ctx, d*factor)
}

// newRequest builds an authenticated request; the trace id of ctx travels as a W3C traceparent so the gateway's
// logs can be matched to the caller's.
func (c *Client) newRequest(ctx context.Context, method, path, contentType, accept string, body []byte) (*http.Request, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, rd)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", accept)
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if id := platform.TraceID(ctx); contracts.ValidTraceID(id) {
		var span [8]byte
		_, _ = rand.Read(span[:])
		req.Header.Set("Traceparent", "00-"+id+"-"+hex.EncodeToString(span[:])+"-01")
	}
	return req, nil
}

// transportError turns a failure to talk to the gateway into the error to return: the caller's own cancellation
// or deadline (parent) as is; our default timeout (ctx) as a timeout; anything else, such as a refused connection
// or a reset, as an unavailable gateway.
func transportError(parent, ctx context.Context, err error) error {
	if parent.Err() != nil {
		return parent.Err()
	}
	if ctx.Err() != nil {
		return newError(ErrorBody{Kind: KindTimeout}, 0, err)
	}
	return newError(ErrorBody{Kind: KindUnavailable}, 0, err)
}

// readFailure builds the error for a non-2xx answer.
func readFailure(resp *http.Response) error {
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var env ErrorEnvelope
	if err := json.Unmarshal(raw, &env); err != nil || env.Error.Kind == "" {
		env.Error = ErrorBody{Kind: kindFromStatus(resp.StatusCode)}
	}
	cause := fmt.Errorf("the AI gateway answered http %d (%s)", resp.StatusCode, env.Error.Kind)
	return newError(env.Error, resp.StatusCode, cause)
}

// Chat runs one chat call and returns the whole reply.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	if !c.Configured() {
		return ChatResponse{}, notConfigured()
	}
	req.Stream = false
	body, err := json.Marshal(req)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("aiapi: encode request: %w", err)
	}
	parent := ctx
	ctx, cancel := c.bound(ctx, 1)
	defer cancel()
	hr, err := c.newRequest(ctx, http.MethodPost, ChatPath, "application/json", "application/json", body)
	if err != nil {
		return ChatResponse{}, newError(ErrorBody{Kind: KindUnavailable}, 0, err)
	}
	resp, err := c.httpClient().Do(hr)
	if err != nil {
		return ChatResponse{}, transportError(parent, ctx, err)
	}
	if resp.StatusCode != http.StatusOK {
		return ChatResponse{}, readFailure(resp)
	}
	defer resp.Body.Close()
	var out ChatResponse
	if err := json.UnmarshalRead(io.LimitReader(resp.Body, 16<<20), &out); err != nil {
		if ctx.Err() != nil {
			return ChatResponse{}, transportError(parent, ctx, ctx.Err())
		}
		return ChatResponse{}, newError(ErrorBody{Kind: KindUnavailable}, resp.StatusCode, fmt.Errorf("bad reply from the AI gateway: %w", err))
	}
	return out, nil
}

// ChatStream runs one chat call as a stream of events. A failure the gateway reports before it starts to stream
// comes back like Chat's; one after is an error event, returned the same way.
func (c *Client) ChatStream(ctx context.Context, req ChatRequest, fn func(Delta) error) (ChatResponse, error) {
	if !c.Configured() {
		return ChatResponse{}, notConfigured()
	}
	req.Stream = true
	body, err := json.Marshal(req)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("aiapi: encode request: %w", err)
	}
	parent := ctx
	ctx, cancel := c.bound(ctx, 3)
	defer cancel()
	hr, err := c.newRequest(ctx, http.MethodPost, ChatPath, "application/json", "application/x-ndjson", body)
	if err != nil {
		return ChatResponse{}, newError(ErrorBody{Kind: KindUnavailable}, 0, err)
	}
	resp, err := c.httpClient().Do(hr)
	if err != nil {
		return ChatResponse{}, transportError(parent, ctx, err)
	}
	if resp.StatusCode != http.StatusOK {
		return ChatResponse{}, readFailure(resp)
	}
	defer resp.Body.Close()

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	var partial strings.Builder
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			return ChatResponse{}, newError(ErrorBody{Kind: KindUnavailable}, resp.StatusCode, fmt.Errorf("bad event from the AI gateway: %w", err))
		}
		switch ev.Type {
		case EventDelta:
			partial.WriteString(ev.Content)
			if fn != nil {
				if err := fn(Delta{Content: ev.Content, ToolCalls: ev.ToolCalls}); err != nil {
					return ChatResponse{Message: Message{Role: RoleAssistant, Content: partial.String()}}, err
				}
			}
		case EventDone:
			if ev.Response == nil {
				return ChatResponse{}, newError(ErrorBody{Kind: KindUnavailable}, resp.StatusCode, errors.New("the AI gateway sent an empty result"))
			}
			return *ev.Response, nil
		case EventError:
			if ev.Error == nil {
				return ChatResponse{}, newError(ErrorBody{Kind: KindUnavailable}, resp.StatusCode, errors.New("the AI gateway sent an empty error"))
			}
			return ChatResponse{Message: Message{Role: RoleAssistant, Content: partial.String()}}, newError(*ev.Error, resp.StatusCode, nil)
		}
	}
	if err := sc.Err(); err != nil {
		return ChatResponse{}, transportError(parent, ctx, err)
	}
	if ctx.Err() != nil {
		return ChatResponse{}, transportError(parent, ctx, ctx.Err())
	}
	return ChatResponse{}, newError(ErrorBody{Kind: KindUnavailable}, resp.StatusCode, errors.New("the AI gateway's stream ended without a result"))
}

// TranscribeOption adjusts one Transcribe call.
type TranscribeOption func(*transcribeOptions)

type transcribeOptions struct {
	keywords     []string
	languageBias []string
}

// WithKeywords biases recognition toward names and jargon (place names, product words).
func WithKeywords(words ...string) TranscribeOption {
	return func(o *transcribeOptions) { o.keywords = words }
}

// WithLanguageBias tells the model which languages to expect, as language names ("Indonesian", "English").
func WithLanguageBias(languages ...string) TranscribeOption {
	return func(o *transcribeOptions) { o.languageBias = languages }
}

// Transcribe sends a recording to be turned into text. The gateway accepts only WAV: mono PCM at 16 or 24 kHz.
func (c *Client) Transcribe(ctx context.Context, audio []byte, filename, contentType string, opts ...TranscribeOption) (Transcript, error) {
	if !c.Configured() {
		return Transcript{}, notConfigured()
	}
	var o transcribeOptions
	for _, opt := range opts {
		opt(&o)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	hdr := textproto.MIMEHeader{}
	hdr.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": FieldAudio, "filename": cleanName(filename)}))
	if contentType == "" {
		contentType = "audio/wav"
	}
	hdr.Set("Content-Type", contentType)
	part, err := mw.CreatePart(hdr)
	if err != nil {
		return Transcript{}, fmt.Errorf("aiapi: encode request: %w", err)
	}
	_, _ = part.Write(audio)
	for _, w := range o.keywords {
		_ = mw.WriteField(FieldKeywords, w)
	}
	for _, l := range o.languageBias {
		_ = mw.WriteField(FieldLanguageBias, l)
	}
	if err := mw.Close(); err != nil {
		return Transcript{}, fmt.Errorf("aiapi: encode request: %w", err)
	}

	parent := ctx
	ctx, cancel := c.bound(ctx, 1)
	defer cancel()
	hr, err := c.newRequest(ctx, http.MethodPost, TranscribePath, mw.FormDataContentType(), "application/json", body.Bytes())
	if err != nil {
		return Transcript{}, newError(ErrorBody{Kind: KindUnavailable}, 0, err)
	}
	resp, err := c.httpClient().Do(hr)
	if err != nil {
		return Transcript{}, transportError(parent, ctx, err)
	}
	if resp.StatusCode != http.StatusOK {
		return Transcript{}, readFailure(resp)
	}
	defer resp.Body.Close()
	var out Transcript
	if err := json.UnmarshalRead(io.LimitReader(resp.Body, 4<<20), &out); err != nil {
		return Transcript{}, newError(ErrorBody{Kind: KindUnavailable}, resp.StatusCode, fmt.Errorf("bad reply from the AI gateway: %w", err))
	}
	return out, nil
}

// cleanName keeps odd characters of a client-supplied file name out of the multipart header.
func cleanName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '"' || r == '/' || r == '\\' {
			return -1
		}
		return r
	}, name)
	if name == "" {
		return "audio.wav"
	}
	return name
}

// Info asks the gateway what it is configured with.
func (c *Client) Info(ctx context.Context) (Info, error) {
	if !c.Configured() {
		return Info{}, notConfigured()
	}
	parent := ctx
	ctx, cancel := c.bound(ctx, 1)
	defer cancel()
	hr, err := c.newRequest(ctx, http.MethodGet, InfoPath, "", "application/json", nil)
	if err != nil {
		return Info{}, newError(ErrorBody{Kind: KindUnavailable}, 0, err)
	}
	resp, err := c.httpClient().Do(hr)
	if err != nil {
		return Info{}, transportError(parent, ctx, err)
	}
	if resp.StatusCode != http.StatusOK {
		return Info{}, readFailure(resp)
	}
	defer resp.Body.Close()
	var out Info
	if err := json.UnmarshalRead(io.LimitReader(resp.Body, 1<<20), &out); err != nil {
		return Info{}, newError(ErrorBody{Kind: KindUnavailable}, resp.StatusCode, fmt.Errorf("bad reply from the AI gateway: %w", err))
	}
	return out, nil
}
