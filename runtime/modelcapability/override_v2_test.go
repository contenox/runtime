package modelcapability_test

import (
	"context"
	"path/filepath"
	"testing"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"

	"github.com/contenox/runtime/runtime/modelcapability"
)

func newTestServiceV2(t *testing.T) (context.Context, modelcapability.Service) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "capability.db")
	db, err := libdb.NewSQLiteDBManager(ctx, path, runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return ctx, modelcapability.New(runtimetypes.New(db.WithoutTransaction()))
}

func TestUnit_ServiceV2_SetGetUnsetThink(t *testing.T) {
	ctx, svc := newTestServiceV2(t)

	override, err := svc.SetThink(ctx, "OpenAI", "gpt-5-mini", true)
	require.NoError(t, err)
	require.Equal(t, "openai", override.Provider)
	require.Equal(t, "gpt-5-mini", override.Model)
	require.NotNil(t, override.CanThink)
	require.True(t, *override.CanThink)

	got, ok, err := svc.Get(ctx, "openai", "gpt-5-mini")
	require.NoError(t, err)
	require.True(t, ok)
	require.NotNil(t, got.CanThink)
	require.True(t, *got.CanThink)

	removed, err := svc.Unset(ctx, "openai", "gpt-5-mini")
	require.NoError(t, err)
	require.True(t, removed)

	_, ok, err = svc.Get(ctx, "openai", "gpt-5-mini")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestUnit_ServiceV2_FalseOverrideIsPreserved(t *testing.T) {
	ctx, svc := newTestServiceV2(t)

	_, err := svc.SetThink(ctx, "vllm", "Qwen/Qwen3-32B", false)
	require.NoError(t, err)

	got, ok, err := svc.Get(ctx, "VLLM", "Qwen/Qwen3-32B")
	require.NoError(t, err)
	require.True(t, ok)
	require.NotNil(t, got.CanThink)
	require.False(t, *got.CanThink)
}

func TestUnit_ServiceV2_SetGetUnsetVision(t *testing.T) {
	ctx, svc := newTestServiceV2(t)

	override, err := svc.SetVision(ctx, "Ollama", "gemma3:12b", true)
	require.NoError(t, err)
	require.Equal(t, "ollama", override.Provider)
	require.Equal(t, "gemma3:12b", override.Model)
	require.NotNil(t, override.CanVision)
	require.True(t, *override.CanVision)
	require.Nil(t, override.CanThink)

	got, ok, err := svc.Get(ctx, "ollama", "gemma3:12b")
	require.NoError(t, err)
	require.True(t, ok)
	require.NotNil(t, got.CanVision)
	require.True(t, *got.CanVision)

	removed, err := svc.Unset(ctx, "ollama", "gemma3:12b")
	require.NoError(t, err)
	require.True(t, removed)

	_, ok, err = svc.Get(ctx, "ollama", "gemma3:12b")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestUnit_ServiceV2_ThinkAndVisionOverridesMerge(t *testing.T) {
	ctx, svc := newTestServiceV2(t)

	_, err := svc.SetThink(ctx, "openai", "gpt-5-mini", true)
	require.NoError(t, err)
	_, err = svc.SetVision(ctx, "openai", "gpt-5-mini", false)
	require.NoError(t, err)

	got, ok, err := svc.Get(ctx, "openai", "gpt-5-mini")
	require.NoError(t, err)
	require.True(t, ok)
	require.NotNil(t, got.CanThink, "setting vision must not clobber the think override")
	require.True(t, *got.CanThink)
	require.NotNil(t, got.CanVision)
	require.False(t, *got.CanVision)

	_, err = svc.SetThink(ctx, "openai", "gpt-5-mini", false)
	require.NoError(t, err)
	got, ok, err = svc.Get(ctx, "openai", "gpt-5-mini")
	require.NoError(t, err)
	require.True(t, ok)
	require.False(t, *got.CanThink)
	require.NotNil(t, got.CanVision, "setting think must not clobber the vision override")
	require.False(t, *got.CanVision)
}

func TestUnit_ServiceV2_RejectsEmptyProviderOrModel(t *testing.T) {
	ctx, svc := newTestServiceV2(t)

	_, err := svc.SetThink(ctx, "", "gpt-5", true)
	require.ErrorContains(t, err, "provider is required")

	_, err = svc.SetThink(ctx, "openai", "  ", true)
	require.ErrorContains(t, err, "model is required")
}

func TestUnit_ServiceV2_OverrideIsFoundUnderEitherSpellingOfTheModelID(t *testing.T) {
	ctx, svc := newTestServiceV2(t)

	_, err := svc.SetCompletion(ctx, "gemini", "models/flash-fixture", "instruct")
	require.NoError(t, err)

	got, ok, err := svc.Get(ctx, "gemini", "flash-fixture")
	require.NoError(t, err)
	require.True(t, ok)
	require.NotNil(t, got.CompletionMode)
	require.Equal(t, "instruct", *got.CompletionMode)

	_, err = svc.SetThink(ctx, "gemini", "flash-fixture", true)
	require.NoError(t, err)
	got, ok, err = svc.Get(ctx, "gemini", "flash-fixture")
	require.NoError(t, err)
	require.True(t, ok)
	require.NotNil(t, got.CanThink, "the later declaration is kept")
	require.NotNil(t, got.CompletionMode, "setting one capability keeps the earlier declaration made under the other spelling")
	require.Equal(t, "instruct", *got.CompletionMode)

	removed, err := svc.Unset(ctx, "gemini", "flash-fixture")
	require.NoError(t, err)
	require.True(t, removed)
	for _, spelling := range []string{"flash-fixture", "models/flash-fixture"} {
		_, ok, err = svc.Get(ctx, "gemini", spelling)
		require.NoError(t, err)
		require.False(t, ok, spelling)
	}
}
