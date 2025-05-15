package taskengine

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/js402/cate/core/llmrepo"
	"github.com/js402/cate/core/llmresolver"
)

type Executor interface {
	Exec(ctx context.Context, chain *ChainDefinition, input string) (any, error)
}

type SimpleExecutor struct {
	promptExec llmrepo.ModelRepo
}

func New(_ context.Context, promptExec llmrepo.ModelRepo) (Executor, error) {
	return SimpleExecutor{promptExec: promptExec}, nil
}

func (exe SimpleExecutor) Exec(ctx context.Context, chain *ChainDefinition, input string) (any, error) {
	vars := map[string]any{
		"input": input,
	}

	currentTask, err := findTaskByID(chain.Tasks, chain.Tasks[0].ID)
	if err != nil {
		return nil, err
	}

	var finalOutput any

	for {
		// Render prompt template
		renderedPrompt, err := renderTemplate(currentTask.PromptTemplate, vars)
		if err != nil {
			return nil, fmt.Errorf("task %s: template error: %v", currentTask.ID, err)
		}

		var rawResponse string
		var output any
		var taskErr error

		// Execute task with retries
		maxRetries := max(currentTask.RetryOnError, 0)

	retryLoop:
		for retry := 0; retry <= maxRetries; retry++ {
			// Handle timeout if specified
			var taskCtx context.Context
			var cancel context.CancelFunc
			if currentTask.Timeout != "" {
				timeout, err := time.ParseDuration(currentTask.Timeout)
				if err != nil {
					return nil, fmt.Errorf("task %s: invalid timeout: %v", currentTask.ID, err)
				}
				taskCtx, cancel = context.WithTimeout(ctx, timeout)
				defer cancel()
			} else {
				taskCtx = ctx
			}

			switch currentTask.Type {
			case PromptToString:
				rawResponse, taskErr = exe.Prompt(taskCtx, renderedPrompt)
				if taskErr != nil {
					continue retryLoop
				}
				output = rawResponse
			case PromptToCondition:
				var hit bool
				hit, taskErr = exe.condition(taskCtx, currentTask.ConditionMapping, renderedPrompt)
				if taskErr != nil {
					continue retryLoop
				}
				output = hit
				rawResponse = strconv.FormatBool(hit)
			case PromptToNumber:
				var number int
				number, taskErr = exe.number(taskCtx, renderedPrompt)
				if taskErr != nil {
					continue retryLoop
				}
				output = number
				rawResponse = strconv.FormatInt(int64(number), 10)
			case PromptToScore:
				var score float64
				score, taskErr = exe.score(taskCtx, renderedPrompt)
				if taskErr != nil {
					continue retryLoop
				}
				output = score
				rawResponse = strconv.FormatFloat(score, 'f', 2, 64)
			case Hook:
				if currentTask.Hook == nil {
					taskErr = fmt.Errorf("hook task missing hook definition")
					continue retryLoop
				}
				output, taskErr = exe.hookengine(taskCtx, *currentTask.Hook)
				if taskErr != nil {
					continue retryLoop
				}
				rawResponse = fmt.Sprintf("%v", output)

			default:
				taskErr = fmt.Errorf("unknown task type: %s", currentTask.Type)
				continue retryLoop
			}

			// If we get here, execution succeeded
			break retryLoop
		}

		if taskErr != nil {
			if currentTask.Transition.OnError != "" {
				currentTask, err = findTaskByID(chain.Tasks, currentTask.Transition.OnError)
				if err != nil {
					return nil, fmt.Errorf("error transition target not found: %v", err)
				}
				continue
			}
			return nil, fmt.Errorf("task %s failed after %d retries: %v",
				currentTask.ID, maxRetries, taskErr)
		}

		// Update execution variables
		vars["previous_output"] = output
		vars[currentTask.ID] = output

		// Handle print statement
		if currentTask.Print != "" {
			printMsg, err := renderTemplate(currentTask.Print, vars)
			if err != nil {
				return nil, fmt.Errorf("task %s: print template error: %v", currentTask.ID, err)
			}
			fmt.Println(printMsg)
		}

		// Evaluate transitions
		nextTaskID, err := evaluateTransitions(currentTask.Transition, rawResponse)
		if err != nil {
			return nil, fmt.Errorf("task %s: transition error: %v", currentTask.ID, err)
		}

		if nextTaskID == "" || nextTaskID == "end" {
			finalOutput = output
			break
		}

		// Find next task
		currentTask, err = findTaskByID(chain.Tasks, nextTaskID)
		if err != nil {
			return nil, fmt.Errorf("next task %s not found: %v", nextTaskID, err)
		}
	}

	return finalOutput, nil
}

func renderTemplate(tmplStr string, vars map[string]any) (string, error) {
	tmpl, err := template.New("prompt").Parse(tmplStr)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vars); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func evaluateTransitions(transition Transition, rawResponse string) (string, error) {
	// First check explicit matches
	for _, ct := range transition.Next {
		if ct.Value == "_default" {
			continue
		}

		match, err := compare(ct.Operator, rawResponse, ct.Value)
		if err != nil {
			return "", err
		}
		if match {
			return ct.ID, nil
		}
	}

	// Then check for default
	for _, ct := range transition.Next {
		if ct.Value == "_default" {
			return ct.ID, nil
		}
	}

	return "", fmt.Errorf("no matching transition found")
}

func compare(operator, response, value string) (bool, error) {
	switch operator {
	case "equals":
		return response == value, nil
	case "contains":
		return strings.Contains(response, value), nil
	case "startsWith":
		return strings.HasPrefix(response, value), nil
	case "endsWith":
		return strings.HasSuffix(response, value), nil
	case ">", "gt":
		resNum, err := strconv.ParseFloat(response, 64)
		if err != nil {
			return false, err
		}
		targetNum, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return false, err
		}
		return resNum > targetNum, nil
	case "<", "lt":
		resNum, err := strconv.ParseFloat(response, 64)
		if err != nil {
			return false, err
		}
		targetNum, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return false, err
		}
		return resNum < targetNum, nil
	default:
		return false, fmt.Errorf("unsupported operator: %s", operator)
	}
}

func findTaskByID(tasks []ChainTask, id string) (*ChainTask, error) {
	for _, task := range tasks {
		if task.ID == id {
			return &task, nil
		}
	}
	return nil, fmt.Errorf("task not found: %s", id)
}

func (exe *SimpleExecutor) Prompt(ctx context.Context, prompt string) (string, error) {
	provider, err := exe.promptExec.GetProvider(ctx)
	if err != nil {
		return "", fmt.Errorf("provider resolution failed: %w", err)
	}

	client, err := llmresolver.ResolvePromptExecute(ctx, llmresolver.ResolvePromptRequest{
		ModelName: provider.ModelName(),
	}, exe.promptExec.GetRuntime(ctx), llmresolver.ResolveRandomly)
	if err != nil {
		return "", fmt.Errorf("client resolution failed: %w", err)
	}

	response, err := client.Prompt(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("prompt execution failed: %w", err)
	}

	return strings.TrimSpace(response), nil
}

func (exe *SimpleExecutor) number(ctx context.Context, prompt string) (int, error) {
	response, err := exe.Prompt(ctx, prompt)
	if err != nil {
		return 0, err
	}
	i, err := strconv.Atoi(response)
	if err != nil {
		return 0, err
	}
	return i, nil
}

func (exe *SimpleExecutor) score(ctx context.Context, prompt string) (float64, error) {
	response, err := exe.Prompt(ctx, prompt)
	if err != nil {
		return 0, err
	}
	f, err := strconv.ParseFloat(response, 10)
	if err != nil {
		return 0, err
	}
	return f, nil
}

func (exe *SimpleExecutor) hookengine(_ context.Context, _ HookCall) (any, error) {
	return nil, fmt.Errorf("unimplemented")
}

func (exe *SimpleExecutor) condition(ctx context.Context, conditionMapping map[string]bool, prompt string) (bool, error) {
	response, err := exe.Prompt(ctx, prompt)
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
