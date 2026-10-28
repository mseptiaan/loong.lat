package api

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/meta"
	"github.com/mseptiaan/loong.lat/backend/services/ai/internal/profile"
)

// Limits on what one chat request may declare, whatever its profile.
const (
	maxMessages     = 64
	maxTools        = 16
	maxSchemaBytes  = 32 << 10
	maxToolIDLength = 128
)

var nameRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func (h *Handler) chat(w http.ResponseWriter, r *http.Request, c *call) {
	if !h.up.Configured() {
		h.refuse(w, r, c, http.StatusServiceUnavailable, unavailable(aiapi.KindNotConfigured))
		return
	}
	// Before the profile is known the largest limit applies; a body over it can only be a photo.
	body, status, bad := h.readBody(w, r, h.profiles.MaxChatBody(), h.tooLargeBeforeProfile())
	if bad != nil {
		h.refuse(w, r, c, status, *bad)
		return
	}
	c.bytes = len(body)
	var req aiapi.ChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		h.refuse(w, r, c, http.StatusBadRequest, invalid("", msgBadRequest))
		return
	}
	c.profile, c.stream, c.messages, c.tools = req.Profile, req.Stream, len(req.Messages), len(req.Tools)
	p, ok := h.profiles[req.Profile]
	if !ok || req.Profile == aiapi.ProfileTranscribe {
		h.refuse(w, r, c, http.StatusUnprocessableEntity, invalid("profile", msgBadRequest))
		return
	}
	if int64(len(body)) > p.MaxBodyBytes {
		h.refuse(w, r, c, http.StatusRequestEntityTooLarge, tooLarge(p))
		return
	}
	mreq, images, bad := build(p, req)
	c.images = images
	if bad != nil {
		h.refuse(w, r, c, http.StatusUnprocessableEntity, *bad)
		return
	}
	release, ok := h.acquire()
	if !ok {
		h.refuse(w, r, c, http.StatusTooManyRequests, tooMany(1))
		return
	}
	defer release()

	if req.Stream {
		h.relay(w, r, c, p, mreq)
		return
	}
	resp, err := h.up.Chat(r.Context(), mreq)
	if err != nil {
		if status, body, answer := h.failure(r.Context(), c, err); answer {
			writeError(w, status, body)
		}
		return
	}
	h.logDone(r, c, p, resp)
	writeJSON(w, http.StatusOK, toResponse(resp))
}

// tooLarge is the refusal of a body over its profile's limit; for an image profile the message is the photo one.
func tooLarge(p profile.Profile) aiapi.ErrorBody {
	if p.Images {
		return invalid("", msgImageTooLarge)
	}
	return invalid("", msgBadRequest)
}

// readBody reads a request body of at most limit bytes. A bad outcome is returned as the status and body to refuse with.
func (h *Handler) readBody(w http.ResponseWriter, r *http.Request, limit int64, tooBig aiapi.ErrorBody) ([]byte, int, *aiapi.ErrorBody) {
	if r.ContentLength > limit {
		return nil, http.StatusRequestEntityTooLarge, &tooBig
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		if _, isLimit := errors.AsType[*http.MaxBytesError](err); isLimit {
			return nil, http.StatusRequestEntityTooLarge, &tooBig
		}
		b := invalid("", msgBadRequest)
		return nil, http.StatusBadRequest, &b
	}
	return body, 0, nil
}

// tooLargeBeforeProfile is the refusal of a body over every profile's limit: the photo message when some profile
// takes photos (only those reach that size), else the generic one.
func (h *Handler) tooLargeBeforeProfile() aiapi.ErrorBody {
	for _, p := range h.profiles {
		if p.Images {
			return invalid("", msgImageTooLarge)
		}
	}
	return invalid("", msgBadRequest)
}

// build checks a request against its profile and turns it into the Meta call. images counts the pictures in it.
// The profile, not the caller, sets model, reasoning effort, token budget and time limit.
func build(p profile.Profile, req aiapi.ChatRequest) (mreq meta.ChatRequest, images int, bad *aiapi.ErrorBody) {
	refuse := func(field, key string) (meta.ChatRequest, int, *aiapi.ErrorBody) {
		b := invalid(field, key)
		return meta.ChatRequest{}, images, &b
	}
	if len(req.Messages) == 0 || len(req.Messages) > maxMessages {
		return refuse("messages", msgBadRequest)
	}
	mreq = meta.ChatRequest{
		Model: p.Model, ReasoningEffort: p.ReasoningEffort, MaxCompletionTokens: p.MaxTokens, Timeout: p.Timeout,
	}
	for i, m := range req.Messages {
		at := fmt.Sprintf("messages.%d", i)
		out := meta.Message{Content: m.Content}
		switch m.Role {
		case aiapi.RoleSystem, aiapi.RoleUser, aiapi.RoleAssistant, aiapi.RoleTool:
			out.Role = meta.Role(m.Role)
		default:
			return refuse(at+".role", msgBadRequest)
		}
		if len(m.Images) > 0 {
			if m.Role != aiapi.RoleUser || !p.Images {
				return refuse(at+".images", msgBadRequest)
			}
			parts := make([]meta.Part, 0, len(m.Images)+1)
			if m.Content != "" {
				parts = append(parts, meta.Text(m.Content))
			}
			for j, img := range m.Images {
				field := fmt.Sprintf("%s.images.%d", at, j)
				images++
				if images > profile.MaxImages {
					return refuse(field, msgBadRequest)
				}
				mime, key := checkImage(img)
				if key != "" {
					return refuse(field, key)
				}
				parts = append(parts, meta.ImageBytes(mime, img.Data))
			}
			out.Parts, out.Content = parts, ""
		}
		if len(m.ToolCalls) > 0 {
			if m.Role != aiapi.RoleAssistant || !p.Tools {
				return refuse(at+".toolCalls", msgBadRequest)
			}
			for j, tc := range m.ToolCalls {
				if tc.ID == "" || len(tc.ID) > maxToolIDLength || !nameRE.MatchString(tc.Name) {
					return refuse(fmt.Sprintf("%s.toolCalls.%d", at, j), msgBadRequest)
				}
				out.ToolCalls = append(out.ToolCalls, meta.ToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
			}
		}
		if m.Role == aiapi.RoleTool {
			if !p.Tools || m.ToolCallID == "" || len(m.ToolCallID) > maxToolIDLength {
				return refuse(at+".toolCallId", msgBadRequest)
			}
			out.ToolCallID = m.ToolCallID
		} else if m.ToolCallID != "" {
			return refuse(at+".toolCallId", msgBadRequest)
		}
		mreq.Messages = append(mreq.Messages, out)
	}

	if len(req.Tools) > 0 && !p.Tools || len(req.Tools) > maxTools {
		return refuse("tools", msgBadRequest)
	}
	seen := map[string]bool{}
	for i, t := range req.Tools {
		field := fmt.Sprintf("tools.%d", i)
		if !nameRE.MatchString(t.Name) || seen[t.Name] {
			return refuse(field+".name", msgBadRequest)
		}
		seen[t.Name] = true
		if len(t.Schema) > 0 && !isObject(t.Schema) {
			return refuse(field+".schema", msgBadRequest)
		}
		mreq.Tools = append(mreq.Tools, meta.Tool{Name: t.Name, Description: t.Description, Parameters: t.Schema})
	}
	if s := req.JSONSchema; s != nil {
		if !nameRE.MatchString(s.Name) {
			return refuse("jsonSchema.name", msgBadRequest)
		}
		if !isObject(s.Schema) {
			return refuse("jsonSchema.schema", msgBadRequest)
		}
		mreq.JSONSchema = &meta.JSONSchema{Name: s.Name, Schema: s.Schema}
	}
	return mreq, images, nil
}

// isObject reports whether v is a valid JSON object of a size fit for a schema.
func isObject(v jsontext.Value) bool {
	return len(v) > 0 && len(v) <= maxSchemaBytes && v.IsValid() && v[0] == '{'
}

// checkImage validates the bytes of an image and returns the MIME type they really are. The declared type is not
// trusted: the bytes are sniffed, and only PNG, JPEG, WebP and GIF pass (the formats the vision model reads). key is
// the catalog message of the refusal.
func checkImage(img aiapi.Image) (mime, key string) {
	switch {
	case len(img.Data) == 0:
		return "", msgFileEmpty
	case len(img.Data) > profile.MaxImageBytes:
		return "", msgImageTooLarge
	}
	switch sniffed := http.DetectContentType(img.Data); sniffed {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		return sniffed, ""
	}
	return "", msgNotImage
}

func toResponse(r meta.ChatResponse) aiapi.ChatResponse {
	out := aiapi.ChatResponse{
		Message:      aiapi.Message{Role: aiapi.RoleAssistant, Content: r.Message.Content},
		FinishReason: r.FinishReason, Model: r.Model,
		Usage: aiapi.Usage{Prompt: r.Usage.PromptTokens, Completion: r.Usage.CompletionTokens, Reasoning: r.Usage.ReasoningTokens, Total: r.Usage.TotalTokens},
	}
	for _, tc := range r.Message.ToolCalls {
		out.Message.ToolCalls = append(out.Message.ToolCalls, aiapi.ToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
	}
	return out
}

// logDone records a finished call: sizes, token usage and latency, never content.
func (h *Handler) logDone(r *http.Request, c *call, p profile.Profile, resp meta.ChatResponse) {
	model := resp.Model
	if model == "" {
		model = p.Model
	}
	h.log.LogAttrs(r.Context(), slog.LevelInfo, "chat", append(c.attrs(),
		slog.String("model", model), slog.String("finish", resp.FinishReason), slog.Int("status", http.StatusOK),
		slog.Int("promptTokens", resp.Usage.PromptTokens), slog.Int("completionTokens", resp.Usage.CompletionTokens),
		slog.Int("reasoningTokens", resp.Usage.ReasoningTokens), slog.Int("toolCalls", len(resp.Message.ToolCalls)))...)
}

// ndjson writes events as they happen, one JSON object per line, flushing each. The response starts with the
// first event: a failure before that is still an ordinary error response with its own status.
type ndjson struct {
	w       http.ResponseWriter
	rc      *http.ResponseController
	started bool
}

func (s *ndjson) send(ev aiapi.Event) error {
	if !s.started {
		h := s.w.Header()
		h.Set("Content-Type", "application/x-ndjson")
		h.Set("Cache-Control", "no-store")
		h.Set("X-Accel-Buffering", "no") // proxies must not hold the stream back
		s.w.WriteHeader(http.StatusOK)
		s.started = true
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if _, err := s.w.Write(append(line, '\n')); err != nil {
		return err
	}
	if err := s.rc.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	return nil
}

// relay streams a reply to the caller as NDJSON events: a delta for each increment that carries text or tool-call
// fragments, then one done event with the assembled reply, or one error event.
func (h *Handler) relay(w http.ResponseWriter, r *http.Request, c *call, p profile.Profile, mreq meta.ChatRequest) {
	out := &ndjson{w: w, rc: http.NewResponseController(w)}
	var sendErr error // a failed write: the caller has gone
	resp, err := h.up.ChatStream(r.Context(), mreq, func(d meta.Delta) error {
		if d.Content == "" && len(d.ToolCalls) == 0 {
			return nil // reasoning and bookkeeping chunks are not the caller's business
		}
		ev := aiapi.Event{Type: aiapi.EventDelta, Content: d.Content}
		for _, tc := range d.ToolCalls {
			ev.ToolCalls = append(ev.ToolCalls, aiapi.ToolCallDelta{Index: tc.Index, ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
		}
		sendErr = out.send(ev)
		return sendErr
	})
	if err != nil {
		if sendErr != nil && errors.Is(err, sendErr) {
			h.log.LogAttrs(r.Context(), slog.LevelInfo, "caller gone", append(c.attrs(), slog.String("cause", "write failed"))...)
			return
		}
		status, body, answer := h.failure(r.Context(), c, err)
		switch {
		case !answer:
		case out.started:
			_ = out.send(aiapi.Event{Type: aiapi.EventError, Error: &body})
		default:
			writeError(w, status, body)
		}
		return
	}
	h.logDone(r, c, p, resp)
	done := toResponse(resp)
	_ = out.send(aiapi.Event{Type: aiapi.EventDone, Response: &done})
}
