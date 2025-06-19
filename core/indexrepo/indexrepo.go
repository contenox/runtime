package indexrepo

import (
	"context"
	"errors"
	"fmt"

	"github.com/contenox/contenox/core/llmrepo"
	"github.com/contenox/contenox/core/llmresolver"
	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/serverops/store"
	"github.com/contenox/contenox/core/serverops/vectors"
	"github.com/contenox/contenox/libs/libdb"
)

type Args struct {
	Epsilon float32 `json:"epsilon"`
	Radius  float32 `json:"radius"`
}

type SearchResult struct {
	ID           string      `json:"id"`
	ResourceType string      `json:"type"`
	Distance     float32     `json:"distance"`
	FileMeta     *store.File `json:"fileMeta"`
}

func ExecuteVectorSearch(
	ctx context.Context,
	embedder llmrepo.ModelRepo,
	vectorsStore vectors.Store,
	dbExec libdb.Exec,
	queries []string,
	topK int,
	searchArgs *Args,
) ([]SearchResult, error) {
	storeInstance := store.New(dbExec)
	searchResults := make([]SearchResult, 0)

	for _, query := range queries {
		if query == "" {
			continue
		}
		provider, err := embedder.GetProvider(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get embedder provider: %w", err)
		}

		embedClient, err := llmresolver.Embed(ctx, llmresolver.EmbedRequest{
			ModelName: provider.ModelName(),
		}, embedder.GetRuntime(ctx), llmresolver.Randomly)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve embed client: %w", err)
		}

		vectorData, err := embedClient.Embed(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("failed to embed query: %w", err)
		}

		vectorData32 := make([]float32, len(vectorData))
		for i, v := range vectorData {
			vectorData32[i] = float32(v)
		}

		var args *vectors.SearchArgs
		if searchArgs != nil {
			args = &vectors.SearchArgs{
				Epsilon: searchArgs.Epsilon,
				Radius:  searchArgs.Radius,
			}
		}

		results, err := vectorsStore.Search(ctx, vectorData32, topK, 1, args)
		if err != nil {
			return nil, fmt.Errorf("vector search failed for query %s: %w", query, err)
		}

		for _, res := range results {
			chunkIndex, err := storeInstance.GetChunkIndexByID(ctx, res.ID)
			if errors.Is(err, libdb.ErrNotFound) {
				if delErr := vectorsStore.Delete(ctx, res.ID); delErr != nil {
					fmt.Printf("failed to clean orphaned vector %s: %v\n", res.ID, delErr)
				}
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("failed to get chunk index: %w", err)
			}

			searchResults = append(searchResults, SearchResult{
				ID:           chunkIndex.ResourceID,
				ResourceType: chunkIndex.ResourceType,
				Distance:     res.Distance,
			})
		}
	}
	if len(searchResults) == 0 {
		return []SearchResult{}, nil
	}
	// TODO deduplication does not aggregate distances
	// Deduplicate results
	deduplicate := make(map[string]SearchResult)
	for _, sr := range searchResults {
		deduplicate[sr.ID] = sr
	}

	// Convert map back to slice
	deduplicatedResults := make([]SearchResult, 0, len(deduplicate))
	for _, sr := range deduplicate {
		deduplicatedResults = append(deduplicatedResults, sr)
	}

	return deduplicatedResults, nil
}

func DummyaugmentStrategy(ctx context.Context, chunk string) (string, error) {
	return chunk, nil
}

func IngestChunks(
	ctx context.Context,
	embedder llmrepo.ModelRepo,
	vectorsStore vectors.Store,
	dbExec libdb.Exec,
	resourceID string,
	resourceType string,
	chunks []string,
	augmentStrategy func(ctx context.Context, chunk string) (string, error),
) (vectorIDs []string, augmentedMetadata []string, err error) {
	// Get embedding provider once
	embedProvider, err := embedder.GetProvider(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get embedder provider: %w", err)
	}
	embedClient, err := llmresolver.Embed(ctx, llmresolver.EmbedRequest{
		ModelName: embedProvider.ModelName(),
	}, embedder.GetRuntime(ctx), llmresolver.Randomly)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to resolve embed client: %w", err)
	}
	if embedClient == nil {
		return nil, nil, errors.New("embed client is nil")
	}

	modelName := embedProvider.ModelName()
	storeInstance := store.New(dbExec)

	vectorIDs = make([]string, 0, len(chunks))
	augmentedMetadata = make([]string, 0, len(chunks))

	for i, chunk := range chunks {
		var enriched string
		enriched, err := augmentStrategy(ctx, chunk)
		if err != nil {
			return vectorIDs, augmentedMetadata, fmt.Errorf("chunk %d: %w", i, err)
		}

		vectorData, err := embedText(ctx, embedClient, enriched)
		if err != nil {
			return vectorIDs, augmentedMetadata, fmt.Errorf("chunk %d: %w", i, err)
		}

		vectorID := fmt.Sprintf("%s-%d", resourceID, i)

		v := vectors.Vector{ID: vectorID, Data: vectorData}
		if err := vectorsStore.Insert(ctx, v); err != nil {
			// Return partial results + current error
			return vectorIDs, augmentedMetadata, fmt.Errorf("chunk %d: failed to insert vector: %w", i, err)
		}

		if err := storeInstance.CreateChunkIndex(ctx, &store.ChunkIndex{
			ID:             vectorID,
			VectorID:       vectorID,
			VectorStore:    "vald",
			ResourceID:     resourceID,
			ResourceType:   resourceType,
			EmbeddingModel: modelName,
		}); err != nil {
			return vectorIDs, augmentedMetadata, fmt.Errorf("chunk %d: failed to create chunk index: %w", i, err)
		}

		vectorIDs = append(vectorIDs, vectorID)
		augmentedMetadata = append(augmentedMetadata, enriched)
	}

	return vectorIDs, augmentedMetadata, nil
}

// Helper function for text embedding
func embedText(ctx context.Context, embedClient serverops.LLMEmbedClient, text string) ([]float32, error) {
	vectorData, err := embedClient.Embed(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("embedding failed: %w", err)
	}

	vectorData32 := make([]float32, len(vectorData))
	for i, v := range vectorData {
		vectorData32[i] = float32(v)
	}
	return vectorData32, nil
}
