package indexrepo

import (
	"context"
	"errors"
	"fmt"

	"github.com/contenox/contenox/core/llmrepo"
	"github.com/contenox/contenox/core/llmresolver"
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
