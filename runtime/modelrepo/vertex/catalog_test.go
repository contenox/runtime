package vertex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/stretchr/testify/require"
)

func TestUnit_GoogleCatalog_ListModels(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.NotEmpty(t, r.Header.Get("Authorization"), "expected ADC bearer token")
		require.Empty(t, r.Header.Get("x-goog-user-project"),
			"an ambient credential names its own quota project in the ADC setup; the endpoint's project segment does not override it")
		require.Equal(t, "/v1beta1/publishers/google/models", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"publisherModels": []map[string]any{
				{"name": "publishers/google/models/gemini-flash-latest"},
				{"name": "publishers/google/models/gemini-2.5-pro"},
			},
		})
	}))
	defer server.Close()

	catalog := &googleCatalogProvider{
		spec: modelrepo.BackendSpec{
			Type:    "vertex-google",
			BaseURL: "https://us-central1-aiplatform.googleapis.com/v1/projects/test-project/locations/us-central1",
		},
		tokenFn: func(_ context.Context) (string, error) { return "fake-token", nil },
		httpClient: &http.Client{
			Transport: bearerInjectTransport{
				inner:     server.Client().Transport,
				serverURL: server.URL,
				token:     "fake-token",
			},
		},
	}

	models, err := catalog.ListModels(context.Background())
	require.NoError(t, err)
	require.Len(t, models, 2)

	flash := models[0]
	require.Equal(t, "gemini-flash-latest", flash.Name)
	require.True(t, flash.CanChat)
	require.True(t, flash.CanPrompt)
	require.True(t, flash.CanStream)

	pro := models[1]
	require.Equal(t, "gemini-2.5-pro", pro.Name)
	require.True(t, pro.CanChat)
	require.False(t, pro.CanEmbed)
	require.True(t, pro.CanThink)
	require.NotNil(t, pro.ThinkingTypes)
	require.True(t, pro.ThinkingTypes.Enabled)
	require.False(t, pro.ThinkingTypes.Disabled)
	require.Equal(t, 1048576, pro.ContextLength)
	require.Equal(t, 65536, pro.MaxOutputTokens)

	provider := catalog.ProviderFor(flash)
	require.Equal(t, "vertex-google", provider.GetType())
	require.Equal(t, "gemini-flash-latest", provider.ModelName())
	require.False(t, provider.CanThink(),
		"a rolling alias the vendor's model pages do not describe declares nothing rather than being guessed from its name")
	require.Equal(t, 0, provider.GetMaxOutputTokens(),
		"a family the vendor's pages do not cover declares no output ceiling, so no clamp is applied")
	require.Equal(t, 0, provider.GetContextLength())
}

type bearerInjectTransport struct {
	inner     http.RoundTripper
	serverURL string
	token     string
}

func (t bearerInjectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.Header.Set("Authorization", "Bearer "+t.token)
	cloned.URL.Scheme = "http"
	cloned.URL.Host = t.serverURL[len("http://"):]
	if t.inner != nil {
		return t.inner.RoundTrip(cloned)
	}
	return http.DefaultTransport.RoundTrip(cloned)
}
