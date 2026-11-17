//go:build !llamanode

package llama_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/internal/modeld/llama"
	"github.com/contenox/runtime/internal/modeld/llama/llamacppshim"
	"github.com/contenox/runtime/internal/transport"
	"github.com/stretchr/testify/require"
)

type routedSession struct {
	transport.Session
	closed bool
	prefix transport.PrefixInput
}

func (s *routedSession) EnsurePrefix(_ context.Context, input transport.PrefixInput) (transport.PrefixStatus, error) {
	s.prefix = input
	return transport.PrefixStatus{}, nil
}

func (s *routedSession) ExplainContext() transport.ContextReport {
	return transport.ContextReport{NumCtx: 4096}
}

func (s *routedSession) Close() error {
	s.closed = true
	return nil
}

func TestOpenSessionRoutesModelAndConfigToBackend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model.gguf")
	require.NoError(t, os.WriteFile(path, []byte("model weights"), 0o600))
	service, err := llama.NewServiceWithInspector(func(got string) (llamacppshim.ModelKVProfile, error) {
		require.Equal(t, path, got)
		return llamacppshim.ModelKVProfile{ContextLength: 32768, BlockCount: 2, HeadCountKV: 1, HeadCount: 2, KeyLength: 128}, nil
	})
	require.NoError(t, err)
	var gotModel string
	var gotConfig transport.Config
	backend := &routedSession{}
	llama.SetSessionFactory(func(model string, cfg transport.Config, _ []llama.AdapterSpec) (transport.Session, error) {
		gotModel, gotConfig = model, cfg
		return backend, nil
	})
	t.Cleanup(func() { llama.SetSessionFactory(nil) })
	requested := transport.Config{NumCtx: 4096, PromptFormat: "chatml", Target: transport.ExecutionTarget{Mode: transport.PlacementCPU}}
	session, err := service.OpenSession(context.Background(), transport.OpenSessionRequest{
		ModelName: "foo", Digest: "sha256:test", Type: "llama", Path: path, Config: requested,
	})
	require.NoError(t, err)
	require.Equal(t, path, gotModel)
	require.Equal(t, requested.NumCtx, gotConfig.NumCtx)
	require.Equal(t, requested.PromptFormat, gotConfig.PromptFormat)
	report := session.ExplainContext()
	require.Equal(t, 4096, report.NumCtx)
	require.NotNil(t, report.Execution)
	require.Equal(t, "foo", report.Execution.ModelName)
	require.Equal(t, "sha256:test", report.Execution.Digest)
	require.Equal(t, requested, report.Execution.Requested)
	require.Equal(t, gotConfig, report.Execution.Resolved)
	prefix := transport.PrefixInput{Text: "stable request prefix"}
	_, err = session.EnsurePrefix(context.Background(), prefix)
	require.NoError(t, err)
	require.Equal(t, prefix, backend.prefix)
	require.NoError(t, session.Close())
	require.True(t, backend.closed)
}
