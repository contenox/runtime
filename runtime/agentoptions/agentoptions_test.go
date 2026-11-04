package agentoptions_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/libacp"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/agentoptions"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func modeOption() libacp.SessionConfigOption {
	return libacp.SessionConfigOption{
		ID:           "mode",
		Name:         "Mode",
		Category:     agentoptions.CategoryMode,
		Type:         "select",
		CurrentValue: "default",
		Options:      libacp.NewSessionConfigValues([]libacp.SessionConfigValue{{Value: "default"}, {Value: "plan"}, {Value: "bypassPermissions"}}),
	}
}

func TestUnit_AgentOptions_SaveAndLoad(t *testing.T) {
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "opts.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	store := runtimetypes.New(db.WithoutTransaction())

	require.Nil(t, agentoptions.Load(ctx, store, "claude-code"), "nothing remembered yet")
	require.NoError(t, agentoptions.Save(ctx, store, "claude-code", []libacp.SessionConfigOption{modeOption()}))
	got := agentoptions.Load(ctx, store, "claude-code")
	require.Len(t, got, 1)
	require.Equal(t, "mode", got[0].ID)
	require.Len(t, got[0].Options.AllValues(), 3)

	require.NoError(t, agentoptions.Save(ctx, store, "claude-code", nil), "an empty save keeps what was remembered")
	require.Len(t, agentoptions.Load(ctx, store, "claude-code"), 1)
}

func TestUnit_AgentOptions_RestrictModes(t *testing.T) {
	got := agentoptions.RestrictModes([]libacp.SessionConfigOption{modeOption()}, []string{"default", "plan"}, "")
	var values []string
	for _, v := range got[0].Options.AllValues() {
		values = append(values, v.Value)
	}
	require.Equal(t, []string{"default", "plan"}, values)
	require.Len(t, agentoptions.RestrictModes([]libacp.SessionConfigOption{modeOption()}, nil, "")[0].Options.AllValues(), 3)
}
