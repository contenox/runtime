//go:build !windows

package agentidentity

import (
	"errors"
	"net"
	"os"

	"golang.org/x/crypto/ssh/agent"
)

func dialSSHAgent() (agent.ExtendedAgent, func(), error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, nil, errors.New("SSH_AUTH_SOCK is not set")
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, nil, err
	}
	return agent.NewClient(conn), func() { _ = conn.Close() }, nil
}
