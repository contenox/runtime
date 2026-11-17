package supervisor_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/modeld/devicelease"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/liblease"
	"github.com/stretchr/testify/require"
)

func deviceRequest(fixture ownerFixture, contextSize int) transport.OpenSessionRequest {
	request := roleRequest(fixture, "primary")
	request.Config.NumCtx = contextSize
	request.Config.Target = transport.ExecutionTarget{Mode: transport.PlacementAccelerator, DeviceID: "pci:0000:01:00.0"}
	request.Config.Execution.Target = request.Config.Target
	return request
}

func TestUnit_SharedOwner_DeviceClaimSurvivesClientSessionSwitch(t *testing.T) {
	t.Setenv(devicelease.EnvLeaseDir, t.TempDir())
	fixture := sharedOwner(t)
	first, second := connectOwner(t, fixture), connectOwner(t, fixture)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	request := deviceRequest(fixture, 256)
	request.Config.SessionNamespace = "first-conversation"
	request.Config.Execution.IdleTTL = "1m"
	session, err := first.OpenSession(ctx, request)
	require.NoError(t, err)
	prefix := transport.PrefixInput{Text: "private conversation"}
	_, err = session.EnsurePrefix(ctx, prefix)
	require.NoError(t, err)
	require.NoError(t, session.Close())
	before, err := first.Status(ctx)
	require.NoError(t, err)
	require.Len(t, before.Workers, 1)
	resident, err := fixture.pool.InspectWorker(ctx, before.Workers[0].Instance)
	require.NoError(t, err)
	require.NotNil(t, resident.Status.Active)
	path, err := devicelease.Path(request.Config.Target.DeviceID)
	require.NoError(t, err)
	claim, err := liblease.Inspect(path)
	require.NoError(t, err)
	request.Config.SessionNamespace = "second-conversation"
	request.Config.Execution.RequesterID = "another-editor"
	next, err := second.OpenSession(ctx, request)
	require.NoError(t, err)
	require.Zero(t, next.ExplainContext().ResidentTokens)
	status, err := next.EnsurePrefix(ctx, prefix)
	require.NoError(t, err)
	require.Zero(t, status.ReusedTokens)
	chunks, err := next.Decode(ctx, transport.DecodeConfig{MaxTokens: 3})
	require.NoError(t, err)
	var output string
	for chunk := range chunks {
		require.NoError(t, chunk.Error)
		output += chunk.Text
	}
	require.Equal(t, "xxx", output)
	require.NoError(t, next.Close())
	after, err := second.Status(ctx)
	require.NoError(t, err)
	require.Len(t, after.Workers, 1)
	require.Equal(t, before.Workers[0].Instance, after.Workers[0].Instance)
	require.Equal(t, []string{request.Config.Target.DeviceID}, after.Workers[0].Devices)
	retained, err := liblease.Inspect(path)
	require.NoError(t, err)
	require.Equal(t, claim.InstanceID, retained.InstanceID)
	admission, err := second.Admission(ctx)
	require.NoError(t, err)
	require.Zero(t, admission.Active)
	require.Len(t, admission.Resources.Allocations, 1)
}

func TestUnit_SharedOwner_DeviceClaimSurvivesSessionCloseAndIsSharedBySiblings(t *testing.T) {
	t.Setenv(devicelease.EnvLeaseDir, t.TempDir())
	firstOwner, secondOwner := sharedOwner(t), sharedOwner(t)
	first, second := connectOwner(t, firstOwner), connectOwner(t, secondOwner)
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	for _, contextSize := range []int{256, 512} {
		session, err := first.OpenSession(ctx, deviceRequest(firstOwner, contextSize))
		require.NoError(t, err)
		require.NoError(t, session.Close())
	}
	status, err := first.Status(ctx)
	require.NoError(t, err)
	require.Len(t, status.Workers, 2)
	var leaseInstance string
	for _, worker := range status.Workers {
		require.Equal(t, []string{"pci:0000:01:00.0"}, worker.Devices)
		claims, err := first.ClaimDevices(ctx, transport.DeviceClaimRequest{OwnerInstance: firstOwner.holder.InstanceID(), WorkerInstance: worker.Instance, Devices: []transport.DeviceInfo{{ID: "pci:0000:01:00.0", Type: "gpu"}}, Config: worker.Spec.Config, Resources: fixtureAllocation()})
		require.NoError(t, err)
		require.Len(t, claims.Claims, 1)
		if leaseInstance != "" {
			require.Equal(t, leaseInstance, claims.Claims[0].Instance)
		}
		leaseInstance = claims.Claims[0].Instance
	}
	_, err = second.OpenSession(ctx, deviceRequest(secondOwner, 256))
	require.ErrorIs(t, err, transport.ErrDeviceBusy)
	require.NoError(t, first.UnloadModel(ctx, transport.UnloadModelRequest{Fence: transport.Fence{OwnerInstanceID: firstOwner.holder.InstanceID()}, WorkerInstance: status.Workers[0].Instance}))
	_, err = second.OpenSession(ctx, deviceRequest(secondOwner, 256))
	require.ErrorIs(t, err, transport.ErrDeviceBusy)
	require.NoError(t, first.UnloadModel(ctx, transport.UnloadModelRequest{Fence: transport.Fence{OwnerInstanceID: firstOwner.holder.InstanceID()}, WorkerInstance: status.Workers[1].Instance}))
	session, err := second.OpenSession(ctx, deviceRequest(secondOwner, 256))
	require.NoError(t, err)
	require.NoError(t, session.Close())
}

func TestUnit_SharedOwner_DeviceLeaseLossFencesResidentWorker(t *testing.T) {
	t.Setenv(devicelease.EnvLeaseDir, t.TempDir())
	fixture := sharedOwner(t)
	client := connectOwner(t, fixture)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	session, err := client.OpenSession(ctx, deviceRequest(fixture, 256))
	require.NoError(t, err)
	path, err := devicelease.Path("pci:0000:01:00.0")
	require.NoError(t, err)
	record, err := liblease.Inspect(path)
	require.NoError(t, err)
	record.InstanceID = "replacement-device-owner"
	raw, err := json.Marshal(record)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	require.Eventually(t, func() bool {
		_, err := session.EnsurePrefix(ctx, transport.PrefixInput{Text: "must be fenced"})
		return err != nil
	}, 5*time.Second, 25*time.Millisecond)
	_, err = client.ClaimDevices(ctx, transport.DeviceClaimRequest{OwnerInstance: "expired-owner", WorkerInstance: "expired-owner/worker", Devices: []transport.DeviceInfo{{ID: "pci:0000:01:00.0", Type: "gpu"}}})
	require.ErrorIs(t, err, transport.ErrStaleFence)
}

func TestUnit_SharedOwner_PhysicalDeviceIDsAreLocalToTheHostNamespace(t *testing.T) {
	firstOwner, secondOwner := sharedOwner(t), sharedOwner(t)
	first, second := connectOwner(t, firstOwner), connectOwner(t, secondOwner)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	t.Setenv(devicelease.EnvLeaseDir, t.TempDir())
	firstSession, err := first.OpenSession(ctx, deviceRequest(firstOwner, 256))
	require.NoError(t, err)
	t.Setenv(devicelease.EnvLeaseDir, t.TempDir())
	secondSession, err := second.OpenSession(ctx, deviceRequest(secondOwner, 256))
	require.NoError(t, err)
	require.NoError(t, firstSession.Close())
	require.NoError(t, secondSession.Close())
}
