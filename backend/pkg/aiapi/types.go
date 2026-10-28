// Package aiapi is the contract between the AI gateway (services/ai) and the services that use it (console-api,
// backoffice-api): the wire types, a client, and the Model interface the agent loop and the callers' tests program
// against. Nothing from the gateway's internals or from Meta's API leaks through: callers name a Profile (what
// the call is for) and the gateway decides model, reasoning effort, token budget and time limit.
//
// Chat replies are a single JSON document, or with streaming one JSON object per line (NDJSON): delta events,
// then a done event with the assembled reply or an error event.
package aiapi

import (
	"encoding/json/jsontext"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

// Paths the gateway serves.
const (
	ChatPath       = "/v1/chat"
	TranscribePath = "/v1/transcribe"
	InfoPath       = "/v1/info"
)

// Profile says what a call is for. The gateway fixes model, reasoning effort, token budget, time limit and the
// largest request for each; a caller cannot override them.
type Profile string

const (
	ProfileCopilot    Profile = "copilot"    // tool-calling assistant over the caller's own data
	ProfileVision     Profile = "vision"     // photo to shipment items
	ProfileIntent     Profile = "intent"     // short text to structured intent
	ProfileInsights   Profile = "insights"   // narrative over numbers the server computed
	ProfileReview     Profile = "review"     // the optimizer's reviewer: facts about candidates to next experiments (JSON)
	ProfileReport     Profile = "report"     // narrative explaining a quote's options from numbers the server computed
	ProfileTranscribe Profile = "transcribe" // speech to text
)

// ChatProfiles are the profiles /v1/chat serves.
var ChatProfiles = []Profile{ProfileCopilot, ProfileVision, ProfileIntent, ProfileInsights, ProfileReview, ProfileReport}

// Role of a message author.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one turn. A user turn may carry Images (vision profile only). An assistant turn that asked for tools
// carries ToolCalls; each is answered by a RoleTool message whose ToolCallID names the call and whose Content is
// the tool's result.
type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content,omitempty"`
	Images     []Image    `json:"images,omitempty"`
	ToolCalls  []ToolCall `json:"toolCalls,omitempty"`
	ToolCallID string     `json:"toolCallId,omitempty"`
}

// Image is a picture the model should look at: PNG, JPEG or WebP bytes (base64 on the wire). The gateway checks
// the bytes, not the declared MIME type.
type Image struct {
	MIME string `json:"mime"`
	Data []byte `json:"data"`
}

// ToolCall is a function call the model asked for. Arguments is a JSON string, which the model may have got wrong.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool declares a function the model may call; Schema is a JSON Schema object for its arguments.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Schema      jsontext.Value `json:"schema,omitzero"`
}

// JSONSchema constrains the reply to a JSON document of this shape.
type JSONSchema struct {
	Name   string         `json:"name"`
	Schema jsontext.Value `json:"schema"`
}

// ChatRequest is one chat call. Tools are allowed only for the copilot profile and images only for vision.
type ChatRequest struct {
	Profile    Profile     `json:"profile"`
	Messages   []Message   `json:"messages"`
	Tools      []Tool      `json:"tools,omitempty"`
	JSONSchema *JSONSchema `json:"jsonSchema,omitempty"`
	// Stream asks for NDJSON events instead of one document. The client sets it for ChatStream.
	Stream bool `json:"stream,omitzero"`
}

// Usage counts tokens; reasoning tokens are part of Completion.
type Usage struct {
	Prompt     int `json:"prompt,omitzero"`
	Completion int `json:"completion,omitzero"`
	Reasoning  int `json:"reasoning,omitzero"`
	Total      int `json:"total,omitzero"`
}

// Add returns the sum of two usages.
func (u Usage) Add(o Usage) Usage {
	return Usage{Prompt: u.Prompt + o.Prompt, Completion: u.Completion + o.Completion, Reasoning: u.Reasoning + o.Reasoning, Total: u.Total + o.Total}
}

// Finish reasons of a ChatResponse.
const (
	FinishStop      = "stop"
	FinishToolCalls = "tool_calls"
	FinishLength    = "length"
)

// ChatResponse is the model's reply: Message.Content is the text, Message.ToolCalls the calls it asked for.
type ChatResponse struct {
	Message      Message `json:"message"`
	FinishReason string  `json:"finishReason,omitempty"`
	Usage        Usage   `json:"usage,omitzero"`
	// Model is the model that answered, for the caller's records.
	Model string `json:"model,omitempty"`
}

// Delta is an increment of a streamed reply: a text fragment and/or fragments of tool calls. Tool-call
// fragments are for callers that relay the stream; the final ChatResponse always has the calls assembled.
type Delta struct {
	Content   string          `json:"content,omitempty"`
	ToolCalls []ToolCallDelta `json:"toolCalls,omitempty"`
}

// ToolCallDelta is a fragment of a streamed tool call, identified by Index.
type ToolCallDelta struct {
	Index     int    `json:"index"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// Event types of a streamed chat reply.
const (
	EventDelta = "delta"
	EventDone  = "done"
	EventError = "error"
)

// Event is one line of a streamed chat reply.
type Event struct {
	Type      string          `json:"type"`
	Content   string          `json:"content,omitempty"`
	ToolCalls []ToolCallDelta `json:"toolCalls,omitempty"`
	Response  *ChatResponse   `json:"response,omitempty"`
	Error     *ErrorBody      `json:"error,omitempty"`
}

// Transcript is the text of a recording. Duration is the length of the audio in milliseconds.
type Transcript struct {
	Text       string `json:"text"`
	DurationMs int64  `json:"durationMs,omitzero"`
}

// Form fields of POST /v1/transcribe: the recording is the file part "audio"; "keywords" and "languageBias" may
// repeat.
const (
	FieldAudio        = "audio"
	FieldKeywords     = "keywords"
	FieldLanguageBias = "languageBias"
)

// Info is what GET /v1/info reports; console and backoffice use Configured to decide whether to offer AI at all.
type Info struct {
	// Configured is false when the gateway runs without a Meta key and answers every call with 503.
	Configured bool          `json:"configured"`
	Profiles   []ProfileInfo `json:"profiles"`
}

// ProfileInfo is the fixed settings of one profile (no secrets).
type ProfileInfo struct {
	Name            Profile `json:"name"`
	Model           string  `json:"model"`
	ReasoningEffort string  `json:"reasoningEffort,omitempty"`
	MaxTokens       int     `json:"maxTokens,omitzero"`
	TimeoutMs       int64   `json:"timeoutMs"`
	MaxRequestBytes int64   `json:"maxRequestBytes"`
}

// ErrorKind says why a call failed, in terms a caller can act on.
type ErrorKind string

const (
	// KindUnavailable is the gateway or the model failing; the same request may work later.
	KindUnavailable ErrorKind = "unavailable"
	// KindNotConfigured is a gateway without a Meta key, or no gateway address at all.
	KindNotConfigured ErrorKind = "not_configured"
	// KindRateLimited is a caller over its limit, or the model being rate limited; RetryAfter says when.
	KindRateLimited ErrorKind = "rate_limited"
	// KindInvalid is a request the gateway refuses: unknown profile, too large, not an image, not a WAV.
	KindInvalid ErrorKind = "invalid"
	// KindUnauthorized is a missing or wrong bearer token (a deployment mistake, not the user's).
	KindUnauthorized ErrorKind = "unauthorized"
	// KindTimeout is the model taking longer than its profile allows.
	KindTimeout ErrorKind = "timeout"
	// KindTruncated is a reply cut off by the token budget with nothing usable in it.
	KindTruncated ErrorKind = "truncated"
)

// ErrorBody is how the gateway reports a failure: JSON {"error": ErrorBody}, or an error event in a stream.
// Msg is a catalog message key a screen can render (srv.engine.unavailable, srv.api.imageTooLarge, ...).
type ErrorBody struct {
	Kind  ErrorKind     `json:"kind"`
	Field string        `json:"field,omitempty"`
	Msg   contracts.Msg `json:"msg"`
	// RetryAfter is in seconds.
	RetryAfter int `json:"retryAfter,omitzero"`
}

// ErrorEnvelope is the body of a non-2xx answer.
type ErrorEnvelope struct {
	Error ErrorBody `json:"error"`
}

// UserText is a user message.
func UserText(text string) Message { return Message{Role: RoleUser, Content: text} }

// SystemText is a system message.
func SystemText(text string) Message { return Message{Role: RoleSystem, Content: text} }
