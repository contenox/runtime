package liblease_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/contenox/runtime/liblease"
)

func TestUnit_Lease_AcquisitionLockFileIsNotRemoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner.lease")
	lock := path + ".acquire.lock"
	if err := os.WriteFile(lock, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	_, err := liblease.Acquire(path, time.Minute)
	if err == nil || errors.Is(err, liblease.ErrAcquireTimeout) {
		t.Fatalf("expected immediate lock-path error, got %v", err)
	}
	data, err := os.ReadFile(lock)
	if err != nil || string(data) != "keep" {
		t.Fatalf("lock-path file changed: %q, %v", data, err)
	}
}

func TestUnit_Lease_StaleHolderCannotReleaseSuccessor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner.lease")
	stale, err := liblease.Acquire(path, time.Nanosecond)
	if err != nil {
		t.Fatal(err)
	}
	current, err := liblease.Acquire(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Release()
	if err := stale.RenewContext(context.Background()); !errors.Is(err, liblease.ErrLost) {
		t.Fatalf("stale renewal: %v", err)
	}
	if err := stale.Release(); err != nil {
		t.Fatal(err)
	}
	record, err := liblease.Inspect(path)
	if err != nil || record.InstanceID != current.InstanceID() {
		t.Fatalf("successor was changed by stale release: %v, %v", record, err)
	}
}
