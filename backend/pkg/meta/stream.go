package meta

import (
	"bufio"
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
)

// Wire shape of one streamed chunk (object "chat.completion.chunk").
type wireChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Delta        wireDelta `json:"delta"`
		FinishReason string    `json:"finish_reason"`
	} `json:"choices"`
	Usage *wireUsage    `json:"usage"`
	Error *apiErrorBody `json:"error"`
}

type wireDelta struct {
	Content          string         `json:"content"`
	ReasoningContent string         `json:"reasoning_content"`
	ToolCalls        []wireToolCall `json:"tool_calls"`
}

// maxEventBytes bounds one SSE line; maxStreamBytes bounds a whole stream, so a misbehaving server cannot make
// the process buffer without end.
const (
	maxEventBytes  = 1 << 20
	maxStreamBytes = 32 << 20
)

// ChatStream runs a chat completion as a server-sent event stream. fn is called for every increment as it
// arrives; returning an error from it stops the stream and is returned as is. The result is the assembled reply
// (text, complete tool calls, finish reason, usage), identical in shape to Chat's. A stream is never retried:
// fragments may already have reached fn.
func (c *Client) ChatStream(ctx context.Context, req ChatRequest, fn func(Delta) error) (ChatResponse, error) {
	body, err := c.encodeChat(req, true)
	if err != nil {
		return ChatResponse{}, err
	}
	callCtx, cancel := c.callContext(ctx, req.Timeout)
	defer cancel()

	resp, err := c.send(ctx, callCtx, call{path: "/chat/completions", body: body, contentType: "application/json", accept: "text/event-stream"})
	if err != nil {
		return ChatResponse{}, err
	}
	defer resp.Body.Close()

	out, err := c.readStream(ctx, callCtx, io.LimitReader(resp.Body, maxStreamBytes), fn)
	if err != nil {
		return out, err
	}
	return out, checkReply(out, req)
}

// assembler accumulates a stream into a ChatResponse.
type assembler struct {
	resp  ChatResponse
	text  strings.Builder
	calls []*ToolCall
	index map[int]*ToolCall
}

func (a *assembler) call(index int) *ToolCall {
	if tc, ok := a.index[index]; ok {
		return tc
	}
	if a.index == nil {
		a.index = map[int]*ToolCall{}
	}
	tc := &ToolCall{}
	a.index[index] = tc
	a.calls = append(a.calls, tc)
	return tc
}

// result is the assembled reply; calls are ordered by their stream index.
func (a *assembler) result() ChatResponse {
	out := a.resp
	out.Message = Message{Role: RoleAssistant, Content: a.text.String()}
	type indexed struct {
		index int
		call  *ToolCall
	}
	var ordered []indexed
	for i, tc := range a.index {
		ordered = append(ordered, indexed{i, tc})
	}
	slices.SortFunc(ordered, func(x, y indexed) int { return x.index - y.index })
	for n, o := range ordered {
		if o.call.ID == "" {
			o.call.ID = "call_" + strconv.Itoa(n)
		}
		out.Message.ToolCalls = append(out.Message.ToolCalls, *o.call)
	}
	return out
}

// readStream parses the SSE body. Events are separated by blank lines, a data field may span lines, comment
// lines start with ":", and the literal data "[DONE]" ends the stream.
func (c *Client) readStream(parent, callCtx context.Context, body io.Reader, fn func(Delta) error) (ChatResponse, error) {
	var (
		a        assembler
		data     []string
		done     bool
		finished bool
	)
	// handle processes one complete event; it reports whether the stream is over.
	handle := func() error {
		if len(data) == 0 {
			return nil
		}
		payload := strings.TrimSpace(strings.Join(data, "\n"))
		data = data[:0]
		if payload == "[DONE]" {
			done = true
			return nil
		}
		var ch wireChunk
		if err := json.Unmarshal([]byte(payload), &ch); err != nil {
			return malformed("a stream chunk is not valid JSON", err, c.cfg.APIKey)
		}
		if ch.Error != nil {
			return fromBody(kindFromType(ch.Error.Type), 0, ch.Error, "", c.cfg.APIKey)
		}
		if ch.ID != "" {
			a.resp.ID = ch.ID
		}
		if ch.Model != "" {
			a.resp.Model = ch.Model
		}
		if ch.Usage != nil {
			a.resp.Usage = ch.Usage.usage()
		}
		for _, choice := range ch.Choices {
			d := Delta{Content: choice.Delta.Content, Reasoning: choice.Delta.ReasoningContent, FinishReason: choice.FinishReason}
			a.text.WriteString(choice.Delta.Content)
			for _, tc := range choice.Delta.ToolCalls {
				acc := a.call(tc.Index)
				if tc.ID != "" {
					acc.ID = tc.ID
				}
				// A server may repeat the whole name in later fragments; a differing one is a continuation.
				if name := tc.Function.Name; name != "" && name != acc.Name {
					acc.Name += name
				}
				acc.Arguments += tc.Function.Arguments
				d.ToolCalls = append(d.ToolCalls, ToolCallDelta{Index: tc.Index, ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
			}
			if choice.FinishReason != "" {
				a.resp.FinishReason = choice.FinishReason
				finished = true
			}
			if d.Content == "" && d.Reasoning == "" && len(d.ToolCalls) == 0 && d.FinishReason == "" {
				continue
			}
			if fn != nil {
				if err := fn(d); err != nil {
					return &callbackError{err}
				}
			}
		}
		return nil
	}

	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64<<10), maxEventBytes)
	for !done && sc.Scan() {
		line := sc.Text()
		var err error
		switch {
		case line == "":
			err = handle()
		case strings.HasPrefix(line, ":"):
			// comment or keep-alive
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
		if err != nil {
			return a.result(), unwrapCallback(err)
		}
	}
	if !done {
		if err := sc.Err(); err != nil {
			if errors.Is(err, bufio.ErrTooLong) {
				return a.result(), malformed("a stream event is too large", nil, c.cfg.APIKey)
			}
			return a.result(), c.failure(parent, callCtx, err, 1)
		}
		if err := handle(); err != nil { // a last event without its blank line
			return a.result(), unwrapCallback(err)
		}
		if parent.Err() != nil {
			return a.result(), parent.Err()
		}
		if callCtx.Err() != nil {
			return a.result(), c.failure(parent, callCtx, callCtx.Err(), 1)
		}
	}
	if !done && !finished {
		return a.result(), malformed("the stream ended before the reply was complete", nil, c.cfg.APIKey)
	}
	return a.result(), nil
}

// callbackError carries an error returned by the caller's callback through readStream untouched.
type callbackError struct{ err error }

func (e *callbackError) Error() string { return e.err.Error() }

func unwrapCallback(err error) error {
	if cb, ok := errors.AsType[*callbackError](err); ok {
		return cb.err
	}
	return err
}
