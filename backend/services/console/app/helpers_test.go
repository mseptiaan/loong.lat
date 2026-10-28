package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus/eventbustest"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform/platformtest"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/testkit"
)

// TestMain makes password hashing cheap (see the auth tests): registering is part of nearly every test here.
func TestMain(m *testing.M) {
	platform.DefaultArgon2 = platform.Argon2Params{Memory: 1024, Time: 1, Threads: 1, KeyLen: 32, SaltLen: 16}
	os.Exit(m.Run())
}

// service is a running console-api on a fresh schema, a fake Kafka and a fake optimizer.
type service struct {
	t         *testing.T
	base      string
	pool      *pgxpool.Pool // on the service's schema, for looking inside
	brokers   []string
	optimizer *testkit.Optimizer
	cfg       Config
	stop      func() error
}

func startService(t *testing.T, mutate ...func(*Config)) *service {
	t.Helper()
	pool := platformtest.Pool(t) // skips without a database; gives the schema the service will live in
	var schema string
	if err := pool.QueryRow(t.Context(), `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	dsn, err := url.Parse(os.Getenv(platformtest.EnvDatabaseURL))
	if err != nil {
		t.Fatal(err)
	}
	q := dsn.Query()
	q.Set("search_path", schema)
	dsn.RawQuery = q.Encode()

	svc := &service{t: t, pool: pool, optimizer: testkit.NewOptimizer(t)}
	svc.brokers = eventbustest.NewCluster(t,
		contracts.TopicCustomer, contracts.TopicWallet, contracts.TopicPlan, contracts.TopicFleet, contracts.TopicDispatch, contracts.TopicExecution,
		contracts.TopicSettings)
	svc.cfg = Config{
		HTTPAddr: "127.0.0.1:0", DatabaseURL: dsn.String(), KafkaBrokers: svc.brokers, GroupPrefix: "console-test",
		OptimizerURL: svc.optimizer.URL, SimulatePayments: true,
	}
	for _, m := range mutate {
		m(&svc.cfg)
	}

	ctx, cancel := context.WithCancel(t.Context())
	a, err := Start(ctx, svc.cfg, testkit.Log)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	svc.base = "http://" + a.Addr()
	svc.stop = func() error { cancel(); return a.Wait() }
	t.Cleanup(func() {
		if err := svc.stop(); err != nil {
			t.Errorf("console-api stopped with %v", err)
		}
	})
	return svc
}

// reply is one HTTP answer.
type reply struct {
	status int
	header http.Header
	body   []byte
	t      *testing.T
}

func (r reply) into(v any) {
	r.t.Helper()
	if r.status != http.StatusOK {
		r.t.Fatalf("status %d: %s", r.status, r.body)
	}
	if err := json.Unmarshal(r.body, v); err != nil {
		r.t.Fatalf("decode %s: %v", r.body, err)
	}
}

// wire is the body of an error answer.
type wire struct {
	Code   string        `json:"code"`
	Status int           `json:"status"`
	Msg    contracts.Msg `json:"msg"`
	Field  string        `json:"field"`
}

func (r reply) failure() wire {
	r.t.Helper()
	var w wire
	if err := json.Unmarshal(r.body, &w); err != nil {
		r.t.Fatalf("not an error body (status %d): %s", r.status, r.body)
	}
	return w
}

// caller makes requests as one signed-in customer (or nobody, without a token).
type caller struct {
	svc   *service
	token string
}

func (c caller) do(method, path string, body any, headers map[string]string) reply {
	c.svc.t.Helper()
	var rd io.Reader
	if body != nil {
		// Deterministic, so that repeating a request repeats its bytes, as a real client does (the
		// Idempotency-Key check compares them).
		b, err := json.Marshal(body, json.Deterministic(true))
		if err != nil {
			c.svc.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(c.svc.t.Context(), method, c.svc.base+path, rd)
	if err != nil {
		c.svc.t.Fatal(err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.svc.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		c.svc.t.Fatal(err)
	}
	return reply{status: resp.StatusCode, header: resp.Header, body: raw, t: c.svc.t}
}

// rpc calls a method; a body of nil is an empty one.
func (c caller) rpc(method string, body any) reply {
	c.svc.t.Helper()
	if body == nil {
		body = struct{}{}
	}
	return c.do(http.MethodPost, "/v1/rpc/"+method, body, nil)
}

// rpcKeyed calls a money method with its Idempotency-Key.
func (c caller) rpcKeyed(method, key string, body any) reply {
	c.svc.t.Helper()
	return c.do(http.MethodPost, "/v1/rpc/"+method, body, map[string]string{"Idempotency-Key": key})
}

// stream calls a streaming method and returns its NDJSON lines.
func (c caller) stream(method string, body any) (lines []string, r reply) {
	c.svc.t.Helper()
	r = c.rpc(method, body)
	if r.status == http.StatusOK {
		sc := bufio.NewScanner(bytes.NewReader(r.body))
		for sc.Scan() {
			lines = append(lines, sc.Text())
		}
	}
	return lines, r
}

// events opens the SSE stream of this customer.
func (c caller) events() <-chan platform.Event {
	c.svc.t.Helper()
	req, err := http.NewRequest(http.MethodGet, c.svc.base+"/v1/events", nil)
	if err != nil {
		c.svc.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	return testkit.Events(c.svc.t, req)
}

func (s *service) anonymous() caller { return caller{svc: s} }

// register signs a new customer up and returns a caller carrying their token, and their id.
func (s *service) register(email string) (caller, string) {
	s.t.Helper()
	r := s.anonymous().rpc("auth.register", map[string]any{"input": map[string]string{
		"name": "Dina Pramesti", "email": email, "phone": "0812-3456-7801", "password": "demo1234",
	}})
	var signed struct {
		UserID string `json:"userId"`
		Token  string `json:"token"`
	}
	r.into(&signed)
	return caller{svc: s, token: signed.Token}, signed.UserID
}

// publish puts an event on a topic, as another service would.
func (s *service) publish(eventType, key string, version int64, data any) {
	s.t.Helper()
	eventbustest.Produce(s.t, s.brokers, eventbustest.Record(s.t, "other-service", eventType, key, version, data))
}

func (s *service) waitFor(what string, ok func() bool) {
	s.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			s.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// topic reads everything on a topic: the event types, in partition order.
func (s *service) topic(topic string, want int) []*kgo.Record {
	s.t.Helper()
	return eventbustest.ReadTopic(s.t, s.brokers, topic, want)
}

func typeOf(r *kgo.Record) string {
	for _, h := range r.Headers {
		if h.Key == contracts.HeaderType {
			return string(h.Value)
		}
	}
	return ""
}
