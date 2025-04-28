package libtestenv_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/js402/cate/libs/libtestenv"
	"github.com/ollama/ollama/api"
	"github.com/stretchr/testify/require"
)

func TestStartupOllamaInstance(t *testing.T) {
	ctx := context.TODO()
	uri, _, cleanup, err := libtestenv.SetupOllamaLocalInstance(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanup()
	})
	u, err := url.Parse(uri)
	require.NoError(t, err)
	client := api.NewClient(u, http.DefaultClient)
	err = client.Heartbeat(ctx)
	require.NoError(t, err)
}
