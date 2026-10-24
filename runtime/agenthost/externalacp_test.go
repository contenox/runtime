package agenthost_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/agenthost"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func buildStubAgent(t *testing.T) string {
	t.Helper()
	binPath := filepath.Join(t.TempDir(), "acp-stub-agent")
	cmd := exec.Command("go", "build", "-o", binPath, "github.com/contenox/runtime/libacp/cmd/acp-stub-agent")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build acp-stub-agent: %v\n%s", err, out)
	}
	return binPath
}

func TestHost_ExternalACPAgent_ConnectAndInitialize(t *testing.T) {
	requireSandboxable(t)
	agentBin := buildStubAgent(t)

	host := agenthost.NewExternalACPAgent(runtimetypes.ExternalACPConfig{
		Transport: runtimetypes.ExternalACPTransportStdio,
		Command:   agentBin,
		Cwd:       t.TempDir(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	handle, err := host.Connect(ctx, libacp.UnimplementedClient{})
	require.NoError(t, err)
	require.NotNil(t, handle)
	require.NotNil(t, handle.Conn)
	defer handle.Close()

	resp, err := handle.Conn.Initialize(ctx, libacp.InitializeRequest{
		ProtocolVersion: libacp.ProtocolVersion,
		ClientCapabilities: libacp.ClientCapabilities{
			FS: libacp.FileSystemCapabilities{ReadTextFile: true, WriteTextFile: true},
		},
		ClientInfo: &libacp.Implementation{Name: "agenthost-test", Version: "test"},
	})
	require.NoError(t, err)
	require.Equal(t, libacp.ProtocolVersion, resp.ProtocolVersion)
	require.NotNil(t, resp.AgentInfo)
	require.Equal(t, "acp-stub-agent", resp.AgentInfo.Name)

	require.NoError(t, handle.Close())

	select {
	case <-handle.Conn.Closed():
	case <-time.After(5 * time.Second):
		t.Fatal("ClientSideConnection did not report closed after Handle.Close")
	}
}

func TestHost_ExternalACPAgent_CloseIsIdempotent(t *testing.T) {
	requireSandboxable(t)
	agentBin := buildStubAgent(t)

	host := agenthost.NewExternalACPAgent(runtimetypes.ExternalACPConfig{
		Transport: runtimetypes.ExternalACPTransportStdio,
		Command:   agentBin,
		Cwd:       t.TempDir(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	handle, err := host.Connect(ctx, libacp.UnimplementedClient{})
	require.NoError(t, err)

	err1 := handle.Close()
	err2 := handle.Close()
	require.NoError(t, err1)
	require.Equal(t, err1, err2)
}

func TestHost_ExternalACPAgent_Connect_RejectsNilHarness(t *testing.T) {
	host := agenthost.NewExternalACPAgent(runtimetypes.ExternalACPConfig{
		Transport: runtimetypes.ExternalACPTransportStdio,
		Command:   "irrelevant-not-actually-spawned",
	})

	_, err := host.Connect(context.Background(), nil)
	require.Error(t, err)
}

func TestHost_ExternalACPAgent_Connect_RejectsInvalidConfig(t *testing.T) {
	host := agenthost.NewExternalACPAgent(runtimetypes.ExternalACPConfig{
		Transport: runtimetypes.ExternalACPTransportStdio,
	})

	_, err := host.Connect(context.Background(), libacp.UnimplementedClient{})
	require.Error(t, err)
}

func TestHost_ExternalACPAgent_Connect_EndpointNotImplemented(t *testing.T) {
	host := agenthost.NewExternalACPAgent(runtimetypes.ExternalACPConfig{
		Transport: runtimetypes.ExternalACPTransportEndpoint,
		URL:       "https://agent.example.com/acp",
	})

	_, err := host.Connect(context.Background(), libacp.UnimplementedClient{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not implemented")
}
