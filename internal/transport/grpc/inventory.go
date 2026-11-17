package grpc

import (
	"context"

	"github.com/contenox/runtime/internal/transport"
)

func (s *Server) inventory(ctx context.Context) (*transport.Inventory, error) {
	if err := checkFence(ctx, s.instanceID); err != nil {
		return nil, encodeError(err)
	}
	provider, ok := s.svc.(interface {
		Inventory(context.Context) (transport.Inventory, error)
	})
	if !ok {
		return nil, encodeError(transport.ErrUnsupportedFeature)
	}
	result, err := provider.Inventory(ctx)
	if err != nil {
		return nil, encodeError(err)
	}
	return &result, nil
}

// Inventory reads native backend and device availability without allocating a model.
func (c *Client) Inventory(ctx context.Context) (transport.Inventory, error) {
	var result transport.Inventory
	err := c.invoke(ctx, "Inventory", struct{}{}, &result)
	return result, err
}
