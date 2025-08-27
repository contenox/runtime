package gemini

import (
	"context"
	"fmt"

	"github.com/contenox/runtime/internal/modelrepo"
)

type GeminiEmbedClient struct {
	geminiClient
}

type geminiEmbedContentRequest struct {
	Model   string        `json:"model"`
	Content geminiContent `json:"content"`
}

type geminiEmbedContentResponse struct {
	Embedding struct {
		Values []float64 `json:"values"`
	} `json:"embedding"`
}

func (c *GeminiEmbedClient) Embed(ctx context.Context, prompt string) ([]float64, error) {
	request := geminiEmbedContentRequest{
		Model: "models/" + c.modelName,
		Content: geminiContent{
			Parts: []geminiPart{{Text: prompt}},
		},
	}

	endpoint := fmt.Sprintf("/v1beta/models/%s:embedContent", c.modelName)
	var response geminiEmbedContentResponse
	if err := c.sendRequest(ctx, endpoint, request, &response); err != nil {
		return nil, err
	}

	if len(response.Embedding.Values) == 0 {
		return nil, fmt.Errorf("no embedding values returned from Gemini for model %s", c.modelName)
	}
	return response.Embedding.Values, nil
}

var _ modelrepo.LLMEmbedClient = (*GeminiEmbedClient)(nil)
