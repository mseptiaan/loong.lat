// Package aiapitest is a scripted fake of the AI gateway for tests of code that talks to aiapi.Model: the agent
// loop, and the console and backoffice methods built on it. It needs no network and behaves like the real client
// where it matters: a streamed reply arrives fragment by fragment and stops as soon as the context is cancelled or
// the callback fails.
package aiapitest

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
)

// Step is one scripted reply of the model.
type Step struct {
	// Text is the answer. With Chunks it is ignored by ChatStream, which sends the chunks instead (and the reply's
	// Content is their concatenation).
	Text   string
	Chunks []string
	// Calls are tool calls the model asks for; a step with Calls and no Text is a pure tool turn.
	Calls []aiapi.ToolCall
	// Err fails the call instead.
	Err error
	// Usage is reported with the reply.
	Usage aiapi.Usage
	// Finish overrides the finish reason (stop, or tool_calls when there are Calls).
	Finish string
	// Hold, when set, is called after the last fragment is sent and before the reply is returned; a test blocks or
	// cancels there.
	Hold func(ctx context.Context)
}

// Say is a final text answer, streamed in chunks if given.
func Say(text string, chunks ...string) Step { return Step{Text: text, Chunks: chunks} }

// Call is one tool call.
func Call(id, name, arguments string) aiapi.ToolCall {
	return aiapi.ToolCall{ID: id, Name: name, Arguments: arguments}
}

// UseTools is a turn in which the model asks for tools.
func UseTools(calls ...aiapi.ToolCall) Step { return Step{Calls: calls} }

// Fail is a call that fails with err.
func Fail(err error) Step { return Step{Err: err} }

// Fake plays its steps in order, one per call, and records the requests it receives.
type Fake struct {
	mu       sync.Mutex
	steps    []Step
	next     int
	requests []aiapi.ChatRequest
}

var _ aiapi.Model = (*Fake)(nil)

// New returns a fake that will play steps.
func New(steps ...Step) *Fake { return &Fake{steps: steps} }

// Requests returns the requests received so far (messages copied, so later appends by the caller do not show).
func (f *Fake) Requests() []aiapi.ChatRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}

// RequestsFor returns the requests received so far that named this profile: a method that calls the model for two
// purposes (the optimizer's reviewer and the console's report share the gateway) can be checked for each.
func (f *Fake) RequestsFor(profile aiapi.Profile) []aiapi.ChatRequest {
	return slices.DeleteFunc(f.Requests(), func(r aiapi.ChatRequest) bool { return r.Profile != profile })
}

// Calls is how many model calls were made.
func (f *Fake) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

// Remaining is how many scripted steps were not used.
func (f *Fake) Remaining() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.steps) - f.next
}

func (f *Fake) take(req aiapi.ChatRequest) (Step, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	req.Messages = slices.Clone(req.Messages)
	f.requests = append(f.requests, req)
	if f.next >= len(f.steps) {
		return Step{}, fmt.Errorf("aiapitest: the script has no step for model call %d", len(f.requests))
	}
	s := f.steps[f.next]
	f.next++
	return s, nil
}

func reply(s Step, content string) aiapi.ChatResponse {
	finish := s.Finish
	if finish == "" {
		finish = aiapi.FinishStop
		if len(s.Calls) > 0 {
			finish = aiapi.FinishToolCalls
		}
	}
	return aiapi.ChatResponse{
		Message:      aiapi.Message{Role: aiapi.RoleAssistant, Content: content, ToolCalls: slices.Clone(s.Calls)},
		FinishReason: finish, Usage: s.Usage, Model: "fake",
	}
}

// Chat plays the next step.
func (f *Fake) Chat(ctx context.Context, req aiapi.ChatRequest) (aiapi.ChatResponse, error) {
	s, err := f.take(req)
	if err != nil {
		return aiapi.ChatResponse{}, err
	}
	if err := ctx.Err(); err != nil {
		return aiapi.ChatResponse{}, err
	}
	if s.Err != nil {
		return aiapi.ChatResponse{}, s.Err
	}
	content := s.Text
	if len(s.Chunks) > 0 {
		content = ""
		for _, c := range s.Chunks {
			content += c
		}
	}
	return reply(s, content), nil
}

// ChatStream plays the next step, sending its chunks to fn one by one.
func (f *Fake) ChatStream(ctx context.Context, req aiapi.ChatRequest, fn func(aiapi.Delta) error) (aiapi.ChatResponse, error) {
	s, err := f.take(req)
	if err != nil {
		return aiapi.ChatResponse{}, err
	}
	if s.Err != nil {
		return aiapi.ChatResponse{}, s.Err
	}
	chunks := s.Chunks
	if len(chunks) == 0 && s.Text != "" {
		chunks = []string{s.Text}
	}
	var sent string
	for _, c := range chunks {
		if err := ctx.Err(); err != nil {
			return aiapi.ChatResponse{}, err
		}
		sent += c
		if fn != nil {
			if err := fn(aiapi.Delta{Content: c}); err != nil {
				return aiapi.ChatResponse{}, err
			}
		}
	}
	if fn != nil && len(s.Calls) > 0 {
		var fragments []aiapi.ToolCallDelta
		for i, c := range s.Calls {
			fragments = append(fragments, aiapi.ToolCallDelta{Index: i, ID: c.ID, Name: c.Name, Arguments: c.Arguments})
		}
		if err := fn(aiapi.Delta{ToolCalls: fragments}); err != nil {
			return aiapi.ChatResponse{}, err
		}
	}
	if s.Hold != nil {
		s.Hold(ctx)
	}
	if err := ctx.Err(); err != nil {
		return aiapi.ChatResponse{}, err
	}
	return reply(s, sent), nil
}
