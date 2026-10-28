// Package optimizerapi is the contract between the optimizer service and the
// services that ask it for quotes (console-api today, backoffice-api for
// re-planning later). It holds the wire types and a client; nothing from the
// optimizer's internals leaks through.
//
// A quote is a stream: progress events as the work advances, then either the
// finished quote or an error, one JSON object per line.
package optimizerapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

// QuotePath is where the optimizer serves quotes.
const QuotePath = "/v1/quote"

// InfoPath is where the optimizer says what it is and what it can do.
const InfoPath = "/v1/info"

// Info is what GET /v1/info reports.
type Info struct {
	// Engine is the build of the optimization engine; Runner says where jobs run (local, exec or modal).
	Engine string `json:"engine"`
	Runner string `json:"runner"`
	// Efforts are the search efforts a quote may ask for.
	Efforts []contracts.Effort `json:"efforts"`
	// AIReview says the model can review a reviewed quote; when false the built-in rules do.
	AIReview bool `json:"aiReview"`
}

// QuoteRequest is what to quote: the shipments, when they should move, how to
// weigh the options, and the fleet and tariffs to price them with.
type QuoteRequest struct {
	PlanID    string               `json:"planId"`
	Shipments []contracts.Shipment `json:"shipments"`
	Schedule  contracts.Schedule   `json:"schedule"`
	Priority  contracts.Priority   `json:"priority"`
	Vehicles  []contracts.Vehicle  `json:"vehicles"`
	// Now is the clock the quote is computed against; the server uses its own when it is zero.
	Now contracts.Time `json:"now,omitzero"`
	// Effort is how hard to search; empty means fast.
	Effort contracts.Effort `json:"effort,omitempty"`
	// AIReview lets the reviewer of a reviewed quote be the AI model (the caller has AI enabled); otherwise the
	// built-in rules review.
	AIReview bool `json:"aiReview,omitzero"`
}

// Event types of the stream.
const (
	EventProgress = "progress"
	EventDone     = "done"
	EventError    = "error"
)

// Event is one line of the quote stream.
type Event struct {
	Type string `json:"type"`
	// Stage counts from 1 to Total while the quote is being computed.
	Stage int `json:"stage,omitzero"`
	Total int `json:"total,omitzero"`
	// Msg, on a progress event, says what a thorough search is doing inside the stage (a round, a polish);
	// the event then repeats the stage it belongs to.
	Msg    *contracts.Msg `json:"msg,omitempty"`
	Result *QuoteResult   `json:"result,omitempty"`
	Error  *Error         `json:"error,omitempty"`
}

// QuoteResult is a computed quote with how it was made.
type QuoteResult struct {
	OptionSet contracts.OptionSet `json:"optionSet"`
	// Truncated means a time limit cut a search short; do not cache such a quote.
	Truncated bool `json:"truncated,omitzero"`
	// Proven means every option was confirmed optimal by an exhaustive search.
	Proven bool `json:"proven,omitzero"`
	// RoadSource is "mapbox", "mapbox-cache" or "estimate".
	RoadSource string `json:"roadSource,omitempty"`
	Engine     string `json:"engine,omitempty"`
}

// Error is a quote that could not be computed. Code is the frontend's ApiError
// code ("tidak_valid" for input the customer can fix, "layanan" for a fault of
// ours), Field the input at fault, Msg a language-neutral explanation.
type Error struct {
	Code  string        `json:"code"`
	Field string        `json:"field,omitempty"`
	Msg   contracts.Msg `json:"msg"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Msg.Key }

// Client asks an optimizer service for quotes.
type Client struct {
	// BaseURL is the optimizer's address, e.g. http://optimizer:8090.
	BaseURL string
	// Token is the shared secret of service-to-service calls.
	Token string
	HTTP  *http.Client
}

// Progress is a progress event: a stage that has begun, or, with Msg set, a step of a thorough search inside it.
type Progress struct {
	Stage, Total int
	Msg          *contracts.Msg
}

// Quote computes a quote. onProgress, which may be nil, is called as stages begin and as a thorough search
// reports its steps. A quote the optimizer refused comes back as *Error.
func (c *Client) Quote(ctx context.Context, req QuoteRequest, onProgress func(Progress)) (*QuoteResult, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+QuotePath, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("Accept", "application/x-ndjson")
	if c.Token != "" {
		hr.Header.Set("Authorization", "Bearer "+c.Token)
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	resp, err := client.Do(hr)
	if err != nil {
		return nil, fmt.Errorf("optimizer: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("optimizer: http %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 32<<20)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			return nil, fmt.Errorf("optimizer: bad event: %w", err)
		}
		switch ev.Type {
		case EventProgress:
			if onProgress != nil {
				onProgress(Progress{Stage: ev.Stage, Total: ev.Total, Msg: ev.Msg})
			}
		case EventDone:
			if ev.Result == nil {
				return nil, errors.New("optimizer: empty result")
			}
			return ev.Result, nil
		case EventError:
			if ev.Error == nil {
				return nil, errors.New("optimizer: empty error")
			}
			return nil, ev.Error
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("optimizer: %w", err)
	}
	return nil, errors.New("optimizer: stream ended without a result")
}
