package anthropic_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/anthropic"
	"github.com/stretchr/testify/require"
)

type capture struct {
	server  *httptest.Server
	bodies  []map[string]any
	headers []http.Header
}

func newCapture(t *testing.T, status int, response string) *capture {
	t.Helper()
	c := &capture{}
	c.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		if len(raw) > 0 {
			var body map[string]any
			require.NoError(t, json.Unmarshal(raw, &body), "request body must be JSON: %s", raw)
			c.bodies = append(c.bodies, body)
		}
		c.headers = append(c.headers, r.Header.Clone())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(c.server.Close)
	return c
}

func (c *capture) body(t *testing.T) map[string]any {
	t.Helper()
	require.Len(t, c.bodies, 1, "expected exactly one request")
	return c.bodies[0]
}

const endTurnResponse = `{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}]}`

func declaredProvider(t *testing.T, c *capture, model string, declared *modelrepo.ThinkingTypes) modelrepo.Provider {
	t.Helper()
	return anthropic.NewAnthropicProvider("test-key", model, []string{c.server.URL},
		modelrepo.CapabilityConfig{CanChat: true, CanStream: true, CanPrompt: true, CanThink: true, ThinkingTypes: declared},
		c.server.Client(), nil)
}

func chatBody(t *testing.T, c *capture, provider modelrepo.Provider, args ...modelrepo.ChatArgument) map[string]any {
	t.Helper()
	chat, err := provider.GetChatConnection(context.Background(), "")
	require.NoError(t, err)
	_, err = chat.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}}, args...)
	require.NoError(t, err)
	return c.body(t)
}

func thinkingBody(t *testing.T, c *capture, declared *modelrepo.ThinkingTypes, think string) map[string]any {
	t.Helper()
	return chatBody(t, c, declaredProvider(t, c, "claude-declared", declared), modelrepo.WithThink(think))
}
