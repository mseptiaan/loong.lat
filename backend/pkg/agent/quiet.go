package agent

import (
	"context"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
)

// QuietChatter wraps a model so that what it says while it asks for tools never reaches the caller. The model
// thinks aloud whatever the prompt says ("I'll pull your plan and compare the options", "I'll get that ready for
// you"), and a sentence that has been streamed to a screen cannot be taken back. A turn that asks for tools is
// work in progress, which the screen already shows as trace lines, so its text is dropped. The text of the turn
// that answers is released when that turn ends, as the pieces it came in: the answer starts appearing a little
// later than it was written, and it never has chatter in front of it.
//
// Wrap the model that is handed to Run for any assistant that shows an answer to a person. Calls without tools
// pass through untouched and stream as they come (a narration over figures has nothing to be quiet about).
// Fragments of tool calls are not forwarded: Run assembles the calls from the final reply.
//
// The conversation keeps what the model said: its own turn stays in the history it is sent next, as it said it.
func QuietChatter(model aiapi.Model) aiapi.Model { return quiet{model} }

type quiet struct{ aiapi.Model }

func (q quiet) ChatStream(ctx context.Context, req aiapi.ChatRequest, fn func(aiapi.Delta) error) (aiapi.ChatResponse, error) {
	if len(req.Tools) == 0 {
		return q.Model.ChatStream(ctx, req, fn)
	}
	var held []string
	resp, err := q.Model.ChatStream(ctx, req, func(d aiapi.Delta) error {
		if d.Content != "" {
			held = append(held, d.Content)
		}
		return ctx.Err() // a caller that went away ends the call
	})
	if err != nil || len(resp.Message.ToolCalls) > 0 {
		return resp, err
	}
	for _, piece := range held {
		if err := fn(aiapi.Delta{Content: piece}); err != nil {
			return resp, err
		}
	}
	return resp, nil
}
