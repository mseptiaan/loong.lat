// Package meta is a small typed client for Meta's Model API (https://api.meta.ai/v1): chat completions with
// streaming, tool calls, image input and structured output (Muse Spark), and file transcription (Muse Voice).
//
// Three properties matter to callers:
//
//   - The API key is a Secret: it prints as "[redacted]", and no error this package returns contains it.
//   - Failures are *Error values with a Kind, so callers branch on errors.Is(err, meta.ErrRateLimit) and never on
//     message text. Non-streaming calls retry 429 and 5xx a bounded number of times; streaming calls never retry.
//   - Reasoning tokens count against max_completion_tokens: a call that spends its whole budget thinking comes
//     back as KindLength instead of an empty answer. Always set ReasoningEffort and a generous MaxCompletionTokens.
package meta

import (
	"encoding/base64"
	"encoding/json/jsontext"
	"log/slog"
	"time"
)

// Defaults of the public Model API.
const (
	DefaultBaseURL         = "https://api.meta.ai/v1"
	DefaultModel           = "muse-spark-1.3-contributor"
	DefaultTranscribeModel = "muse-voice-transcribe-1.0"
)

// Secret is a credential that never prints: String, GoString, text/JSON encoding and slog all give "[redacted]".
// Reveal returns the value for the one place that sends it.
type Secret string

const redacted = "[redacted]"

func (s Secret) String() string {
	if s == "" {
		return ""
	}
	return redacted
}

func (s Secret) GoString() string { return `meta.Secret("` + s.String() + `")` }

func (s Secret) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

func (s Secret) LogValue() slog.Value { return slog.StringValue(s.String()) }

// Reveal returns the secret itself.
func (s Secret) Reveal() string { return string(s) }

// Set reports whether a value is present.
func (s Secret) Set() bool { return s != "" }

// Role of a message author.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one turn of a conversation. Plain turns set Content. A user turn with images sets Parts instead
// (Text and Image build them). An assistant turn that asked for tools carries ToolCalls; the answer to each is a
// RoleTool message whose ToolCallID names the call.
type Message struct {
	Role       Role
	Content    string
	Parts      []Part
	ToolCalls  []ToolCall
	ToolCallID string
}

// Part is one piece of a multi-part message: text, or an image given as a data URL (or a public URL).
type Part struct {
	Text     string
	ImageURL string
}

// Text is a text part.
func Text(s string) Part { return Part{Text: s} }

// Image is an image part from a URL; use ImageBytes for raw bytes.
func Image(url string) Part { return Part{ImageURL: url} }

// ImageBytes is an image part from raw bytes with their MIME type ("image/png", "image/jpeg", "image/webp"),
// sent inline as a base64 data URL.
func ImageBytes(mime string, data []byte) Part {
	return Part{ImageURL: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)}
}

// Tool declares a function the model may call. Parameters is a JSON Schema object.
type Tool struct {
	Name        string
	Description string
	Parameters  jsontext.Value
}

// ToolCall is a function call the model asked for. Arguments is a JSON string that the model may have got wrong:
// validate it before acting on it.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// JSONSchema constrains the reply to a JSON document (response_format json_schema). Schema is a JSON Schema object.
type JSONSchema struct {
	Name   string
	Schema jsontext.Value
	Strict bool
}

// Reasoning effort values the API accepts for Muse Spark ("none" is rejected by the model).
const (
	EffortMinimal = "minimal"
	EffortLow     = "low"
	EffortMedium  = "medium"
	EffortHigh    = "high"
)

// ChatRequest is one chat completion. Model falls back to the client's. Timeout bounds the whole call, retries
// included; zero takes the client's.
type ChatRequest struct {
	Model    string
	Messages []Message
	Tools    []Tool
	// ToolChoice is passed through as tool_choice. The API accepts only "auto" at present ("none", "required"
	// and a named function are refused with 400), which is also what an empty value means.
	ToolChoice          string
	JSONSchema          *JSONSchema
	ReasoningEffort     string
	MaxCompletionTokens int
	Timeout             time.Duration
}

// Usage counts tokens. Reasoning tokens are part of CompletionTokens.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	ReasoningTokens  int
	CachedTokens     int
}

// ChatResponse is the model's reply. Message.Content is the text; Message.ToolCalls the calls it asked for.
type ChatResponse struct {
	ID           string
	Model        string
	Message      Message
	FinishReason string
	Usage        Usage
}

// Finish reasons.
const (
	FinishStop      = "stop"
	FinishLength    = "length"
	FinishToolCalls = "tool_calls"
)

// Delta is one streamed increment. Content and Reasoning are text fragments; ToolCalls are fragments of calls
// (see ToolCallDelta); FinishReason is set on the chunk that ends the answer.
type Delta struct {
	Content      string
	Reasoning    string
	ToolCalls    []ToolCallDelta
	FinishReason string
}

// ToolCallDelta is a fragment of a streamed tool call, identified by Index. ID and Name usually arrive once, in
// the first fragment; Arguments arrives in pieces that concatenate to the JSON string.
type ToolCallDelta struct {
	Index     int
	ID        string
	Name      string
	Arguments string
}

// Transcript is the text of an audio clip.
type Transcript struct {
	Text       string
	SessionID  string
	DurationMs int64
	Turns      []Turn
}

// Turn is one detected utterance; the file endpoint reports none in the default single-turn mode.
type Turn struct {
	ID      int
	StartMs int64
	EndMs   int64
	Text    string
	Speaker string
}
