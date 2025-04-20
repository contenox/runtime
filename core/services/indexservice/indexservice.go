package indexservice

import (
	"context"

	"github.com/js402/cate/core/llmembed"
	"github.com/js402/cate/core/modelprovider"
	"github.com/js402/cate/core/runtimestate"
	"github.com/js402/cate/core/serverops"
)

type Service struct {
	embedder      llmembed.Embedder
	runtime       *runtimestate.State
	modelProvider modelprovider.Provider
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

func (s *Service) Index(request *IndexRequest) (*IndexResponse, error) {
	return &IndexResponse{}, nil
}

func (s *Service) GetServiceName() string {
	return "indexservice"
}

func (s *Service) GetServiceGroup() string {
	return serverops.DefaultDefaultServiceGroup // TODO: is that accurate?
}
