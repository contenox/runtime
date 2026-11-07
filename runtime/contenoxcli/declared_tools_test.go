package contenoxcli

import (
	"context"
	"path/filepath"
	"sort"
	"testing"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/agentdecl"
	"github.com/contenox/runtime/runtime/connectorservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

type recordingWorkers struct {
	started []string
	stopped []string
}

func (r *recordingWorkers) StartWorker(_ context.Context, srv *runtimetypes.MCPServer) error {
	r.started = append(r.started, srv.Name)
	return nil
}

func (r *recordingWorkers) StopWorker(_ context.Context, name string) {
	r.stopped = append(r.stopped, name)
}

func names[T any](rows []*T, name func(*T) string) []string {
	var out []string
	for _, r := range rows {
		out = append(out, name(r))
	}
	sort.Strings(out)
	return out
}

func TestUnit_DeclaredToolsFollowTheAgentFiles(t *testing.T) {
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "decl.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	store := runtimetypes.New(db.WithoutTransaction())
	require.NoError(t, store.CreateMCPServer(ctx, &runtimetypes.MCPServer{Name: "mine", Transport: "stdio", Command: "x", ConnectTimeoutSeconds: 30}))
	workers := &recordingWorkers{}

	results := []agentdecl.SyncResult{
		{
			Name:   "reviewer",
			MCP:    []agentdecl.DeclaredMCPServer{{Declared: "filesystem", Transport: "stdio", Command: "npx", Args: []string{"-y", "fs"}}},
			Remote: []agentdecl.DeclaredRemoteTool{{Declared: "billing", EndpointURL: "https://billing.example"}},
		},
		{Name: "broken", Action: agentdecl.ActionRefused, MCP: []agentdecl.DeclaredMCPServer{{Declared: "x", Transport: "stdio", Command: "x"}}},
	}
	require.NoError(t, syncDeclaredTools(ctx, store, workers, results))

	mcpName := runtimetypes.DeclaredToolName("reviewer", "filesystem")
	remoteName := runtimetypes.DeclaredToolName("reviewer", "billing")
	servers, err := connectorservice.AllMCPServers(ctx, store)
	require.NoError(t, err)
	require.Equal(t, []string{mcpName, "mine"}, names(servers, func(s *runtimetypes.MCPServer) string { return s.Name }))
	remotes, err := connectorservice.AllRemoteTools(ctx, store)
	require.NoError(t, err)
	require.Equal(t, []string{remoteName}, names(remotes, func(s *runtimetypes.RemoteTools) string { return s.Name }))
	require.Equal(t, 10000, remotes[0].TimeoutMs)
	require.Equal(t, []string{mcpName}, workers.started)

	require.NoError(t, syncDeclaredTools(ctx, store, workers, results))
	require.Equal(t, []string{mcpName}, workers.started, "an unchanged declaration does not restart its server")

	results[0].MCP[0].Args = []string{"-y", "fs", "/data"}
	require.NoError(t, syncDeclaredTools(ctx, store, workers, results))
	got, err := store.GetMCPServerByName(ctx, mcpName)
	require.NoError(t, err)
	require.Equal(t, []string{"-y", "fs", "/data"}, got.Args)
	require.Len(t, workers.started, 2)

	require.NoError(t, syncDeclaredTools(ctx, store, workers, nil))
	servers, err = connectorservice.AllMCPServers(ctx, store)
	require.NoError(t, err)
	require.Equal(t, []string{"mine"}, names(servers, func(s *runtimetypes.MCPServer) string { return s.Name }), "operator servers stay")
	remotes, err = connectorservice.AllRemoteTools(ctx, store)
	require.NoError(t, err)
	require.Empty(t, remotes)
	require.Equal(t, []string{mcpName}, workers.stopped)
}
