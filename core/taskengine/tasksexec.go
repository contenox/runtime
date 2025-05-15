package taskengine

import "context"

type StringExec interface {
	Generate(ctx context.Context, input string) (string, error)
}

type NumberExec interface {
	Number(ctx context.Context, input string) (int, error)
}

type ScoreExec interface {
	Score(ctx context.Context, input string) (float64, error)
}

type ConditionExec interface {
	Contidion(ctx context.Context, input string) (bool, error)
}
