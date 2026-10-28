// Package runner executes optimization jobs somewhere: in the process (local),
// in a spawned solver binary (exec), or in a Modal sandbox. Every runner speaks
// the same contract, so a quote does not care where its routes were computed.
package runner

import (
	"context"
	"errors"
	"fmt"

	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
)

// Runner runs one job and returns its result. onProgress, which may be nil, is
// called as presets finish. A cancelled ctx stops the job.
type Runner interface {
	Run(ctx context.Context, j *job.Job, onProgress func(job.Progress)) (*job.Result, error)
}

// ErrUnavailable marks failures of the infrastructure rather than of the job
// (sandbox could not start, no network, quota). Only these are worth retrying
// or sending to a fallback runner; a job the solver rejected would fail again.
var ErrUnavailable = errors.New("runner unavailable")

func unavailable(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnavailable, fmt.Sprintf(format, args...))
}

// Fallback runs jobs on Primary and, when Primary is unavailable, on Backup.
type Fallback struct {
	Primary, Backup Runner
}

// Run implements Runner.
func (f Fallback) Run(ctx context.Context, j *job.Job, onProgress func(job.Progress)) (*job.Result, error) {
	res, err := f.Primary.Run(ctx, j, onProgress)
	if err != nil && errors.Is(err, ErrUnavailable) && ctx.Err() == nil && f.Backup != nil {
		return f.Backup.Run(ctx, j, onProgress)
	}
	return res, err
}

// Limited bounds how many jobs run at once; further jobs wait for a slot or for ctx.
type Limited struct {
	Runner
	slots chan struct{}
}

// Limit wraps r so that at most n jobs run concurrently.
func Limit(r Runner, n int) *Limited {
	return &Limited{Runner: r, slots: make(chan struct{}, max(1, n))}
}

// Run implements Runner.
func (l *Limited) Run(ctx context.Context, j *job.Job, onProgress func(job.Progress)) (*job.Result, error) {
	select {
	case l.slots <- struct{}{}:
		defer func() { <-l.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return l.Runner.Run(ctx, j, onProgress)
}

// Func adapts a function to a Runner; tests use it to script a runner.
type Func func(ctx context.Context, j *job.Job, onProgress func(job.Progress)) (*job.Result, error)

// Run implements Runner.
func (f Func) Run(ctx context.Context, j *job.Job, onProgress func(job.Progress)) (*job.Result, error) {
	return f(ctx, j, onProgress)
}
