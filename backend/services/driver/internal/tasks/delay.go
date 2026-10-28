package tasks

import (
	"context"

	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

// Running lists every route that runs, for the sweep that looks for delays. A route a cancel closed never runs.
func Running(ctx context.Context, db DB) ([]Task, error) {
	rows, err := db.Query(ctx, `SELECT `+taskColumns+` FROM routes WHERE status = 'berjalan' AND cancelled_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// MarkDelay records that route.delayed was published for the stop at step, as the execution event execVersion.
// Call it with the route locked, in the transaction that enqueues the event.
func MarkDelay(ctx context.Context, q platform.Querier, routeID string, execVersion int64, stopID string, step int) error {
	_, err := q.Exec(ctx, `UPDATE routes SET exec_version = $2, delay_stop = $3, delay_step = $4 WHERE id = $1`, routeID, execVersion, stopID, step)
	return err
}
