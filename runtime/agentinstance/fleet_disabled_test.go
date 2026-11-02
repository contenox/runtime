package agentinstance

import (
	"context"
	"path/filepath"
	"testing"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/agentregistryservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_ManagerList_HidesDisabledAgentsWithNothingRunning(t *testing.T) {
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "fleet.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	svc := agentregistryservice.New(db)
	register := func(name string, enabled bool) {
		agent := &runtimetypes.Agent{Name: name, Enabled: enabled}
		require.NoError(t, agent.SetExternalACPConfig(runtimetypes.ExternalACPConfig{Transport: runtimetypes.ExternalACPTransportStdio, Command: "x"}))
		require.NoError(t, svc.Create(ctx, agent))
		if !enabled {
			agent.Enabled = false
			require.NoError(t, svc.Update(ctx, agent))
		}
	}
	register("agent-reviewer", true)
	register("retired-chain", false)

	mgr := New(svc)
	t.Cleanup(func() { _ = mgr.Close() })
	entries, err := mgr.List(ctx)
	require.NoError(t, err)

	var names []string
	for _, e := range entries {
		names = append(names, e.AgentName)
	}
	require.Equal(t, []string{"agent-reviewer"}, names, "a disabled agent with no live unit is not offered on the fleet")
}
