package openai_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/contenox/runtime/runtime/internal/models/copilotauth"
	"github.com/contenox/runtime/runtime/modelrepo"
	_ "github.com/contenox/runtime/runtime/modelrepo/openai"
	"github.com/stretchr/testify/require"
)

func copilotFixture(t *testing.T, handler http.HandlerFunc) modelrepo.CatalogProvider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	catalog, err := modelrepo.NewCatalogProvider(
		modelrepo.BackendSpec{Type: copilotauth.ProviderType, BaseURL: server.URL},
		modelrepo.WithCatalogHTTPClient(server.Client()),
		modelrepo.WithCatalogAuthorizer(func(context.Context) (http.Header, error) {
			return http.Header{"Authorization": {"Bearer cop-token"}, "Copilot-Integration-Id": {"vscode-chat"}}, nil
		}),
	)
	require.NoError(t, err)
	return catalog
}

func TestUnit_CopilotCatalogNeedsSignIn(t *testing.T) {
	_, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: copilotauth.ProviderType})
	require.ErrorIs(t, err, copilotauth.ErrLoginRequired)
}

func TestUnit_CopilotCatalogListsPickerChatModels(t *testing.T) {
	c := copilotFixture(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/models", r.URL.Path)
		require.Equal(t, "Bearer cop-token", r.Header.Get("Authorization"))
		require.Equal(t, "vscode-chat", r.Header.Get("Copilot-Integration-Id"))
		fmt.Fprint(w, `{"data":[
			{"id":"gpt-5","model_picker_enabled":true,"capabilities":{"type":"chat","limits":{"max_context_window_tokens":128000,"max_output_tokens":16000},"supports":{"tool_calls":true,"vision":true}}},
			{"id":"gpt-5","model_picker_enabled":true,"capabilities":{"type":"chat"}},
			{"id":"text-embedding-3-small","model_picker_enabled":false,"capabilities":{"type":"embeddings"}},
			{"id":"hidden","model_picker_enabled":false,"capabilities":{"type":"chat"}},
			{"id":"blocked","model_picker_enabled":true,"capabilities":{"type":"chat"},"policy":{"state":"disabled"}}
		]}`)
	})
	models, err := c.ListModels(context.Background())
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Equal(t, "gpt-5", models[0].Name)
	require.Equal(t, 128000, models[0].ContextLength)
	p := c.ProviderFor(models[0])
	require.Equal(t, copilotauth.ProviderType, p.GetType())
	require.True(t, p.CanChat())
	require.True(t, p.CanVision())
	require.False(t, p.CanEmbed())
}

func TestUnit_CopilotCatalogSignedOut(t *testing.T) {
	c := copilotFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	_, err := c.ListModels(context.Background())
	require.ErrorIs(t, err, copilotauth.ErrLoginRequired)
}

func TestUnit_CopilotChatUsesChatCompletionsWithSignInHeaders(t *testing.T) {
	c := copilotFixture(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/chat/completions", r.URL.Path)
		require.Equal(t, "Bearer cop-token", r.Header.Get("Authorization"))
		require.Equal(t, "vscode-chat", r.Header.Get("Copilot-Integration-Id"))
		body, _ := io.ReadAll(r.Body)
		require.True(t, strings.Contains(string(body), `"model":"gpt-5"`))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"c1","object":"chat.completion","model":"gpt-5","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`)
	})
	p := c.ProviderFor(modelrepo.ObservedModel{Name: "gpt-5", CapabilityConfig: modelrepo.CapabilityConfig{CanChat: true, CanStream: true}})
	client, err := p.GetChatConnection(context.Background(), "")
	require.NoError(t, err)
	res, err := client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)
	require.Equal(t, "hello", res.Message.Content)
}
