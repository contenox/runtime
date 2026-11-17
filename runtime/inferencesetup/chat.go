package inferencesetup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/google/uuid"
)

// ChatOperation executes a chat request against one selected connection and model.
type ChatOperation func(context.Context, []modelrepo.Message, ...modelrepo.ChatArgument) (modelrepo.ChatResult, error)

// VerifyToolUse exercises a structured tool call and consumption of its result through production chat.
func VerifyToolUse(ctx context.Context, chat ChatOperation) error {
	if ctx == nil || chat == nil {
		return errors.New("tool-use verification requires a context and chat operation")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	probe := modelrepo.Tool{Type: "function", Function: &modelrepo.FunctionTool{
		Name: "contenox_ready", Description: "Verify AI setup and return a confirmation code.",
		Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"status": map[string]any{"type": "string", "enum": []string{"ok"}},
			}, "required": []string{"status"}, "additionalProperties": false,
		},
	}}
	messages := []modelrepo.Message{{Role: "user", Content: "Call the contenox_ready tool once with status ok. After receiving its result, reply with only the confirmation code returned by the tool."}}
	args := []modelrepo.ChatArgument{modelrepo.WithMaxTokens(1024), modelrepo.WithThink("off"), modelrepo.WithTemperature(0), modelrepo.WithTool(probe)}
	result, err := chat(ctx, messages, args...)
	if err != nil {
		return fmt.Errorf("tool call failed: %w", err)
	}
	if len(result.ToolCalls) != 1 {
		return errors.New("model did not produce the expected structured tool call; select a model and connection with tool-use support")
	}
	call := result.ToolCalls[0]
	if call.ID == "" || call.Type != "function" || call.Function.Name != "contenox_ready" {
		return errors.New("model returned an invalid setup tool call")
	}
	var arguments map[string]any
	if err := json.Unmarshal([]byte(call.Function.Arguments), &arguments); err != nil {
		return fmt.Errorf("model returned invalid setup tool arguments: %w", err)
	}
	if len(arguments) != 1 || arguments["status"] != "ok" {
		return errors.New("model returned incorrect setup tool arguments")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	confirmation := uuid.NewString()
	result.Message.Role, result.Message.ToolCalls = "assistant", result.ToolCalls
	messages = append(messages, result.Message, modelrepo.Message{Role: "tool", ToolCallID: call.ID, Content: confirmation})
	answer, err := chat(ctx, messages, args...)
	if err != nil {
		return fmt.Errorf("tool-result continuation failed: %w", err)
	}
	if len(answer.ToolCalls) != 0 || strings.TrimSpace(answer.Message.Content) != confirmation {
		return errors.New("model did not consume the setup tool result correctly; select another model or repair its tool-use configuration")
	}
	return ctx.Err()
}
