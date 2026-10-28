// Command solver runs one optimization job: a job.Job as JSON on stdin, and
// newline-delimited job.Events on stdout (progress..., then a result or an
// error). It is the entrypoint of the sandbox image, and works the same on a
// laptop: `solver < job.json`.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/job"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/solve"
	"github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "-version" {
		fmt.Println(version.Engine)
		return
	}
	os.Exit(run(os.Stdin, os.Stdout))
}

// run returns the process exit code: 0 for a result, 1 for a failed job.
func run(in io.Reader, out io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var mu sync.Mutex // progress callbacks arrive from several goroutines
	emit := func(ev job.Event) {
		mu.Lock()
		defer mu.Unlock()
		if err := job.WriteEvent(out, ev); err != nil {
			fmt.Fprintln(os.Stderr, "solver: write:", err)
		}
	}
	fail := func(err error) int {
		je, ok := err.(*job.Error)
		if !ok {
			je = job.Fail(job.CodeInternal, "%v", err)
		}
		emit(job.Event{Type: job.EventError, Error: je})
		return 1
	}

	data, err := io.ReadAll(io.LimitReader(in, 64<<20))
	if err != nil {
		return fail(job.Fail(job.CodeInvalid, "read job: %v", err))
	}
	j, err := job.Decode(data)
	if err != nil {
		return fail(err)
	}
	res, err := solve.Run(ctx, j, func(done, total int) {
		emit(job.Event{Type: job.EventProgress, Progress: &job.Progress{Done: done, Total: total}})
	})
	if err != nil {
		return fail(err)
	}
	emit(job.Event{Type: job.EventResult, Result: res})
	return 0
}
