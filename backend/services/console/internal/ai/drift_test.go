package ai

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

// The catalog copies texts of the frontend, word for word, so that what the Copilot quotes is what the screens
// show. This test keeps the copy from drifting: every key that the frontend also has must read the same. It reads
// the frontend's source when the repository has it and is skipped otherwise.
var tsEntry = regexp.MustCompile(`^\s*'([A-Za-z0-9_.\-]+)':\s*(?:'((?:[^'\\]|\\.)*)'|"((?:[^"\\]|\\.)*)"),?\s*(?://.*)?$`)

func readCatalog(t *testing.T, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("the frontend is not here (%v)", err)
	}
	out := map[string]string{}
	unescape := strings.NewReplacer(`\'`, `'`, `\"`, `"`, `\\`, `\`, `\n`, "\n")
	for line := range strings.SplitSeq(string(raw), "\n") {
		m := tsEntry.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		text := m[2]
		if m[3] != "" {
			text = m[3]
		}
		out[m[1]] = unescape.Replace(text)
	}
	return out
}

func TestTextsMatchTheFrontendCatalog(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "..", "frontend", "packages")
	files := map[platform.Locale][]string{
		platform.LocaleEN: {
			"i18n/src/messages/en/srv/engine.ts", "i18n/src/messages/en/domain.ts", "i18n/src/messages/en/format.ts",
			"mock-api/src/i18n/en/vision.ts", "mock-api/src/i18n/en/intent.ts", "mock-api/src/i18n/en/copilot.ts",
		},
		platform.LocaleID: {
			"i18n/src/messages/id/srv/engine.ts", "i18n/src/messages/id/domain.ts", "i18n/src/messages/id/format.ts",
			"mock-api/src/i18n/id/vision.ts", "mock-api/src/i18n/id/intent.ts", "mock-api/src/i18n/id/copilot.ts",
		},
	}
	for locale, paths := range files {
		frontend := map[string]string{}
		for _, p := range paths {
			for k, v := range readCatalog(t, filepath.Join(root, p)) {
				frontend[k] = v
			}
		}
		if len(frontend) < 100 {
			t.Fatalf("%s: only %d frontend texts were read; the parser no longer fits the files", locale, len(frontend))
		}
		matched := 0
		for key, text := range catalog[locale] {
			want, ok := frontend[key]
			if !ok {
				continue // ours: a trace title or detail that only the server composes
			}
			matched++
			if text != want {
				t.Errorf("%s %s:\n  here     %q\n  frontend %q", locale, key, text, want)
			}
		}
		if matched < 80 {
			t.Errorf("%s: only %d keys were compared", locale, matched)
		}
	}
}
