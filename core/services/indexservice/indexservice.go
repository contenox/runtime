package indexservice

import (
	"context"
	"errors"

	"github.com/js402/cate/core/llmembed"
	"github.com/js402/cate/core/llmresolver"
	"github.com/js402/cate/core/serverops"
)

type Service struct {
	embedder llmembed.Embedder
}

func New(ctx context.Context, embedder llmembed.Embedder) *Service {
	return &Service{
		embedder: embedder,
	}
}

type IndexRequest struct {
	text string
}

type IndexResponse struct {
	// Define fields for the index response
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
	_, err = embedClient.Embed(ctx, request.text)
	if err != nil {
		return nil, err
	}
	// TODO: wire up the index logic here
	return &IndexResponse{}, nil
}

func (s *Service) GetServiceName() string {
	return "indexservice"
}

func (s *Service) GetServiceGroup() string {
	return serverops.DefaultDefaultServiceGroup // TODO: is that accurate?
}
