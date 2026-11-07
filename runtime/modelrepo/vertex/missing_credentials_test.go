package vertex

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/stretchr/testify/require"
)

const googleADCLibraryText = "google: could not find default credentials. See https://cloud.google.com/docs/authentication/external/set-up-adc for more information"

func TestUnit_VertexMissingCredentials_ListModelsTellsThePersonWhatToDo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("no request may reach Vertex without credentials")
	}))
	defer server.Close()

	catalog := &googleCatalogProvider{
		spec: modelrepo.BackendSpec{
			Type:    "vertex-google",
			BaseURL: "https://us-central1-aiplatform.googleapis.com/v1/projects/secret-project-123/locations/us-central1",
		},
		tokenFn: func(context.Context) (string, error) {
			return "", &MissingCredentialsError{cause: errors.New(googleADCLibraryText)}
		},
		httpClient: server.Client(),
	}

	_, err := catalog.ListModels(context.Background())
	require.Error(t, err)
	require.Equal(t, MissingCredentialsMessage, err.Error())
	require.True(t, errors.Is(err, modelrepo.ErrCredentialsMissing))
	require.NotContains(t, err.Error(), "could not find default credentials")
	require.NotContains(t, err.Error(), "secret-project-123")
	require.Contains(t, err.Error(), "gcloud auth application-default login")
	require.Contains(t, err.Error(), "service account key JSON")
}

func TestUnit_VertexMissingCredentials_OnlyTheLibraryTextIsMapped(t *testing.T) {
	require.True(t, isMissingDefaultCredentials(errors.New(googleADCLibraryText)))
	require.False(t, isMissingDefaultCredentials(errors.New("oauth2: token expired")))
	require.False(t, isMissingDefaultCredentials(nil))

	wrapped := &MissingCredentialsError{cause: errors.New(googleADCLibraryText)}
	require.Equal(t, googleADCLibraryText, errors.Unwrap(wrapped).Error())
}
