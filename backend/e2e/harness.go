//go:build e2e

// Package e2e runs the three services and the optimizer in one process against
// the local Postgres and Redpanda (scripts/dev-up.sh) and walks a plan through its
// whole life: a customer pays for a quote, the back office dispatches it, the
// driver delivers it, and every service ends up agreeing on the outcome.
//
// It wipes the dev databases and the loong.* topics first, so it only runs when
// LOONG_E2E=1:
//
//	scripts/dev-up.sh
//	LOONG_E2E=1 go test -tags e2e -count=1 ./e2e
package e2e

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

// env reads a setting, with a default for the local dev stack.
func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// requireStack skips the test unless it was asked for explicitly, since it wipes the dev data.
func requireStack(t *testing.T) (dbURL func(service string) string, brokers []string) {
	t.Helper()
	if os.Getenv("LOONG_E2E") != "1" {
		t.Skip("set LOONG_E2E=1 (with scripts/dev-up.sh running) to run the end-to-end test; it wipes the dev databases and loong.* topics")
	}
	base := env("TEST_DATABASE_URL", "postgres://console:loong_dev@127.0.0.1:55432/console?sslmode=disable")
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("TEST_DATABASE_URL: %v", err)
	}
	password, _ := u.User.Password()
	dbURL = func(service string) string {
		c := *u
		c.User = url.UserPassword(service, password)
		c.Path = "/" + service
		return c.String()
	}
	return dbURL, strings.Split(env("KAFKA_BROKERS", "localhost:19092"), ",")
}

// resetDatabase empties a service's database: each role owns its own database, so dropping the public schema is enough.
func resetDatabase(ctx context.Context, t *testing.T, dsn string) {
	t.Helper()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect %s: %v", redact(dsn), err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatalf("reset %s: %v", redact(dsn), err)
	}
}

func redact(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return "<dsn>"
	}
	return u.Redacted()
}

// resetTopics deletes every loong.* topic and creates them again as declared in contracts, so no event of an
// earlier run can reach the services.
func resetTopics(ctx context.Context, t *testing.T, brokers []string) {
	t.Helper()
	client, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		t.Fatalf("kafka: %v", err)
	}
	defer client.Close()
	admin := kadm.NewClient(client)

	var existing []string
	topics, err := admin.ListTopics(ctx)
	if err != nil {
		t.Fatalf("list topics: %v", err)
	}
	for name := range topics {
		if strings.HasPrefix(name, "loong.") {
			existing = append(existing, name)
		}
	}
	if len(existing) > 0 {
		if _, err := admin.DeleteTopics(ctx, existing...); err != nil {
			t.Fatalf("delete topics: %v", err)
		}
	}
	// Deletion is asynchronous on the broker; creating until it stops saying "being deleted" is the way to wait.
	for _, spec := range contracts.AllTopics() {
		configs := map[string]*string{}
		for k, v := range spec.Configs() {
			configs[k] = kadm.StringPtr(v)
		}
		deadline := time.Now().Add(20 * time.Second)
		for {
			_, err := admin.CreateTopic(ctx, spec.Partitions, 1, configs, spec.Name)
			if err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("create topic %s: %v", spec.Name, err)
			}
			time.Sleep(300 * time.Millisecond)
		}
	}
}

// deadLetters counts the records waiting in the dead-letter topics: after a clean run there are none.
func deadLetters(ctx context.Context, t *testing.T, brokers []string) map[string]int64 {
	t.Helper()
	client, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		t.Fatalf("kafka: %v", err)
	}
	defer client.Close()
	admin := kadm.NewClient(client)
	var dlq []string
	for _, spec := range contracts.Topics() {
		dlq = append(dlq, contracts.DLQTopic(spec.Name))
	}
	ends, err := admin.ListEndOffsets(ctx, dlq...)
	if err != nil {
		t.Fatalf("dlq offsets: %v", err)
	}
	out := map[string]int64{}
	ends.Each(func(o kadm.ListedOffset) {
		if o.Offset > 0 {
			out[o.Topic] += o.Offset
		}
	})
	return out
}

/* ---------------- HTTP RPC ---------------- */

// session is a signed-in caller of one service.
type session struct {
	t     *testing.T
	base  string
	token string
}

// call posts body to an RPC method. The body is encoded with a fixed key order: the idempotency check compares the
// bytes of a request, and a map would otherwise come out in a different order on a replay.
func (s *session) call(method string, body any, out any, headers ...string) error {
	s.t.Helper()
	raw := []byte("{}")
	if body != nil {
		var err error
		if raw, err = json.Marshal(body, json.Deterministic(true)); err != nil {
			s.t.Fatalf("%s: marshal: %v", method, err)
		}
	}
	req, err := http.NewRequest(http.MethodPost, s.base+"/v1/rpc/"+method, bytes.NewReader(raw))
	if err != nil {
		s.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatalf("%s: %v", method, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return &rpcError{method: method, status: resp.StatusCode, body: strings.TrimSpace(string(data))}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			s.t.Fatalf("%s: decode %q: %v", method, truncate(string(data), 300), err)
		}
	}
	return nil
}

// must calls a method that is expected to succeed.
func (s *session) must(method string, body any, out any, headers ...string) {
	s.t.Helper()
	if err := s.call(method, body, out, headers...); err != nil {
		s.t.Fatal(err)
	}
}

type rpcError struct {
	method string
	status int
	body   string
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("%s: http %d: %s", e.method, e.status, truncate(e.body, 400))
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// stream calls a streaming method and returns its NDJSON events as raw messages.
func (s *session) stream(method string, body any) ([]jsontext.Value, error) {
	s.t.Helper()
	raw, _ := json.Marshal(body, json.Deterministic(true))
	req, _ := http.NewRequest(http.MethodPost, s.base+"/v1/rpc/"+method, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, &rpcError{method: method, status: resp.StatusCode, body: string(data)}
	}
	var events []jsontext.Value
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			events = append(events, jsontext.Value(line))
		}
	}
	return events, nil
}

// eventually polls until check reports done, failing with the last reason after the timeout.
func eventually(t *testing.T, what string, timeout time.Duration, check func() (done bool, why string)) {
	t.Helper()
	start := time.Now()
	deadline := start.Add(timeout)
	var why string
	for {
		var done bool
		if done, why = check(); done {
			if waited := time.Since(start); waited > time.Second {
				t.Logf("waited %v for %s", waited.Round(100*time.Millisecond), what)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s: %s", timeout, what, why)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// discardLogger silences the services; LOONG_E2E_LOG=debug|info|warn shows what they say while debugging a failure.
func discardLogger() *slog.Logger {
	if v := os.Getenv("LOONG_E2E_LOG"); v != "" {
		var level slog.Level
		_ = level.UnmarshalText([]byte(v))
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// kafkaState prints the end offset of every topic and the lag of the test's consumer groups: when an event never arrives,
// this says whether it was never produced or never consumed.
func kafkaState(ctx context.Context, t *testing.T, brokers []string, groupPrefix string) {
	t.Helper()
	client, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		t.Logf("kafka state: %v", err)
		return
	}
	defer client.Close()
	admin := kadm.NewClient(client)

	var names []string
	for _, spec := range contracts.AllTopics() {
		names = append(names, spec.Name)
	}
	ends, err := admin.ListEndOffsets(ctx, names...)
	if err != nil {
		t.Logf("end offsets: %v", err)
	} else {
		totals := map[string]int64{}
		ends.Each(func(o kadm.ListedOffset) { totals[o.Topic] += o.Offset })
		for _, name := range names {
			if totals[name] > 0 {
				t.Logf("topic %s holds %d records", name, totals[name])
			}
		}
	}
	groups, err := admin.ListGroups(ctx)
	if err != nil {
		t.Logf("groups: %v", err)
		return
	}
	var ours []string
	for _, g := range groups.Groups() {
		if strings.HasPrefix(g, groupPrefix) {
			ours = append(ours, g)
		}
	}
	lags, err := admin.Lag(ctx, ours...)
	if err != nil {
		t.Logf("lag: %v", err)
		return
	}
	for _, g := range ours {
		l := lags[g]
		t.Logf("group %s: state %s, members %d, total lag %d, err %v", g, l.State, len(l.Members), l.Lag.Total(), l.Error())
	}
}
