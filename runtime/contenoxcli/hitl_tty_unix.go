//go:build !windows

package contenoxcli

import (
	"os"

	"golang.org/x/term"
)

func openControllingTerminal() (*os.File, bool, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		if term.IsTerminal(int(os.Stdin.Fd())) {
			return os.Stdin, true, nil
		}
		return nil, false, err
	}
	if !term.IsTerminal(int(tty.Fd())) {
		tty.Close()
		return nil, false, os.ErrInvalid
	}
	return tty, true, nil
}

func flushInput(f *os.File) {
	flushTerminalInput(f)
}
