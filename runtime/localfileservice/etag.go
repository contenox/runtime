package localfileservice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/contenox/runtime/runtime/vfs"
)

var (
	ErrConflict             = errors.New("file changed since it was read")
	ErrPreconditionRequired = errors.New("a save must name the version it replaces: send If-Match with the ETag you read, or * to replace whatever is there")
)

const MatchAny = "*"

type ConflictError struct {
	Path    string
	Current string
}

func (e *ConflictError) Error() string {
	if e.Current == "" {
		return fmt.Sprintf("%s: %s no longer exists", ErrConflict, e.Path)
	}
	return fmt.Sprintf("%s: %s is now at version %s", ErrConflict, e.Path, e.Current)
}

func (e *ConflictError) Unwrap() error { return ErrConflict }

var ETag = vfs.ContentHash

func NormalizeETag(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "W/")
	return strings.Trim(raw, `"`)
}

type pathLock struct {
	mu   sync.Mutex
	refs int
}

var (
	pathLocksMu sync.Mutex
	pathLocks   = map[string]*pathLock{}
)

func lockPath(abs string) func() {
	pathLocksMu.Lock()
	l, ok := pathLocks[abs]
	if !ok {
		l = &pathLock{}
		pathLocks[abs] = l
	}
	l.refs++
	pathLocksMu.Unlock()
	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		pathLocksMu.Lock()
		l.refs--
		if l.refs == 0 {
			delete(pathLocks, abs)
		}
		pathLocksMu.Unlock()
	}
}

func heldPathLocks() int {
	pathLocksMu.Lock()
	defer pathLocksMu.Unlock()
	return len(pathLocks)
}

func currentETag(abs string) (string, error) {
	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", mapOSError(err)
	}
	return ETag(data), nil
}

func (s *localService) WriteIfMatch(ctx context.Context, relPath string, data []byte, ifMatch string) (*Entry, error) {
	_ = ctx
	if len(data) > MaxWriteSize {
		return nil, fmt.Errorf("%w: file exceeds %d byte limit", ErrInvalidPath, MaxWriteSize)
	}
	want := NormalizeETag(ifMatch)
	if want == "" {
		return nil, ErrPreconditionRequired
	}
	abs, rel, err := s.resolveForWrite(relPath)
	if err != nil {
		return nil, err
	}
	unlock := lockPath(abs)
	defer unlock()
	current, err := currentETag(abs)
	if err != nil {
		return nil, err
	}
	if current == "" || (want != MatchAny && current != want) {
		return nil, &ConflictError{Path: rel, Current: current}
	}
	if info, err := os.Stat(abs); err == nil && info.IsDir() {
		return nil, fmt.Errorf("%w: %s is a directory", ErrInvalidPath, rel)
	}
	if err := vfs.WriteFileAtomic(abs, data); err != nil {
		return nil, mapOSError(err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, mapOSError(err)
	}
	entry := entryFromInfo(rel, info)
	entry.ETag = ETag(data)
	return &entry, nil
}
