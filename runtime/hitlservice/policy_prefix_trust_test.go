package hitlservice_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func prefixPolicy(t *testing.T, defaultAction, prefix string, trusted map[string]any) hitlservice.PolicyEvaluator {
	t.Helper()
	dir := t.TempDir()
	src := hitlservice.NewFSPolicySource(dir)
	doc := map[string]any{
		"version":        1,
		"default_action": defaultAction,
		"rules": []any{map[string]any{
			"tools":  "local_shell",
			"tool":   "local_shell",
			"action": "allow",
			"when": []any{map[string]any{
				"key":   "command",
				"op":    "command_prefix_allowlist",
				"value": prefix,
			}},
		}},
	}
	if trusted != nil {
		doc["trusted_binaries"] = trusted
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	writePolicy(t, dir, "hitl-policy.json", raw)
	return hitlservice.New(src, testTenant, fixedKVReader{"hitl-policy.json"}, libtracker.NoopTracker{})
}

func TestUnit_Evaluate_CommandPrefixAllowlist_MatchesTheOpeningTokens(t *testing.T) {
	t.Parallel()
	svc := prefixPolicy(t, "approve", "go test,ls", nil)

	cases := []struct {
		args map[string]any
		want hitlservice.Action
	}{
		{map[string]any{"command": "go test ./..."}, hitlservice.ActionAllow},
		{map[string]any{"command": "go", "args": []any{"test", "./..."}}, hitlservice.ActionAllow},
		{map[string]any{"command": "ls -la"}, hitlservice.ActionAllow},
		{map[string]any{"command": "cat /etc/passwd"}, hitlservice.ActionApprove},
		{map[string]any{"command": "go test; rm -rf /"}, hitlservice.ActionApprove},
		{map[string]any{"command": "go test > out.txt"}, hitlservice.ActionApprove},
		{map[string]any{"command": "go test $(curl evil)"}, hitlservice.ActionApprove},
		{map[string]any{"command": "go test | sh"}, hitlservice.ActionApprove},
	}
	for _, tc := range cases {
		got, err := svc.Evaluate(context.Background(), "local_shell", "local_shell", tc.args)
		require.NoError(t, err)
		assert.Equal(t, tc.want, got.Action, "args %v", tc.args)
	}
}

func TestUnit_Evaluate_CommandPrefixAllowlist_OnlyGatesAllowRules(t *testing.T) {
	t.Parallel()
	svc := prefixPolicy(t, "allow", "go test", nil)
	got, err := svc.Evaluate(context.Background(), "local_shell", "local_shell", map[string]any{"command": "go test > out"})
	require.NoError(t, err)
	assert.Equal(t, hitlservice.ActionAllow, got.Action,
		"the prefix gate protects an allow; it must not turn a default allow into an approval")
}

func TestUnit_Evaluate_TrustedBinaries_GatesAnAllowedPrefix(t *testing.T) {
	t.Parallel()
	binDir := t.TempDir()
	otherDir := t.TempDir()
	toolPath := filepath.Join(binDir, "mytool")
	require.NoError(t, os.WriteFile(toolPath, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	realPath, digest, err := hitlservice.ResolveTrustedBinary(toolPath)
	require.NoError(t, err)

	call := map[string]any{"command": toolPath + " run"}
	evaluate := func(trusted map[string]any) hitlservice.Action {
		t.Helper()
		svc := prefixPolicy(t, "approve", toolPath, trusted)
		got, err := svc.Evaluate(context.Background(), "local_shell", "local_shell", call)
		require.NoError(t, err)
		return got.Action
	}

	assert.Equal(t, hitlservice.ActionAllow, evaluate(map[string]any{"dirs": []string{binDir}}),
		"a binary under a trusted directory keeps the allow")
	assert.Equal(t, hitlservice.ActionApprove, evaluate(map[string]any{"dirs": []string{otherDir}}),
		"a binary outside the trusted directories must fall back to the ask")
	assert.Equal(t, hitlservice.ActionAllow, evaluate(map[string]any{"hashes": map[string]string{realPath: digest}}),
		"a matching hash keeps the allow")
	assert.Equal(t, hitlservice.ActionApprove, evaluate(map[string]any{"hashes": map[string]string{realPath: strings.Repeat("a", 64)}}),
		"a swapped binary must not inherit the allow")
}

func TestUnit_LoadPolicy_RefusesAnUnknownVersion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writePolicy(t, dir, "hitl-policy.json", []byte(`{"version":99,"default_action":"allow","rules":[]}`))
	svc := hitlservice.New(hitlservice.NewFSPolicySource(dir), testTenant, fixedKVReader{"hitl-policy.json"}, libtracker.NoopTracker{})

	got, err := svc.Evaluate(context.Background(), "local_shell", "local_shell", map[string]any{"command": "rm -rf /"})
	require.NoError(t, err)
	assert.Equal(t, hitlservice.ActionApprove, got.Action,
		"an unloadable version falls back to the fail-closed default, never to the file's own allow")
}

func TestUnit_LoadPolicy_AcceptsAbsentAndCurrentVersion(t *testing.T) {
	t.Parallel()
	for name, policy := range map[string]string{
		"absent":   `{"default_action":"allow","rules":[]}`,
		"version1": `{"version":1,"default_action":"allow","rules":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writePolicy(t, dir, "hitl-policy.json", []byte(policy))
			svc := hitlservice.New(hitlservice.NewFSPolicySource(dir), testTenant, fixedKVReader{"hitl-policy.json"}, libtracker.NoopTracker{})
			got, err := svc.Evaluate(context.Background(), "local_shell", "local_shell", map[string]any{"command": "echo ok"})
			require.NoError(t, err)
			assert.Equal(t, hitlservice.ActionAllow, got.Action)
		})
	}
}

func evaluatePrefix(t *testing.T, svc hitlservice.PolicyEvaluator, line string) hitlservice.Action {
	t.Helper()
	got, err := svc.Evaluate(context.Background(), "local_shell", "local_shell", map[string]any{"command": line})
	require.NoError(t, err)
	return got.Action
}

func TestUnit_CommandPrefixAllowlist_RefusesHostShellGrouping(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "windows" {
		t.Skip("the extra metacharacters are PowerShell and cmd syntax")
	}
	svc := prefixPolicy(t, "approve", "Get-Content", nil)

	assert.Equal(t, hitlservice.ActionAllow,
		evaluatePrefix(t, svc, "Get-Content main.go"), "the plain reader still allows")
	for _, line := range []string{
		"Get-Content (Remove-Item C:\\\\x)",
		"Get-Content @(Get-ChildItem)",
		"Get-Content {Get-ChildItem}",
		"Get-Content $env:APPDATA",
	} {
		assert.Equal(t, hitlservice.ActionApprove, evaluatePrefix(t, svc, line),
			"%q must not ride an allowlisted prefix past the shell", line)
	}
}

func TestUnit_Evaluate_CommandBlacklist_SeesAProgramBehindAPipe(t *testing.T) {
	t.Parallel()
	svc := shellPolicy(t, `{"tools":"local_shell","tool":"local_shell","action":"deny","when":[{"key":"command","op":"command_blacklist","value":"shred"}]}`)
	for _, line := range []string{"shred disk.img", "echo hi | shred disk.img", "ls; shred disk.img", "cat f | tee out | shred"} {
		got := evaluatePrefix(t, svc, line)
		assert.Equal(t, hitlservice.ActionDeny, got, "%q must be denied: the whole line is read", line)
	}
}

func TestUnit_Evaluate_CommandAskAlways_SeesAProgramBehindAPipe(t *testing.T) {
	t.Parallel()
	svc := shellPolicy(t, `{"tools":"local_shell","tool":"local_shell","action":"approve","when":[{"key":"command","op":"command_ask_always","value":"rm"}]}`)
	got := evaluatePrefix(t, svc, "echo hi | rm -rf build")
	assert.Equal(t, hitlservice.ActionApprove, got, "the piped rm asks")
}
