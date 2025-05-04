package indexservice

import (
	"context"
	"errors"
	"fmt"

	"github.com/js402/cate/core/llmembed"
	"github.com/js402/cate/core/llmresolver"
	"github.com/js402/cate/core/serverops"
	"github.com/js402/cate/core/serverops/store"
	"github.com/js402/cate/core/serverops/vectors"
	"github.com/js402/cate/libs/libdb"
)

type Service struct {
	embedder llmembed.Embedder
	vectors  vectors.Store
	db       libdb.DBManager
}

func New(ctx context.Context, embedder llmembed.Embedder, vectors vectors.Store, dbInstance libdb.DBManager) *Service {
	return &Service{
		embedder: embedder,
		vectors:  vectors,
		db:       dbInstance,
	}
}

type IndexRequest struct {
	Chunks  []string `json:"chunks"`
	ID      string   `json:"id"`
	Replace bool     `json:"replace"`
}

type IndexResponse struct {
	ID      string   `json:"id"`
	Vectors []string `json:"vectors"`
}

func (s *Service) Index(ctx context.Context, request *IndexRequest) (*IndexResponse, error) {
	tx, commit, end, err := s.db.WithTransaction(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer end()
	storeInstance := store.New(tx)
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
	if request.Replace {
		chunks, err := storeInstance.ListChunkIndicesByResource(ctx, request.ID, "file")
		if err != nil {
			return nil, fmt.Errorf("failed to get chunk index by ID: %w", err)
		}
		for _, chunk := range chunks {
			err := s.vectors.Delete(ctx, chunk.VectorID)
			if err != nil {
				return nil, fmt.Errorf("failed to delete vector: %w", err)
			}
		}
	}

	ids := make([]string, len(request.Chunks))
	for i, chunk := range request.Chunks {
		vectorData, err := embedClient.Embed(ctx, chunk)
		if err != nil {
			return nil, fmt.Errorf("failed to embed text: %w", err)
		}
		vectorData32 := make([]float32, len(vectorData))
		// Iterate and cast each element
		for i, v := range vectorData {
			vectorData32[i] = float32(v)
		}
		id := fmt.Sprintf("%s-%d", request.ID, i)
		ids[i] = id
		v := vectors.Vector{
			ID:   id,
			Data: vectorData32,
		}

		err = s.vectors.Insert(ctx, v)
		if err != nil {
			return nil, fmt.Errorf("failed to insert vector: %w", err)
		}
		err = storeInstance.CreateChunkIndex(ctx, &store.ChunkIndex{
			ID:           id,
			VectorID:     id,
			VectorStore:  "vald",
			ResourceID:   request.ID,
			ResourceType: "file",
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create chunk index: %w", err)
		}
	}
	// on failure we don't commit the chunk-entries but endup with ingested vectors.
	// this is not an issue, if on search we verify that to the matching vectors chunks exist
	// and only then include them in the response.
	err = commit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to commit: %w", err)
	}
	return &IndexResponse{
		ID:      request.ID,
		Vectors: ids,
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
	storeInstance := store.New(s.db.WithoutTransaction())
	searchResults := make([]SearchResult, len(results))
	for i, res := range results {
		_, err := storeInstance.GetChunkIndexByID(ctx, res.ID)
		if errors.Is(err, libdb.ErrNotFound) {
			err := s.vectors.Delete(ctx, res.ID)
			if err != nil {
				println("SERVERBUG", err)
			}
			continue
		}
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
