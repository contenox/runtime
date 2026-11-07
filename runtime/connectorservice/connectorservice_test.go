package connectorservice

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"
)

type fakeTools struct {
	names []string
	tools map[string][]taskengine.Tool
	fail  map[string]error
}

func (f *fakeTools) Supports(ctx context.Context) ([]string, error) {
	_, scoped := taskengine.RuntimeToolsAllowlistFromContext(ctx)
	var out []string
	for _, n := range f.names {
		if runtimetypes.IsACPManagedMCPServerName(n) && !scoped {
			continue
		}
		out = append(out, n)
	}
	return out, nil
}

func (f *fakeTools) GetSchemasForSupportedTools(context.Context) (map[string]*openapi3.T, error) {
	return nil, nil
}

func (f *fakeTools) GetToolsForToolsByName(_ context.Context, name string) ([]taskengine.Tool, error) {
	if err := f.fail[name]; err != nil {
		return nil, err
	}
	return f.tools[name], nil
}

func setup(t *testing.T) (context.Context, libdb.DBManager, runtimetypes.Store) {
	t.Helper()
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "connectors.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return ctx, db, runtimetypes.New(db.WithoutTransaction())
}

func stdio(name, workspace string) *runtimetypes.MCPServer {
	return &runtimetypes.MCPServer{Name: name, Transport: "stdio", Command: "srv", Workspace: workspace, Env: map[string]string{"TOKEN": "x"}, ConnectTimeoutSeconds: 30}
}

func tool(name string) taskengine.Tool {
	return taskengine.Tool{Type: "function", Function: taskengine.FunctionTool{Name: name, Description: name + " tool"}}
}

func TestUnit_WorkspaceCovers(t *testing.T) {
	root := t.TempDir()
	require.True(t, runtimetypes.WorkspaceCovers("", ""))
	require.True(t, runtimetypes.WorkspaceCovers(root, root))
	require.True(t, runtimetypes.WorkspaceCovers(root, filepath.Join(root, "sub", "dir")))
	require.False(t, runtimetypes.WorkspaceCovers(root, filepath.Dir(root)))
	require.False(t, runtimetypes.WorkspaceCovers(root, root+"-other"))
	require.False(t, runtimetypes.WorkspaceCovers(root, ""))
}

func TestUnit_StoreKeepsEnvAndWorkspace(t *testing.T) {
	ctx, _, store := setup(t)
	ws := t.TempDir()
	require.NoError(t, store.CreateMCPServer(ctx, stdio("fs", ws)))
	got, err := store.GetMCPServerByName(ctx, "fs")
	require.NoError(t, err)
	require.Equal(t, ws, got.Workspace)
	require.Equal(t, map[string]string{"TOKEN": "x"}, got.Env)

	require.NoError(t, store.CreateRemoteTools(ctx, &runtimetypes.RemoteTools{Name: "mail", EndpointURL: "http://x", TimeoutMs: 1000, Workspace: ws}))
	r, err := store.GetRemoteToolsByName(ctx, "mail")
	require.NoError(t, err)
	require.Equal(t, ws, r.Workspace)
}

func TestUnit_AllowlistScopesByWorkspaceSessionAndSwitches(t *testing.T) {
	ctx, _, store := setup(t)
	ws := t.TempDir()
	require.NoError(t, store.CreateMCPServer(ctx, stdio("everywhere", "")))
	require.NoError(t, store.CreateMCPServer(ctx, stdio("project", ws)))
	require.NoError(t, store.CreateMCPServer(ctx, stdio("acp-aaaa-mine", "")))
	require.NoError(t, store.CreateMCPServer(ctx, stdio("acp-bbbb-theirs", "")))
	require.NoError(t, store.CreateRemoteTools(ctx, &runtimetypes.RemoteTools{Name: "api", EndpointURL: "http://x", TimeoutMs: 1000, Workspace: ws}))

	inside, err := Allowlist(ctx, store, filepath.Join(ws, "src"), "chat-1", []string{"acp-aaaa-mine"})
	require.NoError(t, err)
	require.Contains(t, inside, "*")
	require.NotContains(t, inside, "!project")
	require.NotContains(t, inside, "!api")
	require.NotContains(t, inside, "!acp-aaaa-mine")
	require.Contains(t, inside, "!acp-bbbb-theirs")

	outside, err := Allowlist(ctx, store, t.TempDir(), "chat-1", nil)
	require.NoError(t, err)
	require.Contains(t, outside, "!project")
	require.Contains(t, outside, "!api")
	require.NotContains(t, outside, "!everywhere")

	require.NoError(t, SetEnabled(ctx, store, "chat-1", "everywhere", false))
	require.NoError(t, SetEnabled(ctx, store, "chat-1", "local_shell", false))
	off, err := Allowlist(ctx, store, ws, "chat-1", nil)
	require.NoError(t, err)
	require.Contains(t, off, "!everywhere")
	require.Contains(t, off, "!local_shell")

	other, err := Allowlist(ctx, store, ws, "chat-2", nil)
	require.NoError(t, err)
	require.NotContains(t, other, "!everywhere", "a switch belongs to one chat")

	require.NoError(t, SetEnabled(ctx, store, "chat-1", "everywhere", true))
	require.NoError(t, SetEnabled(ctx, store, "chat-1", "local_shell", true))
	require.Empty(t, Disabled(ctx, store, "chat-1"))
}

func TestUnit_ListDescribesScopesAndStatus(t *testing.T) {
	ctx, db, store := setup(t)
	ws := t.TempDir()
	require.NoError(t, store.CreateMCPServer(ctx, stdio("everywhere", "")))
	require.NoError(t, store.CreateMCPServer(ctx, stdio("project", ws)))
	require.NoError(t, store.CreateMCPServer(ctx, stdio("acp-aaaa-mine", "")))
	require.NoError(t, store.CreateMCPServer(ctx, &runtimetypes.MCPServer{Name: "remote", Transport: "http", URL: "https://mcp.example/mcp", AuthType: "oauth", ConnectTimeoutSeconds: 30}))
	require.NoError(t, store.CreateRemoteTools(ctx, &runtimetypes.RemoteTools{Name: "api", EndpointURL: "http://x", TimeoutMs: 1000}))
	tools := &fakeTools{
		names: []string{"local_shell", "everywhere", "project", "acp-aaaa-mine", "remote", "api"},
		tools: map[string][]taskengine.Tool{"local_shell": {tool("run")}, "everywhere": {tool("read"), tool("write")}, "project": {tool("q")}, "acp-aaaa-mine": {tool("m")}, "api": {tool("send")}},
		fail:  map[string]error{"remote": errors.New("mcp oauth not authenticated")},
	}
	svc := New(db, tools)

	all, err := svc.List(ctx, Filter{})
	require.NoError(t, err)
	byName := map[string]Connector{}
	for _, c := range all {
		byName[c.Name] = c
	}
	require.NotContains(t, byName, "acp-aaaa-mine", "chat connectors are not listed system-wide")
	require.Equal(t, ScopeEverywhere, byName["everywhere"].Scope)
	require.Len(t, byName["everywhere"].Tools, 2)
	require.Equal(t, StatusReady, byName["everywhere"].Status)
	require.Equal(t, ScopeWorkspace, byName["project"].Scope)
	require.Equal(t, ws, byName["project"].Workspace)
	require.Equal(t, KindOpenAPI, byName["api"].Kind)
	require.Equal(t, KindBuiltin, byName["local_shell"].Kind)
	require.Equal(t, StatusNeedsSignIn, byName["remote"].Status)
	require.Equal(t, "local_shell", all[len(all)-1].Name, "built-in tools come last")

	chat, err := svc.List(ctx, Filter{Session: true, Cwd: t.TempDir(), SessionNames: []string{"acp-aaaa-mine"}})
	require.NoError(t, err)
	names := map[string]Connector{}
	for _, c := range chat {
		names[c.Name] = c
	}
	require.NotContains(t, names, "project", "a workspace connector stays in its folder")
	require.Equal(t, ScopeSession, names["acp-aaaa-mine"].Scope)
	require.Equal(t, "mine", names["acp-aaaa-mine"].Label)

	checked, err := svc.Check(ctx, "remote")
	require.NoError(t, err)
	require.Equal(t, StatusNeedsSignIn, checked.Status)
	_, err = svc.Check(ctx, "missing")
	require.Error(t, err)
}

func TestUnit_HiddenToolsPerScopeReachTheAllowlist(t *testing.T) {
	ctx, db, store := setup(t)
	ws := t.TempDir()
	require.NoError(t, SetHidden(ctx, store, "", "local_fs", []string{"write_file", "local_fs.sed"}))
	require.NoError(t, SetHidden(ctx, store, ws, "local_shell", []string{"local_shell"}))
	require.NoError(t, SetSessionToolHidden(ctx, store, "chat-1", "github", "delete_repo", true))
	require.Error(t, SetHidden(ctx, store, "relative", "x", []string{"y"}))
	require.Error(t, SetHidden(ctx, store, "", "bad.name", []string{"y"}))

	inside, err := Allowlist(ctx, store, ws, "chat-1", nil)
	require.NoError(t, err)
	require.Contains(t, inside, "!local_fs.write_file")
	require.Contains(t, inside, "!local_fs.sed")
	require.Contains(t, inside, "!local_shell.local_shell")
	require.Contains(t, inside, "!github.delete_repo")

	outside, err := Allowlist(ctx, store, t.TempDir(), "chat-2", nil)
	require.NoError(t, err)
	require.Contains(t, outside, "!local_fs.write_file")
	require.NotContains(t, outside, "!local_shell.local_shell", "a workspace hide stays in its folder")
	require.NotContains(t, outside, "!github.delete_repo", "a chat hide stays in its chat")

	tools := &fakeTools{names: []string{"local_fs", "local_shell", "github"}, tools: map[string][]taskengine.Tool{"local_fs": {tool("write_file")}, "local_shell": {tool("local_shell")}, "github": {tool("delete_repo")}}}
	svc := New(db, tools)
	all, err := svc.List(ctx, Filter{})
	require.NoError(t, err)
	byName := map[string]Connector{}
	for _, c := range all {
		byName[c.Name] = c
	}
	require.Equal(t, []string{"sed", "write_file"}, byName["local_fs"].Hidden[""])
	require.Len(t, byName["local_shell"].Hidden, 1)

	chat, err := svc.List(ctx, Filter{Session: true, Cwd: ws, SessionID: "chat-1"})
	require.NoError(t, err)
	for _, c := range chat {
		byName[c.Name] = c
	}
	require.Equal(t, []string{"local_shell"}, byName["local_shell"].HiddenBySettings)
	require.Equal(t, []string{"delete_repo"}, byName["github"].HiddenInChat)

	require.NoError(t, SetHidden(ctx, store, "", "local_fs", nil))
	require.NoError(t, SetSessionToolHidden(ctx, store, "chat-1", "github", "delete_repo", false))
	require.Empty(t, ListHidden(ctx, store).Everywhere)
	require.Empty(t, SessionHidden(ctx, store, "chat-1"))
}
