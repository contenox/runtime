package ollamatokenizer

import (
	"context"
	"unicode/utf8"
)

type EstimateTokenizer struct{}

var _ Tokenizer = (*EstimateTokenizer)(nil)

func NewEstimateTokenizer() *EstimateTokenizer {
	return &EstimateTokenizer{}
}

func (e *EstimateTokenizer) Tokenize(ctx context.Context, modelName string, prompt string) ([]int, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	n := estimateTokens(prompt)
	tokens := make([]int, n)
	for i := range n {
		tokens[i] = i + 1
	}
	return tokens, nil
}

func (e *EstimateTokenizer) CountTokens(ctx context.Context, modelName string, prompt string) (int, error) {
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	return estimateTokens(prompt), nil
}

func (e *EstimateTokenizer) OptimalModel(ctx context.Context, baseModel string) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return baseModel, nil
}

func estimateTokens(s string) int {
	r := utf8.RuneCountInString(s)
	if r == 0 {
		return 0
	}
	n := r / 4
	if n < 1 {
		n = 1
	}
	return n
}
