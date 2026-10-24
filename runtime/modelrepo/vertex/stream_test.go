package vertex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/stretchr/testify/require"
)

func TestUnit_VertexStreamClient_Stream(t *testing.T) {
	t.Parallel()

	chunks := []vertexResponse{
		{Candidates: []struct {
			Content      vertexContent `json:"content"`
			FinishReason string        `json:"finishReason,omitempty"`
		}{
			{Content: vertexContent{Parts: []vertexPart{{Text: "hello "}}}},
		}},
		{Candidates: []struct {
			Content      vertexContent `json:"content"`
			FinishReason string        `json:"finishReason,omitempty"`
		}{
			{Content: vertexContent{Parts: []vertexPart{{Text: "world"}}}},
		}},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.True(t, strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "))
		require.True(t, strings.HasSuffix(r.URL.Path, ":streamGenerateContent"))

		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range chunks {
			b, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", string(b))
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	client := &vertexStreamClient{
		vertexClient: vertexClient{
			baseURL:       srv.URL + "/v1/projects/test/locations/us-central1",
			publisher:     "google",
			modelName:     "gemini-flash-latest",
			contextLength: 0,
			httpClient: &http.Client{
				Transport: bearerInjectTransport{
					serverURL: srv.URL,
					token:     "fake-adc-token",
				},
			},
			tracker: libtracker.NoopTracker{},
			tokenFn: func(_ context.Context) (string, error) { return "fake-adc-token", nil },
		},
	}

	stream, err := client.Stream(context.Background(), []modelrepo.Message{
		{Role: "user", Content: "hello"},
	})
	require.NoError(t, err)

	var texts []string
	for parcel := range stream {
		require.NoError(t, parcel.Error)
		if parcel.Data != "" {
			texts = append(texts, parcel.Data)
		}
	}

	require.Equal(t, []string{"hello ", "world"}, texts)
}

func TestUnit_VertexStreamClient_GoldenFixture_ToolCallsUsageTerminal(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.True(t, strings.HasSuffix(r.URL.Path, ":streamGenerateContent"))
		w.Header().Set("Content-Type", "text/event-stream")
		lines := []string{
			`{"candidates":[{"content":{"parts":[{"text":"pondering","thought":true}]}}]}`,
			`{"candidates":[{"content":{"parts":[{"text":"let me "}]}}]}`,
			`{"candidates":[{"content":{"parts":[{"text":"check"}]}}]}`,
			`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"get_weather","args":{"city":"Berlin"}},"thoughtSignature":"sig-1"},{"functionCall":{"name":"get_time","args":{"tz":"CET"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":15,"candidatesTokenCount":9,"totalTokenCount":24}}`,
		}
		for _, l := range lines {
			fmt.Fprintf(w, "data: %s\n\n", l)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	client := &vertexStreamClient{
		vertexClient: vertexClient{
			baseURL:    srv.URL + "/v1/projects/test/locations/us-central1",
			publisher:  "google",
			modelName:  "gemini-flash-latest",
			httpClient: srv.Client(),
			tracker:    libtracker.NoopTracker{},
			tokenFn:    func(_ context.Context) (string, error) { return "fake-adc-token", nil },
		},
	}

	stream, err := client.Stream(context.Background(), []modelrepo.Message{
		{Role: "user", Content: "weather?"},
	})
	require.NoError(t, err)

	asm := modelrepo.NewStreamAssembler("vertex-google", "gemini-flash-latest")
	for parcel := range stream {
		require.NoError(t, asm.Consume(parcel))
	}
	res, err := asm.Result()
	require.NoError(t, err)

	require.Equal(t, "let me check", res.Content)
	require.Equal(t, "pondering", res.Thinking)
	require.Len(t, res.ToolCalls, 2)
	require.Equal(t, "get_weather", res.ToolCalls[0].Function.Name)
	require.JSONEq(t, `{"city":"Berlin"}`, res.ToolCalls[0].Function.Arguments)
	require.Equal(t, "sig-1", res.ToolCalls[0].ProviderMeta["thought_signature"])
	require.Equal(t, "get_time", res.ToolCalls[1].Function.Name)
	require.Nil(t, res.ToolCalls[1].ProviderMeta,
		"the vendor returns a signature on the first call of a parallel group only, so it is not copied onto the rest")
	require.NotNil(t, res.Continuation, "the streamed turn is kept verbatim so its thought signatures replay inside their own parts")
	require.Equal(t, "STOP", res.FinishReason)
	require.NotNil(t, res.Usage)
	require.Equal(t, 15, res.Usage.PromptTokens)
	require.Equal(t, 9, res.Usage.CompletionTokens)
	require.Equal(t, 24, res.Usage.TotalTokens)
}
