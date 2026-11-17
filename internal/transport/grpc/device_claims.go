package grpc

import (
	"context"

	"github.com/contenox/runtime/internal/transport"
)

func (s *Server) claimDevices(ctx context.Context, request *transport.DeviceClaimRequest) (*transport.DeviceClaimResult, error) {
	if err := checkFence(ctx, s.instanceID); err != nil {
		return nil, encodeError(err)
	}
	if request.OwnerInstance != s.instanceID {
		return nil, encodeError(transport.ErrStaleFence)
	}
	owner, ok := s.svc.(interface {
		ClaimDevices(context.Context, transport.DeviceClaimRequest) (transport.DeviceClaimResult, error)
	})
	if !ok {
		return nil, encodeError(transport.ErrUnsupportedFeature)
	}
	result, err := owner.ClaimDevices(ctx, *request)
	if err != nil {
		return nil, encodeError(err)
	}
	return &result, nil
}

// ClaimDevices retains all resolved accelerator allocations until the worker terminates.
func (c *Client) ClaimDevices(ctx context.Context, request transport.DeviceClaimRequest) (transport.DeviceClaimResult, error) {
	var result transport.DeviceClaimResult
	err := c.invoke(ctx, "ClaimDevices", request, &result)
	return result, err
}
