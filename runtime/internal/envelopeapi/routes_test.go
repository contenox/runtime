package envelopeapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/runtime/internal/envelopeapi"
	"github.com/stretchr/testify/require"
)

func TestUnit_EnvelopesList_ReportsTheShippedCatalog(t *testing.T) {
	mux := http.NewServeMux()
	envelopeapi.AddRoutes(mux, "")
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/envelopes")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got []envelopeapi.Envelope
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))

	names := make([]string, 0, len(got))
	for _, envelope := range got {
		require.NotEmpty(t, envelope.Description, "%s carries its description", envelope.Name)
		names = append(names, envelope.Name)
	}
	require.ElementsMatch(t, []string{"read_only", "ask_always", "auto_edit", "default", "strict", "acpx", "oracle"}, names)
}
