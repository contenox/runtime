package telegramservice

import (
	"context"

	"github.com/contenox/runtime-mvp/core/serverops"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/google/uuid"
)

var _ Worker = (*workerTrackerDecorator)(nil)

type workerTrackerDecorator struct {
	worker  Worker
	tracker serverops.ActivityTracker
}

func (d *workerTrackerDecorator) ReceiveTick(ctx context.Context) error {
	return d.worker.ReceiveTick(ctx)
}

func (d *workerTrackerDecorator) ProcessTick(ctx context.Context) error {
	return d.worker.ProcessTick(ctx)
}

func (d *workerTrackerDecorator) Process(ctx context.Context, update *tgbotapi.Update) error {
	if _, ok := ctx.Value(serverops.ContextKeyRequestID).(string); !ok {
		ctx = context.WithValue(ctx, serverops.ContextKeyRequestID, uuid.NewString())
	}
	if update.Message == nil {
		// Not a message; don't track
		return d.worker.Process(ctx, update)
	}

	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"process",
		"telegram_message",
		"user", update.SentFrom().UserName,
		"chat_id", update.Message.Chat.ID,
	)
	defer endFn()

	err := d.worker.Process(ctx, update)
	if err != nil {
		reportErrFn(err)
	} else {
		reportChangeFn("processed", map[string]interface{}{
			"text":       update.Message.Text,
			"chat_id":    update.Message.Chat.ID,
			"message_id": update.Message.MessageID,
			"username":   update.SentFrom().UserName,
		})
	}

	return err
}

func (d *workerTrackerDecorator) GetServiceName() string {
	return d.worker.GetServiceName()
}

func (d *workerTrackerDecorator) GetServiceGroup() string {
	return d.worker.GetServiceGroup()
}

// Wrap a Worker with an activity tracker.
func WithWorkerActivityTracker(worker Worker, tracker serverops.ActivityTracker) Worker {
	return &workerTrackerDecorator{
		worker:  worker,
		tracker: tracker,
	}
}
