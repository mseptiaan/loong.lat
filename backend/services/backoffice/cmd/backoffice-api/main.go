// Command backoffice-api serves the admin console: fleet management, the queue of confirmed plans and
// dispatch. See services/backoffice/.env.example for its settings.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/app"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "backoffice-api:", err)
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
	cfg := app.ConfigFromEnv(env)
	if err := env.Err(); err != nil {
		return err
	}
	log := platform.NewLogger(os.Stderr, "backoffice-api", level)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := app.Start(ctx, cfg, log)
	if err != nil {
		return err
	}
	return a.Wait()
}
