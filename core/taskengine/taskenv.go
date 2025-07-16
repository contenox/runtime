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

	"github.com/contenox/runtime-mvp/core/llmresolver"
	"github.com/contenox/runtime-mvp/core/serverops"
)

const (
	StatusSuccess             = 1
	StatusUnknownHookProvider = 2
	StatusError               = 3
)

// DataType represents the type of data that can be passed between tasks
type DataType int

// Constants representing hook execution status
const (
	DataTypeAny DataType = iota
	DataTypeString
	DataTypeBool
	DataTypeInt
	DataTypeFloat
	DataTypeSearchResults
	DataTypeJSON
	DataTypeChatHistory
	DataTypeOpenAIChat
	DataTypeOpenAIChatResponse
)

func (d *DataType) String() string {
	switch *d {
	case DataTypeAny:
		return "any"
	case DataTypeString:
		return "string"
	case DataTypeBool:
		return "bool"
	case DataTypeInt:
		return "int"
	case DataTypeFloat:
		return "float"
	case DataTypeSearchResults:
		return "search_results"
	case DataTypeJSON:
		return "json"
	case DataTypeChatHistory:
		return "chat_history"
	case DataTypeOpenAIChat:
		return "openai_chat"
	case DataTypeOpenAIChatResponse:
		return "openai_chat_response"
	default:
		return "unknown"
	}
}

// EnvExecutor defines an environment for executing ChainDefinitions
type EnvExecutor interface {
	// ExecEnv executes a chain with input and returns final output
	ExecEnv(ctx context.Context, chain *ChainDefinition, input any, dataType DataType) (any, []CapturedStateUnit, error)
}

// ErrUnsupportedTaskType indicates unrecognized task type
var ErrUnsupportedTaskType = errors.New("executor does not support the task type")

// HookRepo defines an interface for external system integrations
// and to conduct side effects on internal state.
type HookRepo interface {
	// Exec runs a hook with input and returns results
	Exec(ctx context.Context, startingTime time.Time, input any, dataType DataType, transition string, args *HookCall) (int, any, DataType, string, error)
	HookRegistry
}

type HookRegistry interface {
	Supports(ctx context.Context) ([]string, error)
}

// SimpleEnv is the default implementation of EnvExecutor.
// this is the default EnvExecutor implementation
// It executes tasks in order, using retry and timeout policies, and tracks execution
// progress using an ActivityTracker.
type SimpleEnv struct {
	exec      TaskExecutor
	tracker   serverops.ActivityTracker
	inspector Inspector
}

// NewEnv creates a new SimpleEnv with the given tracker and task executor.
func NewEnv(
	_ context.Context,
	tracker serverops.ActivityTracker,
	exec TaskExecutor,
	inspector Inspector,
) (EnvExecutor, error) {
	return &SimpleEnv{
		exec:      exec,
		tracker:   tracker,
		inspector: inspector,
	}, nil
}

// ExecEnv executes the given chain with the provided input.
//
// It manages the full lifecycle of task execution: rendering prompts, calling the
// TaskExecutor, handling timeouts, retries, transitions, and collecting final output.
func (exe SimpleEnv) ExecEnv(ctx context.Context, chain *ChainDefinition, input any, dataType DataType) (any, []CapturedStateUnit, error) {
	stack := exe.inspector.Start(ctx)

	vars := map[string]any{
		"input": input,
	}
	varTypes := map[string]DataType{"input": dataType}
	startingTime := time.Now().UTC()
	resolver := llmresolver.Randomly
	var err error

	if len(chain.RoutingStrategy) > 0 {
		resolver, err = llmresolver.PolicyFromString(chain.RoutingStrategy)
		if err != nil {
			return nil, stack.GetExecutionHistory(), err
		}
	}

	if err := validateChain(chain.Tasks); err != nil {
		return nil, stack.GetExecutionHistory(), err
	}

	currentTask, err := findTaskByID(chain.Tasks, chain.Tasks[0].ID)
	if err != nil {
		return nil, stack.GetExecutionHistory(), err
	}

	var finalOutput any
	var transitionEval string
	var output any = input
	var outputType DataType = dataType
	var taskErr error

	for {
		// Determine task input
		taskInput := output
		taskInputType := outputType
		if currentTask.InputVar != "" {
			var ok bool
			taskInput, ok = vars[currentTask.InputVar]
			if !ok {
				return nil, stack.GetExecutionHistory(), fmt.Errorf("task %s: input variable %q not found", currentTask.ID, currentTask.InputVar)
			}
			taskInputType, ok = varTypes[currentTask.InputVar]
			if !ok {
				return nil, stack.GetExecutionHistory(), fmt.Errorf("task %s: input variable %q missing type info", currentTask.ID, currentTask.InputVar)
			}
		}

		// Render prompt template using the determined input
		if taskInputType == DataTypeString && currentTask.PromptTemplate != "" {
			rendered, err := renderTemplate(currentTask.PromptTemplate, vars)
			if err != nil {
				return nil, stack.GetExecutionHistory(), fmt.Errorf("task %s: template error: %v", currentTask.ID, err)
			}
			taskInput = rendered
		}

		maxRetries := max(currentTask.RetryOnFailure, 0)

	retryLoop:
		for retry := 0; retry <= maxRetries; retry++ {
			// Note: Return on breakpoint for now
			if stack.HasBreakpoint(currentTask.ID) {
				return nil, stack.GetExecutionHistory(), fmt.Errorf("task %s: breakpoint set", currentTask.ID)
			}

			// Track task attempt start
			taskCtx := ctx
			var cancel context.CancelFunc
			if currentTask.Timeout != "" {
				timeout, err := time.ParseDuration(currentTask.Timeout)
				if err != nil {
					return nil, stack.GetExecutionHistory(), fmt.Errorf("task %s: invalid timeout: %v", currentTask.ID, err)
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

			startTime := time.Now()

			output, outputType, transitionEval, taskErr = exe.exec.TaskExec(taskCtx, startingTime, resolver, int(chain.TokenLimit), currentTask, taskInput, taskInputType)

			duration := time.Since(startTime)
			errState := ErrorResponse{
				ErrorInternal: taskErr,
			}
			if taskErr != nil {
				errState.Error = taskErr.Error()
			}
			// Record execution step
			step := CapturedStateUnit{
				TaskID:     currentTask.ID,
				TaskType:   currentTask.Type.String(),
				InputType:  taskInputType,
				OutputType: outputType,
				Transition: transitionEval,
				Duration:   duration,
				Error:      errState,
			}
			stack.RecordStep(step)

			if taskErr != nil {
				reportErrAttempt(taskErr)
				continue retryLoop
			}

			// Report successful attempt
			reportChangeAttempt(currentTask.ID, output)
			break retryLoop
		}

		if taskErr != nil {
			if currentTask.Transition.OnFailure != "" {
				previousTaskID := currentTask.ID
				currentTask, err = findTaskByID(chain.Tasks, currentTask.Transition.OnFailure)
				if err != nil {
					return nil, stack.GetExecutionHistory(), fmt.Errorf("error transition target not found: %v", err)
				}
				// Track error-based transition
				_, reportChangeErrTransition, endErrTransition := exe.tracker.Start(
					ctx,
					"next_task",
					previousTaskID,
					"next_task", currentTask.ID,
					"reason", "error",
				)
				defer endErrTransition()
				reportChangeErrTransition(currentTask.ID, taskErr)
				continue
			}
			return nil, stack.GetExecutionHistory(), fmt.Errorf("task %s failed after %d retries: %v", currentTask.ID, maxRetries, taskErr)
		}

		// Update execution variables
		vars["previous_output"] = output
		vars[currentTask.ID] = output
		varTypes["previous_output"] = outputType
		varTypes[currentTask.ID] = outputType

		// Handle print statement
		if currentTask.Print != "" {
			printMsg, err := renderTemplate(currentTask.Print, vars)
			if err != nil {
				return nil, stack.GetExecutionHistory(), fmt.Errorf("task %s: print template error: %v", currentTask.ID, err)
			}
			fmt.Println(printMsg)
		}

		// Evaluate transitions
		nextTaskID, err := evaluateTransitions(currentTask.Transition, transitionEval)
		if err != nil {
			return nil, stack.GetExecutionHistory(), fmt.Errorf("task %s: transition error: %v", currentTask.ID, err)
		}

		if nextTaskID == "" || nextTaskID == TermEnd {
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
			"next_task",
			currentTask.ID,
			"next_task", nextTaskID,
		)
		defer endTransition()
		reportChangeTransition(nextTaskID, transitionEval)

		// Find next task
		currentTask, err = findTaskByID(chain.Tasks, nextTaskID)
		if err != nil {
			return nil, stack.GetExecutionHistory(), fmt.Errorf("next task %s not found: %v", nextTaskID, err)
		}
	}

	return finalOutput, stack.GetExecutionHistory(), nil
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

func evaluateTransitions(transition TaskTransition, eval string) (string, error) {
	// First check explicit matches
	for _, ct := range transition.Branches {
		if ct.Operator == OpDefault {
			continue
		}

		match, err := compare(ct.Operator, eval, ct.When)
		if err != nil {
			return "", err
		}
		if match {
			return ct.Goto, nil
		}
	}

	// Then check for default
	for _, ct := range transition.Branches {
		if ct.Operator == "default" {
			return ct.Goto, nil
		}
	}

	return "", fmt.Errorf("no matching transition found")
}

// parseNumber attempts to parse a string as either an integer or float.
func parseNumber(s string) (float64, error) {
	// Try int first
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return float64(i), nil
	}
	// Fallback to float
	return strconv.ParseFloat(s, 64)
}

// compare applies a logical operator to a model response and a target value.
//
// Supported operators include equality, string containment, numeric comparisons,
// and range checks using "parse_range".
func compare(operator OperatorTerm, response, when string) (bool, error) {
	switch operator {
	case OpEquals:
		return response == when, nil
	case OpContains:
		return strings.Contains(response, when), nil
	case OpStartsWith:
		return strings.HasPrefix(response, when), nil
	case OpEndsWith:
		return strings.HasSuffix(response, when), nil
	case OpGreaterThan, OpGt:
		resNum, err := parseNumber(response)
		if err != nil {
			return false, err
		}
		targetNum, err := parseNumber(when)
		if err != nil {
			return false, err
		}
		return resNum > targetNum, nil
	case OpLessThan, OpLt:
		resNum, err := parseNumber(response)
		if err != nil {
			return false, err
		}
		targetNum, err := parseNumber(when)
		if err != nil {
			return false, err
		}
		return resNum < targetNum, nil
	case OpInRange:
		parts := strings.Split(when, "-")
		if len(parts) != 2 {
			return false, fmt.Errorf("invalid between range format: %s", when)
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

// findTaskByID returns the task with the given ID from the task list.
func findTaskByID(tasks []ChainTask, id string) (*ChainTask, error) {
	for _, task := range tasks {
		if task.ID == id {
			return &task, nil
		}
	}
	return nil, fmt.Errorf("task not found: %s", id)
}

func validateChain(tasks []ChainTask) error {
	if len(tasks) == 0 {
		return fmt.Errorf("chain has no tasks")
	}
	for _, ct := range tasks {
		if ct.ID == "" || ct.ID == TermEnd {
			if ct.ID == "" {
				return fmt.Errorf("task ID cannot be empty")
			}
			if ct.ID == TermEnd {
				return fmt.Errorf("task ID cannot be '%s'", TermEnd)
			}
		}
	}
	return nil
}
