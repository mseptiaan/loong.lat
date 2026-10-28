package runner

import (
	"context"
	"os/exec"

	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/solve"
)

// Local solves jobs inside the calling process. It is the development runner
// and the fallback when the sandbox is unreachable; it shares the machine with
// the caller, so production wraps it in Limit.
type Local struct{}

// Run implements Runner.
func (Local) Run(ctx context.Context, j *job.Job, onProgress func(job.Progress)) (*job.Result, error) {
	return solve.Run(ctx, j, func(done, total int) {
		if onProgress != nil {
			onProgress(job.Progress{Done: done, Total: total})
		}
	})
}

// Exec runs the solver binary as a child process: the same stdin/stdout
// contract as the sandbox, without the network. It proves the wire path.
type Exec struct {
	// Path of the solver binary.
	Path string
}

// Run implements Runner.
func (e Exec) Run(ctx context.Context, j *job.Job, onProgress func(job.Progress)) (*job.Result, error) {
	payload, err := j.Encode()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, e.Path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, unavailable("%v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, unavailable("%v", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, unavailable("start %s: %v", e.Path, err)
	}
	go func() {
		defer stdin.Close()
		stdin.Write(payload)
	}()
	res, readErr := job.ReadResult(stdout, onProgress)
	waitErr := cmd.Wait()
	if readErr != nil {
		if _, rejected := readErr.(*job.Error); !rejected && waitErr != nil {
			return nil, unavailable("%s: %v", e.Path, waitErr)
		}
		return nil, readErr
	}
	return res, nil
}
