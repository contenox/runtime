package contenoxcli

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/modeld/owner"
	"github.com/contenox/runtime/internal/modeld/snapshotauth"
	"github.com/contenox/runtime/internal/transport"
	transportgrpc "github.com/contenox/runtime/internal/transport/grpc"
	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/acpsvc"
	"github.com/contenox/runtime/runtime/agentservice"
	"github.com/contenox/runtime/runtime/backendservice"
	"github.com/contenox/runtime/runtime/llmrepo"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/vfs"
	"github.com/go-git/go-git/v5"
	"github.com/stretchr/testify/require"
)

const harnessContextTokens = 32768

// The deterministic Modeld peer replaces only token generation. The shipped
// agent chain, git provider, workspace, ACP client and gRPC transport are real.
type nativeHarnessService struct {
	*gatewayNativeService
	mu         sync.Mutex
	calls      int
	toolResult string
	namespaces []string
	scopes     []string
}

func (s *nativeHarnessService) Describe(context.Context, transport.OpenSessionRequest) (transport.ModelInfo, error) {
	return transport.ModelInfo{EffectiveContext: harnessContextTokens, ModelMaxContext: harnessContextTokens, HotContextTokens: harnessContextTokens}, nil
}

func (s *nativeHarnessService) OpenSession(ctx context.Context, req transport.OpenSessionRequest) (transport.Session, error) {
	s.mu.Lock()
	s.namespaces = append(s.namespaces, req.Config.SessionNamespace)
	s.scopes = append(s.scopes, snapshotauth.Scope(ctx))
	s.mu.Unlock()
	req.Config.NumCtx = harnessContextTokens
	base, err := s.MemoryService.OpenSession(ctx, req)
	if err != nil {
		return nil, err
	}
	return &nativeHarnessSession{Session: base, service: s}, nil
}

type nativeHarnessSession struct {
	transport.Session
	service *nativeHarnessService
	suffix  transport.SuffixInput
}

func (s *nativeHarnessSession) PrefillSuffix(ctx context.Context, input transport.SuffixInput) (transport.SuffixStatus, error) {
	s.suffix = input
	return s.Session.PrefillSuffix(ctx, input)
}

func (s *nativeHarnessSession) Decode(context.Context, transport.DecodeConfig) (<-chan transport.StreamChunk, error) {
	service := s.service
	service.mu.Lock()
	defer service.mu.Unlock()
	service.calls++
	chunk := transport.StreamChunk{FinishReason: "stop"}
	switch service.calls {
	case 1, 5, 7:
		chunk.Text = "general"
	case 2:
		call := transport.ToolCall{ID: "native-git-call", Type: "function"}
		call.Function.Name, call.Function.Arguments = "git_status", "{}"
		chunk.ToolCalls = []transport.ToolCall{call}
	case 3:
		for _, segment := range s.suffix.Manifest.Segments {
			if segment.Kind == "tool" && segment.ToolCallID == "native-git-call" {
				start := segment.ByteStart - s.suffix.Manifest.StableBytes
				end := segment.ByteEnd - s.suffix.Manifest.StableBytes
				if start >= 0 && end > start && end <= len(s.suffix.Text) {
					service.toolResult = s.suffix.Text[start:end]
				}
			}
		}
		chunk.Text = "Git status returned: " + service.toolResult
	case 4:
		chunk.Text = "Remember the repository status."
	case 6, 8:
		chunk.Text = "Acknowledged."
	default:
		return nil, fmt.Errorf("unexpected inference call %d", service.calls)
	}
	ch := make(chan transport.StreamChunk, 1)
	ch <- chunk
	close(ch)
	return ch, nil
}

func TestSystem_Modeld_HarnessToolRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	root := t.TempDir()
	t.Setenv("CONTENOX_DATA_ROOT", root)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	lease, err := owner.Join(ctx, owner.Config{LeasePath: filepath.Join(root, "modeld.lease"), TTL: time.Minute, Endpoint: listener.Addr().String(), Backend: "llama"})
	require.NoError(t, err)
	defer lease.Release()
	service := &nativeHarnessService{gatewayNativeService: &gatewayNativeService{MemoryService: transport.NewMemoryService()}}
	go func() {
		_ = transportgrpc.Serve(ctx, listener, service, lease.InstanceID(), "llama", transportgrpc.WithLocalSnapshots(root))
	}()

	workspace := t.TempDir()
	_, err = git.PlainInit(workspace, false)
	require.NoError(t, err)
	file, err := os.CreateTemp(workspace, "modeld-untracked-*.txt")
	require.NoError(t, err)
	require.NoError(t, file.Close())
	want := filepath.Base(file.Name())
	db, err := OpenDBAt(ctx, filepath.Join(root, "runtime.db"))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, backendservice.New(db).Create(ctx, &runtimetypes.Backend{ID: "native-worker", Name: "native", Type: "modeld", BaseURL: "local"}))
	engine, err := BuildEngine(ctx, db, chatOpts{ContenoxDir: root, EffectiveDefaultModel: "native-test", EffectiveDefaultProvider: "modeld", EffectiveContext: harnessContextTokens, EffectiveLocalExecAllowedDir: workspace})
	require.NoError(t, err)
	defer engine.Stop()
	roots, err := vfs.NewFactory(workspace)
	require.NoError(t, err)
	server, peer := net.Pipe()
	defer server.Close()
	defer peer.Close()
	agent := libacp.NewAgentSideConnection(server, acpsvc.New(acpsvc.Deps{
		Engine: engine, DB: db, WorkspaceRoots: roots, WorkspaceID: "native-tools", ContenoxDir: root,
		ChainRegistry: acpsvc.NewChainRegistryFor(compiledShippedAgent(t, "acp"), "shipped"), DefaultModel: "native-test", DefaultProvider: "modeld",
	}))
	events := &fwdACPClient{updates: make(chan libacp.SessionNotification, 128)}
	client := libacp.NewClientSideConnection(peer, func(*libacp.ClientSideConnection) libacp.Client { return events })
	go func() { _ = agent.Run(ctx) }()
	go func() { _ = client.Run(ctx) }()
	_, err = client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)
	session, err := client.NewSession(ctx, libacp.NewSessionRequest{Cwd: workspace, McpServers: []libacp.McpServer{}})
	require.NoError(t, err)
	_, err = client.Prompt(ctx, libacp.PromptRequest{SessionID: session.SessionID, Prompt: []libacp.ContentBlock{libacp.NewTextContent("Inspect the repository status.")}})
	require.NoError(t, err)
	service.mu.Lock()
	calls, result := service.calls, service.toolResult
	service.mu.Unlock()
	require.Equal(t, 3, calls)
	require.Contains(t, result, want, "the Modeld continuation must contain the actual git status for this ACP workspace and the original call ID")
	var reply strings.Builder
	for !strings.Contains(reply.String(), want) {
		select {
		case event := <-events.updates:
			if event.SessionID == session.SessionID && event.Update.SessionUpdate == libacp.SessionUpdateAgentMessageChunk && event.Update.Content != nil {
				reply.WriteString(event.Update.Content.Text)
			}
		case <-ctx.Done():
			t.Fatalf("ACP failed to deliver the model continuation: %s", reply.String())
		}
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "chain-compact.json"), []byte(initCompactChain), 0o600))
	_, err = client.Prompt(ctx, libacp.PromptRequest{SessionID: session.SessionID, Prompt: []libacp.ContentBlock{libacp.NewTextContent("/compact 0")}})
	require.NoError(t, err)
	stored := agentservice.New(agentservice.Deps{Engine: engine, DB: db, WorkspaceID: "native-tools", Identity: "acp-client"})
	sessionID, messages, err := stored.SessionLoad(ctx, string(session.SessionID))
	require.NoError(t, err)
	var summaryFound bool
	for _, message := range messages {
		if message.Content == "<compact-summary>\nRemember the repository status.\n</compact-summary>" {
			summaryFound = true
		}
	}
	require.True(t, summaryFound, "the same session must retain the compacted native response")

	_, err = client.ResumeSession(ctx, libacp.ResumeSessionRequest{Cwd: workspace, SessionID: session.SessionID})
	require.NoError(t, err)
	_, err = client.Prompt(ctx, libacp.PromptRequest{SessionID: session.SessionID, Prompt: []libacp.ContentBlock{libacp.NewTextContent("Continue.")}})
	require.NoError(t, err)
	other, err := client.NewSession(ctx, libacp.NewSessionRequest{Cwd: workspace})
	require.NoError(t, err)
	_, err = client.Prompt(ctx, libacp.PromptRequest{SessionID: other.SessionID, Prompt: []libacp.ContentBlock{libacp.NewTextContent("Start fresh.")}})
	require.NoError(t, err)
	otherID, _, err := stored.SessionLoad(ctx, string(other.SessionID))
	require.NoError(t, err)
	require.NotEqual(t, sessionID, otherID)
	service.mu.Lock()
	namespaces := append([]string(nil), service.namespaces...)
	scopes := append([]string(nil), service.scopes...)
	service.mu.Unlock()
	require.Len(t, namespaces, 8)
	require.NotEmpty(t, scopes[0])
	require.NotEmpty(t, scopes[6])
	require.NotEqual(t, scopes[0], scopes[6])
	// The daemon prefixes the opaque Runtime session key with its live connection scope.
	for i, namespace := range namespaces {
		id := sessionID
		scope := scopes[0]
		if i >= 6 {
			id = otherID
			scope = scopes[6]
		}
		require.True(t, strings.HasSuffix(namespace, "/"+llmrepo.DeriveSessionKey(id)), "inference %d used %q for session %s", i, namespace, id)
		require.Equal(t, scope, scopes[i], "authenticated snapshot scope for inference %d", i)
	}
}
