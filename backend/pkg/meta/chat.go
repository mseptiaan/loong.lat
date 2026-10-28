package meta

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strconv"
	"strings"
)

// Wire shapes of POST /chat/completions (OpenAI-compatible). They are separate from the public types so that
// those stay plain Go.

type wireRequest struct {
	Model               string              `json:"model"`
	Messages            []wireMessage       `json:"messages"`
	Tools               []wireTool          `json:"tools,omitempty"`
	ToolChoice          string              `json:"tool_choice,omitempty"`
	ResponseFormat      *wireResponseFormat `json:"response_format,omitempty"`
	ReasoningEffort     string              `json:"reasoning_effort,omitempty"`
	MaxCompletionTokens int                 `json:"max_completion_tokens,omitzero"`
	Stream              bool                `json:"stream,omitzero"`
	StreamOptions       *wireStreamOptions  `json:"stream_options,omitempty"`
}

type wireStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type wireMessage struct {
	Role string `json:"role"`
	// Content is a string, an array of parts, or null (an assistant turn that only called tools).
	Content    any           `json:"content"`
	ToolCalls  []wireOutCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

// wireOutCall is a tool call as sent back in an assistant message (no stream index).
type wireOutCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function wireCallBody `json:"function"`
}

type wirePart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitzero"`
	ImageURL *wireImageURL `json:"image_url,omitempty"`
}

type wireImageURL struct {
	URL string `json:"url"`
}

type wireTool struct {
	Type     string       `json:"type"`
	Function wireFunction `json:"function"`
}

type wireFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  jsontext.Value `json:"parameters,omitzero"`
}

type wireToolCall struct {
	// Index identifies the call in a stream; ID and function.name arrive in the first fragment.
	Index    int          `json:"index"`
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Function wireCallBody `json:"function"`
}

type wireCallBody struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments"`
}

type wireResponseFormat struct {
	Type       string          `json:"type"`
	JSONSchema *wireJSONSchema `json:"json_schema,omitempty"`
}

type wireJSONSchema struct {
	Name   string         `json:"name"`
	Schema jsontext.Value `json:"schema"`
	Strict bool           `json:"strict,omitzero"`
}

type wireUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CompletionDetail *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
	PromptDetail *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

func (u *wireUsage) usage() Usage {
	if u == nil {
		return Usage{}
	}
	out := Usage{PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens, TotalTokens: u.TotalTokens}
	if u.CompletionDetail != nil {
		out.ReasoningTokens = u.CompletionDetail.ReasoningTokens
	}
	if u.PromptDetail != nil {
		out.CachedTokens = u.PromptDetail.CachedTokens
	}
	return out
}

type wireResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message      wireReplyMessage `json:"message"`
		FinishReason string           `json:"finish_reason"`
	} `json:"choices"`
	Usage *wireUsage    `json:"usage"`
	Error *apiErrorBody `json:"error"`
}

type wireReplyMessage struct {
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	Refusal   string         `json:"refusal"`
	ToolCalls []wireToolCall `json:"tool_calls"`
}

// emptyObjectSchema is what a tool without parameters declares.
var emptyObjectSchema = jsontext.Value(`{"type":"object","properties":{}}`)

// encodeChat builds the request body.
func (c *Client) encodeChat(req ChatRequest, stream bool) ([]byte, error) {
	w := wireRequest{
		Model:               cmpOr(req.Model, c.cfg.Model),
		Messages:            make([]wireMessage, 0, len(req.Messages)),
		ToolChoice:          req.ToolChoice,
		ReasoningEffort:     req.ReasoningEffort,
		MaxCompletionTokens: req.MaxCompletionTokens,
		Stream:              stream,
	}
	if stream {
		w.StreamOptions = &wireStreamOptions{IncludeUsage: true}
	}
	for _, m := range req.Messages {
		w.Messages = append(w.Messages, toWireMessage(m))
	}
	for _, t := range req.Tools {
		params := t.Parameters
		if len(params) == 0 {
			params = emptyObjectSchema
		}
		w.Tools = append(w.Tools, wireTool{Type: "function", Function: wireFunction{Name: t.Name, Description: t.Description, Parameters: params}})
	}
	if s := req.JSONSchema; s != nil {
		w.ResponseFormat = &wireResponseFormat{Type: "json_schema", JSONSchema: &wireJSONSchema{Name: s.Name, Schema: s.Schema, Strict: s.Strict}}
	}
	body, err := json.Marshal(w)
	if err != nil {
		return nil, &Error{Kind: KindBadRequest, Message: "the request cannot be encoded: " + clean(err.Error(), c.cfg.APIKey), cause: err}
	}
	return body, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func toWireMessage(m Message) wireMessage {
	w := wireMessage{Role: string(m.Role), ToolCallID: m.ToolCallID}
	switch {
	case len(m.Parts) > 0:
		parts := make([]wirePart, 0, len(m.Parts))
		for _, p := range m.Parts {
			if p.ImageURL != "" {
				parts = append(parts, wirePart{Type: "image_url", ImageURL: &wireImageURL{URL: p.ImageURL}})
				continue
			}
			parts = append(parts, wirePart{Type: "text", Text: p.Text})
		}
		w.Content = parts
	case m.Content == "" && len(m.ToolCalls) > 0:
		w.Content = nil
	default:
		w.Content = m.Content
	}
	for _, tc := range m.ToolCalls {
		args := tc.Arguments
		if args == "" {
			args = "{}"
		}
		w.ToolCalls = append(w.ToolCalls, wireOutCall{ID: tc.ID, Type: "function", Function: wireCallBody{Name: tc.Name, Arguments: args}})
	}
	return w
}

// Chat runs one chat completion and waits for the whole reply. 429 and 5xx are retried with backoff. A reply cut
// off by the token limit with nothing usable in it comes back as KindLength together with the (empty) response,
// so the usage can still be recorded.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	body, err := c.encodeChat(req, false)
	if err != nil {
		return ChatResponse{}, err
	}
	callCtx, cancel := c.callContext(ctx, req.Timeout)
	defer cancel()

	resp, err := c.send(ctx, callCtx, call{path: "/chat/completions", body: body, contentType: "application/json", accept: "application/json", retry: true})
	if err != nil {
		return ChatResponse{}, err
	}
	raw, err := c.readBody(ctx, callCtx, resp)
	if err != nil {
		return ChatResponse{}, err
	}

	var w wireResponse
	if err := json.Unmarshal(raw, &w); err != nil {
		return ChatResponse{}, malformed("the reply is not valid JSON", err, c.cfg.APIKey)
	}
	if w.Error != nil && len(w.Choices) == 0 {
		return ChatResponse{}, fromBody(kindFromType(w.Error.Type), 0, w.Error, "", c.cfg.APIKey)
	}
	if len(w.Choices) == 0 {
		return ChatResponse{}, malformed("the reply has no choices", nil, c.cfg.APIKey)
	}
	choice := w.Choices[0]
	out := ChatResponse{
		ID: w.ID, Model: w.Model, FinishReason: choice.FinishReason, Usage: w.Usage.usage(),
		Message: Message{Role: RoleAssistant, Content: choice.Message.Content, ToolCalls: fromWireCalls(choice.Message.ToolCalls)},
	}
	return out, checkReply(out, req)
}

// fromWireCalls converts complete tool calls, giving a call without an id a stable one.
func fromWireCalls(in []wireToolCall) []ToolCall {
	if len(in) == 0 {
		return nil
	}
	out := make([]ToolCall, 0, len(in))
	for i, tc := range in {
		id := tc.ID
		if id == "" {
			id = "call_" + strconv.Itoa(i)
		}
		out = append(out, ToolCall{ID: id, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
	}
	return out
}

// checkReply turns an unusable reply into the typed error for it: nothing came back because the token limit was
// hit (reasoning tokens count against it) or the filter withheld it, or structured output was cut off mid-document.
func checkReply(r ChatResponse, req ChatRequest) error {
	empty := strings.TrimSpace(r.Message.Content) == "" && len(r.Message.ToolCalls) == 0
	switch {
	case r.FinishReason == FinishLength && (empty || req.JSONSchema != nil):
		msg := "the reply was cut off by max_completion_tokens before it had any content; raise the limit or lower reasoning_effort"
		if !empty {
			msg = "the structured reply was cut off by max_completion_tokens; raise the limit or lower reasoning_effort"
		}
		return &Error{Kind: KindLength, Message: msg}
	case r.FinishReason == "content_filter" && empty:
		return &Error{Kind: KindFiltered, Message: "the reply was withheld by the content filter"}
	}
	return nil
}
