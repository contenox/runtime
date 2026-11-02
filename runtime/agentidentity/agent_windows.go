//go:build windows

package agentidentity

import (
	"os"

	"golang.org/x/crypto/ssh/agent"
)

const windowsAgentPipe = `\\.\pipe\openssh-ssh-agent`

func dialSSHAgent() (agent.ExtendedAgent, func(), error) {
	pipe, err := os.OpenFile(windowsAgentPipe, os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}
	return agent.NewClient(pipe), func() { _ = pipe.Close() }, nil
}
