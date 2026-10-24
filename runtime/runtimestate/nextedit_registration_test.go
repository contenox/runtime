package runtimestate_test

import (
	"testing"

	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_RegistrationCapabilitiesClaimNoEditorOperationWithoutADeclaration(t *testing.T) {
	for _, backendType := range []string{"openai", "ollama", "vllm", "mistral", "deepseek", "anthropic", "gemini"} {
		capabilities, err := runtimestate.RegistrationCapabilities(runtimetypes.Backend{Type: backendType})
		require.NoError(t, err)
		require.NotContains(t, capabilities, runtimetypes.CapabilityNextEdit, backendType)
		require.NotContains(t, capabilities, runtimetypes.CapabilityAutocomplete, backendType)
	}
}

func TestUnit_DeclaredModelCapabilitiesHonourTheDeclaredFormats(t *testing.T) {
	for _, backendType := range []string{"openai", "ollama", "vllm"} {
		capabilities, err := runtimestate.DeclaredModelCapabilities(runtimetypes.Backend{Type: backendType}, runtimetypes.ModelFacts{
			Capabilities:   []string{runtimetypes.CapabilityCompletion, runtimetypes.CapabilityNextEdit},
			NextEditFormat: "sweep-v1",
		})
		require.NoError(t, err)
		require.Contains(t, capabilities, runtimetypes.CapabilityNextEdit, backendType)
	}
	for _, backendType := range []string{"mistral", "deepseek", "anthropic", "gemini"} {
		capabilities, err := runtimestate.DeclaredModelCapabilities(runtimetypes.Backend{Type: backendType}, runtimetypes.ModelFacts{
			Capabilities:   []string{runtimetypes.CapabilityCompletion, runtimetypes.CapabilityNextEdit},
			NextEditFormat: "sweep-v1",
		})
		require.NoError(t, err)
		require.NotContains(t, capabilities, runtimetypes.CapabilityNextEdit, backendType)
	}

	openaiFIM, err := runtimestate.DeclaredModelCapabilities(runtimetypes.Backend{Type: "openai"}, runtimetypes.ModelFacts{
		Capabilities:   []string{runtimetypes.CapabilityCompletion, runtimetypes.CapabilityAutocomplete},
		CompletionMode: "fim",
		FIMFormat:      "qwen2.5-coder",
	})
	require.NoError(t, err)
	require.Contains(t, openaiFIM, runtimetypes.CapabilityAutocomplete,
		"an OpenAI-compatible adapter speaks the models' declared FIM contracts")

	deepseek, err := runtimestate.DeclaredModelCapabilities(runtimetypes.Backend{Type: "deepseek"}, runtimetypes.ModelFacts{
		Capabilities:   []string{runtimetypes.CapabilityCompletion, runtimetypes.CapabilityAutocomplete},
		CompletionMode: "fim",
		FIMFormat:      "deepseek-fim",
	})
	require.NoError(t, err)
	require.Contains(t, deepseek, runtimetypes.CapabilityAutocomplete, "the deepseek adapter speaks its own FIM contract")

	mistralOnDeepseek, err := runtimestate.DeclaredModelCapabilities(runtimetypes.Backend{Type: "deepseek"}, runtimetypes.ModelFacts{
		Capabilities:   []string{runtimetypes.CapabilityCompletion, runtimetypes.CapabilityAutocomplete},
		CompletionMode: "fim",
		FIMFormat:      "mistral-fim",
	})
	require.NoError(t, err)
	require.NotContains(t, mistralOnDeepseek, runtimetypes.CapabilityAutocomplete,
		"an adapter that does not speak the declared FIM contract must not claim the operation")
}
