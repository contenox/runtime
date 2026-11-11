package hitlservice_test

import (
	"context"
	"testing"
	"time"

	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_Respond_NotifiesTheDecisionObserverOnce(t *testing.T) {
	t.Parallel()
	ctx, store, _ := setupHITLDB(t)
	svc := newDurableService(t, store)

	type seen struct {
		id       string
		approved bool
		state    runtimetypes.HITLApprovalState
	}
	var calls []seen
	hitlservice.SetDecisionObserver(svc, func(_ context.Context, approval runtimetypes.HITLApproval, approved bool) {
		calls = append(calls, seen{id: approval.ID, approved: approved, state: approval.State})
	})

	now := time.Now().UTC()
	row := seedPendingRow(t, ctx, store, "deny", now, now.Add(time.Hour))

	require.NoError(t, svc.Respond(ctx, row.ID, false))
	require.ErrorIs(t, svc.Respond(ctx, row.ID, true), hitlservice.ErrApprovalAlreadyResolved)

	require.Len(t, calls, 1)
	require.Equal(t, row.ID, calls[0].id)
	require.False(t, calls[0].approved)
	require.Equal(t, runtimetypes.HITLApprovalDenied, calls[0].state)
}

func TestUnit_SweepExpired_NotifiesTheDecisionObserver(t *testing.T) {
	t.Parallel()
	ctx, store, _ := setupHITLDB(t)
	svc := newDurableService(t, store)

	var states []runtimetypes.HITLApprovalState
	hitlservice.SetDecisionObserver(svc, func(_ context.Context, approval runtimetypes.HITLApproval, _ bool) {
		states = append(states, approval.State)
	})

	past := time.Now().UTC().Add(-time.Hour)
	seedPendingRow(t, ctx, store, "deny", past, past.Add(time.Minute))
	n, err := svc.SweepExpired(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, []runtimetypes.HITLApprovalState{runtimetypes.HITLApprovalExpired}, states,
		"a decision made by the deadline is still a decision the workspace stream records")
}
