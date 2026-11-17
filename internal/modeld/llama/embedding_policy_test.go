package llama

import "testing"

func TestUnit_EmbeddingLayerBudgetHasNoGenerationContextFloor(t *testing.T) {
	for _, tc := range []struct {
		budget  int64
		ceiling int
		want    int
	}{{1000, 10, 10}, {400, 10, 4}, {400, 2, 2}, {0, 10, 0}} {
		if got := embeddingLayersForBudget(1000, 9, tc.ceiling, tc.budget); got != tc.want {
			t.Fatalf("budget=%d cap=%d got=%d want=%d", tc.budget, tc.ceiling, got, tc.want)
		}
	}
}
