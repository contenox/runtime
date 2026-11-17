package owner_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/modeld/owner"
	"github.com/contenox/runtime/liblease"
)

func TestUnit_Owner_ContendedRenewalFencesBeforeLeaseExpiry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path := filepath.Join(t.TempDir(), "owner.lease")
	o, err := owner.Join(ctx, owner.Config{LeasePath: path, TTL: 2 * time.Second, RenewInterval: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer o.Release()
	lock := path + ".acquire.lock"
	if err := os.Mkdir(lock, 0o700); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(lock)
	select {
	case <-o.Lost():
		if !errors.Is(o.LostErr(), liblease.ErrAcquireTimeout) {
			t.Fatalf("expected contention failure, got %v", o.LostErr())
		}
		if !time.Now().Before(o.Holder().ExpiresAt()) {
			t.Fatal("owner continued serving past lease expiry")
		}
	case <-time.After(time.Until(o.Holder().ExpiresAt())):
		t.Fatal("owner did not fence while renewal waited for the acquisition lock")
	}
}
