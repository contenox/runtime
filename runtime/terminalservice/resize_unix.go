//go:build !windows

package terminalservice

import (
	"log/slog"
	"os"

	"golang.org/x/sys/unix"
)

func (s *service) resizeLocalPTY(id string, cols, rows int) {
	sess := s.localByID(id)
	if sess == nil || sess.tty == nil {
		return
	}
	if err := setPTYSize(sess.tty, cols, rows); err != nil {
		slog.Debug("terminal pty resize", "session", id, "error", err)
	}
}

func setPTYSize(tty *os.File, cols, rows int) error {
	raw, err := tty.SyscallConn()
	if err != nil {
		return err
	}
	var resizeErr error
	// Avoid File.Fd: it would restore blocking reads and disable cancellation.
	if err := raw.Control(func(fd uintptr) {
		resizeErr = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, &unix.Winsize{
			Row: uint16(rows), Col: uint16(cols),
		})
	}); err != nil {
		return err
	}
	return resizeErr
}
