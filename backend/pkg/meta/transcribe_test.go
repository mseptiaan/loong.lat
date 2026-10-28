package meta

import (
	"bytes"
	"encoding/binary"
	"encoding/json/v2"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"
)

// wav builds a WAV file: format 1 = PCM; samples bytes of data.
func wav(format, channels uint16, rate uint32, bits uint16, samples int) []byte {
	var b bytes.Buffer
	le := binary.LittleEndian
	data := make([]byte, samples)
	b.WriteString("RIFF")
	binary.Write(&b, le, uint32(36+len(data)))
	b.WriteString("WAVEfmt ")
	binary.Write(&b, le, uint32(16))
	binary.Write(&b, le, format)
	binary.Write(&b, le, channels)
	binary.Write(&b, le, rate)
	binary.Write(&b, le, rate*uint32(channels)*uint32(bits)/8)
	binary.Write(&b, le, channels*bits/8)
	binary.Write(&b, le, bits)
	b.WriteString("data")
	binary.Write(&b, le, uint32(len(data)))
	b.Write(data)
	return b.Bytes()
}

func goodWAV() []byte { return wav(1, 1, 16000, 16, 3200) }

func TestTranscribeSendsMultipartAndParsesTheTranscript(t *testing.T) {
	c, api, _ := newClient(t, func(_ int, w http.ResponseWriter, r *http.Request, body []byte) {
		writeJSON(w, 200, `{"sessionId":"s-1","transcript":"  dua kardus dari Kemang ke Senayan ","audioDurationMs":8240,"turns":[{"turnId":1,"startMs":10,"endMs":8000,"transcript":"dua kardus dari Kemang ke Senayan","speaker":"A"}]}`)
	})
	got, err := c.Transcribe(t.Context(), goodWAV(), "../../clips/ucapan saya.wav", "audio/wav",
		WithKeywords("Kemang", "Senayan"), WithLanguageBias("Indonesian"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "dua kardus dari Kemang ke Senayan" || got.SessionID != "s-1" || got.DurationMs != 8240 || len(got.Turns) != 1 || got.Turns[0].Speaker != "A" {
		t.Errorf("transcript = %+v", got)
	}

	rq := api.request(0)
	if rq.Path != "/v1/asr/transcribe" || rq.Header.Get("Authorization") != "Bearer "+testKey || rq.Header.Get("Accept") != "application/json" {
		t.Errorf("request = %s %v", rq.Path, rq.Header)
	}
	mt, params, err := mime.ParseMediaType(rq.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/form-data" {
		t.Fatalf("content type %q: %v", rq.Header.Get("Content-Type"), err)
	}
	parts := map[string]*multipart.Part{}
	contents := map[string][]byte{}
	mr := multipart.NewReader(bytes.NewReader(rq.Body), params["boundary"])
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		parts[p.FormName()] = p
		contents[p.FormName()], _ = io.ReadAll(p)
	}
	if parts["request"] == nil || parts["audio"] == nil {
		t.Fatalf("parts = %v", parts)
	}
	if ct := parts["request"].Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("request part content type = %q", ct)
	}
	var settings map[string]any
	if err := json.Unmarshal(contents["request"], &settings); err != nil {
		t.Fatal(err)
	}
	if settings["model"] != DefaultTranscribeModel || settings["audioEncoding"] != "WAV" || settings["mode"] != "PUSH_TO_TALK" ||
		len(settings["keywords"].([]any)) != 2 || settings["languageBias"].([]any)[0] != "Indonesian" {
		t.Errorf("settings = %v", settings)
	}
	if parts["audio"].FileName() != "ucapan saya.wav" || parts["audio"].Header.Get("Content-Type") != "audio/wav" || !bytes.Equal(contents["audio"], goodWAV()) {
		t.Errorf("audio part: file %q type %q, %d bytes", parts["audio"].FileName(), parts["audio"].Header.Get("Content-Type"), len(contents["audio"]))
	}
}

func TestTranscribeRetriesRateLimits(t *testing.T) {
	c, api, waits := newClient(t, func(n int, w http.ResponseWriter, _ *http.Request, _ []byte) {
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			apiError(w, 429, "rate_limit_error", "rate_limit_exceeded", "too many concurrent sessions")
			return
		}
		writeJSON(w, 200, `{"transcript":"halo","audioDurationMs":1000,"turns":[]}`)
	})
	got, err := c.Transcribe(t.Context(), goodWAV(), "a.wav", "")
	if err != nil || got.Text != "halo" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if api.count() != 2 || len(*waits) != 1 || (*waits)[0] != time.Second {
		t.Errorf("requests %d, waits %v", api.count(), *waits)
	}
	// The retry must resend the whole multipart body.
	if !bytes.Equal(api.request(0).Body, api.request(1).Body) && !bytes.Contains(api.request(1).Body, goodWAV()) {
		t.Error("the second attempt lost the audio")
	}
}

func TestTranscribeErrors(t *testing.T) {
	c, _, _ := newClient(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) {
		apiError(w, 400, "invalid_request_error", "", "audio exceeds 10 minutes")
	})
	_, err := c.Transcribe(t.Context(), goodWAV(), "a.wav", "audio/wav")
	e := wantKind(t, err, ErrBadRequest)
	if !strings.Contains(e.Message, "10 minutes") {
		t.Errorf("error = %+v", e)
	}

	c, _, _ = newClient(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) { writeJSON(w, 200, `not json`) })
	_, err = c.Transcribe(t.Context(), goodWAV(), "a.wav", "audio/wav")
	wantKind(t, err, ErrMalformed)
}

func TestTranscribeRefusesWhatTheAPICannotReadWithoutCallingIt(t *testing.T) {
	c, api, _ := newClient(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ []byte) { writeJSON(w, 200, `{}`) })
	_, err := c.Transcribe(t.Context(), []byte("\x1aE\xdf\xa3 this is webm"), "a.webm", "audio/webm")
	e := wantKind(t, err, ErrBadRequest)
	if e.Param != "audio" || api.count() != 0 {
		t.Errorf("error %+v, %d requests", e, api.count())
	}
}

func TestCheckWAV(t *testing.T) {
	// A recorder streaming to disk writes the data size as 0 or 0xFFFFFFFF.
	streaming := goodWAV()
	binary.LittleEndian.PutUint32(streaming[40:], 0xFFFFFFFF)
	noSamples := wav(1, 1, 16000, 16, 0)
	oddChunk := append(append([]byte{}, goodWAV()[:36]...), []byte("LIST\x03\x00\x00\x00abc\x00")...)
	oddChunk = append(oddChunk, goodWAV()[36:]...)

	cases := []struct {
		name  string
		audio []byte
		want  string // "" = valid, else a fragment of the reason
	}{
		{"mono 16 kHz 16-bit", goodWAV(), ""},
		{"mono 24 kHz", wav(1, 1, 24000, 16, 100), ""},
		{"extensible PCM", wav(0xFFFE, 1, 16000, 16, 100), ""},
		{"streaming size field", streaming, ""},
		{"a LIST chunk before the data", oddChunk, ""},
		{"empty", nil, "empty"},
		{"not wav", []byte("OggS....not a wav file at all"), "RIFF/WAVE"},
		{"too short", []byte("RIFF"), "RIFF/WAVE"},
		{"stereo", wav(1, 2, 16000, 16, 100), "2 channels"},
		{"44.1 kHz", wav(1, 1, 44100, 16, 100), "44100 Hz"},
		{"float samples", wav(3, 1, 16000, 32, 100), "not integer PCM"},
		{"no samples", noSamples, "no samples"},
		{"header only", goodWAV()[:30], "format chunk"},
	}
	for _, tc := range cases {
		err := CheckWAV(tc.audio)
		if tc.want == "" {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", tc.name, err, tc.want)
			continue
		}
		if !strings.Contains(err.Error(), "bad_request") {
			t.Errorf("%s: want a bad_request error, got %v", tc.name, err)
		}
	}
}

func TestSafeFilename(t *testing.T) {
	for in, want := range map[string]string{
		"":                     "audio.wav",
		"voice.wav":            "voice.wav",
		"../../etc/passwd":     "passwd",
		`C:\Users\me\clip.wav`: "clip.wav",
		"a\"b\r\nc.wav":        "abc.wav",
		"/":                    "audio.wav",
	} {
		if got := safeFilename(in); got != want {
			t.Errorf("safeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}
