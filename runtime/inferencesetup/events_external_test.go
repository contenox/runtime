package inferencesetup_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/enginesvc"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/llmrepo"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

type configurationBus struct {
	libbus.Messenger
	fail atomic.Bool
}

func (b *configurationBus) Publish(ctx context.Context, subject string, data []byte) error {
	if b.fail.Load() {
		return errors.New("notification transport failed")
	}
	return b.Messenger.Publish(ctx, subject, data)
}

func TestIntegration_EmbeddingNotificationsFollowCommittedConfiguration(t *testing.T) {
	ctx := context.Background()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "roles.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus := &configurationBus{Messenger: libbus.NewInMem()}
	t.Cleanup(func() { require.NoError(t, bus.Close()) })
	state, err := runtimestate.New(ctx, db, bus, runtimestate.WithAutoDiscoverModels())
	require.NoError(t, err)
	script := filepath.Join(t.TempDir(), "embedding.json")
	require.NoError(t, os.WriteFile(script, []byte(`{"model":"scripted-embed","capabilities":{"chat":false,"embed":true},"embed_dimensions":8,"turns":[{"text":"ok"}]}`), 0600))
	require.NoError(t, runtimetypes.New(db.WithoutTransaction()).CreateBackend(ctx, &runtimetypes.Backend{ID: "scripted", Name: "scripted", Type: modelrepo.ScriptedTestBackendType, BaseURL: script}))
	require.NoError(t, state.RunBackendCycle(ctx))
	setup, err := inferencesetup.New(db, state, bus, inferencesetup.RuntimeModelFactory{DB: db, Bus: bus, State: state, Tracker: libtracker.NoopTracker{}})
	require.NoError(t, err)
	engine, err := enginesvc.Build(ctx, db, enginesvc.Config{State: state, Bus: bus, SkipBackendCycle: true})
	require.NoError(t, err)
	t.Cleanup(engine.Stop)
	changes := make(chan []byte, 8)
	sub, err := bus.Stream(ctx, inferencesetup.EmbeddingChangedSubject, changes)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sub.Unsubscribe()) })
	awaitChange := func() {
		t.Helper()
		select {
		case <-changes:
		case <-time.After(time.Second):
			t.Fatal("committed embedding configuration sent no notification")
		}
	}
	require.Error(t, setup.SetRoleEnabled(ctx, inferencesetup.RoleEmbedding, true))
	select {
	case <-changes:
		t.Fatal("failed enablement emitted a change")
	default:
	}
	_, err = setup.Assign(ctx, inferencesetup.Assignment{Role: inferencesetup.RoleEmbedding, Provider: "scripted", Model: "scripted-embed"}, inferencesetup.SourceUser)
	require.NoError(t, err)
	awaitChange()
	require.NoError(t, setup.SetRoleEnabled(ctx, inferencesetup.RoleEmbedding, true))
	awaitChange()
	verified, err := setup.VerifiedRole(ctx, inferencesetup.RoleEmbedding)
	require.NoError(t, err)
	require.True(t, verified.Enabled)
	vector, meta, err := engine.Models.Embed(ctx, llmrepo.EmbedRequest{}, "embed through the engine")
	require.NoError(t, err)
	require.Len(t, vector, 8)
	require.Equal(t, "scripted-embed", meta.ModelName)
	require.Equal(t, script, meta.BackendID)
	require.NoError(t, setup.SetRoleEnabled(ctx, inferencesetup.RoleEmbedding, false))
	awaitChange()
	_, err = setup.VerifiedRole(ctx, inferencesetup.RoleEmbedding)
	require.ErrorIs(t, err, inferencesetup.ErrRoleDisabled)
	_, _, err = engine.Models.Embed(ctx, llmrepo.EmbedRequest{}, "disabled role")
	require.ErrorContains(t, err, "embedding role is turned off")
	bus.fail.Store(true)
	err = setup.SetRoleEnabled(ctx, inferencesetup.RoleEmbedding, true)
	require.ErrorContains(t, err, "configuration saved but notification failed")
	verified, err = setup.VerifiedRole(ctx, inferencesetup.RoleEmbedding)
	require.NoError(t, err)
	require.True(t, verified.Enabled)
}
