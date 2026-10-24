package vllm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
)

type vLLMEmbedClient struct {
	baseURL    string
	httpClient *http.Client
	modelName  string
	apiKey     string
	tracker    libtracker.ActivityTracker
}

func NewVLLMEmbedClient(baseURL, modelName string, httpClient *http.Client, apiKey string, tracker libtracker.ActivityTracker) (modelrepo.LLMEmbedClient, error) {
	if httpClient == nil {
		httpClient = modelrepo.SharedHTTPClient
	}
	if tracker == nil {
		tracker = libtracker.NoopTracker{}
	}
	return &vLLMEmbedClient{
		baseURL:    normalizeBaseURL(baseURL),
		httpClient: httpClient,
		modelName:  modelName,
		apiKey:     apiKey,
		tracker:    tracker,
	}, nil
}

type embedRequest struct {
	Model          string `json:"model"`
	Input          string `json:"input"`
	EncodingFormat string `json:"encoding_format"`
}

type embedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
	Usage *struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

func (c *vLLMEmbedClient) Embed(ctx context.Context, prompt string) ([]float64, error) {
	reportErr, reportChange, end := c.tracker.Start(ctx, "embed", "vllm", "model", c.modelName)
	defer end()

	ctx, cancel := modelrepo.NonStreamingContext(ctx)
	defer cancel()

	body, err := json.Marshal(embedRequest{Model: c.modelName, Input: prompt, EncodingFormat: "float"})
	if err != nil {
		reportErr(err)
		return nil, fmt.Errorf("encode embedding request for model %s: %w", c.modelName, err)
	}

	resp, err := modelrepo.DoWithRetry(ctx, c.httpClient, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/embeddings", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if c.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
		}
		return req, nil
	})
	if err != nil {
		err = fmt.Errorf("embedding request failed for model %s: %w", c.modelName, err)
		reportErr(err)
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		reportErr(err)
		return nil, fmt.Errorf("read embedding response for model %s: %w", c.modelName, err)
	}
	if resp.StatusCode != http.StatusOK {
		err = modelrepo.ClassifyProviderError(
			fmt.Errorf("vLLM embedding endpoint returned %d for model %s: %s", resp.StatusCode, c.modelName, string(raw)),
			resp.StatusCode, "", string(raw))
		reportErr(err)
		return nil, err
	}

	var decoded embedResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		err = fmt.Errorf("decode embedding response for model %s: %w", c.modelName, err)
		reportErr(err)
		return nil, err
	}
	if len(decoded.Data) == 0 {
		err = fmt.Errorf("embedding response was empty for model %s", c.modelName)
		reportErr(err)
		return nil, err
	}

	reportChange("embedding_completed", map[string]any{
		"embedding_length": len(decoded.Data[0].Embedding),
	})
	return decoded.Data[0].Embedding, nil
}

var _ modelrepo.LLMEmbedClient = (*vLLMEmbedClient)(nil)
