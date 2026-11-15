package bedrock_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/stretchr/testify/require"
)

func TestContenoxChatUsesDeviceCredential(t *testing.T) {
	transport := &wireTransport{handler: func(r *http.Request, body []byte) *http.Response {
		require.Equal(t, "events.contenox.com", r.URL.Host)
		require.Equal(t, "Bearer subscription-device", r.Header.Get("Authorization"))
		return jsonResponse(r, 200, converseResponse(textBlock("done"), "end_turn", `{"inputTokens":4,"outputTokens":1,"totalTokens":5}`))
	}}
	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "contenox"}, modelrepo.WithCatalogHTTPClient(&http.Client{Transport: transport}), modelrepo.WithCatalogAuthorizer(func(context.Context) (http.Header, error) {
		return http.Header{"Authorization": {"Bearer subscription-device"}}, nil
	}))
	require.NoError(t, err)
	provider := catalog.ProviderFor(modelrepo.ObservedModel{Name: "eu.amazon.nova-2-lite-v1:0", CapabilityConfig: modelrepo.CapabilityConfig{CanChat: true}})
	_, err = chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "Hello"}})
	require.NoError(t, err)
	require.Equal(t, 1, transport.requests)
}
