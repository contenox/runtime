package agentidentity

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/kevinburke/ssh_config"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

var publishedHostKeys = map[string][]string{
	"github.com": {
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl",
		"ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBEmKSENjQEezOmxkZMy7opKgwFB9nkt5YRrYMjNuG5N87uRgg6CLrbo5wAdT/y6v0mKV0U2w0WZ2YB/++Tpockg=",
	},
}

func (src KeySource) sshConfigValue(alias, key string) string {
	data, err := os.ReadFile(filepath.Join(src.SSHDir, "config"))
	if err != nil {
		return ""
	}
	cfg, err := ssh_config.Decode(bytes.NewReader(data))
	if err != nil {
		return ""
	}
	value, err := cfg.Get(alias, key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func (src KeySource) resolveHost(alias string) (host string, port string) {
	host = strings.ToLower(strings.TrimSpace(alias))
	if real := src.sshConfigValue(alias, "HostName"); real != "" {
		host = strings.ToLower(real)
	}
	port = "22"
	if p := src.sshConfigValue(alias, "Port"); p != "" {
		port = p
	}
	return host, port
}

func (src KeySource) HostKeyCallback(alias string) (ssh.HostKeyCallback, []string, error) {
	host, _ := src.resolveHost(alias)
	var published []ssh.PublicKey
	var algos []string
	for _, line := range publishedHostKeys[host] {
		pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			return nil, nil, err
		}
		published = append(published, pub)
		algos = append(algos, pub.Type())
	}
	var fromFile ssh.HostKeyCallback
	knownHostsFile := filepath.Join(src.SSHDir, "known_hosts")
	if _, err := os.Stat(knownHostsFile); err == nil {
		if cb, err := knownhosts.New(knownHostsFile); err == nil {
			fromFile = cb
		}
	}
	if len(published) == 0 && fromFile == nil {
		return nil, nil, fmt.Errorf("%s is not in %s; connect once with ssh to accept its host key", host, knownHostsFile)
	}
	callback := func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		for _, pub := range published {
			if bytes.Equal(pub.Marshal(), key.Marshal()) {
				return nil
			}
		}
		if fromFile != nil {
			return fromFile(hostname, remote, key)
		}
		return fmt.Errorf("%s presented a host key that does not match its published keys", host)
	}
	return callback, algos, nil
}

var greetings = []*regexp.Regexp{
	regexp.MustCompile(`Hi ([^!]+)! You've successfully authenticated`),
	regexp.MustCompile(`Welcome to GitLab, @([^!]+)!`),
	regexp.MustCompile(`logged in as ([^\s.]+)`),
}

const (
	HostReasonKeyUnusable = "key_unusable"
	HostReasonUnknownHost = "unknown_host"
	HostReasonUnreachable = "unreachable"
	HostReasonRejected    = "key_rejected"
	HostReasonFailed      = "failed"
)

type HostCheck struct {
	Host    string `json:"host"`
	OK      bool   `json:"ok"`
	Account string `json:"account,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message"`
}

func (src KeySource) CheckHost(ctx context.Context, ref, host string) HostCheck {
	result := HostCheck{Host: host}
	signer, closeSigner, err := src.Signer(ref)
	if err != nil {
		result.Reason, result.Message = HostReasonKeyUnusable, err.Error()
		return result
	}
	defer closeSigner()
	callback, algos, err := src.HostKeyCallback(host)
	if err != nil {
		result.Reason, result.Message = HostReasonUnknownHost, err.Error()
		return result
	}
	dialHost, dialPort := src.resolveHost(host)
	user := "git"
	if configured := src.sshConfigValue(host, "User"); configured != "" {
		user = configured
	}
	cfg := &ssh.ClientConfig{
		User:              user,
		Auth:              []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback:   callback,
		HostKeyAlgorithms: algos,
		Timeout:           15 * time.Second,
	}
	dialer := net.Dialer{Timeout: 15 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(dialHost, dialPort))
	if err != nil {
		result.Reason, result.Message = HostReasonUnreachable, fmt.Sprintf("cannot reach %s: %v", host, err)
		return result
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, net.JoinHostPort(dialHost, dialPort), cfg)
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "unable to authenticate"):
			result.Reason, result.Message = HostReasonRejected, fmt.Sprintf("%s does not accept this key; add the public key to the agent's account", host)
		case strings.Contains(err.Error(), "host key"):
			result.Reason, result.Message = HostReasonUnknownHost, err.Error()
		default:
			result.Reason, result.Message = HostReasonFailed, err.Error()
		}
		return result
	}
	client := ssh.NewClient(sshConn, chans, reqs)
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		result.Reason, result.Message = HostReasonFailed, err.Error()
		return result
	}
	defer session.Close()
	var out bytes.Buffer
	session.Stdout = &out
	session.Stderr = &out
	_ = session.Shell()
	_ = session.Wait()
	text := strings.TrimSpace(out.String())
	for _, re := range greetings {
		if m := re.FindStringSubmatch(text); m != nil {
			result.OK = true
			result.Account = strings.TrimSpace(m[1])
			result.Message = text
			return result
		}
	}
	result.OK = true
	result.Message = text
	return result
}

type SigningCheck struct {
	OK          bool   `json:"ok"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Message     string `json:"message"`
}

func (src KeySource) CheckSigning(ref string) SigningCheck {
	signer, closeSigner, err := src.Signer(ref)
	if err != nil {
		return SigningCheck{Message: err.Error()}
	}
	defer closeSigner()
	payload := []byte("contenox-runtime agent identity check\n")
	armored, err := NewCommitSigner(signer).Sign(bytes.NewReader(payload))
	if err != nil {
		return SigningCheck{Message: err.Error()}
	}
	pub, err := VerifySignature(armored, payload)
	if err != nil {
		return SigningCheck{Message: err.Error()}
	}
	return SigningCheck{OK: true, Fingerprint: ssh.FingerprintSHA256(pub), Message: "signed and verified a test commit"}
}

var errNoUserIdentity = errors.New("no user.name and user.email in your global git config")

func UserGitIdentity() (object.Signature, error) {
	cfg, err := loadGlobalGitConfig()
	if err != nil {
		return object.Signature{}, err
	}
	if strings.TrimSpace(cfg.User.Name) == "" || strings.TrimSpace(cfg.User.Email) == "" {
		return object.Signature{}, errNoUserIdentity
	}
	return object.Signature{Name: strings.TrimSpace(cfg.User.Name), Email: strings.TrimSpace(cfg.User.Email)}, nil
}
