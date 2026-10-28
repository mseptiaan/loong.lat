package wallet

import (
	"context"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/auth"
)

type topUpBody struct {
	Input TopUpInput `json:"input"`
}

type idBody struct {
	ID string `json:"id"`
}

// Mount adds the wallet.* methods; bodies are named after the frontend's parameters. simulatePay credits a
// top-up without any payment, so it is only offered when simulate is set (development and tests).
func (s *Service) Mount(r *platform.RPC, simulate bool) {
	r.Handle("wallet.get", auth.AsCustomer(func(ctx context.Context, customerID string, _ struct{}) (Wallet, error) {
		return s.Get(ctx, customerID)
	}))
	r.Handle("wallet.transactions", auth.AsCustomer(func(ctx context.Context, customerID string, _ struct{}) ([]Entry, error) {
		return s.Transactions(ctx, customerID)
	}))
	r.Handle("wallet.createTopUp", auth.AsCustomer(func(ctx context.Context, customerID string, in topUpBody) (contracts.TopUp, error) {
		return s.CreateTopUp(ctx, customerID, in.Input)
	}))
	r.Handle("wallet.getTopUp", topUpMethod(s.GetTopUp))
	r.Handle("wallet.cancelTopUp", topUpMethod(s.CancelTopUp))
	if simulate {
		r.Handle("wallet.simulatePay", topUpMethod(s.SimulatePay))
	}
}

// topUpMethod serves an operation on one of the caller's top-ups, named by the body's id.
func topUpMethod(op func(ctx context.Context, customerID, topUpID string) (contracts.TopUp, error)) func(context.Context, idBody) (contracts.TopUp, error) {
	return auth.AsCustomer(func(ctx context.Context, customerID string, in idBody) (contracts.TopUp, error) {
		return op(ctx, customerID, in.ID)
	})
}
