//go:build !windows

package modeldinstall

import "os/exec"

func hideConsole(*exec.Cmd) {}
