package telegramservice

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/taskengine"
	"github.com/contenox/runtime-mvp/libs/libdb"
	"github.com/contenox/runtime-mvp/libs/libroutine"
	"github.com/google/uuid"
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

		worker, err := NewWorker(ctx, fe.BotToken, fe.LastOffset, fe.ChatChain, wf.env, wf.db)
		if err != nil {
			log.Printf("Failed to create worker for bot %s: %v", botID, err)
			continue
		}

		WithWorkerActivityTracker(worker, wf.tracker)
		ctxTelegram := context.WithValue(ctx, serverops.ContextKeyRequestID, "telegram:"+uuid.NewString())
		libroutine.GetPool().StartLoop(ctxTelegram, fe.ID+"ReceiveTick", 1, time.Second, time.Second, worker.ReceiveTick)
		libroutine.GetPool().StartLoop(ctxTelegram, fe.ID+"ProcessTick", 1, time.Second, time.Second, worker.ProcessTick)
	}

	return nil
}
