package enginesvc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/agentdecl"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/mcpworker"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

type execRecorder struct {
	taskengine.ToolsRepo
	hits *[]string
	tag  string
}

func TestDesktopBrowserSelectedProfileGatesDispatch(t *testing.T) {
	dir := t.TempDir()
	launcher := []string{filepath.Join(dir, "desktop"), filepath.Join(dir, "browser-mcp.cjs"), filepath.Join(dir, "endpoint.json")}
	encoded, err := json.Marshal(launcher)
	require.NoError(t, err)
	t.Setenv("CONTENOX_DESKTOP_BROWSER", string(encoded))
	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)
	for _, name := range []string{"default", "strict"} {
		policy, err := agentdecl.RenderEnvelopePolicy(cfg, name, "")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, agentdecl.EnvelopePolicyFile(name)), policy, 0600))
	}
	ctx, cancel := context.WithCancel(t.Context())
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(dir, "local.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	defer db.Close()
	defer cancel()
	bus := libbus.NewInMem()
	store := runtimetypes.New(db.WithoutTransaction())
	var asks []string
	_, _, repo, err := buildTools(ctx, Config{EnableHITL: true, HITLPolicySource: hitlservice.NewFSPolicySource(dir),
		AskApproval: func(ctx context.Context, req hitlservice.ApprovalRequest) (bool, error) {
			asks = append(asks, req.ToolName)
			if req.ToolName == "navigate" {
				server, err := store.GetMCPServerByName(ctx, "browser")
				if err != nil {
					return false, err
				}
				server.Workspace = dir
				return true, store.UpdateMCPServer(ctx, server)
			}
			return false, nil
		}}, db, libtracker.NoopTracker{}, bus)
	require.NoError(t, err)
	server := &runtimetypes.MCPServer{Name: "browser", Transport: "stdio", Command: launcher[0],
		Args: []string{launcher[1], launcher[2], "51832800-606c-464a-9d56-4f1d0adabae2"}, Env: map[string]string{"ELECTRON_RUN_AS_NODE": "1"}}
	require.NoError(t, store.CreateMCPServer(ctx, server))
	// Observe the same wire request the MCP worker receives, without starting an external process.
	calls := make(chan mcpworker.MCPToolRequest, 3)
	_, err = bus.Serve(ctx, mcpworker.SubjectExecute("browser"), func(_ context.Context, data []byte) ([]byte, error) {
		var req mcpworker.MCPToolRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return nil, err
		}
		calls <- req
		return json.Marshal(mcpworker.MCPToolReply{Result: "visible page"})
	})
	require.NoError(t, err)
	call := func(profile, tool string) error {
		_, _, err := repo.Exec(hitlservice.WithPolicyName(ctx, agentdecl.EnvelopePolicyFile(profile)), time.Now(),
			map[string]any{}, false, &taskengine.ToolsCall{Name: "browser", ToolName: tool})
		return err
	}
	require.NoError(t, call("default", "snapshot"))
	require.Len(t, calls, 1)
	require.Equal(t, server.DesktopBrowserKey(), (<-calls).DesktopBrowser)
	require.Error(t, call("default", "tab_audio"))
	require.Error(t, call("strict", "screenshot"))
	require.ErrorContains(t, call("default", "navigate"), "Browser registration changed")
	require.Equal(t, []string{"tab_audio", "navigate"}, asks)
	require.Empty(t, calls)
}

func (e execRecorder) Exec(_ context.Context, _ time.Time, _ any, _ bool, _ *taskengine.ToolsCall) (any, taskengine.DataType, error) {
	*e.hits = append(*e.hits, e.tag)
	return "ok", taskengine.DataTypeString, nil
}

func TestUnit_HITLExempt_MissionProviderBypassesGate(t *testing.T) {
	var hits []string
	gated := execRecorder{hits: &hits, tag: "gated"}
	raw := execRecorder{hits: &hits, tag: "raw"}
	repo := hitlExemptProviders(gated, raw, "mission")

	now := time.Now()
	_, _, err := repo.Exec(context.Background(), now, nil, false, &taskengine.ToolsCall{Name: "mission", ToolName: "mission_report"})
	require.NoError(t, err)
	_, _, err = repo.Exec(context.Background(), now, nil, false, &taskengine.ToolsCall{Name: "local_fs", ToolName: "read_file"})
	require.NoError(t, err)
	_, _, err = repo.Exec(context.Background(), now, nil, false, nil)
	require.NoError(t, err)

	require.Equal(t, []string{"raw", "gated", "gated"}, hits,
		"mission provider bypasses the gate; every other call — including a nil ToolsCall — stays gated")
}
