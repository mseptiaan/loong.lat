// Package apix holds the few helpers every domain package of the service needs to talk to the RPC layer.
package apix

import (
	"context"
	"uuid"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

// Admin wraps an RPC handler so that only a signed-in admin reaches it: 401 without a session, 403 for any
// other role.
func Admin[In, Out any](fn func(ctx context.Context, in In) (Out, error)) func(ctx context.Context, in In) (Out, error) {
	return func(ctx context.Context, in In) (Out, error) {
		if _, err := platform.RequireRole(ctx, contracts.RoleAdmin); err != nil {
			var zero Out
			return zero, err
		}
		return fn(ctx, in)
	}
}

// Field ties an error to the form field it is about, so the console shows it under the field.
func Field(err *platform.APIError, field string) *platform.APIError {
	err.Field = field
	return err
}

// NewID returns a new entity id: the prefix and a UUIDv7, e.g. "drv-0198a5d2-...".
func NewID(prefix string) string { return prefix + "-" + uuid.NewV7().String() }
