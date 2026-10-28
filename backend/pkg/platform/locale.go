package platform

import (
	"context"
	"net/http"
	"strings"
)

// Locale is the language a client asked for. The API speaks in message keys (contracts.Msg) and lets the screen
// translate them; only text the server composes itself (an AI answer, the default reason of a cancel) needs it.
type Locale string

const (
	LocaleEN Locale = "en"
	LocaleID Locale = "id"
)

type localeKey struct{}

// WithLocale returns a context that carries l.
func WithLocale(ctx context.Context, l Locale) context.Context {
	return context.WithValue(ctx, localeKey{}, l)
}

// LocaleFrom returns the language of the request, English when none was given.
func LocaleFrom(ctx context.Context) Locale {
	if l, ok := ctx.Value(localeKey{}).(Locale); ok {
		return l
	}
	return LocaleEN
}

// ParseLocale picks the language of an Accept-Language header: the first tag that is Indonesian ("id", "in" or a
// regional form such as "id-ID") or English wins; anything else is English.
func ParseLocale(header string) Locale {
	for part := range strings.SplitSeq(header, ",") {
		tag, _, _ := strings.Cut(strings.TrimSpace(part), ";")
		base, _, _ := strings.Cut(strings.ToLower(tag), "-")
		switch base {
		case "id", "in":
			return LocaleID
		case "en":
			return LocaleEN
		}
	}
	return LocaleEN
}

// LocaleMiddleware puts the language of the Accept-Language header into the request context (see LocaleFrom).
func LocaleMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(WithLocale(r.Context(), ParseLocale(r.Header.Get("Accept-Language")))))
	})
}
