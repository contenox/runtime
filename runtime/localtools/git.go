package localtools

import (
	"context"
	"errors"
	"fmt"
	iofs "io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/contenox/runtime/runtime/vfs"
	"github.com/getkin/kin-openapi/openapi3"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

const GitToolsName = "native-git"

const (
	gitMaxOutputBytes = 32 * 1024

	gitDefaultLogCount = 10
	gitMaxLogCount     = 200

	gitMaxDiffFiles = 25
)

type GitTools struct {
	allowedDir  string
	name        string
	cwdResolver func(context.Context) string
	identity    GitIdentityResolver
}

func NewGitTools(allowedDir string) taskengine.ToolsRepo {
	return NewGitToolsWith(allowedDir, GitToolsName, nil)
}

func NewGitToolsWith(allowedDir, name string, cwdResolver func(context.Context) string, opts ...GitOption) taskengine.ToolsRepo {
	cleaned := allowedDir
	if cleaned != "" {
		cleaned = filepath.Clean(cleaned)
	}
	if name == "" {
		name = GitToolsName
	}
	tools := &GitTools{allowedDir: cleaned, name: name, cwdResolver: cwdResolver}
	for _, opt := range opts {
		opt(tools)
	}
	return tools
}

func (h *GitTools) Exec(ctx context.Context, startTime time.Time, input any, debug bool, toolsCall *taskengine.ToolsCall) (any, taskengine.DataType, error) {
	res, dt, err := h.execDispatch(ctx, input, toolsCall)
	return res, dt, markSeverity(err)
}

func (h *GitTools) execDispatch(ctx context.Context, input any, toolsCall *taskengine.ToolsCall) (any, taskengine.DataType, error) {
	if toolsCall == nil {
		return nil, taskengine.DataTypeAny, errors.New("git: tools required")
	}

	args, ok := input.(map[string]any)
	if !ok {
		if len(toolsCall.Args) > 0 {
			args = make(map[string]any, len(toolsCall.Args))
			for k, v := range toolsCall.Args {
				args[k] = v
			}
		} else {
			args = map[string]any{}
		}
	}

	toolName := toolsCall.ToolName
	if toolName == "" {
		toolName = toolsCall.Name
	}

	switch toolName {
	case "git_status":
		if err := rejectUnknownArgs(toolName, args); err != nil {
			return nil, taskengine.DataTypeAny, err
		}
		return h.status(ctx)
	case "git_diff":
		if err := rejectUnknownArgs(toolName, args, "path"); err != nil {
			return nil, taskengine.DataTypeAny, err
		}
		return h.diff(ctx, args)
	case "git_log":
		if err := rejectUnknownArgs(toolName, args, "n", "path"); err != nil {
			return nil, taskengine.DataTypeAny, err
		}
		return h.log(ctx, args)
	case "git_show":
		if err := rejectUnknownArgs(toolName, args, "ref"); err != nil {
			return nil, taskengine.DataTypeAny, err
		}
		return h.show(ctx, args)
	case "git_branch_list":
		if err := rejectUnknownArgs(toolName, args); err != nil {
			return nil, taskengine.DataTypeAny, err
		}
		return h.branchList(ctx)
	case "git_blame":
		if err := rejectUnknownArgs(toolName, args, "path"); err != nil {
			return nil, taskengine.DataTypeAny, err
		}
		return h.blame(ctx, args)
	case "git_add":
		if err := rejectUnknownArgs(toolName, args, "paths"); err != nil {
			return nil, taskengine.DataTypeAny, err
		}
		return h.add(ctx, args)
	case "git_commit":
		if err := rejectUnknownArgs(toolName, args, "message"); err != nil {
			return nil, taskengine.DataTypeAny, err
		}
		return h.commit(ctx, args)
	case "git_push":
		if err := rejectUnknownArgs(toolName, args, "remote", "branch"); err != nil {
			return nil, taskengine.DataTypeAny, err
		}
		return h.push(ctx, args)
	case "git_checkout_branch":
		if err := rejectUnknownArgs(toolName, args, "branch", "create"); err != nil {
			return nil, taskengine.DataTypeAny, err
		}
		return h.checkoutBranch(ctx, args)
	case "git_restore":
		if err := rejectUnknownArgs(toolName, args, "paths", "staged"); err != nil {
			return nil, taskengine.DataTypeAny, err
		}
		return h.restore(ctx, args)
	default:
		return nil, taskengine.DataTypeAny, fmt.Errorf("git: unknown tool %s", toolName)
	}
}

func (h *GitTools) policyArgs(ctx context.Context) map[string]string {
	return taskengine.ToolsArgsFromContext(ctx, h.name)
}

func (h *GitTools) cwd(ctx context.Context) string {
	if h.cwdResolver != nil {
		if r := strings.TrimSpace(h.cwdResolver(ctx)); r != "" {
			return filepath.Clean(r)
		}
	}
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

func (h *GitTools) baseDir(ctx context.Context) (dir string, bounded bool, err error) {
	if args := h.policyArgs(ctx); len(args) > 0 {
		if policyDir := strings.TrimSpace(args["_allowed_dir"]); policyDir != "" {
			cleaned := filepath.Clean(policyDir)
			if !filepath.IsAbs(cleaned) {
				if wd := h.cwd(ctx); wd != "" {
					cleaned = filepath.Clean(filepath.Join(wd, cleaned))
				}
			}
			return cleaned, true, nil
		}
	}
	if h.allowedDir != "" {
		return h.allowedDir, true, nil
	}
	// No configured workspace: the working directory is the boundary, exactly
	// as it is for local_fs. A session opened on a subdirectory of a larger
	// repository must not silently read and write the whole repository.
	if wd := h.cwd(ctx); wd != "" {
		return wd, true, nil
	}
	return "", false, errors.New("git: no working directory could be resolved")
}

func findRepoRoot(start string) (string, bool) {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func (h *GitTools) openRepo(ctx context.Context, tool string) (*git.Repository, string, error) {
	base, bounded, err := h.baseDir(ctx)
	if err != nil {
		return nil, "", err
	}
	start, err := vfs.ResolveRoot(base)
	if err != nil {
		return nil, "", fmt.Errorf("%s: invalid allowed dir: %w", tool, err)
	}
	root, found := findRepoRoot(start)
	if !found {
		return nil, "", recoverablef("%s: no git repository at %s or any parent directory — this workspace is not under version control", tool, start)
	}
	if bounded && !vfs.Within(start, root) {
		return nil, "", recoverablef(
			"%s: the git repository at %s lies outside the allowed directory %s — start the agent at the repository root (or point the allowed dir at it) to use the git tools here",
			tool, root, start)
	}
	if err := containGitDir(tool, start, bounded, root); err != nil {
		return nil, "", err
	}
	repo, err := git.PlainOpenWithOptions(root, &git.PlainOpenOptions{EnableDotGitCommonDir: true})
	if err != nil {
		return nil, "", recoverablef("%s: cannot open the git repository at %s: %v", tool, root, err)
	}
	return repo, root, nil
}

func containGitDir(tool, start string, bounded bool, root string) error {
	gitdir, err := effectiveGitDir(root)
	if err != nil {
		return recoverablef("%s: cannot resolve the git directory of %s: %v", tool, root, err)
	}
	if bounded {
		if _, err := vfs.Contain(start, gitdir); err != nil {
			switch {
			case errors.Is(err, vfs.ErrControlPlane):
				return recoverablef("%s: the git directory of the repository at %s resolves inside the runtime control plane — refused", tool, root)
			case errors.Is(err, vfs.ErrEscape):
				return recoverablef("%s: the git directory of the repository at %s escapes the allowed directory %s (a .git gitdir: pointer or symlink redirects it there) — refused", tool, root, start)
			default:
				return recoverablef("%s: %v", tool, err)
			}
		}
		return nil
	}
	if denied, ok := vfs.IsControlPlanePath(gitdir); ok {
		return recoverablef("%s: the git directory of the repository at %s resolves inside the runtime control plane (%s) — refused", tool, root, denied)
	}
	return nil
}

func effectiveGitDir(root string) (string, error) {
	dotgit := filepath.Join(root, ".git")
	fi, err := os.Lstat(dotgit)
	if err != nil {
		return "", err
	}
	if fi.Mode().IsRegular() {
		return parseGitdirFile(root, dotgit)
	}
	return dotgit, nil
}

func parseGitdirFile(root, dotgit string) (string, error) {
	data, err := os.ReadFile(dotgit)
	if err != nil {
		return "", err
	}
	for _, ln := range strings.Split(string(data), "\n") {
		target, ok := strings.CutPrefix(strings.TrimSpace(ln), "gitdir:")
		if !ok {
			continue
		}
		target = strings.TrimSpace(target)
		if target == "" {
			break
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(root, target)
		}
		return filepath.Clean(target), nil
	}
	return "", errors.New(".git file carries no gitdir: pointer")
}

func repoRelPath(root, tool, p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" || p == "." {
		return "", nil
	}
	abs, err := vfs.Contain(root, p)
	if err != nil {
		if errors.Is(err, vfs.ErrEscape) {
			return "", recoverablef("%s: path %s is outside the repository %s", tool, p, root)
		}
		return "", recoverablef("%s: %v", tool, err)
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", recoverablef("%s: path %s cannot be resolved inside the repository %s", tool, p, root)
	}
	if rel == "." {
		return "", nil
	}
	return filepath.ToSlash(rel), nil
}

func repoRelPaths(root, tool string, raw any) ([]string, error) {
	list, err := pathListArg(tool, "paths", raw)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(list))
	for _, p := range list {
		rel, err := repoRelPath(root, tool, p)
		if err != nil {
			return nil, err
		}
		out = append(out, rel)
	}
	if len(out) == 0 {
		return nil, recoverablef("%s: paths is required (a path, or an array of paths, relative to the repository root)", tool)
	}
	return out, nil
}

func truncateGitOutput(tool, s, hint string) string {
	if len(s) <= gitMaxOutputBytes {
		return s
	}
	cut := s[:gitMaxOutputBytes]
	if i := strings.LastIndexByte(cut, '\n'); i > 0 {
		cut = cut[:i+1]
	}
	return cut + fmt.Sprintf("... (%s truncated at %d bytes — %s)\n", tool, gitMaxOutputBytes, hint)
}

func headCommit(repo *git.Repository) (c *object.Commit, ok bool, err error) {
	ref, err := repo.Head()
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, false, err
	}
	return commit, true, nil
}

func shortHash(h plumbing.Hash) string {
	s := h.String()
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func subjectOf(msg string) string {
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	return strings.TrimSpace(msg)
}

func currentBranch(repo *git.Repository) (string, error) {
	ref, err := repo.Head()
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return "(no commits yet)", nil
		}
		return "", err
	}
	if ref.Name().IsBranch() {
		return ref.Name().Short(), nil
	}
	return "(detached at " + shortHash(ref.Hash()) + ")", nil
}

func (r GitStatusResult) AppendGuidance(text string) any {
	r.text += text
	return r
}

func (r GitLogResult) AppendGuidance(text string) any {
	r.text += text
	return r
}

func (r GitBranchListResult) AppendGuidance(text string) any {
	r.text += text
	return r
}

type GitCommitRef struct {
	Hash    string `json:"hash"`
	Subject string `json:"subject"`
}

type GitStatusEntry struct {
	Path string `json:"path"`
	Code string `json:"code"`
}

type GitStatusResult struct {
	Branch    string           `json:"branch"`
	Head      *GitCommitRef    `json:"head,omitempty"`
	Clean     bool             `json:"clean"`
	Staged    []GitStatusEntry `json:"staged"`
	Unstaged  []GitStatusEntry `json:"unstaged"`
	Untracked []string         `json:"untracked"`

	text string
}

func (r GitStatusResult) String() string { return r.text }

type GitLogEntry struct {
	Hash    string `json:"hash"`
	Author  string `json:"author"`
	Email   string `json:"email"`
	Date    string `json:"date"`
	Subject string `json:"subject"`
}

type GitLogResult struct {
	Commits []GitLogEntry `json:"commits"`

	text string
}

func (r GitLogResult) String() string { return r.text }

type GitBranch struct {
	Name    string `json:"name"`
	Hash    string `json:"hash"`
	Current bool   `json:"current"`
}

type GitBranchListResult struct {
	Current  string      `json:"current"`
	Branches []GitBranch `json:"branches"`

	text string
}

func (r GitBranchListResult) String() string { return r.text }

func (h *GitTools) status(ctx context.Context) (any, taskengine.DataType, error) {
	const tool = "git_status"
	repo, _, err := h.openRepo(ctx, tool)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return nil, taskengine.DataTypeAny, recoverablef("%s: %v", tool, err)
	}
	st, err := wt.Status()
	if err != nil {
		return nil, taskengine.DataTypeAny, recoverablef("%s: %v", tool, err)
	}

	branch, err := currentBranch(repo)
	if err != nil {
		return nil, taskengine.DataTypeAny, recoverablef("%s: %v", tool, err)
	}
	out := GitStatusResult{
		Branch:    branch,
		Staged:    []GitStatusEntry{},
		Unstaged:  []GitStatusEntry{},
		Untracked: []string{},
	}
	var staged, unstaged, untracked []string
	for _, path := range sortedStatusPaths(st) {
		fs := st[path]
		if fs.Staging == git.Untracked && fs.Worktree == git.Untracked {
			untracked = append(untracked, "  "+path)
			out.Untracked = append(out.Untracked, path)
			continue
		}
		if fs.Staging != git.Unmodified && fs.Staging != git.Untracked {
			staged = append(staged, fmt.Sprintf("  %c %s", fs.Staging, path))
			out.Staged = append(out.Staged, GitStatusEntry{Path: path, Code: string(fs.Staging)})
		}
		if fs.Worktree != git.Unmodified && fs.Worktree != git.Untracked {
			unstaged = append(unstaged, fmt.Sprintf("  %c %s", fs.Worktree, path))
			out.Unstaged = append(out.Unstaged, GitStatusEntry{Path: path, Code: string(fs.Worktree)})
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "branch %s\n", out.Branch)
	commit, hasHead, err := headCommit(repo)
	if err != nil {
		return nil, taskengine.DataTypeAny, recoverablef("%s: %v", tool, err)
	}
	if hasHead {
		out.Head = &GitCommitRef{Hash: shortHash(commit.Hash), Subject: subjectOf(commit.Message)}
		fmt.Fprintf(&sb, "HEAD %s %s\n", out.Head.Hash, out.Head.Subject)
	}
	if len(staged)+len(unstaged)+len(untracked) == 0 {
		sb.WriteString("working tree clean\n")
		out.Clean = true
		out.text = sb.String()
		return out, taskengine.DataTypeString, nil
	}
	section := func(title string, lines []string) {
		if len(lines) == 0 {
			return
		}
		fmt.Fprintf(&sb, "%s:\n%s\n", title, strings.Join(lines, "\n"))
	}
	section("staged for commit", staged)
	section("changed but not staged", unstaged)
	section("untracked", untracked)
	out.text = truncateGitOutput(tool, sb.String(), "narrow the workspace or commit some of it")
	return out, taskengine.DataTypeString, nil
}

func sortedStatusPaths(st git.Status) []string {
	paths := make([]string, 0, len(st))
	for p := range st {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

type gitDiffView struct {
	repo      *git.Repository
	root      string
	only      string
	changed   []string
	untracked []string
}

// diffView resolves the files git_diff would print content for. The read policy
// gates those paths before diff() reads anything, and diff() reads the same set.
func (h *GitTools) diffView(ctx context.Context, args map[string]any) (*gitDiffView, error) {
	const tool = "git_diff"
	repo, root, err := h.openRepo(ctx, tool)
	if err != nil {
		return nil, err
	}
	view := &gitDiffView{repo: repo, root: root}
	if raw, ok := argString(args, "path"); ok {
		if view.only, err = repoRelPath(root, tool, raw); err != nil {
			return nil, err
		}
	}
	wt, err := repo.Worktree()
	if err != nil {
		return nil, recoverablef("%s: %v", tool, err)
	}
	st, err := wt.Status()
	if err != nil {
		return nil, recoverablef("%s: %v", tool, err)
	}
	for _, path := range sortedStatusPaths(st) {
		if view.only != "" && path != view.only && !strings.HasPrefix(path, view.only+"/") {
			continue
		}
		fs := st[path]
		if fs.Staging == git.Untracked && fs.Worktree == git.Untracked {
			view.untracked = append(view.untracked, path)
			continue
		}
		view.changed = append(view.changed, path)
	}
	return view, nil
}

func (h *GitTools) diff(ctx context.Context, args map[string]any) (any, taskengine.DataType, error) {
	const tool = "git_diff"
	view, err := h.diffView(ctx, args)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	head, hasHead, err := headCommit(view.repo)
	if err != nil {
		return nil, taskengine.DataTypeAny, recoverablef("%s: %v", tool, err)
	}

	var sb strings.Builder
	if len(view.changed) == 0 {
		if view.only != "" {
			fmt.Fprintf(&sb, "no changes against HEAD under %s\n", view.only)
		} else {
			sb.WriteString("no changes against HEAD\n")
		}
	}
	changed := view.changed
	overflow := changed
	if len(changed) > gitMaxDiffFiles {
		overflow = changed[gitMaxDiffFiles:]
		changed = changed[:gitMaxDiffFiles]
	} else {
		overflow = nil
	}
	for _, path := range changed {
		oldContent := ""
		if hasHead {
			f, fErr := head.File(path)
			if fErr != nil && !errors.Is(fErr, object.ErrFileNotFound) {
				return nil, taskengine.DataTypeAny, recoverablef("%s: cannot read %s at HEAD: %v", tool, path, fErr)
			}
			if fErr == nil {
				content, cErr := f.Contents()
				if cErr != nil {
					return nil, taskengine.DataTypeAny, recoverablef("%s: cannot read %s at HEAD: %v", tool, path, cErr)
				}
				oldContent = content
			}
		}
		newContent := ""
		abs, containErr := vfs.Contain(view.root, path)
		if containErr != nil {
			return nil, taskengine.DataTypeAny, recoverablef("%s: refusing to read %s: %v", tool, path, containErr)
		}
		data, rErr := os.ReadFile(abs)
		if rErr != nil && !errors.Is(rErr, iofs.ErrNotExist) {
			return nil, taskengine.DataTypeAny, recoverablef("%s: cannot read %s: %v", tool, path, rErr)
		}
		if rErr == nil {
			newContent = string(data)
		}
		if isBinarySample(sniffPrefix([]byte(oldContent))) || isBinarySample(sniffPrefix([]byte(newContent))) {
			fmt.Fprintf(&sb, "diff --git a/%s b/%s\nBinary files differ\n", path, path)
			continue
		}
		fmt.Fprintf(&sb, "diff --git a/%s b/%s\n%s", path, path, gitFileDiff(path, oldContent, newContent))
	}
	if len(overflow) > 0 {
		fmt.Fprintf(&sb, "... %d more changed file(s) not shown: %s\n", len(overflow), strings.Join(overflow, ", "))
	}
	if len(view.untracked) > 0 {
		fmt.Fprintf(&sb, "untracked (no diff — not in the repository yet): %s\n", strings.Join(view.untracked, ", "))
	}
	return truncateGitOutput(tool, sb.String(), "pass path to diff one file or directory"), taskengine.DataTypeString, nil
}

func gitFileDiff(path, oldContent, newContent string) string {
	body := unifiedDiff(path, oldContent, newContent)
	if body == "(no changes)" {
		return ""
	}
	if i := strings.Index(body, "@@"); i >= 0 {
		body = body[i:]
	}
	return fmt.Sprintf("--- a/%s\n+++ b/%s\n%s", path, path, body)
}

func (h *GitTools) log(ctx context.Context, args map[string]any) (any, taskengine.DataType, error) {
	const tool = "git_log"
	repo, root, err := h.openRepo(ctx, tool)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	n := gitDefaultLogCount
	if v, ok := argInt(args, "n"); ok && v > 0 {
		n = v
	}
	if n > gitMaxLogCount {
		n = gitMaxLogCount
	}
	opts := &git.LogOptions{Order: git.LogOrderCommitterTime}
	if raw, ok := argString(args, "path"); ok {
		rel, relErr := repoRelPath(root, tool, raw)
		if relErr != nil {
			return nil, taskengine.DataTypeAny, relErr
		}
		if rel != "" {
			prefix := rel + "/"
			opts.PathFilter = func(p string) bool { return p == rel || strings.HasPrefix(p, prefix) }
		}
	}
	iter, err := repo.Log(opts)
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return GitLogResult{Commits: []GitLogEntry{}, text: "no commits yet\n"}, taskengine.DataTypeString, nil
		}
		return nil, taskengine.DataTypeAny, recoverablef("%s: %v", tool, err)
	}
	defer iter.Close()

	out := GitLogResult{Commits: []GitLogEntry{}}
	var sb strings.Builder
	count := 0
	err = iter.ForEach(func(c *object.Commit) error {
		if count >= n {
			return storerStop
		}
		count++
		entry := GitLogEntry{
			Hash:    shortHash(c.Hash),
			Author:  c.Author.Name,
			Email:   c.Author.Email,
			Date:    c.Author.When.Format(time.RFC3339),
			Subject: subjectOf(c.Message),
		}
		out.Commits = append(out.Commits, entry)
		fmt.Fprintf(&sb, "%s %s <%s> %s\n    %s\n",
			entry.Hash, entry.Author, entry.Email, entry.Date, entry.Subject)
		return nil
	})
	if err != nil && !errors.Is(err, storerStop) {
		return nil, taskengine.DataTypeAny, recoverablef("%s: %v", tool, err)
	}
	if count == 0 {
		return GitLogResult{Commits: []GitLogEntry{}, text: "no commits match\n"}, taskengine.DataTypeString, nil
	}
	out.text = truncateGitOutput(tool, sb.String(), "ask for fewer commits with n")
	return out, taskengine.DataTypeString, nil
}

var storerStop = errors.New("stop iteration")

func (h *GitTools) show(ctx context.Context, args map[string]any) (any, taskengine.DataType, error) {
	const tool = "git_show"
	ref, ok := argString(args, "ref")
	if !ok || strings.TrimSpace(ref) == "" {
		return nil, taskengine.DataTypeAny, recoverablef("%s: ref is required (a commit hash, branch, tag, or HEAD)", tool)
	}
	ref = strings.TrimSpace(ref)
	repo, _, err := h.openRepo(ctx, tool)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	hash, err := repo.ResolveRevision(plumbing.Revision(ref))
	if err != nil {
		return nil, taskengine.DataTypeAny, recoverablef("%s: cannot resolve %q in this repository — pass a commit hash, branch, tag, or HEAD", tool, ref)
	}
	commit, err := repo.CommitObject(*hash)
	if err != nil {
		return nil, taskengine.DataTypeAny, recoverablef("%s: %q does not name a commit: %v", tool, ref, err)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "commit %s\nAuthor: %s <%s>\nDate:   %s\n\n%s\n\n",
		commit.Hash.String(), commit.Author.Name, commit.Author.Email,
		commit.Author.When.Format(time.RFC3339), strings.TrimSpace(commit.Message))

	if commit.NumParents() == 0 {
		files, fErr := commit.Files()
		if fErr != nil {
			return nil, taskengine.DataTypeAny, recoverablef("%s: %v", tool, fErr)
		}
		sb.WriteString("(root commit) files:\n")
		if fErr := files.ForEach(func(f *object.File) error {
			fmt.Fprintf(&sb, "  %s\n", f.Name)
			return nil
		}); fErr != nil {
			return nil, taskengine.DataTypeAny, recoverablef("%s: %v", tool, fErr)
		}
		return truncateGitOutput(tool, sb.String(), "read individual files with local_fs"), taskengine.DataTypeString, nil
	}
	parent, err := commit.Parent(0)
	if err != nil {
		return nil, taskengine.DataTypeAny, recoverablef("%s: %v", tool, err)
	}
	patch, err := parent.PatchContext(ctx, commit)
	if err != nil {
		return nil, taskengine.DataTypeAny, recoverablef("%s: %v", tool, err)
	}
	sb.WriteString(patch.String())
	return truncateGitOutput(tool, sb.String(), "show a narrower ref, or read files with local_fs"), taskengine.DataTypeString, nil
}

// ReadPaths lists the worktree or history files a call is about to print, so
// the HITL wrapper can run the read policy over each one before any content is
// produced. Tools that print names only return no paths.
func (h *GitTools) ReadPaths(ctx context.Context, toolsCall *taskengine.ToolsCall, args map[string]any) ([]string, error) {
	if toolsCall == nil {
		return nil, nil
	}
	toolName := toolsCall.ToolName
	if toolName == "" {
		toolName = toolsCall.Name
	}
	switch toolName {
	case "git_diff":
		view, err := h.diffView(ctx, args)
		if err != nil {
			return nil, err
		}
		if len(view.changed) > gitMaxDiffFiles {
			return view.changed[:gitMaxDiffFiles], nil
		}
		return view.changed, nil
	case "git_blame":
		_, root, err := h.openRepo(ctx, "git_blame")
		if err != nil {
			return nil, err
		}
		raw, ok := argString(args, "path")
		if !ok || strings.TrimSpace(raw) == "" {
			return nil, nil
		}
		rel, err := repoRelPath(root, "git_blame", raw)
		if err != nil || rel == "" {
			return nil, err
		}
		return []string{rel}, nil
	case "git_show":
		return h.showReadPaths(ctx, args)
	}
	return nil, nil
}

// showReadPaths names the files whose hunks git_show would print. A root commit
// is shown as a file list, so it has no content to gate.
func (h *GitTools) showReadPaths(ctx context.Context, args map[string]any) ([]string, error) {
	ref, ok := argString(args, "ref")
	if !ok || strings.TrimSpace(ref) == "" {
		return nil, nil
	}
	repo, _, err := h.openRepo(ctx, "git_show")
	if err != nil {
		return nil, err
	}
	hash, err := repo.ResolveRevision(plumbing.Revision(strings.TrimSpace(ref)))
	if err != nil {
		return nil, nil
	}
	commit, err := repo.CommitObject(*hash)
	if err != nil {
		return nil, nil
	}
	if commit.NumParents() == 0 {
		return nil, nil
	}
	parent, err := commit.Parent(0)
	if err != nil {
		return nil, nil
	}
	patch, err := parent.PatchContext(ctx, commit)
	if err != nil {
		return nil, nil
	}
	var out []string
	for _, fp := range patch.FilePatches() {
		from, to := fp.Files()
		if from != nil {
			out = append(out, from.Path())
		}
		if to != nil && (from == nil || from.Path() != to.Path()) {
			out = append(out, to.Path())
		}
	}
	return out, nil
}

func (h *GitTools) branchList(ctx context.Context) (any, taskengine.DataType, error) {
	const tool = "git_branch_list"
	repo, _, err := h.openRepo(ctx, tool)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	current, err := currentBranch(repo)
	if err != nil {
		return nil, taskengine.DataTypeAny, recoverablef("%s: %v", tool, err)
	}
	iter, err := repo.Branches()
	if err != nil {
		return nil, taskengine.DataTypeAny, recoverablef("%s: %v", tool, err)
	}
	defer iter.Close()

	out := GitBranchListResult{Current: current, Branches: []GitBranch{}}
	var names []string
	err = iter.ForEach(func(ref *plumbing.Reference) error {
		name := ref.Name().Short()
		marker := "  "
		if name == current {
			marker = "* "
		}
		out.Branches = append(out.Branches, GitBranch{Name: name, Hash: shortHash(ref.Hash()), Current: name == current})
		names = append(names, fmt.Sprintf("%s%s %s", marker, name, shortHash(ref.Hash())))
		return nil
	})
	if err != nil {
		return nil, taskengine.DataTypeAny, recoverablef("%s: %v", tool, err)
	}
	if len(names) == 0 {
		return GitBranchListResult{Current: current, Branches: []GitBranch{}, text: "no branches yet (the repository has no commits)\n"}, taskengine.DataTypeString, nil
	}
	sort.Strings(names)
	sort.Slice(out.Branches, func(i, j int) bool { return out.Branches[i].Name < out.Branches[j].Name })
	out.text = truncateGitOutput(tool, strings.Join(names, "\n")+"\n", "the repository has many branches")
	return out, taskengine.DataTypeString, nil
}

func (h *GitTools) blame(ctx context.Context, args map[string]any) (any, taskengine.DataType, error) {
	const tool = "git_blame"
	raw, ok := argString(args, "path")
	if !ok || strings.TrimSpace(raw) == "" {
		return nil, taskengine.DataTypeAny, recoverablef("%s: path is required (a tracked file, relative to the repository root)", tool)
	}
	repo, root, err := h.openRepo(ctx, tool)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	rel, err := repoRelPath(root, tool, raw)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	if rel == "" {
		return nil, taskengine.DataTypeAny, recoverablef("%s: path must name a file, not the repository root", tool)
	}
	commit, ok, err := headCommit(repo)
	if err != nil {
		return nil, taskengine.DataTypeAny, recoverablef("%s: %v", tool, err)
	}
	if !ok {
		return nil, taskengine.DataTypeAny, recoverablef("%s: the repository has no commits yet, so %s has no history", tool, rel)
	}
	result, err := git.Blame(commit, rel)
	if err != nil {
		return nil, taskengine.DataTypeAny, recoverablef("%s: cannot blame %s — is it tracked in this repository? (%v)", tool, rel, err)
	}
	var sb strings.Builder
	for i, line := range result.Lines {
		fmt.Fprintf(&sb, "%s %-20s %4d) %s\n", shortHash(line.Hash), truncateField(line.AuthorName, 20), i+1, line.Text)
	}
	return truncateGitOutput(tool, sb.String(), "blame a smaller file, or read it with local_fs"), taskengine.DataTypeString, nil
}

func truncateField(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func (h *GitTools) add(ctx context.Context, args map[string]any) (any, taskengine.DataType, error) {
	const tool = "git_add"
	raw, ok := args["paths"]
	if !ok {
		return nil, taskengine.DataTypeAny, recoverablef("%s: paths is required (a path, or an array of paths, relative to the repository root)", tool)
	}
	repo, root, err := h.openRepo(ctx, tool)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	rels, err := repoRelPaths(root, tool, raw)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return nil, taskengine.DataTypeAny, recoverablef("%s: %v", tool, err)
	}
	var staged []string
	for _, rel := range rels {
		opts := &git.AddOptions{Path: rel}
		if rel == "" {
			opts = &git.AddOptions{All: true}
		}
		if err := wt.AddWithOptions(opts); err != nil {
			return nil, taskengine.DataTypeAny, recoverablef("%s: cannot stage %s: %v", tool, displayRel(rel), err)
		}
		staged = append(staged, displayRel(rel))
	}
	return fmt.Sprintf("staged %s\n", strings.Join(staged, ", ")), taskengine.DataTypeString, nil
}

func displayRel(rel string) string {
	if rel == "" {
		return "everything"
	}
	return rel
}

func (h *GitTools) commit(ctx context.Context, args map[string]any) (any, taskengine.DataType, error) {
	const tool = "git_commit"
	message, ok := argString(args, "message")
	if !ok || strings.TrimSpace(message) == "" {
		return nil, taskengine.DataTypeAny, recoverablef("%s: message is required", tool)
	}
	repo, root, err := h.openRepo(ctx, tool)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return nil, taskengine.DataTypeAny, recoverablef("%s: %v", tool, err)
	}
	st, err := wt.Status()
	if err != nil {
		return nil, taskengine.DataTypeAny, recoverablef("%s: %v", tool, err)
	}
	staged := 0
	for _, path := range sortedStatusPaths(st) {
		if fs := st[path]; fs.Staging != git.Unmodified && fs.Staging != git.Untracked {
			staged++
		}
	}
	if staged == 0 {
		return nil, taskengine.DataTypeAny, recoverablef("%s: nothing is staged — call git_add with the paths to include, then commit", tool)
	}

	identity, err := h.resolveIdentity(ctx, tool, root)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	defer identity.release()
	if identity != nil {
		message = withTrailer(message, identity.Trailer)
	}
	hash, err := wt.Commit(strings.TrimSpace(message), commitOptionsFor(identity, time.Now()))
	if err != nil {
		if errors.Is(err, git.ErrMissingAuthor) {
			return nil, taskengine.DataTypeAny, recoverablef("%s: this repository has no commit identity — set user.name and user.email in its git config first", tool)
		}
		return nil, taskengine.DataTypeAny, recoverablef("%s: %v", tool, err)
	}
	branch, err := currentBranch(repo)
	if err != nil {
		return nil, taskengine.DataTypeAny, recoverablef("%s: %v", tool, err)
	}
	return fmt.Sprintf("committed %s on %s%s: %s (%d file(s) staged)\n",
		shortHash(hash), branch, commitAttribution(identity), subjectOf(message), staged), taskengine.DataTypeString, nil
}

func (h *GitTools) checkoutBranch(ctx context.Context, args map[string]any) (any, taskengine.DataType, error) {
	const tool = "git_checkout_branch"
	branch, ok := argString(args, "branch")
	if !ok || strings.TrimSpace(branch) == "" {
		return nil, taskengine.DataTypeAny, recoverablef("%s: branch is required", tool)
	}
	branch = strings.TrimSpace(branch)
	create, _ := argBool(args, "create")

	repo, _, err := h.openRepo(ctx, tool)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return nil, taskengine.DataTypeAny, recoverablef("%s: %v", tool, err)
	}
	err = wt.Checkout(&git.CheckoutOptions{Branch: plumbing.NewBranchReferenceName(branch), Create: create})
	if err != nil {
		switch {
		case errors.Is(err, git.ErrBranchExists):
			return nil, taskengine.DataTypeAny, recoverablef("%s: branch %s already exists — call it again with create=false to switch to it", tool, branch)
		case errors.Is(err, plumbing.ErrReferenceNotFound):
			return nil, taskengine.DataTypeAny, recoverablef("%s: branch %s does not exist — call it again with create=true to create it", tool, branch)
		default:
			return nil, taskengine.DataTypeAny, recoverablef("%s: cannot switch to %s: %v", tool, branch, err)
		}
	}
	verb := "switched to"
	if create {
		verb = "created and switched to"
	}
	return fmt.Sprintf("%s branch %s\n", verb, branch), taskengine.DataTypeString, nil
}

func (h *GitTools) restore(ctx context.Context, args map[string]any) (any, taskengine.DataType, error) {
	const tool = "git_restore"
	raw, ok := args["paths"]
	if !ok {
		return nil, taskengine.DataTypeAny, recoverablef("%s: paths is required (a path, or an array of paths, relative to the repository root)", tool)
	}
	stagedOnly, _ := argBool(args, "staged")

	repo, root, err := h.openRepo(ctx, tool)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	rels, err := repoRelPaths(root, tool, raw)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	for _, rel := range rels {
		if rel == "" {
			return nil, taskengine.DataTypeAny, recoverablef("%s: name the files to restore — restoring the whole repository at once is refused", tool)
		}
	}
	wt, err := repo.Worktree()
	if err != nil {
		return nil, taskengine.DataTypeAny, recoverablef("%s: %v", tool, err)
	}
	opts := &git.RestoreOptions{Files: rels, Staged: true, Worktree: !stagedOnly}
	if err := wt.Restore(opts); err != nil {
		return nil, taskengine.DataTypeAny, recoverablef("%s: cannot restore %s: %v", tool, strings.Join(rels, ", "), err)
	}
	if stagedOnly {
		return fmt.Sprintf("unstaged %s (file contents left alone)\n", strings.Join(rels, ", ")), taskengine.DataTypeString, nil
	}
	return fmt.Sprintf("restored %s to HEAD (uncommitted changes discarded)\n", strings.Join(rels, ", ")), taskengine.DataTypeString, nil
}

func (h *GitTools) Supports(ctx context.Context) ([]string, error) {
	return []string{h.name}, nil
}

func (h *GitTools) GetSchemasForSupportedTools(ctx context.Context) (map[string]*openapi3.T, error) {
	return map[string]*openapi3.T{}, nil
}

func gitPathsProp() map[string]any {
	return map[string]any{
		"type":        []any{"string", "array"},
		"items":       map[string]any{"type": "string"},
		"description": "A path relative to the repository root, or an array of them. One string is one path: it is not split on spaces, so a name containing spaces needs no quoting and two paths need an array.",
	}
}

func (h *GitTools) GetToolsForToolsByName(ctx context.Context, name string) ([]taskengine.Tool, error) {
	allTools := []taskengine.Tool{
		fsTool("git_status", "Branch, HEAD commit, and what is staged, changed, or untracked in the workspace repository.", map[string]any{}),

		fsTool("git_diff", "Unified diff of the working tree against HEAD (staged and unstaged together). Optional path narrows it to one file or directory.", map[string]any{
			"path": fsProp("string", "Optional file or directory, relative to the repository root"),
		}),

		fsTool("git_log", "Recent commits, newest first: short hash, author, date, subject.", map[string]any{
			"n":    fsProp("integer", "How many commits to return (default 10, max 200)"),
			"path": fsProp("string", "Optional file or directory: only commits touching it"),
		}),

		fsTool("git_show", "One commit: metadata, message, and its diff against its first parent.", map[string]any{
			"ref": fsProp("string", "Commit hash, branch, tag, or HEAD (HEAD~1 and friends work)"),
		}, "ref"),

		fsTool("git_branch_list", "Local branches with their head commits; the current branch is marked with *.", map[string]any{}),

		fsTool("git_blame", "Per-line authorship of a tracked file at HEAD: commit, author, line number, text.", map[string]any{
			"path": fsProp("string", "Tracked file, relative to the repository root"),
		}, "path"),

		fsTool("git_add", "Stage paths for the next commit. Pass \".\" to stage everything.", map[string]any{
			"paths": gitPathsProp(),
		}, "paths"),

		fsTool("git_commit", "Commit what is staged. Refuses when nothing is staged. The author is the agent identity set in Settings, or the repository's own git config when none is set.", map[string]any{
			"message": fsProp("string", "Commit message; the first line is the subject"),
		}, "message"),

		fsTool("git_push", "Push a branch to a remote with the agent's SSH key. Never force-pushes: if the remote has commits the branch lacks, it refuses.", map[string]any{
			"remote": fsProp("string", "Remote name; defaults to origin"),
			"branch": fsProp("string", "Branch to push; defaults to the current branch"),
		}),

		fsTool("git_checkout_branch", "Switch to a branch, or create it first with create=true.", map[string]any{
			"branch": fsProp("string", "Branch name"),
			"create": fsProp("boolean", "Create the branch at the current commit instead of expecting it to exist"),
		}, "branch"),

		fsTool("git_restore", "DESTRUCTIVE: throw away uncommitted changes to the named paths (back to HEAD). With staged=true it only unstages them and leaves the file contents alone.", map[string]any{
			"paths":  gitPathsProp(),
			"staged": fsProp("boolean", "Only unstage (index back to HEAD); file contents are left untouched"),
		}, "paths"),
	}

	if name == h.name {
		return allTools, nil
	}
	for _, t := range allTools {
		if t.Function.Name == name {
			return []taskengine.Tool{t}, nil
		}
	}
	return nil, fmt.Errorf("unknown tools tool: %s", name)
}

var _ taskengine.ToolsRepo = (*GitTools)(nil)
var _ ReadPathProvider = (*GitTools)(nil)
