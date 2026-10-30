package contenoxcli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/contenox/runtime/runtime/agentdecl"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnit_SeedAgentDeclarations_WritesTheCatalogAndGeneratedChains(t *testing.T) {
	home := t.TempDir()

	require.NoError(t, seedAgentDeclarations(context.Background(), home))

	_, err := os.Stat(filepath.Join(home, "agents", "acp", "coding", "agent.md"))
	require.NoError(t, err, "the Markdown catalog is seeded")

	var compiled []string
	require.NoError(t, filepath.WalkDir(filepath.Join(home, ".generated"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		compiled = append(compiled, path)
		return nil
	}))
	assert.NotEmpty(t, compiled, "the declarations compile into .generated")
}

func TestUnit_SeedAgentDeclarations_IsIdempotent(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, seedAgentDeclarations(context.Background(), home))

	edited := filepath.Join(home, "agents", "acp", "general", "agent.md")
	custom := []byte("---\nname: general\ndescription: mine\nposture: read_only\n---\n\nMine.\n")
	require.NoError(t, os.WriteFile(edited, custom, 0o644))

	require.NoError(t, seedAgentDeclarations(context.Background(), home))
	got, err := os.ReadFile(edited)
	require.NoError(t, err)
	assert.Equal(t, custom, got, "a second boot must not overwrite an edited declaration")
}

func TestUnit_DefaultChain_PrefersTheCompiledTreeForAnUntouchedHome(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, seedAgentDeclarations(context.Background(), home))

	registry, err := defaultChainFor(home, acpProfileACP)
	require.NoError(t, err)
	assert.Equal(t, "chain-acp", registry.Default().ID)
	assert.Contains(t, filepath.ToSlash(registry.Source()), ".generated/chain-agent-acp.json")
}

func TestUnit_HITLPolicySource_PrefersTheRenderedEnvelope(t *testing.T) {
	home := t.TempDir()
	generatedDir := filepath.Join(home, agentdecl.GeneratedDirName)
	require.NoError(t, os.MkdirAll(generatedDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(generatedDir, "hitl-policy-default.json"),
		[]byte(`{"version":1,"default_action":"deny","rules":[]}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(home, "hitl-policy-default.json"),
		[]byte(`{"version":1,"default_action":"allow","rules":[]}`), 0o644))

	src := hitlPolicySource(home)
	data, err := src.ReadPolicy(context.Background(), "", "hitl-policy-default.json")
	require.NoError(t, err)
	assert.Contains(t, string(data), `"deny"`, "the compiled envelope outranks the raw preset beside it")
}

func TestUnit_DefaultChain_IgnoresTheOldJSONPreset(t *testing.T) {
	home := t.TempDir()
	stale := []byte(`{"id":"mine","tasks":[{"id":"only","handler":"chat_completion","execute_config":{"model":"m","provider":"p"}}]}`)
	require.NoError(t, os.WriteFile(filepath.Join(home, "default-acp-chain.json"), stale, 0o644))
	require.NoError(t, seedAgentDeclarations(context.Background(), home))

	registry, err := defaultChainFor(home, acpProfileACP)
	require.NoError(t, err)
	assert.Equal(t, "chain-acp", registry.Default().ID, "the compiled tree is the chain; the old JSON is not read")
	assert.Contains(t, filepath.ToSlash(registry.Source()), ".generated/chain-agent-acp.json")
}

func TestUnit_SeedAgentDeclarations_UsesTheUsersAgentsToml(t *testing.T) {
	home := t.TempDir()
	override := "[chain]\nmain_rounds = 3\n"
	require.NoError(t, os.WriteFile(filepath.Join(home, "agents.toml"), []byte(override), 0o644))

	require.NoError(t, seedAgentDeclarations(context.Background(), home))

	raw, err := os.ReadFile(filepath.Join(home, ".generated", "chain-agent-acp.json"))
	require.NoError(t, err)
	var chain taskengine.TaskChainDefinition
	require.NoError(t, json.Unmarshal(raw, &chain))
	for _, task := range chain.Tasks {
		if !strings.HasSuffix(task.ID, "-agent") {
			continue
		}
		for _, branch := range task.Transition.Branches {
			if branch.Operator == taskengine.OpEdgeTraversedAtLeast {
				assert.Equal(t, "3", branch.When, "%s takes the user's budget", task.ID)
			}
		}
	}
}

func TestUnit_DefaultChain_BootsWithABrokenHomeDeclaration(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, seedAgentDeclarations(context.Background(), home))
	broken := filepath.Join(home, "agents", "acp", "agent.md")
	require.NoError(t, os.WriteFile(broken, []byte("---\nname: [unclosed\n---\n"), 0o644))
	require.NoError(t, os.Remove(filepath.Join(home, ".generated", "chain-agent-acp.json")))

	registry, err := defaultChainFor(home, acpProfileACP)
	require.NoError(t, err, "a broken user declaration must not stop the runtime")
	assert.Equal(t, "chain-acp", registry.Default().ID, "the shipped tree is compiled in its place")
}
