package meta

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json/v2"
	"fmt"
	"mime"
	"mime/multipart"
	"net/textproto"
	"path"
	"strings"
	"time"
)

// TranscribeOption adjusts one Transcribe call.
type TranscribeOption func(*transcribeOptions)

type transcribeOptions struct {
	model        string
	keywords     []string
	languageBias []string
	timeout      time.Duration
}

// WithTranscribeModel overrides the client's transcription model.
func WithTranscribeModel(model string) TranscribeOption {
	return func(o *transcribeOptions) { o.model = model }
}

// WithKeywords biases recognition toward names and jargon the model would otherwise mishear.
func WithKeywords(words ...string) TranscribeOption {
	return func(o *transcribeOptions) { o.keywords = words }
}

// WithLanguageBias tells the model which languages to expect, as language names ("Indonesian", "English").
func WithLanguageBias(languages ...string) TranscribeOption {
	return func(o *transcribeOptions) { o.languageBias = languages }
}

// WithTranscribeTimeout bounds the call, retries included.
func WithTranscribeTimeout(d time.Duration) TranscribeOption {
	return func(o *transcribeOptions) { o.timeout = d }
}

type wireTranscribeRequest struct {
	Model         string   `json:"model"`
	AudioEncoding string   `json:"audioEncoding"`
	Mode          string   `json:"mode"`
	Keywords      []string `json:"keywords,omitempty"`
	LanguageBias  []string `json:"languageBias,omitempty"`
}

type wireTranscript struct {
	SessionID       string `json:"sessionId"`
	Transcript      string `json:"transcript"`
	AudioDurationMs int64  `json:"audioDurationMs"`
	Turns           []struct {
		TurnID     int    `json:"turnId"`
		StartMs    int64  `json:"startMs"`
		EndMs      int64  `json:"endMs"`
		Transcript string `json:"transcript"`
		Speaker    string `json:"speaker"`
	} `json:"turns"`
}

// Transcribe sends one recording to the file endpoint (POST /asr/transcribe) and returns its text. The API takes
// only WAV: mono integer PCM at 16 or 24 kHz, up to 10 minutes and 32 MB (CheckWAV tells which rule a file
// breaks). contentType defaults to audio/wav. 429 and 5xx are retried like Chat's.
func (c *Client) Transcribe(ctx context.Context, audio []byte, filename, contentType string, opts ...TranscribeOption) (Transcript, error) {
	var o transcribeOptions
	for _, opt := range opts {
		opt(&o)
	}
	if err := CheckWAV(audio); err != nil {
		return Transcript{}, err
	}
	reqPart, err := json.Marshal(wireTranscribeRequest{
		Model: cmpOr(o.model, c.cfg.TranscribeModel), AudioEncoding: "WAV", Mode: "PUSH_TO_TALK",
		Keywords: o.keywords, LanguageBias: o.languageBias,
	})
	if err != nil {
		return Transcript{}, &Error{Kind: KindBadRequest, Message: "the request cannot be encoded", cause: err}
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	hdr := textproto.MIMEHeader{}
	hdr.Set("Content-Disposition", `form-data; name="request"`)
	hdr.Set("Content-Type", "application/json")
	part, err := mw.CreatePart(hdr)
	if err != nil {
		return Transcript{}, &Error{Kind: KindBadRequest, Message: "the request cannot be encoded", cause: err}
	}
	part.Write(reqPart)

	hdr = textproto.MIMEHeader{}
	hdr.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "audio", "filename": safeFilename(filename)}))
	hdr.Set("Content-Type", cmpOr(contentType, "audio/wav"))
	part, err = mw.CreatePart(hdr)
	if err != nil {
		return Transcript{}, &Error{Kind: KindBadRequest, Message: "the request cannot be encoded", cause: err}
	}
	part.Write(audio)
	if err := mw.Close(); err != nil {
		return Transcript{}, &Error{Kind: KindBadRequest, Message: "the request cannot be encoded", cause: err}
	}

	callCtx, cancel := c.callContext(ctx, o.timeout)
	defer cancel()
	resp, err := c.send(ctx, callCtx, call{path: "/asr/transcribe", body: body.Bytes(), contentType: mw.FormDataContentType(), accept: "application/json", retry: true})
	if err != nil {
		return Transcript{}, err
	}
	raw, err := c.readBody(ctx, callCtx, resp)
	if err != nil {
		return Transcript{}, err
	}
	var w wireTranscript
	if err := json.Unmarshal(raw, &w); err != nil {
		return Transcript{}, malformed("the transcript is not valid JSON", err, c.cfg.APIKey)
	}
	out := Transcript{Text: strings.TrimSpace(w.Transcript), SessionID: w.SessionID, DurationMs: w.AudioDurationMs}
	for _, t := range w.Turns {
		out.Turns = append(out.Turns, Turn{ID: t.TurnID, StartMs: t.StartMs, EndMs: t.EndMs, Text: t.Transcript, Speaker: t.Speaker})
	}
	return out, nil
}

// safeFilename keeps a client-supplied name from carrying path parts or odd characters into the multipart header.
func safeFilename(name string) string {
	name = path.Base(strings.ReplaceAll(name, `\`, "/"))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '"' {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." || name == "/" {
		return "audio.wav"
	}
	return name
}

// CheckWAV reports why audio would be refused by the transcription endpoint, or nil. It reads the header only:
// a RIFF/WAVE container holding mono integer PCM at 16 or 24 kHz, with some sample data.
func CheckWAV(audio []byte) error {
	bad := func(format string, args ...any) error {
		return &Error{Kind: KindBadRequest, Param: "audio", Message: "audio must be a mono PCM WAV at 16 or 24 kHz: " + fmt.Sprintf(format, args...)}
	}
	if len(audio) == 0 {
		return bad("the file is empty")
	}
	if len(audio) < 12 || string(audio[0:4]) != "RIFF" || string(audio[8:12]) != "WAVE" {
		return bad("not a RIFF/WAVE file")
	}
	var haveFormat bool
	for pos := 12; pos+8 <= len(audio); {
		id := string(audio[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(audio[pos+4 : pos+8]))
		body := pos + 8
		switch id {
		case "fmt ":
			if size < 16 || body+16 > len(audio) {
				return bad("the format chunk is truncated")
			}
			format := binary.LittleEndian.Uint16(audio[body:])
			channels := binary.LittleEndian.Uint16(audio[body+2:])
			rate := binary.LittleEndian.Uint32(audio[body+4:])
			bits := binary.LittleEndian.Uint16(audio[body+14:])
			if format != 1 && format != 0xFFFE {
				return bad("the samples are not integer PCM (format %d)", format)
			}
			if channels != 1 {
				return bad("it has %d channels, want 1", channels)
			}
			if rate != 16000 && rate != 24000 {
				return bad("the sample rate is %d Hz, want 16000 or 24000", rate)
			}
			if bits == 0 || bits > 32 || bits%8 != 0 {
				return bad("%d bits per sample is not supported", bits)
			}
			haveFormat = true
		case "data":
			if !haveFormat {
				return bad("the data chunk comes before the format chunk")
			}
			// A recorder that streams to a file leaves the size 0 or 0xFFFFFFFF: whatever follows is the data.
			if size == 0 || size == 0xFFFFFFFF || body+size > len(audio) {
				size = len(audio) - body
			}
			if size <= 0 {
				return bad("it holds no samples")
			}
			return nil
		}
		pos = body + size + size%2 // chunks are word aligned
	}
	return bad("there is no sample data")
}
