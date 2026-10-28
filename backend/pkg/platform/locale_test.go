package platform

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseLocale(t *testing.T) {
	for header, want := range map[string]Locale{
		"":                        LocaleEN,
		"en":                      LocaleEN,
		"id":                      LocaleID,
		"id-ID,id;q=0.9,en;q=0.8": LocaleID,
		"en-US,en;q=0.9,id;q=0.5": LocaleEN,
		"in":                      LocaleID,
		"fr-FR, id;q=0.4":         LocaleID,
		"fr":                      LocaleEN,
		"  ID  ":                  LocaleID,
	} {
		if got := ParseLocale(header); got != want {
			t.Errorf("ParseLocale(%q) = %q, want %q", header, got, want)
		}
	}
}

func TestLocaleMiddlewareFeedsTheContext(t *testing.T) {
	var seen Locale
	h := LocaleMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = LocaleFrom(r.Context()) }))
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Accept-Language", "id-ID")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if seen != LocaleID {
		t.Errorf("locale = %q, want id", seen)
	}
	if got := LocaleFrom(t.Context()); got != LocaleEN {
		t.Errorf("a context without a locale is %q, want en", got)
	}
}
