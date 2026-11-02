package agentidentity

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func isolatedGitHome(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", "")
	require.NoError(t, os.Unsetenv("GIT_CONFIG_GLOBAL"))
	return home
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

func TestUnit_Credit_FollowsIncludesAndTheRepositoryOverride(t *testing.T) {
	home := isolatedGitHome(t)
	include := filepath.Join(home, "identity.gitconfig")
	require.NoError(t, os.WriteFile(include, []byte("[user]\n\tname = Included Person\n\temail = included@example.com\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[include]\n\tpath = "+filepath.ToSlash(include)+"\n"), 0o644))

	me, err := DefaultPerson(context.Background())
	require.NoError(t, err)
	require.Equal(t, "Included Person <included@example.com>", me.String(), "an [include] is followed, as git itself does")
	require.Equal(t, PersonFromGit, me.Source)

	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "config", "user.email", "work@example.com")

	identity := Identity{Name: "Agent", Email: "agent@example.com", CreditMe: true}
	person, ok := identity.Credit(context.Background(), repo)
	require.True(t, ok)
	require.Equal(t, "Included Person <work@example.com>", person.String(), "the repository's own user.email wins inside that repository")

	elsewhere := t.TempDir()
	runGit(t, elsewhere, "init", "-q")
	person, ok = identity.Credit(context.Background(), elsewhere)
	require.True(t, ok)
	require.Equal(t, "included@example.com", person.Email)
}

func TestUnit_Credit_OverrideWinsAndTheAgentNeverCreditsItself(t *testing.T) {
	isolatedGitHome(t)
	identity := Identity{Name: "Agent", Email: "agent@example.com", CreditMe: true, CreditAs: "Alex Example <alex@example.com>"}
	person, ok := identity.Credit(context.Background(), "")
	require.True(t, ok)
	require.Equal(t, "Alex Example <alex@example.com>", person.String())
	require.Equal(t, PersonFromOverride, person.Source)

	identity.CreditAs = "Agent <AGENT@example.com>"
	_, ok = identity.Credit(context.Background(), "")
	require.False(t, ok)

	identity.CreditAs = ""
	identity.CreditMe = false
	_, ok = identity.Credit(context.Background(), "")
	require.False(t, ok)

	require.Error(t, Identity{CreditAs: "just a name"}.Validate())
	require.NoError(t, Identity{CreditAs: "Alex <alex@example.com>"}.Validate())
}

func TestUnit_Credit_FallsBackWhenGitIsMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	require.NoError(t, os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\n\tname = Plain Person\n\temail = plain@example.com\n"), 0o644))
	original := gitConfigGet
	gitConfigGet = func(context.Context, string, ...string) (string, error) { return "", exec.ErrNotFound }
	t.Cleanup(func() { gitConfigGet = original })

	me, err := DefaultPerson(context.Background())
	require.NoError(t, err)
	require.Equal(t, "Plain Person <plain@example.com>", me.String())
	require.Equal(t, PersonFromGitConfig, me.Source)
}

type stuckSigner struct{ ssh.Signer }

func (stuckSigner) Sign(io.Reader, []byte) (*ssh.Signature, error) {
	time.Sleep(time.Hour)
	return nil, errors.New("unreachable")
}

func TestUnit_TimedSigner_GivesUpOnAnAgentThatNeverAnswers(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	real, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)
	signer := &timedSigner{inner: NewCommitSigner(stuckSigner{real}), timeout: 50 * time.Millisecond}
	_, err = signer.Sign(bytes.NewReader([]byte("commit")))
	require.ErrorContains(t, err, "did not finish")
}

func TestUnit_Discover_FlagsSecurityKeysAndFindsKeysWithoutAPublicFile(t *testing.T) {
	dir := t.TempDir()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	block, err := ssh.MarshalPrivateKey(priv, "")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "deploy_key"), pem.EncodeToMemory(block), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config"), []byte("Host *\n"), 0o600))

	keys := KeySource{SSHDir: dir}.Discover()
	require.Len(t, keys, 1)
	require.Equal(t, filepath.Join(dir, "deploy_key"), keys[0].Ref)
	require.True(t, keys[0].Usable)

	require.Equal(t, "security_key", typeProblem("sk-ssh-ed25519@openssh.com"))
	require.Equal(t, "unsupported_type", typeProblem(ssh.KeyAlgoDSA))
	require.Empty(t, typeProblem(ssh.KeyAlgoED25519))
}

func TestUnit_HostKeys_ResolveAnSSHConfigAlias(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config"), []byte(strings.Join([]string{
		"Host github-work",
		"  HostName github.com",
		"  User git",
		"Host internal",
		"  HostName git.internal.example",
		"  Port 2222",
	}, "\n")+"\n"), 0o600))
	src := KeySource{SSHDir: dir}

	_, algos, err := src.HostKeyCallback("github-work")
	require.NoError(t, err, "an alias for github.com gets github.com's published keys")
	require.NotEmpty(t, algos)

	host, port := src.resolveHost("internal")
	require.Equal(t, "git.internal.example", host)
	require.Equal(t, "2222", port)
	_, _, err = src.HostKeyCallback("internal")
	require.ErrorContains(t, err, "known_hosts")
}
