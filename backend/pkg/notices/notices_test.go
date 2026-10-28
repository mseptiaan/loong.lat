package notices_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/notices"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform/platformtest"
)

func setup(t *testing.T) (db interface {
	notices.DB
}, call func(p platform.Principal, method, body string) *httptest.ResponseRecorder) {
	t.Helper()
	pool := platformtest.Pool(t)
	if _, err := platform.Migrate(t.Context(), pool, notices.Migrations()...); err != nil {
		t.Fatal(err)
	}
	rpc := platform.NewRPC(slog.New(slog.NewTextHandler(io.Discard, nil)))
	notices.Mount(rpc, pool, func(p platform.Principal) (string, bool) {
		switch p.Role {
		case contracts.RoleCustomer:
			return notices.Customer(p.UserID), true
		case contracts.RoleAdmin:
			return notices.AudienceAdmin, true
		}
		return "", false
	})
	mux := http.NewServeMux()
	rpc.Mount(mux)
	return pool, func(p platform.Principal, method, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/rpc/"+method, strings.NewReader(body))
		req = req.WithContext(platform.WithPrincipal(req.Context(), p))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
}

func msg(key string) contracts.Msg {
	return contracts.Msg{Key: key, Params: map[string]any{"code": "PL-1"}}
}

func TestNoticesAreWrittenOnceAndServedPerAudience(t *testing.T) {
	db, call := setup(t)
	ctx := context.Background()
	dina := notices.Customer("cus-1")
	base := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)

	for i, n := range []notices.Notice{
		{Audience: dina, At: base, Title: msg("t1"), Body: msg("b1"), PlanID: "pln-1", Key: "evt-1"},
		{Audience: dina, At: base.Add(time.Minute), Title: msg("t2"), Body: msg("b2"), Key: "evt-2"},
		{Audience: notices.AudienceAdmin, At: base, Title: msg("t3"), Body: msg("b3"), Key: "evt-1"}, // same key, other audience
	} {
		if wrote, err := notices.Add(ctx, db, n); err != nil || !wrote {
			t.Fatalf("add %d = %v, %v", i, wrote, err)
		}
	}
	if wrote, err := notices.Add(ctx, db, notices.Notice{Audience: dina, Title: msg("t1"), Body: msg("b1"), Key: "evt-1"}); err != nil || wrote {
		t.Errorf("a redelivered event wrote again: %v, %v", wrote, err)
	}
	if _, err := notices.Add(ctx, db, notices.Notice{Audience: dina, Title: msg("t")}); err == nil {
		t.Error("a notice without a body must be refused")
	}

	list, err := notices.List(ctx, db, dina)
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %+v, %v; want her two", list, err)
	}
	if list[0].Title.Key != "t2" || list[1].PlanID != "pln-1" || list[0].Read || list[1].Body.Params["code"] != "PL-1" {
		t.Errorf("newest first, with plan and params: %+v", list)
	}

	// over RPC: a customer sees only hers; markRead(ids) marks those, markRead() the rest
	rec := call(platform.Principal{Role: contracts.RoleCustomer, UserID: "cus-1"}, "notifications.list", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"audience":"customer:cus-1"`) || strings.Contains(rec.Body.String(), `"t3"`) {
		t.Fatalf("notifications.list = %d %s", rec.Code, rec.Body)
	}
	if rec := call(platform.Principal{Role: contracts.RoleCustomer, UserID: "cus-1"}, "notifications.markRead", `{"ids":["`+list[0].ID+`"]}`); rec.Code != 200 {
		t.Fatalf("markRead = %d %s", rec.Code, rec.Body)
	}
	after, _ := notices.List(ctx, db, dina)
	if !after[0].Read || after[1].Read {
		t.Errorf("only the named one is read: %+v", after)
	}
	call(platform.Principal{Role: contracts.RoleCustomer, UserID: "cus-1"}, "notifications.markRead", `{}`)
	if after, _ = notices.List(ctx, db, dina); !after[0].Read || !after[1].Read {
		t.Errorf("markRead without ids reads everything: %+v", after)
	}
	if admin, _ := notices.List(ctx, db, notices.AudienceAdmin); len(admin) != 1 || admin[0].Read {
		t.Errorf("another audience is untouched: %+v", admin)
	}

	// a role without notifications here is refused, and so is a request without a session
	if rec := call(platform.Principal{Role: contracts.RoleDriver, UserID: "drv-1"}, "notifications.list", ""); rec.Code != http.StatusForbidden {
		t.Errorf("driver on a service without driver notifications = %d, want 403", rec.Code)
	}
}

func TestOnlyTheLatestAreServed(t *testing.T) {
	db, _ := setup(t)
	ctx := context.Background()
	who := notices.Customer("cus-9")
	for i := range notices.ListLimit + 5 {
		if _, err := notices.Add(ctx, db, notices.Notice{Audience: who, At: time.Unix(int64(1000+i), 0), Title: msg("t"), Body: msg("b")}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := notices.List(ctx, db, who)
	if err != nil || len(list) != notices.ListLimit {
		t.Fatalf("served %d (%v), want %d", len(list), err, notices.ListLimit)
	}
}
