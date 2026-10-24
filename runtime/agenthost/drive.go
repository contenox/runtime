package agenthost

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/version"
)

type RecordingHarness struct {
	libacp.UnimplementedClient

	mu      sync.Mutex
	updates []libacp.SessionNotification
}

func (h *RecordingHarness) SessionUpdate(_ context.Context, n libacp.SessionNotification) error {
	h.mu.Lock()
	h.updates = append(h.updates, n)
	h.mu.Unlock()
	return nil
}

func (h *RecordingHarness) Updates() []libacp.SessionNotification {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]libacp.SessionNotification(nil), h.updates...)
}

func (h *RecordingHarness) AvailableCommands() []libacp.AvailableCommand {
	var latest []libacp.AvailableCommand
	seen := false
	for _, n := range h.Updates() {
		if n.Update.SessionUpdate == libacp.SessionUpdateAvailableCommands {
			latest, seen = n.Update.AvailableCommands, true
		}
	}
	if !seen {
		return nil
	}
	return append([]libacp.AvailableCommand(nil), latest...)
}

func (h *RecordingHarness) MessageText() string {
	var sb strings.Builder
	for _, n := range h.Updates() {
		if n.Update.SessionUpdate != libacp.SessionUpdateAgentMessageChunk {
			continue
		}
		if c := n.Update.Content; c != nil && c.Type == string(libacp.ContentKindText) {
			sb.WriteString(c.Text)
		}
	}
	return sb.String()
}

type DenyingHarness struct {
	RecordingHarness

	denyMu sync.Mutex
	denied []string
}

func (h *DenyingHarness) RequestPermission(_ context.Context, req libacp.RequestPermissionRequest) (libacp.RequestPermissionResponse, error) {
	title := strings.TrimSpace(req.ToolCall.Title)
	if title == "" {
		title = req.ToolCall.ToolCallID
	}
	h.denyMu.Lock()
	h.denied = append(h.denied, title)
	h.denyMu.Unlock()
	for _, kind := range []libacp.PermissionOptionKind{libacp.PermissionRejectOnce, libacp.PermissionRejectAlways} {
		for _, opt := range req.Options {
			if opt.Kind == kind {
				return libacp.RequestPermissionResponse{Outcome: libacp.RequestPermissionOutcome{
					Outcome:  libacp.PermissionOutcomeSelected,
					OptionID: opt.OptionID,
				}}, nil
			}
		}
	}
	return libacp.RequestPermissionResponse{Outcome: libacp.RequestPermissionOutcome{
		Outcome: libacp.PermissionOutcomeCancelled,
	}}, nil
}

func (h *DenyingHarness) Denied() []string {
	h.denyMu.Lock()
	defer h.denyMu.Unlock()
	return append([]string(nil), h.denied...)
}

type TurnRequest struct {
	Cwd string

	Prompt []libacp.ContentBlock

	ClientInfo *libacp.Implementation

	ClientCapabilities libacp.ClientCapabilities

	McpServers []libacp.McpServer

	Stderr io.Writer

	KillGrace time.Duration
}

type TurnResult struct {
	Initialize libacp.InitializeResponse
	SessionID  libacp.SessionID
	StopReason libacp.StopReason

	ForwardedMcpServers []string
	DroppedMcpServers   []string
}

func DriveTurn(ctx context.Context, agent *runtimetypes.Agent, harness libacp.Client, req TurnRequest) (*TurnResult, error) {
	if req.Cwd == "" {
		return nil, fmt.Errorf("agenthost: TurnRequest.Cwd is required (ACP session/new needs a working directory)")
	}
	if len(req.Prompt) == 0 {
		return nil, fmt.Errorf("agenthost: TurnRequest.Prompt is required")
	}
	cfg, err := agent.ExternalACPConfig()
	if err != nil {
		return nil, fmt.Errorf("agenthost: resolve agent %q: %w", agent.Name, err)
	}

	if cfg.Cwd == "" {
		cfg.Cwd = req.Cwd
	}

	host := &ExternalACPAgent{Config: *cfg, Stderr: req.Stderr, KillGrace: req.KillGrace}
	handle, err := host.Connect(ctx, harness)
	if err != nil {
		return nil, err
	}
	defer handle.Close()

	clientInfo := &libacp.Implementation{Name: "contenox-agenthost"}
	if req.ClientInfo != nil {
		info := *req.ClientInfo
		clientInfo = &info
	}
	if clientInfo.Version == "" {
		clientInfo.Version = version.Get()
	}
	init, err := handle.Conn.Initialize(ctx, libacp.InitializeRequest{
		ProtocolVersion:    libacp.ProtocolVersion,
		ClientCapabilities: req.ClientCapabilities,
		ClientInfo:         clientInfo,
	})
	if err != nil {
		return nil, fmt.Errorf("agenthost: initialize agent %q: %w", agent.Name, err)
	}
	if init.ProtocolVersion != libacp.ProtocolVersion {
		return nil, fmt.Errorf("agenthost: agent %q negotiated unsupported protocol version %d (host speaks %d)",
			agent.Name, init.ProtocolVersion, libacp.ProtocolVersion)
	}

	mcpServers, droppedMcp := filterMcpServersByCapabilities(req.McpServers, init.AgentCapabilities.McpCapabilities)
	if mcpServers == nil {
		mcpServers = []libacp.McpServer{}
	}
	forwardedMcp := make([]string, 0, len(mcpServers))
	for _, srv := range mcpServers {
		forwardedMcp = append(forwardedMcp, srv.Name)
	}

	sess, err := handle.Conn.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        req.Cwd,
		McpServers: mcpServers,
	})
	if err != nil {
		return nil, fmt.Errorf("agenthost: session/new against agent %q: %w", agent.Name, err)
	}

	resp, err := handle.Conn.Prompt(ctx, libacp.PromptRequest{
		SessionID: sess.SessionID,
		Prompt:    req.Prompt,
	})
	if err != nil {
		return nil, fmt.Errorf("agenthost: prompt against agent %q: %w", agent.Name, err)
	}

	if err := handle.Close(); err != nil {
		return nil, fmt.Errorf("agenthost: close agent %q after turn: %w", agent.Name, err)
	}
	return &TurnResult{
		Initialize:          init,
		SessionID:           sess.SessionID,
		StopReason:          resp.StopReason,
		ForwardedMcpServers: forwardedMcp,
		DroppedMcpServers:   droppedMcp,
	}, nil
}
