package plans

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/notify"
)

// OptionSetRef names one computation of a plan's options: the pair that changes whenever the options are computed
// again, and that is what a report written from them has to still match when it is filed.
type OptionSetRef struct {
	InputHash  string
	ComputedAt contracts.Time
}

// RefOf names the computation an option set came from.
func RefOf(set *contracts.OptionSet) OptionSetRef {
	return OptionSetRef{InputHash: set.InputHash, ComputedAt: set.ComputedAt}
}

func (r OptionSetRef) matches(set *contracts.OptionSet) bool {
	return set != nil && set.InputHash == r.InputHash && set.ComputedAt.Equal(r.ComputedAt.Time)
}

// StoreReport files an AI report with the option set it explains, which the customer sees in plans.get. It does so
// only while the plan still holds that very option set, compared under the plan's row lock: a report is slow to
// write, and the shipper may have edited the draft or computed the options again meanwhile, in which case the
// report describes options that no longer exist and is not kept (stored is false). Nothing but the report changes:
// not the version, the time of the last change, the status or the choice among the options.
func StoreReport(ctx context.Context, db platform.TxBeginner, customerID, planID string, of OptionSetRef, report contracts.Report) (stored bool, err error) {
	err = platform.WithTx(ctx, db, func(tx pgx.Tx) error {
		rec, err := lockOwned(ctx, tx, customerID, planID)
		if err != nil {
			return err
		}
		if !of.matches(rec.Plan.OptionSet) {
			return nil
		}
		rec.Plan.OptionSet.Report = &report
		if err := Save(ctx, tx, rec); err != nil {
			return err
		}
		stored = true
		return notify.PlanChanged(ctx, tx, customerID, planID)
	})
	return stored && err == nil, err
}
