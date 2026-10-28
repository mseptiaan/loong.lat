package runner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/solve"
)

// fakeModal stands in for the Modal SDK: a "sandbox" answers like the solver does, either as its entrypoint
// (one-shot) or through Exec (warm), by running the real solver in process.
type fakeModal struct {
	createErr error
	// behave is what the "solver" does with its stdin and stdout.
	behave func(in io.Reader, out io.WriteCloser)

	mu         sync.Mutex
	specs      []sandboxSpec
	created    atomic.Int32
	terminated atomic.Int32
	execs      atomic.Int32
	// dieAfter makes a warm sandbox fail its Exec once it has served this many jobs (0 = never).
	dieAfter int32
}

func (f *fakeModal) create(_ context.Context, spec sandboxSpec) (sandbox, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.mu.Lock()
	f.specs = append(f.specs, spec)
	f.mu.Unlock()
	n := f.created.Add(1)
	sb := &fakeSandbox{f: f, name: "sb-" + string(rune('0'+n))}
	if len(spec.Command) > 0 && spec.Command[0] != "sleep" { // one-shot: the command is the solver
		in, out := io.Pipe()
		res, resW := io.Pipe()
		sb.in, sb.out = out, res
		go func() { f.behave(in, resW) }()
	}
	return sb, nil
}

type fakeSandbox struct {
	f      *fakeModal
	name   string
	in     io.WriteCloser
	out    io.Reader
	served atomic.Int32
}

func (s *fakeSandbox) id() string                        { return s.name }
func (s *fakeSandbox) stdin() io.WriteCloser             { return s.in }
func (s *fakeSandbox) stdout() io.Reader                 { return s.out }
func (s *fakeSandbox) wait(context.Context) (int, error) { return 137, nil }
func (s *fakeSandbox) terminate(context.Context)         { s.f.terminated.Add(1) }

func (s *fakeSandbox) exec(_ context.Context, command []string, _ time.Duration) (process, error) {
	s.f.execs.Add(1)
	if command[0] == "true" {
		return &fakeProcess{in: nopCloser{io.Discard}, out: eof{}}, nil
	}
	if limit := s.f.dieAfter; limit > 0 && s.served.Add(1) > limit {
		return nil, errors.New("sandbox terminated")
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go s.f.behave(inR, outW)
	return &fakeProcess{in: inW, out: outR}, nil
}

type fakeProcess struct {
	in  io.WriteCloser
	out io.Reader
}

func (p *fakeProcess) stdin() io.WriteCloser             { return p.in }
func (p *fakeProcess) stdout() io.Reader                 { return p.out }
func (p *fakeProcess) wait(context.Context) (int, error) { return 0, nil }

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

type eof struct{}

func (eof) Read([]byte) (int, error) { return 0, io.EOF }

// solverBehaviour runs the real solver in process.
func solverBehaviour(in io.Reader, out io.WriteCloser) {
	defer out.Close()
	data, _ := io.ReadAll(in)
	j, err := job.Decode(data)
	if err != nil {
		job.WriteEvent(out, job.Event{Type: job.EventError, Error: err.(*job.Error)})
		return
	}
	res, err := solve.Run(context.Background(), j, func(done, total int) {
		job.WriteEvent(out, job.Event{Type: job.EventProgress, Progress: &job.Progress{Done: done, Total: total}})
	})
	if err != nil {
		job.WriteEvent(out, job.Event{Type: job.EventError, Error: err.(*job.Error)})
		return
	}
	job.WriteEvent(out, job.Event{Type: job.EventResult, Result: res})
}

func fakeConfig(pool int) ModalConfig {
	return ModalConfig{App: "t", Volume: "v", SolverVersion: "test", PoolSize: pool}
}

func newFakeRunner(f *fakeModal, pool int) *Modal {
	return newModal(fakeConfig(pool), f, slog.New(slog.DiscardHandler))
}

func TestOneShotSandboxRunsAJobAndStreamsProgress(t *testing.T) {
	f := &fakeModal{behave: solverBehaviour}
	var progress int
	res, err := newFakeRunner(f, 0).Run(context.Background(), sampleJob(), func(job.Progress) { progress++ })
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Candidates) != len(job.Presets) || progress != len(job.Presets) {
		t.Errorf("candidates %d, progress %d, want %d each", len(res.Candidates), progress, len(job.Presets))
	}
	if f.terminated.Load() != 1 {
		t.Errorf("the sandbox must be terminated once, was %d", f.terminated.Load())
	}
	spec := f.specs[0]
	if spec.CPU != 2 || spec.Env["GOMAXPROCS"] != "4" || spec.Command[0] != "/loong/solver-test" {
		t.Errorf("unexpected sandbox spec %+v", spec)
	}
}

func TestWarmPoolReusesSandboxesAndRunsEveryJobAsAnExec(t *testing.T) {
	f := &fakeModal{behave: solverBehaviour}
	m := newFakeRunner(f, 2)
	for range 4 {
		if _, err := m.Run(context.Background(), sampleJob(), nil); err != nil {
			t.Fatal(err)
		}
	}
	if f.created.Load() != 1 {
		t.Errorf("%d sandboxes for four sequential jobs, want one reused sandbox", f.created.Load())
	}
	if f.execs.Load() != 4 {
		t.Errorf("%d execs, want one per job", f.execs.Load())
	}
	if f.terminated.Load() != 0 {
		t.Error("a healthy warm sandbox must stay alive between jobs")
	}
	if spec := f.specs[0]; spec.Command[0] != "sleep" || spec.IdleTimeout != 5*time.Minute {
		t.Errorf("a warm sandbox sleeps until used and ends when idle: %+v", spec)
	}
	m.Close()
	time.Sleep(50 * time.Millisecond)
	if f.terminated.Load() != 1 {
		t.Errorf("Close must end the warm sandbox, terminated = %d", f.terminated.Load())
	}
}

func TestWarmPoolNeverExceedsItsSizeAndQueuesTheRest(t *testing.T) {
	var running, peak atomic.Int32
	f := &fakeModal{behave: func(in io.Reader, out io.WriteCloser) {
		n := running.Add(1)
		defer running.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		io.ReadAll(in)
		time.Sleep(30 * time.Millisecond)
		solverBehaviour(bytesReader(sampleJobBytes()), out)
	}}
	m := newFakeRunner(f, 2)
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			if _, err := m.Run(context.Background(), sampleJob(), nil); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if f.created.Load() > 2 {
		t.Errorf("%d sandboxes created, the pool is limited to 2", f.created.Load())
	}
	if peak.Load() > 2 {
		t.Errorf("%d jobs ran at once, the pool is limited to 2", peak.Load())
	}
}

func TestADeadWarmSandboxIsReplacedAndTheJobRetried(t *testing.T) {
	f := &fakeModal{behave: solverBehaviour, dieAfter: 1}
	m := newFakeRunner(f, 1)
	if _, err := m.Run(context.Background(), sampleJob(), nil); err != nil {
		t.Fatal(err)
	}
	// The sandbox now fails its next Exec (Modal ended it); the runner must discard it and use a fresh one.
	if _, err := m.Run(context.Background(), sampleJob(), nil); err != nil {
		t.Fatalf("a dead warm sandbox must not fail the job: %v", err)
	}
	if f.created.Load() != 2 {
		t.Errorf("%d sandboxes created, want the dead one replaced by one more", f.created.Load())
	}
}

func TestPoolDropsSandboxesThatHaveBeenIdleTooLong(t *testing.T) {
	f := &fakeModal{behave: solverBehaviour}
	m := newFakeRunner(f, 1)
	clock := time.Now()
	m.pool.now = func() time.Time { return clock }
	if _, err := m.Run(context.Background(), sampleJob(), nil); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(10 * time.Minute) // far past the 5 minute idle timeout
	if _, err := m.Run(context.Background(), sampleJob(), nil); err != nil {
		t.Fatal(err)
	}
	if f.created.Load() != 2 {
		t.Errorf("%d sandboxes, an idle one past its timeout must be replaced, not reused", f.created.Load())
	}
}

func TestModalSeparatesInfrastructureFailuresFromRejectedJobs(t *testing.T) {
	for _, pool := range []int{0, 1} {
		down := newFakeRunner(&fakeModal{createErr: errors.New("quota exceeded")}, pool)
		if _, err := down.Run(context.Background(), sampleJob(), nil); !errors.Is(err, ErrUnavailable) {
			t.Errorf("pool %d: a sandbox that cannot start is unavailable, got %v", pool, err)
		}

		crash := newFakeRunner(&fakeModal{behave: func(in io.Reader, out io.WriteCloser) {
			io.ReadAll(in)
			out.Close() // the container died without saying anything
		}}, pool)
		if _, err := crash.Run(context.Background(), sampleJob(), nil); !errors.Is(err, ErrUnavailable) {
			t.Errorf("pool %d: a sandbox that ends silently is unavailable, got %v", pool, err)
		}

		bad := sampleJob()
		bad.Version = 99
		rejecting := newFakeRunner(&fakeModal{behave: solverBehaviour}, pool)
		_, err := rejecting.Run(context.Background(), bad, nil)
		var je *job.Error
		if !errors.As(err, &je) || errors.Is(err, ErrUnavailable) {
			t.Errorf("pool %d: a job the solver refuses is the caller's problem, not an outage; got %v", pool, err)
		}
		if pool > 0 && rejecting.pool.total != 1 {
			t.Errorf("a refused job must leave the healthy sandbox in the pool, total = %d", rejecting.pool.total)
		}
	}
}

func TestModalStopsWhenTheCallerGivesUp(t *testing.T) {
	for _, pool := range []int{0, 1} {
		f := &fakeModal{behave: func(in io.Reader, out io.WriteCloser) { io.ReadAll(in); select {} }}
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		if _, err := newFakeRunner(f, pool).Run(ctx, sampleJob(), nil); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("pool %d: want the context error, got %v", pool, err)
		}
		cancel()
		time.Sleep(50 * time.Millisecond)
		if f.terminated.Load() != 1 {
			t.Errorf("pool %d: the sandbox must be terminated after cancellation, was %d", pool, f.terminated.Load())
		}
	}
}

func TestPrewarmStartsSandboxesBeforeTheFirstJob(t *testing.T) {
	f := &fakeModal{behave: solverBehaviour}
	cfg := fakeConfig(3)
	cfg.Prewarm = 2
	m := newModal(cfg, f, slog.New(slog.DiscardHandler))
	m.prewarm(context.Background())
	deadline := time.Now().Add(2 * time.Second)
	for f.created.Load() < 2 || f.execs.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("created %d, started %d, want two warm sandboxes", f.created.Load(), f.execs.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestModalConfigNeedsASolverSource(t *testing.T) {
	for name, cfg := range map[string]ModalConfig{
		"empty":        {},
		"no solver":    {App: "a"},
		"volume only":  {App: "a", Volume: "v"},
		"version only": {App: "a", SolverVersion: "x"},
	} {
		if _, err := NewModal(context.Background(), cfg, nil); err == nil {
			t.Errorf("%s: must be rejected before any network call", name)
		}
	}
	if (&ModalConfig{Image: "reg/img:1"}).solverPath() != "/solver" {
		t.Error("an image has the solver at /solver")
	}
}
