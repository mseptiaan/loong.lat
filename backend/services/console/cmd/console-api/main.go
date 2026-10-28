// Command console-api serves the shipper dashboard: sign-in, wallet, drafting and paying for plans, and
// following them as drivers run them. See app.Config for its settings, which come from the environment.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/console/app"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "console-api:", err)
		os.Exit(1)
	}
}

func run() error {
	// A .env next to the service (gitignored) feeds local development; real variables win.
	if err := platform.LoadDotEnv(".env"); err != nil {
		return err
	}
	env := platform.NewEnv()
	level, err := platform.ParseLevel(env.String("LOG_LEVEL", ""))
	if err != nil {
		return err
	}
	log := platform.NewLogger(os.Stderr, "console-api", level)
	cfg := app.ConfigFromEnv(env)
	if err := env.Err(); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := app.Start(ctx, cfg, log)
	if err != nil {
		return err
	}
	log.Info("console-api started", "addr", a.Addr())
	return a.Wait()
}
