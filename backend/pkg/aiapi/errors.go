package aiapi

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

// MsgUnavailable is the catalog key every failure the user cannot fix is reported with.
const MsgUnavailable = "srv.engine.unavailable"

// Error is a failed call to the gateway. It unwraps to the *platform.APIError a handler should answer with, so a
// service method can simply `return err`: platform.RPC answers 500 layanan srv.engine.unavailable when the
// gateway is down or failing, 429 when over a limit, and 422 tidak_valid (with the gateway's message key and
// field) for input it refused. Branch on Kind (or errors.Is with the Err* sentinels) to do better, for example to
// answer with srv.api.aiError for one feature. The cause, never shown to clients, is kept for logs.
type Error struct {
	Kind ErrorKind
	// Status is the HTTP status the gateway answered with; zero when it could not be reached.
	Status     int
	Field      string
	Msg        contracts.Msg
	RetryAfter time.Duration
	api        *platform.APIError
}

// Sentinels for errors.Is: errors.Is(err, aiapi.ErrNotConfigured) is true for any *Error of that Kind.
var (
	ErrUnavailable   = &Error{Kind: KindUnavailable}
	ErrNotConfigured = &Error{Kind: KindNotConfigured}
	ErrRateLimited   = &Error{Kind: KindRateLimited}
	ErrInvalid       = &Error{Kind: KindInvalid}
	ErrUnauthorized  = &Error{Kind: KindUnauthorized}
	ErrTimeout       = &Error{Kind: KindTimeout}
	ErrTruncated     = &Error{Kind: KindTruncated}
)

func (e *Error) Error() string {
	text := "aiapi: " + string(e.Kind)
	if e.Status != 0 {
		text += fmt.Sprintf(" (http %d)", e.Status)
	}
	if e.api != nil {
		if cause := errors.Unwrap(e.api); cause != nil {
			text += ": " + cause.Error()
		} else if e.Msg.Key != "" {
			text += ": " + e.Msg.Key
		}
	}
	return text
}

// Unwrap exposes the *platform.APIError (and through it the cause).
func (e *Error) Unwrap() error {
	if e.api == nil {
		return nil
	}
	return e.api
}

// Is matches any *Error of the same Kind.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Kind == e.Kind
}

// APIError is the error to answer the caller with.
func (e *Error) APIError() *platform.APIError { return e.api }

// newError builds the Error for a failure body (or a synthesized one), choosing the platform error by Kind.
func newError(body ErrorBody, status int, cause error) *Error {
	e := &Error{Kind: body.Kind, Status: status, Field: body.Field, Msg: body.Msg, RetryAfter: time.Duration(body.RetryAfter) * time.Second}
	if e.Msg.Key == "" {
		e.Msg = contracts.Msg{Key: MsgUnavailable}
		if body.Kind == KindInvalid {
			e.Msg = contracts.Msg{Key: platform.MsgBadRequest}
		}
	}
	var api *platform.APIError
	switch body.Kind {
	case KindInvalid:
		api = platform.Invalid(e.Field, e.Msg)
	case KindRateLimited:
		api = platform.TooManyRequests(max(e.RetryAfter, time.Second))
	default:
		// Whatever went wrong, the user can only try again later; the details are for the logs.
		api = platform.Unavailable(contracts.Msg{Key: MsgUnavailable})
	}
	if cause != nil {
		api = api.WithCause(cause)
	} else if body.Kind != KindInvalid && body.Kind != KindRateLimited {
		api = api.WithCause(errors.New("the AI gateway reported " + string(body.Kind)))
	}
	e.api = api
	return e
}

// kindFromStatus is the best guess when the gateway's answer has no readable error body: a proxy in between, a
// crashed process, an older gateway.
func kindFromStatus(status int) ErrorKind {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return KindUnauthorized
	case http.StatusTooManyRequests:
		return KindRateLimited
	case http.StatusGatewayTimeout, http.StatusRequestTimeout:
		return KindTimeout
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
		return KindInvalid
	}
	return KindUnavailable
}
