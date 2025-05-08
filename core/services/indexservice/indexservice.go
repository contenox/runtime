package indexservice

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/js402/cate/core/llmrepo"
	"github.com/js402/cate/core/llmresolver"
	"github.com/js402/cate/core/serverops"
	"github.com/js402/cate/core/serverops/store"
	"github.com/js402/cate/core/serverops/vectors"
	"github.com/js402/cate/libs/libdb"
)

type Service struct {
	embedder   llmrepo.ModelRepo
	promptExec llmrepo.ModelRepo
	vectors    vectors.Store
	db         libdb.DBManager
}

func New(ctx context.Context, embedder, promptExec llmrepo.ModelRepo, vectors vectors.Store, dbInstance libdb.DBManager) *Service {
	return &Service{
		embedder:   embedder,
		promptExec: promptExec,
		vectors:    vectors,
		db:         dbInstance,
	}
}

type IndexRequest struct {
	Chunks   []string `json:"chunks"`
	ID       string   `json:"id"`
	Replace  bool     `json:"replace"`
	JobID    string   `json:"jobId"`
	LeaserID string   `json:"leaserId"`
}

type IndexResponse struct {
	ID                string   `json:"id"`
	VectorIDs         []string `json:"vectors"`
	AugmentedMetadata []string `json:"augmentedMetadata"`
}

func (s *Service) Index(ctx context.Context, request *IndexRequest) (*IndexResponse, error) {
	if request.LeaserID == "" {
		return nil, serverops.ErrMissingParameter
	}
	if request.JobID == "" {
		return nil, serverops.ErrMissingParameter
	}
	tx, commit, end, err := s.db.WithTransaction(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer end()
	storeInstance := store.New(tx)
	if err := serverops.CheckServiceAuthorization(ctx, storeInstance, s, store.PermissionEdit); err != nil {
		return nil, err
	}
	job, err := storeInstance.GetLeasedJob(ctx, request.JobID)
	if err != nil {
		return nil, fmt.Errorf("failed to get leased job %s: %w", request.JobID, err)
	}
	if job.Leaser != request.LeaserID {
		return nil, fmt.Errorf("job is not leased by this leaser")
	}
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
	meta := []string{}
	ids := make([]string, len(request.Chunks))
	for i, chunk := range request.Chunks {
		keywords, err := s.findKeywords(ctx, chunk)
		meta = append(meta, keywords)
		if err != nil {
			return nil, fmt.Errorf("failed to enrich chunk: %w", err)
		}
		enriched := fmt.Sprintf("%s\n\nKeywords: %s", chunk, keywords)
		vectorData, err := embedClient.Embed(ctx, enriched)
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
			ID:             id,
			VectorID:       id,
			VectorStore:    "vald",
			ResourceID:     request.ID,
			ResourceType:   job.EntityType,
			EmbeddingModel: provider.ModelName(),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create chunk index: %w", err)
		}
	}
	err = storeInstance.DeleteLeasedJob(ctx, job.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to delete leased job: %w", err)
	}
	// on failure we don't commit the chunk-entries but endup with ingested vectors.
	// this is not an issue, if on search we verify that to the matching vectors chunks exist
	// and only then include them in the response.
	err = commit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to commit: %w", err)
	}
	return &IndexResponse{
		ID:                request.ID,
		VectorIDs:         ids,
		AugmentedMetadata: meta,
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
	ID           string  `json:"id"`
	ResourceType string  `json:"type"`
	Distance     float32 `json:"distance"`
}

type SearchResponse struct {
	Results []SearchResult `json:"results"`
}

func (s *Service) Search(ctx context.Context, request *SearchRequest) (*SearchResponse, error) {
	storeInstance := store.New(s.db.WithoutTransaction())
	if err := serverops.CheckServiceAuthorization(ctx, storeInstance, s, store.PermissionView); err != nil {
		return nil, err
	}

	// Enhance the search query
	enhancedQuery, err := s.enhanceQuery(ctx, request.Query)
	if err != nil {
		// Fallback to original query on enhancement failure
		enhancedQuery = request.Query
	}

	// Generate enhanced query embedding
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

	vectorData, err := embedClient.Embed(ctx, enhancedQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to embed query: %w", err)
	}

	vectorData32 := make([]float32, len(vectorData))
	for i, v := range vectorData {
		vectorData32[i] = float32(v)
	}

	// Handle TopK default
	topK := request.TopK
	if topK <= 0 {
		topK = 10
	}

	// Perform vector search
	var args *vectors.SearchArgs
	if request.SearchRequestArgs != nil {
		args = &vectors.SearchArgs{
			Epsilon: request.Epsilon,
			Radius:  request.Radius,
		}
	}

	results, err := s.vectors.Search(ctx, vectorData32, topK, 1, args)
	if err != nil {
		return nil, fmt.Errorf("vector search failed: %w", err)
	}

	// Process results and verify chunk indices
	searchResults := make([]SearchResult, 0, len(results))
	for _, res := range results {
		chunkIndex, err := storeInstance.GetChunkIndexByID(ctx, res.ID)
		if errors.Is(err, libdb.ErrNotFound) {
			if delErr := s.vectors.Delete(ctx, res.ID); delErr != nil {
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

	return &SearchResponse{Results: searchResults}, nil
}

func (s *Service) findKeywords(ctx context.Context, chunk string) (string, error) {
	prompt := fmt.Sprintf(`Extract 5-7 keywords from the following text:

%s

Return a comma-separated list of keywords.`, chunk)

	provider, err := s.promptExec.GetProvider(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get provider: %w", err)
	}

	promptClient, err := llmresolver.ResolvePromptExecute(ctx, llmresolver.ResolvePromptRequest{
		ModelName: provider.ModelName(),
	}, s.promptExec.GetRuntime(ctx), llmresolver.ResolveRandomly)
	if err != nil {
		return "", fmt.Errorf("failed to resolve prompt client for model %s: %w", provider.ModelName(), err)
	}
	response, err := promptClient.Prompt(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("failed to execute the prompt: %w", err)
	}
	return response, nil
}

func (s *Service) enhanceQuery(ctx context.Context, query string) (string, error) {
	isQuestion, err := s.classifyQuestion(ctx, query)
	if err != nil {
		return "", fmt.Errorf("question classification failed: %w", err)
	}

	if isQuestion {
		return s.rewriteQuery(ctx, query, questionRewritePrompt)
	}

	isOptimal, err := s.checkQueryOptimality(ctx, query)
	if err != nil || isOptimal {
		return query, err
	}

	return s.rewriteQuery(ctx, query, standardRewritePrompt)
}

const (
	questionRewritePrompt = `Transform the following question into a comprehensive search query. Retain the core intent while adding relevant context and keywords.

Question: %s

Optimized query:`

	standardRewritePrompt = `Improve the following search query for a document retrieval system. Enhance clarity and add missing context while preserving original meaning.

Original query: %s

Improved query:`
)

func (s *Service) classifyQuestion(ctx context.Context, query string) (bool, error) {
	prompt := fmt.Sprintf(`Is the following input a question? Answer strictly with "yes" or "no".

Input: %s`, query)

	response, err := s.executePrompt(ctx, prompt)
	if err != nil {
		return false, err
	}

	return strings.EqualFold(strings.TrimSpace(response), "yes"), nil
}

func (s *Service) checkQueryOptimality(ctx context.Context, query string) (bool, error) {
	prompt := fmt.Sprintf(`Does this query contain sufficient context and keywords for effective document search? Answer with "yes" or "no".

Query: %s`, query)

	response, err := s.executePrompt(ctx, prompt)
	if err != nil {
		return false, err
	}

	return strings.EqualFold(strings.TrimSpace(response), "yes"), nil
}

func (s *Service) rewriteQuery(ctx context.Context, query, promptTemplate string) (string, error) {
	prompt := fmt.Sprintf(promptTemplate, query)
	return s.executePrompt(ctx, prompt)
}

func (s *Service) executePrompt(ctx context.Context, prompt string) (string, error) {
	provider, err := s.promptExec.GetProvider(ctx)
	if err != nil {
		return "", fmt.Errorf("provider resolution failed: %w", err)
	}

	client, err := llmresolver.ResolvePromptExecute(ctx, llmresolver.ResolvePromptRequest{
		ModelName: provider.ModelName(),
	}, s.promptExec.GetRuntime(ctx), llmresolver.ResolveRandomly)
	if err != nil {
		return "", fmt.Errorf("client resolution failed: %w", err)
	}

	response, err := client.Prompt(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("prompt execution failed: %w", err)
	}

	return strings.TrimSpace(response), nil
}

func (s *Service) GetServiceName() string {
	return "indexservice"
}

func (s *Service) GetServiceGroup() string {
	return serverops.DefaultDefaultServiceGroup // TODO: is that accurate?
}
