package bedrock_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/contenox/runtime/runtime/modelrepo"
)

const (
	effortAutoInChat  = "`/default inference.reasoning.effort auto`"
	effortAutoOnCLI   = "`contenox-cli config set inference.reasoning.effort auto`"
	effortLevelInChat = "`/default inference.reasoning.effort <level>`"
	effortLevelOnCLI  = "`contenox-cli config set inference.reasoning.effort <level>`"
	novaMicro         = "eu.amazon.nova-micro-v1:0"
)

func TestUnit_BedrockThinkingRefusals_NameTheControlAndTheCommandsThatSetIt(t *testing.T) {
	noDisable := adaptiveCapabilities("high")
	noDisable.ThinkingTypes.Disabled = false
	for _, tc := range []struct {
		name         string
		capabilities modelrepo.CapabilityConfig
		level        string
		cause        string
		chat, cli    string
	}{
		{"no declared controls refuses off", modelrepo.CapabilityConfig{CanChat: true}, "off", "has no declared thinking controls, so think=off cannot be expressed", effortAutoInChat, effortAutoOnCLI},
		{"no declared controls refuses a level", modelrepo.CapabilityConfig{CanChat: true}, "high", "has no declared thinking controls, so think=high cannot be expressed", effortAutoInChat, effortAutoOnCLI},
		{"declared controls without a disabled control refuse off", noDisable, "off", "do not include a disabled control", effortLevelInChat, effortLevelOnCLI},
		{"an effort the declaration does not name is refused", declared("low", "medium", "high"), "xhigh", "does not declare the xhigh effort level", effortLevelInChat, effortLevelOnCLI},
		{"a declaration with no control to turn on is refused", modelrepo.CapabilityConfig{CanChat: true, CanThink: true, ThinkingTypes: &modelrepo.ThinkingTypes{}}, "high", "declares no thinking control a request can turn on", effortLevelInChat, effortLevelOnCLI},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
			provider := newWireProvider(claudeSonnet45, tc.capabilities, transport)
			_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}}, thinkArg(tc.level))
			require.ErrorContains(t, err, tc.cause)
			require.ErrorContains(t, err, tc.chat, "the refusal names the command that sets the control in the chat")
			require.ErrorContains(t, err, tc.cli, "and on the command line")
			require.ErrorContains(t, err, "then start a new Contenox invocation", "and when the setting takes effect")
			require.NotContains(t, err.Error(), "connection facts", "the refusal no longer points at a declaration no command makes")
			require.Zero(t, transport.requests, "a refused level leaves before any request does")
		})
	}
}

func TestUnit_BedrockThinkingAuto_SendsNoThinkingControlForAModelThatDeclaresNone(t *testing.T) {
	transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
	provider := newWireProvider(novaMicro, modelrepo.CapabilityConfig{CanChat: true}, transport)

	_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}}, thinkArg("auto"), modelrepo.WithMaxTokens(1024), modelrepo.WithTemperature(0))

	require.NoError(t, err)
	require.Equal(t, 1, transport.requests)
	_, body := transport.request()
	require.NotContains(t, body, "additionalModelRequestFields", "auto states nothing about thinking")
	require.Equal(t, float64(1024), inferenceConfig(t, body)["maxTokens"])
}

func TestUnit_BedrockRegistersWhyAReasoningDeclarationChangesNothing(t *testing.T) {
	reason, refused := modelrepo.ThinkDeclarationRefusal("Bedrock")

	require.True(t, refused, "the adapter registers that it acts on no reasoning declaration")
	require.Contains(t, reason, "a reasoning declaration declares none")
	require.Contains(t, reason, effortAutoInChat)
	require.Contains(t, reason, effortAutoOnCLI)
}
