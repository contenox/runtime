package supervisor_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/modeld/internal/sessionkit"
	"github.com/contenox/runtime/internal/transport"
	"github.com/stretchr/testify/require"
)

type cancellableMemory struct{ transport.Service }

func (s cancellableMemory) Describe(ctx context.Context, request transport.OpenSessionRequest) (transport.ModelInfo, error) {
	info, err := s.Service.Describe(ctx, request)
	if err != nil {
		return info, err
	}
	config := request.Config
	if config.NumBatch == 0 {
		config.NumBatch = 512
	}
	if config.Target.Normalize().Mode == transport.PlacementAutomatic {
		config.Target = transport.ExecutionTarget{Mode: transport.PlacementCPU}
	}
	info.Allocation = &transport.NativeAllocation{Config: config, Devices: []transport.DeviceInfo{}, Resources: fixtureAllocation()}
	return info, nil
}

func (s cancellableMemory) WithDeviceClaims(claimant transport.DeviceClaimant) (transport.Service, error) {
	return claimedMemory{Service: s, claimant: claimant}, nil
}

type claimedMemory struct {
	transport.Service
	claimant transport.DeviceClaimant
}

func (s claimedMemory) OpenSession(ctx context.Context, request transport.OpenSessionRequest) (transport.Session, error) {
	devices := []transport.DeviceInfo{}
	if request.Config.Target.Mode == transport.PlacementAccelerator {
		devices = append(devices, transport.DeviceInfo{ID: request.Config.Target.DeviceID, Type: "gpu"})
	}
	allocation := transport.NativeAllocation{Devices: devices, Config: request.Config, Resources: fixtureAllocation()}
	if err := s.claimant.ClaimDevices(ctx, allocation); err != nil {
		return nil, err
	}
	session, err := s.Service.OpenSession(ctx, request)
	if err != nil {
		return nil, err
	}
	return sessionkit.WithExecution(session, transport.SessionExecution{ModelName: request.ModelName, Digest: request.Digest, Backend: request.Type, Requested: request.Config, Resolved: request.Config, Capacity: transport.ModelInfo{Allocation: &allocation}})
}

func fixtureAllocation() admission.Allocation {
	return admission.Allocation{CanYield: true, Threads: 1, ThreadLimit: 64, Pools: []admission.PoolAllocation{{PoolID: "host", Bytes: 1024, LimitBytes: 1 << 30, AvailableBytes: 1 << 30}}}
}

func (s cancellableMemory) OpenSession(ctx context.Context, req transport.OpenSessionRequest) (transport.Session, error) {
	session, err := s.Service.OpenSession(ctx, req)
	if err != nil {
		return nil, err
	}
	return &cancellableSession{Session: session}, nil
}

type cancellableSession struct{ transport.Session }

func (s *cancellableSession) Decode(ctx context.Context, config transport.DecodeConfig) (<-chan transport.StreamChunk, error) {
	if config.MaxTokens == 888888 {
		chunks := make(chan transport.StreamChunk, 1)
		go func() {
			defer close(chunks)
			for token := 0; ; token++ {
				if err := sessionkit.NativeBoundary(ctx); err != nil {
					return
				}
				if !sessionkit.Send(ctx, chunks, transport.StreamChunk{Text: strconv.Itoa(token)}) {
					return
				}
			}
		}()
		return chunks, nil
	}
	if config.MaxTokens != 999999 {
		return s.Session.Decode(ctx, config)
	}
	chunks := make(chan transport.StreamChunk, 1)
	chunks <- transport.StreamChunk{Text: "started"}
	go func() {
		defer close(chunks)
		<-ctx.Done()
		chunks <- transport.StreamChunk{Error: ctx.Err()}
	}()
	return chunks, nil
}

func TestUnit_SharedOwner_SupersessionCancelsActiveNativeDecode(t *testing.T) {
	fixture := sharedOwner(t)
	client := connectOwner(t, fixture)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	request := roleRequest(fixture, "autocomplete")
	request.Config.Execution.Priority = string(admission.Autocomplete)
	request.Config.Execution.Supersede = true
	first, err := client.OpenSession(ctx, request)
	require.NoError(t, err)
	chunks, err := first.Decode(ctx, transport.DecodeConfig{MaxTokens: 999999})
	require.NoError(t, err)
	select {
	case chunk := <-chunks:
		require.Equal(t, "started", chunk.Text)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	second, err := client.OpenSession(ctx, request)
	require.NoError(t, err)
	var cancelled error
	for chunk := range chunks {
		if chunk.Error != nil {
			cancelled = chunk.Error
		}
	}
	require.ErrorIs(t, cancelled, admission.ErrSuperseded)
	_, err = first.EnsurePrefix(ctx, transport.PrefixInput{Text: "stale"})
	require.ErrorIs(t, err, admission.ErrSuperseded)
	_, err = second.EnsurePrefix(ctx, transport.PrefixInput{Text: "current"})
	require.NoError(t, err)
	require.NoError(t, first.Close())
	require.NoError(t, second.Close())
	status, err := client.Admission(ctx)
	require.NoError(t, err)
	require.Zero(t, status.Active)
}

func TestUnit_SharedOwner_EditorIdentityDoesNotCancelAnotherClient(t *testing.T) {
	fixture := sharedOwner(t)
	firstClient, secondClient := connectOwner(t, fixture), connectOwner(t, fixture)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	request := roleRequest(fixture, "autocomplete")
	request.Config.Execution.Priority = string(admission.Autocomplete)
	request.Config.Execution.Supersede = true
	first, err := firstClient.OpenSession(ctx, request)
	require.NoError(t, err)
	type result struct {
		session transport.Session
		err     error
	}
	opened := make(chan result, 1)
	go func() { session, err := secondClient.OpenSession(ctx, request); opened <- result{session, err} }()
	require.Eventually(t, func() bool {
		status, err := firstClient.Admission(ctx)
		return err == nil && len(status.Workers) == 1 && status.Workers[0].QueuedForeground == 1
	}, 5*time.Second, 10*time.Millisecond)
	_, err = first.EnsurePrefix(ctx, transport.PrefixInput{Text: "still owned by first IDE"})
	require.NoError(t, err)
	require.NoError(t, first.Close())
	select {
	case next := <-opened:
		require.NoError(t, next.err)
		require.Zero(t, next.session.ExplainContext().ResidentTokens)
		require.NoError(t, next.session.Close())
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
