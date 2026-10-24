package anthropic_test

import (
	"context"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/anthropic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func adaptiveOnly() *modelrepo.ThinkingTypes {
	return &modelrepo.ThinkingTypes{
		Adaptive:     true,
		EffortLevels: []string{"low", "medium", "high", "xhigh", "max"},
	}
}

func reasoningOff() *modelrepo.ThinkingTypes {
	return &modelrepo.ThinkingTypes{Disabled: true}
}

func thinkingOf(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	thinking, ok := body["thinking"].(map[string]any)
	require.True(t, ok, "request must carry a thinking member: %v", body)
	return thinking
}

func effortOf(t *testing.T, body map[string]any) string {
	t.Helper()
	output, ok := body["output_config"].(map[string]any)
	require.True(t, ok, "request must carry an output_config member: %v", body)
	effort, _ := output["effort"].(string)
	return effort
}

func TestUnit_AnthropicThinking_AdaptiveModelUsesAdaptiveAndEffort(t *testing.T) {
	c := newCapture(t, 200, endTurnResponse)
	body := thinkingBody(t, c, adaptiveOnly(), "xhigh")

	thinking := thinkingOf(t, body)
	assert.Equal(t, "adaptive", thinking["type"])
	assert.Equal(t, "summarized", thinking["display"])
	assert.NotContains(t, thinking, "budget_tokens", "adaptive thinking takes no caller-set budget")
	assert.Equal(t, "xhigh", effortOf(t, body))
}

func TestUnit_AnthropicThinking_MaxEffortIsReachable(t *testing.T) {
	c := newCapture(t, 200, endTurnResponse)
	body := thinkingBody(t, c, adaptiveOnly(), "max")

	assert.Equal(t, "max", effortOf(t, body))
	assert.Equal(t, "adaptive", thinkingOf(t, body)["type"])
}

func TestUnit_AnthropicThinking_MinimalMapsToTheLowestEffort(t *testing.T) {
	c := newCapture(t, 200, endTurnResponse)
	body := thinkingBody(t, c, adaptiveOnly(), "minimal")

	assert.Equal(t, "low", effortOf(t, body))
}

func TestUnit_AnthropicThinking_UnsupportedEffortDescendsToTheHighestDeclared(t *testing.T) {
	c := newCapture(t, 200, endTurnResponse)
	body := thinkingBody(t, c, &modelrepo.ThinkingTypes{
		Adaptive:     true,
		EffortLevels: []string{"low", "medium"},
	}, "max")

	assert.Equal(t, "medium", effortOf(t, body))
}

func TestUnit_AnthropicThinking_DeclarationWithoutEffortSendsNoEffort(t *testing.T) {
	c := newCapture(t, 200, endTurnResponse)
	body := thinkingBody(t, c, &modelrepo.ThinkingTypes{Adaptive: true}, "high")

	assert.Equal(t, "adaptive", thinkingOf(t, body)["type"])
	assert.NotContains(t, body, "output_config")
}

func TestUnit_AnthropicThinking_EnabledModelGetsABudgetWithinBounds(t *testing.T) {
	c := newCapture(t, 200, endTurnResponse)
	body := thinkingBody(t, c, &modelrepo.ThinkingTypes{Enabled: true, Disabled: true}, "medium")

	thinking := thinkingOf(t, body)
	assert.Equal(t, "enabled", thinking["type"])
	budget := thinking["budget_tokens"].(float64)
	assert.GreaterOrEqual(t, budget, float64(1024))
	assert.Less(t, budget, body["max_tokens"].(float64))
	assert.NotContains(t, body, "output_config", "a caller-set budget is steered by the budget, not by effort")
}

func TestUnit_AnthropicThinking_UndeclaredModelFallsBackToEnabled(t *testing.T) {
	c := newCapture(t, 200, endTurnResponse)
	body := thinkingBody(t, c, nil, "medium")

	thinking := thinkingOf(t, body)
	assert.Equal(t, "enabled", thinking["type"])
	assert.Equal(t, float64(2048), thinking["budget_tokens"])
}

func TestUnit_AnthropicThinking_OffIsDisabledWhereDeclared(t *testing.T) {
	c := newCapture(t, 200, endTurnResponse)
	body := thinkingBody(t, c, reasoningOff(), "off")

	assert.Equal(t, "disabled", thinkingOf(t, body)["type"])
}

func TestUnit_AnthropicThinking_OffOmitsTheControlWhereDisabledIsRejected(t *testing.T) {
	c := newCapture(t, 200, endTurnResponse)
	body := thinkingBody(t, c, adaptiveOnly(), "off")

	assert.NotContains(t, body, "thinking")
	assert.NotContains(t, body, "output_config")
}

func TestUnit_AnthropicThinking_UndeclaredLevelIsRefusedLocally(t *testing.T) {
	c := newCapture(t, 200, endTurnResponse)
	provider := declaredProvider(t, c, "claude-no-controls", &modelrepo.ThinkingTypes{})
	chat, err := provider.GetChatConnection(context.Background(), "")
	require.NoError(t, err)

	_, err = chat.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}},
		modelrepo.WithThink("high"))
	require.ErrorContains(t, err, "no thinking control")
	assert.Empty(t, c.bodies, "a refused request must not reach the vendor")
}

func TestUnit_AnthropicThinking_NonThinkingModelSendsNothing(t *testing.T) {
	c := newCapture(t, 200, endTurnResponse)
	provider := anthropic.NewAnthropicProvider("test-key", "claude-text-only", []string{c.server.URL},
		modelrepo.CapabilityConfig{CanChat: true}, c.server.Client(), nil)
	body := chatBody(t, c, provider, modelrepo.WithThink("high"))

	assert.NotContains(t, body, "thinking")
	assert.NotContains(t, body, "output_config")
}

func TestUnit_AnthropicThinking_BudgetThatCannotFitIsRefusedLocally(t *testing.T) {
	c := newCapture(t, 200, endTurnResponse)
	provider := anthropic.NewAnthropicProvider("test-key", "claude-budget", []string{c.server.URL},
		modelrepo.CapabilityConfig{CanChat: true, CanThink: true, ThinkingTypes: &modelrepo.ThinkingTypes{Enabled: true, Disabled: true}},
		c.server.Client(), nil)
	chat, err := provider.GetChatConnection(context.Background(), "")
	require.NoError(t, err)

	_, err = chat.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}},
		modelrepo.WithMaxTokens(1024), modelrepo.WithThink("medium"))
	require.ErrorContains(t, err, "max_tokens")
	assert.Empty(t, c.bodies, "a refused request must not reach the vendor")
}

func TestUnit_AnthropicThinking_BudgetFollowsTheClampedCeiling(t *testing.T) {
	c := newCapture(t, 200, endTurnResponse)
	provider := anthropic.NewAnthropicProvider("test-key", "claude-budget", []string{c.server.URL},
		modelrepo.CapabilityConfig{
			CanChat: true, CanThink: true, MaxOutputTokens: 1500,
			ThinkingTypes: &modelrepo.ThinkingTypes{Enabled: true, Disabled: true},
		},
		c.server.Client(), nil)
	body := chatBody(t, c, provider, modelrepo.WithMaxTokens(9000), modelrepo.WithThink("medium"))

	assert.Equal(t, float64(1500), body["max_tokens"])
	assert.Equal(t, float64(1499), thinkingOf(t, body)["budget_tokens"])
}

func TestUnit_AnthropicSampling_StrippedByDeclaration(t *testing.T) {
	t.Run("adaptive-only declaration strips temperature and top_p", func(t *testing.T) {
		c := newCapture(t, 200, endTurnResponse)
		body := chatBody(t, c, declaredProvider(t, c, "claude-x", adaptiveOnly()),
			modelrepo.WithTemperature(0.7), modelrepo.WithTopP(0.9))
		assert.NotContains(t, body, "temperature")
		assert.NotContains(t, body, "top_p")
	})

	t.Run("a model that accepts a budget keeps them", func(t *testing.T) {
		c := newCapture(t, 200, endTurnResponse)
		body := chatBody(t, c, declaredProvider(t, c, "claude-x", &modelrepo.ThinkingTypes{Enabled: true, Disabled: true}),
			modelrepo.WithTemperature(0.7), modelrepo.WithTopP(0.9))
		assert.Equal(t, 0.7, body["temperature"])
		assert.Equal(t, 0.9, body["top_p"])
	})

	t.Run("an undeclared model keeps them", func(t *testing.T) {
		c := newCapture(t, 200, endTurnResponse)
		body := chatBody(t, c, declaredProvider(t, c, "claude-x", nil),
			modelrepo.WithTemperature(0.7))
		assert.Equal(t, 0.7, body["temperature"])
	})
}

func TestUnit_AnthropicSampling_StripPredicate(t *testing.T) {
	assert.True(t, anthropic.AnthropicStripsSamplingParams(adaptiveOnly()))
	assert.False(t, anthropic.AnthropicStripsSamplingParams(&modelrepo.ThinkingTypes{Enabled: true, Disabled: true}))
	assert.False(t, anthropic.AnthropicStripsSamplingParams(&modelrepo.ThinkingTypes{Adaptive: true, Enabled: true}))
	assert.False(t, anthropic.AnthropicStripsSamplingParams(nil))
}

func TestUnit_AnthropicSampling_PromptPathFollowsTheDeclaration(t *testing.T) {
	c := newCapture(t, 200, endTurnResponse)
	provider := declaredProvider(t, c, "claude-x", adaptiveOnly())
	prompt, err := provider.GetPromptConnection(context.Background(), "")
	require.NoError(t, err)

	_, _, err = prompt.Prompt(context.Background(), "be terse", 0.7, "hi")
	require.NoError(t, err)
	assert.NotContains(t, c.body(t), "temperature")
}

func TestUnit_AnthropicChat_StopAndToolChoiceReachTheWire(t *testing.T) {
	c := newCapture(t, 200, endTurnResponse)
	body := chatBody(t, c, declaredProvider(t, c, "claude-x", nil),
		modelrepo.WithStopSequences("###", "STOP"),
		modelrepo.WithTool(modelrepo.Tool{Type: "function", Function: &modelrepo.FunctionTool{Name: "fs.read"}}),
		modelrepo.WithToolChoice(modelrepo.ToolChoice{Mode: modelrepo.ToolChoiceRequired}),
		modelrepo.WithParallelToolCalls(false),
	)

	assert.Equal(t, []any{"###", "STOP"}, body["stop_sequences"])
	choice := body["tool_choice"].(map[string]any)
	assert.Equal(t, "any", choice["type"])
	assert.Equal(t, true, choice["disable_parallel_tool_use"])
}

func TestUnit_AnthropicChat_StructuredOutputAndEffortShareOutputConfig(t *testing.T) {
	c := newCapture(t, 200, endTurnResponse)
	schema := map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}}
	body := chatBody(t, c, declaredProvider(t, c, "claude-x", adaptiveOnly()),
		modelrepo.WithResponseFormat(modelrepo.ResponseFormat{Mode: modelrepo.ResponseFormatJSONSchema, Schema: schema}),
		modelrepo.WithThink("medium"),
	)

	output := body["output_config"].(map[string]any)
	format := output["format"].(map[string]any)
	assert.Equal(t, "json_schema", format["type"])
	assert.Equal(t, schema, format["schema"])
	assert.Equal(t, "medium", output["effort"], "the format must survive the thinking control's effort")
}

func TestUnit_AnthropicChat_TextFormatSendsNoOutputConfig(t *testing.T) {
	c := newCapture(t, 200, endTurnResponse)
	body := chatBody(t, c, declaredProvider(t, c, "claude-x", nil),
		modelrepo.WithResponseFormat(modelrepo.ResponseFormat{Mode: modelrepo.ResponseFormatText}))

	assert.NotContains(t, body, "output_config")
}
