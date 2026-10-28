package eventbus

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// fakeDB is an in-memory stand-in for the consumer's database: it implements only what the eventbus itself
// does with a transaction (the inbox insert, commit, rollback), so consumer behaviour can be tested without
// Postgres. The real SQL is covered by the TEST_DATABASE_URL tests.
type fakeDB struct {
	mu       sync.Mutex
	inbox    map[string]bool
	effects  []string
	commits  int
	rollback int
}

func newFakeDB() *fakeDB { return &fakeDB{inbox: map[string]bool{}} }

func (d *fakeDB) Begin(context.Context) (pgx.Tx, error) { return &fakeTx{db: d}, nil }

// effectsSnapshot returns the effects handlers committed.
func (d *fakeDB) effectsSnapshot() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.effects...)
}

type execCall struct {
	sql  string
	args []any
}

// fakeTx embeds a nil pgx.Tx: calling anything beyond the methods below panics, which is what a test of the
// eventbus should want.
type fakeTx struct {
	pgx.Tx
	db      *fakeDB
	pending []string
	effects []string
	execs   []execCall
	closed  bool
}

// addEffect lets a test handler stage a side effect that is applied only if the transaction commits.
func (t *fakeTx) addEffect(effect string) { t.effects = append(t.effects, effect) }

func (t *fakeTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	t.execs = append(t.execs, execCall{sql, args})
	if !strings.Contains(sql, "INSERT INTO inbox") {
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	}
	key := args[0].(string) + "|" + args[1].(string)
	t.db.mu.Lock()
	defer t.db.mu.Unlock()
	if t.db.inbox[key] || slices.Contains(t.pending, key) {
		return pgconn.NewCommandTag("INSERT 0 0"), nil
	}
	t.pending = append(t.pending, key)
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (t *fakeTx) Commit(context.Context) error {
	t.db.mu.Lock()
	defer t.db.mu.Unlock()
	for _, key := range t.pending {
		t.db.inbox[key] = true
	}
	t.db.effects = append(t.db.effects, t.effects...)
	t.db.commits++
	t.closed = true
	return nil
}

func (t *fakeTx) Rollback(context.Context) error {
	if t.closed {
		return pgx.ErrTxClosed
	}
	t.db.mu.Lock()
	defer t.db.mu.Unlock()
	t.db.rollback++
	t.closed = true
	return nil
}

// eventually polls cond until it holds or the timeout passes.
func eventually(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s", timeout, what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
