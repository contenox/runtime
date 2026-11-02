package contenoxcli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testTenant = "00000000-0000-0000-0000-000000000001"

type nopKV struct{}

func (nopKV) GetKV(_ context.Context, _ string, _ any) error { return errors.New("not found") }

func seededPolicyService(t *testing.T, name, content string) hitlservice.Service {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	return hitlservice.NewWithDefaultPolicy(hitlservice.NewFSPolicySource(dir), testTenant, nopKV{}, libtracker.NoopTracker{}, name)
}

func assertSeededSecretInvariant(t *testing.T, name, content string) {
	t.Helper()
	svc := seededPolicyService(t, name, content)
	ctx := context.Background()

	quarantined := []string{
		"/home/u/.ssh/id_rsa",
		"/home/u/.gnupg/secring.gpg",
		"/home/u/.aws/credentials",
		"/home/u/.config/gcloud/access_tokens.db",
		"/home/u/.password-store/work.gpg",
		"/home/u/Library/Keychains/login.keychain-db",
		"/home/u/.mozilla/firefox/p/cookies.sqlite",
		"/home/u/.git-credentials",
		"/home/u/keys/id_ed25519",
		"/home/u/funds.kdbx",
	}
	for _, path := range quarantined {
		for _, tool := range []string{"read_file", "read_file_range", "grep", "stat_file", "count_stats"} {
			r, err := svc.Evaluate(ctx, "local_fs", tool, map[string]any{"path": path})
			require.NoError(t, err)
			assert.Equal(t, hitlservice.ActionDeny, r.Action, "%s: local_fs.%s(%q) must be denied", name, tool, path)
		}
	}

	persistence := []string{
		"/home/u/.ssh/authorized_keys",
		"/home/u/.config/autostart/x.desktop",
		"/home/u/Library/LaunchAgents/com.x.plist",
		"/home/u/.bashrc",
		"/etc/cron.d/x",
		"/usr/bin/x",
		"/home/u/.contenox/hitl-policy-acp.json",
		"proj/hitl-policy-strict.json",
	}
	for _, path := range persistence {
		for _, tool := range []string{"write_file", "sed"} {
			r, err := svc.Evaluate(ctx, "local_fs", tool, map[string]any{"path": path})
			require.NoError(t, err)
			assert.Equal(t, hitlservice.ActionDeny, r.Action, "%s: local_fs.%s(%q) must be denied", name, tool, path)
		}
	}

	for _, path := range []string{"src/main.go", "/home/u/proj/README.md", "internal/foo_test.go"} {
		r, err := svc.Evaluate(ctx, "local_fs", "read_file", map[string]any{"path": path})
		require.NoError(t, err)
		assert.Equal(t, hitlservice.ActionAllow, r.Action, "%s: ordinary read %q must stay allowed", name, path)
	}

	for _, path := range []string{"deploy/server.pem", "config/tls.key", "proj/.env", "proj/.env.production"} {
		r, err := svc.Evaluate(ctx, "local_fs", "read_file", map[string]any{"path": path})
		require.NoError(t, err)
		assert.NotEqual(t, hitlservice.ActionAllow, r.Action, "%s: sensitive project file %q must never be auto-allowed (approve on interactive policies, deny on untrusted-driver acpx)", name, path)
	}
}

func TestUnit_SeededACPPolicy_SecretInvariant(t *testing.T) {
	t.Parallel()
	assertSeededSecretInvariant(t, "hitl-policy-acp.json", shippedHITLPolicy(t, "acp"))
}

func TestUnit_SeededStrictPolicy_SecretInvariant(t *testing.T) {
	t.Parallel()
	assertSeededSecretInvariant(t, "hitl-policy-strict.json", shippedHITLPolicy(t, "strict"))
}

func TestUnit_SeededACPXPolicy_SecretInvariantAndHeavyDeltas(t *testing.T) {
	t.Parallel()
	assertSeededSecretInvariant(t, "hitl-policy-acpx.json", shippedHITLPolicy(t, "acpx"))

	svc := seededPolicyService(t, "hitl-policy-acpx.json", shippedHITLPolicy(t, "acpx"))
	ctx := context.Background()

	deny := map[string][2]string{
		"shell":      {"local_shell", "local_shell"},
		"web_post":   {"webtools", "web_post"},
		"web_put":    {"webtools", "web_put"},
		"web_patch":  {"webtools", "web_patch"},
		"web_delete": {"webtools", "web_delete"},
		"web_get":    {"webtools", "web_get"},
		"web_head":   {"webtools", "web_head"},
		"write_file": {"local_fs", "write_file"},
		"sed":        {"local_fs", "sed"},
	}
	for label, tt := range deny {
		r, err := svc.Evaluate(ctx, tt[0], tt[1], nil)
		require.NoError(t, err)
		assert.Equal(t, hitlservice.ActionDeny, r.Action, "acpx must deny %s (no approve tier on an untrusted driver)", label)
	}

	r, err := svc.Evaluate(ctx, "local_fs", "read_file", nil)
	require.NoError(t, err)
	assert.Equal(t, hitlservice.ActionAllow, r.Action, "acpx must allow plain reads")

	r, err = svc.Evaluate(ctx, "some_unaccounted_mcp", "arbitrary_tool", nil)
	require.NoError(t, err)
	assert.Equal(t, hitlservice.ActionDeny, r.Action, "acpx default_action must be deny (untrusted driver, least privilege)")
}

func TestUnit_InteractivePoliciesRequireApprovalForPlainShellFallback(t *testing.T) {
	t.Parallel()
	for name, content := range map[string]string{
		"hitl-policy-default.json": shippedHITLPolicy(t, "default"),
		"hitl-policy-dev.json":     shippedHITLPolicy(t, "dev"),
		"hitl-policy-acp.json":     shippedHITLPolicy(t, "acp"),
		"hitl-policy-strict.json":  shippedHITLPolicy(t, "strict"),
	} {
		name, content := name, content
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc := seededPolicyService(t, name, content)
			r, err := svc.Evaluate(context.Background(), "local_shell", "local_shell", map[string]any{"command": "python3"})
			require.NoError(t, err)
			assert.Equal(t, hitlservice.ActionApprove, r.Action, "%s must not auto-allow plain shell fallback", name)
		})
	}
}

// The git toolset is served in every session. A preset that does not name it
// leaves reads to default_action (asking for git_status) and, on a read_only
// posture, offers to approve a commit that is supposed to be impossible.
func TestUnit_SeededPolicies_GateGitTools(t *testing.T) {
	t.Parallel()
	reads := []string{"git_status", "git_diff", "git_log", "git_show", "git_branch_list", "git_blame"}
	writes := []string{"git_commit", "git_checkout_branch", "git_restore"}
	for _, tc := range []struct {
		name    string
		content string
		read    hitlservice.Action
		add     hitlservice.Action
		write   hitlservice.Action
		secret  hitlservice.Action
	}{
		{"hitl-policy-default.json", shippedHITLPolicy(t, "default"), hitlservice.ActionAllow, hitlservice.ActionApprove, hitlservice.ActionApprove, hitlservice.ActionApprove},
		{"hitl-policy-acp.json", shippedHITLPolicy(t, "acp"), hitlservice.ActionAllow, hitlservice.ActionApprove, hitlservice.ActionApprove, hitlservice.ActionApprove},
		{"hitl-policy-strict.json", shippedHITLPolicy(t, "strict"), hitlservice.ActionAllow, hitlservice.ActionApprove, hitlservice.ActionApprove, hitlservice.ActionApprove},
		{"hitl-policy-acpx.json", shippedHITLPolicy(t, "acpx"), hitlservice.ActionAllow, hitlservice.ActionDeny, hitlservice.ActionDeny, hitlservice.ActionDeny},
		{"hitl-policy-dev.json", shippedHITLPolicy(t, "dev"), hitlservice.ActionAllow, hitlservice.ActionAllow, hitlservice.ActionApprove, hitlservice.ActionAllow},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := seededPolicyService(t, tc.name, tc.content)
			ctx := context.Background()
			for _, tool := range reads {
				r, err := svc.Evaluate(ctx, "native-git", tool, nil)
				require.NoError(t, err)
				assert.Equal(t, tc.read, r.Action, "%s: native-git.%s", tc.name, tool)
			}
			r, err := svc.Evaluate(ctx, "native-git", "git_add", nil)
			require.NoError(t, err)
			assert.Equal(t, tc.add, r.Action, "%s: native-git.git_add", tc.name)
			for _, tool := range writes {
				r, err := svc.Evaluate(ctx, "native-git", tool, nil)
				require.NoError(t, err)
				assert.Equal(t, tc.write, r.Action, "%s: native-git.%s", tc.name, tool)
			}
			// The multi-file read gate evaluates a diff's files as
			// local_fs.read_file, so a repo-root .env must not be auto-allowed
			// on an interactive preset.
			r, err = svc.Evaluate(ctx, "local_fs", "read_file", map[string]any{"path": ".env"})
			require.NoError(t, err)
			assert.Equal(t, tc.secret, r.Action, "%s: reading .env", tc.name)
		})
	}
}

// Safe repository inspection through the shell is a read and runs without a
// prompt; changing the index, worktree or history still asks. A line carrying a
// metacharacter must not ride an allowlisted prefix.
func TestUnit_SeededPolicies_AllowSafeGitShellReads(t *testing.T) {
	t.Parallel()
	for name, content := range map[string]string{
		"hitl-policy-default.json": shippedHITLPolicy(t, "default"),
		"hitl-policy-acp.json":     shippedHITLPolicy(t, "acp"),
		"hitl-policy-dev.json":     shippedHITLPolicy(t, "dev"),
	} {
		name, content := name, content
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc := seededPolicyService(t, name, content)
			ctx := context.Background()
			for _, line := range []string{"status --short", "diff --stat", "log -1 --oneline", "show --stat HEAD"} {
				r, err := svc.Evaluate(ctx, "local_shell", "local_shell", map[string]any{"command": "git", "args": line})
				require.NoError(t, err)
				assert.Equal(t, hitlservice.ActionAllow, r.Action, "%s: git %s", name, line)
			}
			for _, line := range []string{"commit -m live", "checkout main", "restore a.txt", "branch -D main"} {
				r, err := svc.Evaluate(ctx, "local_shell", "local_shell", map[string]any{"command": "git", "args": line})
				require.NoError(t, err)
				assert.Equal(t, hitlservice.ActionApprove, r.Action, "%s: git %s must ask", name, line)
			}
			r, err := svc.Evaluate(ctx, "local_shell", "local_shell", map[string]any{"command": "git", "args": "status; rm -rf ."})
			require.NoError(t, err)
			assert.NotEqual(t, hitlservice.ActionAllow, r.Action, "%s: a metacharacter must not ride the git prefix", name)
		})
	}
}
