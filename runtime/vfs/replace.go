package vfs

import (
	"errors"
	"os"
	"runtime"
	"syscall"
	"time"
)

const replaceRetryWindow = 2 * time.Second

const (
	windowsAccessDenied     syscall.Errno = 5
	windowsSharingViolation syscall.Errno = 32
	windowsLockViolation    syscall.Errno = 33
)

func ReplaceFile(src, dst string) error {
	if runtime.GOOS != "windows" {
		return os.Rename(src, dst)
	}
	deadline := time.Now().Add(replaceRetryWindow)
	delay := time.Millisecond
	for {
		err := os.Rename(src, dst)
		if err == nil || !heldByAnotherHandle(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(delay)
		if delay < 100*time.Millisecond {
			delay *= 2
		}
	}
}

func heldByAnotherHandle(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch errno {
	case windowsAccessDenied, windowsSharingViolation, windowsLockViolation:
		return true
	}
	return false
}
