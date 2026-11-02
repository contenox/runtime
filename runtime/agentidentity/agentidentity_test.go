package agentidentity_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/agentidentity"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func ed25519Signer(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)
	return signer
}

func TestUnit_CommitSigner_ProducesAVerifiableSSHSignature(t *testing.T) {
	signer := ed25519Signer(t)
	message := []byte("tree 0000\nauthor Agent <agent@example.com> 0 +0000\n\nfix: the thing\n")

	armored, err := agentidentity.NewCommitSigner(signer).Sign(bytes.NewReader(message))
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(armored, []byte("-----BEGIN SSH SIGNATURE-----\n")))
	require.True(t, bytes.HasSuffix(armored, []byte("-----END SSH SIGNATURE-----\n")))

	pub, err := agentidentity.VerifySignature(armored, message)
	require.NoError(t, err)
	require.Equal(t, ssh.FingerprintSHA256(signer.PublicKey()), ssh.FingerprintSHA256(pub))

	_, err = agentidentity.VerifySignature(armored, append([]byte("x"), message...))
	require.Error(t, err, "a changed commit no longer verifies")
}

func TestUnit_CommitSigner_SignsRSAKeysWithSHA512(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)
	message := []byte("commit body\n")

	armored, err := agentidentity.NewCommitSigner(signer).Sign(bytes.NewReader(message))
	require.NoError(t, err)
	_, err = agentidentity.VerifySignature(armored, message)
	require.NoError(t, err, "git rejects ssh-rsa (SHA-1) signatures, so RSA keys must sign with rsa-sha2-512")
}

func TestUnit_PublishedGitHubHostKeys_MatchTheirPublishedFingerprints(t *testing.T) {
	src := agentidentity.KeySource{SSHDir: t.TempDir()}
	callback, algos, err := src.HostKeyCallback("github.com")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256}, algos)

	published := map[string]string{
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl":                                                                                 "SHA256:+DiY3wvvV6TuJJhbpZisF/zLDA0zPMSvHdkr4UvCOqU",
		"ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBEmKSENjQEezOmxkZMy7opKgwFB9nkt5YRrYMjNuG5N87uRgg6CLrbo5wAdT/y6v0mKV0U2w0WZ2YB/++Tpockg=": "SHA256:p2QAMXNIC1TJYWeIOttrVc98/R1BUFWu3/LiyKgUfQM",
	}
	for line, fingerprint := range published {
		pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
		require.NoError(t, err)
		require.Equal(t, fingerprint, ssh.FingerprintSHA256(pub))
		require.NoError(t, callback("github.com:22", nil, pub))
	}
	require.Error(t, callback("github.com:22", nil, ed25519Signer(t).PublicKey()), "any other key is refused")

	_, _, err = src.HostKeyCallback("git.example.com")
	require.Error(t, err, "an unknown host with no known_hosts entry is refused, not trusted on first use")
}

func TestUnit_KeySource_DiscoversGeneratesAndFlagsProtectedKeys(t *testing.T) {
	dir := t.TempDir()
	src := agentidentity.KeySource{SSHDir: dir}

	generated, err := src.Generate("agent@example.com")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, agentidentity.GeneratedKeyName), generated.Ref)
	require.Equal(t, ssh.KeyAlgoED25519, generated.Type)
	require.Contains(t, generated.PublicKey, "agent@example.com")
	_, err = src.Generate("again")
	require.Error(t, err, "an existing agent key is never overwritten")

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "mine", []byte("secret"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "id_protected"), pem.EncodeToMemory(block), 0o600))
	sshPub, err := ssh.NewPublicKey(priv.Public())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "id_protected.pub"), ssh.MarshalAuthorizedKey(sshPub), 0o644))

	byRef := map[string]agentidentity.Key{}
	for _, key := range src.Discover() {
		byRef[key.Ref] = key
	}
	require.True(t, byRef[generated.Ref].Usable)
	protected := byRef[filepath.Join(dir, "id_protected")]
	require.False(t, protected.Usable)
	require.Equal(t, "passphrase_protected", protected.Problem)

	signer, closeSigner, err := src.Signer(generated.Ref)
	require.NoError(t, err)
	closeSigner()
	require.Equal(t, generated.Fingerprint, ssh.FingerprintSHA256(signer.PublicKey()))

	_, _, err = src.Signer(filepath.Join(dir, "id_protected"))
	require.ErrorContains(t, err, "ssh-add", "the error says how to make a protected key usable")

	check := src.CheckSigning(generated.Ref)
	require.True(t, check.OK, check.Message)
	require.Equal(t, generated.Fingerprint, check.Fingerprint)
}

func TestUnit_Identity_ValidateAndRoundTrip(t *testing.T) {
	for _, bad := range []agentidentity.Identity{
		{Name: "Agent"},
		{Email: "agent@example.com"},
		{Name: "Agent", Email: "not-an-email"},
		{Name: "Agent <x>", Email: "agent@example.com"},
		{Name: "Agent", Email: "agent@example.com", Sign: true},
	} {
		require.Errorf(t, bad.Validate(), "%+v", bad)
	}
	require.NoError(t, agentidentity.Identity{}.Validate(), "no identity at all is valid: commits keep the repository's own identity")

	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "identity.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	store := runtimetypes.New(db.WithoutTransaction())

	fresh := agentidentity.Load(ctx, store)
	require.False(t, fresh.Active())
	require.True(t, fresh.CreditMe, "crediting the user is on until they turn it off")
	want := agentidentity.Identity{Name: " Contenox Agent ", Email: "agent@example.com", Key: "/k", Sign: true, CreditMe: true}
	require.NoError(t, agentidentity.Save(ctx, store, want))
	got := agentidentity.Load(ctx, store)
	require.Equal(t, want.Normalized(), got)
	require.True(t, got.Active())
}
