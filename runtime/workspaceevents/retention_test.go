package workspaceevents_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/contenox/runtime/runtime/workspaceevents"
	"github.com/stretchr/testify/require"
)

type fakePruner struct {
	cutoffs []time.Time
	err     error
}

func (p *fakePruner) PrunePartitionsBefore(_ context.Context, t time.Time) ([]string, error) {
	p.cutoffs = append(p.cutoffs, t)
	return []string{"20261001"}, p.err
}

func TestUnit_SweepRetention_FollowsTheOperatorSetting(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	pruner := &fakePruner{}

	dropped, err := workspaceevents.SweepRetention(ctx, pruner, func(context.Context) (time.Duration, bool, error) {
		return 30 * 24 * time.Hour, true, nil
	}, now, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"20261001"}, dropped)
	require.Equal(t, []time.Time{now.Add(-30 * 24 * time.Hour)}, pruner.cutoffs)

	dropped, err = workspaceevents.SweepRetention(ctx, pruner, func(context.Context) (time.Duration, bool, error) {
		return 0, false, nil
	}, now, nil)
	require.NoError(t, err)
	require.Empty(t, dropped)
	require.Len(t, pruner.cutoffs, 1, "off keeps every day")

	_, err = workspaceevents.SweepRetention(ctx, pruner, func(context.Context) (time.Duration, bool, error) {
		return 0, false, errors.New("event-retention-days must be a whole number of days")
	}, now, nil)
	require.Error(t, err)
	require.Len(t, pruner.cutoffs, 1, "an unreadable setting prunes nothing")
}
