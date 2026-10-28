package agent

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// schema is a compiled JSON Schema, the subset a tool needs to describe its arguments: type (one or several of
// object, array, string, number, integer, boolean, null), properties, required, additionalProperties (boolean or
// schema), items, enum, const, minimum, maximum, minLength, maxLength, pattern, minItems, maxItems and anyOf.
// Keywords outside the subset that would change what is valid ($ref, allOf, oneOf, not, if, patternProperties, ...)
// make compile fail, so that a schema never silently validates less than it says. Annotations (description,
// title, default, examples, format) are ignored.
type schema struct {
	none   bool // the schema `false`; the schema `true` is the empty schema, which allows anything
	types  []string
	props  map[string]*schema
	req    []string
	extra  *schema // additionalProperties as a schema
	noMore bool    // additionalProperties: false
	items  *schema
	enum   []any
	konst  *any
	min    *float64
	max    *float64
	minLen *int
	maxLen *int
	minN   *int
	maxN   *int
	re     *regexp.Regexp
	anyOf  []*schema
}

var unsupportedKeywords = []string{"$ref", "$defs", "definitions", "allOf", "oneOf", "not", "if", "then", "else", "patternProperties", "dependentSchemas", "dependentRequired", "propertyNames", "prefixItems", "uniqueItems", "exclusiveMinimum", "exclusiveMaximum", "multipleOf"}

// compileSchema parses a tool's argument schema, which must be a JSON object.
func compileSchema(raw jsontext.Value) (*schema, error) {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("not valid JSON: %w", err)
	}
	if _, ok := doc.(map[string]any); !ok {
		return nil, errors.New("must be a JSON object")
	}
	return compile(doc, "schema")
}

func compile(doc any, at string) (*schema, error) {
	switch v := doc.(type) {
	case bool:
		return &schema{none: !v}, nil
	case map[string]any:
		return compileObject(v, at)
	}
	return nil, fmt.Errorf("%s: must be an object or a boolean", at)
}

func compileObject(m map[string]any, at string) (*schema, error) {
	for _, k := range unsupportedKeywords {
		if _, has := m[k]; has {
			return nil, fmt.Errorf("%s: the keyword %q is not supported", at, k)
		}
	}
	s := &schema{}
	if t, has := m["type"]; has {
		switch tv := t.(type) {
		case string:
			s.types = []string{tv}
		case []any:
			for _, e := range tv {
				name, ok := e.(string)
				if !ok {
					return nil, fmt.Errorf("%s.type: must be a string or a list of strings", at)
				}
				s.types = append(s.types, name)
			}
		default:
			return nil, fmt.Errorf("%s.type: must be a string or a list of strings", at)
		}
		for _, name := range s.types {
			if !slices.Contains([]string{"object", "array", "string", "number", "integer", "boolean", "null"}, name) {
				return nil, fmt.Errorf("%s.type: unknown type %q", at, name)
			}
		}
	}
	if p, has := m["properties"]; has {
		pm, ok := p.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s.properties: must be an object", at)
		}
		s.props = make(map[string]*schema, len(pm))
		for name, sub := range pm {
			c, err := compile(sub, at+".properties."+name)
			if err != nil {
				return nil, err
			}
			s.props[name] = c
		}
	}
	if r, has := m["required"]; has {
		list, ok := r.([]any)
		if !ok {
			return nil, fmt.Errorf("%s.required: must be a list of names", at)
		}
		for _, e := range list {
			name, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("%s.required: must be a list of names", at)
			}
			s.req = append(s.req, name)
		}
	}
	if a, has := m["additionalProperties"]; has {
		if b, ok := a.(bool); ok {
			s.noMore = !b
		} else {
			c, err := compile(a, at+".additionalProperties")
			if err != nil {
				return nil, err
			}
			s.extra = c
		}
	}
	if it, has := m["items"]; has {
		c, err := compile(it, at+".items")
		if err != nil {
			return nil, err
		}
		s.items = c
	}
	if e, has := m["enum"]; has {
		list, ok := e.([]any)
		if !ok || len(list) == 0 {
			return nil, fmt.Errorf("%s.enum: must be a non-empty list", at)
		}
		s.enum = list
	}
	if c, has := m["const"]; has {
		s.konst = &c
	}
	var err error
	if s.min, err = number(m, "minimum", at); err != nil {
		return nil, err
	}
	if s.max, err = number(m, "maximum", at); err != nil {
		return nil, err
	}
	for key, dst := range map[string]**int{"minLength": &s.minLen, "maxLength": &s.maxLen, "minItems": &s.minN, "maxItems": &s.maxN} {
		f, err := number(m, key, at)
		if err != nil {
			return nil, err
		}
		if f != nil {
			n := int(*f)
			if *f < 0 || float64(n) != *f {
				return nil, fmt.Errorf("%s.%s: must be a non-negative integer", at, key)
			}
			*dst = &n
		}
	}
	if p, has := m["pattern"]; has {
		str, ok := p.(string)
		if !ok {
			return nil, fmt.Errorf("%s.pattern: must be a string", at)
		}
		if s.re, err = regexp.Compile(str); err != nil {
			return nil, fmt.Errorf("%s.pattern: %w", at, err)
		}
	}
	if a, has := m["anyOf"]; has {
		list, ok := a.([]any)
		if !ok || len(list) == 0 {
			return nil, fmt.Errorf("%s.anyOf: must be a non-empty list", at)
		}
		for i, sub := range list {
			c, err := compile(sub, fmt.Sprintf("%s.anyOf[%d]", at, i))
			if err != nil {
				return nil, err
			}
			s.anyOf = append(s.anyOf, c)
		}
	}
	return s, nil
}

func number(m map[string]any, key, at string) (*float64, error) {
	v, has := m[key]
	if !has {
		return nil, nil
	}
	f, ok := v.(float64)
	if !ok {
		return nil, fmt.Errorf("%s.%s: must be a number", at, key)
	}
	return &f, nil
}

// validate checks a decoded JSON value (as json.Unmarshal into any produces it) and returns the first violation,
// worded for the model that has to correct itself: "limit: expected integer, got string".
func (s *schema) validate(v any, path string) error {
	if s.none {
		return fail(path, "no value is allowed here")
	}
	if len(s.anyOf) > 0 {
		if err := s.matchAny(v, path); err != nil {
			return err
		}
	}
	if len(s.types) > 0 && !slices.ContainsFunc(s.types, func(t string) bool { return isType(v, t) }) {
		return fail(path, "expected "+strings.Join(s.types, " or ")+", got "+typeName(v))
	}
	if len(s.enum) > 0 && !slices.ContainsFunc(s.enum, func(e any) bool { return reflect.DeepEqual(e, v) }) {
		return fail(path, "must be one of "+listValues(s.enum))
	}
	if s.konst != nil && !reflect.DeepEqual(*s.konst, v) {
		return fail(path, "must be "+listValues([]any{*s.konst}))
	}
	switch x := v.(type) {
	case string:
		n := utf8.RuneCountInString(x)
		if s.minLen != nil && n < *s.minLen {
			return fail(path, fmt.Sprintf("must be at least %d characters", *s.minLen))
		}
		if s.maxLen != nil && n > *s.maxLen {
			return fail(path, fmt.Sprintf("must be at most %d characters", *s.maxLen))
		}
		if s.re != nil && !s.re.MatchString(x) {
			return fail(path, "does not match the pattern "+s.re.String())
		}
	case float64:
		if s.min != nil && x < *s.min {
			return fail(path, fmt.Sprintf("must be at least %v", *s.min))
		}
		if s.max != nil && x > *s.max {
			return fail(path, fmt.Sprintf("must be at most %v", *s.max))
		}
	case []any:
		if s.minN != nil && len(x) < *s.minN {
			return fail(path, fmt.Sprintf("must have at least %d items", *s.minN))
		}
		if s.maxN != nil && len(x) > *s.maxN {
			return fail(path, fmt.Sprintf("must have at most %d items", *s.maxN))
		}
		if s.items != nil {
			for i, e := range x {
				if err := s.items.validate(e, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	case map[string]any:
		for _, name := range s.req {
			if _, ok := x[name]; !ok {
				return fail(join(path, name), "required property is missing")
			}
		}
		for _, name := range slices.Sorted(mapKeys(x)) { // sorted: the first violation reported is deterministic
			sub, known := s.props[name]
			switch {
			case known:
				if err := sub.validate(x[name], join(path, name)); err != nil {
					return err
				}
			case s.noMore:
				return fail(join(path, name), "unknown property")
			case s.extra != nil:
				if err := s.extra.validate(x[name], join(path, name)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// matchAny passes when v satisfies at least one of the anyOf alternatives.
func (s *schema) matchAny(v any, path string) error {
	var reasons []string
	for _, sub := range s.anyOf {
		err := sub.validate(v, path)
		if err == nil {
			return nil
		}
		reasons = append(reasons, err.Error())
	}
	return fail(path, "does not match any allowed form ("+strings.Join(reasons, "; ")+")")
}

func mapKeys(m map[string]any) func(yield func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func fail(path, msg string) error {
	if path == "" {
		path = "arguments"
	}
	return errors.New(path + ": " + msg)
}

func isType(v any, t string) bool {
	switch t {
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "null":
		return v == nil
	case "number":
		_, ok := v.(float64)
		return ok
	case "integer":
		f, ok := v.(float64)
		return ok && f == math.Trunc(f) && !math.IsInf(f, 0)
	}
	return false
}

func typeName(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64:
		if x == math.Trunc(x) {
			return "integer"
		}
		return "number"
	}
	return "unknown"
}

func listValues(vals []any) string {
	parts := make([]string, len(vals))
	for i, v := range vals {
		b, err := json.Marshal(v, json.Deterministic(true))
		if err != nil {
			b = []byte("?")
		}
		parts[i] = string(b)
	}
	return strings.Join(parts, ", ")
}
