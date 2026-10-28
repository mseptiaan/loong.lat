package platform

import (
	"net/http"
	"strings"
)

// CORS lets browsers on the listed origins call the API. Every service is reached directly on its own address, with no
// gateway in front to answer for it, so each answers its own preflights.
//
// Origins are matched exactly (scheme, host and port, e.g. "https://console.loong.lat"); there is no wildcard, since
// the point of the list is to say which web apps may talk to this service. An empty list switches CORS off, which
// leaves only same-origin callers. Authentication is a bearer token in a header, never a cookie, so credentials are
// not allowed across origins.
func CORS(origins []string) Middleware {
	allowed := make(map[string]bool, len(origins))
	for _, o := range origins {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o != "" {
			allowed[strings.ToLower(o)] = true
		}
	}
	if len(allowed) == 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Add("Vary", "Origin")
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r) // not a cross-origin browser request
				return
			}
			ok := allowed[strings.ToLower(origin)]
			preflight := r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
			switch {
			case ok && preflight:
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, Idempotency-Key, Traceparent")
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
			case preflight:
				// The browser will refuse the request without these headers; a status says so in the access log too.
				w.WriteHeader(http.StatusForbidden)
			case ok:
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Expose-Headers", "X-Trace-Id, Retry-After")
				next.ServeHTTP(w, r)
			default:
				next.ServeHTTP(w, r) // served, but the browser will not let the page read the answer
			}
		})
	}
}
