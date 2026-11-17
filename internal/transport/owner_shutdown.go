package transport

import "context"

// OwnerShutdownRequest stops a fenced owner only when every worker is idle.
type OwnerShutdownRequest struct {
	Fence
}

// OwnerShutdown rejects active sessions and waits for confirmed child termination.
type OwnerShutdown interface {
	ShutdownOwner(context.Context, OwnerShutdownRequest) error
	ShutdownReady() <-chan struct{}
}
