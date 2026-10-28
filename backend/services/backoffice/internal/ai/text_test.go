package ai

import (
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

func TestFiguresAreFormattedLikeTheScreensDo(t *testing.T) {
	en, id := newLang(platform.LocaleEN), newLang(platform.LocaleID)
	at := time.Date(2026, time.October, 21, 7, 5, 0, 0, time.UTC) // 14:05 in Jakarta

	tests := []struct {
		name   string
		format func(lang) string
		wantEN string
		wantID string
	}{
		{"rupiah", func(l lang) string { return l.rupiah(1250000) }, "Rp 1,250,000", "Rp 1.250.000"},
		{"small rupiah", func(l lang) string { return l.rupiah(950) }, "Rp 950", "Rp 950"},
		{"zero", func(l lang) string { return l.rupiah(0) }, "Rp 0", "Rp 0"},
		{"km", func(l lang) string { return l.km(3.24) }, "3.2 km", "3,2 km"},
		{"whole km", func(l lang) string { return l.km(2) }, "2 km", "2 km"},
		{"percent", func(l lang) string { return l.pct(87.5) }, "87.5%", "87,5%"},
		{"whole percent", func(l lang) string { return l.pct(50) }, "50%", "50%"},
		{"minutes", func(l lang) string { return l.duration(45) }, "45 min", "45 mnt"},
		{"hour and minutes", func(l lang) string { return l.duration(65) }, "1 h 05 min", "1 j 05 mnt"},
		{"whole hours", func(l lang) string { return l.duration(120) }, "2 h", "2 j"},
		{"clock", func(l lang) string { return l.clock(at) }, "14:05", "14.05"},
		{"date and time", func(l lang) string { return l.dateTime(at) }, "21 Oct 14:05", "21 Okt 14.05"},
		{"date", func(l lang) string { return l.date(at) }, "21 Oct 2026", "21 Okt 2026"},
		{"one plan", func(l lang) string { return l.plans(1) }, "1 plan", "1 plan"},
		{"several plans", func(l lang) string { return l.plans(3) }, "3 plans", "3 plan"},
	}
	for _, tt := range tests {
		if got := tt.format(en); got != tt.wantEN {
			t.Errorf("%s (en) = %q, want %q", tt.name, got, tt.wantEN)
		}
		if got := tt.format(id); got != tt.wantID {
			t.Errorf("%s (id) = %q, want %q", tt.name, got, tt.wantID)
		}
	}
}

func TestIssueTextsFollowTheCatalog(t *testing.T) {
	en, id := newLang(platform.LocaleEN), newLang(platform.LocaleID)
	plain := &contracts.Msg{Key: "dom.issue.alamat_salah"}
	if label, note := en.issue(plain); label != "Address not found" || note != "" {
		t.Errorf("plain issue = %q %q", label, note)
	}
	withNote := &contracts.Msg{Key: "x", Params: map[string]any{"issue": map[string]any{"key": "dom.issue.barang_rusak"}, "note": "kotak penyok"}}
	if label, note := id.issue(withNote); label != "Barang rusak" || note != "kotak penyok" {
		t.Errorf("issue with a note = %q %q", label, note)
	}
	if label, note := en.issue(nil); label != "" || note != "" {
		t.Errorf("no issue = %q %q", label, note)
	}
}
