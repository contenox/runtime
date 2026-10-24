package contenoxcli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/accessview"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/internal/accessapi"
	"github.com/contenox/runtime/runtime/internal/localfileapi"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/vfs"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

const accessTestPolicyName = "hitl-policy-access-test.json"

const accessTestPolicy = `{
  "default_action": "approve",
  "rules": [
    {"tools": "local_fs", "tool": "*", "action": "deny", "when": [{"key": "path", "op": "glob", "value": "**/.ssh/**"}]},
    {"tools": "local_fs", "tool": "read_file", "action": "allow"},
    {"tools": "local_fs", "tool": "list_dir", "action": "allow"}
  ]
}`

type emptyKVReaderStub struct{}

func (emptyKVReaderStub) GetKV(context.Context, string, interface{}) error {
	return os.ErrNotExist
}

func setupAccessTestServer(t *testing.T, root, policyDir string) (*vfs.Factory, *httptest.Server) {
	t.Helper()
	factory, err := vfs.NewFactory(root)
	require.NoError(t, err)

	src := hitlservice.NewFSPolicySource(policyDir)
	hitlFor := func(policyName string) hitlservice.Service {
		if policyName == "" {
			policyName = accessTestPolicyName
		}
		return hitlservice.NewWithDefaultPolicy(src, runtimetypes.LocalTenantID, emptyKVReaderStub{}, libtracker.NoopTracker{}, policyName)
	}

	apiMux := http.NewServeMux()
	accessapi.AddRoutes(apiMux, factory, hitlFor)
	rootMux := http.NewServeMux()
	rootMux.Handle("/api/", http.StripPrefix("/api", apiMux))

	srv := httptest.NewServer(rootMux)
	t.Cleanup(srv.Close)
	return factory, srv
}

func newWorkspaceAccessTestCmd() *cobra.Command {
	c := &cobra.Command{Use: "access", Args: cobra.MinimumNArgs(1), RunE: runWorkspaceAccess}
	addServeClientFlags(c)
	c.Flags().String("root", "", "")
	c.Flags().String("policy", "", "")
	return c
}

func accessTestWorkspace(t *testing.T) (root, outsideSibling string) {
	t.Helper()
	parent := t.TempDir()
	root = filepath.Join(parent, "workspace")
	outsideSibling = filepath.Join(parent, "escape")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".ssh"), 0o755))
	require.NoError(t, os.MkdirAll(outsideSibling, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".ssh", "id_rsa"), []byte("fake-key\n"), 0o600))
	return root, outsideSibling
}

func runWorkspaceAccessCmd(t *testing.T, srv *httptest.Server, args ...string) (string, error) {
	t.Helper()
	cmd := newWorkspaceAccessTestCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(append([]string{"--server", srv.URL}, args...))
	err := cmd.Execute()
	return buf.String(), err
}

func TestUnit_WorkspaceAccess_NormalFileAllowsReadApprovesWrite(t *testing.T) {
	root, _ := accessTestWorkspace(t)
	policyDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(policyDir, accessTestPolicyName), []byte(accessTestPolicy), 0o644))
	_, srv := setupAccessTestServer(t, root, policyDir)

	out, err := runWorkspaceAccessCmd(t, srv, "src/main.go")
	require.NoError(t, err)
	require.Contains(t, out, "Policy: "+accessTestPolicyName)
	require.Contains(t, out, "src/main.go")
	require.Contains(t, out, "true")
	require.Contains(t, out, "allow")
	require.Contains(t, out, "approve")
	require.Contains(t, out, "write:default_action")
	require.NotContains(t, out, "read:", "an allow dimension must not appear in the REASON column")
}

func TestUnit_WorkspaceAccess_SecretPathDeniesBothDimensionsWithMatchedRule(t *testing.T) {
	root, _ := accessTestWorkspace(t)
	policyDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(policyDir, accessTestPolicyName), []byte(accessTestPolicy), 0o644))
	_, srv := setupAccessTestServer(t, root, policyDir)

	out, err := runWorkspaceAccessCmd(t, srv, ".ssh/id_rsa")
	require.NoError(t, err)
	require.Contains(t, out, ".ssh/id_rsa")
	require.Contains(t, out, "read:matched_rule#0")
	require.Contains(t, out, "write:matched_rule#0")

	lines := bytes.Split([]byte(out), []byte("\n"))
	var row string
	for _, l := range lines {
		if bytes.Contains(l, []byte(".ssh/id_rsa")) {
			row = string(l)
		}
	}
	require.NotEmpty(t, row, "the .ssh/id_rsa row must be present")
	require.Contains(t, row, "deny")
	require.NotContains(t, row, "allow", "a denied path's row must not also show allow")
}

func TestUnit_WorkspaceAccess_UnreachablePathHasNoVerdict(t *testing.T) {
	root, _ := accessTestWorkspace(t)
	policyDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(policyDir, accessTestPolicyName), []byte(accessTestPolicy), 0o644))
	_, srv := setupAccessTestServer(t, root, policyDir)

	out, err := runWorkspaceAccessCmd(t, srv, "../escape")
	require.NoError(t, err, "an unreachable path is a 200 with reachable:false, not a command error")
	require.Contains(t, out, "../escape")
	require.Contains(t, out, "false")

	lines := bytes.Split([]byte(out), []byte("\n"))
	var row string
	for _, l := range lines {
		if bytes.Contains(l, []byte("../escape")) {
			row = string(l)
		}
	}
	require.NotEmpty(t, row)
	require.Contains(t, row, "false")
}

func TestUnit_WorkspaceAccess_AllThreeCasesInOneBatch(t *testing.T) {
	root, _ := accessTestWorkspace(t)
	policyDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(policyDir, accessTestPolicyName), []byte(accessTestPolicy), 0o644))
	_, srv := setupAccessTestServer(t, root, policyDir)

	out, err := runWorkspaceAccessCmd(t, srv, "src/main.go", ".ssh/id_rsa", "../escape")
	require.NoError(t, err)

	rowFor := func(path string) string {
		for _, l := range bytes.Split([]byte(out), []byte("\n")) {
			if bytes.HasPrefix(l, []byte(path+"\t")) || bytes.Contains(l, []byte(path)) {
				return string(l)
			}
		}
		return ""
	}

	main := rowFor("src/main.go")
	require.Contains(t, main, "true")
	require.Contains(t, main, "allow")
	require.Contains(t, main, "approve")

	secret := rowFor(".ssh/id_rsa")
	require.Contains(t, secret, "true")
	require.Contains(t, secret, "deny")

	escape := rowFor("../escape")
	require.Contains(t, escape, "false")
}

func TestUnit_WorkspaceAccess_RootFlagSelectsAllowlistedRoot(t *testing.T) {
	root, _ := accessTestWorkspace(t)
	secondRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(secondRoot, "note.txt"), []byte("hi\n"), 0o644))

	policyDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(policyDir, accessTestPolicyName), []byte(accessTestPolicy), 0o644))

	factory, err := vfs.NewFactory(root)
	require.NoError(t, err)
	require.NoError(t, factory.SetRoots([]string{root, secondRoot}))

	src := hitlservice.NewFSPolicySource(policyDir)
	hitlFor := localfileapi.PolicyEvaluatorFactory(func(policyName string) hitlservice.Service {
		if policyName == "" {
			policyName = accessTestPolicyName
		}
		return hitlservice.NewWithDefaultPolicy(src, runtimetypes.LocalTenantID, emptyKVReaderStub{}, libtracker.NoopTracker{}, policyName)
	})
	apiMux := http.NewServeMux()
	accessapi.AddRoutes(apiMux, factory, hitlFor)
	rootMux := http.NewServeMux()
	rootMux.Handle("/api/", http.StripPrefix("/api", apiMux))
	srv := httptest.NewServer(rootMux)
	t.Cleanup(srv.Close)

	out, err := runWorkspaceAccessCmd(t, srv, "--root", secondRoot, "note.txt")
	require.NoError(t, err)
	require.Contains(t, out, "note.txt")
	require.Contains(t, out, "true")
}

func TestUnit_WorkspaceAccess_UnreachableServerFailsWithNonZeroExit(t *testing.T) {
	cmd := newWorkspaceAccessTestCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--server", "http://127.0.0.1:1", "src/main.go"})
	err := cmd.Execute()
	require.Error(t, err)
}

func TestUnit_WorkspaceAccess_BadRootSurfacesServeErrorMessage(t *testing.T) {
	root, _ := accessTestWorkspace(t)
	policyDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(policyDir, accessTestPolicyName), []byte(accessTestPolicy), 0o644))
	_, srv := setupAccessTestServer(t, root, policyDir)

	out, err := runWorkspaceAccessCmd(t, srv, "--root", "/etc", "passwd")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not under any configured workspace root")
	_ = out
}

func TestUnit_AccessTableHelpers(t *testing.T) {
	allow := &accessview.DimensionVerdict{Action: "allow", Reason: "matched_rule", Rule: intPtr(4)}
	approveDefault := &accessview.DimensionVerdict{Action: "approve", Reason: "default_action"}
	denyRule := &accessview.DimensionVerdict{Action: "deny", Reason: "matched_rule", Rule: intPtr(0)}

	require.Equal(t, "-", dimensionAction(nil))
	require.Equal(t, "allow", dimensionAction(allow))
	require.Equal(t, "deny", dimensionAction(denyRule))

	require.Equal(t, "", dimensionReasonFragment("read", nil))
	require.Equal(t, "", dimensionReasonFragment("read", allow), "an allow dimension contributes no reason fragment")
	require.Equal(t, "write:default_action", dimensionReasonFragment("write", approveDefault))
	require.Equal(t, "read:matched_rule#0", dimensionReasonFragment("read", denyRule))

	require.Equal(t, "-", accessReasonColumn(nil, nil), "unreachable rows render a dash")
	require.Equal(t, "-", accessReasonColumn(allow, allow), "an all-allow verdict needs no explaining")
	require.Equal(t, "write:default_action", accessReasonColumn(allow, approveDefault))
	require.Equal(t, "read:matched_rule#0 write:matched_rule#0", accessReasonColumn(denyRule, denyRule))
}

func intPtr(i int) *int { return &i }
