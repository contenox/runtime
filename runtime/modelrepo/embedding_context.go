package modelrepo

import "context"

type EmbeddingPurpose string

const (
	EmbeddingDocument EmbeddingPurpose = "document"
	EmbeddingQuery    EmbeddingPurpose = "query"
)

type embeddingPurposeKey struct{}

func WithEmbeddingPurpose(ctx context.Context, purpose EmbeddingPurpose) context.Context {
	return context.WithValue(ctx, embeddingPurposeKey{}, purpose)
}

func EmbeddingPurposeFromContext(ctx context.Context) EmbeddingPurpose {
	if purpose, ok := ctx.Value(embeddingPurposeKey{}).(EmbeddingPurpose); ok {
		return purpose
	}
	return EmbeddingDocument
}
