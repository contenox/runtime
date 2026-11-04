package externalagents

import (
	"os/exec"
	"runtime"

	"github.com/contenox/runtime/runtime/internal/winproc"
)

func hidden(cmd *exec.Cmd) *exec.Cmd {
	winproc.Hide(cmd)
	return cmd
}

func goarch() string {
	return runtime.GOARCH
}
