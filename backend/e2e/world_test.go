//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	backofficeapp "github.com/mseptiaan/loong.lat/backend/services/backoffice/app"
	consoleapp "github.com/mseptiaan/loong.lat/backend/services/console/app"
	driverapp "github.com/mseptiaan/loong.lat/backend/services/driver/app"
	optimizerapp "github.com/mseptiaan/loong.lat/backend/services/optimizer/app"
)

// world is the whole platform running in this process: the optimizer, the three services, and the infrastructure
// they share. Each service can be stopped and started again to see how the others cope.
type world struct {
	t       *testing.T
	ctx     context.Context
	dbURL   func(string) string
	brokers []string
	// group makes the consumer groups of one run its own; each service adds its name, since services must never share a group.
	group string

	optimizerURL  string
	consoleURL    string
	backofficeURL string
	driverURL     string

	stopBackoffice func()
}

const (
	adminEmail    = "admin@loong.test"
	adminPassword = "e2e-admin-password"
)

func newWorld(t *testing.T) *world {
	t.Helper()
	dbURL, brokers := requireStack(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	for _, svc := range []string{"console", "backoffice", "driver"} {
		resetDatabase(ctx, t, dbURL(svc))
	}
	resetTopics(ctx, t, brokers)

	w := &world{t: t, ctx: ctx, dbURL: dbURL, brokers: brokers, group: fmt.Sprintf("e2e-%d", time.Now().UnixNano())}
	log := discardLogger()

	opt, err := optimizerapp.Start(ctx, optimizerapp.Config{HTTPAddr: "127.0.0.1:0", Dev: true, Runner: "local", Timeout: 30 * time.Second}, log)
	if err != nil {
		t.Fatalf("optimizer: %v", err)
	}
	w.optimizerURL = "http://" + opt.Addr()

	drv, err := driverapp.Start(ctx, driverapp.Config{
		HTTPAddr: "127.0.0.1:0", DatabaseURL: dbURL("driver"), KafkaBrokers: brokers, GroupPrefix: w.group + "-driver",
	}, log)
	if err != nil {
		t.Fatalf("driver-api: %v", err)
	}
	w.driverURL = "http://" + drv.Addr()

	con, err := consoleapp.Start(ctx, consoleapp.Config{
		HTTPAddr: "127.0.0.1:0", DatabaseURL: dbURL("console"), KafkaBrokers: brokers, GroupPrefix: w.group + "-console",
		OptimizerURL: w.optimizerURL, SimulatePayments: true,
	}, log)
	if err != nil {
		t.Fatalf("console-api: %v", err)
	}
	w.consoleURL = "http://" + con.Addr()

	w.startBackoffice()
	return w
}

// startBackoffice brings the back office up on a fixed address, so its clients keep working across a restart.
func (w *world) startBackoffice() {
	w.t.Helper()
	addr := "127.0.0.1:0"
	if w.backofficeURL != "" {
		addr = w.backofficeURL[len("http://"):]
	}
	ctx, cancel := context.WithCancel(w.ctx)
	app, err := backofficeapp.Start(ctx, backofficeapp.Config{
		HTTPAddr: addr, DatabaseURL: w.dbURL("backoffice"), KafkaBrokers: w.brokers, GroupPrefix: w.group + "-backoffice",
		AdminEmail: adminEmail, AdminPassword: adminPassword, AdminName: "Admin", DispatchWorker: true, SweepInterval: time.Second,
	}, discardLogger())
	if err != nil {
		cancel()
		w.t.Fatalf("backoffice-api: %v", err)
	}
	w.backofficeURL = "http://" + app.Addr()
	w.stopBackoffice = func() {
		cancel()
		if err := app.Wait(); err != nil {
			w.t.Errorf("backoffice-api stopped with: %v", err)
		}
	}
}

// dump prints the rows of the back office tables that dispatch decides from, for a failing test to explain itself.
func (w *world) dump() {
	w.t.Helper()
	kafkaState(w.ctx, w.t, w.brokers, w.group)
	conn, err := pgx.Connect(w.ctx, w.dbURL("backoffice"))
	if err != nil {
		w.t.Logf("dump: %v", err)
		return
	}
	defer conn.Close(w.ctx)
	for _, table := range []string{"settings", "vehicles", "drivers", "routes"} {
		rows, err := conn.Query(w.ctx, fmt.Sprintf(`SELECT to_jsonb(t)::text FROM %s t`, table))
		if err != nil {
			w.t.Logf("dump %s: %v", table, err)
			continue
		}
		for rows.Next() {
			var row string
			_ = rows.Scan(&row)
			w.t.Logf("%s: %s", table, truncate(row, 600))
		}
		rows.Close()
	}
}
