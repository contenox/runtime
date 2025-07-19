package telegramservice

import (
	"context"
	"fmt"
	"log"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/taskengine"
	"github.com/contenox/runtime-mvp/libs/libdb"
)

type WorkerFactory struct {
	db      libdb.DBManager
	service Service
	env     taskengine.EnvExecutor
	tracker serverops.ActivityTracker
}

func NewWorkerFactory(db libdb.DBManager, env taskengine.EnvExecutor, tracker serverops.ActivityTracker) *WorkerFactory {
	return &WorkerFactory{
		db:      db,
		service: New(db),
		env:     env,
		tracker: tracker,
	}
}

func (wf *WorkerFactory) ReceiveTick(ctx context.Context) error {
	frontends, err := wf.service.List(ctx)
	if err != nil {
		return fmt.Errorf("listing telegram frontends: %w", err)
	}

	for _, fe := range frontends {
		botID := fe.ID

		worker, err := NewWorker(ctx, fe.BotToken, 0, wf.env, wf.db)
		if err != nil {
			log.Printf("Failed to create worker for bot %s: %v", botID, err)
			continue
		}

		WithWorkerActivityTracker(worker, wf.tracker)

		// Run worker logic inline (short-lived, not goroutine-based)
		if err := worker.ReceiveTick(ctx); err != nil {
			log.Printf("Worker ReceiveTick error for bot %s: %v", botID, err)
		}
		if err := worker.ProcessTick(ctx); err != nil {
			log.Printf("Worker ProcessTick error for bot %s: %v", botID, err)
		}
	}

	return nil
}
