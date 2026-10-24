package bedrock_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/bedrock"
	"github.com/stretchr/testify/require"
)

type invokeTransport struct {
	requestBody []byte
	requestPath string
}

func (t *invokeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.requestBody, _ = io.ReadAll(request.Body)
	t.requestPath = request.URL.Path
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"outputs":[{"text":"return 42","stop_reason":"stop"}]}`)),
		Request:    request,
	}, nil
}

func TestUnit_BedrockMistralPromptUsesModelSpecificInvokeOperation(t *testing.T) {
	transport := &invokeTransport{}
	client := &http.Client{Transport: transport}
	provider := bedrock.NewBedrockProvider("us-east-1", `{"access_key_id":"key","secret_access_key":"secret"}`, "mistral.mistral-7b-instruct-v0:2", modelrepo.CapabilityConfig{CanPrompt: true, MaxOutputTokens: 128}, client, nil)
	require.False(t, provider.CanChat())
	require.True(t, provider.CanPrompt())
	connection, err := provider.GetPromptConnection(context.Background(), "bedrock:us-east-1")
	require.NoError(t, err)
	output, _, err := connection.Prompt(context.Background(), "Return code only.", 0, "Write a function.", modelrepo.WithPromptMaxTokens(32))
	require.NoError(t, err)
	require.Equal(t, "return 42", output)
	require.Equal(t, "/model/mistral.mistral-7b-instruct-v0:2/invoke", transport.requestPath)
	var body struct {
		Prompt      string  `json:"prompt"`
		MaxTokens   int     `json:"max_tokens"`
		Temperature float32 `json:"temperature"`
	}
	require.NoError(t, json.Unmarshal(transport.requestBody, &body))
	require.Equal(t, "<s>[INST] Return code only.\n\nWrite a function. [/INST]", body.Prompt)
	require.Equal(t, 32, body.MaxTokens)
	require.Equal(t, float32(0), body.Temperature)
}

func TestUnit_BedrockMistralPromptRejectsOutputLimitAboveModelBound(t *testing.T) {
	provider := bedrock.NewBedrockProvider("us-east-1", `{"access_key_id":"key","secret_access_key":"secret"}`, "mistral.mistral-7b-instruct-v0:2", modelrepo.CapabilityConfig{CanPrompt: true, MaxOutputTokens: 32}, http.DefaultClient, nil)
	connection, err := provider.GetPromptConnection(context.Background(), "bedrock:us-east-1")
	require.NoError(t, err)
	_, _, err = connection.Prompt(context.Background(), "", 0, "Complete.", modelrepo.WithPromptMaxTokens(64))
	require.ErrorContains(t, err, "exceeds the model's declared maximum")
}
