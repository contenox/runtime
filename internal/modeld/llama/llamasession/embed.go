//go:build llamanode && llamacpp_direct

package llamasession

import (
	"context"
	"fmt"
	"math"
	"runtime"

	"github.com/contenox/runtime/internal/modeld/llama"
	"github.com/contenox/runtime/internal/modeld/llama/llamacppshim"
)

const (
	defaultEmbedTokenLimit = 4096
	maxEmbedTokenLimit     = 32768
)

func init() {
	llama.SetEmbedFunc(embed)
	llama.SetEmbedReleaseFunc(embedModels.close)
}

var embedModels embedModelCache[*llamacppshim.Model]

func acquireEmbedModel(modelPath string, cfg llama.Config) (*cachedEmbedModel[*llamacppshim.Model], func(), error) {
	return embedModels.acquire(modelPath, cfg, func() (*llamacppshim.Model, error) {
		m, err := llamacppshim.LoadModel(modelPath, modelConfig(cfg))
		if err != nil {
			return nil, fmt.Errorf("llama embed: load model %q: %w", modelPath, err)
		}
		return m, nil
	})
}

func embedTokenLimit(contextLength int) int {
	limit := contextLength
	if limit <= 0 {
		limit = defaultEmbedTokenLimit
	}
	if limit > maxEmbedTokenLimit {
		limit = maxEmbedTokenLimit
	}
	return limit
}

// embed keeps the entire sequence in one microbatch for encoder attention and sequence pooling.
func embed(ctx context.Context, modelPath string, cfg llama.Config, input string) ([]float64, error) {
	em, release, err := acquireEmbedModel(modelPath, cfg)
	if err != nil {
		return nil, err
	}
	defer release()

	tokens, err := em.model.Tokenize(input, true, true)
	if err != nil {
		return nil, fmt.Errorf("llama embed: tokenize: %w", err)
	}
	limit := embedTokenLimit(cfg.NumCtx)
	if len(tokens) > limit {
		return nil, fmt.Errorf("llama embed: input is %d tokens but the limit is %d; raise context length or chunk the input", len(tokens), limit)
	}
	batchSize := len(tokens)
	if batchSize < 1 {
		batchSize = 1
	}
	threads := cfg.NumThreads
	if threads <= 0 {
		threads = runtime.NumCPU()
	}

	lctx, err := llamacppshim.NewContext(em.model, llamacppshim.ContextConfig{
		NumCtx:           batchSize,
		NumBatch:         batchSize,
		NumSeqMax:        1,
		NumThreads:       threads,
		Embeddings:       true,
		EmbeddingPooling: cfg.EmbeddingPooling,
	})
	if err != nil {
		return nil, fmt.Errorf("llama embed: create context: %w", err)
	}
	defer lctx.Close()
	batch, err := llamacppshim.NewBatch(batchSize, 1, 0)
	if err != nil {
		return nil, fmt.Errorf("llama embed: create batch: %w", err)
	}
	defer batch.Free()

	for i, tok := range tokens {
		if err := batch.Add(tok, nil, i, true, 0); err != nil {
			return nil, fmt.Errorf("llama embed: batch add: %w", err)
		}
	}
	if err := lctx.EmbedBatch(batch); err != nil {
		return nil, fmt.Errorf("llama embed: evaluate: %w", err)
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	emb, err := extractEmbedding(lctx)
	if err != nil {
		return nil, err
	}
	return l2Normalize(emb)
}

func extractEmbedding(lctx *llamacppshim.Context) ([]float64, error) {
	if pooled := lctx.GetEmbeddingsSeq(0); pooled != nil {
		out := make([]float64, len(pooled))
		for i, v := range pooled {
			out[i] = float64(v)
		}
		return out, nil
	}
	return nil, fmt.Errorf("llama embed: model returned no pooled embedding; configure its retrieval pooling contract")
}

func l2Normalize(vec []float64) ([]float64, error) {
	var sum float64
	for _, v := range vec {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("llama embed: embedding contains NaN or Inf")
		}
		sum += v * v
	}
	if sum == 0 || math.IsInf(sum, 0) {
		return nil, fmt.Errorf("llama embed: embedding has an invalid norm")
	}
	norm := 1.0 / math.Sqrt(sum)
	for i := range vec {
		vec[i] *= norm
	}
	return vec, nil
}
