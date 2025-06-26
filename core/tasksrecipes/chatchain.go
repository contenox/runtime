package tasksrecipes

import "github.com/contenox/runtime-mvp/core/taskengine"

func BuildOpenAIChatChain(model string, llmProvider string) *taskengine.ChainDefinition {
	return &taskengine.ChainDefinition{
		ID:          "openai_chat_chain",
		Description: "OpenAI Style chat processing pipeline with hooks",
		Tasks: []taskengine.ChainTask{
			{
				ID:          "convert_openai_to_history",
				Description: "Convert OpenAI request to internal history",
				Type:        taskengine.Hook,
				Hook: &taskengine.HookCall{
					Type: "convert_openai_to_history",
					Args: map[string]string{},
				},
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: "default", Goto: "execute_model_on_messages"},
					},
				},
			},
			{
				ID:              "execute_model_on_messages",
				Description:     "Run inference using selected LLM",
				Type:            taskengine.Hook,
				PreferredModels: []string{model},
				LLMProvider:     llmProvider,
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: "default", Goto: "convert_history_to_openai"},
					},
				},
				Hook: &taskengine.HookCall{
					Type: "execute_model_on_messages",
					Args: map[string]string{},
				},
			},
			{
				ID:          "convert_history_to_openai",
				Description: "Convert chat history to OpenAI response",
				Type:        taskengine.Hook,
				Hook: &taskengine.HookCall{
					Type: "convert_history_to_openai",
					Args: map[string]string{
						"model": model,
					},
				},
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: "default", Goto: taskengine.TermEnd},
					},
				},
			},
		},
	}
}

func BuildChatChain(subjectID string, preferredModelNames ...string) *taskengine.ChainDefinition {
	return &taskengine.ChainDefinition{
		ID:          "chat_chain",
		Description: "Standard chat processing pipeline with hooks",
		Tasks: []taskengine.ChainTask{
			{
				ID:          "append_user_message",
				Description: "Append user message to chat history",
				Type:        taskengine.Hook,
				Hook: &taskengine.HookCall{
					Type: "append_user_message",
					Args: map[string]string{
						"subject_id": subjectID,
					},
				},
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: "default", Goto: "mux_input"},
					},
				},
			},
			{
				ID:          "mux_input",
				Description: "Check for commands like /echo using Mux",
				Type:        taskengine.Hook,
				Hook: &taskengine.HookCall{
					Type: "command_router",
					Args: map[string]string{
						"subject_id": subjectID,
					},
				},
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: "default", Goto: "execute_model_on_messages"},
						{
							Operator: "equals",
							When:     "echo",
							Goto:     "persist_input_output",
						},
					},
				},
			},
			{
				ID:              "execute_model_on_messages",
				Description:     "Run inference using selected LLM",
				Type:            taskengine.Hook,
				PreferredModels: preferredModelNames,
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: "default", Goto: "persist_input_output"},
					},
				},
				Hook: &taskengine.HookCall{
					Type: "execute_model_on_messages",
					Args: map[string]string{
						"subject_id": subjectID,
					},
				},
			},
			{
				ID:          "persist_input_output",
				Description: "Persist the conversation",
				Type:        taskengine.Hook,
				Hook: &taskengine.HookCall{
					Type: "persist_input_output",
					Args: map[string]string{
						"subject_id": subjectID,
					},
				},
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: "default", Goto: taskengine.TermEnd},
					},
				},
			},
		},
	}
}

func BuildAppendInstruction(subjectID string) *taskengine.ChainDefinition {
	return &taskengine.ChainDefinition{
		Tasks: []taskengine.ChainTask{
			{
				ID:          "append_system_message",
				Description: "Append instruction message to chat history",
				Type:        taskengine.Hook,
				Hook: &taskengine.HookCall{
					Type: "append_system_message",
					Args: map[string]string{
						"subject_id": subjectID,
					},
				},
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: "default", Goto: taskengine.TermEnd},
					},
				},
			},
		},
	}
}
