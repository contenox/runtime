package vertex_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/vertex"
	"github.com/stretchr/testify/require"
)

func TestUnit_VertexGeminiEmbedding2RetrievalWire(t *testing.T) {
	for _, test := range []struct {
		purpose modelrepo.EmbeddingPurpose
		text    string
	}{
		{modelrepo.EmbeddingDocument, "title: none | text: sample"},
		{modelrepo.EmbeddingQuery, "task: search result | query: sample"},
	} {
		t.Run(string(test.purpose), func(t *testing.T) {
			var request map[string]any
			var requestPath string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestPath = r.URL.Path
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "fixture-key", r.Header.Get("X-Goog-Api-Key"))
				require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
				w.Header().Set("Content-Type", "application/json")
				_, err := w.Write([]byte(`{"embedding":{"values":[0.25,-0.5,0.75]}}`))
				require.NoError(t, err)
			}))
			defer server.Close()
			provider := vertex.NewVertexProvider("google", "gemini-embedding-2", []string{server.URL + "/v1/projects/test/locations/global"}, modelrepo.CapabilityConfig{CanEmbed: true}, "fixture-key", server.Client(), libtracker.NoopTracker{})
			client, err := provider.GetEmbedConnection(context.Background(), "")
			require.NoError(t, err)
			values, err := client.Embed(modelrepo.WithEmbeddingPurpose(context.Background(), test.purpose), "sample")
			require.NoError(t, err)
			require.Equal(t, []float64{0.25, -0.5, 0.75}, values)
			require.Equal(t, "/v1/projects/test/locations/global/publishers/google/models/gemini-embedding-2:embedContent", requestPath)
			require.Equal(t, map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": test.text}}}}, request)
		})
	}
}

func TestUnit_VertexGeminiEmbedding2Refusals(t *testing.T) {
	for _, test := range []struct {
		name, body, refusal string
		status              int
		purpose             modelrepo.EmbeddingPurpose
		requests            int
	}{
		{"unknown-purpose", "", "has no documented instruction", 200, "unknown", 0},
		{"empty-vector", `{"embedding":{"values":[]}}`, "returned no embedding values", 200, modelrepo.EmbeddingDocument, 1},
		{"malformed", `{`, "failed to decode response", 200, modelrepo.EmbeddingDocument, 1},
		{"upstream-refusal", `{"error":{"status":"INVALID_ARGUMENT","message":"unsupported input"}}`, "unsupported input", 400, modelrepo.EmbeddingDocument, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.WriteHeader(test.status)
				_, err := w.Write([]byte(test.body))
				require.NoError(t, err)
			}))
			defer server.Close()
			provider := vertex.NewVertexProvider("google", "gemini-embedding-2", []string{server.URL}, modelrepo.CapabilityConfig{CanEmbed: true}, "fixture-key", server.Client(), libtracker.NoopTracker{})
			client, err := provider.GetEmbedConnection(context.Background(), "")
			require.NoError(t, err)
			values, err := client.Embed(modelrepo.WithEmbeddingPurpose(context.Background(), test.purpose), "sample")
			require.ErrorContains(t, err, test.refusal)
			require.Nil(t, values)
			require.Equal(t, test.requests, requests)
		})
	}
}
