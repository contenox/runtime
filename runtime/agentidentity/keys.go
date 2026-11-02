package agentidentity

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

const agentRefPrefix = "agent:"

var errSecurityKey = errors.New("hardware security keys need a touch for every signature, which an unattended agent cannot give; choose a regular key or generate one for the agent")

type Key struct {
	Ref         string `json:"ref"`
	Label       string `json:"label"`
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
	PublicKey   string `json:"publicKey"`
	Source      string `json:"source"`
	Usable      bool   `json:"usable"`
	Problem     string `json:"problem,omitempty"`
}

type KeySource struct {
	SSHDir string
	Agent  func() (agent.ExtendedAgent, func(), error)
}

func DefaultKeySource() KeySource {
	home, _ := os.UserHomeDir()
	return KeySource{SSHDir: filepath.Join(home, ".ssh"), Agent: dialSSHAgent}
}

func (src KeySource) agentSigners() ([]ssh.Signer, func()) {
	if src.Agent == nil {
		return nil, func() {}
	}
	client, closeFn, err := src.Agent()
	if err != nil {
		return nil, func() {}
	}
	signers, err := client.Signers()
	if err != nil {
		closeFn()
		return nil, func() {}
	}
	return signers, closeFn
}

func (src KeySource) Discover() []Key {
	signers, closeAgent := src.agentSigners()
	defer closeAgent()
	inAgent := map[string]ssh.Signer{}
	for _, s := range signers {
		inAgent[ssh.FingerprintSHA256(s.PublicKey())] = s
	}

	var keys []Key
	seen := map[string]bool{}
	seenFile := map[string]bool{}
	entries, _ := os.ReadDir(src.SSHDir)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".pub") {
			continue
		}
		private := filepath.Join(src.SSHDir, strings.TrimSuffix(entry.Name(), ".pub"))
		if _, err := os.Stat(private); err != nil {
			continue
		}
		pub, comment, err := readPublicKey(filepath.Join(src.SSHDir, entry.Name()))
		if err != nil {
			continue
		}
		fp := ssh.FingerprintSHA256(pub)
		key := Key{
			Ref:         private,
			Label:       labelFor(filepath.Base(private), comment),
			Type:        pub.Type(),
			Fingerprint: fp,
			PublicKey:   authorizedLine(pub, comment),
			Source:      "file",
			Usable:      true,
		}
		if _, err := parsePrivateKeyFile(private); err != nil {
			var missing *ssh.PassphraseMissingError
			switch {
			case errors.As(err, &missing) && inAgent[fp] != nil:
			case errors.As(err, &missing):
				key.Usable = false
				key.Problem = "passphrase_protected"
			default:
				key.Usable = false
				key.Problem = "unreadable"
			}
		}
		if problem := typeProblem(pub.Type()); problem != "" {
			key.Usable = false
			key.Problem = problem
		}
		seen[fp] = true
		seenFile[private] = true
		keys = append(keys, key)
	}
	for _, entry := range entries {
		private := filepath.Join(src.SSHDir, entry.Name())
		if entry.IsDir() || seenFile[private] || strings.Contains(entry.Name(), ".") || !looksLikePrivateKey(private) {
			continue
		}
		signer, err := parsePrivateKeyFile(private)
		if err != nil {
			continue
		}
		pub := signer.PublicKey()
		fp := ssh.FingerprintSHA256(pub)
		if seen[fp] {
			continue
		}
		key := Key{
			Ref:         private,
			Label:       entry.Name(),
			Type:        pub.Type(),
			Fingerprint: fp,
			PublicKey:   authorizedLine(pub, ""),
			Source:      "file",
			Usable:      true,
		}
		if problem := typeProblem(pub.Type()); problem != "" {
			key.Usable = false
			key.Problem = problem
		}
		seen[fp] = true
		keys = append(keys, key)
	}
	for fp, s := range inAgent {
		if seen[fp] {
			continue
		}
		comment := ""
		if ak, ok := s.PublicKey().(*agent.Key); ok {
			comment = ak.Comment
		}
		key := Key{
			Ref:         agentRefPrefix + fp,
			Label:       labelFor("ssh-agent", comment),
			Type:        s.PublicKey().Type(),
			Fingerprint: fp,
			PublicKey:   authorizedLine(s.PublicKey(), comment),
			Source:      "agent",
			Usable:      true,
		}
		if problem := typeProblem(key.Type); problem != "" {
			key.Usable = false
			key.Problem = problem
		}
		keys = append(keys, key)
	}
	sort.SliceStable(keys, func(i, j int) bool { return keys[i].Label < keys[j].Label })
	return keys
}

func (src KeySource) Signer(ref string) (ssh.Signer, func(), error) {
	ref = strings.TrimSpace(ref)
	noop := func() {}
	if ref == "" {
		return nil, noop, errors.New("no SSH key is set for the agent")
	}
	if fp, ok := strings.CutPrefix(ref, agentRefPrefix); ok {
		signer, closeFn, err := src.agentSigner(fp, ref)
		if err == nil && typeProblem(signer.PublicKey().Type()) == "security_key" {
			closeFn()
			return nil, noop, errSecurityKey
		}
		return signer, closeFn, err
	}
	signer, err := parsePrivateKeyFile(ref)
	if err == nil {
		if typeProblem(signer.PublicKey().Type()) == "security_key" {
			return nil, noop, errSecurityKey
		}
		return signer, noop, nil
	}
	var missing *ssh.PassphraseMissingError
	if !errors.As(err, &missing) {
		return nil, noop, fmt.Errorf("read SSH key %s: %w", ref, err)
	}
	pub, _, pubErr := readPublicKey(ref + ".pub")
	if pubErr != nil {
		return nil, noop, fmt.Errorf("SSH key %s is passphrase-protected and has no .pub beside it to find it in your SSH agent", ref)
	}
	signer, closeFn, agentErr := src.agentSigner(ssh.FingerprintSHA256(pub), ref)
	if agentErr != nil {
		return nil, noop, fmt.Errorf("SSH key %s is passphrase-protected and not loaded in your SSH agent; run ssh-add %s", ref, ref)
	}
	return signer, closeFn, nil
}

func (src KeySource) agentSigner(fp, ref string) (ssh.Signer, func(), error) {
	signers, closeAgent := src.agentSigners()
	for _, s := range signers {
		if ssh.FingerprintSHA256(s.PublicKey()) == fp {
			return s, closeAgent, nil
		}
	}
	closeAgent()
	return nil, func() {}, fmt.Errorf("SSH key %s is not loaded in your SSH agent", ref)
}

func typeProblem(keyType string) string {
	switch {
	case keyType == ssh.KeyAlgoDSA:
		return "unsupported_type"
	case strings.HasPrefix(keyType, "sk-"):
		return "security_key"
	}
	return ""
}

func looksLikePrivateKey(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 64)
	n, _ := f.Read(head)
	return strings.HasPrefix(string(head[:n]), "-----BEGIN ") && strings.Contains(string(head[:n]), "PRIVATE KEY")
}

func parsePrivateKeyFile(path string) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(data)
}

func readPublicKey(path string) (ssh.PublicKey, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	pub, comment, _, _, err := ssh.ParseAuthorizedKey(data)
	return pub, comment, err
}

func authorizedLine(pub ssh.PublicKey, comment string) string {
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	if comment = strings.TrimSpace(comment); comment != "" {
		line += " " + comment
	}
	return line
}

func labelFor(name, comment string) string {
	if comment = strings.TrimSpace(comment); comment != "" {
		return name + " (" + comment + ")"
	}
	return name
}
