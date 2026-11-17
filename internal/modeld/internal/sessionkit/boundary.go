package sessionkit

import (
	"context"
	"errors"
)

type boundaryKey struct{}

// WithNativeBoundary attaches a managed worker's compute checkpoint to a native operation.
func WithNativeBoundary(ctx context.Context, boundary func(context.Context) error) (context.Context, error) {
	if ctx == nil || boundary == nil {
		return nil, errors.New("native operation requires context and a compute boundary")
	}
	return context.WithValue(ctx, boundaryKey{}, boundary), nil
}

// NativeBoundary observes cancellation and the owner's compute checkpoint between native steps.
// Direct backend operations use their caller's cancellation without owner scheduling.
func NativeBoundary(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if boundary, managed := ctx.Value(boundaryKey{}).(func(context.Context) error); managed {
		return boundary(ctx)
	}
	return nil
}
