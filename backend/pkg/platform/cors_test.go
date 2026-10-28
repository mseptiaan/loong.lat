package platform

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func corsServer(origins ...string) http.Handler {
	return CORS(origins)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Trace-Id", "t")
		w.Write([]byte("ok"))
	}))
}

func do(h http.Handler, method, origin string, headers ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/v1/rpc/plans.get", nil)
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestCORSAnswersPreflightsOfListedOriginsOnly(t *testing.T) {
	h := corsServer("https://console.loong.lat", "http://console.loong.localhost/")

	for _, origin := range []string{"https://console.loong.lat", "http://console.loong.localhost", "HTTP://Console.Loong.Localhost"} {
		w := do(h, http.MethodOptions, origin, "Access-Control-Request-Method", "POST", "Access-Control-Request-Headers", "authorization,idempotency-key")
		if w.Code != http.StatusNoContent {
			t.Errorf("%s: preflight status %d, want 204", origin, w.Code)
		}
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Errorf("%s: allow-origin = %q, want the origin echoed back", origin, got)
		}
		for _, want := range []string{"Authorization", "Idempotency-Key", "Content-Type"} {
			if !containsFold(w.Header().Get("Access-Control-Allow-Headers"), want) {
				t.Errorf("%s: preflight does not allow the %s header: %q", origin, want, w.Header().Get("Access-Control-Allow-Headers"))
			}
		}
		if w.Header().Get("Access-Control-Allow-Credentials") != "" {
			t.Errorf("%s: credentials must not be allowed; tokens travel in a header", origin)
		}
	}

	w := do(h, http.MethodOptions, "https://evil.example", "Access-Control-Request-Method", "POST")
	if w.Code != http.StatusForbidden || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("an unlisted origin must be refused: status %d, allow-origin %q", w.Code, w.Header().Get("Access-Control-Allow-Origin"))
	}
}

func containsFold(list, want string) bool {
	return strings.Contains(strings.ToLower(list), strings.ToLower(want))
}

func TestCORSMarksActualResponsesAndLeavesOtherCallersAlone(t *testing.T) {
	h := corsServer("https://console.loong.lat")

	w := do(h, http.MethodPost, "https://console.loong.lat")
	if w.Code != http.StatusOK || w.Header().Get("Access-Control-Allow-Origin") != "https://console.loong.lat" {
		t.Errorf("a listed origin: status %d, allow-origin %q", w.Code, w.Header().Get("Access-Control-Allow-Origin"))
	}
	if !containsFold(w.Header().Get("Access-Control-Expose-Headers"), "X-Trace-Id") {
		t.Errorf("the page needs to read X-Trace-Id to report errors: %q", w.Header().Get("Access-Control-Expose-Headers"))
	}
	if w.Header().Get("Vary") != "Origin" {
		t.Errorf("answers depend on the origin, so Vary: Origin is needed: %q", w.Header().Get("Vary"))
	}

	// An unlisted origin still gets an answer, without the header that would let a page read it.
	if w := do(h, http.MethodPost, "https://evil.example"); w.Code != http.StatusOK || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("an unlisted origin: status %d, allow-origin %q", w.Code, w.Header().Get("Access-Control-Allow-Origin"))
	}
	// Server-to-server callers send no Origin and see no CORS headers at all.
	if w := do(h, http.MethodPost, ""); w.Code != http.StatusOK || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("no origin: status %d, allow-origin %q", w.Code, w.Header().Get("Access-Control-Allow-Origin"))
	}
	// An OPTIONS request that is not a preflight goes on to the handler.
	if w := do(h, http.MethodOptions, "https://console.loong.lat"); w.Code != http.StatusOK {
		t.Errorf("a plain OPTIONS is not a preflight, status %d", w.Code)
	}
}

func TestCORSWithNoOriginsIsOff(t *testing.T) {
	h := corsServer()
	w := do(h, http.MethodOptions, "https://console.loong.lat", "Access-Control-Request-Method", "POST")
	if w.Header().Get("Access-Control-Allow-Origin") != "" || w.Header().Get("Vary") != "" {
		t.Errorf("with no origins CORS must add nothing: %v", w.Header())
	}
}
