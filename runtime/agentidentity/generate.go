package agentidentity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

const GeneratedKeyName = "contenox_agent_ed25519"

func (src KeySource) Generate(comment string) (Key, error) {
	if err := os.MkdirAll(src.SSHDir, 0o700); err != nil {
		return Key{}, fmt.Errorf("create %s: %w", src.SSHDir, err)
	}
	private := filepath.Join(src.SSHDir, GeneratedKeyName)
	if _, err := os.Stat(private); err == nil {
		return Key{}, fmt.Errorf("%s already exists; choose it from the list instead of generating a new one", private)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Key{}, err
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return Key{}, err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return Key{}, err
	}
	if err := os.WriteFile(private, pem.EncodeToMemory(block), 0o600); err != nil {
		return Key{}, fmt.Errorf("write %s: %w", private, err)
	}
	line := authorizedLine(sshPub, comment)
	if err := os.WriteFile(private+".pub", []byte(line+"\n"), 0o644); err != nil {
		_ = os.Remove(private)
		return Key{}, fmt.Errorf("write %s.pub: %w", private, err)
	}
	return Key{
		Ref:         private,
		Label:       labelFor(GeneratedKeyName, comment),
		Type:        sshPub.Type(),
		Fingerprint: ssh.FingerprintSHA256(sshPub),
		PublicKey:   line,
		Source:      "file",
		Usable:      true,
	}, nil
}
