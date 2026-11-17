//go:build !llamanode

package llama_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/internal/modeld/capacity"
	"github.com/contenox/runtime/internal/modeld/llama"
	"github.com/contenox/runtime/internal/modeld/llama/llamacppshim"
	"github.com/contenox/runtime/internal/transport"
	"github.com/stretchr/testify/require"
)

type preparedMemory struct {
	free  int64
	calls int
}

func (m *preparedMemory) FreeBytes() (int64, error) {
	m.calls++
	return m.free, nil
}

func (m *preparedMemory) Snapshot() (capacity.DeviceSnapshot, error) {
	free, err := m.FreeBytes()
	return capacity.DeviceSnapshot{Kind: "cpu", TotalBytes: 16 << 30, FreeBytes: free}, err
}

type preparedClaimant struct {
	allocations []transport.NativeAllocation
	err         error
}

func (c *preparedClaimant) ClaimDevices(_ context.Context, allocation transport.NativeAllocation) error {
	c.allocations = append(c.allocations, allocation)
	return c.err
}

func TestUnit_ManagedSessionRetainsPreparedAllocationAcrossNamespaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model.gguf")
	require.NoError(t, os.WriteFile(path, []byte("model weights"), 0o600))
	memory := &preparedMemory{free: 8 << 30}
	host := &preparedMemory{free: 12 << 30}
	service, err := llama.NewServiceWithInspector(func(string) (llamacppshim.ModelKVProfile, error) {
		return llamacppshim.ModelKVProfile{ContextLength: 32768, BlockCount: 2, HeadCountKV: 1, HeadCount: 2, KeyLength: 128}, nil
	}, llama.WithMemorySource(memory), llama.WithHostMemorySource(host))
	require.NoError(t, err)
	claimant := &preparedClaimant{}
	managed, err := service.WithDeviceClaims(claimant)
	require.NoError(t, err)
	var opened []transport.Config
	llama.SetSessionFactory(func(_ string, config transport.Config, _ []llama.AdapterSpec) (transport.Session, error) {
		opened = append(opened, config)
		return &routedSession{}, nil
	})
	t.Cleanup(func() { llama.SetSessionFactory(nil) })
	request := transport.OpenSessionRequest{ModelName: "model", Digest: "sha256:model", Type: "llama", Path: path, Config: transport.Config{Operation: "chat", NumCtx: 4096, Target: transport.ExecutionTarget{Mode: transport.PlacementCPU}}}
	info, err := managed.Describe(t.Context(), request)
	require.NoError(t, err)
	require.NotNil(t, info.Allocation)
	request.Config = info.Allocation.Config
	probes := memory.calls
	for _, namespace := range []string{"first-conversation", "second-conversation"} {
		request.Config.SessionNamespace = namespace
		session, err := managed.OpenSession(t.Context(), request)
		require.NoError(t, err)
		require.NoError(t, session.Close())
		memory.free = 0
		host.free -= 1 << 20
	}
	require.Equal(t, probes, memory.calls)
	require.Len(t, opened, 2)
	require.Len(t, claimant.allocations, 2)
	for index, namespace := range []string{"first-conversation", "second-conversation"} {
		expected := info.Allocation.Config
		expected.SessionNamespace = namespace
		require.Equal(t, expected, opened[index])
		require.Equal(t, expected, claimant.allocations[index].Config)
	}
	require.Less(t, claimant.allocations[1].Resources.Pools[0].AvailableBytes, claimant.allocations[0].Resources.Pools[0].AvailableBytes)
	claimant.err = errors.New("admission refused current resources")
	_, err = managed.OpenSession(t.Context(), request)
	require.ErrorIs(t, err, claimant.err)
	require.Len(t, opened, 2)
	request.Config.NumCtx++
	_, err = managed.OpenSession(t.Context(), request)
	require.ErrorIs(t, err, transport.ErrContextOverflow)
	require.Greater(t, memory.calls, probes)
	require.Len(t, opened, 2)
}
