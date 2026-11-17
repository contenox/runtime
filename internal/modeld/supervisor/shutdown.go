package supervisor

import (
	"context"

	"github.com/contenox/runtime/internal/transport"
)

// ShutdownOwner refuses active work before stopping and joining idle children.
func (s *Service) ShutdownOwner(ctx context.Context, request transport.OwnerShutdownRequest) error {
	if request.OwnerInstanceID != s.epoch {
		return transport.ErrStaleFence
	}
	pool, ok := s.pool.(interface{ CloseIdle(context.Context) error })
	if !ok {
		return transport.ErrUnsupportedFeature
	}
	if err := pool.CloseIdle(ctx); err != nil {
		return err
	}
	s.shutdownOnce.Do(func() { close(s.shutdown) })
	return nil
}

// ShutdownReady closes after idle children terminate and the transport may drain.
func (s *Service) ShutdownReady() <-chan struct{} { return s.shutdown }
