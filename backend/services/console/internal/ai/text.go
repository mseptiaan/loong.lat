package ai

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

// This file is the language of the AI features: the text the server composes itself (trace titles, action labels,
// progress labels, summaries, the rendered reasons of an option) in the language of the request. Everything else
// the API says is a message key the screen translates. The catalog below copies the frontend's texts for the
// keys that reach a model or a trace (srv.engine.* labels and reasons, dom.*, cat.*, fmt.*), with the same
// `{name}` / `{name|format}` syntax, `_one`/`_other` plurals and nested messages, so what the Copilot quotes is
// word for word what the screens show.

// wib is Jakarta time. A fixed zone: the runtime image has no tz database.
var wib = time.FixedZone("WIB", 7*3600)

// speech is the language of one request.
type speech struct{ loc platform.Locale }

func newSpeech(l platform.Locale) speech {
	if l != platform.LocaleID {
		l = platform.LocaleEN
	}
	return speech{loc: l}
}

// name is the language's name as a prompt writes it.
func (s speech) name() string {
	if s.loc == platform.LocaleID {
		return "Indonesian (Bahasa Indonesia)"
	}
	return "English"
}

// t renders a catalog key with the parameters given as name, value pairs.
func (s speech) t(key string, pairs ...any) string {
	params := make(map[string]any, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		params[pairs[i].(string)] = pairs[i+1]
	}
	return s.render(key, params)
}

// msg renders a language-neutral message from plan data.
func (s speech) msg(m contracts.Msg) string { return s.render(m.Key, m.Params) }

// render fills a catalog entry. A missing key falls back to English and then to the key itself, as on the screens.
func (s speech) render(key string, params map[string]any) string {
	template, ok := s.lookup(key, params)
	if !ok {
		return key
	}
	return tokenRE.ReplaceAllStringFunc(template, func(whole string) string {
		sub := tokenRE.FindStringSubmatch(whole)
		value, present := params[sub[1]]
		if !present {
			return whole
		}
		if nested, isMsg := asMsg(value); isMsg {
			return s.render(nested.Key, nested.Params)
		}
		if sub[2] != "" {
			return s.format(sub[2], value)
		}
		return plain(value)
	})
}

func (s speech) lookup(key string, params map[string]any) (string, bool) {
	langs := []platform.Locale{s.loc}
	if s.loc != platform.LocaleEN {
		langs = append(langs, platform.LocaleEN)
	}
	count, hasCount := toFloat(params["count"])
	for _, l := range langs {
		c := catalog[l]
		if hasCount {
			if v, ok := c[key+"_"+plural(l, count)]; ok {
				return v, true
			}
			if v, ok := c[key+"_other"]; ok {
				return v, true
			}
		}
		if v, ok := c[key]; ok {
			return v, true
		}
	}
	return "", false
}

// plural is the CLDR category for a count: English has "one", Indonesian only "other".
func plural(l platform.Locale, n float64) string {
	if l == platform.LocaleEN && n == 1 {
		return "one"
	}
	return "other"
}

func asMsg(v any) (contracts.Msg, bool) {
	switch m := v.(type) {
	case contracts.Msg:
		return m, true
	case *contracts.Msg:
		if m != nil {
			return *m, true
		}
	case map[string]any:
		if key, ok := m["key"].(string); ok {
			params, _ := m["params"].(map[string]any)
			return contracts.Msg{Key: key, Params: params}, true
		}
	}
	return contracts.Msg{}, false
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	}
	return 0, false
}

func plain(v any) string {
	if f, ok := v.(float64); ok && f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return fmt.Sprint(v)
}

var tokenRE = regexp.MustCompile(`\{(\w+)(?:\|(\w+))?\}`)

// format applies a named formatter of the frontend ({price|rupiah}) to a parameter.
func (s speech) format(name string, v any) string {
	if name == "time" || name == "date" {
		if t, ok := toTime(v); ok {
			if name == "date" {
				return s.date(t)
			}
			return s.clock(t)
		}
		return plain(v)
	}
	f, ok := toFloat(v)
	if !ok {
		return plain(v)
	}
	switch name {
	case "rupiah":
		return s.rupiah(f)
	case "number":
		return s.integer(math.Round(f))
	case "decimal":
		return s.decimal(f, 1)
	case "decimal2":
		return s.decimal(f, 2)
	case "float":
		return s.float(f)
	case "km":
		return s.km(f)
	case "kg":
		return s.kg(f)
	case "pct":
		return s.integer(math.Round(f)) + "%"
	case "duration":
		return s.duration(f)
	}
	return plain(v)
}

func toTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case contracts.Time:
		return t.Time, true
	case string:
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
			if parsed, err := time.Parse(layout, t); err == nil {
				return parsed, true
			}
		}
	}
	return time.Time{}, false
}

func (s speech) seps() (thousands, decimal string) {
	if s.loc == platform.LocaleID {
		return ".", ","
	}
	return ",", "."
}

// integer is a whole number with the language's digit grouping.
func (s speech) integer(n float64) string {
	thousands, _ := s.seps()
	neg := n < 0
	digits := strconv.FormatInt(int64(math.Abs(n)), 10)
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteString(thousands)
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// decimal is a number with exactly `digits` decimals.
func (s speech) decimal(f float64, digits int) string {
	scale := math.Pow10(digits)
	f = math.Round(f*scale) / scale
	whole, frac, _ := strings.Cut(strconv.FormatFloat(math.Abs(f), 'f', digits, 64), ".")
	n, _ := strconv.ParseFloat(whole, 64)
	_, dec := s.seps()
	out := s.integer(n)
	if digits > 0 {
		out += dec + frac
	}
	if f < 0 {
		out = "-" + out
	}
	return out
}

// float is a number with up to two decimals and no trailing zeros.
func (s speech) float(f float64) string {
	text := s.decimal(f, 2)
	_, dec := s.seps()
	if strings.Contains(text, dec) {
		text = strings.TrimRight(strings.TrimRight(text, "0"), dec)
	}
	return text
}

func (s speech) rupiah(n float64) string { return "Rp " + s.integer(math.Round(n)) }

func (s speech) km(km float64) string { return s.decimal(math.Round(km*10)/10, 1) + " km" }

func (s speech) kg(kg float64) string {
	if kg >= 10 {
		return s.integer(math.Round(kg)) + " kg"
	}
	text := s.decimal(kg, 1)
	_, dec := s.seps()
	text = strings.TrimSuffix(text, dec+"0")
	return text + " kg"
}

func (s speech) duration(min float64) string {
	total := int(math.Max(0, math.Round(min)))
	if total < 60 {
		return s.t("fmt.duration.min", "n", total)
	}
	h, m := total/60, total%60
	if m == 0 {
		return s.t("fmt.duration.hour", "h", h)
	}
	return s.t("fmt.duration.hourMin", "h", h, "m", fmt.Sprintf("%02d", m))
}

// clock is HH:MM in Jakarta time: 14:20 in English, 14.20 in Indonesian.
func (s speech) clock(t time.Time) string {
	text := t.In(wib).Format("15:04")
	if s.loc == platform.LocaleID {
		text = strings.Replace(text, ":", ".", 1)
	}
	return text
}

var months = map[platform.Locale][12]string{
	platform.LocaleEN: {"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"},
	platform.LocaleID: {"Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des"},
}

// date is "21 Oct" / "21 Okt".
func (s speech) date(t time.Time) string {
	t = t.In(wib)
	return fmt.Sprintf("%d %s", t.Day(), months[s.loc][t.Month()-1])
}

// dateTime is "21 Oct, 14:20".
func (s speech) dateTime(t time.Time) string { return s.date(t) + ", " + s.clock(t) }

// fleet is the vehicles of an option or route set as a screen words them: "1 motorbike + 1 blind van".
func (s speech) fleet(t contracts.Totals) string {
	var parts []string
	if t.Motor > 0 {
		parts = append(parts, s.t("srv.engine.fleet.motor", "count", t.Motor))
	}
	if t.Blindvan > 0 {
		parts = append(parts, s.t("srv.engine.fleet.blindvan", "count", t.Blindvan))
	}
	return strings.Join(parts, " + ")
}

// when is a moment as a short text: the clock time on the day of now, the date and time otherwise (Jakarta time);
// empty for a moment that is not set.
func (s speech) when(t contracts.Time, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	if t.In(wib).Format("2006-01-02") == now.In(wib).Format("2006-01-02") {
		return s.clock(t.Time)
	}
	return s.dateTime(t.Time)
}

// known reports whether the catalog can word a message, nested messages included. A message it cannot word (a key
// a newer optimizer added) would render as its own key, which must not reach a model or a reader.
func (s speech) known(m contracts.Msg) bool {
	if _, ok := s.lookup(m.Key, m.Params); !ok {
		return false
	}
	for _, v := range m.Params {
		if nested, isMsg := asMsg(v); isMsg && !s.known(nested) {
			return false
		}
	}
	return true
}

func (s speech) preset(p contracts.Preset) string { return s.t("dom.preset." + string(p)) }

func (s speech) planStatus(p contracts.PlanStatus) string { return s.t("dom.planStatus." + string(p)) }

func (s speech) vehicle(v contracts.VehicleType) string { return s.t("dom.vehicleType." + string(v)) }
