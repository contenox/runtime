package grpc

import (
	"context"

	"github.com/contenox/runtime/internal/transport"
)

type ownerShutdownResult struct{}

// ShutdownOwner requests idle-only shutdown of the selected owner epoch.
func (c *Client) ShutdownOwner(ctx context.Context, request transport.OwnerShutdownRequest) error {
	if request.OwnerInstanceID == "" {
		request.OwnerInstanceID = c.owner
	}
	return c.invoke(ctx, "ShutdownOwner", &request, new(ownerShutdownResult))
}

func (s *Server) shutdownOwner(ctx context.Context, request *transport.OwnerShutdownRequest) (*ownerShutdownResult, error) {
	if err := checkFence(ctx, s.instanceID); err != nil {
		return nil, encodeError(err)
	}
	owner, ok := s.svc.(transport.OwnerShutdown)
	if !ok {
		return nil, encodeError(transport.ErrUnsupportedFeature)
	}
	if err := owner.ShutdownOwner(ctx, *request); err != nil {
		return nil, encodeError(err)
	}
	return &ownerShutdownResult{}, nil
}
