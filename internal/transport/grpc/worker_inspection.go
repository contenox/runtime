package grpc

import (
	"context"
	"github.com/contenox/runtime/internal/transport"
)

// InspectWorker reads an owned worker through the selected daemon endpoint.
func (c *Client) InspectWorker(ctx context.Context, request transport.WorkerInspectionRequest) (transport.WorkerInspection, error) {
	var result transport.WorkerInspection
	err := c.invoke(ctx, "InspectWorker", request, &result)
	return result, err
}

func (s *Server) inspectWorker(ctx context.Context, request *transport.WorkerInspectionRequest) (*transport.WorkerInspection, error) {
	if err := checkFence(ctx, s.instanceID); err != nil {
		return nil, encodeError(err)
	}
	if request.OwnerInstanceID != s.instanceID {
		return nil, encodeError(transport.ErrStaleFence)
	}
	owner, ok := s.svc.(interface {
		InspectWorker(context.Context, transport.WorkerInspectionRequest) (transport.WorkerInspection, error)
	})
	if !ok {
		return nil, encodeError(transport.ErrUnsupportedFeature)
	}
	result, err := owner.InspectWorker(ctx, *request)
	if err != nil {
		return nil, encodeError(err)
	}
	return &result, nil
}
