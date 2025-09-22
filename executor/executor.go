package executor

import (
	"context"
	"time"

	"github.com/contenox/runtime/internal/eventdispatch"
)

type ExecutorManager interface {
	StartSync(ctx context.Context, syncInterval time.Duration)
	StopSync()
	ExecutorSyncTrigger

	eventdispatch.Executor
}

type ExecutorSyncTrigger interface {
	TriggerSync()
}
