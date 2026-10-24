//go:build windows

package contenoxcli

import (
	"os"

	"golang.org/x/sys/windows"
)

func openControllingTerminal() (*os.File, bool, error) {
	con, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		if isConsole(os.Stdin) {
			return os.Stdin, true, nil
		}
		return nil, false, err
	}
	if !isConsole(con) {
		con.Close()
		return nil, false, os.ErrInvalid
	}
	return con, true, nil
}

func isConsole(f *os.File) bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(f.Fd()), &mode) == nil
}

func flushInput(f *os.File) {
	_ = windows.FlushConsoleInputBuffer(windows.Handle(f.Fd()))
}
