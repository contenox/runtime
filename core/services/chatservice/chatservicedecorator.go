package chatservice

import (
	"context"
	"fmt"
	"time"

	"github.com/contenox/contenox/core/serverops"
)

type activityTrackerDecorator struct {
	service Service
	tracker serverops.ActivityTracker
}

func (d *activityTrackerDecorator) NewInstance(ctx context.Context, subject string, preferredModels ...string) (string, error) {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"create",
		"chat-session",
		"subject", subject,
		"preferredModels", fmt.Sprintf("%v", preferredModels),
	)
	defer endFn()

	sessionID, err := d.service.NewInstance(ctx, subject, preferredModels...)
	if err != nil {
		reportErrFn(err)
	} else {
		reportChangeFn(sessionID, map[string]interface{}{
			"id":             sessionID,
			"startedAt":      time.Now().UTC(),
			"preferredModel": preferredModels[0],
		})
	}

	return sessionID, err
}

func (d *activityTrackerDecorator) Chat(ctx context.Context, subjectID string, message string, preferredModelNames ...string) (string, int, int, error) {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"chat",
		"message",
		"subjectID", subjectID,
		"model", preferredModelNames[0],
	)
	defer endFn()

	response, tokencount, outputtokencount, err := d.service.Chat(ctx, subjectID, message, preferredModelNames...)
	if err != nil {
		reportErrFn(err)
	} else {
		reportChangeFn(subjectID, map[string]interface{}{
			"user_message":       message,
			"response":           response,
			"input_token_count":  tokencount,
			"output_token_count": outputtokencount,
		})
	}

	return response, tokencount, outputtokencount, err
}

func (d *activityTrackerDecorator) AddInstruction(ctx context.Context, id string, message string) error {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"add",
		"instruction",
		"chatID", id,
		"length", len(message),
	)
	defer endFn()

	err := d.service.AddInstruction(ctx, id, message)
	if err != nil {
		reportErrFn(err)
	} else {
		reportChangeFn(id, map[string]interface{}{
			"content": message,
		})
	}

	return err
}

func (d *activityTrackerDecorator) GetChatHistory(ctx context.Context, id string) ([]ChatMessage, error) {
	reportErrFn, _, endFn := d.tracker.Start(
		ctx,
		"read",
		"chat-history",
		"chatID", id,
	)
	defer endFn()

	history, err := d.service.GetChatHistory(ctx, id)
	if err != nil {
		reportErrFn(err)
	}

	return history, err
}

func (d *activityTrackerDecorator) ListChats(ctx context.Context) ([]ChatSession, error) {
	reportErrFn, _, endFn := d.tracker.Start(ctx, "list", "chats")
	defer endFn()

	sessions, err := d.service.ListChats(ctx)
	if err != nil {
		reportErrFn(err)
	}

	return sessions, err
}

func (d *activityTrackerDecorator) GetServiceName() string {
	return d.service.GetServiceName()
}

func (d *activityTrackerDecorator) GetServiceGroup() string {
	return d.service.GetServiceGroup()
}

func WithActivityTracker(service Service, tracker serverops.ActivityTracker) Service {
	return &activityTrackerDecorator{
		service: service,
		tracker: tracker,
	}
}

var _ Service = (*activityTrackerDecorator)(nil)
