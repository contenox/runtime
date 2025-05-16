package taskengine

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/js402/cate/core/llmrepo"
	"github.com/js402/cate/core/llmresolver"
)

type TaskExecutor interface {
	TaskExec(ctx context.Context, resolver llmresolver.Policy, currentTask *ChainTask, renderedPrompt string) (any, string, error)
}

type SimpleExec struct {
	promptExec   llmrepo.ModelRepo
	hookProvider HookProvider
}

func NewExec(
	_ context.Context,
	promptExec llmrepo.ModelRepo,
	hookProvider HookProvider,
) (TaskExecutor, error) {
	return &SimpleExec{
		hookProvider: hookProvider,
		promptExec:   promptExec,
	}, nil
}

func (exe *SimpleExec) Prompt(ctx context.Context, resolver llmresolver.Policy, prompt string) (string, error) {
	provider, err := exe.promptExec.GetProvider(ctx)
	if err != nil {
		return "", fmt.Errorf("provider resolution failed: %w", err)
	}

	client, err := llmresolver.PromptExecute(ctx, llmresolver.PromptRequest{
		ModelName: provider.ModelName(),
	}, exe.promptExec.GetRuntime(ctx), resolver)
	if err != nil {
		return "", fmt.Errorf("client resolution failed: %w", err)
	}

	response, err := client.Prompt(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("prompt execution failed: %w", err)
	}

	return strings.TrimSpace(response), nil
}

func (exe *SimpleExec) number(ctx context.Context, resolver llmresolver.Policy, prompt string) (int, error) {
	response, err := exe.Prompt(ctx, resolver, prompt)
	if err != nil {
		return 0, err
	}
	i, err := strconv.Atoi(response)
	if err != nil {
		return 0, err
	}
	return i, nil
}

func (exe *SimpleExec) score(ctx context.Context, resolver llmresolver.Policy, prompt string) (float64, error) {
	response, err := exe.Prompt(ctx, resolver, prompt)
	if err != nil {
		return 0, err
	}
	f, err := strconv.ParseFloat(response, 10)
	if err != nil {
		return 0, err
	}
	return f, nil
}

func (exe *SimpleExec) TaskExec(taskCtx context.Context, resolver llmresolver.Policy, currentTask *ChainTask, renderedPrompt string) (any, string, error) {
	var rawResponse string
	var taskErr error
	var output any
	switch currentTask.Type {
	case PromptToString:
		rawResponse, taskErr = exe.Prompt(taskCtx, resolver, renderedPrompt)
		output = rawResponse
	case PromptToCondition:
		var hit bool
		hit, taskErr = exe.condition(taskCtx, resolver, currentTask.ConditionMapping, renderedPrompt)
		output = hit
		rawResponse = strconv.FormatBool(hit)
	case PromptToNumber:
		var number int
		number, taskErr = exe.number(taskCtx, resolver, renderedPrompt)
		output = number
		rawResponse = strconv.FormatInt(int64(number), 10)
	case PromptToScore:
		var score float64
		score, taskErr = exe.score(taskCtx, resolver, renderedPrompt)
		output = score
		rawResponse = strconv.FormatFloat(score, 'f', 2, 64)
	case Hook:
		if currentTask.Hook == nil {
			taskErr = fmt.Errorf("hook task missing hook definition")
		} else {
			output, taskErr = exe.hookengine(taskCtx, *currentTask.Hook)
			rawResponse = fmt.Sprintf("%v", output)
		}
	default:
		taskErr = fmt.Errorf("unknown task type: %w %s", ErrUnsupportedTaskType, currentTask.Type)
	}

	return output, rawResponse, taskErr
}

func (exe *SimpleExec) hookengine(_ context.Context, _ HookCall) (any, error) {
	// TODO: exe.hookProvider
	return nil, fmt.Errorf("unimplemented")
}

func (exe *SimpleExec) condition(ctx context.Context, resolver llmresolver.Policy, conditionMapping map[string]bool, prompt string) (bool, error) {
	response, err := exe.Prompt(ctx, resolver, prompt)
	if err != nil {
		return false, err
	}
	for key, val := range conditionMapping {
		if strings.EqualFold(response, key) {
			if val {
				return strings.EqualFold(strings.TrimSpace(response), key), nil
			}
			return !strings.EqualFold(strings.TrimSpace(response), key), nil
		}
	}

	return strings.EqualFold(strings.TrimSpace(response), "yes"), nil
}
