package api

import (
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/meta"
)

// Limits on the hints of a transcription request.
const (
	maxHints    = 50
	maxHintLen  = 64
	maxLanguage = 5
)

func (h *Handler) transcribe(w http.ResponseWriter, r *http.Request, c *call) {
	c.profile = aiapi.ProfileTranscribe
	p := h.profiles[aiapi.ProfileTranscribe]
	if !h.up.Configured() {
		h.refuse(w, r, c, http.StatusServiceUnavailable, unavailable(aiapi.KindNotConfigured))
		return
	}
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "multipart/form-data" {
		h.refuse(w, r, c, http.StatusBadRequest, invalid("", msgBadRequest))
		return
	}
	tooBig := invalid(aiapi.FieldAudio, msgBadRequest)
	if r.ContentLength > p.MaxBodyBytes {
		h.refuse(w, r, c, http.StatusRequestEntityTooLarge, tooBig)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, p.MaxBodyBytes) // before MultipartReader, which reads from r.Body
	mr, err := r.MultipartReader()
	if err != nil {
		h.refuse(w, r, c, http.StatusBadRequest, invalid("", msgBadRequest))
		return
	}

	var (
		audio                 []byte
		filename, contentType string
		haveAudio             bool
		keywords, languages   []string
	)
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			h.refuse(w, r, c, bodyStatus(err), bodyProblem(err, tooBig))
			return
		}
		switch part.FormName() {
		case aiapi.FieldAudio:
			if haveAudio {
				h.refuse(w, r, c, http.StatusUnprocessableEntity, invalid(aiapi.FieldAudio, msgBadRequest))
				return
			}
			audio, err = io.ReadAll(part)
			if err != nil {
				h.refuse(w, r, c, bodyStatus(err), bodyProblem(err, tooBig))
				return
			}
			haveAudio, filename, contentType = true, part.FileName(), part.Header.Get("Content-Type")
		case aiapi.FieldKeywords, aiapi.FieldLanguageBias:
			text, err := io.ReadAll(io.LimitReader(part, 4*maxHintLen+1))
			if err != nil {
				h.refuse(w, r, c, bodyStatus(err), bodyProblem(err, tooBig))
				return
			}
			value := strings.TrimSpace(string(text))
			if len(text) > 4*maxHintLen || utf8.RuneCountInString(value) > maxHintLen || !utf8.ValidString(value) {
				h.refuse(w, r, c, http.StatusUnprocessableEntity, invalid(part.FormName(), msgBadRequest))
				return
			}
			switch {
			case value == "":
			case part.FormName() == aiapi.FieldKeywords:
				keywords = append(keywords, value)
			default:
				languages = append(languages, value)
			}
			if len(keywords) > maxHints || len(languages) > maxLanguage {
				h.refuse(w, r, c, http.StatusUnprocessableEntity, invalid(part.FormName(), msgBadRequest))
				return
			}
		}
	}
	c.bytes = len(audio)
	if !haveAudio {
		h.refuse(w, r, c, http.StatusUnprocessableEntity, invalid(aiapi.FieldAudio, msgBadRequest))
		return
	}
	if len(audio) == 0 {
		h.refuse(w, r, c, http.StatusUnprocessableEntity, invalid(aiapi.FieldAudio, msgNoSpeech))
		return
	}
	if err := meta.CheckWAV(audio); err != nil {
		h.refuse(w, r, c, http.StatusUnprocessableEntity, invalid(aiapi.FieldAudio, msgBadRequest))
		return
	}
	release, ok := h.acquire()
	if !ok {
		h.refuse(w, r, c, http.StatusTooManyRequests, tooMany(1))
		return
	}
	defer release()

	opts := []meta.TranscribeOption{meta.WithTranscribeModel(p.Model), meta.WithTranscribeTimeout(p.Timeout)}
	if len(keywords) > 0 {
		opts = append(opts, meta.WithKeywords(keywords...))
	}
	if len(languages) > 0 {
		opts = append(opts, meta.WithLanguageBias(languages...))
	}
	tr, err := h.up.Transcribe(r.Context(), audio, filename, contentType, opts...)
	if err != nil {
		if status, body, answer := h.failure(r.Context(), c, err); answer {
			writeError(w, status, body)
		}
		return
	}
	// Lengths only: what was said is not ours to keep.
	h.log.LogAttrs(r.Context(), slog.LevelInfo, "transcribe", append(c.attrs(),
		slog.String("model", p.Model), slog.Int("status", http.StatusOK), slog.Int64("audioMs", tr.DurationMs), slog.Int("chars", utf8.RuneCountInString(tr.Text)))...)
	writeJSON(w, http.StatusOK, aiapi.Transcript{Text: tr.Text, DurationMs: tr.DurationMs})
}

// bodyStatus and bodyProblem classify a failure to read the request body: over the limit, or just unreadable.
func bodyStatus(err error) int {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

func bodyProblem(err error, tooBig aiapi.ErrorBody) aiapi.ErrorBody {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return tooBig
	}
	return invalid("", msgBadRequest)
}
