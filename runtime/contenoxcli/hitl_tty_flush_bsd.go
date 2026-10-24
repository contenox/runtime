//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package contenoxcli

import (
	"os"

	"golang.org/x/sys/unix"
)

const fread = 0x0001

func flushTerminalInput(f *os.File) {
	_ = unix.IoctlSetPointerInt(int(f.Fd()), unix.TIOCFLUSH, fread)
}
