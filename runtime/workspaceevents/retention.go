package workspaceevents

import (
	"context"
	"time"

	"github.com/contenox/runtime/libroutine"
	"github.com/contenox/runtime/libtracker"
)

const DefaultRetentionSweep = 6 * time.Hour

type PartitionPruner interface {
	PrunePartitionsBefore(ctx context.Context, t time.Time) ([]string, error)
}

type RetentionSetting func(ctx context.Context) (keep time.Duration, enabled bool, err error)

func SweepRetention(ctx context.Context, log PartitionPruner, setting RetentionSetting, now time.Time, tracker libtracker.ActivityTracker) ([]string, error) {
	if tracker == nil {
		tracker = libtracker.NoopTracker{}
	}
	reportErr, reportChange, end := tracker.Start(ctx, "prune", "workspace_event_days")
	defer end()
	keep, enabled, err := setting(ctx)
	if err != nil {
		reportErr(err)
		return nil, err
	}
	if !enabled {
		return nil, nil
	}
	dropped, err := log.PrunePartitionsBefore(ctx, now.Add(-keep))
	if len(dropped) > 0 {
		reportChange("dropped", dropped)
	}
	if err != nil {
		reportErr(err)
	}
	return dropped, err
}

func StartRetention(ctx context.Context, log PartitionPruner, setting RetentionSetting, every time.Duration, tracker libtracker.ActivityTracker) func() {
	sweepCtx, cancel := context.WithCancel(ctx)
	routine := libroutine.NewRoutine(3, every)
	go routine.Loop(sweepCtx, every, nil, func(ctx context.Context) error {
		_, err := SweepRetention(ctx, log, setting, time.Now(), tracker)
		return err
	}, func(error) {})
	return cancel
}
