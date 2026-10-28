package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/modal-labs/modal-client/go"

	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
)

// ModalConfig says where and how jobs run on Modal.
//
// Measured on Modal: a sandbox that starts for one job takes about 4.5 s from request to output, while a sandbox that is
// already running takes about 1 s for the same job through Exec. So jobs normally run in a small pool of warm
// sandboxes (PoolSize > 0); PoolSize 0 starts a sandbox per job, which is slower but costs nothing while idle.
type ModalConfig struct {
	// App is the Modal app that owns the sandboxes.
	App string
	// Environment is the Modal environment; empty means the profile's default.
	Environment string

	// The solver reaches the sandbox in one of two ways. Volume (the default) mounts a Modal Volume that holds the
	// binary as solver-<SolverVersion>, uploaded with cmd/modalpublish: it needs no registry, and the Volume is private
	// to your Modal workspace. Image instead names a registry image that has the binary at /solver; the solver is the
	// engine's source, so such a registry should be private (RegistrySecret holds its credentials).
	Volume         string
	SolverVersion  string
	Image          string
	RegistrySecret string

	// CPU is the number of physical cores (two vCPUs each) a job may use. A job runs one search per preset, so four
	// presets want about two cores.
	CPU float64
	// MemoryMiB is the memory limit of a sandbox.
	MemoryMiB int
	// Timeout bounds one job inside a sandbox.
	Timeout time.Duration
	// Regions restricts where sandboxes run (closest to the users, for latency); empty means any.
	Regions []string

	// PoolSize is the most warm sandboxes kept; each runs one job at a time. Zero means a sandbox per job.
	PoolSize int
	// IdleTimeout ends a warm sandbox that has had no job for this long, so an idle platform costs nothing.
	IdleTimeout time.Duration
	// Lifetime is the longest a warm sandbox lives before it is replaced.
	Lifetime time.Duration
	// Prewarm starts this many sandboxes at startup, so the first quotes do not wait for a cold start.
	Prewarm int

	// TokenID and TokenSecret authenticate the client. Both empty means the
	// MODAL_TOKEN_ID and MODAL_TOKEN_SECRET environment variables or ~/.modal.toml.
	TokenID, TokenSecret string
}

func (c *ModalConfig) withDefaults() ModalConfig {
	out := *c
	if out.CPU <= 0 {
		out.CPU = 2
	}
	if out.MemoryMiB <= 0 {
		out.MemoryMiB = 1024
	}
	if out.Timeout <= 0 {
		out.Timeout = 90 * time.Second
	}
	if out.IdleTimeout <= 0 {
		out.IdleTimeout = 5 * time.Minute
	}
	if out.Lifetime <= 0 {
		out.Lifetime = time.Hour
	}
	return out
}

// solverPath is where the solver binary is inside a sandbox.
func (c *ModalConfig) solverPath() string {
	if c.Image != "" {
		return "/solver"
	}
	return VolumeMount + "/solver-" + c.SolverVersion
}

func (c *ModalConfig) validate() error {
	switch {
	case c.App == "":
		return errors.New("modal runner needs an app")
	case c.Image == "" && (c.Volume == "" || c.SolverVersion == ""):
		return errors.New("modal runner needs either an image, or a volume and a solver version")
	}
	return nil
}

// VolumeMount is where the solver Volume appears inside a sandbox.
const VolumeMount = "/loong"

// SolverFile is the name of the solver binary of a version inside the Volume.
func SolverFile(version string) string { return "solver-" + version }

// Modal runs jobs in Modal sandboxes: no network, no secrets. The job goes in on stdin and the events come back on
// stdout, exactly as with the solver binary on a laptop.
type Modal struct {
	cfg  ModalConfig
	api  sandboxAPI
	log  *slog.Logger
	pool *pool
}

// NewModal connects to Modal, makes the solver available, and warms the pool.
func NewModal(ctx context.Context, cfg ModalConfig, log *slog.Logger) (*Modal, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	api, err := newSDK(ctx, cfg)
	if err != nil {
		return nil, err
	}
	m := newModal(cfg, api, log)
	m.prewarm(ctx)
	return m, nil
}

func newModal(cfg ModalConfig, api sandboxAPI, log *slog.Logger) *Modal {
	cfg = cfg.withDefaults()
	m := &Modal{cfg: cfg, api: api, log: log}
	if cfg.PoolSize > 0 {
		m.pool = &pool{api: api, spec: m.warmSpec(), max: cfg.PoolSize, idle: cfg.IdleTimeout, now: time.Now, free: make(chan struct{}, 1)}
	}
	return m
}

// prewarm starts the configured number of warm sandboxes in the background. All of them are taken from the pool
// before any is started: one put back early would be handed to the next get, which would reuse it instead of
// creating another, and fewer sandboxes than asked for would be warm.
func (m *Modal) prewarm(ctx context.Context) {
	if m.pool == nil {
		return
	}
	go func() {
		var taken []*pooled
		for range min(m.cfg.Prewarm, m.cfg.PoolSize) {
			s, err := m.pool.get(ctx)
			if err != nil {
				m.log.Warn("could not warm a sandbox", "err", err)
				break
			}
			taken = append(taken, s)
		}
		for _, s := range taken {
			go func() {
				// The first Exec is what actually starts the container; do it now rather than on the first quote.
				if err := s.start(ctx); err != nil {
					m.log.Warn("could not start a warm sandbox", "err", err)
					m.pool.discard(s)
					return
				}
				m.pool.put(s)
			}()
		}
	}()
}

// Close ends every idle warm sandbox and stops handing out new ones. Jobs in flight finish first; their sandboxes
// end when they are put back.
func (m *Modal) Close() {
	if m.pool != nil {
		m.pool.close()
	}
}

func (m *Modal) warmSpec() sandboxSpec {
	return sandboxSpec{
		// A sandbox needs a command; this one only keeps it alive until the Lifetime ends it.
		Command: []string{"sleep", strconv.Itoa(int(m.cfg.Lifetime.Seconds()))},
		CPU:     m.cfg.CPU, MemoryMiB: m.cfg.MemoryMiB, Timeout: m.cfg.Lifetime, IdleTimeout: m.cfg.IdleTimeout,
		Regions: m.cfg.Regions, Env: m.env(),
	}
}

// env sets GOMAXPROCS to the cores asked for, because the container sees the host's CPUs.
func (m *Modal) env() map[string]string {
	return map[string]string{"GOMAXPROCS": strconv.Itoa(int(m.cfg.CPU * 2))}
}

// Run implements Runner.
func (m *Modal) Run(ctx context.Context, j *job.Job, onProgress func(job.Progress)) (*job.Result, error) {
	payload, err := j.Encode()
	if err != nil {
		return nil, err
	}
	if m.pool == nil {
		return m.runAlone(ctx, j, payload, onProgress)
	}
	// A warm sandbox may have been ended by Modal since it was last used; one retry on a fresh one covers that.
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		s, err := m.pool.get(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, unavailable("get sandbox: %v", err)
		}
		started := time.Now()
		res, err := m.runIn(ctx, s, payload, onProgress)
		var rejected *job.Error
		switch {
		case err == nil:
			m.pool.put(s)
			m.log.Info("optimizer job done", "job", j.ID, "sandbox", s.sb.id(), "ms", time.Since(started).Milliseconds(), "engine", res.Engine, "pool", m.cfg.PoolSize)
			return res, nil
		case errors.As(err, &rejected):
			m.pool.put(s) // the solver ran and refused the job; the sandbox is fine
			return nil, err
		case ctx.Err() != nil:
			m.pool.discard(s) // the caller gave up mid-job; do not hand the sandbox to someone else
			return nil, ctx.Err()
		}
		m.pool.discard(s)
		lastErr = err
		m.log.Warn("warm sandbox failed, trying another", "sandbox", s.sb.id(), "err", err)
	}
	return nil, lastErr
}

// runIn runs the job as an Exec in a warm sandbox.
func (m *Modal) runIn(ctx context.Context, s *pooled, payload []byte, onProgress func(job.Progress)) (*job.Result, error) {
	proc, err := s.sb.exec(ctx, []string{m.cfg.solverPath()}, m.cfg.Timeout)
	if err != nil {
		return nil, unavailable("start solver: %v", err)
	}
	s.used = true
	if _, err := proc.stdin().Write(payload); err != nil {
		return nil, unavailable("send job: %v", err)
	}
	if err := proc.stdin().Close(); err != nil {
		return nil, unavailable("send job: %v", err)
	}
	res, readErr := readWithContext(ctx, proc.stdout(), onProgress)
	if readErr != nil {
		var rejected *job.Error
		if ctx.Err() != nil || errors.As(readErr, &rejected) {
			return nil, readErr
		}
		waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		code, waitErr := proc.wait(waitCtx)
		return nil, unavailable("solver ended without a result (exit %d, %v): %v", code, waitErr, readErr)
	}
	return res, nil
}

// runAlone starts a sandbox for this one job; slower, but nothing runs while idle.
func (m *Modal) runAlone(ctx context.Context, j *job.Job, payload []byte, onProgress func(job.Progress)) (*job.Result, error) {
	created := time.Now()
	sb, err := m.api.create(ctx, sandboxSpec{
		Command: []string{m.cfg.solverPath()},
		CPU:     m.cfg.CPU, MemoryMiB: m.cfg.MemoryMiB, Timeout: m.cfg.Timeout, Regions: m.cfg.Regions, Env: m.env(),
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, unavailable("create sandbox: %v", err)
	}
	// The job's own context may already be over when it is time to clean up.
	defer terminate(ctx, sb)

	if _, err := sb.stdin().Write(payload); err != nil {
		return nil, unavailable("send job: %v", err)
	}
	if err := sb.stdin().Close(); err != nil {
		return nil, unavailable("send job: %v", err)
	}
	res, readErr := readWithContext(ctx, sb.stdout(), onProgress)
	if readErr != nil {
		var rejected *job.Error
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.As(readErr, &rejected) {
			return nil, readErr // the solver ran and refused the job
		}
		waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		code, waitErr := sb.wait(waitCtx)
		return nil, unavailable("sandbox %s ended without a result (exit %d, %v): %v", sb.id(), code, waitErr, readErr)
	}
	m.log.Info("optimizer job done", "job", j.ID, "sandbox", sb.id(), "ms", time.Since(created).Milliseconds(), "engine", res.Engine, "pool", 0)
	return res, nil
}

func terminate(ctx context.Context, sb sandbox) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	sb.terminate(cleanup)
}

// readWithContext reads the event stream but gives up when ctx ends; the sandbox
// has no way to interrupt a blocked read on its own.
func readWithContext(ctx context.Context, r io.Reader, onProgress func(job.Progress)) (*job.Result, error) {
	type outcome struct {
		res *job.Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := job.ReadResult(r, onProgress)
		done <- outcome{res, err}
	}()
	select {
	case o := <-done:
		return o.res, o.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

/* ---- the warm pool ---- */

// pooled is a warm sandbox and what the pool knows about it.
type pooled struct {
	sb      sandbox
	used    bool      // a command has run in it, so its container is up
	lastEnd time.Time // when it last finished a job
}

// start runs a no-op so the container is up before the first job needs it.
func (p *pooled) start(ctx context.Context) error {
	proc, err := p.sb.exec(ctx, []string{"true"}, 30*time.Second)
	if err != nil {
		return err
	}
	if _, err := proc.wait(ctx); err != nil {
		return err
	}
	p.used = true
	return nil
}

// pool hands out warm sandboxes, one job at a time each, up to max of them.
type pool struct {
	api  sandboxAPI
	spec sandboxSpec
	max  int
	idle time.Duration // sandboxes unused this long are assumed gone (Modal ends them at the same mark)
	now  func() time.Time

	mu     sync.Mutex
	ready  []*pooled // free sandboxes, most recently used last
	total  int       // sandboxes existing or being created
	closed bool
	free   chan struct{} // woken when a sandbox is put back or a slot opens
}

// get returns a free sandbox, creates one while there is room, or waits for one.
func (p *pool) get(ctx context.Context) (*pooled, error) {
	for {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return nil, errors.New("sandbox pool is closed")
		}
		// Prefer the most recently used: it is the likeliest to still be alive.
		var stale []*pooled
		for len(p.ready) > 0 {
			s := p.ready[len(p.ready)-1]
			p.ready = p.ready[:len(p.ready)-1]
			if p.expired(s) {
				p.total--
				stale = append(stale, s)
				continue
			}
			p.mu.Unlock()
			p.terminateAll(stale)
			return s, nil
		}
		if p.total < p.max {
			p.total++
			p.mu.Unlock()
			p.terminateAll(stale)
			sb, err := p.api.create(ctx, p.spec)
			if err != nil {
				p.mu.Lock()
				p.total--
				p.mu.Unlock()
				p.wake()
				return nil, err
			}
			return &pooled{sb: sb, lastEnd: p.now()}, nil
		}
		p.mu.Unlock()
		p.terminateAll(stale)

		select {
		case <-p.free:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// expired says a sandbox has been idle long enough that Modal has probably ended it.
func (p *pool) expired(s *pooled) bool {
	return s.used && p.now().Sub(s.lastEnd) > p.idle-15*time.Second
}

// put returns a healthy sandbox for the next job.
func (p *pool) put(s *pooled) {
	s.lastEnd = p.now()
	p.mu.Lock()
	if p.closed {
		p.total--
		p.mu.Unlock()
		p.terminateAll([]*pooled{s})
		return
	}
	p.ready = append(p.ready, s)
	p.mu.Unlock()
	p.wake()
}

// discard ends a sandbox that failed and frees its slot.
func (p *pool) discard(s *pooled) {
	p.mu.Lock()
	p.total--
	p.mu.Unlock()
	p.terminateAll([]*pooled{s})
	p.wake()
}

func (p *pool) wake() {
	select {
	case p.free <- struct{}{}:
	default:
	}
}

func (p *pool) close() {
	p.mu.Lock()
	p.closed = true
	ready := p.ready
	p.ready = nil
	p.total -= len(ready)
	p.mu.Unlock()
	p.terminateAll(ready)
}

// terminateAll ends sandboxes in the background; a slow Modal call must not hold up a quote.
func (p *pool) terminateAll(list []*pooled) {
	for _, s := range list {
		go terminate(context.Background(), s.sb)
	}
}

/* ---- the slice of the Modal SDK the runner uses, so that tests can stand in for Modal ---- */

type sandboxSpec struct {
	Command     []string
	CPU         float64
	MemoryMiB   int
	Timeout     time.Duration
	IdleTimeout time.Duration
	Regions     []string
	Env         map[string]string
}

type sandboxAPI interface {
	create(ctx context.Context, spec sandboxSpec) (sandbox, error)
}

type sandbox interface {
	id() string
	stdin() io.WriteCloser
	stdout() io.Reader
	wait(ctx context.Context) (exitCode int, err error)
	terminate(ctx context.Context)
	exec(ctx context.Context, command []string, timeout time.Duration) (process, error)
}

type process interface {
	stdin() io.WriteCloser
	stdout() io.Reader
	wait(ctx context.Context) (exitCode int, err error)
}

type sdk struct {
	client *modal.Client
	app    *modal.App
	image  *modal.Image
	volume *modal.Volume
}

func newSDK(ctx context.Context, cfg ModalConfig) (*sdk, error) {
	client, err := modal.NewClientWithOptions(&modal.ClientParams{TokenID: cfg.TokenID, TokenSecret: cfg.TokenSecret, Environment: cfg.Environment})
	if err != nil {
		return nil, fmt.Errorf("modal client: %w", err)
	}
	app, err := client.Apps.FromName(ctx, cfg.App, &modal.AppFromNameParams{Environment: cfg.Environment, CreateIfMissing: true})
	if err != nil {
		return nil, fmt.Errorf("modal app %q: %w", cfg.App, err)
	}
	s := &sdk{client: client, app: app}

	if cfg.Image != "" {
		var params *modal.ImageFromRegistryParams
		if cfg.RegistrySecret != "" {
			secret, err := client.Secrets.FromName(ctx, cfg.RegistrySecret, &modal.SecretFromNameParams{Environment: cfg.Environment})
			if err != nil {
				return nil, fmt.Errorf("modal secret %q: %w", cfg.RegistrySecret, err)
			}
			params = &modal.ImageFromRegistryParams{Secret: secret}
		}
		s.image = client.Images.FromRegistry(cfg.Image, params)
	} else {
		// The public base image carries no secret; the solver comes from the Volume.
		s.image = client.Images.FromRegistry(BaseImage, nil)
		vol, err := client.Volumes.FromName(ctx, cfg.Volume, &modal.VolumeFromNameParams{Environment: cfg.Environment})
		if err != nil {
			return nil, fmt.Errorf("modal volume %q (publish the solver with cmd/modalpublish): %w", cfg.Volume, err)
		}
		readOnly := true
		s.volume = vol.WithMountOptions(&modal.VolumeMountOptionsParams{ReadOnly: &readOnly})
	}
	// Pull and build the image once, so that creating a sandbox never waits for it.
	built, err := s.image.Build(ctx, app, nil)
	if err != nil {
		return nil, fmt.Errorf("modal image: %w", err)
	}
	s.image = built
	return s, nil
}

// BaseImage is the public image the solver Volume is mounted into; Modal's own documentation runs sandboxes on it.
const BaseImage = "alpine:3.21"

func (s *sdk) create(ctx context.Context, spec sandboxSpec) (sandbox, error) {
	params := &modal.SandboxCreateParams{
		Command:        spec.Command,
		CPU:            spec.CPU,
		CPULimit:       spec.CPU,
		MemoryMiB:      spec.MemoryMiB,
		MemoryLimitMiB: spec.MemoryMiB,
		Timeout:        spec.Timeout,
		IdleTimeout:    spec.IdleTimeout,
		Env:            spec.Env,
		Regions:        spec.Regions,
		BlockNetwork:   true,
	}
	if s.volume != nil {
		params.Volumes = map[string]*modal.Volume{VolumeMount: s.volume}
	}
	sb, err := s.client.Sandboxes.Create(ctx, s.app, s.image, params)
	if err != nil {
		return nil, err
	}
	return &sdkSandbox{sb: sb}, nil
}

type sdkSandbox struct{ sb *modal.Sandbox }

func (s *sdkSandbox) id() string            { return s.sb.SandboxID }
func (s *sdkSandbox) stdin() io.WriteCloser { return s.sb.Stdin }
func (s *sdkSandbox) stdout() io.Reader     { return s.sb.Stdout }
func (s *sdkSandbox) wait(ctx context.Context) (int, error) {
	return s.sb.Wait(ctx, nil)
}
func (s *sdkSandbox) terminate(ctx context.Context) {
	s.sb.Terminate(ctx, nil)
	s.sb.Detach()
}

func (s *sdkSandbox) exec(ctx context.Context, command []string, timeout time.Duration) (process, error) {
	p, err := s.sb.Exec(ctx, command, &modal.SandboxExecParams{Timeout: timeout.Truncate(time.Second)})
	if err != nil {
		return nil, err
	}
	return &sdkProcess{p: p}, nil
}

type sdkProcess struct{ p *modal.ContainerProcess }

func (p *sdkProcess) stdin() io.WriteCloser { return p.p.Stdin }
func (p *sdkProcess) stdout() io.Reader     { return p.p.Stdout }
func (p *sdkProcess) wait(ctx context.Context) (int, error) {
	return p.p.Wait(ctx, nil)
}
