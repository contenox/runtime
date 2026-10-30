package taskengine

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/internal/llmresolver"
	"github.com/contenox/runtime/runtime/llmrepo"
	libmodelprovider "github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/taskengine/llmretry"
	"github.com/google/uuid"
)

type TaskExecutor interface {
	TaskExec(ctx context.Context, startingTime time.Time, ctxLength int, chainContext *ChainContext, currentTask *TaskDefinition, input any, dataType DataType) (any, DataType, string, error)
}

func ensureUniqueToolCallID(id string) string {
	if id == "" {
		return uuid.NewString()
	}
	return id + "-" + uuid.NewString()[:4]
}

func toolCallIDFor(id string) string {
	if id == "" {
		return uuid.NewString()
	}
	return id
}

type SimpleExec struct {
	repo          llmrepo.ModelRepo
	toolsProvider ToolsRepo
	tracker       libtracker.ActivityTracker
	eventSink     TaskEventSink
}

func NewExec(
	ctx context.Context,
	repo llmrepo.ModelRepo,
	toolsProvider ToolsRepo,
	tracker libtracker.ActivityTracker,
) (TaskExecutor, error) {
	if toolsProvider == nil {
		return nil, fmt.Errorf("tools provider is nil")
	}
	if repo == nil {
		return nil, fmt.Errorf("repo executor is nil")
	}
	return &SimpleExec{
		toolsProvider: toolsProvider,
		repo:          repo,
		tracker:       tracker,
		eventSink:     taskEventSinkFromContext(ctx),
	}, nil
}

func (exe *SimpleExec) publishStepChunk(ctx context.Context, meta llmrepo.Meta, content, thinking string) {
	if content == "" && thinking == "" {
		return
	}
	if exe.eventSink == nil || !exe.eventSink.Wants(TaskEventStepChunk) {
		return
	}
	_, _, end := exe.tracker.Start(ctx, "publish_step_chunk", "task_event",
		"content_len", len(content),
		"thinking_len", len(thinking),
		"model", meta.ModelName,
	)
	defer end()
	event := NewTaskEvent(ctx, TaskEventStepChunk)
	event.ModelName = meta.ModelName
	event.ProviderType = meta.ProviderType
	event.BackendID = meta.BackendID
	event.Content = content
	event.Thinking = thinking
	publishTaskEventBestEffort(ctx, exe.tracker, exe.eventSink, event)
}

func (exe *SimpleExec) publishStepStreamEnd(ctx context.Context, meta llmrepo.Meta, chunkCount int, finishReason string, usage *libmodelprovider.TokenUsage) {
	if exe.eventSink == nil || !exe.eventSink.Wants(TaskEventStepStreamEnd) {
		return
	}
	event := NewTaskEvent(ctx, TaskEventStepStreamEnd)
	event.ModelName = meta.ModelName
	event.ProviderType = meta.ProviderType
	event.BackendID = meta.BackendID
	event.ChunkCount = chunkCount
	event.FinishReason = finishReason
	if usage != nil {
		event.Usage = &TokenUsage{
			Prompt:     usage.PromptTokens,
			Completion: usage.CompletionTokens,
			Thinking:   usage.ThinkingTokens,
			Total:      usage.TotalTokens,
			CacheRead:  usage.CacheReadTokens,
			CacheWrite: usage.CacheWriteTokens,
		}
	}
	publishTaskEventBestEffort(ctx, exe.tracker, exe.eventSink, event)
}

func countsAsStreamChunk(parcel *libmodelprovider.StreamParcel) bool {
	return parcel != nil && (parcel.Data != "" || parcel.Thinking != "")
}

func (exe *SimpleExec) countTokensAndCheckLimit(ctx context.Context, modelName string, text string, ctxLength int) (int, error) {
	if ctxLength <= 0 {
		return 0, nil
	}

	tokenCount, err := exe.repo.CountTokens(ctx, modelName, text)
	if err != nil {
		return 0, fmt.Errorf("token count failed: %w", err)
	}

	if tokenCount > ctxLength {
		return tokenCount, fmt.Errorf("%w: input token count %d > %d", ErrContextLengthExceeded, tokenCount, ctxLength)
	}

	return tokenCount, nil
}

func (exe *SimpleExec) countChatHistoryTokens(ctx context.Context, modelName string, history ChatHistory, ctxLength int) (int, error) {
	if ctxLength <= 0 {
		return 0, nil
	}

	if history.InputTokens > 0 && history.OutputTokens > 0 {
		totalTokens := history.InputTokens + history.OutputTokens
		if totalTokens > ctxLength {
			return totalTokens, fmt.Errorf("%w: chat history token count %d > %d", ErrContextLengthExceeded, totalTokens, ctxLength)
		}
		return totalTokens, nil
	}

	totalTokens := 0
	for _, msg := range history.Messages {
		tokenCount, err := exe.repo.CountTokens(ctx, modelName, msg.Content)
		if err != nil {
			return 0, fmt.Errorf("token count failed for message: %w", err)
		}
		totalTokens += tokenCount
	}

	if totalTokens > ctxLength {
		return totalTokens, fmt.Errorf("%w: chat history token count %d > %d", ErrContextLengthExceeded, totalTokens, ctxLength)
	}

	return totalTokens, nil
}

func reserveOutputTokens(llmCall *LLMExecutionConfig, ctxLength int) int {
	if llmCall.MaxTokens != nil && *llmCall.MaxTokens > 0 {
		return *llmCall.MaxTokens
	}
	if ctxLength >= 8 {
		return ctxLength / 8
	}
	return 0
}

func requestedContextRequirement(ctx context.Context, actualTokens int) int {
	if requested := RequestedContextLengthFromContext(ctx); requested > actualTokens {
		return requested
	}
	return actualTokens
}

func (exe *SimpleExec) shiftMessagesToFit(ctx context.Context, modelName string, msgs []Message, budget int) ([]Message, int, error) {
	toks := make([]int, len(msgs))
	for i, m := range msgs {
		n, err := exe.repo.CountTokens(ctx, modelName, m.Content)
		if err != nil {
			return nil, 0, fmt.Errorf("token count failed: %w", err)
		}
		toks[i] = n
	}

	type unit struct {
		idx    []int
		tokens int
		system bool
	}
	var units []unit
	for i := 0; i < len(msgs); i++ {
		switch {
		case msgs[i].Role == "system":
			units = append(units, unit{idx: []int{i}, tokens: toks[i], system: true})
		case msgs[i].Role == "tool":
		case msgs[i].Role == "assistant" && len(msgs[i].CallTools) > 0:
			u := unit{idx: []int{i}, tokens: toks[i]}
			for j := i + 1; j < len(msgs) && msgs[j].Role == "tool"; j++ {
				u.idx = append(u.idx, j)
				u.tokens += toks[j]
				i = j
			}
			units = append(units, u)
		default:
			units = append(units, unit{idx: []int{i}, tokens: toks[i]})
		}
	}

	systemTokens := 0
	for _, u := range units {
		if u.system {
			systemTokens += u.tokens
		}
	}
	if systemTokens > budget {
		return nil, 0, ErrContextLengthExceeded
	}

	keepUnit := make([]bool, len(units))
	used := systemTokens
	for k := range units {
		if units[k].system {
			keepUnit[k] = true
		}
	}
	for k := len(units) - 1; k >= 0; k-- {
		if units[k].system {
			continue
		}
		if used+units[k].tokens > budget {
			break
		}
		keepUnit[k] = true
		used += units[k].tokens
	}

	keepIdx := make([]bool, len(msgs))
	for k, u := range units {
		if keepUnit[k] {
			for _, ix := range u.idx {
				keepIdx[ix] = true
			}
		}
	}
	out := make([]Message, 0, len(msgs))
	for i := range msgs {
		if keepIdx[i] {
			out = append(out, msgs[i])
		}
	}

	out = repairToolCallPairing(out)
	if len(out) == 0 {
		return nil, 0, ErrContextLengthExceeded
	}

	total := 0
	for _, m := range out {
		n, err := exe.repo.CountTokens(ctx, modelName, m.Content)
		if err != nil {
			return nil, 0, fmt.Errorf("token count failed: %w", err)
		}
		total += n
	}
	return out, total, nil
}

func temperatureValue(t *float32) (float32, bool) {
	if t == nil {
		return 0, false
	}
	return *t, true
}

func GetPrimaryModel(llmCall *LLMExecutionConfig) string {
	if llmCall.Model != "" {
		return llmCall.Model
	}
	if len(llmCall.Models) > 0 {
		return llmCall.Models[0]
	}
	return "default"
}

func routeHistoryPrompt(history ChatHistory) string {
	lines := make([]string, 0, len(history.Messages))
	for _, msg := range history.Messages {
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = "message"
		}
		lines = append(lines, role+": "+strings.TrimSpace(msg.Content))
	}
	return strings.Join(lines, "\n")
}

func (exe *SimpleExec) Prompt(ctx context.Context, systemInstruction string, llmCall LLMExecutionConfig, prompt string, ctxLength int) (result string, meta llmrepo.Meta, retErr error) {
	reportErr, reportChange, end := exe.tracker.Start(ctx, "SimpleExec", "prompt_model",
		"model_name", llmCall.Model,
		"model_names", llmCall.Models,
		"provider_types", llmCall.Providers,
		"provider_type", llmCall.Provider,
	)
	defer end()

	if prompt == "" {
		err := fmt.Errorf("unprocessable empty prompt")
		reportErr(err)
		return "", meta, err
	}

	modelName := GetPrimaryModel(&llmCall)
	combinedText := systemInstruction + "\n" + prompt
	promptTokens, err := exe.countTokensAndCheckLimit(ctx, modelName, combinedText, ctxLength)
	if err != nil {
		reportErr(err)
		return "", meta, err
	}

	providerNames := []string{}
	if llmCall.Provider != "" {
		providerNames = append(providerNames, llmCall.Provider)
	}
	if llmCall.Providers != nil {
		providerNames = append(providerNames, llmCall.Providers...)
	}
	modelNames := []string{}
	if llmCall.Model != "" {
		modelNames = append(modelNames, llmCall.Model)
	}
	if llmCall.Models != nil {
		modelNames = append(modelNames, llmCall.Models...)
	}
	req := llmrepo.Request{
		WorkerRole:    llmCall.ModelRole,
		ProviderTypes: providerNames,
		ModelNames:    modelNames,
		ContextLength: requestedContextRequirement(ctx, promptTokens),
		Tracker:       exe.tracker,
	}

	promptTemp, _ := temperatureValue(llmCall.Temperature)
	streamArgs := []libmodelprovider.ChatArgument{
		libmodelprovider.WithTemperature(float64(promptTemp)),
	}
	if llmCall.MaxTokens != nil && *llmCall.MaxTokens > 0 {
		streamArgs = append(streamArgs, libmodelprovider.WithMaxTokens(*llmCall.MaxTokens))
	}
	if llmCall.Think != "" {
		streamArgs = append(streamArgs, libmodelprovider.WithThink(llmCall.Think))
	}
	if llmCall.Shift {
		streamArgs = append(streamArgs, libmodelprovider.WithShift{})
	}

	messages := []libmodelprovider.Message{}
	if systemInstruction != "" {
		messages = append(messages, libmodelprovider.Message{Role: "system", Content: systemInstruction})
	}
	messages = append(messages, libmodelprovider.Message{Role: "user", Content: prompt})

	stream, meta, err := exe.repo.Stream(ctx, req, messages, streamArgs...)
	if err == nil {
		asm := libmodelprovider.NewStreamAssembler(meta.ProviderType, meta.ModelName)
		chunkCount := 0
		for parcel := range stream {
			if err := asm.Consume(parcel); err != nil {
				err = fmt.Errorf("prompt stream failed: %w", err)
				reportErr(err)
				return "", meta, err
			}
			if countsAsStreamChunk(parcel) {
				chunkCount++
			}
			exe.publishStepChunk(ctx, meta, parcel.Data, parcel.Thinking)
		}
		res, err := asm.Result()
		if err != nil {
			err = fmt.Errorf("prompt stream failed: %w", err)
			reportErr(err)
			return "", meta, err
		}
		exe.publishStepStreamEnd(ctx, meta, chunkCount, res.FinishReason, res.Usage)
		return strings.TrimSpace(res.Content), meta, nil
	}

	if llmCall.MaxTokens != nil && *llmCall.MaxTokens > 0 {
		err = fmt.Errorf("bounded prompt stream failed: %w", err)
		reportErr(err)
		return "", meta, err
	}
	response, meta, err := exe.promptWithRetry(ctx, reportChange, &llmCall, req, systemInstruction, prompt)
	if err != nil {
		err = fmt.Errorf("prompt execution failed: %w", err)
		reportErr(err)
		return "", meta, err
	}

	if meta.Usage != nil && exe.eventSink.Wants(TaskEventTokenUsage) {
		ev := NewTaskEvent(ctx, TaskEventTokenUsage)
		ev.ModelName = meta.ModelName
		ev.TokenUsed = meta.Usage.TotalTokens
		ev.TokenSize = ctxLength
		publishTaskEventBestEffort(ctx, nil, exe.eventSink, ev)
	}

	return strings.TrimSpace(response), meta, nil
}

func (exe *SimpleExec) promptWithRetry(
	ctx context.Context,
	reportChange func(id string, data any),
	llmCall *LLMExecutionConfig,
	req llmrepo.Request,
	systemInstruction, prompt string,
) (string, llmrepo.Meta, error) {
	policy := llmretry.RetryPolicy{}
	if llmCall != nil && llmCall.RetryPolicy != nil {
		policy = *llmCall.RetryPolicy
	}
	primary := GetPrimaryModel(llmCall)
	type promptResult struct {
		response string
		meta     llmrepo.Meta
	}
	attempt := 0
	var prevErr error
	result, outcome, err := llmretry.Do(ctx, policy, primary, func(modelID string) (any, error) {
		attempt++
		if attempt > 1 && reportChange != nil {
			reportChange("retry_attempt", map[string]any{
				"attempt":          attempt,
				"model":            modelID,
				"prev_error_class": string(llmretry.ClassifyError(prevErr)),
				"prev_error":       prevErr.Error(),
			})
		}
		callReq := req
		if modelID != "" && modelID != primary {
			callReq.ModelNames = []string{modelID}
		}
		promptTemp, _ := temperatureValue(llmCall.Temperature)
		r, m, e := exe.repo.PromptExecute(ctx, callReq, systemInstruction, promptTemp, prompt)
		prevErr = e
		if e != nil {
			return nil, e
		}
		return promptResult{response: r, meta: m}, nil
	})
	appendRetryOutcome(ctx, outcome)
	if reportChange != nil {
		reportChange("retry_outcome", map[string]any{
			"attempts":         outcome.Attempts,
			"used_fallback":    outcome.UsedFallback,
			"last_error_class": string(outcome.LastErrorClass),
			"elapsed":          outcome.Elapsed.String(),
		})
	}
	if err != nil {
		return "", llmrepo.Meta{}, err
	}
	pr := result.(promptResult)
	return pr.response, pr.meta, nil
}

func declaredRoutes(branches []TransitionBranch) []string {
	routes := make([]string, 0, len(branches))
	for _, b := range branches {
		if b.Operator == OpEquals && strings.TrimSpace(b.When) != "" {
			routes = append(routes, b.When)
		}
	}
	return routes
}

func selectRoute(answer string, routes []string) string {
	chosen := strings.TrimSpace(answer)
	for _, r := range routes {
		if strings.EqualFold(chosen, r) {
			return r
		}
	}
	for _, r := range routes {
		if strings.Contains(strings.ToLower(chosen), strings.ToLower(r)) {
			return r
		}
	}
	return chosen
}

func (exe *SimpleExec) TaskExec(taskCtx context.Context, startingTime time.Time, ctxLength int, chainContext *ChainContext, currentTask *TaskDefinition, input any, dataType DataType) (any, DataType, string, error) {
	var transitionEval string
	var taskErr error
	var output any = input
	var outputType DataType = dataType
	if taskCtx.Err() != nil {
		return nil, DataTypeAny, "request was canceled", fmt.Errorf("task execution failed: %w", taskCtx.Err())
	}
	if currentTask.Handler == HandleNoop {
		return output, outputType, TransitionNoop, nil
	}
	if currentTask.Tools == nil {
		currentTask.Tools = &ToolsCall{}
	}
	getPrompt := func() (string, error) {
		switch outputType {
		case DataTypeString:
			prompt, ok := input.(string)
			if !ok {
				return "", fmt.Errorf("SEVERBUG: input is not a string")
			}
			return prompt, nil
		case DataTypeInt:
			return fmt.Sprintf("%d", input), nil
		case DataTypeChatHistory:
			history, ok := input.(ChatHistory)
			if !ok {
				return "", fmt.Errorf("SEVERBUG: input is not a chat history")
			}
			if len(history.Messages) == 0 {
				return "", fmt.Errorf("SEVERBUG: chat history is empty")
			}
			return history.Messages[len(history.Messages)-1].Content, nil
		default:
			return "", fmt.Errorf("getPrompt unsupported input type for task %v: %v", currentTask.Handler.String(), outputType.String())
		}
	}
	if len(currentTask.Handler) == 0 {
		return output, dataType, transitionEval, fmt.Errorf("%w: task-type is empty", ErrUnsupportedTaskType)
	}
	switch currentTask.Handler {
	case HandleRaiseError:
		message, err := getPrompt()
		if err != nil {
			return nil, DataTypeAny, "", fmt.Errorf("failed to get prompt: %w", err)
		}
		return nil, DataTypeAny, "", errors.New(message)

	case HandleRoute:
		if currentTask.ExecuteConfig == nil {
			currentTask.ExecuteConfig = &LLMExecutionConfig{}
		}
		routes := declaredRoutes(currentTask.Transition.Branches)
		if len(routes) == 0 {
			return nil, DataTypeAny, "", fmt.Errorf("route task %s has no equals branches to route between", currentTask.ID)
		}
		sys := currentTask.SystemInstruction
		if sys != "" {
			sys += "\n\n"
		}
		sys += "Respond with exactly one of the following labels and nothing else: " + strings.Join(routes, ", ")

		if dataType == DataTypeChatHistory && currentTask.ExecuteConfig.Shift && ctxLength > 0 {
			history, ok := input.(ChatHistory)
			if !ok {
				return nil, DataTypeAny, "", fmt.Errorf("route task %s: input type mismatch for shift: %v", currentTask.ID, dataType)
			}
			if len(history.Messages) > 0 {
				modelName := GetPrimaryModel(currentTask.ExecuteConfig)
				sysTokens, countErr := exe.repo.CountTokens(taskCtx, modelName, sys)
				if countErr != nil {
					return nil, DataTypeAny, "", fmt.Errorf("route task %s: token count failed: %w", currentTask.ID, countErr)
				}
				routeOutputReserve := reserveOutputTokens(currentTask.ExecuteConfig, ctxLength)
				budget := ctxLength - sysTokens - routeOutputReserve
				if budget > 0 {
					slid, _, slideErr := exe.shiftMessagesToFit(taskCtx, modelName, history.Messages, budget)
					if slideErr != nil {
						return nil, DataTypeAny, "", fmt.Errorf("route task %s: %w", currentTask.ID, slideErr)
					}
					history.Messages = slid
					input = history
				}
			}
		}

		var (
			prompt string
			err    error
		)
		switch dataType {
		case DataTypeString, DataTypeInt:
			outputType = dataType
			prompt, err = getPrompt()
		case DataTypeChatHistory:
			history, ok := input.(ChatHistory)
			if !ok {
				return nil, DataTypeAny, "", fmt.Errorf("failed to get prompt: route task %s: input is not a chat history", currentTask.ID)
			}
			prompt = routeHistoryPrompt(history)
		default:
			return nil, DataTypeAny, "", fmt.Errorf("getPrompt unsupported input type for task %v: %v", currentTask.Handler.String(), dataType.String())
		}
		if err != nil {
			return nil, DataTypeAny, "", fmt.Errorf("failed to get prompt: %w", err)
		}

		answer, meta, err := exe.Prompt(taskCtx, sys, *currentTask.ExecuteConfig, prompt, ctxLength)
		chainContext.ModelCall = meta
		if err != nil {
			return nil, DataTypeAny, "", fmt.Errorf("route task %s: %w", currentTask.ID, err)
		}
		return input, dataType, selectRoute(answer, routes), nil

	case HandleChatCompletion:
		if currentTask.ExecuteConfig == nil {
			currentTask.ExecuteConfig = &LLMExecutionConfig{}
		}

		var chatHistory ChatHistory
		var finalExecConfig *LLMExecutionConfig = currentTask.ExecuteConfig
		if input == nil {
			return nil, DataTypeAny, "", fmt.Errorf("input is nil for task %s", currentTask.ID)
		}

		switch dataType {
		case DataTypeChatHistory:
			var ok bool
			chatHistory, ok = input.(ChatHistory)
			if !ok {
				return nil, DataTypeAny, "", fmt.Errorf("input data for handler %s claimed to be %s but was %T", currentTask.Handler, dataType.String(), input)
			}

			if len(chatHistory.Messages) > 0 {
				last := chatHistory.Messages[len(chatHistory.Messages)-1]
				if last.Role == "assistant" && len(last.CallTools) > 0 {
					savedHandler := currentTask.Handler
					currentTask.Handler = HandleExecuteToolCalls
					flushOut, _, _, flushErr := exe.TaskExec(taskCtx, startingTime, ctxLength, chainContext, currentTask, chatHistory, dataType)
					currentTask.Handler = savedHandler
					if flushErr != nil {
						var pendErr *ApprovalPendingError
						if errors.As(flushErr, &pendErr) {
							if h, ok := flushOut.(ChatHistory); ok {
								return h, DataTypeChatHistory, "", flushErr
							}
							return nil, DataTypeAny, "", flushErr
						}
						return nil, DataTypeAny, "", fmt.Errorf("guard: failed to flush pending tool calls: %w", flushErr)
					}
					if h, ok := flushOut.(ChatHistory); ok {
						chatHistory = h
						chatHistory.InputTokens = 0
					}
				}
			}

		case DataTypeString:
			strInput, ok := input.(string)
			if !ok {
				return nil, DataTypeAny, "", fmt.Errorf("input data for handler %s claimed to be string but was %T", currentTask.Handler, input)
			}
			chatHistory = ChatHistory{
				Messages: []Message{
					{Role: "user", Content: strInput, Timestamp: time.Now().UTC()},
				},
			}

		default:
			return nil, DataTypeAny, "", fmt.Errorf("handler '%s' requires input of type 'chat_history' or 'string', used var: %s but got '%s'", currentTask.Handler, currentTask.InputVar, dataType.String())
		}

		modelName := GetPrimaryModel(finalExecConfig)
		if !finalExecConfig.Shift {
			if _, err := exe.countChatHistoryTokens(taskCtx, modelName, chatHistory, ctxLength); err != nil {
				return nil, DataTypeAny, "", err
			}
		}

		if currentTask.SystemInstruction != "" {
			alreadyPresent := false
			for _, msg := range chatHistory.Messages {
				if msg.Role == "system" && msg.Content == currentTask.SystemInstruction {
					alreadyPresent = true
					break
				}
			}
			if !alreadyPresent {
				messages := []Message{{ID: uuid.NewString(), Role: "system", Content: currentTask.SystemInstruction, Timestamp: time.Now().UTC()}}
				chatHistory.Messages = append(messages, chatHistory.Messages...)
				chatHistory.InputTokens = 0
			}
		}

		output, outputType, transitionEval, chainContext.ModelCall, taskErr = exe.executeLLM(
			taskCtx,
			chatHistory,
			ctxLength,
			finalExecConfig,
			chainContext.ClientTools,
			chainContext.Tools,
			nil,
		)

	case HandleExecuteToolCalls:
		if dataType != DataTypeChatHistory {
			taskErr = fmt.Errorf("handler '%s' requires 'chat_history' input, but got '%s'",
				currentTask.Handler, dataType.String())
			break
		}

		chatHistory, ok := input.(ChatHistory)
		if !ok {
			taskErr = fmt.Errorf("input data is not of type ChatHistory")
			break
		}

		if len(chatHistory.Messages) == 0 {
			transitionEval = TransitionNoop
			break
		}

		assistantIdx, preAnswered := resumableToolCallBatch(chatHistory.Messages)
		if assistantIdx < 0 {
			transitionEval = TransitionNoCallsFound
			break
		}
		lastMessage := chatHistory.Messages[assistantIdx]

		allowedTools, explicitToolsScope, err := exe.executionToolsScope(taskCtx, currentTask)
		if err != nil {
			taskErr = fmt.Errorf("failed to resolve execution tools scope: %w", err)
			break
		}
		hiddenTools := executionHiddenTools(currentTask)
		executedAny := false
		priorToolMessages := append([]Message(nil), chatHistory.Messages[:assistantIdx]...)
		batchRepeatCounts := make(map[string]int)
		answeredBatch := make(map[string]bool, len(preAnswered))
		for id := range preAnswered {
			answeredBatch[id] = true
		}
		suspendedBatch := false

		for _, toolCall := range lastMessage.CallTools {
			if toolCall.ID != "" && answeredBatch[toolCall.ID] {
				continue
			}
			argsStr := normalizedToolArguments(toolCall.Function.Arguments)
			repeatIndex := nextToolCallRepeatIndex(priorToolMessages, batchRepeatCounts, toolCall.Function.Name, argsStr)

			var args map[string]any
			argsErr := json.Unmarshal([]byte(argsStr), &args)

			resolutionInfo, found := resolveToolWithResolution(chainContext, toolCall.Function.Name)
			if !found {
				errStr := fmt.Sprintf("tool %s not found", toolCall.Function.Name)
				if hint := toolNotFoundSuggestion(chainContext, hiddenTools, toolCall.Function.Name); hint != "" {
					errStr += hint
				}
				exe.reportInvalidToolCall(taskCtx, toolCall, "not_found", errStr, repeatIndex)
				exe.appendToolErrorResult(taskCtx, &chatHistory, toolCall, errStr, args)
				answeredBatch[toolCall.ID] = true
				continue
			}
			if resolutionInfo.Function.Name != "" && resolutionInfo.Function.Name != toolCall.Function.Name {
				toolCall.Function.Name = resolutionInfo.Function.Name
			}

			if isExecutionToolHidden(hiddenTools, toolCall.Function.Name, resolutionInfo) {
				errStr := fmt.Sprintf("tool %s is hidden for task %s", toolCall.Function.Name, currentTask.ID)
				exe.reportInvalidToolCall(taskCtx, toolCall, "hidden", errStr, repeatIndex)
				exe.appendToolErrorResult(taskCtx, &chatHistory, toolCall, errStr, args)
				answeredBatch[toolCall.ID] = true
				continue
			}
			if explicitToolsScope {
				if _, ok := allowedTools[resolutionInfo.ToolsName]; !ok {
					errStr := fmt.Sprintf("tool %s from tools %q is not allowed for task %s", toolCall.Function.Name, resolutionInfo.ToolsName, currentTask.ID)
					exe.reportInvalidToolCall(taskCtx, toolCall, "not_allowed", errStr, repeatIndex, "tools_name", resolutionInfo.ToolsName)
					exe.appendToolErrorResult(taskCtx, &chatHistory, toolCall, errStr, args)
					answeredBatch[toolCall.ID] = true
					continue
				}
			}

			if argsErr != nil {
				taskErr = fmt.Errorf("failed to unmarshal tool arguments for %s: %w",
					toolCall.Function.Name, argsErr)

				errStr := taskErr.Error()
				exe.reportInvalidToolCall(taskCtx, toolCall, "invalid_arguments", errStr, repeatIndex, "tools_name", resolutionInfo.ToolsName)
				exe.appendToolErrorResult(taskCtx, &chatHistory, toolCall, errStr, args)
				answeredBatch[toolCall.ID] = true
				break
			}

			toolsArgs := make(map[string]string)
			if currentTask.Tools != nil && currentTask.Tools.Args != nil {
				toolsArgs = currentTask.Tools.Args
			}
			toolsCall := &ToolsCall{
				Name:     resolutionInfo.ToolsName,
				ToolName: strings.TrimPrefix(toolCall.Function.Name, resolutionInfo.ToolsName+"."),
				Args:     toolsArgs,
			}

			callCtx := taskCtx
			if currentTask.ExecuteConfig != nil {
				if policy, ok := currentTask.ExecuteConfig.ToolsPolicies[resolutionInfo.ToolsName]; ok && len(policy) > 0 {
					callCtx = WithToolsArgs(callCtx, resolutionInfo.ToolsName, policy)
				}
			}

			currentTokens := chatHistory.InputTokens + chatHistory.OutputTokens
			if currentTokens == 0 && currentTask.ExecuteConfig != nil {
				modelName := GetPrimaryModel(currentTask.ExecuteConfig)
				var err error
				currentTokens, err = exe.countChatHistoryTokens(taskCtx, modelName, chatHistory, ctxLength)
				if err != nil {
					return nil, DataTypeAny, "", fmt.Errorf("failed to count chat history tokens: %w", err)
				}
			}
			remainingTokens := max(ctxLength-currentTokens, 0)
			budgetBytes := max(int64(remainingTokens-500)*3, 0)
			callCtx = context.WithValue(callCtx, ContextKeyOutputByteLimit, budgetBytes)
			callCtx = context.WithValue(callCtx, ContextKeyToolCallID, toolCall.ID)
			if toolCall.ArgsMissing {
				callCtx = WithToolArgsMissing(callCtx, true)
			}
			callCtx = WithSuspendableToolCall(callCtx)

			toolReportErr, toolReportChange, toolEnd := exe.tracker.Start(
				callCtx, "tool_call", toolCall.Function.Name,
				"tools_name", resolutionInfo.ToolsName,
				"call_id", toolCall.ID,
				"repeat_index", repeatIndex,
			)

			if exe.eventSink.Wants(TaskEventToolCallPending) {
				pendingEvent := NewTaskEvent(callCtx, TaskEventToolCallPending)
				pendingEvent.ToolName = toolCall.Function.Name
				pendingEvent.ApprovalID = toolCall.ID
				pendingEvent.ApprovalArgs = args
				publishTaskEventBestEffort(callCtx, exe.tracker, exe.eventSink, pendingEvent)
			}

			result, resultType, err := exe.toolsProvider.Exec(callCtx, startingTime, args, chainContext.Debug, toolsCall)

			var pendErr *ApprovalPendingError
			if errors.As(err, &pendErr) {
				toolEnd()
				taskErr = err
				suspendedBatch = true
				break
			}

			toolExecErr := err
			switch {
			case err != nil && (errors.Is(err, context.Canceled) || callCtx.Err() != nil):
				toolReportErr(err)
				taskErr = err
			case err != nil && errors.Is(err, ErrGateRecordFailed):
				toolReportErr(err)
				taskErr = err
			case err != nil:
				toolReportErr(fmt.Errorf("tool %s execution failed: %w", toolCall.Function.Name, err))
				if toolCall.ArgsMissing {
					result = fmt.Sprintf("the arguments for tool %s never reached the runtime (runtime problem, not your mistake; repeating the same call will not help). The tool reported: %s", toolCall.Function.Name, err)
				} else {
					result = fmt.Sprintf("tool %s execution failed: %s", toolCall.Function.Name, err)
				}
				err = nil
			}

			executedAny = true

			content, marshalErr := serializeToolResultContent(result, resultType)
			if marshalErr != nil {
				taskErr = fmt.Errorf("failed to marshal tool %s result: %w", toolCall.Function.Name, marshalErr)
				toolExecErr = taskErr
				content = toolErrorContent(taskErr.Error())
			}
			if guidance := repeatGuidance(toolCall.Function.Name, repeatIndex, toolExecErr != nil); guidance != "" && resultType == DataTypeString {
				content += "\n" + guidance
			}
			content, capInfo := capToolResultContentFromContext(callCtx, toolCall.Function.Name, content)
			if taskErr == nil {
				reportToolResultContent(toolReportChange, resultType, capInfo)
				reportToolCallRepeat(toolReportChange, repeatIndex)
			}
			toolEnd()

			toolResultMessage := Message{
				ID:              uuid.NewString(),
				Role:            "tool",
				Content:         content,
				ToolCallID:      toolCall.ID,
				ToolResultError: toolExecErr != nil,
				Timestamp:       time.Now().UTC(),
			}
			chatHistory.Messages = append(chatHistory.Messages, toolResultMessage)
			answeredBatch[toolCall.ID] = true

			if exe.eventSink.Wants(TaskEventToolCall) {
				toolEvent := NewTaskEvent(callCtx, TaskEventToolCall)
				toolEvent.ToolName = toolCall.Function.Name
				toolEvent.ApprovalID = toolCall.ID
				toolEvent.ApprovalArgs = args
				toolEvent.Content = content
				if toolExecErr != nil {
					toolEvent.Error = toolExecErr.Error()
				}
				if diff, ok := toolDiffFromResult(result); ok {
					toolEvent.ToolDiffPath = diff.Path
					toolEvent.ToolDiffOldText = diff.OldText
					toolEvent.ToolDiffNewText = diff.NewText
				}
				publishTaskEventBestEffort(callCtx, exe.tracker, exe.eventSink, toolEvent)
			}

			if taskErr != nil {
				break
			}
		}

		if !suspendedBatch {
			for _, tc := range lastMessage.CallTools {
				if tc.ID == "" || answeredBatch[tc.ID] {
					continue
				}
				answeredBatch[tc.ID] = true
				exe.appendToolErrorResult(taskCtx, &chatHistory, tc, interruptedToolCallResult, nil)
			}
		}

		output = chatHistory
		outputType = DataTypeChatHistory

		switch {
		case suspendedBatch:
			transitionEval = ""
		case taskErr != nil:
			transitionEval = TransitionFailed
		case executedAny:
			transitionEval = TransitionToolsExecuted
		default:
			transitionEval = TransitionNoCallsFound
		}

	case HandleTools:
		if currentTask.Tools == nil {
			taskErr = fmt.Errorf("tools task missing tools definition")
		} else {
			if currentTask.Tools.Args == nil {
				currentTask.Tools.Args = make(map[string]string)
			}
			toolsCtx := context.WithValue(taskCtx, ContextKeyToolCallID, ensureUniqueToolCallID(currentTask.ID))
			if currentTask.ExecuteConfig != nil {
				if policy, ok := currentTask.ExecuteConfig.ToolsPolicies[currentTask.Tools.Name]; ok && len(policy) > 0 {
					toolsCtx = WithToolsArgs(toolsCtx, currentTask.Tools.Name, policy)
				}
			}

			toolReportErr, toolReportChange, toolEnd := exe.tracker.Start(
				toolsCtx, "tool_call", currentTask.Tools.ToolName,
				"tools_name", currentTask.Tools.Name,
			)

			output, outputType, transitionEval, taskErr = exe.toolsengine(
				toolsCtx,
				startingTime,
				output,
				currentTask.Tools,
				chainContext.Debug,
				currentTask.OutputTemplate,
			)

			toolExecErr := taskErr
			if taskErr != nil {
				toolReportErr(fmt.Errorf("tools task execution failed: %w", taskErr))
			} else {
				toolReportChange("result_type", outputType.String())
			}
			toolEnd()

			if exe.eventSink.Wants(TaskEventToolCall) {
				toolEvent := NewTaskEvent(toolsCtx, TaskEventToolCall)

				toolName := currentTask.Tools.Name
				if currentTask.Tools.ToolName != "" {
					toolName += "." + currentTask.Tools.ToolName
				}
				toolEvent.ToolName = toolName

				if m, ok := input.(map[string]any); ok {
					toolEvent.ApprovalArgs = m
				} else if s, ok := input.(string); ok {
					toolEvent.ApprovalArgs = map[string]any{"input": s}
				}

				content, marshalErr := serializeToolResultContent(output, outputType)
				if marshalErr != nil {
					content = fmt.Sprintf("error: failed to marshal output: %v", marshalErr)
					if toolExecErr == nil {
						toolExecErr = marshalErr
					}
				}

				toolEvent.Content = content
				if toolExecErr != nil {
					toolEvent.Error = toolExecErr.Error()
				}
				if diff, ok := toolDiffFromResult(output); ok {
					toolEvent.ToolDiffPath = diff.Path
					toolEvent.ToolDiffOldText = diff.OldText
					toolEvent.ToolDiffNewText = diff.NewText
				}

				publishTaskEventBestEffort(toolsCtx, exe.tracker, exe.eventSink, toolEvent)
			}
		}

	default:
		taskErr = fmt.Errorf("unknown task type: %w -- %s", ErrUnsupportedTaskType, currentTask.Handler.String())
	}

	return output, outputType, transitionEval, taskErr
}

const noResolvedToolsInstruction = "No tools are available in this turn. Do not claim to have inspected files, run commands, opened URLs, or used tools. Answer only from the provided conversation and context; if tool access or external inspection is needed, say so explicitly."

func llmCallRequestedTools(llmCall *LLMExecutionConfig) bool {
	return llmCall != nil && (llmCall.PassClientsTools || len(llmCall.Tools) > 0)
}

func (exe *SimpleExec) executeLLM(
	ctx context.Context,
	input ChatHistory,
	ctxLength int,
	llmCall *LLMExecutionConfig,
	clientTools []Tool,
	toolsResolution map[string]ToolWithResolution,
	prelude []Message,
) (any, DataType, string, llmrepo.Meta, error) {
	var meta llmrepo.Meta
	reportErr, reportChange, end := exe.tracker.Start(ctx, "SimpleExec", "prompt_model",
		"model_name", llmCall.Model,
		"model_names", llmCall.Models,
		"provider_types", llmCall.Providers,
		"provider_type", llmCall.Provider)
	defer end()

	input.Messages = repairToolCallPairing(input.Messages)

	tools := []libmodelprovider.Tool{}
	hiddenTools := map[string]struct{}{}
	for _, toolName := range llmCall.HideTools {
		hiddenTools[toolName] = struct{}{}
	}

	if llmCall.PassClientsTools {
		for _, t := range clientTools {
			if _, ok := hiddenTools[t.Function.Name]; ok {
				continue
			}
			tools = append(tools, libmodelprovider.Tool{
				Type: t.Type,
				Function: &libmodelprovider.FunctionTool{
					Name:        t.Function.Name,
					Description: t.Function.Description,
					Parameters:  t.Function.Parameters,
				},
			})
		}
	}

	if len(llmCall.Tools) > 0 {
		resolvedNames, err := resolveToolsNames(ctx, llmCall.Tools, exe.toolsProvider)
		if err != nil {
			return nil, DataTypeAny, "", meta, fmt.Errorf("failed to resolve tools for LLM call: %w", err)
		}
		included := make(map[string]struct{}, len(resolvedNames))
		for _, name := range resolvedNames {
			included[name] = struct{}{}
		}
		for _, twr := range toolsResolution {
			if _, ok := hiddenTools[twr.Function.Name]; ok {
				continue
			}
			if _, ok := included[twr.ToolsName]; ok {
				tools = append(tools, libmodelprovider.Tool{
					Type: twr.Type,
					Function: &libmodelprovider.FunctionTool{
						Name:        twr.Function.Name,
						Description: twr.Function.Description,
						Parameters:  twr.Function.Parameters,
					},
				})
			}
		}
	}
	if len(tools) == 0 && llmCallRequestedTools(llmCall) {
		prelude = append(prelude, Message{
			Role:    "system",
			Content: noResolvedToolsInstruction,
		})
		reportChange("no_tools_prompt_guard", map[string]any{
			"requested_registry_tools": len(llmCall.Tools) > 0,
			"requested_client_tools":   llmCall.PassClientsTools,
		})
	}

	modelName := GetPrimaryModel(llmCall)

	var messagesTokens int

	if input.InputTokens > 0 {
		messagesTokens = input.InputTokens
	} else {
		var total int
		for _, m := range input.Messages {
			cnt, err := exe.repo.CountTokens(ctx, modelName, m.Content)
			if err != nil {
				reportErr(fmt.Errorf("token count failed: %w", err))
				return nil, DataTypeAny, "", meta, fmt.Errorf("token count failed: %w", err)
			}
			total += cnt
		}
		messagesTokens = total
	}

	preludeTokens := 0
	for _, m := range prelude {
		cnt, err := exe.repo.CountTokens(ctx, modelName, m.Content)
		if err != nil {
			reportErr(fmt.Errorf("token count failed: %w", err))
			return nil, DataTypeAny, "", meta, fmt.Errorf("token count failed: %w", err)
		}
		preludeTokens += cnt
	}
	messagesTokens += preludeTokens

	toolTokens, err := exe.countToolTokens(ctx, modelName, tools)
	if err != nil {
		reportErr(err)
		return nil, DataTypeAny, "", meta, fmt.Errorf("failed to count tool tokens: %w", err)
	}

	totalTokens := messagesTokens + toolTokens

	reportChange("token_usage", map[string]any{
		"messages_tokens": messagesTokens,
		"tool_tokens":     toolTokens,
		"total_tokens":    totalTokens,
		"limit":           ctxLength,
	})
	if exe.eventSink.Wants(TaskEventTokenUsage) {
		ev := NewTaskEvent(ctx, TaskEventTokenUsage)
		ev.ModelName = modelName
		ev.TokenUsed = totalTokens
		ev.TokenSize = ctxLength
		publishTaskEventBestEffort(ctx, nil, exe.eventSink, ev)
	}

	trimFired := false
	if ctxLength > 0 && totalTokens > ctxLength {
		if !llmCall.Shift {
			err := fmt.Errorf("%w: total token count %d (messages: %d, tools: %d) > %d", ErrContextLengthExceeded,
				totalTokens, messagesTokens, toolTokens, ctxLength)
			reportErr(err)
			return nil, DataTypeAny, "", meta, err
		}
		trimFired = true
		reserve := reserveOutputTokens(llmCall, ctxLength)
		budget := ctxLength - toolTokens - preludeTokens - reserve
		slid, slidTokens, err := exe.shiftMessagesToFit(ctx, modelName, input.Messages, budget)
		if err != nil {
			wrapped := fmt.Errorf("%w: irreducible context after shift (tools: %d, prelude: %d, reserve: %d, limit: %d)",
				ErrContextLengthExceeded, toolTokens, preludeTokens, reserve, ctxLength)
			reportErr(wrapped)
			return nil, DataTypeAny, "", meta, wrapped
		}
		input.Messages = slid
		messagesTokens = slidTokens + preludeTokens
		totalTokens = messagesTokens + toolTokens
		reportChange("token_usage_post_shift", map[string]any{
			"messages_tokens": messagesTokens,
			"tool_tokens":     toolTokens,
			"total_tokens":    totalTokens,
			"limit":           ctxLength,
			"kept_messages":   len(slid),
		})
		if exe.eventSink.Wants(TaskEventTokenUsage) {
			ev := NewTaskEvent(ctx, TaskEventTokenUsage)
			ev.ModelName = modelName
			ev.TokenUsed = totalTokens
			ev.TokenSize = ctxLength
			publishTaskEventBestEffort(ctx, nil, exe.eventSink, ev)
		}
	}

	messagesC := providerMessagesFromEngine(prelude, input.Messages)

	chatArgs := chatArgsForLLMCall(llmCall, tools)
	toolSchemaBytes := 0
	if len(tools) > 0 {
		if b, err := json.Marshal(tools); err == nil {
			toolSchemaBytes = len(b)
		}
	}
	reportChange("tools_prepared", map[string]any{
		"count":        len(tools),
		"model":        llmCall.Model,
		"schema_bytes": toolSchemaBytes,
	})
	if len(prelude) > 0 {
		preludeBytes := 0
		for _, m := range prelude {
			preludeBytes += len(m.Content)
		}
		reportChange("prelude_injected", map[string]any{
			"count": len(prelude),
			"bytes": preludeBytes,
		})
	}
	providerNames := []string{}
	if llmCall.Provider != "" {
		providerNames = append(providerNames, llmCall.Provider)
	}
	if llmCall.Providers != nil {
		providerNames = append(providerNames, llmCall.Providers...)
	}
	modelNames := []string{}
	if llmCall.Model != "" {
		modelNames = append(modelNames, llmCall.Model)
	}
	if llmCall.Models != nil {
		modelNames = append(modelNames, llmCall.Models...)
	}
	stableHistoryLen := 0
	if !trimFired && len(messagesC) > 1 {
		stableHistoryLen = len(messagesC) - 1
	}
	reserveContext := ctxLength
	if requested := RequestedContextLengthFromContext(ctx); requested > 0 && (reserveContext <= 0 || requested < reserveContext) {
		reserveContext = requested
	}
	outputReserve := reserveOutputTokens(llmCall, reserveContext)
	req := llmrepo.Request{
		WorkerRole:        llmCall.ModelRole,
		ProviderTypes:     providerNames,
		ModelNames:        modelNames,
		ContextLength:     requestedContextRequirement(ctx, totalTokens+outputReserve),
		FitContextToModel: true,
		Tracker:           exe.tracker,
		CacheHints: &llmrepo.CacheHints{
			StableSystem:     true,
			StableTools:      true,
			StableHistoryLen: stableHistoryLen,
		},
	}

	streamed, resp, meta, err := exe.streamWithRetry(ctx, reportChange, llmCall, req, messagesC, chatArgs)
	if !streamed && err != nil && streamSetupCanFallback(err) {
		resp, meta, err = exe.chatWithRetry(ctx, reportChange, llmCall, req, messagesC, chatArgs)
		if err != nil {
			if len(tools) > 0 && isRecoverableToolSurfaceError(err) {
				reportChange("tools_disabled_after_tool_surface_error", map[string]any{
					"tool_count": len(tools),
					"error":      err.Error(),
				})
				noToolReq := req
				noToolReq.ContextLength = requestedContextRequirement(ctx, totalTokens-toolTokens+outputReserve)
				noToolMessages := stripToolProtocolMessages(messagesC)
				noToolArgs := chatArgsForLLMCall(llmCall, nil)
				resp, meta, err = exe.chatWithRetry(ctx, reportChange, llmCall, noToolReq, noToolMessages, noToolArgs)
				if err == nil {
					reportChange("tools_disabled_chat_succeeded", map[string]any{
						"model":         meta.ModelName,
						"provider_type": meta.ProviderType,
					})
				}
			}
		}
		if err == nil {
			fallbackChunks := 0
			if resp.Message.Content != "" || resp.Message.Thinking != "" {
				fallbackChunks = 1
			}
			exe.publishStepChunk(ctx, meta, resp.Message.Content, resp.Message.Thinking)
			exe.publishStepStreamEnd(ctx, meta, fallbackChunks, "", nil)
		}
	}
	if err != nil {
		return nil, DataTypeAny, "", meta, fmt.Errorf("chat failed: %w", err)
	}

	callTools := make([]ToolCall, len(resp.ToolCalls))
	for i, tc := range resp.ToolCalls {
		function := FunctionCall{
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		}
		callTools[i] = ToolCall{
			ID:           toolCallIDFor(tc.ID),
			Function:     function,
			Type:         tc.Type,
			ProviderMeta: tc.ProviderMeta,
			ArgsMissing:  tc.ArgsMissing,
		}
	}
	respMessage := resp.Message
	if repaired := reconcileToolCallArguments(callTools, respMessage.Continuation); repaired > 0 {
		reportChange("tool_arguments_repaired", map[string]any{
			"count": repaired,
			"model": meta.ModelName,
		})
	}
	input.Messages = append(input.Messages, Message{
		Continuation: respMessage.Continuation,
		ID:           uuid.NewString(),
		Role:         respMessage.Role,
		Content:      respMessage.Content,
		Thinking:     respMessage.Thinking,
		CallTools:    callTools,
		Timestamp:    time.Now().UTC(),
	})

	var outputTokensCount int
	if message := resp.Message; len(message.Content) != 0 {
		outputTokensCount, err = exe.repo.CountTokens(ctx, meta.ModelName, message.Content)
		if err != nil {
			err = fmt.Errorf("tokenizer failed: %w", err)
			reportErr(err)
			return nil, DataTypeAny, "", meta, err
		}
	}
	input.OutputTokens = outputTokensCount
	input.FinishReason = resp.FinishReason

	if len(callTools) > 0 {
		return input, DataTypeChatHistory, TransitionToolCall, meta, nil
	}
	return input, DataTypeChatHistory, TransitionExecuted, meta, nil
}

func (exe *SimpleExec) streamChatOnce(
	ctx context.Context,
	reportChange func(id string, data any),
	req llmrepo.Request,
	messages []libmodelprovider.Message,
	chatArgs []libmodelprovider.ChatArgument,
) (streamed bool, produced int, _ libmodelprovider.ChatResult, _ llmrepo.Meta, _ error) {
	stream, meta, err := exe.repo.Stream(ctx, req, messages, chatArgs...)
	if err != nil {
		return false, 0, libmodelprovider.ChatResult{}, llmrepo.Meta{}, err
	}

	asm := libmodelprovider.NewStreamAssembler(meta.ProviderType, meta.ModelName)
	chunkCount := 0
	for parcel := range stream {
		if err := asm.Consume(parcel); err != nil {
			return true, chunkCount, libmodelprovider.ChatResult{}, meta, fmt.Errorf("chat stream failed: %w", err)
		}
		if countsAsStreamChunk(parcel) {
			chunkCount++
		}
		exe.publishStepChunk(ctx, meta, parcel.Data, parcel.Thinking)
	}
	res, err := asm.Result()
	if err != nil {
		return true, chunkCount, libmodelprovider.ChatResult{}, meta, fmt.Errorf("chat stream failed: %w", err)
	}
	exe.publishStepStreamEnd(ctx, meta, chunkCount, res.FinishReason, res.Usage)

	finish := map[string]any{
		"finish_reason": res.FinishReason,
		"model":         meta.ModelName,
		"provider_type": meta.ProviderType,
	}
	if res.Usage != nil {
		finish["prompt_tokens"] = res.Usage.PromptTokens
		finish["completion_tokens"] = res.Usage.CompletionTokens
		finish["total_tokens"] = res.Usage.TotalTokens
	}
	reportChange("stream_finished", finish)

	out := libmodelprovider.ChatResult{
		Message: libmodelprovider.Message{
			Continuation: res.Continuation,
			Role:         "assistant",
			Content:      res.Content,
			Thinking:     res.Thinking,
		},
		ToolCalls:    res.ToolCalls,
		FinishReason: res.FinishReason,
	}
	return true, chunkCount, out, meta, nil
}

func (exe *SimpleExec) streamWithRetry(
	ctx context.Context,
	reportChange func(id string, data any),
	llmCall *LLMExecutionConfig,
	req llmrepo.Request,
	messages []libmodelprovider.Message,
	chatArgs []libmodelprovider.ChatArgument,
) (streamed bool, _ libmodelprovider.ChatResult, _ llmrepo.Meta, _ error) {
	policy := llmretry.RetryPolicy{}
	if llmCall != nil && llmCall.RetryPolicy != nil {
		policy = *llmCall.RetryPolicy
	}
	primary := GetPrimaryModel(llmCall)
	type streamResult struct {
		resp libmodelprovider.ChatResult
		meta llmrepo.Meta
	}
	attempt := 0
	var prevErr error
	var opened bool
	var lastMeta llmrepo.Meta
	result, outcome, err := llmretry.Do(ctx, policy, primary, func(modelID string) (any, error) {
		attempt++
		if attempt > 1 && reportChange != nil {
			reportChange("retry_attempt", map[string]any{
				"attempt":          attempt,
				"model":            modelID,
				"prev_error_class": string(llmretry.ClassifyError(prevErr)),
				"prev_error":       prevErr.Error(),
			})
		}
		callReq := req
		if modelID != "" && modelID != primary {
			callReq.ModelNames = []string{modelID}
		}
		s, produced, r, m, e := exe.streamChatOnce(ctx, reportChange, callReq, messages, chatArgs)
		opened = s
		lastMeta = m
		prevErr = e
		if e != nil {
			if !s && streamSetupCanFallback(e) {
				return nil, llmretry.NoRetry(e)
			}
			if produced > 0 {
				return nil, llmretry.NoRetry(e)
			}
			return nil, e
		}
		return streamResult{resp: r, meta: m}, nil
	})
	appendRetryOutcome(ctx, outcome)
	if reportChange != nil {
		reportChange("retry_outcome", map[string]any{
			"attempts":         outcome.Attempts,
			"used_fallback":    outcome.UsedFallback,
			"last_error_class": string(outcome.LastErrorClass),
			"elapsed":          outcome.Elapsed.String(),
		})
	}
	if err != nil {
		return opened, libmodelprovider.ChatResult{}, lastMeta, err
	}
	sr := result.(streamResult)
	return true, sr.resp, sr.meta, nil
}

func chatArgsForLLMCall(llmCall *LLMExecutionConfig, tools []libmodelprovider.Tool) []libmodelprovider.ChatArgument {
	chatArgs := make([]libmodelprovider.ChatArgument, 0, 6)
	if len(tools) > 0 {
		chatArgs = append(chatArgs, libmodelprovider.WithTools(tools...))
	} else if !llmCallRequestedTools(llmCall) {
		chatArgs = append(chatArgs, libmodelprovider.WithToolChoice(libmodelprovider.ToolChoice{Mode: libmodelprovider.ToolChoiceNone}))
	}
	if v, ok := temperatureValue(llmCall.Temperature); ok {
		chatArgs = append(chatArgs, libmodelprovider.WithTemperature(float64(v)))
	}
	if llmCall.Think != "" {
		chatArgs = append(chatArgs, libmodelprovider.WithThink(llmCall.Think))
	}
	if llmCall.MaxTokens != nil && *llmCall.MaxTokens > 0 {
		chatArgs = append(chatArgs, libmodelprovider.WithMaxTokens(*llmCall.MaxTokens))
	}
	if llmCall.Shift {
		chatArgs = append(chatArgs, libmodelprovider.WithShift{})
	}
	return chatArgs
}

func isRecoverableToolSurfaceError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	if strings.Contains(s, "unsupported feature") && strings.Contains(s, "tool call") {
		return true
	}
	if strings.Contains(s, "json schema conversion failed") || strings.Contains(s, "unrecognized schema") {
		return true
	}
	if strings.Contains(s, "chat template") && strings.Contains(s, "tool") {
		return true
	}
	return strings.Contains(s, "context overflow") || strings.Contains(s, "exceeded the session context window")
}

func providerMessagesFromEngine(prelude, messages []Message) []libmodelprovider.Message {
	out := make([]libmodelprovider.Message, 0, len(prelude)+len(messages))
	for _, m := range prelude {
		out = append(out, libmodelprovider.Message{
			Role:    m.Role,
			Content: m.Content,
		})
	}
	for _, m := range messages {
		var toolCalls []libmodelprovider.ToolCall
		if len(m.CallTools) > 0 {
			toolCalls = make([]libmodelprovider.ToolCall, len(m.CallTools))
			for i, tc := range m.CallTools {
				toolCalls[i].ID = tc.ID
				toolCalls[i].Type = tc.Type
				toolCalls[i].Function.Name = tc.Function.Name
				toolCalls[i].Function.Arguments = tc.Function.Arguments
				toolCalls[i].ProviderMeta = tc.ProviderMeta
			}
		}
		var images []libmodelprovider.ImagePart
		if len(m.Images) > 0 {
			images = make([]libmodelprovider.ImagePart, len(m.Images))
			for i, img := range m.Images {
				images[i] = libmodelprovider.ImagePart{Data: img.Data, MimeType: img.MimeType}
			}
		}
		var audio []libmodelprovider.AudioPart
		if len(m.Audio) > 0 {
			audio = make([]libmodelprovider.AudioPart, len(m.Audio))
			for i, a := range m.Audio {
				audio[i] = libmodelprovider.AudioPart{Data: a.Data, MimeType: a.MimeType}
			}
		}
		out = append(out, libmodelprovider.Message{
			Role:         m.Role,
			Content:      m.Content,
			Images:       images,
			Audio:        audio,
			Continuation: m.Continuation,
			ToolCalls:    toolCalls,
			ToolCallID:   m.ToolCallID,
		})
	}
	return out
}

func stripToolProtocolMessages(messages []libmodelprovider.Message) []libmodelprovider.Message {
	out := make([]libmodelprovider.Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == "tool" {
			continue
		}
		hadToolCalls := len(msg.ToolCalls) > 0
		msg.ToolCalls = nil
		msg.Continuation = nil
		msg.ToolCallID = ""
		if msg.Role == "assistant" && hadToolCalls && strings.TrimSpace(msg.Content) == "" {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func (exe *SimpleExec) toolsengine(
	ctx context.Context,
	startingTime time.Time,
	input any,
	tools *ToolsCall,
	debug bool,
	outputTemplate string,
) (any, DataType, string, error) {
	if tools.Args == nil {
		tools.Args = make(map[string]string)
	}

	toolsOutput, dataType, err := exe.toolsProvider.Exec(ctx, startingTime, input, debug, tools)
	if err != nil {
		return nil, dataType, TransitionFailed, err
	}

	toolsOutput, dataType, normErr := NormalizeDataType(toolsOutput, dataType)
	if normErr != nil {
		return nil, DataTypeAny, TransitionFailed, normErr
	}

	finalOutput := toolsOutput
	finalOutputType := dataType
	finalTransitionEval := TransitionToolsExecuted

	if outputTemplate != "" {
		rendered, tplErr := renderTemplate(outputTemplate, finalOutput)
		if tplErr != nil {
			return nil, DataTypeAny, TransitionFailed, fmt.Errorf("failed to render tools output template: %w", tplErr)
		}
		finalOutput = rendered
		finalOutputType = DataTypeString
		finalTransitionEval = rendered
	}

	return finalOutput, finalOutputType, finalTransitionEval, nil
}

func resolveToolWithResolution(chainContext *ChainContext, toolName string) (ToolWithResolution, bool) {
	if chainContext == nil {
		return ToolWithResolution{}, false
	}

	if twr, ok := chainContext.Tools[toolName]; ok {
		return twr, true
	}

	for _, twr := range chainContext.Tools {
		if twr.Function.Name == toolName || twr.ToolsName == toolName {
			return twr, true
		}
	}

	if !strings.Contains(toolName, ".") {
		var match ToolWithResolution
		matches := 0
		for _, twr := range chainContext.Tools {
			leaf := strings.TrimPrefix(twr.Function.Name, twr.ToolsName+".")
			if leaf == toolName {
				match = twr
				matches++
			}
		}
		if matches == 1 {
			return match, true
		}
	}

	return ToolWithResolution{}, false
}

func toolNotFoundSuggestion(chainContext *ChainContext, hidden map[string]struct{}, name string) string {
	if chainContext == nil {
		return ""
	}
	_, reqLeaf := splitToolName(name)
	type candidate struct {
		name  string
		score int
	}
	seen := map[string]bool{}
	toolsets := map[string]bool{}
	var ranked []candidate
	for _, twr := range chainContext.Tools {
		candidateName := twr.Function.Name
		if candidateName == "" {
			candidateName = twr.ToolsName
		}
		if candidateName == "" || candidateName == name || seen[candidateName] {
			continue
		}
		seen[candidateName] = true
		if isExecutionToolHidden(hidden, candidateName, twr) {
			continue
		}
		namespace, leaf := splitToolName(candidateName)
		if namespace == "" {
			continue
		}
		toolsets[namespace] = true
		score := 10 * sharedNamespaceTokens(name, namespace)
		switch {
		case reqLeaf != "" && leaf == reqLeaf:
			score += 100
		case reqLeaf != "" && leaf != "" && (strings.Contains(leaf, reqLeaf) || strings.Contains(reqLeaf, leaf)):
			score += 40
		}
		ranked = append(ranked, candidate{candidateName, score})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].name < ranked[j].name
	})
	var names []string
	for _, c := range ranked {
		if c.score <= 0 || len(names) == 3 {
			break
		}
		names = append(names, c.name)
	}
	if len(names) > 0 {
		return "; did you mean " + strings.Join(names, ", ") + "?"
	}
	if len(toolsets) == 0 {
		return ""
	}
	all := make([]string, 0, len(toolsets))
	for namespace := range toolsets {
		all = append(all, namespace)
	}
	sort.Strings(all)
	if len(all) > 5 {
		all = all[:5]
	}
	return "; available toolsets: " + strings.Join(all, ", ")
}

func splitToolName(name string) (namespace, leaf string) {
	name = strings.TrimSpace(name)
	idx := strings.LastIndex(name, ".")
	if idx <= 0 || idx == len(name)-1 {
		return "", name
	}
	return name[:idx], name[idx+1:]
}

func sharedNamespaceTokens(a, b string) int {
	if a == "" || b == "" {
		return 0
	}
	tokens := func(namespace string) map[string]bool {
		out := map[string]bool{}
		for _, token := range strings.FieldsFunc(namespace, func(r rune) bool { return r == '-' || r == '_' || r == '.' }) {
			if len(token) >= 3 {
				out[token] = true
			}
		}
		return out
	}
	have := tokens(a)
	shared := 0
	for token := range tokens(b) {
		if have[token] {
			shared++
		}
	}
	return shared
}

func (exe *SimpleExec) executionToolsScope(ctx context.Context, currentTask *TaskDefinition) (map[string]struct{}, bool, error) {
	if currentTask == nil || currentTask.ExecuteConfig == nil || currentTask.ExecuteConfig.Tools == nil {
		return nil, false, nil
	}
	names, err := resolveToolsNames(ctx, currentTask.ExecuteConfig.Tools, exe.toolsProvider)
	if err != nil {
		return nil, true, err
	}
	allowed := make(map[string]struct{}, len(names))
	for _, name := range names {
		allowed[name] = struct{}{}
	}
	return allowed, true, nil
}

func executionHiddenTools(currentTask *TaskDefinition) map[string]struct{} {
	if currentTask == nil || currentTask.ExecuteConfig == nil || len(currentTask.ExecuteConfig.HideTools) == 0 {
		return nil
	}
	hidden := make(map[string]struct{}, len(currentTask.ExecuteConfig.HideTools))
	for _, name := range currentTask.ExecuteConfig.HideTools {
		name = strings.TrimSpace(name)
		if name != "" {
			hidden[name] = struct{}{}
		}
	}
	return hidden
}

func isExecutionToolHidden(hidden map[string]struct{}, toolName string, resolution ToolWithResolution) bool {
	if len(hidden) == 0 {
		return false
	}
	if _, ok := hidden[toolName]; ok {
		return true
	}
	if _, ok := hidden[resolution.Function.Name]; ok {
		return true
	}
	leaf := strings.TrimPrefix(toolName, resolution.ToolsName+".")
	if _, ok := hidden[leaf]; ok {
		return true
	}
	namespaced := resolution.ToolsName + "." + leaf
	if _, ok := hidden[namespaced]; ok {
		return true
	}
	return false
}

func toolErrorContent(errStr string) string {
	payload, err := json.Marshal(map[string]string{"error": errStr})
	if err != nil {
		return fmt.Sprintf(`{"error": "%s"}`, errStr)
	}
	return string(payload)
}

type toolResultCapInfo struct {
	OriginalBytes int
	FinalBytes    int
	MaxBytes      int64
	CapActive     bool
	Truncated     bool
}

type toolDiff struct {
	Path    string
	OldText string
	NewText string
}

type toolDiffProvider interface {
	ToolDiff() (path string, oldText string, newText string, ok bool)
}

func serializeToolResultContent(result any, resultType DataType) (string, error) {
	switch resultType {
	case DataTypeNil:
		return "null", nil
	case DataTypeAny, DataTypeJSON:
		b, err := json.Marshal(result)
		if err != nil {
			return "", err
		}
		return string(b), nil
	default:
		return fmt.Sprintf("%v", result), nil
	}
}

func toolDiffFromResult(result any) (toolDiff, bool) {
	provider, ok := result.(toolDiffProvider)
	if !ok {
		return toolDiff{}, false
	}
	path, oldText, newText, ok := provider.ToolDiff()
	if !ok || path == "" || oldText == newText {
		return toolDiff{}, false
	}
	return toolDiff{Path: path, OldText: oldText, NewText: newText}, true
}

func reconcileToolCallArguments(calls []ToolCall, continuation *libmodelprovider.Continuation) int {
	if continuation == nil || len(calls) == 0 || len(continuation.Items) == 0 {
		return 0
	}
	var replay struct {
		Output []struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"output"`
	}
	if err := json.Unmarshal(continuation.Items, &replay); err != nil || len(replay.Output) == 0 {
		return 0
	}
	type recordedCall struct {
		name      string
		arguments string
	}
	var ordered []recordedCall
	indexByID := make(map[string]int, len(replay.Output))
	for _, item := range replay.Output {
		if !strings.EqualFold(item.Type, "function_call") {
			continue
		}
		key := item.CallID
		if key == "" {
			key = item.ID
		}
		if key == "" {
			continue
		}
		indexByID[key] = len(ordered)
		ordered = append(ordered, recordedCall{name: item.Name, arguments: item.Arguments})
	}
	if len(ordered) == 0 {
		return 0
	}
	used := make([]bool, len(ordered))
	repaired := 0
	for i := range calls {
		var want string
		known := false
		if idx, ok := indexByID[calls[i].ID]; ok && !used[idx] {
			want, known, used[idx] = ordered[idx].arguments, true, true
		}
		if !known {
			for idx := range ordered {
				if used[idx] || ordered[idx].name != calls[i].Function.Name {
					continue
				}
				want, known, used[idx] = ordered[idx].arguments, true, true
				break
			}
		}
		if !known || strings.TrimSpace(want) == "" {
			continue
		}
		if normalizedToolArguments(calls[i].Function.Arguments) == normalizedToolArguments(want) {
			continue
		}
		calls[i].Function.Arguments = want
		calls[i].ArgsMissing = false
		repaired++
	}
	return repaired
}

func normalizedToolArguments(arguments string) string {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return "{}"
	}
	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return trimmed
	}
	normalized, err := json.Marshal(decoded)
	if err != nil {
		return trimmed
	}
	return string(normalized)
}

func nextToolCallRepeatIndex(priorMessages []Message, batchCounts map[string]int, toolName string, arguments string) int {
	key := toolCallRepeatKey(toolName, arguments)
	prior := 0
	for _, msg := range priorMessages {
		for _, call := range msg.CallTools {
			if toolCallRepeatKey(call.Function.Name, normalizedToolArguments(call.Function.Arguments)) == key {
				prior++
			}
		}
	}
	batchCounts[key]++
	return prior + batchCounts[key]
}

func toolCallRepeatKey(toolName string, arguments string) string {
	return toolName + "\x00" + arguments
}

func capToolResultContentFromContext(ctx context.Context, toolName string, content string) (string, toolResultCapInfo) {
	maxBytes, ok := ctx.Value(ContextKeyOutputByteLimit).(int64)
	if !ok {
		return content, toolResultCapInfo{
			OriginalBytes: len(content),
			FinalBytes:    len(content),
		}
	}
	return capToolResultContent(toolName, content, maxBytes)
}

func capToolResultContent(toolName string, content string, maxBytes int64) (string, toolResultCapInfo) {
	info := toolResultCapInfo{
		OriginalBytes: len(content),
		FinalBytes:    len(content),
		MaxBytes:      maxBytes,
		CapActive:     true,
	}
	if maxBytes < 0 || int64(len(content)) <= maxBytes {
		return content, info
	}

	sum := sha256.Sum256([]byte(content))
	payload := map[string]any{
		"error":          "tool_result_too_large",
		"tool":           toolName,
		"original_bytes": info.OriginalBytes,
		"max_bytes":      maxBytes,
		"truncated":      true,
		"sha256":         fmt.Sprintf("%x", sum),
	}
	if preview := toolResultPreview(content, maxBytes); preview != "" {
		payload["preview"] = preview
		payload["preview_kind"] = "head"
	}

	b, err := json.Marshal(payload)
	if err != nil {
		fallback := fmt.Sprintf(`{"error":"tool_result_too_large","tool":%q,"original_bytes":%d,"max_bytes":%d,"truncated":true}`,
			toolName, info.OriginalBytes, maxBytes)
		info.FinalBytes = len(fallback)
		info.Truncated = true
		return fallback, info
	}

	capped := string(b)
	info.FinalBytes = len(capped)
	info.Truncated = true
	return capped, info
}

func toolResultPreview(content string, maxBytes int64) string {
	if maxBytes <= 512 {
		return ""
	}
	previewBytes := int(min(int64(4096), maxBytes/2))
	if previewBytes <= 0 {
		return ""
	}
	if len(content) <= previewBytes {
		return content
	}
	return content[:previewBytes]
}

func reportToolResultContent(reportChange func(string, any), resultType DataType, info toolResultCapInfo) {
	if reportChange == nil {
		return
	}
	reportChange("result_type", resultType.String())
	reportChange("result_bytes", info.FinalBytes)
	reportChange("result_original_bytes", info.OriginalBytes)
	reportChange("result_truncated", info.Truncated)
	if info.CapActive {
		reportChange("result_max_bytes", info.MaxBytes)
	}
}

func reportToolCallRepeat(reportChange func(string, any), repeatIndex int) {
	if reportChange == nil {
		return
	}
	reportChange("repeat_index", repeatIndex)
	if repeatIndex > 1 {
		reportChange("repeated_call", true)
	}
}

func (exe *SimpleExec) reportInvalidToolCall(ctx context.Context, toolCall ToolCall, class string, errStr string, repeatIndex int, kvArgs ...any) {
	callCtx := context.WithValue(ctx, ContextKeyToolCallID, toolCall.ID)
	startArgs := []any{
		"call_id", toolCall.ID,
		"invalid_call", true,
		"invalid_call_class", class,
		"repeat_index", repeatIndex,
	}
	startArgs = append(startArgs, kvArgs...)
	reportErr, reportChange, end := exe.tracker.Start(callCtx, "tool_call", toolCall.Function.Name, startArgs...)
	reportChange("invalid_call", true)
	reportChange("invalid_call_class", class)
	reportToolCallRepeat(reportChange, repeatIndex)
	content := toolErrorContent(errStr)
	reportToolResultContent(reportChange, DataTypeString, toolResultCapInfo{
		OriginalBytes: len(content),
		FinalBytes:    len(content),
	})
	reportErr(errors.New(errStr))
	end()
}

const repeatGuidanceThreshold = 3

func repeatGuidance(toolName string, repeatIndex int, failed bool) string {
	if repeatIndex < repeatGuidanceThreshold {
		return ""
	}
	if !failed && !isReadLikeToolName(toolName) {
		return ""
	}
	if failed {
		return fmt.Sprintf("[harness] This exact %s call has now failed %d times with the same error. Do not call it again with the same arguments. Answer from the results you already have and state plainly what is still missing.", toolName, repeatIndex)
	}
	return fmt.Sprintf("[harness] This exact %s call has already succeeded %d times and its result is above. Do not repeat it. Continue with the next step or answer the user.", toolName, repeatIndex)
}

func isReadLikeToolName(name string) bool {
	leaf := name
	if idx := strings.LastIndex(leaf, "."); idx >= 0 {
		leaf = leaf[idx+1:]
	}
	leaf = strings.ToLower(leaf)
	for _, marker := range []string{"read", "list", "grep", "find", "stat", "count", "search"} {
		if strings.Contains(leaf, marker) {
			return true
		}
	}
	return false
}

func (exe *SimpleExec) appendToolErrorResult(ctx context.Context, chatHistory *ChatHistory, toolCall ToolCall, errStr string, args map[string]any) {
	if chatHistory == nil {
		return
	}
	toolResultMessage := Message{
		ID:              uuid.NewString(),
		Role:            "tool",
		Content:         toolErrorContent(errStr),
		ToolCallID:      toolCall.ID,
		ToolResultError: true,
		Timestamp:       time.Now().UTC(),
	}
	chatHistory.Messages = append(chatHistory.Messages, toolResultMessage)

	if exe.eventSink.Wants(TaskEventToolCall) {
		toolEvent := NewTaskEvent(ctx, TaskEventToolCall)
		toolEvent.ToolName = toolCall.Function.Name
		toolEvent.ApprovalID = toolCall.ID
		toolEvent.Scope.ToolCall = toolCall.ID
		toolEvent.ApprovalArgs = args
		toolEvent.Error = errStr
		toolEvent.Content = toolErrorContent(errStr)
		publishTaskEventBestEffort(ctx, exe.tracker, exe.eventSink, toolEvent)
	}
}

func (exe *SimpleExec) chatWithRetry(
	ctx context.Context,
	reportChange func(id string, data any),
	llmCall *LLMExecutionConfig,
	req llmrepo.Request,
	messages []libmodelprovider.Message,
	chatArgs []libmodelprovider.ChatArgument,
) (libmodelprovider.ChatResult, llmrepo.Meta, error) {
	policy := llmretry.RetryPolicy{}
	if llmCall != nil && llmCall.RetryPolicy != nil {
		policy = *llmCall.RetryPolicy
	}
	primary := GetPrimaryModel(llmCall)
	type chatResult struct {
		resp libmodelprovider.ChatResult
		meta llmrepo.Meta
	}
	attempt := 0
	var prevErr error
	result, outcome, err := llmretry.Do(ctx, policy, primary, func(modelID string) (any, error) {
		attempt++
		if attempt > 1 && reportChange != nil {
			data := map[string]any{
				"attempt":          attempt,
				"model":            modelID,
				"prev_error_class": string(llmretry.ClassifyError(prevErr)),
				"prev_error":       prevErr.Error(),
			}
			reportChange("retry_attempt", data)
		}
		callReq := req
		if modelID != "" && modelID != primary {
			callReq.ModelNames = []string{modelID}
		}
		r, m, e := exe.repo.Chat(ctx, callReq, messages, chatArgs...)
		prevErr = e
		if e != nil {
			return nil, e
		}
		return chatResult{resp: r, meta: m}, nil
	})
	appendRetryOutcome(ctx, outcome)
	if reportChange != nil {
		reportChange("retry_outcome", map[string]any{
			"attempts":         outcome.Attempts,
			"used_fallback":    outcome.UsedFallback,
			"last_error_class": string(outcome.LastErrorClass),
			"elapsed":          outcome.Elapsed.String(),
		})
	}
	if err != nil {
		return libmodelprovider.ChatResult{}, llmrepo.Meta{}, err
	}
	cr := result.(chatResult)
	return cr.resp, cr.meta, nil
}

func (exe *SimpleExec) countToolTokens(ctx context.Context, modelName string, tools []libmodelprovider.Tool) (int, error) {
	if len(tools) == 0 {
		return 0, nil
	}
	toolsJSON, err := json.Marshal(tools)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal tools: %w", err)
	}
	tokenCount, err := exe.repo.CountTokens(ctx, modelName, string(toolsJSON))
	if err != nil {
		return 0, fmt.Errorf("failed to count tool tokens: %w", err)
	}
	return tokenCount, nil
}

func streamSetupCanFallback(err error) bool {
	var httpErr *libmodelprovider.HTTPError
	return !errors.As(err, &httpErr) &&
		!errors.Is(err, llmresolver.ErrNoSatisfactoryModel) &&
		!errors.Is(err, llmresolver.ErrNoAvailableModels) &&
		!errors.Is(err, context.Canceled) &&
		!errors.Is(err, context.DeadlineExceeded)
}
