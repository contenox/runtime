package acpsvc

import (
	"context"

	libacp "github.com/contenox/runtime/libacp"
)

const (
	terminalAuthMethodID = "terminal"
	envAuthMethodID      = "env"
)

func (t *Transport) Authenticate(ctx context.Context, req libacp.AuthenticateRequest) (libacp.AuthenticateResponse, error) {
	switch req.MethodID {
	case terminalAuthMethodID:
		if clientSupportsTerminalAuth(t.getClientCaps()) {
			return libacp.AuthenticateResponse{}, nil
		}
	case envAuthMethodID:
		if t.deps.Engine == nil && t.deps.EnvSetup != nil && t.deps.EnvSetup.Complete != nil {
			if err := t.deps.EnvSetup.Complete(ctx); err != nil {
				return libacp.AuthenticateResponse{}, libacp.NewErrorf(libacp.ErrAuthRequired, "environment-based setup incomplete: %v", err)
			}
			return libacp.AuthenticateResponse{}, nil
		}
	}
	return libacp.AuthenticateResponse{}, libacp.NewErrorf(libacp.ErrInvalidParams, "auth method %q is not supported; use the terminal method to run --setup", req.MethodID)
}

func (t *Transport) Logout(_ context.Context, _ libacp.LogoutRequest) (libacp.LogoutResponse, error) {
	return libacp.LogoutResponse{}, libacp.MethodNotFound(libacp.MethodLogout)
}
