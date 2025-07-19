package telegramservice

import (
	"context"

	"github.com/contenox/runtime-mvp/core/serverops"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/google/uuid"
)

var _ Worker = (*activityTrackerDecorator)(nil)

type activityTrackerDecorator struct {
	worker  Worker
	tracker serverops.ActivityTracker
}

func (d *activityTrackerDecorator) ReceiveTick(ctx context.Context) error {
	return d.worker.ReceiveTick(ctx)
}

func (d *activityTrackerDecorator) ProcessTick(ctx context.Context) error {
	return d.worker.ProcessTick(ctx)
}

func (d *activityTrackerDecorator) Process(ctx context.Context, update *tgbotapi.Update) error {
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

func (d *activityTrackerDecorator) GetServiceName() string {
	return d.worker.GetServiceName()
}

func (d *activityTrackerDecorator) GetServiceGroup() string {
	return d.worker.GetServiceGroup()
}

// Wrap a Worker with an activity tracker.
func WithActivityTracker(worker Worker, tracker serverops.ActivityTracker) Worker {
	return &activityTrackerDecorator{
		worker:  worker,
		tracker: tracker,
	}
}
