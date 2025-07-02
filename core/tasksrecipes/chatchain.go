package tasksrecipes

import (
	"strings"

	"github.com/contenox/runtime-mvp/core/taskengine"
)

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
				ID:          "execute_model_on_messages",
				Description: "Run inference using selected LLM",
				Type:        taskengine.Hook,
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: "default", Goto: "convert_history_to_openai"},
					},
				},
				Hook: &taskengine.HookCall{
					Type: "execute_model_on_messages",
					Args: map[string]string{
						"model":    model,
						"provider": llmProvider,
					},
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

type BuildChatChainReq struct {
	SubjectID           string
	PreferredModelNames []string
	Provider            string
}

func BuildChatChain(req BuildChatChainReq) *taskengine.ChainDefinition {
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
						"subject_id": req.SubjectID,
					},
				},
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: "default", Goto: "preappend_message_to_history"},
					},
				},
			},
			{
				ID:          "preappend_message_to_history",
				Description: "Add system level instructions to chat history",
				Type:        taskengine.Hook,
				Hook: &taskengine.HookCall{
					Type: "preappend_message_to_history",
					Args: map[string]string{
						"role":    "system",
						"message": "You are a helpful assistant. Part of a larger system named \"contenox\".",
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
						"subject_id": req.SubjectID,
					},
				},
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: "default", Goto: "execute_model_on_messages"},
						{
							Operator: "equals",
							When:     "echo",
							Goto:     "persist_messages",
						},
					},
				},
			},
			{
				ID:          "execute_model_on_messages",
				Description: "Run inference using selected LLM",
				Type:        taskengine.Hook,
				Transition: taskengine.TaskTransition{
					Branches: []taskengine.TransitionBranch{
						{Operator: "default", Goto: "persist_messages"},
					},
				},
				Hook: &taskengine.HookCall{
					Type: "execute_model_on_messages",
					Args: map[string]string{
						"subject_id": req.SubjectID,
						"models":     strings.Join(req.PreferredModelNames, ","),
						"provider":   req.Provider,
					},
				},
			},
			{
				ID:          "persist_messages",
				Description: "Persist the conversation",
				Type:        taskengine.Hook,
				Hook: &taskengine.HookCall{
					Type: "persist_messages",
					Args: map[string]string{
						"subject_id": req.SubjectID,
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
