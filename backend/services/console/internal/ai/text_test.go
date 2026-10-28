package ai

import (
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

// These expectations are what the frontend's formatters (packages/i18n/src/format.ts) print for the same values:
// the Copilot quotes numbers in the very form the screens show them.
func TestFormattersMatchTheFrontend(t *testing.T) {
	en, id := newSpeech(platform.LocaleEN), newSpeech(platform.LocaleID)
	cases := []struct {
		name   string
		got    func(speech) string
		wantEN string
		wantID string
	}{
		{"rupiah", func(s speech) string { return s.rupiah(46500) }, "Rp 46,500", "Rp 46.500"},
		{"a million", func(s speech) string { return s.rupiah(1234567) }, "Rp 1,234,567", "Rp 1.234.567"},
		{"rupiah rounds", func(s speech) string { return s.rupiah(999.5) }, "Rp 1,000", "Rp 1.000"},
		{"zero", func(s speech) string { return s.rupiah(0) }, "Rp 0", "Rp 0"},
		{"km", func(s speech) string { return s.km(12.34) }, "12.3 km", "12,3 km"},
		{"km whole", func(s speech) string { return s.km(8) }, "8.0 km", "8,0 km"},
		{"km over ten", func(s speech) string { return s.km(123.456) }, "123.5 km", "123,5 km"},
		{"minutes", func(s speech) string { return s.duration(52) }, "52 min", "52 mnt"},
		{"hour and minutes", func(s speech) string { return s.duration(65) }, "1 h 05 min", "1 j 05 mnt"},
		{"whole hours", func(s speech) string { return s.duration(120) }, "2 h", "2 j"},
		{"duration rounds", func(s speech) string { return s.duration(59.6) }, "1 h", "1 j"},
		{"negative duration", func(s speech) string { return s.duration(-5) }, "0 min", "0 mnt"},
		{"kg", func(s speech) string { return s.kg(2) }, "2 kg", "2 kg"},
		{"kg decimal", func(s speech) string { return s.kg(2.5) }, "2.5 kg", "2,5 kg"},
		{"kg large", func(s speech) string { return s.kg(19.2) }, "19 kg", "19 kg"},
		{"clock", func(s speech) string { return s.clock(time.Date(2026, 10, 21, 7, 20, 0, 0, time.UTC)) }, "14:20", "14.20"},
		{"clock past midnight in Jakarta", func(s speech) string { return s.clock(time.Date(2026, 10, 21, 18, 5, 0, 0, time.UTC)) }, "01:05", "01.05"},
		{"date", func(s speech) string { return s.dateTime(time.Date(2026, 10, 21, 7, 20, 0, 0, time.UTC)) }, "21 Oct, 14:20", "21 Okt, 14.20"},
		{"float", func(s speech) string { return s.float(0.12) }, "0.12", "0,12"},
		{"float trims", func(s speech) string { return s.float(3.2) }, "3.2", "3,2"},
		{"float whole", func(s speech) string { return s.float(190) }, "190", "190"},
	}
	for _, tc := range cases {
		if got := tc.got(en); got != tc.wantEN {
			t.Errorf("%s (en) = %q, want %q", tc.name, got, tc.wantEN)
		}
		if got := tc.got(id); got != tc.wantID {
			t.Errorf("%s (id) = %q, want %q", tc.name, got, tc.wantID)
		}
	}
}

func TestMessagesRenderLikeTheFrontendCatalog(t *testing.T) {
	en, id := newSpeech(platform.LocaleEN), newSpeech(platform.LocaleID)
	label := msg("srv.engine.label.two", map[string]any{
		"a": msg("dom.preset.termurah", nil),
		"b": map[string]any{"key": "srv.engine.fleet.motor", "params": map[string]any{"count": float64(2)}}, // as decoded from JSON
	})
	if got := en.msg(label); got != "Cheapest · 2 motorbikes" {
		t.Errorf("en label = %q", got)
	}
	if got := id.msg(label); got != "Termurah · 2 motor" {
		t.Errorf("id label = %q", got)
	}
	// plurals: English has one and other, Indonesian only other
	one := msg("srv.engine.reason.fitsMotor", map[string]any{"count": float64(1), "kg": 2.5})
	many := msg("srv.engine.reason.fitsMotor", map[string]any{"count": float64(3), "kg": 12.0})
	if got := en.msg(one); got != "1 item (2.5 kg) fits on a motorbike." {
		t.Errorf("one = %q", got)
	}
	if got := en.msg(many); got != "3 items (12 kg) fit on a motorbike." {
		t.Errorf("many = %q", got)
	}
	if got := id.msg(one); got != "1 barang (2,5 kg) muat di motor." {
		t.Errorf("id one = %q", got)
	}
	// times are ISO instants shown in Jakarta time
	headline := msg("srv.engine.headline", map[string]any{"fleet": "1 motorbike", "count": float64(3), "time": "2026-10-21T08:20:00.000Z"})
	if got := en.msg(headline); got != "1 motorbike · 3 stops · arrives 15:20" {
		t.Errorf("headline = %q", got)
	}
	// an unknown key shows as the key; a missing parameter stays as written
	if got := en.msg(msg("srv.engine.nope", nil)); got != "srv.engine.nope" {
		t.Errorf("unknown key = %q", got)
	}
	if got := en.t("ai.detail.walletShort"); got != "short by: {amount|rupiah}" {
		t.Errorf("missing parameter = %q", got)
	}
	// every key has an Indonesian text, so a customer never reads English by accident
	for key := range catalog[platform.LocaleEN] {
		if _, ok := catalog[platform.LocaleID][key]; ok {
			continue
		}
		// Indonesian has no singular: its plural keys are only _other
		if len(key) > 4 && key[len(key)-4:] == "_one" {
			if _, ok := catalog[platform.LocaleID][key[:len(key)-4]+"_other"]; ok {
				continue
			}
		}
		t.Errorf("%s has no Indonesian text", key)
	}
	for key := range catalog[platform.LocaleID] {
		if _, ok := catalog[platform.LocaleEN][key]; !ok {
			if len(key) > 6 && key[len(key)-6:] == "_other" {
				continue
			}
			t.Errorf("%s has no English text", key)
		}
	}
}
