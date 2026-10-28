// Command modalpublish uploads the solver binary to the Modal Volume the optimizer mounts, and checks that a fresh
// sandbox can run it. Run it whenever the engine changes; the version it prints goes into MODAL_SOLVER_VERSION.
//
//	scripts/publish-solver.sh            # builds the linux/amd64 binary and calls this
//
// Credentials come from MODAL_TOKEN_ID / MODAL_TOKEN_SECRET (or backend/.env, or ~/.modal.toml).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/app"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/runner"
)

func main() {
	bin := flag.String("bin", "", "path of the linux/amd64 solver binary")
	version := flag.String("version", "", "engine version the binary was built with (its -version output)")
	flag.Parse()
	if *bin == "" || *version == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*bin, *version); err != nil {
		fmt.Fprintln(os.Stderr, "modalpublish:", err)
		os.Exit(1)
	}
}

func run(binPath, version string) error {
	if err := platform.LoadDotEnv(".env"); err != nil {
		return err
	}
	env := platform.NewEnv()
	cfg := app.ModalFromEnv(env)
	if err := env.Err(); err != nil {
		return err
	}
	data, err := os.ReadFile(binPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := runner.Publish(ctx, cfg, data, version, func(format string, args ...any) { fmt.Printf(format+"\n", args...) }); err != nil {
		return err
	}
	fmt.Printf("\nset MODAL_VOLUME=%s and MODAL_SOLVER_VERSION=%s\n", cfg.Volume, version)
	return nil
}
