package agentdecl_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/agentdecl"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnit_Bootstrap_SeedsAndCompilesTheShippedCatalog(t *testing.T) {
	home := t.TempDir()
	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)

	preseeded, results, err := agentdecl.Bootstrap(context.Background(), home, cfg)
	require.NoError(t, err)
	require.NotEmpty(t, preseeded.Created, "a fresh home receives the shipped declarations")

	agentFile := filepath.Join(home, "agents", "acp", "coding", "agent.md")
	_, err = os.Stat(agentFile)
	require.NoError(t, err, "the shipped Markdown lands under agents/")

	var compiled []string
	require.NoError(t, filepath.WalkDir(filepath.Join(home, agentdecl.GeneratedDirName), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		compiled = append(compiled, path)
		return nil
	}))
	require.NotEmpty(t, compiled, "the declarations compile into .generated")
	require.NotEmpty(t, results, "the sync reports one result per declaration")
	for _, result := range results {
		assert.NotEqual(t, agentdecl.ActionRefused, result.Action, "%s: %s", result.Name, result.Reason)
	}

	// A second pass has nothing to write and nothing to compile.
	second, secondResults, err := agentdecl.Bootstrap(context.Background(), home, cfg)
	require.NoError(t, err)
	assert.Empty(t, second.Created)
	assert.Empty(t, second.Updated)
	for _, result := range secondResults {
		assert.NotEqual(t, agentdecl.ActionRefused, result.Action, "%s: %s", result.Name, result.Reason)
		assert.Equal(t, agentdecl.ActionUnchanged, result.Action, "%s is already compiled", result.Name)
	}
}

func TestUnit_Bootstrap_KeepsAnEditedDeclaration(t *testing.T) {
	home := t.TempDir()
	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)
	_, _, err = agentdecl.Bootstrap(context.Background(), home, cfg)
	require.NoError(t, err)

	edited := filepath.Join(home, "agents", "acp", "coding", "agent.md")
	custom := []byte("---\nname: coding\ndescription: mine\nposture: read_only\n---\n\nMy own prompt.\n")
	require.NoError(t, os.WriteFile(edited, custom, 0o644))

	preseeded, _, err := agentdecl.Bootstrap(context.Background(), home, cfg)
	require.NoError(t, err)
	var editedReported bool
	for _, path := range preseeded.Edited {
		if strings.HasSuffix(filepath.ToSlash(path), "agents/acp/coding/agent.md") {
			editedReported = true
		}
	}
	assert.True(t, editedReported, "an edited copy is reported, not overwritten: %v", preseeded.Edited)
	got, err := os.ReadFile(edited)
	require.NoError(t, err)
	assert.Equal(t, custom, got)
}

func TestUnit_Bootstrap_KeepsTheLastGoodChainWhenADeclarationBreaks(t *testing.T) {
	home := t.TempDir()
	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)
	_, _, err = agentdecl.Bootstrap(context.Background(), home, cfg)
	require.NoError(t, err)

	compiled := filepath.Join(home, agentdecl.GeneratedDirName, "chain-agent-acp.json")
	before, err := os.ReadFile(compiled)
	require.NoError(t, err)

	broken := filepath.Join(home, "agents", "acp", "agent.md")
	require.NoError(t, os.WriteFile(broken, []byte("---\nname: [unclosed\n---\n"), 0o644))
	_, results, err := agentdecl.Bootstrap(context.Background(), home, cfg)
	require.NoError(t, err)

	refused := false
	for _, result := range results {
		if result.Action == agentdecl.ActionRefused {
			refused = true
			assert.NotEmpty(t, result.Reason)
		}
	}
	assert.True(t, refused, "the broken declaration is reported")
	after, err := os.ReadFile(compiled)
	require.NoError(t, err, "its previously compiled chain is kept")
	assert.Equal(t, before, after)
}

func TestUnit_Bootstrap_RendersEveryEnvelope(t *testing.T) {
	home := t.TempDir()
	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)
	_, _, err = agentdecl.Bootstrap(context.Background(), home, cfg)
	require.NoError(t, err)

	generated := filepath.Join(home, agentdecl.GeneratedDirName)
	for _, name := range cfg.EnvelopeNames() {
		_, statErr := os.Stat(filepath.Join(generated, "hitl-policy-"+name+".json"))
		require.NoError(t, statErr, "envelope %q is rendered at boot", name)
	}

	svc := hitlservice.New(hitlservice.NewFSPolicySource(generated), envelopeTestTenant, activePolicyName{"hitl-policy-default.json"}, libtracker.NoopTracker{})
	got, err := svc.Evaluate(context.Background(), "local_fs", "read_file", map[string]any{"path": "main.go"})
	require.NoError(t, err)
	assert.Equal(t, hitlservice.ActionAllow, got.Action, "the rendered default envelope is what a session evaluates")
}

func TestUnit_Bootstrap_RecordsTheProblemOfARefusedDeclaration(t *testing.T) {
	home := t.TempDir()
	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "agents.toml"), []byte("[chain]\nmain_rounds = 3\n"), 0o644))
	_, _, err = agentdecl.Bootstrap(context.Background(), home, cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "agents", "bad.md"), []byte("---\nname: [unclosed\n---\n"), 0o644))
	_, _, err = agentdecl.Bootstrap(context.Background(), home, cfg)
	require.NoError(t, err)

	found := false
	for _, p := range agentdecl.LastProblems() {
		if p.Action == "refused" && strings.Contains(filepath.ToSlash(p.Source), "agents/bad.md") {
			found = true
			assert.NotEmpty(t, p.Reason)
		}
	}
	assert.True(t, found, "the refused declaration is reported to the app: %v", agentdecl.LastProblems())
}

func TestUnit_BootstrapFor_CompilesAProjectDeclaration(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	agentDir := filepath.Join(project, ".contenox", "agents")
	require.NoError(t, os.MkdirAll(agentDir, 0o755))
	decl := []byte("---\nname: project-agent\ndescription: from the project\nposture: read_only\n---\n\nProject prompt.\n")
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "project-agent.md"), decl, 0o644))

	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)
	_, _, err = agentdecl.BootstrapFor(context.Background(), home, project, cfg)
	require.NoError(t, err)

	_, statErr := os.Stat(filepath.Join(home, agentdecl.GeneratedDirName, "chain-agent-project-agent.json"))
	assert.NoError(t, statErr, "a project declaration compiles beside the home's")
}

func TestUnit_Bootstrap_ReseedsADeletedShippedDeclaration(t *testing.T) {
	home := t.TempDir()
	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)
	_, _, err = agentdecl.Bootstrap(context.Background(), home, cfg)
	require.NoError(t, err)

	path := filepath.Join(home, "agents", "acp", "coding", "agent.md")
	require.NoError(t, os.Remove(path))
	_, _, err = agentdecl.Bootstrap(context.Background(), home, cfg)
	require.NoError(t, err)
	_, statErr := os.Stat(path)
	assert.NoError(t, statErr, "a deleted shipped file is written again")
}

func TestUnit_TreeSignature_ChangesWhenADeclarationChanges(t *testing.T) {
	home := t.TempDir()
	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)
	_, _, err = agentdecl.Bootstrap(context.Background(), home, cfg)
	require.NoError(t, err)
	before := agentdecl.TreeSignature(home)

	path := filepath.Join(home, "agents", "acp", "coding", "agent.md")
	require.NoError(t, os.WriteFile(path, []byte("changed\n"), 0o644))
	assert.NotEqual(t, before, agentdecl.TreeSignature(home))
}

func TestUnit_CompileLock_SerializesConcurrentSeeds(t *testing.T) {
	home := t.TempDir()
	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, lockErr := agentdecl.CompileLock(home)
			if lockErr != nil {
				errs <- lockErr
				return
			}
			defer release()
			_, _, bootErr := agentdecl.Bootstrap(context.Background(), home, cfg)
			errs <- bootErr
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	_, statErr := os.Stat(filepath.Join(home, agentdecl.GeneratedDirName, "chain-agent-acp.json"))
	assert.NoError(t, statErr, "both passes leave a complete chain behind")
}
