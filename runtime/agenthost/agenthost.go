package agenthost

import (
	"context"
	"sync"

	"github.com/contenox/runtime/libacp"
)

type Agent interface {
	Connect(ctx context.Context, harness libacp.Client) (*Handle, error)
}

type Handle struct {
	Conn *libacp.ClientSideConnection

	closeFn   func() error
	closeOnce sync.Once
	closeErr  error
}

func (h *Handle) Close() error {
	h.closeOnce.Do(func() {
		h.closeErr = h.closeFn()
	})
	return h.closeErr
}
