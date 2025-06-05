package hooks

// type ChatHook struct {
// 	dbInstance  libdb.DBManager
// 	chatManager chat.Manager
// }

// func NewChatHook(dbInstance libdb.DBManager, chatManager chat.Manager) *ChatHook {
// 	return &ChatHook{
// 		dbInstance:  dbInstance,
// 		chatManager: chatManager,
// 	}
// }

// func (h *ChatHook) Get(name string) (func(context.Context, any, *taskengine.HookCall) (int, any, error), error) {
// 	switch name {
// 	case "load_chat_history":
// 		return h.LoadChatHistory, nil
// 	case "append_user_input":
// 		return h.AppendUserInput, nil
// 	case "execute_chat_model":
// 		return h.ChatExec, nil
// 	case "persist_input_output":
// 		return h.PersistMessages, nil
// 	default:
// 		return nil, fmt.Errorf("unknown hook: %s", name)
// 	}
// }

// func (h *ChatHook) Exec(ctx context.Context, input any, args *taskengine.HookCall) (int, any, error) {
// 	if args == nil || args.Type == "" {
// 		return taskengine.StatusError, nil, fmt.Errorf("invalid hook call: missing type")
// 	}

// 	hookFunc, err := h.Get(args.Type)
// 	if err != nil {
// 		return taskengine.StatusError, nil, fmt.Errorf("failed to get hook function: %w", err)
// 	}

// 	return hookFunc(ctx, input, args)
// }

// // LoadChatHistory loads chat history from storage
// func (h *ChatHook) LoadChatHistory(ctx context.Context, hook *taskengine.HookCall) (int, any, error) {
// 	if hook.Args == nil || hook.Args["subject_id"] == "" {
// 		return taskengine.StatusError, nil, fmt.Errorf("missing session_id")
// 	}

// 	subjectID := hook.Args["subject_id"]
// 	tx := h.dbInstance.WithoutTransaction()
// 	messages, err := h.chatManager.ListMessages(ctx, tx, subjectID)
// 	if err != nil {
// 		return taskengine.StatusError, nil, err
// 	}

// 	return taskengine.StatusSuccess, messages, nil
// }

// // AppendUserInput appends new user input to chat history
// func (h *ChatHook) AppendUserInput(ctx context.Context, input any, hook *taskengine.HookCall) (int, any, error) {
// 	if hook.Args == nil {
// 		return taskengine.StatusError, nil, fmt.Errorf("missing args")
// 	}

// 	// Parse input messages
// 	var messages []serverops.Message
// 	if data, ok := hook.Args["input"]; ok && data != "" {
// 		if err := json.Unmarshal([]byte(data), &messages); err != nil {
// 			return taskengine.StatusError, nil, fmt.Errorf("failed to parse input: %w", err)
// 		}
// 	}

// 	// Append new message
// 	userMsg := serverops.Message{
// 		Role:    "user",
// 		Content: hook.Input,
// 	}

// 	messages = append(messages, userMsg)

// 	return taskengine.StatusSuccess, messages, nil
// }

// // ChatExec runs a chat completion using the current history
// func (h *ChatHook) ChatExec(ctx context.Context, hook *taskengine.HookCall) (int, any, error) {
// 	if hook.Args == nil {
// 		return taskengine.StatusError, nil, fmt.Errorf("missing args")
// 	}

// 	// Parse input messages
// 	var messages []serverops.Message
// 	if data, ok := hook.Args["input"]; ok && data != "" {
// 		if err := json.Unmarshal([]byte(data), &messages); err != nil {
// 			return taskengine.StatusError, nil, fmt.Errorf("failed to parse messages: %w", err)
// 		}
// 	}

// 	// Get session ID
// 	sessionID := hook.Args["session_id"]
// 	if sessionID == "" {
// 		sessionID = hook.Args["subject_id"]
// 	}

// 	if sessionID == "" {
// 		return taskengine.StatusError, nil, fmt.Errorf("missing session ID")
// 	}

// 	// Get preferred model names
// 	modelNames := []string{"default"}
// 	if models, ok := hook.Args["model_names"]; ok && models != "" {
// 		modelNames = strings.Split(models, ",")
// 	}

// 	// Execute chat completion
// 	beginTime := time.Now().UTC()
// 	responseMessage, contextLength, err := h.chatManager.ChatExec(
// 		ctx, messages, len(messages), modelNames...,
// 	)
// 	if err != nil {
// 		return taskengine.StatusError, nil, fmt.Errorf("chat exec failed: %w", err)
// 	}

// 	// Create response wrapper
// 	response := struct {
// 		Response      *serverops.Message `json:"response"`
// 		ContextLength int                `json:"context_length"`
// 	}{
// 		Response:      responseMessage,
// 		ContextLength: contextLength,
// 	}

// 	return taskengine.StatusSuccess, response, nil
// }

// // PersistMessages saves the latest input/output to storage
// func (h *ChatHook) PersistMessages(ctx context.Context, input any, hook *taskengine.HookCall) (int, any, error) {
// 	if hook.Args == nil {
// 		return taskengine.StatusError, nil, fmt.Errorf("missing args")
// 	}

// 	// Parse input messages
// 	var messages []serverops.Message
// 	if data, ok := hook.Args["input"]; ok && data != "" {
// 		if err := json.Unmarshal([]byte(data), &messages); err != nil {
// 			return taskengine.StatusError, nil, fmt.Errorf("failed to parse messages: %w", err)
// 		}
// 	}

// 	// Get last message for input
// 	input := ""
// 	if len(messages) > 0 {
// 		input = messages[len(messages)-1].Content
// 	}

// 	// Get model response
// 	var response *serverops.Message
// 	if data, ok := hook.Args["output"]; ok && data != "" {
// 		if err := json.Unmarshal([]byte(data), &response); err != nil {
// 			return taskengine.StatusError, nil, fmt.Errorf("failed to parse response: %w", err)
// 		}
// 	}

// 	// If no response yet, just save input
// 	if response == nil {
// 		return taskengine.StatusError, nil, fmt.Errorf("no response")
// 	}

// 	// Save to storage
// 	tx := h.dbInstance.WithoutTransaction()
// 	err := h.chatManager.AppendMessages(
// 		ctx, tx, time.Now(), subjectID,
// 		&serverops.Message{
// 			Role:    "user",
// 			Content: input,
// 		},
// 		response,
// 	)

// 	if err != nil {
// 		return taskengine.StatusError, nil, fmt.Errorf("failed to persist messages: %w", err)
// 	}

// 	// Return success with original input
// 	return taskengine.StatusSuccess, input, nil
// }
