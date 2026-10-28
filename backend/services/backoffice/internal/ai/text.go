package ai

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/fleet"
)

// This file is the language of the AI features: the text the server composes itself (trace titles, action labels,
// what a tool says about a status) in the language of the request. Everything else the API says is a message key
// the screen translates. The words copy the frontend's catalog (dom.*, fmt.*, the mock's ai.* texts) so that what
// the Copilot quotes is what the screens show, and every figure is formatted the way formatRupiah, formatKm and
// formatDuration there do.

// wib is Jakarta time, the time of the dispatchers. A fixed zone: the runtime image has no tz database.
var wib = time.FixedZone("WIB", 7*3600)

// lang is the language of one request: English, or Indonesian.
type lang struct{ loc platform.Locale }

func newLang(l platform.Locale) lang {
	if l != platform.LocaleID {
		l = platform.LocaleEN
	}
	return lang{loc: l}
}

func (l lang) indonesian() bool { return l.loc == platform.LocaleID }

// pick chooses the text of the request's language.
func (l lang) pick(en, id string) string {
	if l.indonesian() {
		return id
	}
	return en
}

// name is the language's name as a prompt writes it.
func (l lang) name() string {
	return l.pick("English", "Indonesian (Bahasa Indonesia)")
}

// count writes a number with its noun: "1 plan", "3 plans" (Indonesian does not inflect).
func (l lang) count(n int, one, other, id string) string {
	switch {
	case l.indonesian():
		return fmt.Sprintf("%d %s", n, id)
	case n == 1:
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, other)
}

func (l lang) plans(n int) string   { return l.count(n, "plan", "plans", "plan") }
func (l lang) drivers(n int) string { return l.count(n, "driver", "drivers", "driver") }
func (l lang) routes(n int) string  { return l.count(n, "route", "routes", "rute") }

func (l lang) separators() (thousands, decimal string) {
	if l.indonesian() {
		return ".", ","
	}
	return ",", "."
}

// number groups the digits of a whole number: 1.250.000 (id), 1,250,000 (en).
func (l lang) number(n int64) string {
	sep, _ := l.separators()
	digits := strconv.FormatUint(uint64(max(n, -n)), 10)
	var b strings.Builder
	if n < 0 {
		b.WriteByte('-')
	}
	for i, c := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteString(sep)
		}
		b.WriteRune(c)
	}
	return b.String()
}

// decimal writes v with at most digits decimals, trailing zeros dropped (2,0 is written 2).
func (l lang) decimal(v float64, digits int) string {
	_, point := l.separators()
	text := strconv.FormatFloat(v, 'f', digits, 64)
	if strings.Contains(text, ".") {
		text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	}
	whole, frac, _ := strings.Cut(text, ".")
	if n, err := strconv.ParseInt(whole, 10, 64); err == nil && math.Abs(float64(n)) >= 1000 {
		whole = l.number(n)
	}
	if frac == "" {
		return whole
	}
	return whole + point + frac
}

func (l lang) rupiah(n int64) string { return "Rp " + l.number(n) }

func (l lang) km(v float64) string { return l.decimal(math.Round(v*10)/10, 1) + " km" }

func (l lang) pct(v float64) string { return l.decimal(math.Round(v*10)/10, 1) + "%" }

// duration is 52 min, 1 h 05 min, 2 h (id: 52 mnt, 1 j 05 mnt, 2 j).
func (l lang) duration(min float64) string {
	total := int(math.Max(0, math.Round(min)))
	unitMin, unitHour := l.pick("min", "mnt"), l.pick("h", "j")
	if total < 60 {
		return fmt.Sprintf("%d %s", total, unitMin)
	}
	h, m := total/60, total%60
	if m == 0 {
		return fmt.Sprintf("%d %s", h, unitHour)
	}
	return fmt.Sprintf("%d %s %02d %s", h, unitHour, m, unitMin)
}

// clock is the time of day in Jakarta: 14.05 (id), 14:05 (en).
func (l lang) clock(t time.Time) string {
	sep := l.pick(":", ".")
	return t.In(wib).Format("15" + sep + "04")
}

var monthsID = [...]string{"Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des"}

// dateTime is a date with its time: "21 Oct 14:05", "21 Okt 14.05".
func (l lang) dateTime(t time.Time) string {
	t = t.In(wib)
	month := t.Month().String()[:3]
	if l.indonesian() {
		month = monthsID[t.Month()-1]
	}
	return fmt.Sprintf("%d %s %s", t.Day(), month, l.clock(t))
}

// date is a day: "21 Oct 2026", "21 Okt 2026".
func (l lang) date(t time.Time) string {
	t = t.In(wib)
	month := t.Month().String()[:3]
	if l.indonesian() {
		month = monthsID[t.Month()-1]
	}
	return fmt.Sprintf("%d %s %d", t.Day(), month, t.Year())
}

// when is a moment as a short text: the time of day when it is today, the date and time otherwise.
func (l lang) when(t, now time.Time) string {
	switch {
	case t.IsZero():
		return ""
	case sameDay(t, now):
		return l.clock(t)
	}
	return l.dateTime(t)
}

// sameDay reports whether two moments fall on the same day in Jakarta.
func sameDay(a, b time.Time) bool {
	ay, am, ad := a.In(wib).Date()
	by, bm, bd := b.In(wib).Date()
	return ay == by && am == bm && ad == bd
}

// startOfDay is the last midnight of Jakarta time at or before t.
func startOfDay(t time.Time) time.Time {
	y, m, d := t.In(wib).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, wib)
}

func (l lang) planStatus(s contracts.PlanStatus) string {
	switch s {
	case contracts.PlanStatusDraft:
		return "Draft"
	case contracts.PlanStatusOptionsReady:
		return l.pick("Awaiting confirmation", "Menunggu konfirmasi")
	case contracts.PlanStatusAwaitingDriver:
		return l.pick("Finding a driver", "Mencari driver")
	case contracts.PlanStatusAssigned:
		return l.pick("Driver assigned", "Driver ditugaskan")
	case contracts.PlanStatusRunning:
		return l.pick("On the way", "Dalam perjalanan")
	case contracts.PlanStatusDone:
		return l.pick("Done", "Selesai")
	case contracts.PlanStatusCancelled:
		return l.pick("Cancelled", "Dibatalkan")
	}
	return string(s)
}

func (l lang) routeStatus(s contracts.RouteStatus) string {
	switch s {
	case contracts.RouteStatusAwaitingDriver:
		return l.pick("waiting for a driver", "menunggu driver")
	case contracts.RouteStatusAssigned:
		return l.pick("driver assigned, not started", "driver ditugaskan, belum berangkat")
	case contracts.RouteStatusRunning:
		return l.pick("on the way", "dalam perjalanan")
	case contracts.RouteStatusDone:
		return l.pick("done", "selesai")
	}
	return string(s)
}

func (l lang) stopStatus(s contracts.StopStatus) string {
	switch s {
	case contracts.StopStatusWaiting:
		return l.pick("waiting", "menunggu")
	case contracts.StopStatusEnRoute:
		return l.pick("on the way", "menuju")
	case contracts.StopStatusArrived:
		return l.pick("arrived", "tiba")
	case contracts.StopStatusDone:
		return l.pick("done", "selesai")
	case contracts.StopStatusProblem:
		return l.pick("problem", "bermasalah")
	}
	return string(s)
}

func (l lang) stopKind(k contracts.StopKind) string {
	if k == contracts.StopKindPickup {
		return l.pick("pickup", "ambil")
	}
	return l.pick("drop-off", "antar")
}

func (l lang) vehicle(t contracts.VehicleType) string {
	switch t {
	case contracts.VehicleTypeMotor:
		return "Motor"
	case contracts.VehicleTypeBlindvan:
		return "Blindvan"
	}
	return string(t)
}

func (l lang) vehicleStatus(s contracts.VehicleStatus) string {
	switch s {
	case contracts.VehicleStatusActive:
		return l.pick("active", "aktif")
	case contracts.VehicleStatusService:
		return l.pick("in service (workshop)", "servis")
	case contracts.VehicleStatusInactive:
		return l.pick("inactive", "nonaktif")
	}
	return string(s)
}

func (l lang) preset(p contracts.Preset) string {
	switch p {
	case contracts.PresetCheapest:
		return l.pick("Cheapest", "Termurah")
	case contracts.PresetShortest:
		return l.pick("Shortest", "Terpendek")
	case contracts.PresetFastest:
		return l.pick("Fastest", "Tercepat")
	case contracts.PresetBalanced:
		return l.pick("Balanced", "Seimbang")
	}
	return string(p)
}

func (l lang) driverStatus(s fleet.Status) string {
	switch s {
	case fleet.StatusOnline:
		return l.pick("online and idle", "online tanpa tugas")
	case fleet.StatusOnDuty:
		return l.pick("on duty", "bertugas")
	}
	return "offline"
}

// issue is the reason a driver gave for a problem stop: the catalog's label of the kind (dom.issue.*), with the
// driver's own note when there is one. The note is text a person wrote.
func (l lang) issue(m *contracts.Msg) (label, note string) {
	if m == nil {
		return "", ""
	}
	if inner, ok := m.Params["issue"]; ok {
		if nested, isMsg := inner.(contracts.Msg); isMsg {
			label = l.issueKind(nested.Key)
		} else if fields, isMap := inner.(map[string]any); isMap {
			key, _ := fields["key"].(string)
			label = l.issueKind(key)
		}
		note, _ = m.Params["note"].(string)
		return label, note
	}
	return l.issueKind(m.Key), ""
}

func (l lang) issueKind(key string) string {
	switch contracts.IssueKind(strings.TrimPrefix(key, "dom.issue.")) {
	case contracts.IssueKindNobodyThere:
		return l.pick("Nobody at the location", "Tidak ada orang di lokasi")
	case contracts.IssueKindDamagedGoods:
		return l.pick("Damaged item", "Barang rusak")
	case contracts.IssueKindWrongAddress:
		return l.pick("Address not found", "Alamat tidak ditemukan")
	case contracts.IssueKindRecipientRefused:
		return l.pick("Recipient refused the item", "Penerima menolak barang")
	case contracts.IssueKindOther:
		return l.pick("Other problem", "Masalah lain")
	}
	return ""
}

// quantity is a figure with the text a screen would show for it: the model quotes the text and never formats or
// computes a number itself.
type quantity struct {
	Value float64 `json:"value"`
	Text  string  `json:"text"`
}

func (l lang) rp(n int64) quantity { return quantity{float64(n), l.rupiah(n)} }
func (l lang) kmq(v float64) quantity {
	return quantity{math.Round(v*10) / 10, l.km(v)}
}
func (l lang) mins(v float64) quantity { return quantity{math.Round(v), l.duration(v)} }
func (l lang) pctq(v float64) quantity { return quantity{math.Round(v*10) / 10, l.pct(v)} }
