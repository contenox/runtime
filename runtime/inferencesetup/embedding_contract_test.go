package inferencesetup_test

import (
	"context"
	"math"
	"testing"

	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/llmrepo"
	"github.com/contenox/runtime/runtime/modelrepo"
)

type retrievalModels struct {
	verifyingModels
	vectors  [][]float64
	purposes []modelrepo.EmbeddingPurpose
	routes   []llmrepo.EmbedRequest
}

func (m *retrievalModels) Embed(ctx context.Context, route llmrepo.EmbedRequest, _ string) ([]float64, llmrepo.Meta, error) {
	m.purposes = append(m.purposes, modelrepo.EmbeddingPurposeFromContext(ctx))
	m.routes = append(m.routes, route)
	return m.vectors[len(m.purposes)-1], llmrepo.Meta{}, nil
}

func TestUnit_RetrievalVerificationUsesConfiguredDocumentAndQueryRoutes(t *testing.T) {
	models := &retrievalModels{vectors: [][]float64{{1, 0}, {0, 1}, {1, 0.1}}}
	dimension, err := inferencesetup.VerifyRetrieval(context.Background(), models, inferencesetup.Assignment{Provider: "selected-provider", Model: "selected-embedding"})
	if err != nil || dimension != 2 {
		t.Fatalf("retrieval dimension %d: %v", dimension, err)
	}
	for index, purpose := range []modelrepo.EmbeddingPurpose{modelrepo.EmbeddingDocument, modelrepo.EmbeddingDocument, modelrepo.EmbeddingQuery} {
		if models.purposes[index] != purpose || models.routes[index].ProviderType != "selected-provider" || models.routes[index].ModelName != "selected-embedding" {
			t.Fatalf("input %d did not preserve its purpose and explicit route", index)
		}
	}
}

func TestUnit_RetrievalVerificationRejectsConstantReversedAndInconsistentVectors(t *testing.T) {
	for _, vectors := range [][][]float64{
		{{1, 0}, {1, 0}, {1, 0}},
		{{1, 0}, {0, 1}, {0, 1}},
		{{1, 0}, {0, 1}, {1, 0, 0}},
	} {
		models := &retrievalModels{vectors: vectors}
		if _, err := inferencesetup.VerifyRetrieval(context.Background(), models, inferencesetup.Assignment{Provider: "provider", Model: "embedding"}); err == nil {
			t.Fatalf("accepted invalid retrieval vectors %v", vectors)
		}
	}
}

func TestUnit_EmbeddingVerificationRejectsUnsearchableVectors(t *testing.T) {
	for _, vector := range [][]float64{nil, {}, {0, 0}, {math.NaN()}, {math.Inf(1)}, {math.MaxFloat64}} {
		if err := inferencesetup.ValidateEmbedding(vector); err == nil {
			t.Fatalf("accepted invalid vector %v", vector)
		}
	}
	if err := inferencesetup.ValidateEmbedding([]float64{0.25, -0.5, 1}); err != nil {
		t.Fatal(err)
	}
}
