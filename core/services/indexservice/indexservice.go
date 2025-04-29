package indexservice

import (
	"context"
	"errors"

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
	Text string `json:"text"`
	ID   string `json:"id"`
}

type IndexResponse struct {
	ID string `json:"id"`
}

func (s *Service) Index(ctx context.Context, request *IndexRequest) (*IndexResponse, error) {
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
	vectorData, err := embedClient.Embed(ctx, request.Text)
	if err != nil {
		return nil, err
	}
	vectorData32 := make([]float32, len(vectorData))
	// Iterate and cast each element
	for i, v := range vectorData {
		vectorData32[i] = float32(v)
	}
	err = s.vectors.Insert(ctx, vectors.Vector{
		ID:   request.ID,
		Data: vectorData32,
	})
	if err != nil {
		return nil, err
	}
	return &IndexResponse{
		ID: request.ID,
	}, nil
}

func (s *Service) GetServiceName() string {
	return "indexservice"
}

func (s *Service) GetServiceGroup() string {
	return serverops.DefaultDefaultServiceGroup // TODO: is that accurate?
}
