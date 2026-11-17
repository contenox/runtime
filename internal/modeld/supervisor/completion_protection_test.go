package supervisor_test

import (
	"context"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/transport"
	"github.com/stretchr/testify/require"
)

func TestUnit_SharedOwner_ClientCompletionProtectionSurvivesOtherClientClosure(t *testing.T) {
	fixture := sharedOwner(t)
	first, second := connectOwner(t, fixture), connectOwner(t, fixture)
	request := roleRequest(fixture, "autocomplete")
	session, err := first.OpenSession(t.Context(), request)
	require.NoError(t, err)
	allocation := session.ExplainContext().Execution.Capacity.Allocation.Resources
	require.NotEmpty(t, allocation.Identity)
	require.NoError(t, session.Close())
	request.Config.Execution.Completion = transport.CompletionProtection{State: "protected", Reservations: []admission.Protection{{Identity: allocation.Identity, Pools: allocation.Pools}}}
	for _, client := range []interface {
		OpenSession(context.Context, transport.OpenSessionRequest) (transport.Session, error)
	}{first, second} {
		session, err := client.OpenSession(t.Context(), request)
		require.NoError(t, err)
		require.NoError(t, session.Close())
	}
	before, err := second.Admission(t.Context())
	require.NoError(t, err)
	require.Len(t, before.Resources.Protections, 2)
	require.Len(t, before.Resources.Allocations, 1)
	require.NoError(t, first.Close())
	require.Eventually(t, func() bool {
		snapshot, err := second.Admission(t.Context())
		return err == nil && len(snapshot.Resources.Protections) == 1 && len(snapshot.Resources.Allocations) == 1
	}, 3*time.Second, 20*time.Millisecond)
}
