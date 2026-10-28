package plans

import (
	"context"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/auth"
)

type idBody struct {
	ID string `json:"id"`
}

type listBody struct {
	Filter Filter `json:"filter"`
}

// computeBody is the body of plans.computeOptions; effort is optional and means fast when left out.
type computeBody struct {
	ID     string           `json:"id"`
	Effort contracts.Effort `json:"effort"`
}

type saveDraftBody struct {
	ID    string     `json:"id"`
	Patch DraftPatch `json:"patch"`
}

type chooseOptionBody struct {
	ID       string `json:"id"`
	OptionID string `json:"optionId"`
}

type confirmBody struct {
	ID            string                  `json:"id"`
	PaymentMethod contracts.PaymentMethod `json:"paymentMethod"`
}

type cancelBody struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

type rescheduleBody struct {
	ID       string             `json:"id"`
	Schedule contracts.Schedule `json:"schedule"`
}

// Mount adds the plans.* methods the console serves; bodies are named after the frontend's parameters.
func (s *Service) Mount(r *platform.RPC) {
	r.Handle("plans.list", auth.AsCustomer(func(ctx context.Context, customerID string, in listBody) ([]Summary, error) {
		return s.List(ctx, customerID, in.Filter)
	}))
	r.Handle("plans.get", auth.AsCustomer(func(ctx context.Context, customerID string, in idBody) (contracts.Plan, error) {
		return s.Get(ctx, customerID, in.ID)
	}))
	r.Handle("plans.createDraft", auth.AsCustomer(func(ctx context.Context, customerID string, _ struct{}) (contracts.Plan, error) {
		return s.CreateDraft(ctx, customerID)
	}))
	r.Handle("plans.saveDraft", auth.AsCustomer(func(ctx context.Context, customerID string, in saveDraftBody) (contracts.Plan, error) {
		return s.SaveDraft(ctx, customerID, in.ID, in.Patch)
	}))
	r.Stream("plans.computeOptions", func(ctx context.Context, in computeBody, emit func(ComputeEvent) error) error {
		customerID, err := auth.Customer(ctx)
		if err != nil {
			return err
		}
		return s.Compute(ctx, customerID, in.ID, in.Effort, emit)
	})
	r.Handle("plans.chooseOption", auth.AsCustomer(func(ctx context.Context, customerID string, in chooseOptionBody) (contracts.Plan, error) {
		return s.ChooseOption(ctx, customerID, in.ID, in.OptionID)
	}))
	r.Handle("plans.confirm", auth.AsCustomer(func(ctx context.Context, customerID string, in confirmBody) (contracts.Plan, error) {
		return s.Confirm(ctx, customerID, in.ID, in.PaymentMethod)
	}))
	r.Handle("plans.cancel", auth.AsCustomer(func(ctx context.Context, customerID string, in cancelBody) (contracts.Plan, error) {
		return s.Cancel(ctx, customerID, in.ID, in.Reason)
	}))
	r.Handle("plans.reschedule", auth.AsCustomer(func(ctx context.Context, customerID string, in rescheduleBody) (contracts.Plan, error) {
		return s.Reschedule(ctx, customerID, in.ID, in.Schedule)
	}))
	r.Handle("plans.remove", auth.AsCustomer(func(ctx context.Context, customerID string, in idBody) (struct{}, error) {
		return struct{}{}, s.Remove(ctx, customerID, in.ID)
	}))
}
