package testkit

import (
	"bufio"
	"context"
	"encoding/json/v2"
	"net/http"
	"strings"
	"testing"

	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

// Events opens a Server-Sent Events request and returns the events it delivers as they arrive. It returns once
// the stream is open, so an event sent afterwards is not missed. The stream is closed when the test ends.
func Events(t testing.TB, req *http.Request) <-chan platform.Event {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("event stream: status %d", resp.StatusCode)
	}
	events := make(chan platform.Event, 64)
	go func() {
		defer resp.Body.Close()
		defer close(events)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			data, ok := strings.CutPrefix(sc.Text(), "data: ")
			if !ok {
				continue
			}
			var ev platform.Event
			if json.Unmarshal([]byte(data), &ev) == nil {
				events <- ev
			}
		}
	}()
	return events
}
