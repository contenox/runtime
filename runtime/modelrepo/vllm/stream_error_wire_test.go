package vllm_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/vllm"
	"github.com/stretchr/testify/require"
)

func TestUnit_VLLMStreamedErrorFrameWithAVendorCodeFailsTheTurn(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"par\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"error\":{\"message\":\"injected mid-stream error\",\"type\":\"server_error\",\"code\":\"engine_dead\"}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client, err := vllm.NewVLLMStreamClient(context.Background(), server.URL, "m", 4096, 1024, server.Client(), "", false, false, libtracker.NoopTracker{})
	require.NoError(t, err)
	parcels, err := client.Stream(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)

	assembly := modelrepo.NewStreamAssembler("vllm", "m")
	var failure error
	for parcel := range parcels {
		if err := assembly.Consume(parcel); err != nil {
			failure = err
		}
	}
	require.ErrorContains(t, failure, "injected mid-stream error")
	_, err = assembly.Result()
	require.ErrorContains(t, err, "injected mid-stream error")
}
