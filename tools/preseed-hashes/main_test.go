package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnit_ReadTableMissingFileIsEmpty(t *testing.T) {
	table, err := readTable(filepath.Join(t.TempDir(), "missing.go"))
	require.NoError(t, err)
	require.Empty(t, table)
}

func TestUnit_WriteTableThenReadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "table.go")
	want := map[string][]string{
		"agents.toml":       {"aaaa", "bbbb"},
		"agents/README.md":  {"cccc"},
		"agents/acp/run.md": {"dddd", "eeee", "ffff"},
	}
	require.NoError(t, writeTable(path, want))

	got, err := readTable(path)
	require.NoError(t, err)
	require.Equal(t, want, got)
}
