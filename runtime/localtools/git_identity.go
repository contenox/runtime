package localtools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/contenox/runtime/runtime/taskengine"
	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

const pushTimeout = 3 * time.Minute

type GitIdentity struct {
	Name     string
	Email    string
	Signer   git.Signer
	Trailer  string
	PushAuth func(user, host string) (transport.AuthMethod, func(), error)
	Close    func()
}

func (g *GitIdentity) active() bool {
	return g != nil && strings.TrimSpace(g.Name) != "" && strings.TrimSpace(g.Email) != ""
}

func (g *GitIdentity) release() {
	if g != nil && g.Close != nil {
		g.Close()
	}
}

type GitIdentityResolver func(ctx context.Context, repoDir string) (*GitIdentity, error)

type GitOption func(*GitTools)

func WithGitIdentity(resolve GitIdentityResolver) GitOption {
	return func(h *GitTools) { h.identity = resolve }
}

func (h *GitTools) resolveIdentity(ctx context.Context, tool, repoDir string) (*GitIdentity, error) {
	if h.identity == nil {
		return nil, nil
	}
	identity, err := h.identity(ctx, repoDir)
	if err != nil {
		return nil, recoverablef("%s: %v", tool, err)
	}
	return identity, nil
}

func commitOptionsFor(identity *GitIdentity, now time.Time) *git.CommitOptions {
	opts := &git.CommitOptions{}
	if !identity.active() {
		return opts
	}
	sig := &object.Signature{Name: identity.Name, Email: identity.Email, When: now}
	opts.Author = sig
	opts.Committer = sig
	opts.Signer = identity.Signer
	return opts
}

var trailerLine = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*: \S`)

func withTrailer(message, trailer string) string {
	message = strings.TrimSpace(message)
	trailer = strings.TrimSpace(trailer)
	if trailer == "" || strings.Contains(strings.ToLower(message), strings.ToLower(trailer)) {
		return message
	}
	paragraphs := strings.Split(message, "\n\n")
	if len(paragraphs) > 1 && isTrailerBlock(paragraphs[len(paragraphs)-1]) {
		return message + "\n" + trailer
	}
	return message + "\n\n" + trailer
}

func isTrailerBlock(paragraph string) bool {
	for _, line := range strings.Split(strings.TrimSpace(paragraph), "\n") {
		if !trailerLine.MatchString(strings.TrimSpace(line)) {
			return false
		}
	}
	return true
}

func commitAttribution(identity *GitIdentity) string {
	if !identity.active() {
		return ""
	}
	out := fmt.Sprintf(" as %s <%s>", identity.Name, identity.Email)
	if identity.Signer != nil {
		out += ", signed"
	}
	return out
}

func usesLFS(root string) bool {
	data, err := os.ReadFile(filepath.Join(root, ".gitattributes"))
	return err == nil && strings.Contains(string(data), "filter=lfs")
}

func (h *GitTools) push(ctx context.Context, args map[string]any) (any, taskengine.DataType, error) {
	const tool = "git_push"
	repo, root, err := h.openRepo(ctx, tool)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	if usesLFS(root) {
		return nil, taskengine.DataTypeAny, recoverablef("%s: this repository stores files with Git LFS, which this push cannot upload; push it with git instead", tool)
	}
	remoteName, _ := argString(args, "remote")
	remoteName = strings.TrimSpace(remoteName)
	if remoteName == "" {
		remoteName = "origin"
	}
	branch, _ := argString(args, "branch")
	branch = strings.TrimSpace(branch)
	if branch == "" {
		if branch, err = currentBranch(repo); err != nil {
			return nil, taskengine.DataTypeAny, recoverablef("%s: %v", tool, err)
		}
	}
	if branch == "" || branch == "HEAD" {
		return nil, taskengine.DataTypeAny, recoverablef("%s: HEAD is detached; check out a branch first", tool)
	}
	remote, err := repo.Remote(remoteName)
	if err != nil {
		return nil, taskengine.DataTypeAny, recoverablef("%s: this repository has no remote named %q", tool, remoteName)
	}
	urls := remote.Config().URLs
	if len(urls) == 0 {
		return nil, taskengine.DataTypeAny, recoverablef("%s: remote %q has no URL", tool, remoteName)
	}
	endpoint, err := transport.NewEndpoint(urls[0])
	if err != nil {
		return nil, taskengine.DataTypeAny, recoverablef("%s: remote %q: %v", tool, remoteName, err)
	}
	if endpoint.Protocol != "ssh" {
		return nil, taskengine.DataTypeAny, recoverablef("%s: remote %q is %s; the agent pushes with its SSH key, so the remote must be an SSH URL such as git@github.com:owner/repo.git", tool, remoteName, urls[0])
	}
	identity, err := h.resolveIdentity(ctx, tool, root)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	defer identity.release()
	if identity == nil || identity.PushAuth == nil {
		return nil, taskengine.DataTypeAny, recoverablef("%s: the agent has no SSH key to push with; set one under Settings, Agent identity", tool)
	}
	user := endpoint.User
	if user == "" {
		user = "git"
	}
	auth, closeAuth, err := identity.PushAuth(user, endpoint.Host)
	if err != nil {
		return nil, taskengine.DataTypeAny, recoverablef("%s: %v", tool, err)
	}
	defer closeAuth()
	spec := gitconfig.RefSpec(fmt.Sprintf("refs/heads/%s:refs/heads/%s", branch, branch))
	pushCtx, cancel := context.WithTimeout(ctx, pushTimeout)
	defer cancel()
	err = repo.PushContext(pushCtx, &git.PushOptions{
		RemoteName: remoteName,
		RefSpecs:   []gitconfig.RefSpec{spec},
		Auth:       auth,
	})
	switch {
	case errors.Is(err, git.NoErrAlreadyUpToDate):
		return fmt.Sprintf("%s on %s is already up to date\n", branch, remoteName), taskengine.DataTypeString, nil
	case err != nil && errors.Is(pushCtx.Err(), context.DeadlineExceeded):
		return nil, taskengine.DataTypeAny, recoverablef("%s: %s did not answer within %s; check the connection and try again", tool, endpoint.Host, pushTimeout)
	case err != nil && strings.Contains(err.Error(), "non-fast-forward"):
		return nil, taskengine.DataTypeAny, recoverablef("%s: %s has commits that %s lacks; bring them in first. The agent never force-pushes", tool, remoteName, branch)
	case err != nil:
		return nil, taskengine.DataTypeAny, recoverablef("%s: %v", tool, err)
	}
	return fmt.Sprintf("pushed %s to %s (%s)\n", branch, remoteName, urls[0]), taskengine.DataTypeString, nil
}
