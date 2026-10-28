package agent

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strings"
	"testing"
)

func mustCompile(t *testing.T, raw string) *schema {
	t.Helper()
	s, err := compileSchema(jsontext.Value(raw))
	if err != nil {
		t.Fatalf("compile %s: %v", raw, err)
	}
	return s
}

func check(t *testing.T, s *schema, doc string) error {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		t.Fatalf("%s: %v", doc, err)
	}
	return s.validate(v, "")
}

func TestSchemaValidation(t *testing.T) {
	const planArgs = `{
		"type":"object",
		"properties":{
			"id":{"type":"string","pattern":"^PL-[0-9]+$"},
			"limit":{"type":"integer","minimum":1,"maximum":50},
			"status":{"type":"string","enum":["menunggu_driver","berjalan","selesai"]},
			"ids":{"type":"array","items":{"type":"string","minLength":2},"minItems":1,"maxItems":3},
			"include":{"type":"boolean"},
			"filter":{"type":"object","properties":{"driver":{"type":"string"}},"required":["driver"],"additionalProperties":false},
			"when":{"anyOf":[{"type":"string","minLength":4},{"type":"null"}]},
			"score":{"type":"number"},
			"kind":{"const":"plan"},
			"tags":{"type":"object","additionalProperties":{"type":"string"}},
			"free":{}
		},
		"required":["id"],
		"additionalProperties":false
	}`
	s := mustCompile(t, planArgs)
	cases := []struct {
		name string
		doc  string
		want string // "" = valid
	}{
		{"minimal", `{"id":"PL-2041"}`, ""},
		{"everything", `{"id":"PL-1","limit":10,"status":"berjalan","ids":["ab","cd"],"include":true,"filter":{"driver":"drv-1"},"when":"2026-10-21","score":0.5,"kind":"plan","tags":{"a":"b"},"free":[1,{"x":null}]}`, ""},
		{"null where null is allowed", `{"id":"PL-1","when":null}`, ""},
		{"a whole number written as a float is an integer", `{"id":"PL-1","limit":10.0}`, ""},
		{"missing required", `{}`, "id: required property is missing"},
		{"wrong type", `{"id":5}`, "id: expected string, got integer"},
		{"not an object", `[]`, "arguments: expected object, got array"},
		{"null instead of an object", `null`, "arguments: expected object, got null"},
		{"pattern", `{"id":"2041"}`, "id: does not match the pattern"},
		{"fractional integer", `{"id":"PL-1","limit":1.5}`, "limit: expected integer, got number"},
		{"below minimum", `{"id":"PL-1","limit":0}`, "limit: must be at least 1"},
		{"above maximum", `{"id":"PL-1","limit":51}`, "limit: must be at most 50"},
		{"enum", `{"id":"PL-1","status":"dibatalkan"}`, `status: must be one of "menunggu_driver", "berjalan", "selesai"`},
		{"too few items", `{"id":"PL-1","ids":[]}`, "ids: must have at least 1 items"},
		{"too many items", `{"id":"PL-1","ids":["aa","bb","cc","dd"]}`, "ids: must have at most 3 items"},
		{"item type", `{"id":"PL-1","ids":["aa",7]}`, "ids[1]: expected string, got integer"},
		{"item length", `{"id":"PL-1","ids":["a"]}`, "ids[0]: must be at least 2 characters"},
		{"nested required", `{"id":"PL-1","filter":{}}`, "filter.driver: required property is missing"},
		{"nested unknown property", `{"id":"PL-1","filter":{"driver":"d","extra":1}}`, "filter.extra: unknown property"},
		{"unknown property", `{"id":"PL-1","admin":true}`, "admin: unknown property"},
		{"anyOf none match", `{"id":"PL-1","when":5}`, "when: does not match any allowed form"},
		{"anyOf string too short", `{"id":"PL-1","when":"now"}`, "when: does not match any allowed form"},
		{"const", `{"id":"PL-1","kind":"driver"}`, `kind: must be "plan"`},
		{"additionalProperties schema", `{"id":"PL-1","tags":{"a":1}}`, "tags.a: expected string, got integer"},
		{"boolean type", `{"id":"PL-1","include":"yes"}`, "include: expected boolean, got string"},
	}
	for _, tc := range cases {
		err := check(t, s, tc.doc)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: err = %v, want it to contain %q", tc.name, err, tc.want)
		}
	}
}

func TestSchemaLengthCountsCharactersNotBytes(t *testing.T) {
	s := mustCompile(t, `{"type":"object","properties":{"name":{"type":"string","maxLength":4}}}`)
	if err := check(t, s, `{"name":"Kemang"}`); err == nil {
		t.Error("six characters should exceed maxLength 4")
	}
	if err := check(t, s, `{"name":"日本語です"}`); err == nil {
		t.Error("five characters should exceed maxLength 4")
	}
	if err := check(t, s, `{"name":"日本語で"}`); err != nil {
		t.Errorf("four characters of three bytes each fit: %v", err)
	}
}

func TestSchemaTypeLists(t *testing.T) {
	s := mustCompile(t, `{"type":"object","properties":{"v":{"type":["string","null"]}}}`)
	for doc, ok := range map[string]bool{`{"v":"x"}`: true, `{"v":null}`: true, `{"v":1}`: false} {
		if err := check(t, s, doc); (err == nil) != ok {
			t.Errorf("%s: err = %v", doc, err)
		}
	}
}

func TestSchemaFalseDisallowsAProperty(t *testing.T) {
	s := mustCompile(t, `{"type":"object","properties":{"never":false,"anything":true}}`)
	if err := check(t, s, `{"anything":[1]}`); err != nil {
		t.Error(err)
	}
	if err := check(t, s, `{"never":1}`); err == nil || !strings.Contains(err.Error(), "never") {
		t.Errorf("err = %v", err)
	}
}

func TestSchemaFirstViolationIsDeterministic(t *testing.T) {
	s := mustCompile(t, `{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"},"c":{"type":"string"}}}`)
	for range 20 {
		err := check(t, s, `{"c":1,"b":2,"a":3}`)
		if err == nil || !strings.HasPrefix(err.Error(), "a:") {
			t.Fatalf("err = %v, want the violation of a first", err)
		}
	}
}

func TestSchemaCompileRejectsWhatItCannotEnforce(t *testing.T) {
	cases := map[string]string{
		"not json":         `{"type":`,
		"not an object":    `[1]`,
		"a ref":            `{"type":"object","properties":{"a":{"$ref":"#/$defs/x"}}}`,
		"allOf":            `{"type":"object","allOf":[{}]}`,
		"oneOf":            `{"type":"object","properties":{"a":{"oneOf":[{}]}}}`,
		"not":              `{"type":"object","not":{}}`,
		"unknown type":     `{"type":"object","properties":{"a":{"type":"date"}}}`,
		"bad properties":   `{"type":"object","properties":[]}`,
		"bad required":     `{"type":"object","required":"id"}`,
		"bad enum":         `{"type":"object","properties":{"a":{"enum":[]}}}`,
		"bad pattern":      `{"type":"object","properties":{"a":{"pattern":"("}}}`,
		"negative length":  `{"type":"object","properties":{"a":{"minLength":-1}}}`,
		"bad minimum":      `{"type":"object","properties":{"a":{"minimum":"1"}}}`,
		"bad nested":       `{"type":"object","properties":{"a":{"items":"x"}}}`,
		"exclusive bounds": `{"type":"object","properties":{"a":{"exclusiveMinimum":0}}}`,
	}
	for name, raw := range cases {
		if _, err := compileSchema(jsontext.Value(raw)); err == nil {
			t.Errorf("%s: compiled, but it must be refused", name)
		}
	}
	// Annotations are fine.
	if _, err := compileSchema(jsontext.Value(`{"type":"object","title":"x","description":"y","properties":{"a":{"type":"string","description":"d","default":"z","examples":["q"],"format":"uuid"}}}`)); err != nil {
		t.Errorf("annotations should be ignored: %v", err)
	}
}
