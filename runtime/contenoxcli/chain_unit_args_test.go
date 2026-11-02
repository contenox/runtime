package contenoxcli

import (
	"testing"

	"github.com/contenox/runtime/runtime/acpsvc"
	"github.com/stretchr/testify/require"
)

func TestUnit_chainUnitArgsCarriesDatabaseDataDirAndPinnedModels(t *testing.T) {
	got := chainUnitArgs("/appdata/local.db", "/appdata/data", acpsvc.LiveDefaults{
		Provider:    "ollama",
		Model:       "qwen3",
		AltProvider: "openai",
		AltModel:    "gpt-5",
	})
	require.Equal(t, []string{
		"--db", "/appdata/local.db",
		"--data-dir", "/appdata/data",
		"--provider", "ollama",
		"--model", "qwen3",
		"--alt-provider", "openai",
		"--alt-model", "gpt-5",
	}, got)
}

func TestUnit_chainUnitArgsOmitsUnpinnedModels(t *testing.T) {
	got := chainUnitArgs("/appdata/local.db", "/appdata/data", acpsvc.LiveDefaults{MaxTokens: "100", Think: "high"})
	require.Equal(t, []string{"--db", "/appdata/local.db", "--data-dir", "/appdata/data"}, got)
}
