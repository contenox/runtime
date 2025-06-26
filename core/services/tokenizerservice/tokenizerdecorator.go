package tokenizerservice

import (
	"context"

	"github.com/contenox/runtime-mvp/core/serverops"
)

type activityTrackerDecorator struct {
	client  Tokenizer
	tracker serverops.ActivityTracker
}

func (d *activityTrackerDecorator) Tokenize(ctx context.Context, modelName string, prompt string) ([]int, error) {
	reportErrFn, _, endFn := d.tracker.Start(
		ctx,
		"tokenize",
		"tokenizer",
		"model", modelName,
		"prompt_length", len(prompt),
	)
	defer endFn()

	tokens, err := d.client.Tokenize(ctx, modelName, prompt)
	if err != nil {
		reportErrFn(err)
	}

	return tokens, err
}

func (d *activityTrackerDecorator) CountTokens(ctx context.Context, modelName string, prompt string) (int, error) {
	reportErrFn, _, endFn := d.tracker.Start(
		ctx,
		"count",
		"tokenizer",
		"model", modelName,
		"prompt_length", len(prompt),
	)
	defer endFn()

	count, err := d.client.CountTokens(ctx, modelName, prompt)
	if err != nil {
		reportErrFn(err)
	}

	return count, err
}

func (d *activityTrackerDecorator) OptimalModel(ctx context.Context, baseModel string) (string, error) {
	reportErrFn, _, endFn := d.tracker.Start(
		ctx,
		"optimal_model",
		"tokenizer",
		"base_model", baseModel,
	)
	defer endFn()

	model, err := d.client.OptimalModel(ctx, baseModel)
	if err != nil {
		reportErrFn(err)
	}

	return model, err
}

// WithActivityTracker decorates the given Tokenizer with activity tracking
func WithActivityTracker(client Tokenizer, tracker serverops.ActivityTracker) Tokenizer {
	return &activityTrackerDecorator{
		client:  client,
		tracker: tracker,
	}
}

var _ Tokenizer = (*activityTrackerDecorator)(nil)
