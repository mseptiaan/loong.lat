package platform

import (
	"encoding/json/v2"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

// ErrorCode is the frontend's ApiErrorCode (packages/domain/src/api.ts) as the server produces it.
type ErrorCode string

const (
	CodeInvalid  ErrorCode = "tidak_valid"
	CodeNotFound ErrorCode = "tidak_ditemukan"
	CodeDenied   ErrorCode = "ditolak"
	CodeConflict ErrorCode = "bentrok"
	CodeService  ErrorCode = "layanan"
)

// Message keys the platform raises itself. The first five exist in the frontend catalog; the rest are new and
// must be added to it (en and id) before a screen can render them.
const (
	MsgSignInFirst    = "srv.api.signInFirst"
	MsgRoleDenied     = "srv.api.roleDenied"
	MsgServiceError   = "srv.api.serviceError"
	MsgInternal       = "srv.rpc.internal"
	MsgUnknownMethod  = "srv.rpc.unknownMethod"
	MsgBadRequest     = "srv.rpc.badRequest"
	MsgTooManyRequest = "srv.rpc.tooManyRequests"
	MsgIdemRequired   = "srv.rpc.idempotencyKeyRequired"
	MsgIdemReused     = "srv.rpc.idempotencyKeyReused"
	MsgIdemInFlight   = "srv.rpc.idempotencyInFlight"
)

// APIError is the error a handler returns to answer a client with a specific code. Anything else a handler
// returns is logged and answered as a generic 500 (layanan) without leaking details.
type APIError struct {
	Code ErrorCode
	// Status is the HTTP status; the constructors set the one the frontend contract maps to the code.
	Status int
	// Field is the form field to attach the error to (tidak_valid).
	Field string
	// Msg is the language-neutral text screens render.
	Msg contracts.Msg
	// RetryAfter, when positive, is sent as a Retry-After header.
	RetryAfter time.Duration
	cause      error
}

func (e *APIError) Error() string {
	if e.cause != nil {
		return e.Msg.Key + ": " + e.cause.Error()
	}
	return e.Msg.Key
}

func (e *APIError) Unwrap() error { return e.cause }

// WithCause records the underlying error for logs; it is never sent to the client.
func (e *APIError) WithCause(err error) *APIError {
	copied := *e
	copied.cause = err
	return &copied
}

// Invalid is a validation failure: 422 tidak_valid, optionally tied to a field.
func Invalid(field string, msg contracts.Msg) *APIError {
	return &APIError{Code: CodeInvalid, Status: http.StatusUnprocessableEntity, Field: field, Msg: msg}
}

// NotFound is 404 tidak_ditemukan.
func NotFound(msg contracts.Msg) *APIError {
	return &APIError{Code: CodeNotFound, Status: http.StatusNotFound, Msg: msg}
}

// Denied is an authenticated caller who may not do this: 403 ditolak.
func Denied(msg contracts.Msg) *APIError {
	return &APIError{Code: CodeDenied, Status: http.StatusForbidden, Msg: msg}
}

// Unauthenticated is a caller with no valid session: 401 ditolak.
func Unauthenticated() *APIError {
	return &APIError{Code: CodeDenied, Status: http.StatusUnauthorized, Msg: contracts.Msg{Key: MsgSignInFirst}}
}

// Conflict is a request that clashes with current state: 409 bentrok.
func Conflict(msg contracts.Msg) *APIError {
	return &APIError{Code: CodeConflict, Status: http.StatusConflict, Msg: msg}
}

// Unavailable is a failure on our side or a dependency's: 500 layanan.
func Unavailable(msg contracts.Msg) *APIError {
	return &APIError{Code: CodeService, Status: http.StatusInternalServerError, Msg: msg}
}

// TooManyRequests is a rate-limited caller: 429 ditolak with Retry-After.
func TooManyRequests(retryAfter time.Duration) *APIError {
	seconds := max(1, int(retryAfter.Seconds()+0.5))
	return &APIError{
		Code: CodeDenied, Status: http.StatusTooManyRequests, RetryAfter: retryAfter,
		Msg: contracts.Msg{Key: MsgTooManyRequest, Params: map[string]any{"seconds": seconds}},
	}
}

// wireError is the frontend's WireError (mock-api/src/rpc/protocol.ts): what the HTTP client turns back into an
// ApiError. `message` is for logs and tests; screens render `msg`.
type wireError struct {
	Name    string         `json:"name"`
	Code    ErrorCode      `json:"code"`
	Status  int            `json:"status"`
	Message string         `json:"message"`
	Msg     *contracts.Msg `json:"msg,omitempty"`
	Field   string         `json:"field,omitempty"`
}

// internalError is the generic 500 for failures a client cannot act on; it carries the trace id so a report
// can be matched to the log line, and nothing else about the failure.
func internalError(traceID string) *APIError {
	params := map[string]any{"detail": "ref " + traceID}
	return &APIError{Code: CodeService, Status: http.StatusInternalServerError, Msg: contracts.Msg{Key: MsgInternal, Params: params}}
}

// resolveError turns any handler error into the APIError to send; unexpected reports an error that is not an
// APIError and so should be logged.
func resolveError(err error, traceID string) (apiErr *APIError, unexpected bool) {
	if apiErr, ok := errors.AsType[*APIError](err); ok {
		return apiErr, false
	}
	return internalError(traceID), true
}

func (e *APIError) wire() wireError {
	msg := e.Msg
	return wireError{Name: "ApiError", Code: e.Code, Status: e.Status, Message: e.Msg.Key, Msg: &msg, Field: e.Field}
}

// writeError answers with the APIError as JSON (before any streaming started).
func writeError(w http.ResponseWriter, e *APIError) {
	w.Header().Set("Content-Type", "application/json")
	if e.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(e.RetryAfter.Seconds()+0.5))))
	}
	w.WriteHeader(e.Status)
	body, _ := json.Marshal(e.wire(), json.Deterministic(true))
	_, _ = w.Write(body)
}
