//go:build linux

package contenoxcli

import (
	"os"

	"golang.org/x/sys/unix"
)

func flushTerminalInput(f *os.File) {
	_ = unix.IoctlSetInt(int(f.Fd()), unix.TCFLSH, unix.TCIFLUSH)
}
