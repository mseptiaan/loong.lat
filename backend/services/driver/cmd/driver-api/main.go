// Command driver-api serves the driver's mobile app: sign-in, going online, GPS pings, the tasks dispatch
// assigns and the execution of a route stop by stop. Its settings are the common ones in .env.example.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/driver/app"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "driver-api:", err)
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
	log := platform.NewLogger(os.Stderr, "driver-api", level)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := app.Start(ctx, cfg, log)
	if err != nil {
		return err
	}
	return a.Wait()
}
