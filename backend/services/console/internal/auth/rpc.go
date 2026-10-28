package auth

import (
	"context"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

type registerBody struct {
	Input RegisterInput `json:"input"`
}

type loginBody struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Mount adds the auth.* methods; bodies are named after the frontend's parameters.
func (s *Service) Mount(r *platform.RPC) {
	r.Handle("auth.register", func(ctx context.Context, in registerBody) (SignedIn, error) {
		return s.Register(ctx, in.Input)
	})
	r.Handle("auth.loginCustomer", func(ctx context.Context, in loginBody) (SignedIn, error) {
		return s.Login(ctx, in.Email, in.Password)
	})
	// Not signed in is a normal answer here (null), not an error: the app asks on start-up.
	r.Handle("auth.session", func(ctx context.Context, _ struct{}) (*contracts.Session, error) {
		p, ok := platform.PrincipalFrom(ctx)
		if !ok {
			return nil, nil
		}
		return &p, nil
	})
	r.Handle("auth.logout", func(ctx context.Context, _ struct{}) (struct{}, error) {
		return struct{}{}, s.Logout(ctx)
	})
}
