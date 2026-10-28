package ai

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi/aiapitest"
)

func intentReply(preset string, contradictory bool, priorityWords string, present bool, deadline, deadlineWords, leftover string) string {
	raw, _ := json.Marshal(map[string]any{
		"priority": map[string]any{"preset": preset, "contradictory": contradictory, "words": priorityWords},
		"deadline": map[string]any{"present": present, "local_datetime": deadline, "words": deadlineWords},
		"leftover": leftover,
	})
	return string(raw)
}

func parseIntentVia(t *testing.T, reply, text, lang string) (res response, r *rig) {
	t.Helper()
	r = newRig(t, func(o *rigOptions) { o.steps = []aiapitest.Step{aiapitest.Say(reply)} })
	return r.call(r.customer("Dina"), lang, "ai.parseIntent", map[string]any{"text": text}), r
}

func chipsOf(t *testing.T, res response) []map[string]any {
	t.Helper()
	if res.Status != 200 {
		t.Fatalf("status %d: %s", res.Status, res.Raw)
	}
	var out []map[string]any
	for _, c := range res.Body["chips"].([]any) {
		out = append(out, c.(map[string]any))
	}
	return out
}

func TestIntentBecomesPriorityAndDeadlineChips(t *testing.T) {
	// "now" is Wednesday 21 October 2026, 14:05 in Jakarta.
	reply := intentReply("termurah", false, "paling hemat", true, "2026-10-21T17:00", "sebelum jam 5 sore", "")
	res, r := parseIntentVia(t, reply, "paling hemat tapi sampai sebelum jam 5 sore", "id")
	chips := chipsOf(t, res)
	if len(chips) != 2 {
		t.Fatalf("chips = %v", chips)
	}
	if c := chips[0]; c["id"] != "prioritas" || c["kind"] != "prioritas" || c["preset"] != "termurah" || c["label"] != "Prioritas: Termurah" || c["deadline"] != nil {
		t.Errorf("priority chip = %v", c)
	}
	if c := chips[1]; c["id"] != "batas" || c["kind"] != "batas_waktu" || c["deadline"] != "2026-10-21T10:00:00.000Z" || c["label"] != "Tiba sebelum 17.00" || c["preset"] != nil {
		t.Errorf("deadline chip = %v", c)
	}
	if got := res.Body["understood"].([]any); len(got) != 2 || got[0] != "paling hemat" || got[1] != "sebelum jam 5 sore" {
		t.Errorf("understood = %v", got)
	}
	if res.Body["leftover"] != "" {
		t.Errorf("leftover = %v", res.Body["leftover"])
	}

	req := r.gw.Requests()[0]
	if req.Profile != aiapi.ProfileIntent || req.JSONSchema == nil || len(req.Messages) != 2 || req.Messages[1].Content != "paling hemat tapi sampai sebelum jam 5 sore" {
		t.Fatalf("request = %+v", req)
	}
	if sys := req.Messages[0].Content; !strings.Contains(sys, "2026-10-21 14:05 (Wednesday)") || !strings.Contains(sys, "WIB, UTC+7") || !strings.Contains(sys, "data written by the customer") {
		t.Errorf("the model must be told the time in Jakarta and that the sentence is data: %s", sys)
	}
}

func TestIntentLabelsFollowTheLanguage(t *testing.T) {
	reply := intentReply("tercepat", false, "as fast as possible", true, "2026-10-21T17:30", "before 5:30 pm", "")
	res, _ := parseIntentVia(t, reply, "as fast as possible but before 5:30 pm", "en")
	chips := chipsOf(t, res)
	if chips[0]["label"] != "Priority: Fastest" || chips[1]["label"] != "Arrive before 17:30" {
		t.Errorf("chips = %v", chips)
	}
}

func TestContradictoryWordsBecomeBalanced(t *testing.T) {
	reply := intentReply("termurah", true, "cheap and fast", false, "", "", "")
	res, _ := parseIntentVia(t, reply, "cheap and fast please", "en")
	chips := chipsOf(t, res)
	if len(chips) != 1 || chips[0]["preset"] != "seimbang" || chips[0]["label"] != "Priority: Balanced (some words contradict each other)" {
		t.Errorf("chips = %v", chips)
	}
}

func TestInvalidChipsAreDroppedNotFailed(t *testing.T) {
	cases := []struct {
		name         string
		reply        string
		text         string
		wantChips    int
		wantLeftover string
		wantPreset   string
	}{
		{"a made-up preset", intentReply("gratis", false, "free", false, "", "", ""), "free please", 0, "", ""},
		{"no preset said", intentReply("none", false, "", false, "", "", ""), "hello", 0, "", ""},
		{"a preset quoting words that are not in the sentence", intentReply("tercepat", false, "super urgent", false, "", "", ""), "cheapest please", 0, "", ""},
		{"a deadline already past", intentReply("termurah", false, "cheapest", true, "2026-10-21T09:00", "before 9 am", ""), "cheapest but before 9 am", 1, "before 9 am", "termurah"},
		{"a deadline right now", intentReply("none", false, "", true, "2026-10-21T14:05", "now", ""), "now", 0, "now", ""},
		{"a deadline a month away", intentReply("none", false, "", true, "2026-11-21T17:00", "before 21 November", ""), "before 21 November", 0, "before 21 November", ""},
		{"a deadline that is not a date", intentReply("none", false, "", true, "tomorrow afternoon", "tomorrow afternoon", ""), "tomorrow afternoon", 0, "tomorrow afternoon", ""},
		{"a deadline claimed without words", intentReply("none", false, "", true, "2026-10-21T17:00", "", ""), "hello", 0, "", ""},
		{"a deadline quoting words that are not there", intentReply("none", false, "", true, "2026-10-21T17:00", "before five", ""), "hello", 0, "", ""},
		{"a leftover that was not said", intentReply("none", false, "", false, "", "", "something invented"), "hello there", 0, "", ""},
		{"a leftover that was said", intentReply("none", false, "", false, "", "", "for my mother, hello"), "a gift for my mother, hello", 0, "for my mother, hello", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, _ := parseIntentVia(t, tc.reply, tc.text, "en")
			chips := chipsOf(t, res)
			if len(chips) != tc.wantChips {
				t.Errorf("chips = %v", chips)
			}
			if res.Body["leftover"] != tc.wantLeftover {
				t.Errorf("leftover = %q, want %q", res.Body["leftover"], tc.wantLeftover)
			}
			if tc.wantPreset != "" && chips[0]["preset"] != tc.wantPreset {
				t.Errorf("preset = %v", chips[0]["preset"])
			}
			// The answer always has the three fields, as arrays and a string.
			if _, ok := res.Body["chips"].([]any); !ok {
				t.Errorf("chips must be an array: %s", res.Raw)
			}
			if _, ok := res.Body["understood"].([]any); !ok {
				t.Errorf("understood must be an array: %s", res.Raw)
			}
		})
	}
}

func TestDeadlineHorizonAndSemantics(t *testing.T) {
	sp := newSpeech("en")
	cases := map[string]bool{
		"2026-10-21T17:00":    true,  // this afternoon
		"2026-10-22T09:00":    true,  // tomorrow
		"2026-10-28T14:04":    true,  // just under a week
		"2026-10-28T14:30":    false, // beyond a week
		"2026-10-21T14:05":    false, // now
		"2026-10-21T14:05:30": false, // a few seconds ahead is no deadline
		"2026-10-21 17:00":    true,
		"2026-10-21T17:00:00": true,
		"17:00":               false,
		"":                    false,
	}
	for in, want := range cases {
		if _, ok := parseDeadline(in, now); ok != want {
			t.Errorf("parseDeadline(%q) = %v, want %v", in, ok, want)
		}
	}
	_ = sp
}

func TestIntentRefusesAnEmptySentence(t *testing.T) {
	r := newRig(t, func(o *rigOptions) { o.steps = []aiapitest.Step{aiapitest.Say("{}")} })
	res := r.call(r.customer("Dina"), "en", "ai.parseIntent", map[string]any{"text": "   \n "})
	res.wantError(t, 422, "srv.api.intentEmpty")
	if res.Body["field"] != "text" || r.gw.Calls() != 0 {
		t.Errorf("field %v, %d model calls", res.Body["field"], r.gw.Calls())
	}
}

func TestIntentGarbageFromTheModelIsAFeatureError(t *testing.T) {
	r := newRig(t, func(o *rigOptions) { o.steps = []aiapitest.Step{aiapitest.Say("sure! cheapest it is")} })
	res := r.call(r.customer("Dina"), "en", "ai.parseIntent", map[string]any{"text": "cheapest"})
	res.wantError(t, 500, "srv.api.aiError")
}

func TestIntentLongTextIsCutAndNeverLogged(t *testing.T) {
	r := newRig(t, func(o *rigOptions) {
		o.steps = []aiapitest.Step{aiapitest.Say(intentReply("none", false, "", false, "", "", ""))}
	})
	long := "SECRET-SENTENCE " + strings.Repeat("a ", 1000)
	if res := r.call(r.customer("Dina"), "en", "ai.parseIntent", map[string]any{"text": long}); res.Status != 200 {
		t.Fatalf("status %d: %s", res.Status, res.Raw)
	}
	if got := r.gw.Requests()[0].Messages[1].Content; len([]rune(got)) > maxIntentRunes {
		t.Errorf("%d characters were sent to the model", len([]rune(got)))
	}
	if strings.Contains(r.logs.String(), "SECRET-SENTENCE") {
		t.Error("the sentence was logged")
	}
}

func intentReplyAt(present bool, deadline, place, words string) string {
	raw, _ := json.Marshal(map[string]any{
		"priority": map[string]any{"preset": "none", "contradictory": false, "words": ""},
		"deadline": map[string]any{"present": present, "local_datetime": deadline, "place": place, "words": words},
		"leftover": "",
	})
	return string(raw)
}

func TestDeadlineNamesItsPlaceAndADateThatIsNotToday(t *testing.T) {
	// "now" is Wednesday 21 October 2026, 14:05 in Jakarta: a deadline tomorrow must say so, and the drop-off named
	// in the sentence travels with the chip so the wizard sets it on that shipment only.
	text := "the cake to Kota Kasablanka must arrive before 5 pm tomorrow"
	res, _ := parseIntentVia(t, intentReplyAt(true, "2026-10-22T17:00", "Kota Kasablanka", "before 5 pm tomorrow"), text, "en")
	chips := chipsOf(t, res)
	if len(chips) != 1 {
		t.Fatalf("chips = %v", chips)
	}
	c := chips[0]
	if c["place"] != "Kota Kasablanka" || c["deadline"] != "2026-10-22T10:00:00.000Z" {
		t.Errorf("deadline chip = %v", c)
	}
	label, _ := c["label"].(string)
	if !strings.HasPrefix(label, "Arrive at Kota Kasablanka before 17:00 on ") || !strings.Contains(label, "22") {
		t.Errorf("label = %q, want the place and tomorrow's date", label)
	}

	// today, no place: the plain label
	res, _ = parseIntentVia(t, intentReplyAt(true, "2026-10-21T17:00", "", "before 5 pm"), "everything before 5 pm", "en")
	if c := chipsOf(t, res)[0]; c["label"] != "Arrive before 17:00" || c["place"] != nil {
		t.Errorf("today chip = %v", c)
	}

	// a place the customer never wrote is dropped, the deadline stays for every parcel
	res, _ = parseIntentVia(t, intentReplyAt(true, "2026-10-21T17:00", "Plaza Senayan", "before 5 pm"), "everything before 5 pm", "en")
	if c := chipsOf(t, res)[0]; c["place"] != nil || c["label"] != "Arrive before 17:00" {
		t.Errorf("ungrounded place kept: %v", c)
	}
}
