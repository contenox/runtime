package agentdecl_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/contenox/runtime/runtime/agentdecl"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

func writeDecl(t *testing.T, dir, name, frontmatter, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	doc := "---\n" + frontmatter + "\n---\n\n" + body + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(doc), 0o644))
}

func acpTree(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "acp")
	writeDecl(t, root, agentdecl.AgentFilename,
		"name: acp\ndescription: Route the prompt to the right loop\ndefault: general",
		"Classify the user's latest message.")

	writeDecl(t, filepath.Join(root, "coding"), agentdecl.AgentFilename,
		"name: coding\ndescription: Change code and files\ntools: Read, Write, Edit, Bash",
		"You change code.")
	writeDecl(t, filepath.Join(root, "coding"), agentdecl.RecoveryFilename,
		"name: coding-recovery\ndescription: Second attempt",
		"The first attempt failed. Try a smaller step.")

	writeDecl(t, filepath.Join(root, "review"), agentdecl.AgentFilename,
		"name: review\ndescription: Read-only review\ntools: Read, Grep",
		"You review code without changing it.")

	writeDecl(t, filepath.Join(root, "general"), agentdecl.AgentFilename,
		"name: general\ndescription: Questions and explanations\ntools: Read",
		"You answer questions.")
	writeDecl(t, filepath.Join(root, "general"), agentdecl.RecoveryFilename,
		"name: general-recovery\ndescription: Second attempt",
		"The first attempt failed. Answer directly.")
	return root
}

func taskByID(chain *taskengine.TaskChainDefinition, id string) *taskengine.TaskDefinition {
	for i := range chain.Tasks {
		if chain.Tasks[i].ID == id {
			return &chain.Tasks[i]
		}
	}
	return nil
}

func gotoFor(task *taskengine.TaskDefinition, op taskengine.OperatorTerm, when string) string {
	for _, b := range task.Transition.Branches {
		if b.Operator == op && b.When == when {
			return b.Goto
		}
	}
	return ""
}

func TestUnit_Tree_ReproducesTheShippedRoutingShape(t *testing.T) {
	cfg := mustShipped(t)
	tree, err := agentdecl.LoadTree(context.Background(), rootOf(t, acpTree(t)), cfg)
	require.NoError(t, err)
	require.True(t, tree.IsRouter())
	require.Equal(t, []string{"coding", "general", "review"}, tree.Labels(),
		"children are sorted, so emission is deterministic")

	chain, err := agentdecl.EmitTree(tree, cfg)
	require.NoError(t, err)

	require.Equal(t, "acp-route", chain.Tasks[0].ID)
	require.Equal(t, taskengine.HandleRoute, chain.Tasks[0].Handler)

	route := taskByID(chain, "acp-route")
	require.Equal(t, "acp-coding-agent", gotoFor(route, taskengine.OpEquals, "coding"))
	require.Equal(t, "acp-review-agent", gotoFor(route, taskengine.OpEquals, "review"))
	require.Equal(t, "acp-general-agent", gotoFor(route, taskengine.OpEquals, "general"))
	require.Equal(t, "acp-general-agent", gotoFor(route, taskengine.OpDefault, ""),
		"the declared default is where an unmapped answer lands")

	coding := taskByID(chain, "acp-coding-agent")
	require.NotNil(t, coding)
	require.Equal(t, "acp-coding-tools", gotoFor(coding, taskengine.OpEquals, "tool_call"))
	require.NotNil(t, taskByID(chain, "acp-coding-recovery"))
	require.NotNil(t, taskByID(chain, "acp-coding-recovery-tools"))

	require.NotNil(t, taskByID(chain, "acp-summarise"))
	terminals := 0
	for _, task := range chain.Tasks {
		if task.ID == "acp-summarise" {
			terminals++
		}
	}
	require.Equal(t, 1, terminals)
}

func TestUnit_Tree_ALeafWithoutRecoveryFallsStraightToTheTerminal(t *testing.T) {
	cfg := mustShipped(t)
	tree, err := agentdecl.LoadTree(context.Background(), rootOf(t, acpTree(t)), cfg)
	require.NoError(t, err)
	chain, err := agentdecl.EmitTree(tree, cfg)
	require.NoError(t, err)

	require.Nil(t, taskByID(chain, "acp-review-recovery"),
		"no recovery.md means no recovery stage is invented")
	review := taskByID(chain, "acp-review-agent")
	require.NotNil(t, review)
	require.Equal(t, "acp-summarise", review.Transition.OnFailure)
	require.Equal(t, "acp-summarise",
		gotoFor(review, taskengine.OpEdgeTraversedAtLeast, strconv.Itoa(cfg.Chain.MainRounds)),
		"an exhausted review loop lands on the terminal, not on a stage that does not exist")

	coding := taskByID(chain, "acp-coding-agent")
	require.Equal(t, "acp-coding-recovery", coding.Transition.OnFailure)
}

func TestUnit_Tree_LabelsAreInjectedIntoTheClassifierPrompt(t *testing.T) {
	cfg := mustShipped(t)
	tree, err := agentdecl.LoadTree(context.Background(), rootOf(t, acpTree(t)), cfg)
	require.NoError(t, err)
	chain, err := agentdecl.EmitTree(tree, cfg)
	require.NoError(t, err)

	prompt := taskByID(chain, "acp-route").SystemInstruction
	require.Contains(t, prompt, "Classify the user's latest message.", "the author's own prompt survives")
	for _, label := range tree.Labels() {
		require.Contains(t, prompt, "- "+label, "every branch label reaches the prompt")
	}
	require.Contains(t, prompt, "Read-only review", "a branch's description explains when to pick it")
	require.Contains(t, prompt, "answer general", "the fallback is stated to the classifier too")

	require.Empty(t, taskByID(chain, "acp-route").ExecuteConfig.Tools)
}

func TestUnit_Tree_RefusesARouterWithNoUsableDefault(t *testing.T) {
	cfg := mustShipped(t)
	root := filepath.Join(t.TempDir(), "acp")
	writeDecl(t, root, agentdecl.AgentFilename, "name: acp\ndescription: Router", "Classify.")
	writeDecl(t, filepath.Join(root, "a"), agentdecl.AgentFilename, "name: a\ndescription: A", "A.")
	writeDecl(t, filepath.Join(root, "b"), agentdecl.AgentFilename, "name: b\ndescription: B", "B.")

	_, err := agentdecl.LoadTree(context.Background(), rootOf(t, root), cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "names no default")

	writeDecl(t, root, agentdecl.AgentFilename, "name: acp\ndescription: Router\ndefault: nope", "Classify.")
	_, err = agentdecl.LoadTree(context.Background(), rootOf(t, root), cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not one of")
}

func TestUnit_Tree_ASingleChildNeedsNoDeclaredDefault(t *testing.T) {
	cfg := mustShipped(t)
	root := filepath.Join(t.TempDir(), "solo")
	writeDecl(t, root, agentdecl.AgentFilename, "name: solo\ndescription: Router", "Classify.")
	writeDecl(t, filepath.Join(root, "only"), agentdecl.AgentFilename, "name: only\ndescription: Only", "Do it.")

	tree, err := agentdecl.LoadTree(context.Background(), rootOf(t, root), cfg)
	require.NoError(t, err)
	require.Equal(t, "only", tree.Default)
}

func TestUnit_Tree_NestsWithoutAFurtherConcept(t *testing.T) {
	cfg := mustShipped(t)
	root := filepath.Join(t.TempDir(), "top")
	writeDecl(t, root, agentdecl.AgentFilename, "name: top\ndescription: Top\ndefault: work", "Classify.")
	writeDecl(t, filepath.Join(root, "work"), agentdecl.AgentFilename,
		"name: work\ndescription: Work\ndefault: fast", "Classify again.")
	writeDecl(t, filepath.Join(root, "work", "fast"), agentdecl.AgentFilename, "name: fast\ndescription: Fast", "Go.")
	writeDecl(t, filepath.Join(root, "work", "slow"), agentdecl.AgentFilename, "name: slow\ndescription: Slow", "Go.")

	tree, err := agentdecl.LoadTree(context.Background(), rootOf(t, root), cfg)
	require.NoError(t, err)
	chain, err := agentdecl.EmitTree(tree, cfg)
	require.NoError(t, err)

	require.NotNil(t, taskByID(chain, "top-route"))
	inner := taskByID(chain, "top-work-route")
	require.NotNil(t, inner, "a child that branches is a router too")
	require.Equal(t, "top-work-route", gotoFor(taskByID(chain, "top-route"), taskengine.OpEquals, "work"))
	require.Equal(t, "top-work-fast-agent", gotoFor(inner, taskengine.OpEquals, "fast"))
}

func TestUnit_Tree_RefusesRecoveryOnARouter(t *testing.T) {
	cfg := mustShipped(t)
	root := filepath.Join(t.TempDir(), "acp")
	writeDecl(t, root, agentdecl.AgentFilename, "name: acp\ndescription: Router\ndefault: a", "Classify.")
	writeDecl(t, root, agentdecl.RecoveryFilename, "name: oops\ndescription: Nope", "No.")
	writeDecl(t, filepath.Join(root, "a"), agentdecl.AgentFilename, "name: a\ndescription: A", "A.")

	_, err := agentdecl.LoadTree(context.Background(), rootOf(t, root), cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "router has no recovery")
}

func mustShipped(t *testing.T) agentdecl.Config {
	t.Helper()
	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)
	return cfg
}

func TestUnit_Tree_SyncEmitsOneChainForTheWholeTree(t *testing.T) {
	cfg := mustShipped(t)
	root := t.TempDir()
	agentsDir := filepath.Join(root, "agents")

	writeDecl(t, filepath.Join(agentsDir, "acp"), agentdecl.AgentFilename,
		"name: acp\ndescription: Router\ndefault: general", "Classify.")
	writeDecl(t, filepath.Join(agentsDir, "acp", "coding"), agentdecl.AgentFilename,
		"name: coding\ndescription: Coding", "Code.")
	writeDecl(t, filepath.Join(agentsDir, "acp", "coding"), agentdecl.RecoveryFilename,
		"name: coding-recovery\ndescription: Retry", "Retry.")
	writeDecl(t, filepath.Join(agentsDir, "acp", "general"), agentdecl.AgentFilename,
		"name: general\ndescription: General", "Answer.")
	writeDecl(t, agentsDir, "solo.md", "name: solo\ndescription: Solo", "Solo.")

	generated := filepath.Join(root, ".generated")
	results, err := syncAt(t, context.Background(),
		[]agentdecl.SourceDir{{Root: rootOf(t, agentsDir), Native: true}}, generated, cfg)
	require.NoError(t, err)

	names := map[string]bool{}
	for _, r := range results {
		if r.Action == agentdecl.ActionRefused {
			t.Fatalf("refused %s: %s", r.Source, r.Reason)
		}
		names[r.Name] = true
	}
	require.True(t, names["acp"], "the tree emits one agent named for its directory")
	require.True(t, names["solo"], "a flat declaration beside a tree still works")
	require.False(t, names["coding"], "a branch is not an agent of its own")
	require.False(t, names["coding-recovery"], "a recovery prompt is not an agent of its own")
	require.Len(t, results, 2, "one tree + one flat file = two agents")

	chains, err := filepath.Glob(filepath.Join(generated, "chain-agent-acp*.json"))
	require.NoError(t, err)
	require.Len(t, chains, 1)
	raw, err := os.ReadFile(chains[0])
	require.NoError(t, err)
	require.Contains(t, string(raw), "acp-route")
	require.Contains(t, string(raw), "acp-coding-agent")
}

func TestUnit_Tree_PreseedShipsAWorkingTreeExample(t *testing.T) {
	dir := t.TempDir()
	created, err := agentdecl.Preseed(context.Background(), rootOf(t, dir))
	require.NoError(t, err)

	root := filepath.Join(dir, agentdecl.NativeSourceDir, "triage")
	require.FileExists(t, filepath.Join(root, agentdecl.AgentFilename))
	require.FileExists(t, filepath.Join(root, "code", agentdecl.AgentFilename))
	require.FileExists(t, filepath.Join(root, "code", agentdecl.RecoveryFilename))
	require.FileExists(t, filepath.Join(root, "docs", agentdecl.AgentFilename))
	require.Contains(t, created.Created, filepath.Join(root, "code", agentdecl.RecoveryFilename))

	cfg := mustShipped(t)
	tree, err := agentdecl.LoadTree(context.Background(), rootOf(t, root), cfg)
	require.NoError(t, err)
	chain, err := agentdecl.EmitTree(tree, cfg)
	require.NoError(t, err)
	require.Equal(t, "triage-route", chain.Tasks[0].ID)
	require.NotNil(t, taskByID(chain, "triage-code-recovery"))
	require.Nil(t, taskByID(chain, "triage-docs-recovery"), "docs declares no recovery")

	require.NoError(t, os.WriteFile(filepath.Join(root, "docs", agentdecl.AgentFilename),
		[]byte("---\nname: docs\ndescription: Mine now\n---\n\nMine.\n"), 0o644))
	_, err = agentdecl.Preseed(context.Background(), rootOf(t, dir))
	require.NoError(t, err)
	again, err := os.ReadFile(filepath.Join(root, "docs", agentdecl.AgentFilename))
	require.NoError(t, err)
	require.Contains(t, string(again), "Mine now")
}

func TestUnit_Tree_EmittedChainsLint(t *testing.T) {
	cfg := mustShipped(t)
	for _, name := range []string{"acp", "triage"} {
		t.Run(name, func(t *testing.T) {
			tree, err := agentdecl.LoadTree(context.Background(), rootOf(t, filepath.Join("preseed", "agents", name)), cfg)
			require.NoError(t, err)
			chain, err := agentdecl.EmitTree(tree, cfg)
			require.NoError(t, err)
			require.NoError(t, taskengine.LintChain(chain), "emitted chain must lint")
		})
	}
}

func TestUnit_Tree_LoopMacrosResolveToThisLeafAndTheRealBudget(t *testing.T) {
	cfg := mustShipped(t)
	root := filepath.Join(t.TempDir(), "svc")
	writeDecl(t, root, agentdecl.AgentFilename, "name: svc\ndescription: Router\ndefault: work", "Classify.")
	writeDecl(t, filepath.Join(root, "work"), agentdecl.AgentFilename,
		"name: work\ndescription: Work", "Do it.")
	writeDecl(t, filepath.Join(root, "work"), agentdecl.RecoveryFilename,
		"name: work-recovery\ndescription: Retry",
		"Used {{rounds_used}} of {{main_rounds}} main and {{recovery_rounds_used}} of {{recovery_rounds}} recovery.")

	tree, err := agentdecl.LoadTree(context.Background(), rootOf(t, root), cfg)
	require.NoError(t, err)
	chain, err := agentdecl.EmitTree(tree, cfg)
	require.NoError(t, err)

	got := taskByID(chain, "svc-work-recovery").SystemInstruction
	require.Contains(t, got, "{{edge_count:svc-work-agent->svc-work-tools}}")
	require.Contains(t, got, "{{edge_count:svc-work-recovery->svc-work-recovery-tools}}")
	require.Contains(t, got, fmt.Sprintf("of %d main", cfg.Chain.MainRounds))
	require.Contains(t, got, fmt.Sprintf("of %d recovery", cfg.Chain.RecoveryRounds))
	require.NotContains(t, got, "{{main_rounds}}")
	require.NoError(t, taskengine.LintChain(chain))
}

func TestUnit_Tree_ShippedDeclarationsNameNoEmittedTaskIDs(t *testing.T) {
	err := filepath.WalkDir("preseed/agents", func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return err
		}
		body, rErr := os.ReadFile(p)
		if rErr != nil {
			return rErr
		}
		require.NotContainsf(t, string(body), "{{edge_count:", "%s names emitted task ids; use %s", p, agentdecl.MacroRoundsUsed)
		return nil
	})
	require.NoError(t, err)
}

func TestUnit_ShippedTree_MaxTokensMacroCarriesAFallback(t *testing.T) {
	cfg, err := agentdecl.Shipped()
	require.NoError(t, err)
	chain, err := agentdecl.CompileShippedTree(cfg)
	require.NoError(t, err)
	for _, task := range chain.Tasks {
		if task.ExecuteConfig == nil || task.ExecuteConfig.MaxTokensTemplate == "" {
			continue
		}
		require.Containsf(t, task.ExecuteConfig.MaxTokensTemplate, "|",
			"task %s: a shipped max_tokens macro must expand when no default-max-tokens is configured", task.ID)
	}
}
