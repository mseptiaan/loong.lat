package account

import (
	"context"

	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/auth"
)

type updateBody struct {
	Patch Patch `json:"patch"`
}

type addBody struct {
	Input AddressInput `json:"input"`
}

type updateAddressBody struct {
	ID    string       `json:"id"`
	Patch AddressPatch `json:"patch"`
}

type idBody struct {
	ID string `json:"id"`
}

// Mount adds the account.* methods; bodies are named after the frontend's parameters.
func (s *Service) Mount(r *platform.RPC) {
	r.Handle("account.profile", auth.AsCustomer(func(ctx context.Context, customerID string, _ struct{}) (User, error) {
		return s.Profile(ctx, customerID)
	}))
	r.Handle("account.update", auth.AsCustomer(func(ctx context.Context, customerID string, in updateBody) (User, error) {
		return s.Update(ctx, customerID, in.Patch)
	}))
	r.Handle("account.addresses.add", auth.AsCustomer(func(ctx context.Context, customerID string, in addBody) (SavedAddress, error) {
		return s.AddAddress(ctx, customerID, in.Input)
	}))
	r.Handle("account.addresses.update", auth.AsCustomer(func(ctx context.Context, customerID string, in updateAddressBody) (SavedAddress, error) {
		return s.UpdateAddress(ctx, customerID, in.ID, in.Patch)
	}))
	r.Handle("account.addresses.remove", auth.AsCustomer(func(ctx context.Context, customerID string, in idBody) (struct{}, error) {
		return struct{}{}, s.RemoveAddress(ctx, customerID, in.ID)
	}))
}
