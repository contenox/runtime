package bedrock_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/bedrock"
)

type catalogueFixture struct {
	models   []map[string]any
	profiles []map[string]any
	profile  bool
}

func catalogueTransport(fixture catalogueFixture) *wireTransport {
	return &wireTransport{handler: func(request *http.Request, _ []byte) *http.Response {
		switch {
		case request.URL.Path == "/foundation-models":
			body, _ := json.Marshal(map[string]any{"modelSummaries": fixture.models})
			return jsonResponse(request, http.StatusOK, string(body))
		case request.URL.Path == "/inference-profiles":
			body, _ := json.Marshal(map[string]any{"inferenceProfileSummaries": fixture.profiles})
			return jsonResponse(request, http.StatusOK, string(body))
		default:
			return jsonResponse(request, http.StatusNotFound, `{"message":"unexpected path"}`)
		}
	}}
}

func listModels(t *testing.T, transport *wireTransport) []modelrepo.ObservedModel {
	t.Helper()
	catalogue, err := modelrepo.NewCatalogProvider(
		modelrepo.BackendSpec{Type: "bedrock", BaseURL: "https://bedrock-runtime.us-east-1.amazonaws.com", APIKey: staticCredentials},
		modelrepo.WithCatalogHTTPClient(&http.Client{Transport: transport}),
	)
	require.NoError(t, err)
	models, err := catalogue.ListModels(context.Background())
	require.NoError(t, err)
	return models
}

func modelSummary(id string, name string, streaming bool, inputs []string, outputs []string) map[string]any {
	summary := map[string]any{
		"modelId":                    id,
		"modelName":                  name,
		"responseStreamingSupported": streaming,
		"inferenceTypesSupported":    []string{"ON_DEMAND"},
	}
	if inputs != nil {
		summary["inputModalities"] = inputs
	}
	if outputs != nil {
		summary["outputModalities"] = outputs
	}
	return summary
}

func TestUnit_BedrockCatalog_CapabilitiesComeFromTheVendorSummary(t *testing.T) {
	transport := catalogueTransport(catalogueFixture{models: []map[string]any{
		modelSummary("anthropic.claude-3-5-sonnet-20241022-v2:0", "Claude 3.5 Sonnet v2", true, []string{"TEXT", "IMAGE"}, []string{"TEXT"}),
		modelSummary("meta.llama3-70b-instruct-v1:0", "Llama 3 70B Instruct", false, []string{"TEXT"}, []string{"TEXT"}),
		modelSummary("amazon.titan-embed-text-v2:0", "Titan Text Embeddings V2", false, []string{"TEXT"}, []string{"EMBEDDING"}),
		modelSummary("some.undescribed-v1:0", "Undescribed", true, nil, nil),
	}})
	models := listModels(t, transport)
	require.Len(t, models, 4)

	byName := map[string]modelrepo.ObservedModel{}
	for _, model := range models {
		byName[model.Name] = model
	}

	vision := byName["anthropic.claude-3-5-sonnet-20241022-v2:0"]
	require.True(t, vision.CanChat)
	require.True(t, vision.CanStream)
	require.True(t, vision.CanPrompt)
	require.True(t, vision.CanVision)
	require.False(t, vision.CanEmbed)
	require.False(t, vision.CanThink, "the vendor publishes no reasoning field, so none is inferred")
	require.Nil(t, vision.ThinkingTypes)
	require.Nil(t, vision.PromptCache)
	require.Equal(t, "Claude 3.5 Sonnet v2", vision.Meta[modelrepo.ObservedDisplayNameMetaKey])

	textOnly := byName["meta.llama3-70b-instruct-v1:0"]
	require.True(t, textOnly.CanChat)
	require.False(t, textOnly.CanStream, "the vendor's own streaming flag decides streaming")
	require.False(t, textOnly.CanVision)

	embedding := byName["amazon.titan-embed-text-v2:0"]
	require.False(t, embedding.CanChat, "an embedding output modality is not a conversational model")
	require.False(t, embedding.CanStream)
	require.False(t, embedding.CanPrompt)
	require.False(t, embedding.CanEmbed, "this adapter speaks only Converse, which does not embed")

	undescribed := byName["some.undescribed-v1:0"]
	require.False(t, undescribed.CanChat)
	require.False(t, undescribed.CanStream)
	require.False(t, undescribed.CanPrompt)
	require.False(t, undescribed.CanVision)
}

func TestUnit_BedrockCatalog_ResolvesTheInvocableModelID(t *testing.T) {
	transport := catalogueTransport(catalogueFixture{
		models: []map[string]any{
			modelSummary("anthropic.claude-3-haiku-20240307-v1:0", "Claude 3 Haiku", true, []string{"TEXT"}, []string{"TEXT"}),
			func() map[string]any {
				summary := modelSummary("anthropic.claude-sonnet-4-5-20250929-v1:0", "Claude Sonnet 4.5", true, []string{"TEXT"}, []string{"TEXT"})
				summary["inferenceTypesSupported"] = []string{"INFERENCE_PROFILE"}
				return summary
			}(),
			func() map[string]any {
				summary := modelSummary("anthropic.claude-opus-4-6-v1", "Claude Opus 4.6", true, []string{"TEXT"}, []string{"TEXT"})
				summary["inferenceTypesSupported"] = []string{"INFERENCE_PROFILE"}
				return summary
			}(),
			func() map[string]any {
				summary := modelSummary("meta.llama4-maverick-17b-instruct-v1:0", "Llama 4 Maverick", true, []string{"TEXT"}, []string{"TEXT"})
				summary["inferenceTypesSupported"] = []string{"INFERENCE_PROFILE"}
				return summary
			}(),
		},
		profiles: []map[string]any{
			{"inferenceProfileId": "us.anthropic.claude-sonnet-4-5-20250929-v1:0"},
			{"inferenceProfileId": "global.anthropic.claude-sonnet-4-5-20250929-v1:0"},
			{"inferenceProfileId": "global.meta.llama4-maverick-17b-instruct-v1:0"},
		},
	})
	models := listModels(t, transport)
	names := make([]string, 0, len(models))
	for _, model := range models {
		names = append(names, model.Name)
	}
	require.Equal(t, []string{
		"anthropic.claude-3-haiku-20240307-v1:0",
		"us.anthropic.claude-sonnet-4-5-20250929-v1:0",
		"global.meta.llama4-maverick-17b-instruct-v1:0",
	}, names, "a profile-only model resolves to its geographic profile, its global profile when that is all there is, and is omitted when it has neither")
}

func TestUnit_BedrockMistralClassification(t *testing.T) {
	catalogue, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "bedrock"})
	require.NoError(t, err)

	for _, c := range []struct {
		model string
		path  string
	}{
		{"mistral.mistral-large-2407-v1:0", "/model/mistral.mistral-large-2407-v1:0/converse"},
		{"mistral.mistral-7b-instruct-v0:2", "/model/mistral.mistral-7b-instruct-v0:2/invoke"},
		{"mistral.mixtral-8x7b-instruct-v0:1", "/model/mistral.mixtral-8x7b-instruct-v0:1/invoke"},
		{"mistral.mistral-large-2402-v1:0", "/model/mistral.mistral-large-2402-v1:0/invoke"},
	} {
		t.Run(c.model, func(t *testing.T) {
			transport := &wireTransport{handler: func(request *http.Request, _ []byte) *http.Response {
				if request.URL.Path == "/model/"+c.model+"/invoke" {
					return jsonResponse(request, http.StatusOK, `{"outputs":[{"text":"code","stop_reason":"stop"}]}`)
				}
				return jsonResponse(request, http.StatusOK, textOut("code", "end_turn"))
			}}
			provider := catalogue.ProviderFor(modelrepo.ObservedModel{
				Name:             c.model,
				CapabilityConfig: modelrepo.CapabilityConfig{CanChat: true, CanPrompt: true, MaxOutputTokens: 128},
			})
			provider = withTransport(provider, transport)
			connection, err := provider.GetPromptConnection(context.Background(), "bedrock:us-east-1")
			require.NoError(t, err)
			text, _, err := connection.Prompt(context.Background(), "", 0, "write code", modelrepo.WithPromptMaxTokens(64))
			require.NoError(t, err)
			require.Equal(t, "code", text)
			transport.mu.Lock()
			path := transport.path
			transport.mu.Unlock()
			require.Equal(t, c.path, path)
		})
	}
}

func withTransport(provider modelrepo.Provider, transport *wireTransport) modelrepo.Provider {
	type named interface{ ModelName() string }
	model := provider.(named).ModelName()
	return bedrock.NewBedrockProvider("us-east-1", staticCredentials, model, modelrepo.CapabilityConfig{
		CanChat: true, CanPrompt: true, MaxOutputTokens: 128,
	}, &http.Client{Transport: transport}, nil)
}
