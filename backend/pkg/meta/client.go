package meta

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Config configures a Client. The zero value of everything but APIKey takes a sensible default.
type Config struct {
	// BaseURL is the API root, https://api.meta.ai/v1 by default.
	BaseURL string
	APIKey  Secret
	// Model is used when a ChatRequest names none; TranscribeModel likewise for Transcribe.
	Model           string
	TranscribeModel string
	// HTTP is the transport; the default has no overall timeout because every call carries its own deadline.
	HTTP *http.Client
	// Timeout bounds one call, retries included, when the request sets none. Default 60s.
	Timeout time.Duration
	// MaxRetries is how many times a non-streaming call is repeated after a 429 or 5xx: 0 takes the default (2),
	// a negative number turns retrying off.
	MaxRetries int
	// Backoff is the first wait between retries (doubling, with jitter); MaxBackoff caps one wait and also the
	// longest Retry-After that is honoured: beyond it the call fails and the caller decides.
	Backoff    time.Duration
	MaxBackoff time.Duration
	UserAgent  string
}

// Client talks to the Model API. It is safe for concurrent use.
type Client struct {
	cfg   Config
	http  *http.Client
	sleep func(ctx context.Context, d time.Duration) error // replaced in tests
}

// New returns a client. It never fails: a missing key shows up as KindAuth on the first call (see Configured).
func New(cfg Config) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	if cfg.TranscribeModel == "" {
		cfg.TranscribeModel = DefaultTranscribeModel
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	switch {
	case cfg.MaxRetries == 0:
		cfg.MaxRetries = 2
	case cfg.MaxRetries < 0:
		cfg.MaxRetries = 0
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = 500 * time.Millisecond
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 8 * time.Second
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "loong-ai-gateway"
	}
	h := cfg.HTTP
	if h == nil {
		h = &http.Client{}
	}
	return &Client{cfg: cfg, http: h, sleep: sleepCtx}
}

// Configured reports whether an API key is present.
func (c *Client) Configured() bool { return c.cfg.APIKey.Set() }

// Model is the default chat model.
func (c *Client) Model() string { return c.cfg.Model }

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// call carries one request through retries.
type call struct {
	path        string
	body        []byte
	contentType string
	accept      string
	retry       bool
}

// callContext derives the context of one call: the caller's, bounded by the per-call timeout.
func (c *Client) callContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = c.cfg.Timeout
	}
	return context.WithTimeout(parent, timeout)
}

// failure turns an error from the transport or from reading a body into the error to return. A cancelled or
// expired caller context comes back as is (the caller asked for it); our own deadline is KindTimeout; anything
// else, such as a refused connection, is KindServer. attempts is how many requests were made.
func (c *Client) failure(parent, callCtx context.Context, err error, attempts int) error {
	if e, ok := asError(err); ok {
		return e
	}
	if parent.Err() != nil {
		return parent.Err()
	}
	if callCtx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
		return &Error{Kind: KindTimeout, Message: "the call timed out", Attempts: attempts, cause: context.DeadlineExceeded}
	}
	return &Error{Kind: KindServer, Message: clean(err.Error(), c.cfg.APIKey), Attempts: attempts, cause: errors.New(clean(err.Error(), c.cfg.APIKey))}
}

// send posts the request and returns the response once it is a 2xx; the caller closes the body. Errors are
// *Error, or the caller's own context error. With retry set, 429 and 5xx are repeated with backoff.
func (c *Client) send(parent, callCtx context.Context, rq call) (*http.Response, error) {
	if !c.cfg.APIKey.Set() {
		return nil, &Error{Kind: KindAuth, Message: "no API key is configured"}
	}
	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(callCtx, http.MethodPost, c.cfg.BaseURL+rq.path, bytes.NewReader(rq.body))
		if err != nil {
			return nil, &Error{Kind: KindBadRequest, Message: "invalid request: " + clean(err.Error(), c.cfg.APIKey), Attempts: attempt}
		}
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey.Reveal())
		req.Header.Set("Content-Type", rq.contentType)
		req.Header.Set("Accept", rq.accept)
		req.Header.Set("User-Agent", c.cfg.UserAgent)

		resp, err := c.http.Do(req)
		if err != nil {
			return nil, c.failure(parent, callCtx, err, attempt)
		}
		if resp.StatusCode/100 == 2 {
			return resp, nil
		}
		apiErr := c.readError(resp)
		apiErr.Attempts = attempt
		if !rq.retry || attempt > c.cfg.MaxRetries || (apiErr.Kind != KindRateLimit && apiErr.Kind != KindServer) {
			return nil, apiErr
		}
		wait, ok := c.waitFor(callCtx, attempt, apiErr.RetryAfter)
		if !ok {
			return nil, apiErr
		}
		if err := c.sleep(callCtx, wait); err != nil {
			return nil, c.failure(parent, callCtx, err, attempt)
		}
	}
}

// waitFor is how long to wait before retry number `attempt`, or false when waiting is pointless: the API asked
// for longer than MaxBackoff, or the call would run out of time before the wait ended.
func (c *Client) waitFor(ctx context.Context, attempt int, retryAfter time.Duration) (time.Duration, bool) {
	var wait time.Duration
	if retryAfter > 0 {
		if retryAfter > c.cfg.MaxBackoff {
			return 0, false
		}
		wait = retryAfter
	} else {
		wait = min(c.cfg.Backoff<<(attempt-1), c.cfg.MaxBackoff)
		wait = wait/2 + rand.N(wait/2+1) // jitter: between half and the full step
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= wait {
		return 0, false
	}
	return wait, true
}

// readError reads (a bounded part of) an error response, closes it, and builds the *Error.
func (c *Client) readError(resp *http.Response) *Error {
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	var envelope struct {
		Error *apiErrorBody `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		envelope.Error = nil
	}
	e := fromBody(kindFromStatus(resp.StatusCode), resp.StatusCode, envelope.Error, string(raw), c.cfg.APIKey)
	e.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
	return e
}

// parseRetryAfter reads Retry-After as seconds or as an HTTP date; zero when absent or unreadable.
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		return max(0, time.Duration(secs)*time.Second)
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(0, t.Sub(now))
	}
	return 0
}

// maxResponseBytes bounds a buffered reply; completions are a few kilobytes.
const maxResponseBytes = 8 << 20

// readBody reads a successful response in full and closes it.
func (c *Client) readBody(parent, callCtx context.Context, resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, c.failure(parent, callCtx, err, 0)
	}
	if len(raw) > maxResponseBytes {
		return nil, &Error{Kind: KindMalformed, Message: "the reply is too large"}
	}
	return raw, nil
}

func malformed(msg string, err error, key Secret) *Error {
	e := &Error{Kind: KindMalformed, Message: msg}
	if err != nil {
		e.Message = fmt.Sprintf("%s: %s", msg, clean(err.Error(), key))
		e.cause = err
	}
	return e
}
