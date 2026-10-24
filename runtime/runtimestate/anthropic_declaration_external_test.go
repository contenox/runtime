package runtimestate_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	libbus "github.com/contenox/runtime/libbus"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

const haikuModelsListing = `{"data":[{"type":"model","id":"claude-haiku-5-5","display_name":"Claude Haiku 5.5",
"created_at":"2026-10-07T18:00:00Z","max_input_tokens":1000000,"max_tokens":128000,
"capabilities":{"thinking":{"supported":true,"types":{"enabled":{"supported":false},"adaptive":{"supported":true},"disabled":{"supported":true}}},
"effort":{"supported":true,"low":{"supported":true},"medium":{"supported":true},"high":{"supported":true},"xhigh":{"supported":true},"max":{"supported":true}},
"image_input":{"supported":true},"structured_outputs":{"supported":true}}}],"has_more":false,"first_id":"claude-haiku-5-5","last_id":"claude-haiku-5-5"}`

func TestUnit_RunBackendCycle_AnthropicDeclarationReachesTheChatRequest(t *testing.T) {
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "runtime.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	store := runtimetypes.New(db.WithoutTransaction())

	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			_, _ = io.WriteString(w, haikuModelsListing)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/messages":
			raw, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(raw, &gotBody))
			_, _ = io.WriteString(w, `{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":12,"output_tokens":1}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	require.NoError(t, store.CreateBackend(ctx, &runtimetypes.Backend{ID: "anthropic-backend", Name: "anthropic", Type: "anthropic", BaseURL: server.URL}))
	config, err := json.Marshal(runtimestate.ProviderConfig{APIKey: "test-key", Type: "anthropic"})
	require.NoError(t, err)
	require.NoError(t, store.SetKV(ctx, runtimestate.BackendCredentialKey("anthropic", "anthropic-backend"), config))
	require.NoError(t, store.AppendModel(ctx, &runtimetypes.Model{ID: "declared-haiku", Model: "claude-haiku-5-5", CanChat: true, CanPrompt: true, CanStream: true}))

	bus := libbus.NewSQLite(db.WithoutTransaction())
	defer bus.Close()
	state, err := runtimestate.New(ctx, db, bus)
	require.NoError(t, err)
	require.NoError(t, state.RunBackendCycle(ctx))
	runtime := state.Get(ctx)
	require.Empty(t, runtime["anthropic-backend"].Error)
	require.Len(t, runtime["anthropic-backend"].PulledModels, 1)
	pulled := runtime["anthropic-backend"].PulledModels[0]
	require.NotNil(t, pulled.ThinkingTypes, "the pulled model must carry the declaration the catalogue read")
	require.True(t, pulled.ThinkingTypes.Adaptive)
	require.False(t, pulled.ThinkingTypes.Enabled)

	providers, err := runtimestate.LocalProviderAdapter(ctx, libtracker.NoopTracker{}, runtime)(ctx, "anthropic")
	require.NoError(t, err)
	require.Len(t, providers, 1)
	chat, err := providers[0].GetChatConnection(ctx, "")
	require.NoError(t, err)
	_, err = chat.Chat(ctx, []modelrepo.Message{{Role: "user", Content: "reply with the single word ok"}},
		modelrepo.WithThink("high"), modelrepo.WithMaxTokens(64), modelrepo.WithTemperature(0.2))
	require.NoError(t, err)

	thinking, ok := gotBody["thinking"].(map[string]any)
	require.True(t, ok, "request must carry the thinking member: %v", gotBody)
	require.Equal(t, "adaptive", thinking["type"], "a model that rejects a caller-set budget is sent the adaptive control")
	require.NotContains(t, thinking, "budget_tokens")
	output, ok := gotBody["output_config"].(map[string]any)
	require.True(t, ok, "request must carry output_config: %v", gotBody)
	require.Equal(t, "high", output["effort"])
	require.NotContains(t, gotBody, "temperature", "an adaptive-only model is sent no sampling parameter")
}
