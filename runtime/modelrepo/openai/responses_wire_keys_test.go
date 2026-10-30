package openai

import (
	"encoding/json"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/stretchr/testify/require"
)

// walkKeys fails the test when a forbidden key appears anywhere in v or when
// any object value is null.
func walkKeys(t *testing.T, path string, v any) {
	t.Helper()
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			require.NotEqual(t, "refusal", k, "unexpected refusal key at %s", path)
			require.NotNil(t, val, "null value for %s.%s", path, k)
			walkKeys(t, path+"."+k, val)
		}
	case []any:
		for i, val := range x {
			walkKeys(t, path+"[]", val)
			_ = i
		}
	}
}

func TestUnit_ResponsesRequestHasNoRefusalOrNullKeys(t *testing.T) {
	msgs := []modelrepo.Message{
		{Role: "system", Content: "be brief"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
		{Role: "user", Content: "classify this"},
	}
	for name, c := range map[string]*openAIClient{
		"codex":  {codex: &codexCatalog{}, modelName: "gpt-5.6-luna"},
		"openai": {modelName: "gpt-5.6-luna"},
	} {
		t.Run(name, func(t *testing.T) {
			req, _, err := c.buildResponsesRequest(msgs, nil)
			require.NoError(t, err)
			req.Stream = true
			raw, err := json.Marshal(req)
			require.NoError(t, err)
			var generic any
			require.NoError(t, json.Unmarshal(raw, &generic))
			walkKeys(t, "$", generic)
			if name == "codex" {
				input := generic.(map[string]any)["input"].([]any)
				for _, it := range input {
					parts := it.(map[string]any)["content"].([]any)
					for _, p := range parts {
						require.Len(t, p.(map[string]any), 2, "content part %v", p)
					}
				}
			}
		})
	}
}
