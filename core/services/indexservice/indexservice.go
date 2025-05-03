package indexservice

import (
	"context"
	"errors"
	"fmt"

	"github.com/js402/cate/core/llmembed"
	"github.com/js402/cate/core/llmresolver"
	"github.com/js402/cate/core/serverops"
	"github.com/js402/cate/core/serverops/vectors"
)

type Service struct {
	embedder llmembed.Embedder
	vectors  vectors.Store
}

func New(ctx context.Context, embedder llmembed.Embedder, vectors vectors.Store) *Service {
	return &Service{
		embedder: embedder,
		vectors:  vectors,
	}
}

type IndexRequest struct {
	Text    string `json:"text"`
	ID      string `json:"id"`
	Replace bool   `json:"replace"`
}

type IndexResponse struct {
	ID string `json:"id"`
}

func (s *Service) Index(ctx context.Context, request *IndexRequest) (*IndexResponse, error) {
	provider, err := s.embedder.GetProvider(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider: %w", err)
	}
	embedClient, err := llmresolver.ResolveEmbed(ctx, llmresolver.ResolveEmbedRequest{
		ModelName: provider.ModelName(),
	}, s.embedder.GetRuntime(ctx), llmresolver.ResolveRandomly)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve embed client: %w", err)
	}
	if embedClient == nil {
		return nil, errors.New("embed client is nil")
	}
	vectorData, err := embedClient.Embed(ctx, request.Text)
	if err != nil {
		return nil, fmt.Errorf("failed to embed text: %w", err)
	}
	vectorData32 := make([]float32, len(vectorData))
	// Iterate and cast each element
	for i, v := range vectorData {
		vectorData32[i] = float32(v)
	}
	v := vectors.Vector{
		ID:   request.ID,
		Data: vectorData32,
	}
	if request.Replace {
		err = s.vectors.Upsert(ctx, v)
		if err != nil {
			return nil, fmt.Errorf("failed to upsert vector: %w", err)
		}
		return &IndexResponse{
			ID: request.ID,
		}, nil
	}
	err = s.vectors.Insert(ctx, v)
	if err != nil {
		return nil, fmt.Errorf("failed to insert vector: %w", err)
	}
	return &IndexResponse{
		ID: request.ID,
	}, nil
}

type SearchRequest struct {
	Query string `json:"text"`
	TopK  int    `json:"topK"`
	*SearchRequestArgs
}

type SearchRequestArgs struct {
	Epsilon float32 `json:"epsilon"`
	Radius  float32 `json:"radius"`
}

type SearchResult struct {
	ID       string  `json:"id"`
	Distance float32 `json:"distance"`
}

type SearchResponse struct {
	Results []SearchResult `json:"results"`
}

func (s *Service) Search(ctx context.Context, request *SearchRequest) (*SearchResponse, error) {
	provider, err := s.embedder.GetProvider(ctx)
	if err != nil {
		return nil, err
	}
	embedClient, err := llmresolver.ResolveEmbed(ctx, llmresolver.ResolveEmbedRequest{
		ModelName: provider.ModelName(),
	}, s.embedder.GetRuntime(ctx), llmresolver.ResolveRandomly)
	if err != nil {
		return nil, err
	}
	if embedClient == nil {
		return nil, errors.New("embed client is nil")
	}

	// Generate query embedding
	vectorData, err := embedClient.Embed(ctx, request.Query)
	if err != nil {
		return nil, err
	}
	vectorData32 := make([]float32, len(vectorData))
	for i, v := range vectorData {
		vectorData32[i] = float32(v)
	}

	// Handle TopK default
	topK := request.TopK
	if topK <= 0 {
		topK = 10 // Default to 10 results
	}

	// Perform vector search
	var args *vectors.SearchArgs
	if request.SearchRequestArgs != nil {
		args = &vectors.SearchArgs{
			Epsilon: request.SearchRequestArgs.Epsilon,
			Radius:  request.SearchRequestArgs.Radius,
		}
	}

	results, err := s.vectors.Search(ctx, vectorData32, topK, 1, args)
	if err != nil {
		return nil, err
	}

	// Convert to API response format
	searchResults := make([]SearchResult, len(results))
	for i, res := range results {
		searchResults[i] = SearchResult{
			ID:       res.ID,
			Distance: res.Distance,
		}
	}

	return &SearchResponse{Results: searchResults}, nil
}

func (s *Service) GetServiceName() string {
	return "indexservice"
}

func (s *Service) GetServiceGroup() string {
	return serverops.DefaultDefaultServiceGroup // TODO: is that accurate?
}
