package terminalservice

import "errors"

var (
	ErrDisabled        = errors.New("terminal: disabled")
	ErrNotImplemented  = errors.New("terminal: not implemented on this platform")
	ErrSessionNotFound = errors.New("terminal: session not found")
	ErrTooManySessions = errors.New("terminal: too many concurrent sessions")
)
