package localtools_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/contenox/runtime/runtime/agentidentity"
	"github.com/contenox/runtime/runtime/localtools"
	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func agentIdentityFor(t *testing.T, signer ssh.Signer) localtools.GitIdentityResolver {
	t.Helper()
	return func(context.Context, string) (*localtools.GitIdentity, error) {
		identity := &localtools.GitIdentity{
			Name:    "Contenox Agent",
			Email:   "agent@example.com",
			Trailer: "Co-authored-by: Test Author <test@example.com>",
		}
		if signer != nil {
			identity.Signer = agentidentity.NewCommitSigner(signer)
		}
		return identity, nil
	}
}

func TestUnit_GitCommit_UsesTheAgentIdentitySignsAndCreditsTheUser(t *testing.T) {
	dir := gitInitRepo(t)
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)
	tools := localtools.NewGitToolsWith(dir, localtools.GitToolsName, nil, localtools.WithGitIdentity(agentIdentityFor(t, signer)))

	gitWriteFile(t, dir, "b.txt", "new\n")
	mustExecGitTool(t, tools, "git_add", map[string]any{"paths": "b.txt"})
	_, out := mustExecGitTool(t, tools, "git_commit", map[string]any{"message": "add b"})
	require.Contains(t, out, "as Contenox Agent <agent@example.com>, signed")

	repo, err := git.PlainOpen(dir)
	require.NoError(t, err)
	head, err := repo.Head()
	require.NoError(t, err)
	commit, err := repo.CommitObject(head.Hash())
	require.NoError(t, err)
	require.Equal(t, "Contenox Agent", commit.Author.Name)
	require.Equal(t, "agent@example.com", commit.Committer.Email)
	require.Equal(t, "add b\n\nCo-authored-by: Test Author <test@example.com>", commit.Message)
	require.NotEmpty(t, commit.PGPSignature, "the commit carries its signature")

	encoded := &plumbing.MemoryObject{}
	require.NoError(t, commit.EncodeWithoutSignature(encoded))
	reader, err := encoded.Reader()
	require.NoError(t, err)
	payload := make([]byte, encoded.Size())
	_, err = reader.Read(payload)
	require.NoError(t, err)
	pub, err := agentidentity.VerifySignature([]byte(commit.PGPSignature), payload)
	require.NoError(t, err, "the signature verifies against the commit as stored")
	require.Equal(t, ssh.FingerprintSHA256(signer.PublicKey()), ssh.FingerprintSHA256(pub))
}

func TestUnit_GitCommit_WithoutAnIdentityKeepsTheRepositoryAuthor(t *testing.T) {
	dir := gitInitRepo(t)
	tools := localtools.NewGitToolsWith(dir, localtools.GitToolsName, nil, localtools.WithGitIdentity(func(context.Context, string) (*localtools.GitIdentity, error) {
		return &localtools.GitIdentity{}, nil
	}))
	gitWriteFile(t, dir, "b.txt", "new\n")
	mustExecGitTool(t, tools, "git_add", map[string]any{"paths": "b.txt"})
	_, out := mustExecGitTool(t, tools, "git_commit", map[string]any{"message": "add b"})
	require.NotContains(t, out, " as ")

	repo, err := git.PlainOpen(dir)
	require.NoError(t, err)
	head, err := repo.Head()
	require.NoError(t, err)
	commit, err := repo.CommitObject(head.Hash())
	require.NoError(t, err)
	require.Equal(t, "Test Author", commit.Author.Name)
	require.Empty(t, commit.PGPSignature)
}

func TestUnit_GitCommit_RefusesWhenTheSigningKeyCannotBeUsed(t *testing.T) {
	dir := gitInitRepo(t)
	tools := localtools.NewGitToolsWith(dir, localtools.GitToolsName, nil, localtools.WithGitIdentity(func(context.Context, string) (*localtools.GitIdentity, error) {
		return nil, errors.New("the agent's signing key cannot be used (gone); fix it under Settings, Agent identity")
	}))
	gitWriteFile(t, dir, "b.txt", "new\n")
	mustExecGitTool(t, tools, "git_add", map[string]any{"paths": "b.txt"})
	_, _, err := execGitTool(t, tools, "git_commit", map[string]any{"message": "add b"})
	require.ErrorContains(t, err, "Settings, Agent identity", "a broken key fails the commit instead of committing unsigned")
}

func TestUnit_GitPush_RefusesWhatItCannotDoSafely(t *testing.T) {
	dir := gitInitRepo(t)
	repo, err := git.PlainOpen(dir)
	require.NoError(t, err)
	_, err = repo.CreateRemote(&gitconfig.RemoteConfig{Name: "origin", URLs: []string{"https://github.com/example/repo.git"}})
	require.NoError(t, err)
	_, err = repo.CreateRemote(&gitconfig.RemoteConfig{Name: "ssh", URLs: []string{"git@github.com:example/repo.git"}})
	require.NoError(t, err)

	noKey := localtools.NewGitToolsWith(dir, localtools.GitToolsName, nil, localtools.WithGitIdentity(func(context.Context, string) (*localtools.GitIdentity, error) {
		return &localtools.GitIdentity{}, nil
	}))
	_, _, err = execGitTool(t, noKey, "git_push", nil)
	require.ErrorContains(t, err, "must be an SSH URL", "an HTTPS remote is named, with the form that works")
	_, _, err = execGitTool(t, noKey, "git_push", map[string]any{"remote": "ssh"})
	require.ErrorContains(t, err, "no SSH key to push with")
	_, _, err = execGitTool(t, noKey, "git_push", map[string]any{"remote": "nope"})
	require.ErrorContains(t, err, `no remote named "nope"`)

	var gotUser, gotHost string
	withKey := localtools.NewGitToolsWith(dir, localtools.GitToolsName, nil, localtools.WithGitIdentity(func(context.Context, string) (*localtools.GitIdentity, error) {
		return &localtools.GitIdentity{PushAuth: func(user, host string) (transport.AuthMethod, func(), error) {
			gotUser, gotHost = user, host
			return nil, func() {}, errors.New("stop before the network")
		}}, nil
	}))
	_, _, err = execGitTool(t, withKey, "git_push", map[string]any{"remote": "ssh"})
	require.ErrorContains(t, err, "stop before the network")
	require.Equal(t, "git", gotUser)
	require.Equal(t, "github.com", gotHost)
}

func TestUnit_GitCommit_JoinsAnExistingTrailerBlock(t *testing.T) {
	dir := gitInitRepo(t)
	tools := localtools.NewGitToolsWith(dir, localtools.GitToolsName, nil, localtools.WithGitIdentity(agentIdentityFor(t, nil)))
	gitWriteFile(t, dir, "b.txt", "new\n")
	mustExecGitTool(t, tools, "git_add", map[string]any{"paths": "b.txt"})
	mustExecGitTool(t, tools, "git_commit", map[string]any{"message": "add b\n\nWhy it was needed.\n\nSigned-off-by: Someone <s@example.com>"})

	repo, err := git.PlainOpen(dir)
	require.NoError(t, err)
	head, err := repo.Head()
	require.NoError(t, err)
	commit, err := repo.CommitObject(head.Hash())
	require.NoError(t, err)
	require.Equal(t, "add b\n\nWhy it was needed.\n\nSigned-off-by: Someone <s@example.com>\nCo-authored-by: Test Author <test@example.com>", commit.Message,
		"the credit joins the trailer block instead of starting a second one")

	gitWriteFile(t, dir, "c.txt", "new\n")
	mustExecGitTool(t, tools, "git_add", map[string]any{"paths": "c.txt"})
	mustExecGitTool(t, tools, "git_commit", map[string]any{"message": "add c\n\nco-authored-by: test author <test@example.com>"})
	head, err = repo.Head()
	require.NoError(t, err)
	commit, err = repo.CommitObject(head.Hash())
	require.NoError(t, err)
	require.Equal(t, "add c\n\nco-authored-by: test author <test@example.com>", commit.Message, "an existing credit is not repeated")
}

func TestUnit_GitPush_RefusesAnLFSRepository(t *testing.T) {
	dir := gitInitRepo(t)
	gitWriteFile(t, dir, ".gitattributes", "*.bin filter=lfs diff=lfs merge=lfs -text\n")
	tools := localtools.NewGitToolsWith(dir, localtools.GitToolsName, nil)
	_, _, err := execGitTool(t, tools, "git_push", nil)
	require.ErrorContains(t, err, "Git LFS")
}
