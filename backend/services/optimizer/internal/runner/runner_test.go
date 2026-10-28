package runner

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
)

func TestFallbackOnlyCoversInfrastructureFailures(t *testing.T) {
	ok := Func(func(context.Context, *job.Job, func(job.Progress)) (*job.Result, error) {
		return &job.Result{Engine: "backup"}, nil
	})
	down := Func(func(context.Context, *job.Job, func(job.Progress)) (*job.Result, error) {
		return nil, unavailable("no sandbox")
	})
	rejects := Func(func(context.Context, *job.Job, func(job.Progress)) (*job.Result, error) {
		return nil, job.Fail(job.CodeInvalid, "bad job")
	})

	if res, err := (Fallback{Primary: down, Backup: ok}).Run(context.Background(), &job.Job{}, nil); err != nil || res.Engine != "backup" {
		t.Errorf("an unavailable primary must fall back, got %v %v", res, err)
	}
	var je *job.Error
	if _, err := (Fallback{Primary: rejects, Backup: ok}).Run(context.Background(), &job.Job{}, nil); !errors.As(err, &je) {
		t.Errorf("a rejected job must not fall back, got %v", err)
	}
	if _, err := (Fallback{Primary: down}).Run(context.Background(), &job.Job{}, nil); !errors.Is(err, ErrUnavailable) {
		t.Errorf("without a backup the failure stays, got %v", err)
	}
}

func TestLimitedBoundsConcurrency(t *testing.T) {
	var running, peak atomic.Int32
	slow := Func(func(ctx context.Context, _ *job.Job, _ func(job.Progress)) (*job.Result, error) {
		n := running.Add(1)
		defer running.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		return &job.Result{}, nil
	})
	l := Limit(slow, 2)
	done := make(chan struct{})
	for range 6 {
		go func() { l.Run(context.Background(), &job.Job{}, nil); done <- struct{}{} }()
	}
	for range 6 {
		<-done
	}
	if peak.Load() > 2 {
		t.Errorf("%d jobs ran at once, the limit is 2", peak.Load())
	}
}

func TestLimitedGivesUpWhileWaiting(t *testing.T) {
	block := make(chan struct{})
	l := Limit(Func(func(ctx context.Context, _ *job.Job, _ func(job.Progress)) (*job.Result, error) {
		<-block
		return &job.Result{}, nil
	}), 1)
	go l.Run(context.Background(), &job.Job{}, nil)
	time.Sleep(10 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := l.Run(ctx, &job.Job{}, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a waiting job must honor its context, got %v", err)
	}
	close(block)
}

var errorsIs = errors.Is
