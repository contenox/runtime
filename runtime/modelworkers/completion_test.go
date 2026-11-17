package modelworkers_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/backendservice"
	"github.com/contenox/runtime/runtime/clikv"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/modelworkers"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_CompletionProtectionPersistsVerifiedHostAndRejectsChangedSettings(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "runtime.db")
	db, err := libdbexec.NewSQLiteDBManager(ctx, path, runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	connection := &runtimetypes.Backend{ID: "personal-remote", Name: "Personal", Type: "modeld", BaseURL: "gpu.example:4321"}
	require.NoError(t, backendservice.New(db).Create(ctx, connection))
	store := runtimetypes.New(db.WithoutTransaction())
	provider := runtimestate.ConnectionSelector(connection.ID)
	options := modeldconn.WorkerOptions{Target: transport.ExecutionTarget{Mode: transport.PlacementCPU}, Residency: "protected", Memory: "2GiB"}
	raw, err := json.Marshal(options)
	require.NoError(t, err)
	require.NoError(t, clikv.SetString(ctx, store, "inference.modeld.autocomplete", string(raw)))
	require.NoError(t, clikv.SetString(ctx, store, "default-autocomplete-model", "same-name"))
	require.NoError(t, clikv.SetString(ctx, store, "default-autocomplete-provider", provider))
	source := modelworkers.CompletionSource(db)
	_, err = source(ctx, connection.BaseURL)
	require.ErrorContains(t, err, "verify Autocomplete")
	verification, _ := modeldconn.CaptureExecution(ctx)
	repair, err := source(verification, connection.BaseURL)
	require.NoError(t, err)
	require.Equal(t, "retain", repair.Protection.State)
	other, err := source(ctx, "local")
	require.NoError(t, err)
	require.Equal(t, "external", other.Protection.State)
	require.NoError(t, db.Close())
	db, err = libdbexec.NewSQLiteDBManager(ctx, path, runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store = runtimetypes.New(db.WithoutTransaction())
	report := transport.SessionExecution{ModelName: "same-name", OwnerEpoch: "remote-owner", WorkerIdentity: "remote-child",
		Capacity: transport.ModelInfo{Allocation: &transport.NativeAllocation{Resources: admission.Allocation{Identity: "resolved-allocation", Pools: []admission.PoolAllocation{{PoolID: "host", Bytes: 512, LimitBytes: 1024, AvailableBytes: 1024}}}}}}
	require.NoError(t, modelworkers.SaveCompletionProtection(ctx, store, provider, "same-name", []transport.SessionExecution{report, report}))
	require.NoError(t, db.Close())
	db, err = libdbexec.NewSQLiteDBManager(ctx, path, runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	store = runtimetypes.New(db.WithoutTransaction())
	restarted := modelworkers.CompletionSource(db)
	plan, err := restarted(ctx, connection.BaseURL)
	require.NoError(t, err)
	require.Equal(t, connection.BaseURL, plan.Endpoint)
	require.Equal(t, "protected", plan.Protection.State)
	require.Len(t, plan.Protection.Reservations, 1)
	require.Equal(t, "resolved-allocation", plan.Protection.Reservations[0].Identity)
	options.Memory = "3GiB"
	raw, err = json.Marshal(options)
	require.NoError(t, err)
	require.NoError(t, clikv.SetString(ctx, store, "inference.modeld.autocomplete", string(raw)))
	_, err = restarted(ctx, connection.BaseURL)
	require.ErrorContains(t, err, "differs from the current route")
	repair, err = restarted(verification, connection.BaseURL)
	require.NoError(t, err)
	require.Equal(t, "retain", repair.Protection.State)
	other, err = restarted(ctx, "another-gpu.example:4321")
	require.NoError(t, err)
	require.Equal(t, "external", other.Protection.State)
}
