package runner

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/modal-labs/modal-client/go"
)

// Publish puts a solver binary (linux/amd64, static) into the Modal Volume named by cfg, as solver-<version>, and then
// proves it arrived: a fresh sandbox mounts the Volume read-only, as the runner will, and the binary must report
// that version. Publishing the same version twice replaces the file.
//
// The Volume is private to the Modal workspace, so the solver (the engine's source) needs no registry.
func Publish(ctx context.Context, cfg ModalConfig, binary []byte, version string, logf func(format string, args ...any)) error {
	if cfg.Volume == "" || version == "" {
		return fmt.Errorf("publishing needs a volume name and a version")
	}
	client, err := modal.NewClientWithOptions(&modal.ClientParams{TokenID: cfg.TokenID, TokenSecret: cfg.TokenSecret, Environment: cfg.Environment})
	if err != nil {
		return fmt.Errorf("modal client: %w", err)
	}
	app, err := client.Apps.FromName(ctx, cfg.App, &modal.AppFromNameParams{Environment: cfg.Environment, CreateIfMissing: true})
	if err != nil {
		return fmt.Errorf("modal app %q: %w", cfg.App, err)
	}
	image, err := client.Images.FromRegistry(BaseImage, nil).Build(ctx, app, nil)
	if err != nil {
		return fmt.Errorf("modal image: %w", err)
	}
	vol, err := client.Volumes.FromName(ctx, cfg.Volume, &modal.VolumeFromNameParams{Environment: cfg.Environment, CreateIfMissing: true})
	if err != nil {
		return fmt.Errorf("modal volume %q: %w", cfg.Volume, err)
	}

	path := VolumeMount + "/" + SolverFile(version)
	logf("uploading %d bytes to %s:%s", len(binary), cfg.Volume, SolverFile(version))
	started := time.Now()
	up, err := client.Sandboxes.Create(ctx, app, image, &modal.SandboxCreateParams{
		Command: []string{"sleep", "600"}, Timeout: 10 * time.Minute, Volumes: map[string]*modal.Volume{VolumeMount: vol},
	})
	if err != nil {
		return fmt.Errorf("start uploader: %w", err)
	}
	defer func() {
		up.Terminate(context.WithoutCancel(ctx), &modal.SandboxTerminateParams{Wait: true})
		up.Detach()
	}()
	if err := up.Filesystem.WriteBytes(ctx, binary, path, nil); err != nil {
		return fmt.Errorf("upload: %w", err)
	}
	chmod, err := up.Exec(ctx, []string{"chmod", "+x", path}, nil)
	if err != nil {
		return fmt.Errorf("chmod: %w", err)
	}
	if code, err := chmod.Wait(ctx, nil); err != nil || code != 0 {
		return fmt.Errorf("chmod exited %d: %v", code, err)
	}
	logf("uploaded in %v; ending the uploader so the Volume is committed", time.Since(started).Round(time.Millisecond))
	up.Terminate(ctx, &modal.SandboxTerminateParams{Wait: true})

	// Verify as the runner will use it: read-only mount, fresh sandbox, no network.
	readOnly := true
	check, err := client.Sandboxes.Create(ctx, app, image, &modal.SandboxCreateParams{
		Command: []string{path, "-version"}, Timeout: 2 * time.Minute, BlockNetwork: true,
		Volumes: map[string]*modal.Volume{VolumeMount: vol.WithMountOptions(&modal.VolumeMountOptionsParams{ReadOnly: &readOnly})},
	})
	if err != nil {
		return fmt.Errorf("start verification: %w", err)
	}
	defer func() {
		check.Terminate(context.WithoutCancel(ctx), nil)
		check.Detach()
	}()
	out, err := io.ReadAll(check.Stdout)
	if err != nil {
		return fmt.Errorf("verification output: %w", err)
	}
	if got := strings.TrimSpace(string(out)); got != version {
		return fmt.Errorf("the published solver reports version %q, want %q", got, version)
	}
	logf("verified: a fresh sandbox runs %s and it reports version %s", SolverFile(version), version)
	return nil
}
