package acpsvc

import (
	"testing"

	libacp "github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_SessionModelSurvivesReload(t *testing.T) {
	ctx, db := setupConfigOptionsDB(t)
	tr := &Transport{
		deps:            Deps{DB: db},
		defaultProvider: "openai",
		defaultModel:    "gpt-5-mini",
	}
	tr.persistSessionModel(ctx, libacp.SessionID("acp-1"), "connection:b1", "amazon.nova-pro-v1:0")

	entry := &sessionEntry{Provider: "openai", Model: "gpt-5-mini"}
	tr.restoreSessionModel(ctx, runtimetypes.New(db.WithoutTransaction()), libacp.SessionID("acp-1"), entry)
	require.Equal(t, "connection:b1", entry.Provider)
	require.Equal(t, "amazon.nova-pro-v1:0", entry.Model)

	other := &sessionEntry{Provider: "openai", Model: "gpt-5-mini"}
	tr.restoreSessionModel(ctx, runtimetypes.New(db.WithoutTransaction()), libacp.SessionID("acp-2"), other)
	require.Equal(t, "gpt-5-mini", other.Model, "a session that never chose a model keeps the default")
}
