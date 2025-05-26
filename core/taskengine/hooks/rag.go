package hooks

import (
	"context"

	"github.com/contenox/contenox/core/taskengine"
)

type RagHook struct {
}

var _ taskengine.HookRepo = (*RagHook)(nil)

func (h *RagHook) Exec(ctx context.Context, hook *taskengine.HookCall) (int, any, error) {
	return 0, nil, nil
}
