package liblease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/google/uuid"
)

var ErrHeld = errors.New("liblease: lease is held by another instance")

var ErrLost = errors.New("liblease: lease was taken over by another instance")

var ErrAcquireTimeout = errors.New("liblease: timed out waiting for the acquisition lock")

type Record struct {
	InstanceID string            `json:"instance_id"`
	PID        int               `json:"pid"`
	Host       string            `json:"host"`
	AcquiredAt time.Time         `json:"acquired_at"`
	RenewedAt  time.Time         `json:"renewed_at"`
	TTL        time.Duration     `json:"ttl"`
	Meta       map[string]string `json:"meta,omitempty"`
}

type Option func(*Record)

func WithMeta(meta map[string]string) Option {
	return func(r *Record) { r.Meta = meta }
}

func (r Record) ExpiresAt() time.Time { return r.RenewedAt.Add(r.TTL) }

func (r Record) expired(now time.Time) bool { return now.After(r.ExpiresAt()) }

type Lease struct {
	mu   sync.Mutex
	path string
	rec  Record
}

func Acquire(path string, ttl time.Duration, opts ...Option) (*Lease, error) {
	return AcquireContext(context.Background(), path, ttl, opts...)
}

func AcquireContext(ctx context.Context, path string, ttl time.Duration, opts ...Option) (*Lease, error) {
	if ttl <= 0 {
		return nil, errors.New("liblease: ttl must be positive")
	}
	unlock, err := acquireLeaseLock(ctx, path, ttl)
	if err != nil {
		return nil, err
	}
	defer unlock()

	now := time.Now()
	host, _ := os.Hostname()
	rec := Record{
		InstanceID: uuid.NewString(),
		PID:        os.Getpid(),
		Host:       host,
		AcquiredAt: now,
		RenewedAt:  now,
		TTL:        ttl,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&rec)
		}
	}

	won, err := tryCreate(path, rec)
	if err != nil {
		return nil, err
	}
	if won {
		return &Lease{path: path, rec: rec}, nil
	}

	if cur, rerr := readRecord(path); rerr == nil && !cur.expired(now) {
		return nil, fmt.Errorf("%w: instance %s (pid %d) until %s",
			ErrHeld, cur.InstanceID, cur.PID, cur.ExpiresAt().Format(time.RFC3339))
	}

	if err := writeRecord(path, rec); err != nil {
		return nil, err
	}
	after, err := readRecord(path)
	if err != nil {
		return nil, err
	}
	if after.InstanceID != rec.InstanceID {
		return nil, fmt.Errorf("%w: lost takeover race to instance %s", ErrHeld, after.InstanceID)
	}
	return &Lease{path: path, rec: rec}, nil
}

const leaseLockStaleAfter = 5 * time.Second

const leaseLockMaxWait = 30 * time.Second

const leaseLockPollInterval = time.Millisecond

func acquireLeaseLock(ctx context.Context, path string, ttl time.Duration) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAcquireTimeout, err)
	}
	ctx, cancel := context.WithTimeout(ctx, leaseLockMaxWait)
	defer cancel()

	lockPath := path + ".acquire.lock"
	staleAfter := leaseLockStaleAfter
	if ttl > 0 && ttl < staleAfter {
		staleAfter = ttl
	}
	if staleAfter < 100*time.Millisecond {
		staleAfter = 100 * time.Millisecond
	}

	timer := time.NewTimer(leaseLockPollInterval)
	defer timer.Stop()

	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("%w: %q: %w", ErrAcquireTimeout, lockPath, err)
		}
		err := os.Mkdir(lockPath, 0o700)
		if err == nil {
			return func() { _ = os.Remove(lockPath) }, nil
		}
		info, statErr := leaseLockInfo(lockPath)
		contended := os.IsExist(err) || runtime.GOOS == "windows" && os.IsPermission(err) && statErr == nil && info.IsDir()
		if runtime.GOOS == "windows" && os.IsPermission(err) && errors.Is(statErr, os.ErrNotExist) {
			probe, probeErr := os.MkdirTemp(filepath.Dir(lockPath), ".lease-permission-*")
			if probeErr != nil {
				return nil, err
			}
			if removeErr := os.Remove(probe); removeErr != nil {
				return nil, removeErr
			}
			contended = true
		}
		if !contended {
			return nil, err
		}
		if statErr == nil && !info.IsDir() {
			return nil, fmt.Errorf("liblease: acquisition lock %q is not a directory", lockPath)
		}
		if statErr == nil && time.Since(info.ModTime()) > staleAfter {
			_ = os.Remove(lockPath)
			continue
		} else if statErr != nil && errors.Is(statErr, os.ErrNotExist) {
			continue
		} else if statErr != nil {
			return nil, statErr
		}

		timer.Reset(leaseLockPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("%w: %q: %w", ErrAcquireTimeout, lockPath, ctx.Err())
		case <-timer.C:
		}
	}
}

func leaseLockInfo(path string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if runtime.GOOS != "windows" || !os.IsPermission(err) {
		return info, err
	}
	entries, readErr := os.ReadDir(filepath.Dir(path))
	if readErr != nil {
		return nil, readErr
	}
	for _, entry := range entries {
		if entry.Name() == filepath.Base(path) {
			return entry.Info()
		}
	}
	return nil, os.ErrNotExist
}

func tryCreate(path string, rec Record) (bool, error) {
	tmpName, err := writeTemp(path, rec)
	if err != nil {
		return false, err
	}
	defer os.Remove(tmpName)
	if err := os.Link(tmpName, path); err != nil {
		if os.IsExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (l *Lease) Renew() error { return l.RenewContext(context.Background()) }

func (l *Lease) RenewContext(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	unlock, err := acquireLeaseLock(ctx, l.path, l.rec.TTL)
	if err != nil {
		return fmt.Errorf("liblease: renew lock: %w", err)
	}
	defer unlock()

	cur, err := readRecord(l.path)
	if err != nil {
		return fmt.Errorf("liblease: renew: %w", err)
	}
	if cur.InstanceID != l.rec.InstanceID {
		return fmt.Errorf("%w: now held by %s", ErrLost, cur.InstanceID)
	}
	if l.rec.expired(time.Now()) {
		return fmt.Errorf("%w: lease expired before renewal", ErrLost)
	}
	l.rec.RenewedAt = time.Now()
	return writeRecord(l.path, l.rec)
}

func (l *Lease) Release() error { return l.ReleaseContext(context.Background()) }

func (l *Lease) ReleaseContext(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	unlock, err := acquireLeaseLock(ctx, l.path, l.rec.TTL)
	if err != nil {
		return err
	}
	defer unlock()

	cur, err := readRecord(l.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if cur.InstanceID != l.rec.InstanceID {
		return nil
	}
	return os.Remove(l.path)
}

func (l *Lease) Expired() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rec.expired(time.Now())
}

func (l *Lease) InstanceID() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rec.InstanceID
}

func (l *Lease) Record() Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rec
}

func Inspect(path string) (Record, error) {
	return InspectContext(context.Background(), path)
}

func InspectContext(ctx context.Context, path string) (Record, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	return readRecord(path)
}

func readRecord(path string) (Record, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Record{}, err
	}
	var rec Record
	if err := json.Unmarshal(b, &rec); err != nil {
		return Record{}, fmt.Errorf("liblease: corrupt lease %q: %w", path, err)
	}
	return rec, nil
}

func writeRecord(path string, rec Record) error {
	tmpName, err := writeTemp(path, rec)
	if err != nil {
		return err
	}
	defer os.Remove(tmpName)
	return os.Rename(tmpName, path)
}

func writeTemp(path string, rec Record) (string, error) {
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".lease-*.tmp")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	return tmpName, nil
}
