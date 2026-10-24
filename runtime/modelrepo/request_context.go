package modelrepo

import "context"

type requestedContextLengthKey struct{}

func WithRequestedContextLength(ctx context.Context, contextLength int) context.Context {
	if contextLength <= 0 {
		return ctx
	}
	return context.WithValue(ctx, requestedContextLengthKey{}, contextLength)
}

func RequestedContextLengthFromContext(ctx context.Context) int {
	v, _ := ctx.Value(requestedContextLengthKey{}).(int)
	if v <= 0 {
		return 0
	}
	return v
}

func WithoutRequestedContextLength(ctx context.Context) context.Context {
	return context.WithValue(ctx, requestedContextLengthKey{}, 0)
}
