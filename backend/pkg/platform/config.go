// Package platform is the plumbing every service shares: configuration, logging, HTTP serving, the RPC and
// SSE transports, authentication primitives, database helpers and migrations. It knows nothing about any
// service's domain.
package platform

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Env reads typed settings from environment variables. Problems are collected rather than returned one by one,
// so a misconfigured service reports all of them at once. An empty variable counts as unset.
type Env struct {
	lookup func(string) string
	errs   []error
}

// NewEnv reads the process environment.
func NewEnv() *Env {
	return &Env{lookup: os.Getenv}
}

// NewEnvFrom reads from vars instead of the process environment (tests).
func NewEnvFrom(vars map[string]string) *Env {
	return &Env{lookup: func(key string) string { return vars[key] }}
}

func (e *Env) fail(key, format string, args ...any) {
	e.errs = append(e.errs, fmt.Errorf("%s: %s", key, fmt.Sprintf(format, args...)))
}

// String returns the variable, or def when unset.
func (e *Env) String(key, def string) string {
	if v := e.lookup(key); v != "" {
		return v
	}
	return def
}

// Require returns the variable and records an error when it is unset.
func (e *Env) Require(key string) string {
	v := e.lookup(key)
	if v == "" {
		e.fail(key, "required but not set")
	}
	return v
}

func (e *Env) Int(key string, def int) int {
	v := e.lookup(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		e.fail(key, "%q is not an integer", v)
		return def
	}
	return n
}

func (e *Env) Bool(key string, def bool) bool {
	v := e.lookup(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		e.fail(key, "%q is not a boolean", v)
		return def
	}
	return b
}

// Duration parses Go duration syntax such as "30s" or "15m".
func (e *Env) Duration(key string, def time.Duration) time.Duration {
	v := e.lookup(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		e.fail(key, "%q is not a duration such as 30s", v)
		return def
	}
	return d
}

// List splits a comma-separated variable, trimming blanks; def applies when unset.
func (e *Env) List(key string, def ...string) []string {
	v := e.lookup(key)
	if v == "" {
		return def
	}
	var out []string
	for part := range strings.SplitSeq(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// Err reports every problem found so far, or nil.
func (e *Env) Err() error { return errors.Join(e.errs...) }

// LoadDotEnv sets variables from a KEY=VALUE file for local development. Variables already present in the
// environment win, empty values are skipped, and a missing file is not an error. Never commit the file.
func LoadDotEnv(path string) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		text = strings.TrimPrefix(text, "export ")
		key, value, ok := strings.Cut(text, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return fmt.Errorf("%s:%d: expected KEY=VALUE", path, line)
		}
		value = unquote(strings.TrimSpace(value))
		if _, set := os.LookupEnv(key); set || value == "" {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("%s:%d: %w", path, line, err)
		}
	}
	return scanner.Err()
}

// unquote strips matching quotes, or a trailing " # comment" from an unquoted value.
func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	if before, _, found := strings.Cut(v, " #"); found {
		return strings.TrimSpace(before)
	}
	return v
}
