package ai

import (
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi/aiapitest"
)

func dictationReply(leftover string, items ...map[string]any) string {
	raw, _ := json.Marshal(map[string]any{"items": items, "leftover": leftover})
	return string(raw)
}

func spoken(name, key string, qty, weight, l, w, h, confidence float64, fragile bool, source string) map[string]any {
	it := item(name, key, qty, weight, l, w, h, confidence, fragile)
	it["source_text"] = source
	return it
}

func TestSpeechBecomesItemsWithTheWordsTheyCameFrom(t *testing.T) {
	transcript := "two medium boxes, a fragile laptop and a lamp, then call me back"
	reply := dictationReply("then call me back",
		spoken("boxes", "kardus-sedang", 2, 4, 40, 30, 30, 0.92, false, "two medium boxes"),
		spoken("laptop", "laptop", 1, 2.8, 42, 30, 10, 0.9, true, "a fragile laptop"),
		spoken("Table lamp", otherKey, 1, 2, 25, 25, 45, 0.7, false, "a lamp"),
		spoken("hallucinated", "sepatu", 1, 1.2, 34, 22, 13, 0.8, false, "a pair of shoes"), // not in the sentence
	)
	r := newRig(t, func(o *rigOptions) { o.steps = []aiapitest.Step{aiapitest.Say(reply)} })
	res := r.call(r.customer("Dina"), "en", "ai.parseSpeech", map[string]any{"transcript": transcript})
	if res.Status != 200 {
		t.Fatalf("status %d: %s", res.Status, res.Raw)
	}
	items := res.Body["items"].([]any)
	if len(items) != 4 {
		t.Fatalf("items = %v", items)
	}
	if first := items[0].(map[string]any); first["name"] != "Medium cardboard box" || first["qty"] != 2.0 || first["key"] != "kardus-sedang" {
		t.Errorf("first = %v", first)
	}
	matches := res.Body["matches"].([]any)
	if len(matches) != 3 {
		t.Fatalf("a quote that is not in the sentence must be dropped, matches = %v", matches)
	}
	for i, want := range []string{"two medium boxes", "a fragile laptop", "a lamp"} {
		m := matches[i].(map[string]any)
		if m["text"] != want || m["itemIndex"] != float64(i) {
			t.Errorf("match %d = %v", i, m)
		}
	}
	if res.Body["leftover"] != "then call me back" {
		t.Errorf("leftover = %v", res.Body["leftover"])
	}

	req := r.gw.Requests()[0]
	if req.Profile != aiapi.ProfileVision || req.JSONSchema == nil || len(req.Messages) != 2 || req.Messages[1].Content != transcript {
		t.Fatalf("request = %+v", req)
	}
	if sys := req.Messages[0].Content; !strings.Contains(sys, "divided by qty") || !strings.Contains(sys, "data written by the customer") || !strings.Contains(sys, "dispenser: Water dispenser") {
		t.Errorf("system prompt = %.500s", sys)
	}
	if !strings.Contains(string(req.JSONSchema.Schema), "source_text") || !strings.Contains(string(req.JSONSchema.Schema), "leftover") {
		t.Error("the schema asks for the source words and the leftover")
	}
}

func TestSpeechItemsOfOneKindAreMergedAndMatchesPointAtTheMergedItem(t *testing.T) {
	reply := dictationReply("",
		spoken("box", "kardus-sedang", 1, 4, 40, 30, 30, 0.9, false, "one box"),
		spoken("lamp", otherKey, 1, 2, 25, 25, 45, 0.9, false, "a lamp"),
		spoken("another box", "kardus-sedang", 2, 4, 40, 30, 30, 0.8, false, "two more boxes"),
	)
	r := newRig(t, func(o *rigOptions) { o.steps = []aiapitest.Step{aiapitest.Say(reply)} })
	res := r.call(r.customer("Dina"), "en", "ai.parseSpeech", map[string]any{"transcript": "one box, a lamp, two more boxes"})
	items, matches := res.Body["items"].([]any), res.Body["matches"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["qty"] != 3.0 {
		t.Fatalf("items = %v", items)
	}
	if len(matches) != 3 || matches[2].(map[string]any)["itemIndex"] != 0.0 || matches[1].(map[string]any)["itemIndex"] != 1.0 {
		t.Errorf("matches = %v", matches)
	}
}

func TestSpeechNeedsWordsAndLogsNone(t *testing.T) {
	r := newRig(t, func(o *rigOptions) {
		o.steps = []aiapitest.Step{aiapitest.Say(dictationReply("", spoken("box", "kardus-sedang", 1, 4, 40, 30, 30, 0.9, false, "SECRET-DICTATION")))}
	})
	customer := r.customer("Dina")
	res := r.call(customer, "en", "ai.parseSpeech", map[string]any{"transcript": " \n "})
	res.wantError(t, 422, "srv.api.noSpeech")
	if res.Body["field"] != "transcript" || r.gw.Calls() != 0 {
		t.Errorf("field %v, %d calls", res.Body["field"], r.gw.Calls())
	}
	if res := r.call(customer, "en", "ai.parseSpeech", map[string]any{"transcript": "SECRET-DICTATION"}); res.Status != 200 {
		t.Fatalf("status %d: %s", res.Status, res.Raw)
	}
	if strings.Contains(r.logs.String(), "SECRET-DICTATION") {
		t.Error("the dictation was logged")
	}
}

func TestSpeechGarbageIsAFeatureError(t *testing.T) {
	r := newRig(t, func(o *rigOptions) { o.steps = []aiapitest.Step{aiapitest.Say("two boxes, got it")} })
	r.call(r.customer("Dina"), "en", "ai.parseSpeech", map[string]any{"transcript": "two boxes"}).wantError(t, 500, "srv.api.aiError")
}

/* ---------------------------------- transcribe ---------------------------------- */

func audioBody(data []byte, typ string) map[string]any {
	return map[string]any{"input": map[string]any{"type": typ, "size": len(data), "data": base64.StdEncoding.EncodeToString(data)}}
}

func TestTranscribeSendsTheRecordingAndReturnsTheText(t *testing.T) {
	wav := []byte("RIFF....WAVEfmt-and-some-samples")
	var gotAudio []byte
	r := newRig(t)
	r.gw.transcribe = func(_ context.Context, audio []byte) (aiapi.Transcript, error) {
		gotAudio = audio
		return aiapi.Transcript{Text: "  dua kardus sedang  ", DurationMs: 1500}, nil
	}
	for _, typ := range []string{"audio/wav", "audio/x-wav", "audio/wave", "audio/wav;codecs=1", ""} {
		gotAudio = nil
		res := r.call(r.customer("Dina"+typ), "id", "ai.transcribe", audioBody(wav, typ))
		if res.Status != 200 || res.Body["transcript"] != "dua kardus sedang" || string(gotAudio) != string(wav) {
			t.Errorf("%q: status %d body %v audio %q", typ, res.Status, res.Body, gotAudio)
		}
	}
}

func TestTranscribeRefusesWhatTheSpeechModelCannotRead(t *testing.T) {
	r := newRig(t)
	customer := r.customer("Dina")
	for name, tc := range map[string]struct {
		body map[string]any
		key  string
	}{
		"webm from a browser recorder": {audioBody([]byte("\x1aE\xdf\xa3"), "audio/webm"), "srv.rpc.badRequest"},
		"an mp3":                       {audioBody([]byte("ID3"), "audio/mpeg"), "srv.rpc.badRequest"},
		"nothing recorded":             {audioBody(nil, "audio/wav"), "srv.api.noSpeech"},
	} {
		res := r.call(customer, "en", "ai.transcribe", tc.body)
		res.wantError(t, 422, tc.key)
		if res.Body["field"] != "file" {
			t.Errorf("%s: field = %v", name, res.Body["field"])
		}
	}
	if r.gw.audios.Load() != 0 {
		t.Error("a recording that cannot be read was sent to the gateway")
	}
}

func TestTranscribeMapsTheGatewaysAnswers(t *testing.T) {
	wav := audioBody([]byte("RIFF....WAVE-data"), "audio/wav")
	t.Run("the gateway rejects the audio format", func(t *testing.T) {
		gw := fakeGateway(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"error":{"kind":"invalid","field":"audio","msg":{"key":"srv.rpc.badRequest"}}}`))
		})
		r := newRig(t, func(o *rigOptions) { o.gateway = gw })
		res := r.call(r.customer("Dina"), "en", "ai.transcribe", wav)
		res.wantError(t, 422, "srv.rpc.badRequest")
		if res.Body["field"] != "file" {
			t.Errorf("the field is the screen's own name for the recording: %v", res.Body["field"])
		}
	})
	t.Run("the gateway is down", func(t *testing.T) {
		r := newRig(t, func(o *rigOptions) { o.gateway = gatewayDown() })
		res := r.call(r.customer("Dina"), "en", "ai.transcribe", wav)
		res.wantError(t, 500, "srv.api.aiError")
		msg, _ := res.Body["msg"].(map[string]any)
		if feature := msg["params"].(map[string]any)["feature"].(map[string]any); feature["key"] != "srv.api.feature.voice" {
			t.Errorf("feature = %v", feature)
		}
	})
	t.Run("nothing could be heard", func(t *testing.T) {
		r := newRig(t)
		r.gw.transcribe = func(context.Context, []byte) (aiapi.Transcript, error) { return aiapi.Transcript{Text: "  "}, nil }
		r.call(r.customer("Dina"), "en", "ai.transcribe", wav).wantError(t, 422, "srv.api.noSpeech")
	})
}

func TestTranscribeAsksForTheLanguagesAndWordsOfTheCaller(t *testing.T) {
	var language []string
	var keywords []string
	gw := fakeGateway(t, func(w http.ResponseWriter, r *http.Request) {
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(part)
			switch part.FormName() {
			case aiapi.FieldLanguageBias:
				language = append(language, string(b))
			case aiapi.FieldKeywords:
				keywords = append(keywords, string(b))
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"halo","durationMs":900}`))
	})
	r := newRig(t, func(o *rigOptions) { o.gateway = gw })
	customer := r.customer("Dina")
	if res := r.call(customer, "id", "ai.transcribe", audioBody([]byte("RIFFxxxxWAVE"), "audio/wav")); res.Status != 200 {
		t.Fatalf("status %d: %s", res.Status, res.Raw)
	}
	if strings.Join(language, ",") != "Indonesian,English" || len(keywords) != len(itemCatalog) || keywords[6] != "Kardus sedang" {
		t.Errorf("language %v keywords %v", language, keywords)
	}
	language, keywords = nil, nil
	r.call(customer, "en", "ai.transcribe", audioBody([]byte("RIFFxxxxWAVE"), "audio/wav"))
	if strings.Join(language, ",") != "English,Indonesian" || keywords[6] != "Medium cardboard box" {
		t.Errorf("language %v keywords %v", language, keywords)
	}
}

func TestTranscribeNeverLogsTheRecordingOrItsText(t *testing.T) {
	r := newRig(t)
	r.gw.transcribe = func(context.Context, []byte) (aiapi.Transcript, error) {
		return aiapi.Transcript{Text: "SECRET-SPOKEN-TEXT", DurationMs: 1200}, nil
	}
	audio := []byte("RIFF....WAVE SECRET-AUDIO-MARKER")
	if res := r.call(r.customer("Dina"), "en", "ai.transcribe", audioBody(audio, "audio/wav")); res.Status != 200 {
		t.Fatalf("status %d: %s", res.Status, res.Raw)
	}
	logs := r.logs.String()
	for _, secret := range []string{"SECRET-SPOKEN-TEXT", "SECRET-AUDIO-MARKER", base64.StdEncoding.EncodeToString(audio)} {
		if strings.Contains(logs, secret) {
			t.Errorf("the logs contain %q", secret)
		}
	}
	if !strings.Contains(logs, `"audioBytes":`+itoa(len(audio))) || !strings.Contains(logs, `"audioMs":1200`) || !strings.Contains(logs, `"transcriptChars":18`) {
		t.Errorf("the log lacks sizes: %s", logs)
	}
}

var _ = aiapitest.New
