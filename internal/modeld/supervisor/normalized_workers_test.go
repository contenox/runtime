package supervisor_test

import (
	"testing"

	"github.com/contenox/runtime/internal/transport"
	"github.com/stretchr/testify/require"
)

func TestUnit_SharedOwner_ReusesBackendNormalizedAllocationAcrossRoles(t *testing.T) {
	fixture := sharedOwner(t)
	first, second := connectOwner(t, fixture), connectOwner(t, fixture)
	request := roleRequest(fixture, "primary")
	session, err := first.OpenSession(t.Context(), request)
	require.NoError(t, err)
	execution := session.ExplainContext().Execution
	require.NotNil(t, execution)
	require.Equal(t, transport.PlacementAutomatic, execution.Requested.Target.Mode)
	require.Equal(t, transport.PlacementCPU, execution.Resolved.Target.Mode)
	require.Equal(t, fixture.holder.InstanceID(), execution.OwnerEpoch)
	require.NotEmpty(t, execution.WorkerIdentity)
	require.Zero(t, execution.Resolved.AdmissionPermit.ID)
	require.Empty(t, execution.Capacity.Allocation.Config.SessionNamespace)
	require.NoError(t, session.Close())
	before, err := first.Status(t.Context())
	require.NoError(t, err)
	require.Len(t, before.Workers, 1)
	request = roleRequest(fixture, "alternative")
	request.Config.NumBatch = 512
	request.Config.Target = transport.ExecutionTarget{Mode: transport.PlacementCPU}
	request.Config.Execution.Target = request.Config.Target
	request.Config.SessionNamespace = "another-client-session"
	session, err = second.OpenSession(t.Context(), request)
	require.NoError(t, err)
	require.NoError(t, session.Close())
	after, err := second.Status(t.Context())
	require.NoError(t, err)
	require.Len(t, after.Workers, 1)
	require.Equal(t, before.Workers[0].Instance, after.Workers[0].Instance)
	require.Equal(t, transport.PlacementCPU, after.Workers[0].Spec.Config.Target.Mode)
	require.Equal(t, 512, after.Workers[0].Spec.Config.NumBatch)
	reservations, err := second.Admission(t.Context())
	require.NoError(t, err)
	require.Len(t, reservations.Resources.Allocations, 1)
}
