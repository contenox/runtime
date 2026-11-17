package inferencesetup_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/backendservice"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

type assignmentAttemptDB struct {
	libdbexec.DBManager
	attempting chan struct{}
	once       sync.Once
}

func (db *assignmentAttemptDB) WithTransaction(ctx context.Context, rollback ...func()) (libdbexec.Exec, libdbexec.CommitTx, libdbexec.ReleaseTx, error) {
	exec, commit, release, err := db.DBManager.WithTransaction(ctx, rollback...)
	if err != nil {
		return nil, nil, nil, err
	}
	return assignmentAttemptExec{Exec: exec, db: db}, commit, release, nil
}

func (db *assignmentAttemptDB) WithTransactionOptions(ctx context.Context, opts *sql.TxOptions, rollback ...func()) (libdbexec.Exec, libdbexec.CommitTx, libdbexec.ReleaseTx, error) {
	exec, commit, release, err := db.DBManager.WithTransactionOptions(ctx, opts, rollback...)
	if err != nil {
		return nil, nil, nil, err
	}
	return assignmentAttemptExec{Exec: exec, db: db}, commit, release, nil
}

type assignmentAttemptExec struct {
	libdbexec.Exec
	db *assignmentAttemptDB
}

func (exec assignmentAttemptExec) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	exec.db.once.Do(func() { close(exec.db.attempting) })
	return exec.Exec.ExecContext(ctx, query, args...)
}

type commitGateDB struct {
	libdbexec.DBManager
	ready   chan struct{}
	proceed <-chan struct{}
	once    sync.Once
}

func (db *commitGateDB) WithTransaction(ctx context.Context, rollback ...func()) (libdbexec.Exec, libdbexec.CommitTx, libdbexec.ReleaseTx, error) {
	exec, commit, release, err := db.DBManager.WithTransaction(ctx, rollback...)
	if err != nil {
		return nil, nil, nil, err
	}
	return exec, func(commitCtx context.Context) error {
		db.once.Do(func() { close(db.ready) })
		select {
		case <-db.proceed:
			return commit(commitCtx)
		case <-commitCtx.Done():
			return commitCtx.Err()
		}
	}, release, nil
}

func (db *commitGateDB) WithTransactionOptions(ctx context.Context, opts *sql.TxOptions, rollback ...func()) (libdbexec.Exec, libdbexec.CommitTx, libdbexec.ReleaseTx, error) {
	return db.DBManager.WithTransactionOptions(ctx, opts, rollback...)
}

func TestIntegration_AssignWaitsForConcurrentProviderWriteBeforeReadingSnapshot(t *testing.T) {
	ctx := context.Background()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "runtime.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	connection, err := backendservice.New(db).SaveConnection(ctx, backendservice.SaveConnectionRequest{
		Name: "provider", Type: "openai", Endpoint: "http://provider.invalid",
		CredentialChange: backendservice.ReplaceCredentials, APIKey: "fixture-key",
	})
	require.NoError(t, err)
	bus := libbus.NewSQLite(db.WithoutTransaction())
	t.Cleanup(func() { require.NoError(t, bus.Close()) })
	state, err := runtimestate.New(ctx, db, bus)
	require.NoError(t, err)
	factory := modelFactory{repo: &verifyingModels{}}
	setup, err := inferencesetup.New(db, state, bus, factory)
	require.NoError(t, err)
	require.NoError(t, setup.RegisterModel(ctx, inferencesetup.RegisterModelRequest{
		ConnectionID: connection.ID,
		Model:        "chat-model",
		Facts:        runtimetypes.ModelFacts{Registered: true, ContextLength: 8192, Capabilities: []string{"completion"}, CompletionMode: "instruct"},
	}))
	require.NoError(t, state.ReconcileDeclarations(ctx, connection.ID))
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	writeReady := make(chan struct{})
	allowWriteCommit := make(chan struct{})
	var allowOnce sync.Once
	allowCommit := func() { allowOnce.Do(func() { close(allowWriteCommit) }) }
	t.Cleanup(allowCommit)
	providerDB := &commitGateDB{DBManager: db, ready: writeReady, proceed: allowWriteCommit}
	providerWriter, err := inferencesetup.New(providerDB, state, bus, factory)
	require.NoError(t, err)
	providerWrite := make(chan error, 1)
	go func() {
		_, err := providerWriter.SaveProvider(ctx, inferencesetup.ProviderRequest{
			ID: connection.ID, Name: "provider", Provider: "openai", Endpoint: "http://provider.invalid",
		})
		providerWrite <- err
	}()
	select {
	case <-writeReady:
	case <-ctx.Done():
		t.Fatal("provider metadata write did not reach its commit gate")
	}

	attempting := make(chan struct{})
	assignmentDB := &assignmentAttemptDB{DBManager: db, attempting: attempting}
	assignments, err := inferencesetup.New(assignmentDB, state, bus, factory)
	require.NoError(t, err)
	assignment := make(chan error, 1)
	go func() {
		_, err := assignments.AssignAll(ctx, []inferencesetup.Assignment{{
			Role: inferencesetup.RoleChat, Provider: "openai", Model: "chat-model",
		}}, inferencesetup.SourceUser)
		assignment <- err
	}()
	select {
	case <-attempting:
	case <-ctx.Done():
		allowCommit()
		t.Fatal("assignment did not attempt its write reservation")
	}
	allowCommit()

	select {
	case err := <-providerWrite:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("provider metadata write did not commit")
	}
	select {
	case err := <-assignment:
		require.ErrorIs(t, err, inferencesetup.ErrConfigurationChanged)
	case <-ctx.Done():
		t.Fatal("assignment did not review the provider configuration after the write committed")
	}

	roles, err := assignments.Roles(ctx)
	require.NoError(t, err)
	var chat inferencesetup.RoleStatus
	for _, role := range roles {
		if role.Role == inferencesetup.RoleChat {
			chat = role
		}
	}
	require.Empty(t, chat.Model)
	require.Nil(t, chat.Verification)
}
