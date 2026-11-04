package externalagents

import (
	"context"
	"path/filepath"
	"testing"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/agentregistryservice"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_NodePlatformNames(t *testing.T) {
	require.Equal(t, "win32", nodePlatform("windows"))
	require.Equal(t, "linux", nodePlatform("linux"))
	require.Equal(t, "darwin", nodePlatform("darwin"))
	require.Equal(t, "x64", nodeArch("amd64"))
	require.Equal(t, "arm64", nodeArch("arm64"))
}

func TestUnit_ClaudePresetFollowsUnofficialSignIns(t *testing.T) {
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "flag.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	home, data := t.TempDir(), t.TempDir()
	svc := New(Deps{Agents: agentregistryservice.New(db), DB: db, Home: home, ContenoxDir: data})
	p, ok := presetByID(PresetClaudeCode)
	require.True(t, ok)

	require.True(t, p.isUnofficialMethod("claude-ai-login"))
	require.False(t, p.isUnofficialMethod("console-login"))

	require.False(t, svc.unofficialAllowed(ctx), "off until the operator opts in")
	require.Equal(t, filepath.Join(data, "external-agents", PresetClaudeCode, "home"), svc.presetConfigDir(ctx, p),
		"without the opt-in the preset never reads the operator's own Claude login")

	require.NoError(t, clikv.SetString(ctx, runtimetypes.New(db.WithoutTransaction()), clikv.UnofficialSignInsKey, "true"))
	require.True(t, svc.unofficialAllowed(ctx))
	require.Equal(t, filepath.Join(home, ".claude"), svc.presetConfigDir(ctx, p))
}
