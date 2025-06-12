package taskengine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/contenox/contenox/core/llmrepo"
	"github.com/contenox/contenox/core/llmresolver"
)

// TaskExecutor defines the interface for executing a single task step.
// It consumes a prompt and resolver policy, and returns structured output
// alongside the raw LLM response.
type TaskExecutor interface {
	TaskExec(ctx context.Context, startingTime time.Time, resolver llmresolver.Policy, currentTask *ChainTask, input any, dataType DataType) (any, DataType, string, error)
}

// SimpleExec is a basic implementation of TaskExecutor.
// It supports prompt-to-string, number, score, range, boolean condition evaluation,
// and delegation to registered hooks.
type SimpleExec struct {
	promptExec   llmrepo.ModelRepo
	hookProvider HookRepo
}

// NewExec creates a new instance of SimpleExec.
func NewExec(
	_ context.Context,
	promptExec llmrepo.ModelRepo,
	hookProvider HookRepo,
) (TaskExecutor, error) {
	if hookProvider == nil {
		return nil, fmt.Errorf("hook provider is nil")
	}
	if promptExec == nil {
		return nil, fmt.Errorf("prompt executor is nil")
	}
	return &SimpleExec{
		hookProvider: hookProvider,
		promptExec:   promptExec,
	}, nil
}

// Prompt resolves a model client using the resolver policy and sends the prompt
// to be executed. Returns the trimmed response string or an error.
func (exe *SimpleExec) Prompt(ctx context.Context, resolver llmresolver.Policy, prompt string) (string, error) {
	if prompt == "" {
		return "", fmt.Errorf("unprocessable empty prompt")
	}
	provider, err := exe.promptExec.GetProvider(ctx)
	if err != nil {
		return "", fmt.Errorf("provider resolution failed: %w", err)
	}
	if provider == nil {
		return "", fmt.Errorf("provider is nil for prompt execution")
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

// rang executes the prompt and attempts to parse the response as a range string (e.g. "6-8").
// If the response is a single number, it returns a degenerate range like "6-6".
func (exe *SimpleExec) rang(ctx context.Context, resolver llmresolver.Policy, prompt string) (string, error) {
	response, err := exe.Prompt(ctx, resolver, prompt)
	if err != nil {
		return "", err
	}
	rangeStr := strings.TrimSpace(response)
	clean := strings.ReplaceAll(rangeStr, " ", "")

	// Check for a range format like "6-8"
	if strings.Contains(clean, "-") {
		parts := strings.Split(clean, "-")
		if len(parts) != 2 {
			return "", fmt.Errorf("invalid range format: %s", rangeStr)
		}
		_, err = strconv.Atoi(parts[0])
		if err != nil {
			return "", err
		}
		_, err = strconv.Atoi(parts[1])
		if err != nil {
			return "", err
		}
		return strings.Join(parts, "-"), nil
	}

	// Fallback: try parsing as a single number
	if _, err := strconv.Atoi(clean); err != nil {
		return "", fmt.Errorf("invalid number format: %s", rangeStr)
	}

	// Treat a single number as a degenerate range like "6-6"
	return clean + "-" + clean, nil
}

// number executes the prompt and parses the response as an integer.
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

// score executes the prompt and parses the response as a floating-point score.
func (exe *SimpleExec) score(ctx context.Context, resolver llmresolver.Policy, prompt string) (float64, error) {
	response, err := exe.Prompt(ctx, resolver, prompt)
	if err != nil {
		return 0, err
	}
	cleaned := strings.ReplaceAll(response, " ", "")
	f, err := strconv.ParseFloat(cleaned, 64)
	if err != nil {
		return 0, err
	}
	return f, nil
}

// TaskExec dispatches task execution based on the task type.
// It handles prompt-based task types like string, number, score, condition, and range,
// as well as custom hook invocations.
func (exe *SimpleExec) TaskExec(taskCtx context.Context, startingTime time.Time, resolver llmresolver.Policy, currentTask *ChainTask, input any, dataType DataType) (any, DataType, string, error) {
	var transitionEval string
	var taskErr error
	var output any = input
	var outputType DataType = dataType
	switch currentTask.Type {
	case PromptToString:
		prompt, ok := input.(string)
		if !ok {
			return nil, DataTypeAny, "", fmt.Errorf("input is not a string")
		}
		transitionEval, taskErr = exe.Prompt(taskCtx, resolver, prompt)
		output = transitionEval
		outputType = DataTypeString
	case PromptToCondition:
		var hit bool
		prompt, ok := input.(string)
		if !ok {
			return nil, DataTypeAny, "", fmt.Errorf("input is not a string")
		}
		hit, taskErr = exe.condition(taskCtx, resolver, currentTask.ConditionMapping, prompt)
		output = hit
		outputType = DataTypeBool
		transitionEval = strconv.FormatBool(hit)
	case PromptToNumber:
		var number int
		prompt, ok := input.(string)
		if !ok {
			return nil, DataTypeAny, "", fmt.Errorf("input is not a string")
		}
		number, taskErr = exe.number(taskCtx, resolver, prompt)
		output = number
		outputType = DataTypeInt
		transitionEval = strconv.FormatInt(int64(number), 10)
	case PromptToScore:
		var score float64
		prompt, ok := input.(string)
		if !ok {
			return nil, DataTypeAny, "", fmt.Errorf("input is not a string")
		}
		score, taskErr = exe.score(taskCtx, resolver, prompt)
		output = score
		outputType = DataTypeFloat
		transitionEval = strconv.FormatFloat(score, 'f', 2, 64)
	case PromptToRange:
		prompt, ok := input.(string)
		if !ok {
			return nil, DataTypeAny, "", fmt.Errorf("input is not a string")
		}
		transitionEval, taskErr = exe.rang(taskCtx, resolver, prompt)
		outputType = DataTypeString
		output = transitionEval
	case Hook:
		if currentTask.Hook == nil {
			taskErr = fmt.Errorf("hook task missing hook definition")
		} else {
			output, outputType, transitionEval, taskErr = exe.hookengine(taskCtx, startingTime, output, outputType, transitionEval, currentTask.Hook)
		}
	default:
		taskErr = fmt.Errorf("unknown task type: %w %s", ErrUnsupportedTaskType, currentTask.Type)
	}

	return output, outputType, transitionEval, taskErr
}

func (exe *SimpleExec) hookengine(ctx context.Context, startingTime time.Time, input any, dataType DataType, transition string, hook *HookCall) (any, DataType, string, error) {
	status, res, dataType, transition, err := exe.hookProvider.Exec(ctx, startingTime, input, dataType, transition, hook)
	if err != nil {
		return nil, dataType, "", err
	}
	if status != StatusSuccess {
		return nil, dataType, "", fmt.Errorf("hook execution failed")
	}
	return res, dataType, transition, nil
}

// condition executes a prompt and evaluates its result against a provided condition mapping.
// It returns true/false based on the resolved condition value or fallback heuristics.
func (exe *SimpleExec) condition(ctx context.Context, resolver llmresolver.Policy, conditionMapping map[string]bool, prompt string) (bool, error) {
	response, err := exe.Prompt(ctx, resolver, prompt)
	if err != nil {
		return false, err
	}
	found := false
	for k := range conditionMapping {
		if k == response {
			found = true
		}
	}
	if !found {
		return false, fmt.Errorf("failed to parse into valid condition output was %s", response)
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
