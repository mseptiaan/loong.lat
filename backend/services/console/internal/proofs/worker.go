package proofs

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/aiapi"
	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/ai"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/notify"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/plans"
)

// Defaults of Options.
const (
	// DefaultPoll is how often an idle worker looks for a due job.
	DefaultPoll = 2 * time.Second
	// DefaultLease is how long a claimed job belongs to its worker. The model's call is cut off at three quarters of
	// it, so a worker that is merely slow is done (or has given up) before another may take the job.
	DefaultLease = 2 * time.Minute
)

// maxAttempts is how many times a job is tried before it is given up; backoff is the wait after each failed try but
// the last.
const maxAttempts = 4

var backoff = [maxAttempts - 1]time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute}

// bookkeepingTimeout bounds the statement that records how an attempt ended. It runs even when the worker is being
// stopped, so that a shutdown does not leave the job looking like a crashed one.
const bookkeepingTimeout = 10 * time.Second

// maxErrorChars is how much of an error is kept in a job's last_error.
const maxErrorChars = 300

// Job states (the proof_jobs.state column).
const (
	statePending = "pending"
	stateDone    = "done"
	stateSkipped = "skipped"
	stateFailed  = "failed"
)

// Analyzer reads a photo; *ai.Service is the real one.
type Analyzer interface {
	AnalyzeProof(ctx context.Context, in ai.ProofInput) (contracts.ProofAnalysis, error)
}

// Options wires a Worker.
type Options struct {
	Pool     *pgxpool.Pool
	Analyzer Analyzer
	// Flags reports the admin's switches as the settings projection serves them (as ai.Options.Flags). The vision
	// switch decides whether a photo is read at all.
	Flags func(ctx context.Context) (contracts.AIFeatures, error)
	Log   *slog.Logger
	// Poll is how often an idle worker looks for a due job; zero takes DefaultPoll.
	Poll time.Duration
	// Lease is how long a claimed job belongs to its worker; zero takes DefaultLease.
	Lease time.Duration
}

// Worker works the proof jobs off, one at a time.
type Worker struct {
	pool     *pgxpool.Pool
	analyzer Analyzer
	flags    func(context.Context) (contracts.AIFeatures, error)
	log      *slog.Logger
	poll     time.Duration
	lease    time.Duration
}

// New returns a Worker. Pool, Analyzer and Flags are required.
func New(o Options) *Worker {
	w := &Worker{pool: o.Pool, analyzer: o.Analyzer, flags: o.Flags, log: o.Log, poll: o.Poll, lease: o.Lease}
	if w.log == nil {
		w.log = slog.New(slog.DiscardHandler)
	}
	if w.poll <= 0 {
		w.poll = DefaultPoll
	}
	if w.lease <= 0 {
		w.lease = DefaultLease
	}
	return w
}

// Run works jobs off until ctx is done, resting for the poll interval when there is nothing due. It never fails the
// service: a database that cannot be reached is logged and tried again, and every job's own failure is recorded on
// the job.
func (w *Worker) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		worked, err := w.Step(ctx)
		if err != nil && ctx.Err() == nil {
			w.log.ErrorContext(ctx, "proof analysis could not claim a job", "error", err.Error())
		}
		if worked {
			continue // there may be more due; look again at once
		}
		select {
		case <-ctx.Done():
		case <-time.After(w.poll):
		}
	}
	return nil
}

// job is a claimed proof job.
type job struct {
	planID, routeID, stopID string
	// attempts counts this one: the claim has already added it.
	attempts int
	// photoBytes is the size of the photo, for the log.
	photoBytes int
}

// Step claims the next due job and works it to an end, which is not always an analysis (see the package comment). It
// reports whether there was a job. An error is a failure to claim; a failure of the job itself is recorded on it.
func (w *Worker) Step(ctx context.Context) (bool, error) {
	j, ok, err := w.claim(ctx)
	if err != nil || !ok {
		return false, err
	}
	started := time.Now()
	cause := w.attempt(ctx, &j)
	result := w.finish(ctx, j, cause)

	attrs := []any{"planId", j.planID, "stopId", j.stopID, "attempt", j.attempts, "result", result, "photoBytes", j.photoBytes,
		"latencyMs", time.Since(started).Milliseconds()}
	switch result {
	case statePending, stateFailed:
		w.log.WarnContext(ctx, "proof analysis", append(attrs, "error", scrub(cause))...)
	case stateSkipped:
		w.log.InfoContext(ctx, "proof analysis", append(attrs, "reason", scrub(cause))...)
	default:
		w.log.InfoContext(ctx, "proof analysis", attrs...)
	}
	return true, nil
}

// claim takes the most overdue pending job for the lease, or reports that none is due. The same statement counts
// the attempt and pushes next_at out by the lease, so a second worker sees no due job where the first found one, and
// a crashed worker's job is due again when the lease ends.
func (w *Worker) claim(ctx context.Context) (job, bool, error) {
	var j job
	err := w.pool.QueryRow(ctx,
		`UPDATE proof_jobs
		    SET attempts = attempts + 1, next_at = now() + make_interval(secs => $1), updated_at = now()
		  WHERE (plan_id, stop_id) = (
		        SELECT plan_id, stop_id FROM proof_jobs
		         WHERE state = 'pending' AND next_at <= now()
		         ORDER BY next_at
		         LIMIT 1
		           FOR UPDATE SKIP LOCKED)
		RETURNING plan_id, route_id, stop_id, attempts`, w.lease.Seconds()).
		Scan(&j.planID, &j.routeID, &j.stopID, &j.attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return job{}, false, nil
	}
	if err != nil {
		return job{}, false, err
	}
	return j, true, nil
}

// skipError says the job needs no analysis (the reason is for the log and last_error).
type skipError struct{ reason string }

func (e skipError) Error() string { return e.reason }

// permanentError says that trying again cannot help.
type permanentError struct{ err error }

func (e permanentError) Error() string { return e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }

// attempt does the work of one claim. It returns nil when the analysis is stored (the job is then done, marked in the
// same transaction), a skipError or a permanentError for an end without one, and any other error for a failure worth
// another try.
func (w *Worker) attempt(ctx context.Context, j *job) error {
	in, photo, err := w.prepare(ctx, j)
	if err != nil {
		return err
	}
	j.photoBytes = len(in.Data)

	// Cut the call off before the lease runs out, so another worker never takes a job that is still being worked.
	callCtx, cancel := context.WithTimeout(ctx, w.lease*3/4)
	defer cancel()
	analysis, err := w.analyzer.AnalyzeProof(callCtx, in)
	switch {
	case errors.Is(err, ai.ErrProofImage), errors.Is(err, aiapi.ErrInvalid):
		return permanentError{err}
	case err != nil:
		return err
	}
	return w.store(ctx, *j, photo, analysis)
}

// prepare reads the plan outside any transaction and decides what the analyzer is to look at, or why there is
// nothing to do. photo identifies the exact photo the analysis will be about.
func (w *Worker) prepare(ctx context.Context, j *job) (ai.ProofInput, [sha256.Size]byte, error) {
	var none [sha256.Size]byte
	rec, err := plans.Load(ctx, w.pool, j.planID)
	if errors.Is(err, plans.ErrNotFound) {
		return ai.ProofInput{}, none, skipError{"the plan is gone"}
	}
	if err != nil {
		return ai.ProofInput{}, none, err
	}
	stop := locate(&rec.Plan, j.routeID, j.stopID)
	switch {
	case stop == nil:
		return ai.ProofInput{}, none, skipError{"the stop is gone"}
	case !hasPhoto(stop):
		return ai.ProofInput{}, none, skipError{"the stop has no photo proof"}
	case stop.Proof.Analysis != nil:
		return ai.ProofInput{}, none, skipError{"the photo has been analysed already"}
	}

	flags, err := w.flags(ctx)
	if err != nil {
		return ai.ProofInput{}, none, err
	}
	if !flags.Vision {
		return ai.ProofInput{}, none, skipError{"the vision feature is switched off"}
	}

	mime, data, err := decodePhoto(stop.Proof.Photo)
	if err != nil {
		return ai.ProofInput{}, none, permanentError{err}
	}
	return ai.ProofInput{MIME: mime, Data: data, Parcels: parcelsOf(stop)}, sha256.Sum256([]byte(stop.Proof.Photo)), nil
}

// store puts the analysis on the stop. The plan is locked and read again first: the analysis took seconds, and
// it is only kept when the stop still holds the photo it describes and has no analysis yet (another worker, after a
// lease that ran out, may have been first). Only the stop's proof changes: the plan's version, the route versions
// and the timeline are not touched, because nothing the shipper decided or the driver did has changed.
func (w *Worker) store(ctx context.Context, j job, photo [sha256.Size]byte, analysis contracts.ProofAnalysis) error {
	return platform.WithTx(ctx, w.pool, func(tx pgx.Tx) error {
		rec, err := plans.Lock(ctx, tx, j.planID)
		if errors.Is(err, plans.ErrNotFound) {
			return skipError{"the plan is gone"}
		}
		if err != nil {
			return err
		}
		stop := locate(&rec.Plan, j.routeID, j.stopID)
		switch {
		case stop == nil:
			return skipError{"the stop is gone"}
		case !hasPhoto(stop) || sha256.Sum256([]byte(stop.Proof.Photo)) != photo:
			return skipError{"the photo changed while it was analysed"}
		case stop.Proof.Analysis != nil:
			return skipError{"the photo has been analysed already"}
		}
		stop.Proof.Analysis = &analysis
		if err := plans.Save(ctx, tx, rec); err != nil {
			return err
		}
		if err := notify.PlanChanged(ctx, tx, rec.Plan.UserID, rec.Plan.ID); err != nil {
			return err
		}
		return mark(ctx, tx, j, stateDone, "")
	})
}

// finish records how an attempt ended and returns the state the job is left in ("released" for a job given back
// because the worker is stopping). A failed bookkeeping statement leaves the job pending: its lease runs out and it
// is tried again.
func (w *Worker) finish(ctx context.Context, j job, cause error) string {
	if cause == nil {
		return stateDone // marked by store, in its transaction
	}
	skip, isSkip := errors.AsType[skipError](cause)
	_, isPermanent := errors.AsType[permanentError](cause)
	stopping := ctx.Err() != nil
	// Recording an end must work while the worker is being stopped too.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bookkeepingTimeout)
	defer cancel()

	var state string
	var err error
	switch {
	case isSkip:
		state, err = stateSkipped, mark(ctx, w.pool, j, stateSkipped, skip.reason)
	case isPermanent:
		state, err = stateFailed, mark(ctx, w.pool, j, stateFailed, scrub(cause))
	case stopping:
		// The worker is going away, not the job failing: give the attempt back, so that a restart is not a strike.
		state, err = "released", release(ctx, w.pool, j)
	case j.attempts >= maxAttempts:
		state, err = stateFailed, mark(ctx, w.pool, j, stateFailed, scrub(cause))
	default:
		state, err = statePending, retryLater(ctx, w.pool, j, backoff[j.attempts-1], scrub(cause))
	}
	if err != nil {
		w.log.ErrorContext(ctx, "proof analysis could not record the outcome of a job", "planId", j.planID, "stopId", j.stopID, "error", err.Error())
	}
	return state
}

// retryLater keeps the job pending and due again after the delay.
func retryLater(ctx context.Context, db platform.Querier, j job, delay time.Duration, lastError string) error {
	_, err := db.Exec(ctx,
		`UPDATE proof_jobs SET next_at = now() + make_interval(secs => $4), last_error = $5, updated_at = now()
		  WHERE plan_id = $1 AND stop_id = $2 AND state = 'pending' AND attempts = $3`,
		j.planID, j.stopID, j.attempts, delay.Seconds(), lastError)
	return err
}

// release makes the job due at once and takes back the attempt the claim counted.
func release(ctx context.Context, db platform.Querier, j job) error {
	_, err := db.Exec(ctx,
		`UPDATE proof_jobs SET attempts = attempts - 1, next_at = now(), updated_at = now()
		  WHERE plan_id = $1 AND stop_id = $2 AND state = 'pending' AND attempts = $3`,
		j.planID, j.stopID, j.attempts)
	return err
}

// mark ends a job. It only touches the job as this claim left it: if the lease ran out and another worker took it
// meanwhile (its claim counted one more attempt), this one's word no longer counts.
func mark(ctx context.Context, db platform.Querier, j job, state, lastError string) error {
	_, err := db.Exec(ctx,
		`UPDATE proof_jobs SET state = $4, last_error = $5, updated_at = now()
		  WHERE plan_id = $1 AND stop_id = $2 AND state = 'pending' AND attempts = $3`,
		j.planID, j.stopID, j.attempts, state, lastError)
	return err
}

// locate finds the stop of a job in the plan, or nil.
func locate(p *contracts.Plan, routeID, stopID string) *contracts.RouteStop {
	for i := range p.Routes {
		if p.Routes[i].ID != routeID {
			continue
		}
		for k := range p.Routes[i].Stops {
			if p.Routes[i].Stops[k].ID == stopID {
				return &p.Routes[i].Stops[k]
			}
		}
	}
	return nil
}

func hasPhoto(s *contracts.RouteStop) bool {
	return s.Proof != nil && s.Proof.Kind == contracts.ProofKindPhoto && s.Proof.Photo != ""
}

// parcelsOf describes what was to be delivered at the stop, for the model to compare the photo with.
func parcelsOf(s *contracts.RouteStop) []string {
	var lines []string
	for _, it := range s.Items {
		if name := strings.TrimSpace(it.Name); name != "" {
			lines = append(lines, fmt.Sprintf("%d x %s", max(it.Qty, 1), name))
		}
	}
	return lines
}

// scrub makes an error fit for last_error and the log: one line, without control characters (Postgres text cannot
// hold a NUL), cut at the first sight of a data URL (so a photo can never end up in it) and short.
func scrub(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, "data:"); i >= 0 {
		msg = msg[:i] + "[data URL]"
	}
	msg = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(msg, ""))
	msg = strings.Join(strings.Fields(msg), " ")
	if r := []rune(msg); len(r) > maxErrorChars {
		msg = string(r[:maxErrorChars])
	}
	return msg
}
