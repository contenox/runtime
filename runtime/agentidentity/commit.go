package agentidentity

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/contenox/runtime/runtime/localtools"
	"github.com/contenox/runtime/runtime/runtimetypes"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"golang.org/x/crypto/ssh"
)

const signTimeout = 30 * time.Second

func loadGlobalGitConfig() (*gitconfig.Config, error) {
	return gitconfig.LoadConfig(gitconfig.GlobalScope)
}

func CoAuthorTrailer(person Person) string {
	return "Co-authored-by: " + person.String()
}

func (src KeySource) Resolver(store runtimetypes.Store) localtools.GitIdentityResolver {
	return func(ctx context.Context, repoDir string) (*localtools.GitIdentity, error) {
		return src.Resolve(ctx, Load(ctx, store), repoDir)
	}
}

func (src KeySource) Resolve(ctx context.Context, identity Identity, repoDir string) (*localtools.GitIdentity, error) {
	identity = identity.Normalized()
	out := &localtools.GitIdentity{}
	if identity.Active() {
		out.Name, out.Email = identity.Name, identity.Email
		if person, ok := identity.Credit(ctx, repoDir); ok {
			out.Trailer = CoAuthorTrailer(person)
		}
		if identity.Sign {
			signer, closeSigner, err := src.Signer(identity.Key)
			if err != nil {
				return nil, fmt.Errorf("the agent's signing key cannot be used (%v); fix it under Settings, Agent identity", err)
			}
			out.Signer = &timedSigner{inner: NewCommitSigner(signer), timeout: signTimeout}
			out.Close = closeSigner
		}
	}
	if identity.Key != "" {
		key := identity.Key
		out.PushAuth = func(user, host string) (transport.AuthMethod, func(), error) {
			signer, closeSigner, err := src.Signer(key)
			if err != nil {
				return nil, func() {}, fmt.Errorf("the agent's SSH key cannot be used (%v); fix it under Settings, Agent identity", err)
			}
			callback, algos, err := src.HostKeyCallback(host)
			if err != nil {
				closeSigner()
				return nil, func() {}, err
			}
			if configured := src.sshConfigValue(host, "User"); configured != "" && user == "git" {
				user = configured
			}
			return &pushAuth{user: user, signer: signer, callback: callback, algos: algos}, closeSigner, nil
		}
	}
	return out, nil
}

type timedSigner struct {
	inner   *CommitSigner
	timeout time.Duration
}

func (s *timedSigner) Sign(message io.Reader) ([]byte, error) {
	payload, err := io.ReadAll(message)
	if err != nil {
		return nil, err
	}
	type result struct {
		sig []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		sig, err := s.inner.Sign(strings.NewReader(string(payload)))
		done <- result{sig, err}
	}()
	select {
	case r := <-done:
		return r.sig, r.err
	case <-time.After(s.timeout):
		return nil, fmt.Errorf("signing did not finish within %s; if your SSH agent asks to confirm each use, approve it or choose a key that does not", s.timeout)
	}
}

type pushAuth struct {
	user     string
	signer   ssh.Signer
	callback ssh.HostKeyCallback
	algos    []string
}

func (a *pushAuth) Name() string { return "ssh-public-keys" }

func (a *pushAuth) String() string {
	return fmt.Sprintf("user: %s, key: %s", a.user, ssh.FingerprintSHA256(a.signer.PublicKey()))
}

func (a *pushAuth) ClientConfig() (*ssh.ClientConfig, error) {
	return &ssh.ClientConfig{
		User:              a.user,
		Auth:              []ssh.AuthMethod{ssh.PublicKeys(a.signer)},
		HostKeyCallback:   a.callback,
		HostKeyAlgorithms: a.algos,
		Timeout:           20 * time.Second,
	}, nil
}
