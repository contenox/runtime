package acpsvc

import (
	"context"
	"testing"

	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/statetype"
	"github.com/stretchr/testify/require"
)

func TestUnit_LiveDefaultsFollowDBConfig(t *testing.T) {
	ctx, db := setupConfigOptionsDB(t)
	store := runtimetypes.New(db.WithoutTransaction())
	calls := 0
	tr := &Transport{deps: Deps{
		DB: db,
		LiveDefaults: func(ctx context.Context) LiveDefaults {
			calls++
			return LiveDefaults{
				Model:       ReadConfigValue(ctx, db, "default-model"),
				Provider:    ReadConfigValue(ctx, db, "default-provider"),
				AltModel:    ReadConfigValue(ctx, db, "default-alt-model"),
				AltProvider: ReadConfigValue(ctx, db, "default-alt-provider"),
				MaxTokens:   ReadConfigValue(ctx, db, "default-max-tokens"),
			}
		},
	}}
	// The model option is grouped by connection, so the option can only name a
	// value when a connection actually serves that model.
	tr.statesFn = func() []statetype.BackendRuntimeState {
		return []statetype.BackendRuntimeState{{
			Backend:      runtimetypes.Backend{ID: "o1", Name: "openai", Type: "openai"},
			PulledModels: []statetype.ModelPullStatus{{Model: "gpt-5", CanChat: true}},
		}}
	}
	sess := &sessionEntry{driver: &nativeDriver{t: tr}}

	// Nothing configured yet: the provider/model vars are absent.
	require.Empty(t, tr.chainTemplateVars(sess)["provider"])

	// Another process (e.g. PUT /api/cli-config) saves defaults.
	for k, v := range map[string]string{
		"default-model": "gpt-5", "default-provider": "openai",
		"default-alt-model": "gpt-5-mini", "default-alt-provider": "openai",
		"default-max-tokens": "2048",
	} {
		require.NoError(t, clikv.WriteConfig(ctx, store, tr.workspaceID(), k, v))
	}

	// Within the TTL the cached snapshot is reused: no DB storm.
	before := calls
	_ = tr.chainTemplateVars(sess)
	_ = tr.chainTemplateVars(sess)
	require.Equal(t, before, calls)

	tr.invalidateDefaults() // stands in for the TTL elapsing
	vars := tr.chainTemplateVars(sess)
	require.Equal(t, "openai", vars["provider"])
	require.Equal(t, "gpt-5", vars["model"])
	require.Equal(t, "gpt-5-mini", vars["alt_model"])
	require.Equal(t, "2048", vars["max_tokens"])

	// The ACP model config option reflects the live default.
	require.Equal(t, "connection:o1/gpt-5", tr.modelConfigOption(ctx, sess).CurrentValue)

	// /model persists and updates immediately; it wins over the stale cache.
	_, err := tr.handleModel(ctx, "gpt-5-nano")
	require.NoError(t, err)
	require.Equal(t, "gpt-5-nano", tr.chainTemplateVars(sess)["model"])
	tr.invalidateDefaults()
	require.Equal(t, "gpt-5-nano", tr.chainTemplateVars(sess)["model"])

	// A session-level selection still beats the default.
	pinned := &sessionEntry{Model: "m", Provider: "p"}
	require.Equal(t, "m", tr.chainTemplateVars(pinned)["model"])
	require.Equal(t, "p", tr.chainTemplateVars(pinned)["provider"])
}

func TestUnit_HandleModel_ProviderQualifiedValueSetsBothDefaults(t *testing.T) {
	ctx, db := setupConfigOptionsDB(t)
	tr := &Transport{deps: Deps{DB: db}}
	tr.statesFn = func() []statetype.BackendRuntimeState {
		return []statetype.BackendRuntimeState{
			conn("d1", "deepseek", "deepseek", "", chat("deepseek-flash", 1_000_000)),
		}
	}

	reply, err := tr.handleModel(ctx, "deepseek/deepseek-flash")
	require.NoError(t, err)
	require.Contains(t, reply, "deepseek-flash")
	require.Equal(t, "deepseek-flash", ReadConfigValue(ctx, db, "default-model"))
	require.Equal(t, "deepseek", ReadConfigValue(ctx, db, "default-provider"), "a picked provider/model pins both defaults")
}
