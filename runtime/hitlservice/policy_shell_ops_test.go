package hitlservice_test

import (
	"context"
	"testing"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func shellPolicy(t *testing.T, rulesJSON string) hitlservice.PolicyEvaluator {
	t.Helper()
	dir := t.TempDir()
	src := hitlservice.NewFSPolicySource(dir)
	writePolicy(t, dir, "hitl-policy.json", []byte(`{"default_action":"allow","rules":[`+rulesJSON+`]}`))
	return hitlservice.New(src, testTenant, fixedKVReader{"hitl-policy.json"}, libtracker.NoopTracker{})
}

func TestUnit_Evaluate_CommandBlacklist_MatchesBareBasename(t *testing.T) {
	t.Parallel()
	svc := shellPolicy(t, `{"tools":"local_shell","tool":"local_shell","action":"deny","when":[{"key":"command","op":"command_blacklist","value":"mkfs,fdisk,shred"}]}`)

	denied := []map[string]any{
		{"command": "mkfs"},
		{"command": "/sbin/mkfs"},
		{"command": "fdisk"},
		{"command": "/usr/sbin/fdisk"},
		{"command": "shred"},
	}
	for _, args := range denied {
		r, err := svc.Evaluate(context.Background(), "local_shell", "local_shell", args)
		require.NoError(t, err)
		assert.Equal(t, hitlservice.ActionDeny, r.Action, "command_blacklist must deny %v", args)
	}
}

func TestUnit_Evaluate_CommandBlacklist_MultiwordEntryNeverMatches(t *testing.T) {
	t.Parallel()
	svc := shellPolicy(t, `{"tools":"local_shell","tool":"local_shell","action":"deny","when":[{"key":"command","op":"command_blacklist","value":"rm -rf"}]}`)

	r, err := svc.Evaluate(context.Background(), "local_shell", "local_shell", map[string]any{"command": "rm"})
	require.NoError(t, err)
	assert.Equal(t, hitlservice.ActionAllow, r.Action, "multi-word blacklist entry 'rm -rf' must not match basename 'rm'")
}

func TestUnit_Evaluate_CommandBlacklist_NonMatchingCommandPasses(t *testing.T) {
	t.Parallel()
	svc := shellPolicy(t, `{"tools":"local_shell","tool":"local_shell","action":"deny","when":[{"key":"command","op":"command_blacklist","value":"mkfs,fdisk"}]}`)

	allowed := []map[string]any{
		{"command": "git"},
		{"command": "cargo"},
		{"command": "make"},
		{"command": "/usr/bin/grep"},
	}
	for _, args := range allowed {
		r, err := svc.Evaluate(context.Background(), "local_shell", "local_shell", args)
		require.NoError(t, err)
		assert.Equal(t, hitlservice.ActionAllow, r.Action, "non-blacklisted command must not be denied: %v", args)
	}
}

func TestUnit_Evaluate_CommandBlacklist_EmptyListNeverMatches(t *testing.T) {
	t.Parallel()
	svc := shellPolicy(t, `{"tools":"local_shell","tool":"local_shell","action":"deny","when":[{"key":"command","op":"command_blacklist","value":""}]}`)

	r, err := svc.Evaluate(context.Background(), "local_shell", "local_shell", map[string]any{"command": "rm"})
	require.NoError(t, err)
	assert.Equal(t, hitlservice.ActionAllow, r.Action, "empty blacklist must not match anything")
}

func TestUnit_Evaluate_CommandAskAlways_MatchesListedCommand(t *testing.T) {
	t.Parallel()
	svc := shellPolicy(t, `{"tools":"local_shell","tool":"local_shell","action":"approve","when":[{"key":"command","op":"command_ask_always","value":"rm,sudo,dd"}]}`)

	flagged := []map[string]any{
		{"command": "rm"},
		{"command": "/bin/rm"},
		{"command": "sudo"},
		{"command": "dd"},
	}
	for _, args := range flagged {
		r, err := svc.Evaluate(context.Background(), "local_shell", "local_shell", args)
		require.NoError(t, err)
		assert.Equal(t, hitlservice.ActionApprove, r.Action, "command_ask_always must require approval for %v", args)
	}
}

func TestUnit_Evaluate_CommandAskAlways_UnlistedCommandPasses(t *testing.T) {
	t.Parallel()
	svc := shellPolicy(t, `{"tools":"local_shell","tool":"local_shell","action":"approve","when":[{"key":"command","op":"command_ask_always","value":"rm,sudo"}]}`)

	r, err := svc.Evaluate(context.Background(), "local_shell", "local_shell", map[string]any{"command": "git"})
	require.NoError(t, err)
	assert.Equal(t, hitlservice.ActionAllow, r.Action, "unlisted command must not be caught by ask-always rule")
}

func TestUnit_Evaluate_NoCommandSubstitution_BlocksShellMetachars(t *testing.T) {
	t.Parallel()
	svc := shellPolicy(t, `{"tools":"local_shell","tool":"local_shell","action":"approve","when":[{"key":"args","op":"no_command_substitution","value":""}]}`)

	suspicious := []map[string]any{
		{"command": "echo", "args": "$(whoami)"},
		{"command": "ls", "args": "`pwd`"},
		{"command": "cat", "args": "<(ls /)"},
		{"command": "echo", "args": ">(tee /tmp/x)"},
		{"command": "echo", "args": "$((1+1))"},
	}
	for _, args := range suspicious {
		r, err := svc.Evaluate(context.Background(), "local_shell", "local_shell", args)
		require.NoError(t, err)
		assert.Equal(t, hitlservice.ActionApprove, r.Action, "command substitution pattern must require approval: %v", args)
	}
}

func TestUnit_Evaluate_NoCommandSubstitution_AllowsPlainEnvVars(t *testing.T) {
	t.Parallel()
	svc := shellPolicy(t, `{"tools":"local_shell","tool":"local_shell","action":"approve","when":[{"key":"args","op":"no_command_substitution","value":""}]}`)

	r, err := svc.Evaluate(context.Background(), "local_shell", "local_shell", map[string]any{
		"command": "echo",
		"args":    "$HOME/projects",
	})
	require.NoError(t, err)
	assert.Equal(t, hitlservice.ActionAllow, r.Action, "plain $VAR reference must not trigger substitution block")
}

func TestUnit_Evaluate_NoCommandSubstitution_DollarBraceDoesNotBlockVarRef(t *testing.T) {
	t.Parallel()
	svc := shellPolicy(t, `{"tools":"local_shell","tool":"local_shell","action":"approve","when":[{"key":"args","op":"no_command_substitution","value":""}]}`)

	r, err := svc.Evaluate(context.Background(), "local_shell", "local_shell", map[string]any{
		"command": "echo",
		"args":    "${MY_VAR}",
	})
	require.NoError(t, err)
	assert.Equal(t, hitlservice.ActionAllow, r.Action, "${MY_VAR} must not trigger substitution block — only the literal \"${}\" pattern would match")
}

func TestUnit_Evaluate_NoCommandSubstitution_CleanCommandPasses(t *testing.T) {
	t.Parallel()
	svc := shellPolicy(t, `{"tools":"local_shell","tool":"local_shell","action":"approve","when":[{"key":"args","op":"no_command_substitution","value":""}]}`)

	clean := []map[string]any{
		{"command": "git", "args": "status --short"},
		{"command": "make", "args": "build"},
		{"command": "ls", "args": "-la /tmp"},
		{"command": "go", "args": "test ./..."},
	}
	for _, args := range clean {
		r, err := svc.Evaluate(context.Background(), "local_shell", "local_shell", args)
		require.NoError(t, err)
		assert.Equal(t, hitlservice.ActionAllow, r.Action, "clean command must not trigger substitution block: %v", args)
	}
}
