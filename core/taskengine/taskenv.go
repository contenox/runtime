package taskengine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/contenox/contenox/core/llmresolver"
	"github.com/contenox/contenox/core/serverops"
)

type EnvExecutor interface {
	ExecEnv(ctx context.Context, chain *ChainDefinition, input string) (any, error)
}

var ErrUnsupportedTaskType = errors.New("executor does not support the task type")

type HookProvider any

type SimpleEnv struct {
	exec    TaskExecutor
	tracker serverops.ActivityTracker
}

func NewEnv(
	_ context.Context,
	tracker serverops.ActivityTracker,
	exec TaskExecutor,
) (EnvExecutor, error) {
	return &SimpleEnv{
		exec:    exec,
		tracker: tracker,
	}, nil
}

func (exe SimpleEnv) ExecEnv(ctx context.Context, chain *ChainDefinition, input string) (any, error) {
	vars := map[string]any{
		"input": input,
	}
	resolver := llmresolver.Randomly
	var err error
	if len(chain.RoutingStrategy) > 0 {
		resolver, err = llmresolver.PolicyFromString(chain.RoutingStrategy)
		if err != nil {
			return nil, err
		}
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

		maxRetries := max(currentTask.RetryOnError, 0)

	retryLoop:
		for retry := 0; retry <= maxRetries; retry++ {
			// Track task attempt start
			taskCtx := ctx
			var cancel context.CancelFunc
			if currentTask.Timeout != "" {
				timeout, err := time.ParseDuration(currentTask.Timeout)
				if err != nil {
					return nil, fmt.Errorf("task %s: invalid timeout: %v", currentTask.ID, err)
				}
				taskCtx, cancel = context.WithTimeout(ctx, timeout)
				defer cancel()
			}

			reportErrAttempt, reportChangeAttempt, endAttempt := exe.tracker.Start(
				taskCtx,
				"task_attempt",
				currentTask.ID,
				"retry", retry,
				"task_type", currentTask.Type,
			)
			defer endAttempt()
			output, rawResponse, taskErr = exe.exec.TaskExec(taskCtx, resolver, currentTask, renderedPrompt)
			if taskErr != nil {
				reportErrAttempt(taskErr)
				continue retryLoop
			}

			// Report successful attempt
			reportChangeAttempt(currentTask.ID, output)
			break retryLoop
		}

		if taskErr != nil {
			if currentTask.Transition.OnError != "" {
				previousTaskID := currentTask.ID
				currentTask, err = findTaskByID(chain.Tasks, currentTask.Transition.OnError)
				if err != nil {
					return nil, fmt.Errorf("error transition target not found: %v", err)
				}
				// Track error-based transition
				_, reportChangeErrTransition, endErrTransition := exe.tracker.Start(
					ctx,
					"transition",
					previousTaskID,
					"next_task", currentTask.ID,
					"reason", "error",
				)
				defer endErrTransition()
				reportChangeErrTransition(currentTask.ID, taskErr)
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
			// Track final output
			_, reportChangeFinal, endFinal := exe.tracker.Start(
				ctx,
				"chain_complete",
				"chain",
				"final_output", finalOutput,
			)
			defer endFinal()
			reportChangeFinal("chain", finalOutput)
			break
		}

		// Track normal transition to next task
		_, reportChangeTransition, endTransition := exe.tracker.Start(
			ctx,
			"transition",
			currentTask.ID,
			"next_task", nextTaskID,
		)
		defer endTransition()
		reportChangeTransition(nextTaskID, nil)

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

func parseNumber(s string) (float64, error) {
	// Try int first
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return float64(i), nil
	}
	// Fallback to float
	return strconv.ParseFloat(s, 64)
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
		resNum, err := parseNumber(response)
		if err != nil {
			return false, err
		}
		targetNum, err := parseNumber(value)
		if err != nil {
			return false, err
		}
		return resNum > targetNum, nil
	case "<", "lt":
		resNum, err := parseNumber(response)
		if err != nil {
			return false, err
		}
		targetNum, err := parseNumber(value)
		if err != nil {
			return false, err
		}
		return resNum < targetNum, nil
	case "between":
		parts := strings.Split(value, "-")
		if len(parts) != 2 {
			return false, fmt.Errorf("invalid between range format: %s", value)
		}
		lower, err := parseNumber(strings.TrimSpace(parts[0]))
		if err != nil {
			return false, fmt.Errorf("invalid lower bound: %v", err)
		}
		upper, err := parseNumber(strings.TrimSpace(parts[1]))
		if err != nil {
			return false, fmt.Errorf("invalid upper bound: %v", err)
		}
		resNum, err := parseNumber(response)
		if err != nil {
			return false, err
		}
		return resNum >= lower && resNum <= upper, nil
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
