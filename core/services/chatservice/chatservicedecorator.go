package chatservice

import (
	"context"
	"time"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/taskengine"
)

type activityTrackerDecorator struct {
	service Service
	tracker serverops.ActivityTracker
}

func (d *activityTrackerDecorator) NewInstance(ctx context.Context, subject string) (string, error) {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"create",
		"chat-session",
		"subject", subject,
	)
	defer endFn()

	sessionID, err := d.service.NewInstance(ctx, subject)
	if err != nil {
		reportErrFn(err)
		return "", err
	}

	reportChangeFn(sessionID, map[string]interface{}{
		"id":      sessionID,
		"subject": subject,
	})

	return sessionID, nil
}

func (d *activityTrackerDecorator) Chat(ctx context.Context, req ChatRequest) (string, int, int, []taskengine.CapturedStateUnit, error) {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"chat",
		"message",
		"subject_id", req.SubjectID,
		"models", req.PreferredModelNames,
		"provider", req.Provider,
	)
	defer endFn()

	response, tokencount, outputtokencount, capturedStateUnits, err := d.service.Chat(ctx, req)
	if err != nil {
		reportErrFn(err)
		return response, tokencount, outputtokencount, capturedStateUnits, err
	}

	reportChangeFn(req.SubjectID, map[string]interface{}{
		"user_message":       req.Message,
		"response":           response,
		"input_token_count":  tokencount,
		"output_token_count": outputtokencount,
		"timestamp":          time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"state":              capturedStateUnits,
	})

	return response, tokencount, outputtokencount, capturedStateUnits, nil
}

func (d *activityTrackerDecorator) AddInstruction(ctx context.Context, id string, message string) error {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"add",
		"instruction",
		"chat_id", id,
		"length", len(message),
	)
	defer endFn()

	err := d.service.AddInstruction(ctx, id, message)
	if err != nil {
		reportErrFn(err)
		return err
	}

	reportChangeFn(id, map[string]interface{}{
		"content":   message,
		"chat_id":   id,
		"timestamp": time.Now().UTC().Format("2006-01-02T15:04:05Z"),
	})

	return nil
}

func (d *activityTrackerDecorator) GetChatHistory(ctx context.Context, id string) ([]ChatMessage, error) {
	reportErrFn, _, endFn := d.tracker.Start(
		ctx,
		"read",
		"chat-history",
		"chat_id", id,
	)
	defer endFn()

	history, err := d.service.GetChatHistory(ctx, id)
	if err != nil {
		reportErrFn(err)
		return nil, err
	}

	return history, nil
}

func (d *activityTrackerDecorator) ListChats(ctx context.Context) ([]ChatSession, error) {
	reportErrFn, _, endFn := d.tracker.Start(ctx, "list", "chats")
	defer endFn()

	sessions, err := d.service.ListChats(ctx)
	if err != nil {
		reportErrFn(err)
		return nil, err
	}

	return sessions, nil
}

func (d *activityTrackerDecorator) OpenAIChatCompletions(ctx context.Context, req taskengine.OpenAIChatRequest) (*taskengine.OpenAIChatResponse, error) {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"openai_chat_completion",
		"request",
		"model", req.Model,
		"user", req.User,
		"temperature", req.Temperature,
	)
	defer endFn()

	resp, err := d.service.OpenAIChatCompletions(ctx, req)
	if err != nil {
		reportErrFn(err)
		return nil, err
	}

	reportChangeFn(req.Model, map[string]interface{}{
		"request":  req,
		"response": resp,
	})

	return resp, nil
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
