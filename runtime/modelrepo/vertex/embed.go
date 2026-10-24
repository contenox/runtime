package vertex

import (
	"context"
	"fmt"

	"github.com/contenox/runtime/runtime/modelrepo"
)

type vertexEmbedClient struct {
	vertexClient
}

type vertexPredictEmbedRequest struct {
	Instances []vertexEmbedInstance `json:"instances"`
}

type vertexEmbedInstance struct {
	Content  string `json:"content"`
	TaskType string `json:"task_type,omitempty"`
}

type vertexEmbedContentRequest struct {
	Content vertexContent `json:"content"`
}

type vertexEmbedContentResponse struct {
	Embedding struct {
		Values []float64 `json:"values"`
	} `json:"embedding"`
}

type vertexPredictEmbedResponse struct {
	Predictions []struct {
		Embeddings struct {
			Values []float64 `json:"values"`
		} `json:"embeddings"`
	} `json:"predictions"`
}

func (c *vertexEmbedClient) Embed(ctx context.Context, prompt string) ([]float64, error) {
	reportErr, reportChange, end := c.tracker.Start(ctx, "embed", "vertex", "model", c.modelName)
	defer end()

	if c.modelName == "gemini-embedding-2" {
		values, err := c.embedContent(ctx, prompt)
		if err != nil {
			reportErr(err)
			return nil, err
		}
		reportChange("embedding_completed", map[string]any{"embedding_length": len(values)})
		return values, nil
	}

	taskType, err := vertexEmbeddingTaskType(modelrepo.EmbeddingPurposeFromContext(ctx))
	if err != nil {
		reportErr(err)
		return nil, err
	}
	request := vertexPredictEmbedRequest{
		Instances: []vertexEmbedInstance{{Content: prompt, TaskType: taskType}},
	}
	var response vertexPredictEmbedResponse
	if err := c.sendRequest(ctx, c.endpoint("predict"), request, &response); err != nil {
		reportErr(err)
		return nil, err
	}

	if len(response.Predictions) == 0 || len(response.Predictions[0].Embeddings.Values) == 0 {
		err := fmt.Errorf("vertex model %s returned no embedding values; it may not be an embedding model — use a text-embedding model such as gemini-embedding-001", c.modelName)
		reportErr(err)
		return nil, err
	}

	reportChange("embedding_completed", map[string]any{
		"embedding_length": len(response.Predictions[0].Embeddings.Values),
	})
	return response.Predictions[0].Embeddings.Values, nil
}

var _ modelrepo.LLMEmbedClient = (*vertexEmbedClient)(nil)

func vertexEmbeddingTaskType(purpose modelrepo.EmbeddingPurpose) (string, error) {
	switch purpose {
	case modelrepo.EmbeddingDocument:
		return "RETRIEVAL_DOCUMENT", nil
	case modelrepo.EmbeddingQuery:
		return "RETRIEVAL_QUERY", nil
	default:
		return "", fmt.Errorf("vertex: embedding purpose %q has no documented task type", purpose)
	}
}

func (c *vertexEmbedClient) embedContent(ctx context.Context, prompt string) ([]float64, error) {
	var text string
	switch purpose := modelrepo.EmbeddingPurposeFromContext(ctx); purpose {
	case modelrepo.EmbeddingDocument:
		text = "title: none | text: " + prompt
	case modelrepo.EmbeddingQuery:
		text = "task: search result | query: " + prompt
	default:
		return nil, fmt.Errorf("vertex: embedding purpose %q has no documented instruction for %s", purpose, c.modelName)
	}
	request := vertexEmbedContentRequest{Content: vertexContent{Parts: []vertexPart{{Text: text}}}}
	var response vertexEmbedContentResponse
	if err := c.sendRequest(ctx, c.endpoint("embedContent"), request, &response); err != nil {
		return nil, err
	}
	if len(response.Embedding.Values) == 0 {
		return nil, fmt.Errorf("vertex model %s returned no embedding values", c.modelName)
	}
	return response.Embedding.Values, nil
}
