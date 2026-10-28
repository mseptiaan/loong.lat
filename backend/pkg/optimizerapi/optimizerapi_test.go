package optimizerapi

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

func TestTheClientRelaysTheStepsOfAThoroughSearch(t *testing.T) {
	var got QuoteRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.UnmarshalRead(r.Body, &got); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		for _, ev := range []Event{
			{Type: EventProgress, Stage: 3, Total: 4},
			{Type: EventProgress, Stage: 3, Total: 4, Msg: &contracts.Msg{Key: "srv.engine.search.round", Params: map[string]any{"round": 2}}},
			{Type: EventDone, Result: &QuoteResult{Proven: true}},
		} {
			data, _ := json.Marshal(ev)
			w.Write(append(data, '\n'))
		}
	}))
	defer srv.Close()

	var seen []Progress
	res, err := (&Client{BaseURL: srv.URL}).Quote(context.Background(), QuoteRequest{PlanID: "PL-1", Effort: contracts.EffortReviewed, AIReview: true}, func(p Progress) { seen = append(seen, p) })
	if err != nil || !res.Proven {
		t.Fatalf("res %+v err %v", res, err)
	}
	if got.Effort != contracts.EffortReviewed || !got.AIReview {
		t.Errorf("the server received %+v", got)
	}
	if len(seen) != 2 || seen[0].Msg != nil || seen[1].Msg == nil || seen[1].Msg.Key != "srv.engine.search.round" || seen[1].Stage != 3 {
		t.Fatalf("progress = %+v", seen)
	}
	if round, _ := seen[1].Msg.Params["round"].(float64); round != 2 {
		t.Errorf("params = %v", seen[1].Msg.Params)
	}
}

func TestAFastRequestAndItsEventsStayAsTheyWere(t *testing.T) {
	data, err := json.Marshal(QuoteRequest{PlanID: "PL-1"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "effort") || strings.Contains(string(data), "aiReview") {
		t.Errorf("a request that names no effort carries no effort or aiReview: %s", data)
	}
	data, _ = json.Marshal(Event{Type: EventProgress, Stage: 1, Total: 4})
	if strings.Contains(string(data), "msg") {
		t.Errorf("a stage event carries no msg: %s", data)
	}
}
