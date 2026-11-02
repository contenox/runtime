package localtools_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/contenox/runtime/runtime/localtools"
	"github.com/contenox/runtime/runtime/taskengine"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gitSignature() *object.Signature {
	return &object.Signature{
		Name:  "Test Author",
		Email: "test@example.com",
		When:  time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
	}
}

func gitInitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitInitRepoAt(t, dir)
	return dir
}

func gitInitRepoAt(t *testing.T, dir string) *git.Repository {
	t.Helper()
	repo, err := git.PlainInit(dir, false)
	require.NoError(t, err)
	cfg, err := repo.Config()
	require.NoError(t, err)
	cfg.User.Name = "Test Author"
	cfg.User.Email = "test@example.com"
	require.NoError(t, repo.SetConfig(cfg))

	gitWriteFile(t, dir, "a.txt", "one\ntwo\n")
	wt, err := repo.Worktree()
	require.NoError(t, err)
	_, err = wt.Add("a.txt")
	require.NoError(t, err)
	_, err = wt.Commit("initial commit", &git.CommitOptions{Author: gitSignature()})
	require.NoError(t, err)
	return repo
}

func gitWriteFile(t *testing.T, dir, name, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
}

func execGitTool(t *testing.T, tools taskengine.ToolsRepo, tool string, args map[string]any) (any, taskengine.DataType, error) {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	return tools.Exec(context.Background(), time.Now(), args, false,
		&taskengine.ToolsCall{Name: localtools.GitToolsName, ToolName: tool})
}

func gitResultText(t *testing.T, res any) string {
	t.Helper()
	switch v := res.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		t.Fatalf("git result %T is neither a string nor a fmt.Stringer", res)
		return ""
	}
}

func mustExecGitTool(t *testing.T, tools taskengine.ToolsRepo, tool string, args map[string]any) (any, string) {
	t.Helper()
	res, dt, err := execGitTool(t, tools, tool, args)
	require.NoError(t, err, "%s must succeed", tool)
	require.Equal(t, taskengine.DataTypeString, dt, "%s must render as text", tool)
	return res, gitResultText(t, res)
}

func TestUnit_GitTools_StatusCleanAndDirty(t *testing.T) {
	t.Parallel()
	dir := gitInitRepo(t)
	tools := localtools.NewGitTools(dir)

	res, text := mustExecGitTool(t, tools, "git_status", nil)
	clean, ok := res.(localtools.GitStatusResult)
	require.True(t, ok, "git_status returned %T", res)
	assert.True(t, clean.Clean)
	assert.Contains(t, text, "working tree clean")
	assert.NotNil(t, clean.Head)

	gitWriteFile(t, dir, "a.txt", "one\ntwo\nthree\n")
	gitWriteFile(t, dir, "b.txt", "new\n")
	res, text = mustExecGitTool(t, tools, "git_status", nil)
	dirty, ok := res.(localtools.GitStatusResult)
	require.True(t, ok, "git_status returned %T", res)
	assert.False(t, dirty.Clean)
	assert.Contains(t, text, "changed but not staged")
	assert.Contains(t, text, "untracked")
	assert.Empty(t, dirty.Staged)
	require.Len(t, dirty.Unstaged, 1)
	assert.Equal(t, "a.txt", dirty.Unstaged[0].Path)
	assert.Equal(t, "M", dirty.Unstaged[0].Code)
	assert.Equal(t, []string{"b.txt"}, dirty.Untracked)
}

func TestUnit_GitTools_Diff(t *testing.T) {
	t.Parallel()
	dir := gitInitRepo(t)
	gitWriteFile(t, dir, "a.txt", "one\ntwo\nthree\n")
	tools := localtools.NewGitTools(dir)

	_, diff := mustExecGitTool(t, tools, "git_diff", nil)
	assert.Contains(t, diff, "diff --git a/a.txt b/a.txt")
	assert.Contains(t, diff, "--- a/a.txt")
	assert.Contains(t, diff, "+++ b/a.txt")
	assert.Contains(t, diff, "+three")

	_, scoped := mustExecGitTool(t, tools, "git_diff", map[string]any{"path": "a.txt"})
	assert.Contains(t, scoped, "+three")

	_, empty := mustExecGitTool(t, tools, "git_diff", map[string]any{"path": "b.txt"})
	assert.Contains(t, empty, "no changes against HEAD under b.txt")
	assert.NotContains(t, empty, "diff --git a/a.txt")
}

func TestUnit_GitTools_Log(t *testing.T) {
	t.Parallel()
	dir := gitInitRepo(t)
	tools := localtools.NewGitTools(dir)

	res, text := mustExecGitTool(t, tools, "git_log", nil)
	logResult, ok := res.(localtools.GitLogResult)
	require.True(t, ok, "git_log returned %T", res)
	require.Len(t, logResult.Commits, 1)
	assert.Equal(t, "initial commit", logResult.Commits[0].Subject)
	assert.Equal(t, "Test Author", logResult.Commits[0].Author)
	assert.Contains(t, text, "initial commit")
	assert.Contains(t, text, "test@example.com")

	_, scoped := mustExecGitTool(t, tools, "git_log", map[string]any{"path": "a.txt", "n": 5})
	assert.Contains(t, scoped, "initial commit")
}

func TestUnit_GitTools_Show(t *testing.T) {
	t.Parallel()
	dir := gitInitRepo(t)
	tools := localtools.NewGitTools(dir)

	_, root := mustExecGitTool(t, tools, "git_show", map[string]any{"ref": "HEAD"})
	assert.Contains(t, root, "commit ")
	assert.Contains(t, root, "initial commit")
	assert.Contains(t, root, "(root commit) files:")
	assert.Contains(t, root, "a.txt")

	repo, err := git.PlainOpen(dir)
	require.NoError(t, err)
	wt, err := repo.Worktree()
	require.NoError(t, err)
	gitWriteFile(t, dir, "a.txt", "one\ntwo\nthree\n")
	_, err = wt.Add("a.txt")
	require.NoError(t, err)
	_, err = wt.Commit("second commit", &git.CommitOptions{Author: gitSignature()})
	require.NoError(t, err)

	_, patch := mustExecGitTool(t, tools, "git_show", map[string]any{"ref": "HEAD"})
	assert.Contains(t, patch, "second commit")
	assert.Contains(t, patch, "+++ b/a.txt")
	assert.Contains(t, patch, "+three")
}

func TestUnit_GitTools_BranchList(t *testing.T) {
	t.Parallel()
	dir := gitInitRepo(t)
	repo, err := git.PlainOpen(dir)
	require.NoError(t, err)
	ref, err := repo.Head()
	require.NoError(t, err)
	current := ref.Name().Short()

	res, text := mustExecGitTool(t, localtools.NewGitTools(dir), "git_branch_list", nil)
	branches, ok := res.(localtools.GitBranchListResult)
	require.True(t, ok, "git_branch_list returned %T", res)
	assert.Equal(t, current, branches.Current)
	require.Len(t, branches.Branches, 1)
	assert.Equal(t, current, branches.Branches[0].Name)
	assert.Contains(t, text, "* "+current)
}

func TestUnit_GitTools_Blame(t *testing.T) {
	t.Parallel()
	dir := gitInitRepo(t)
	_, text := mustExecGitTool(t, localtools.NewGitTools(dir), "git_blame", map[string]any{"path": "a.txt"})
	assert.Contains(t, text, "Test Author")
	assert.Contains(t, text, "one")
	assert.Contains(t, text, "two")
}

func TestUnit_GitTools_AddAndCommit(t *testing.T) {
	t.Parallel()
	dir := gitInitRepo(t)
	tools := localtools.NewGitTools(dir)
	gitWriteFile(t, dir, "a.txt", "one\ntwo\nthree\n")

	res, _, err := execGitTool(t, tools, "git_commit", map[string]any{"message": "nothing staged"})
	require.Error(t, err)
	assert.Nil(t, res)
	assert.Contains(t, err.Error(), "nothing is staged")

	_, added := mustExecGitTool(t, tools, "git_add", map[string]any{"paths": "a.txt"})
	assert.Contains(t, added, "staged a.txt")

	res, text := mustExecGitTool(t, tools, "git_status", nil)
	staged, ok := res.(localtools.GitStatusResult)
	require.True(t, ok, "git_status returned %T", res)
	assert.Contains(t, text, "staged for commit")
	require.Len(t, staged.Staged, 1)
	assert.Equal(t, "a.txt", staged.Staged[0].Path)

	_, committed := mustExecGitTool(t, tools, "git_commit", map[string]any{"message": "second commit"})
	assert.Contains(t, committed, "committed ")
	assert.Contains(t, committed, "second commit")

	_, clean := mustExecGitTool(t, tools, "git_status", nil)
	assert.Contains(t, clean, "working tree clean")

	_, logOut := mustExecGitTool(t, tools, "git_log", nil)
	assert.Contains(t, logOut, "second commit")
}

func TestUnit_GitTools_CheckoutNewBranch(t *testing.T) {
	t.Parallel()
	dir := gitInitRepo(t)
	tools := localtools.NewGitTools(dir)
	repo, err := git.PlainOpen(dir)
	require.NoError(t, err)
	ref, err := repo.Head()
	require.NoError(t, err)
	original := ref.Name().Short()

	_, created := mustExecGitTool(t, tools, "git_checkout_branch", map[string]any{"branch": "feature", "create": true})
	assert.Contains(t, created, "created and switched to branch feature")

	_, status := mustExecGitTool(t, tools, "git_status", nil)
	assert.Contains(t, status, "branch feature")
	_, branches := mustExecGitTool(t, tools, "git_branch_list", nil)
	assert.Contains(t, branches, "* feature")

	_, switched := mustExecGitTool(t, tools, "git_checkout_branch", map[string]any{"branch": original})
	assert.Contains(t, switched, "switched to branch "+original)
}

func TestUnit_GitTools_Restore(t *testing.T) {
	t.Parallel()
	dir := gitInitRepo(t)
	tools := localtools.NewGitTools(dir)
	gitWriteFile(t, dir, "a.txt", "dirty\n")

	_, restored := mustExecGitTool(t, tools, "git_restore", map[string]any{"paths": "a.txt"})
	assert.Contains(t, restored, "restored a.txt to HEAD")
	content, err := os.ReadFile(filepath.Join(dir, "a.txt"))
	require.NoError(t, err)
	assert.Equal(t, "one\ntwo\n", string(content))

	gitWriteFile(t, dir, "a.txt", "dirty\n")
	_, _ = mustExecGitTool(t, tools, "git_add", map[string]any{"paths": "a.txt"})
	_, unstaged := mustExecGitTool(t, tools, "git_restore", map[string]any{"paths": []string{"a.txt"}, "staged": true})
	assert.Contains(t, unstaged, "unstaged a.txt")
	content, err = os.ReadFile(filepath.Join(dir, "a.txt"))
	require.NoError(t, err)
	assert.Equal(t, "dirty\n", string(content))
	_, status := mustExecGitTool(t, tools, "git_status", nil)
	assert.Contains(t, status, "changed but not staged")
	assert.NotContains(t, status, "staged for commit")
}

func TestUnit_GitTools_NoRepositoryIsRefused(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	res, _, err := execGitTool(t, localtools.NewGitTools(dir), "git_status", nil)
	require.Error(t, err)
	assert.Nil(t, res)
	assert.Contains(t, err.Error(), "no git repository")
	assert.Contains(t, err.Error(), "not under version control")
}

func TestUnit_GitTools_RepositoryOutsideAllowedDirIsRefused(t *testing.T) {
	t.Parallel()
	outer := t.TempDir()
	gitInitRepoAt(t, outer)
	inner := filepath.Join(outer, "sub")
	require.NoError(t, os.MkdirAll(inner, 0o755))
	tools := localtools.NewGitTools(inner)

	for _, tool := range []string{"git_status", "git_log"} {
		res, _, err := execGitTool(t, tools, tool, nil)
		require.Error(t, err, "%s must refuse a repository outside the allowed dir", tool)
		assert.Nil(t, res, "%s must not return an empty success", tool)
		assert.Contains(t, err.Error(), "outside the allowed directory")
	}
}

func TestUnit_GitTools_DiffRefusesSymlinkOutsideRepository(t *testing.T) {
	t.Parallel()
	dir := gitInitRepo(t)
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	require.NoError(t, os.WriteFile(secret, []byte("outside-secret\n"), 0o644))

	// Stage a link whose target stays inside the repository: vfs.Contain
	// follows the leaf, so our git_add refuses a link that already points out.
	link := filepath.Join(dir, "leak.txt")
	if err := os.Symlink("a.txt", link); err != nil {
		t.Skipf("symlinks are unavailable here: %v", err)
	}
	repo, err := git.PlainOpen(dir)
	require.NoError(t, err)
	wt, err := repo.Worktree()
	require.NoError(t, err)
	_, err = wt.Add("leak.txt")
	require.NoError(t, err)
	_, err = wt.Commit("track a link", &git.CommitOptions{Author: gitSignature()})
	require.NoError(t, err)

	// Retarget the tracked link outside the repository. git_diff must refuse to
	// follow it when it reads the changed worktree file.
	require.NoError(t, os.Remove(link))
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks are unavailable here: %v", err)
	}

	tools := localtools.NewGitTools(dir)
	res, _, err := execGitTool(t, tools, "git_diff", nil)
	require.Error(t, err)
	assert.Nil(t, res)
	assert.Contains(t, err.Error(), "refusing to read leak.txt")
}

func TestUnit_GitTools_WorkingDirectoryIsTheBoundary(t *testing.T) {
	t.Parallel()
	outer := t.TempDir()
	gitInitRepoAt(t, outer)
	inner := filepath.Join(outer, "sub")
	require.NoError(t, os.MkdirAll(inner, 0o755))

	// No configured allowed dir: the session working directory is the boundary,
	// so a project opened inside a larger repository must not reach the parent.
	tools := localtools.NewGitToolsWith("", localtools.GitToolsName, func(context.Context) string { return inner })
	for _, tool := range []string{"git_status", "git_diff", "git_log"} {
		res, _, err := execGitTool(t, tools, tool, nil)
		require.Error(t, err, "%s must refuse the parent repository", tool)
		assert.Nil(t, res)
		assert.Contains(t, err.Error(), "outside the allowed directory")
	}
}

func TestUnit_GitTools_Declarations(t *testing.T) {
	t.Parallel()
	tools := localtools.NewGitTools(t.TempDir())
	supported, err := tools.Supports(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{localtools.GitToolsName}, supported)

	schemas, err := tools.GetSchemasForSupportedTools(context.Background())
	require.NoError(t, err)
	assert.Empty(t, schemas)

	declared, err := tools.GetToolsForToolsByName(context.Background(), localtools.GitToolsName)
	require.NoError(t, err)
	names := make([]string, 0, len(declared))
	for _, d := range declared {
		names = append(names, d.Function.Name)
	}
	assert.ElementsMatch(t, []string{
		"git_status", "git_diff", "git_log", "git_show", "git_branch_list",
		"git_blame", "git_add", "git_commit", "git_push", "git_checkout_branch", "git_restore",
	}, names)

	one, err := tools.GetToolsForToolsByName(context.Background(), "git_blame")
	require.NoError(t, err)
	require.Len(t, one, 1)
	assert.Equal(t, "git_blame", one[0].Function.Name)
}
