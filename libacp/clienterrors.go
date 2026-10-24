package libacp

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"syscall"
)

var (
	ErrAgentStartFailed = errors.New("libacp: agent start failed")

	ErrIdleTimeout = errors.New("libacp: agent idle timeout")

	ErrNoDisplayableOutput = errors.New("libacp: prompt turn produced no displayable output")
)

func IsStartupError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrAgentStartFailed) || errors.Is(err, exec.ErrNotFound)
}

func IsTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrIdleTimeout) {
		return true
	}
	var rpcErr *Error
	return errors.As(err, &rpcErr) && rpcErr.Code == ErrRequestTimeout
}

func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || IsStartupError(err) {
		return false
	}
	if IsTimeoutError(err) {
		return true
	}
	if errors.Is(err, ErrConnectionClosed) ||
		errors.Is(err, ErrNoDisplayableOutput) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET) {
		return true
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "connection closed") ||
		strings.Contains(msg, "file already closed")
}
