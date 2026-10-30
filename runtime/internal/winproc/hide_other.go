//go:build !windows

package winproc

import "os/exec"

// Hide is a no-op where child processes cannot open a console window.
func Hide(*exec.Cmd) {}
