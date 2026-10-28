// Package proofs reads the photos drivers take as proof of delivery. When a stop is completed with a photo, the
// projection that stores the proof also adds a job here (Enqueue, in the same transaction), and a worker works the
// jobs off: it asks the AI to describe the photo (ai.Service.AnalyzeProof, through the gateway's vision profile) and
// stores the answer on the stop as proof.analysis, which the shipper's dashboard shows as an "AI check" card.
//
// The rules, each covered by tests:
//
//   - The event handler stays fast. It writes a row; the model is only ever called by the worker, outside any
//     transaction, so a slow gateway never holds a plan's row lock or a consumer.
//   - A job is claimed with a lease. A worker that dies leaves its job to come back when the lease runs out, and
//     two workers never hold one job (FOR UPDATE SKIP LOCKED, and the lease moves the job out of the due window).
//   - What is stored is checked at the moment of storing, under the plan's lock: the stop must still hold the very
//     photo that was analysed and have no analysis yet. An analysis of a photo that is gone is dropped.
//   - The admin's vision switch is respected: off means off. A photo that arrives while the switch is off is skipped
//     for good; switching it on again does not go back for old photos.
//   - Failures are told apart. A photo that cannot be analysed is final; a gateway that is down, slow or limited, or
//     a model that did not answer with an analysis, is retried with a growing delay, and given up after the last
//     attempt. The shipper loses only the card, never the delivery.
//   - Nothing the driver sent is logged or kept in a job's error text: only ids, sizes and timings.
package proofs

import (
	"context"

	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

// Enqueue adds the job that reads the photo proving a stop, inside tx (the transaction that stores the proof). A
// stop has one job: a redelivered event finds it and adds nothing, whatever state it is in.
func Enqueue(ctx context.Context, tx platform.Querier, planID, routeID, stopID string) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO proof_jobs (plan_id, route_id, stop_id) VALUES ($1, $2, $3) ON CONFLICT (plan_id, stop_id) DO NOTHING`,
		planID, routeID, stopID)
	return err
}
