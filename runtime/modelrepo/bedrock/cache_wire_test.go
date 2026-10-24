package bedrock_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/contenox/runtime/runtime/modelrepo"
)

func cacheMessages() []modelrepo.Message {
	return []modelrepo.Message{
		{Role: "system", Content: "be terse"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}
}

func cacheArgs(hints modelrepo.CacheHints, tools ...modelrepo.Tool) []modelrepo.ChatArgument {
	args := []modelrepo.ChatArgument{modelrepo.WithCacheHints(hints)}
	if len(tools) > 0 {
		args = append(args, modelrepo.WithTools(tools...))
	}
	return args
}

func checkpointIndex(content []any) int {
	for i, block := range content {
		if _, ok := block.(map[string]any)["cachePoint"]; ok {
			return i
		}
	}
	return -1
}

func TestUnit_BedrockCachePoints_FollowTheDeclaredContract(t *testing.T) {
	claude := cacheContract(
		[]string{modelrepo.PromptCacheFieldTools, modelrepo.PromptCacheFieldSystem, modelrepo.PromptCacheFieldMessages},
		[]string{modelrepo.PromptCacheTTLFiveMinutes, modelrepo.PromptCacheTTLOneHour},
	)
	claude.CanChat = true
	transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
	provider := newWireProvider(claudeSonnet45, claude, transport)
	_, err := chatOnce(t, provider, cacheMessages(),
		cacheArgs(modelrepo.CacheHints{StableSystem: true, StableTools: true, StableHistoryLen: 2},
			toolSpec("fs.list", "list", objectSchema()))...)
	require.NoError(t, err)
	body := transport.mustBody(t)

	tools := body["toolConfig"].(map[string]any)["tools"].([]any)
	require.Len(t, tools, 2)
	checkpoint := tools[1].(map[string]any)["cachePoint"].(map[string]any)
	require.Equal(t, "default", checkpoint["type"])
	require.NotContains(t, checkpoint, "ttl", "the vendor's default lifetime is selected by omitting the member")

	system := body["system"].([]any)
	require.Len(t, system, 2)
	require.Contains(t, system[1], "cachePoint")

	messages := body["messages"].([]any)
	require.Equal(t, -1, checkpointIndex(messages[0].(map[string]any)["content"].([]any)))
	require.NotEqual(t, -1, checkpointIndex(messages[1].(map[string]any)["content"].([]any)),
		"the declared stable history ends at the second message")
}

func TestUnit_BedrockCachePoints_FollowTheModelFamilyConstraints(t *testing.T) {
	nova := cacheContract(
		[]string{modelrepo.PromptCacheFieldSystem, modelrepo.PromptCacheFieldMessages},
		[]string{modelrepo.PromptCacheTTLFiveMinutes},
	)
	nova.CanChat = true
	transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
	provider := newWireProvider("us.amazon.nova-pro-v1:0", nova, transport)
	_, err := chatOnce(t, provider, cacheMessages(),
		cacheArgs(modelrepo.CacheHints{StableSystem: true, StableTools: true},
			toolSpec("fs.list", "list", objectSchema()))...)
	require.NoError(t, err)
	body := transport.mustBody(t)
	tools := body["toolConfig"].(map[string]any)["tools"].([]any)
	require.Len(t, tools, 1, "the tool list accepts no checkpoint for this model")
	require.NotContains(t, tools[0], "cachePoint")
	require.Len(t, body["system"].([]any), 2)
}

func TestUnit_BedrockCachePoints_AnUndeclaredModelGetsNone(t *testing.T) {
	transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
	provider := newWireProvider("meta.llama3-70b-instruct-v1:0", modelrepo.CapabilityConfig{CanChat: true}, transport)
	_, err := chatOnce(t, provider, cacheMessages(),
		cacheArgs(modelrepo.CacheHints{StableSystem: true, StableTools: true},
			toolSpec("fs.list", "list", objectSchema()))...)
	require.NoError(t, err)
	body := transport.mustBody(t)
	require.Len(t, body["system"].([]any), 1)
	require.Len(t, body["toolConfig"].(map[string]any)["tools"].([]any), 1)
}

func TestUnit_BedrockCachePoints_LongLifetimeNeedsItsDeclaration(t *testing.T) {
	t.Run("declared", func(t *testing.T) {
		claude := cacheContract([]string{modelrepo.PromptCacheFieldSystem}, []string{modelrepo.PromptCacheTTLFiveMinutes, modelrepo.PromptCacheTTLOneHour})
		claude.CanChat = true
		transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
		provider := newWireProvider(claudeSonnet45, claude, transport)
		_, err := chatOnce(t, provider, cacheMessages(), cacheArgs(modelrepo.CacheHints{StableSystem: true, TTL: time.Hour})...)
		require.NoError(t, err)
		system := transport.mustBody(t)["system"].([]any)
		require.Equal(t, "1h", system[1].(map[string]any)["cachePoint"].(map[string]any)["ttl"])
	})

	t.Run("undeclared", func(t *testing.T) {
		nova := cacheContract([]string{modelrepo.PromptCacheFieldSystem}, []string{modelrepo.PromptCacheTTLFiveMinutes})
		nova.CanChat = true
		transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
		provider := newWireProvider("us.amazon.nova-pro-v1:0", nova, transport)
		_, err := chatOnce(t, provider, cacheMessages(), cacheArgs(modelrepo.CacheHints{StableSystem: true, TTL: time.Hour})...)
		require.ErrorContains(t, err, "does not declare the 1h prompt-cache lifetime")
		require.Zero(t, transport.requests)
	})
}

func TestUnit_BedrockCachePoints_StableHistoryBeyondTheRequestIsRefused(t *testing.T) {
	claude := cacheContract([]string{modelrepo.PromptCacheFieldMessages}, []string{modelrepo.PromptCacheTTLFiveMinutes})
	claude.CanChat = true
	transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
	provider := newWireProvider(claudeSonnet45, claude, transport)
	_, err := chatOnce(t, provider, cacheMessages(), cacheArgs(modelrepo.CacheHints{StableHistoryLen: 5})...)
	require.ErrorContains(t, err, "exceeds the 2 messages sent")
	require.Zero(t, transport.requests)
}

func TestUnit_BedrockCachePoints_NeverChangeModelVisibleContent(t *testing.T) {
	claude := cacheContract(
		[]string{modelrepo.PromptCacheFieldTools, modelrepo.PromptCacheFieldSystem, modelrepo.PromptCacheFieldMessages},
		[]string{modelrepo.PromptCacheTTLFiveMinutes},
	)
	claude.CanChat = true

	plainTransport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
	plainProvider := newWireProvider(claudeSonnet45, claude, plainTransport)
	_, err := chatOnce(t, plainProvider, cacheMessages(), cacheArgs(modelrepo.CacheHints{}, toolSpec("fs.list", "list", objectSchema()))...)
	require.NoError(t, err)

	hintedTransport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
	hintedProvider := newWireProvider(claudeSonnet45, claude, hintedTransport)
	_, err = chatOnce(t, hintedProvider, cacheMessages(),
		cacheArgs(modelrepo.CacheHints{StableSystem: true, StableTools: true, StableHistoryLen: 2}, toolSpec("fs.list", "list", objectSchema()))...)
	require.NoError(t, err)

	plain := plainTransport.mustBody(t)
	hinted := stripCheckpoints(t, hintedTransport.mustBody(t))
	require.Equal(t, plain, hinted)
}

func stripCheckpoints(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	strip := func(blocks []any) []any {
		kept := make([]any, 0, len(blocks))
		for _, block := range blocks {
			if _, ok := block.(map[string]any)["cachePoint"]; ok {
				continue
			}
			kept = append(kept, block)
		}
		return kept
	}
	if system, ok := decoded["system"].([]any); ok {
		decoded["system"] = strip(system)
	}
	if messages, ok := decoded["messages"].([]any); ok {
		for _, message := range messages {
			entry := message.(map[string]any)
			entry["content"] = strip(entry["content"].([]any))
		}
	}
	if config, ok := decoded["toolConfig"].(map[string]any); ok {
		config["tools"] = strip(config["tools"].([]any))
	}
	return decoded
}
