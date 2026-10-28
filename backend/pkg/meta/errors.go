package meta

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"
)

// Kind classifies a failure so callers can decide what to do without reading messages.
type Kind int

const (
	// KindServer is the API failing (5xx), an error event in a stream, or not being reachable.
	KindServer Kind = iota + 1
	// KindAuth is a rejected or missing API key.
	KindAuth
	// KindRateLimit is 429; RetryAfter says when to try again.
	KindRateLimit
	// KindBadRequest is a request the API refuses (4xx other than auth and rate limit): our bug or a policy denial.
	KindBadRequest
	// KindTimeout is the call's own time limit running out.
	KindTimeout
	// KindMalformed is a reply that could not be understood: broken JSON, no choices, a stream cut short.
	KindMalformed
	// KindLength is a reply cut off by max_completion_tokens: no content at all (reasoning ate the budget), or
	// truncated structured output that cannot be parsed.
	KindLength
	// KindFiltered is a reply withheld by the content filter.
	KindFiltered
)

func (k Kind) String() string {
	switch k {
	case KindServer:
		return "server"
	case KindAuth:
		return "auth"
	case KindRateLimit:
		return "rate_limit"
	case KindBadRequest:
		return "bad_request"
	case KindTimeout:
		return "timeout"
	case KindMalformed:
		return "malformed"
	case KindLength:
		return "length"
	case KindFiltered:
		return "filtered"
	}
	return "unknown"
}

// Sentinels for errors.Is: errors.Is(err, meta.ErrRateLimit) is true for any *Error of that Kind.
var (
	ErrServer     = &Error{Kind: KindServer}
	ErrAuth       = &Error{Kind: KindAuth}
	ErrRateLimit  = &Error{Kind: KindRateLimit}
	ErrBadRequest = &Error{Kind: KindBadRequest}
	ErrTimeout    = &Error{Kind: KindTimeout}
	ErrMalformed  = &Error{Kind: KindMalformed}
	ErrLength     = &Error{Kind: KindLength}
	ErrFiltered   = &Error{Kind: KindFiltered}
)

// Error is a failed call to the Model API. Message is the API's own client-safe message (or a description of
// what went wrong here) with the API key scrubbed. It is not safe to show to end users.
type Error struct {
	Kind Kind
	// Status is the HTTP status, zero when there was none (a stream error event, a timeout).
	Status int
	// Message, Type, Code and Param come from the API's error object.
	Message string
	Type    string
	Code    string
	Param   string
	// RetryAfter is the API's Retry-After, when it sent one.
	RetryAfter time.Duration
	// Attempts is how many requests were made before giving up.
	Attempts int
	cause    error
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("meta: ")
	b.WriteString(e.Kind.String())
	if e.Status != 0 {
		fmt.Fprintf(&b, " (http %d)", e.Status)
	}
	if e.Code != "" {
		fmt.Fprintf(&b, " [%s]", e.Code)
	}
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	return b.String()
}

func (e *Error) Unwrap() error { return e.cause }

// Is matches any *Error of the same Kind, so the Err* sentinels work with errors.Is.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Kind == e.Kind
}

// Retryable reports whether the same request may succeed later: rate limits, server faults and timeouts.
func (e *Error) Retryable() bool {
	switch e.Kind {
	case KindServer, KindRateLimit, KindTimeout:
		return true
	}
	return false
}

// kindFromStatus maps an HTTP status the API answered with to a Kind.
func kindFromStatus(status int) Kind {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return KindAuth
	case status == http.StatusTooManyRequests:
		return KindRateLimit
	case status >= 500:
		return KindServer
	}
	return KindBadRequest
}

// kindFromType maps the API's error.type (seen in stream error events, which carry no status).
func kindFromType(typ string) Kind {
	switch typ {
	case "authentication_error":
		return KindAuth
	case "rate_limit_error":
		return KindRateLimit
	case "invalid_request_error":
		return KindBadRequest
	}
	return KindServer
}

// maxMessage bounds the API text kept in an error.
const maxMessage = 300

// clean makes API-supplied text safe to log and to carry in an error: control characters become spaces, the
// text is cut to maxMessage runes, and the key, if it ever appears in it, is replaced.
func clean(s string, key Secret) string {
	if key.Set() {
		s = strings.ReplaceAll(s, key.Reveal(), redacted)
	}
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxMessage {
		s = string(r[:maxMessage]) + "..."
	}
	return s
}

// apiErrorBody is the API's error object: {"error":{"message","type","param","code"}}. param and code may be null.
type apiErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Param   string `json:"param"`
	Code    string `json:"code"`
}

// fromBody builds the *Error for a body the API sent with an error status, or inside a stream.
func fromBody(kind Kind, status int, body *apiErrorBody, raw string, key Secret) *Error {
	e := &Error{Kind: kind, Status: status}
	if body != nil && (body.Message != "" || body.Code != "" || body.Type != "") {
		e.Message = clean(body.Message, key)
		e.Type = clean(body.Type, key)
		e.Code = clean(body.Code, key)
		e.Param = clean(body.Param, key)
		return e
	}
	e.Message = clean(raw, key)
	if e.Message == "" {
		e.Message = http.StatusText(status)
	}
	return e
}

// asError reports err as an *Error, if it is one.
func asError(err error) (*Error, bool) { return errors.AsType[*Error](err) }
