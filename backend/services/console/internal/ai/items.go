package ai

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// catalogItem is one entry of the shipper's quick-add catalog (frontend ITEM_CATALOG): what a typical item of that
// kind weighs and measures. The model maps what it sees or hears to these where it can, and the defaults keep its
// estimates honest.
type catalogItem struct {
	Key      string
	WeightKg float64
	LengthCm float64
	WidthCm  float64
	HeightCm float64
	Fragile  bool
}

var itemCatalog = []catalogItem{
	{"dokumen", 0.3, 32, 24, 2, false},
	{"paket-kecil", 0.8, 30, 20, 10, false},
	{"sepatu", 1.2, 34, 22, 13, false},
	{"kue", 1.5, 30, 30, 15, true},
	{"laptop", 2.8, 42, 30, 10, true},
	{"makanan", 5, 40, 30, 25, false},
	{"kardus-sedang", 4, 40, 30, 30, false},
	{"kardus-besar", 12, 60, 40, 40, false},
	{"galon", 19, 30, 30, 48, false},
	{"dispenser", 7, 35, 35, 100, true},
	{"kursi", 9, 60, 60, 100, false},
	{"rak", 25, 120, 30, 20, false},
	{"kulkas-mini", 22, 50, 50, 85, true},
}

// otherKey is the key of a detected item that is not a catalog item.
const otherKey = "lainnya"

func catalogByKey(key string) (catalogItem, bool) {
	i := slices.IndexFunc(itemCatalog, func(c catalogItem) bool { return c.Key == key })
	if i < 0 {
		return catalogItem{}, false
	}
	return itemCatalog[i], true
}

func catalogKeys() []string {
	keys := make([]string, len(itemCatalog))
	for i, c := range itemCatalog {
		keys[i] = c.Key
	}
	return keys
}

// catalogGuide lists the catalog for a prompt: key, name in the language and typical weight and size.
func (s speech) catalogGuide() string {
	var b strings.Builder
	for _, c := range itemCatalog {
		b.WriteString("- " + c.Key + ": " + s.t("cat."+c.Key) + ", about " + trimFloat(c.WeightKg) + " kg, " +
			trimFloat(c.LengthCm) + "x" + trimFloat(c.WidthCm) + "x" + trimFloat(c.HeightCm) + " cm")
		if c.Fragile {
			b.WriteString(", fragile")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// trimFloat is a number in plain form, without grouping or trailing zeros, for prompts.
func trimFloat(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// Limits on what a detected item may say; the model's numbers are never trusted past them.
const (
	maxDetectedItems = 12
	minWeightKg      = 0.05
	maxWeightKg      = 500
	minDimCm         = 1
	maxDimCm         = 300
	minQty           = 1
	maxQty           = 99
	maxNameRunes     = 60
	maxConfidence    = 0.99
	// LowConfidence is the confidence below which the screens ask the customer to check an item.
	lowConfidence = 0.7
)

// DetectedItem is the frontend's DetectedItem: an item the AI read from a photo or a sentence.
type DetectedItem struct {
	Key        string  `json:"key"`
	Name       string  `json:"name"`
	Qty        int     `json:"qty"`
	WeightKg   float64 `json:"weightKg"`
	LengthCm   float64 `json:"lengthCm"`
	WidthCm    float64 `json:"widthCm"`
	HeightCm   float64 `json:"heightCm"`
	Fragile    bool    `json:"fragile"`
	Confidence float64 `json:"confidence"`
}

// modelItem is an item as the model reports it. Numbers are floats whatever the schema asked for: a model that
// writes 2.0 for a count must not fail the whole answer.
type modelItem struct {
	Name       string   `json:"name"`
	CatalogKey string   `json:"catalog_key"`
	Qty        *float64 `json:"qty"`
	WeightKg   *float64 `json:"weight_kg"`
	LengthCm   *float64 `json:"length_cm"`
	WidthCm    *float64 `json:"width_cm"`
	HeightCm   *float64 `json:"height_cm"`
	Fragile    bool     `json:"fragile"`
	Confidence *float64 `json:"confidence"`
	SourceText string   `json:"source_text"`
}

// itemFields is the schema of one item, shared by what the model sees in a photo and hears in a sentence.
func itemSchemaProperties() string {
	keys := `"` + strings.Join(append(catalogKeys(), otherKey), `","`) + `"`
	return `"name":{"type":"string","description":"Short name of the item in the user's language."},
		"catalog_key":{"type":"string","enum":[` + keys + `],"description":"The catalog item this is, or ` + otherKey + ` when none fits."},
		"qty":{"type":"integer","description":"How many of this item."},
		"weight_kg":{"type":"number","description":"Weight of ONE item in kilograms."},
		"length_cm":{"type":"number"},"width_cm":{"type":"number"},"height_cm":{"type":"number"},
		"fragile":{"type":"boolean"},
		"confidence":{"type":"number","description":"0 to 1: how sure you are of this item and its numbers."}`
}

const itemRequired = `"name","catalog_key","qty","weight_kg","length_cm","width_cm","height_cm","fragile","confidence"`

// clean validates and normalizes one item from the model, or says it cannot be used. Nothing the model says is
// believed beyond sane bounds: counts, weights and sizes are clamped, a catalog item whose numbers are far from
// its kind's typical ones takes the typical ones, and the name of a catalog item is the catalog's own.
func (s speech) clean(m modelItem) (DetectedItem, bool) {
	name := clip(m.Name, maxNameRunes*2)
	cat, isCatalog := catalogByKey(m.CatalogKey)
	if !isCatalog && name == "" {
		return DetectedItem{}, false // nothing to call it by, and no catalog item to name it
	}
	item := DetectedItem{Key: otherKey, Fragile: m.Fragile, Qty: int(clamp(math.Round(value(m.Qty, 1)), minQty, maxQty))}
	weight, length, width, height := value(m.WeightKg, 0), value(m.LengthCm, 0), value(m.WidthCm, 0), value(m.HeightCm, 0)
	if isCatalog {
		item.Key = cat.Key
		item.Name = s.t("cat." + cat.Key)
		item.Fragile = m.Fragile || cat.Fragile
		weight, length, width, height = nearOr(weight, cat.WeightKg), nearOr(length, cat.LengthCm), nearOr(width, cat.WidthCm), nearOr(height, cat.HeightCm)
	} else {
		item.Name = truncateRunes(name, maxNameRunes)
		// Nothing to compare with: an item without usable numbers cannot be priced, so it is dropped.
		if !usable(weight) || !usable(length) || !usable(width) || !usable(height) {
			return DetectedItem{}, false
		}
	}
	item.WeightKg = round(clamp(weight, minWeightKg, maxWeightKg), 2)
	item.LengthCm = round(clamp(length, minDimCm, maxDimCm), 1)
	item.WidthCm = round(clamp(width, minDimCm, maxDimCm), 1)
	item.HeightCm = round(clamp(height, minDimCm, maxDimCm), 1)
	// An unsure model should read as unsure: a missing confidence is a low one.
	item.Confidence = round(clamp(value(m.Confidence, 0.5), 0, maxConfidence), 2)
	return item, true
}

func usable(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 }

// nearOr keeps the model's estimate when it is within a factor of four of the catalog's typical value, else
// takes the typical value.
func nearOr(v, typical float64) float64 {
	if !usable(v) || v < typical/4 || v > typical*4 {
		return typical
	}
	return v
}

func value(p *float64, def float64) float64 {
	if p == nil || math.IsNaN(*p) || math.IsInf(*p, 0) {
		return def
	}
	return *p
}

func clamp(v, lo, hi float64) float64 { return math.Min(math.Max(v, lo), hi) }

func round(v float64, digits int) float64 {
	scale := math.Pow10(digits)
	return math.Round(v*scale) / scale
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:n]))
}

// merge adds an item to a list, folding a second item of the same catalog kind into the first.
func merge(items []DetectedItem, it DetectedItem) (out []DetectedItem, index int) {
	if it.Key != otherKey {
		if i := slices.IndexFunc(items, func(x DetectedItem) bool { return x.Key == it.Key }); i >= 0 {
			items[i].Qty = min(items[i].Qty+it.Qty, maxQty)
			items[i].Confidence = min(items[i].Confidence, it.Confidence)
			return items, i
		}
	}
	return append(items, it), len(items)
}
