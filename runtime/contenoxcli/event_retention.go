package contenoxcli

import (
	"context"
	"errors"
	"fmt"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/workspaceevents"
)

func seedEventRetention(ctx context.Context, store runtimetypes.Store) (bool, error) {
	var current string
	err := store.GetKV(ctx, clikv.Prefix+clikv.EventRetentionDaysKey, &current)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, libdb.ErrNotFound) {
		return false, fmt.Errorf("read %s: %w", clikv.EventRetentionDaysKey, err)
	}
	if err := clikv.SetString(ctx, store, clikv.EventRetentionDaysKey, clikv.ShippedEventRetentionDays); err != nil {
		return false, fmt.Errorf("set %s: %w", clikv.EventRetentionDaysKey, err)
	}
	return true, nil
}

func applyShippedEventRetention(ctx context.Context, store runtimetypes.Store, tracker libtracker.ActivityTracker) {
	reportErr, reportChange, end := tracker.Start(ctx, "seed", "event_retention")
	defer end()
	seeded, err := seedEventRetention(ctx, store)
	if err != nil {
		reportErr(err)
		return
	}
	if seeded {
		reportChange(clikv.EventRetentionDaysKey, clikv.ShippedEventRetentionDays)
	}
}

func eventRetentionSetting(store runtimetypes.Store) workspaceevents.RetentionSetting {
	return func(ctx context.Context) (time.Duration, bool, error) {
		return clikv.EventRetention(ctx, store)
	}
}
