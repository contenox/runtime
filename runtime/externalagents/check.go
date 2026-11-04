package externalagents

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/libsandbox"
	"github.com/contenox/runtime/runtime/agenthost"
	"github.com/contenox/runtime/runtime/agentoptions"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/version"
)

const (
	ProblemSandboxUnavailable = "sandbox_unavailable"
	ProblemCommandNotFound    = "command_not_found"
	ProblemStartFailed        = "start_failed"
	ProblemHandshakeFailed    = "handshake_failed"
	ProblemDisabled           = "disabled"
)

const checkTimeout = 60 * time.Second

const authStatusNotification = "_auth/status_update"

type AuthMethod struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"`
	Unofficial  bool   `json:"unofficial,omitempty"`
}

type AgentInfo struct {
	Name    string `json:"name,omitempty"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

type CheckResult struct {
	Ready       bool         `json:"ready"`
	Agent       *AgentInfo   `json:"agent,omitempty"`
	AuthMethods []AuthMethod `json:"authMethods"`
	SignedIn    *bool        `json:"signedIn,omitempty"`
	AuthLabel   string       `json:"authLabel,omitempty"`
	Problem     *Problem     `json:"problem,omitempty"`
	CheckedAt   time.Time    `json:"checkedAt"`
}

type authStatus struct {
	AuthStatus struct {
		Kind  string `json:"kind"`
		Label string `json:"label"`
	} `json:"authStatus"`
}

type handshake struct {
	wantOptions bool
	options     []libacp.SessionConfigOption
	init        libacp.InitializeResponse
	authKind    string
	authLabel   string
	authSeen    bool
	authMethod  map[string]libacp.AuthMethod
}

func (s *Service) checkWorkspace() (string, error) {
	dir := filepath.Join(s.deps.ContenoxDir, "external-agents", "check-workspace")
	return dir, os.MkdirAll(dir, 0o755)
}

func (s *Service) shake(ctx context.Context, cfg runtimetypes.ExternalACPConfig, wantOptions bool) (*handshake, *Problem) {
	cwd, err := s.checkWorkspace()
	if err != nil {
		return nil, &Problem{Kind: ProblemStartFailed, Message: "could not prepare a folder to start the agent in", Detail: err.Error()}
	}
	cfg.Cwd = cwd
	if _, err := s.lookPath(cfg.Command); err != nil {
		return nil, &Problem{Kind: ProblemCommandNotFound, Message: "the agent's command was not found", Detail: err.Error()}
	}
	host := &agenthost.ExternalACPAgent{Config: cfg, KillGrace: 2 * time.Second, Stderr: &boundedBuffer{limit: 8192}}
	handle, err := host.Connect(ctx, libacp.UnimplementedClient{})
	if err != nil {
		return nil, startProblem(err, host.Stderr)
	}
	defer handle.Close()

	var mu sync.Mutex
	hs := &handshake{authMethod: map[string]libacp.AuthMethod{}, wantOptions: wantOptions}
	statusSeen := make(chan struct{}, 1)
	handle.Conn.SetExtNotificationHandler(func(_ context.Context, method string, params json.RawMessage) {
		if method != authStatusNotification {
			return
		}
		var st authStatus
		if json.Unmarshal(params, &st) != nil {
			return
		}
		mu.Lock()
		hs.authKind, hs.authLabel, hs.authSeen = st.AuthStatus.Kind, st.AuthStatus.Label, true
		mu.Unlock()
		select {
		case statusSeen <- struct{}{}:
		default:
		}
	})

	init, err := handle.Conn.Initialize(ctx, libacp.InitializeRequest{
		ProtocolVersion:    libacp.ProtocolVersion,
		ClientCapabilities: libacp.ClientCapabilities{Auth: libacp.AuthCapabilities{Terminal: true}},
		ClientInfo:         &libacp.Implementation{Name: "contenox", Version: version.Get()},
	})
	if err != nil {
		_ = handle.Close()
		return nil, &Problem{Kind: ProblemHandshakeFailed, Message: "the agent started but did not answer the ACP handshake", Detail: joinDetail(err.Error(), host.Stderr)}
	}
	hs.init = init
	for _, m := range init.AuthMethods {
		hs.authMethod[m.ID] = m
	}
	if hs.wantOptions {
		if sess, err := handle.Conn.NewSession(ctx, libacp.NewSessionRequest{Cwd: cwd, McpServers: []libacp.McpServer{}, Meta: cfg.SessionMeta}); err == nil {
			hs.options = agentoptions.RestrictModes(sess.ConfigOptions, cfg.Modes, "")
		}
	}
	select {
	case <-statusSeen:
	case <-time.After(3 * time.Second):
	case <-ctx.Done():
	}
	mu.Lock()
	defer mu.Unlock()
	cp := *hs
	return &cp, nil
}

func (s *Service) Check(ctx context.Context, name string) (CheckResult, error) {
	if p, ok := presetByAgentName(name); ok {
		if err := s.syncPreset(ctx, p); err != nil {
			return CheckResult{}, err
		}
	}
	a, err := s.get(ctx, name)
	if err != nil {
		return CheckResult{}, err
	}
	res := CheckResult{AuthMethods: []AuthMethod{}, CheckedAt: time.Now().UTC()}
	if !a.Enabled {
		res.Problem = &Problem{Kind: ProblemDisabled, Message: "the agent is turned off"}
		return res, nil
	}
	cfg, err := a.ExternalACPConfig()
	if err != nil {
		return CheckResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	reportErr, reportChange, end := s.deps.Tracker.Start(ctx, "check", "external_agent", "agent", name)
	defer end()
	hs, problem := s.shake(ctx, *cfg, true)
	if problem != nil {
		reportErr(errors.New(problem.Kind + ": " + problem.Message))
		res.Problem = problem
		return res, nil
	}
	if s.deps.DB != nil && len(hs.options) > 0 {
		_ = agentoptions.Save(ctx, runtimetypes.New(s.deps.DB.WithoutTransaction()), name, hs.options)
	}
	if info := hs.init.AgentInfo; info != nil {
		res.Agent = &AgentInfo{Name: info.Name, Title: info.Title, Version: info.Version}
	}
	preset, isPreset := presetByAgentName(name)
	allowUnofficial := s.unofficialAllowed(ctx)
	for _, m := range hs.init.AuthMethods {
		if isPreset && preset.isUnofficialMethod(m.ID) && !allowUnofficial {
			continue
		}
		res.AuthMethods = append(res.AuthMethods, AuthMethod{ID: m.ID, Name: m.Name, Description: strings.TrimSpace(m.Description), Type: m.Type, Unofficial: isPreset && preset.isUnofficialMethod(m.ID)})
	}
	if hs.authSeen {
		signed := hs.authKind != "" && hs.authKind != "none"
		res.SignedIn = &signed
		res.AuthLabel = hs.authLabel
	}
	res.Ready = res.SignedIn == nil || *res.SignedIn
	reportChange(name, map[string]any{"ready": res.Ready})
	return res, nil
}

func startProblem(err error, stderr any) *Problem {
	detail := joinDetail(err.Error(), stderr)
	switch {
	case errors.Is(err, libsandbox.ErrIsolation):
		return &Problem{Kind: ProblemSandboxUnavailable, Message: "the agent sandbox cannot be built on this computer", Detail: detail}
	case errors.Is(err, os.ErrNotExist):
		return &Problem{Kind: ProblemCommandNotFound, Message: "the agent's command was not found", Detail: detail}
	}
	return &Problem{Kind: ProblemStartFailed, Message: "the agent could not be started", Detail: detail}
}

func joinDetail(msg string, stderr any) string {
	if b, ok := stderr.(*boundedBuffer); ok {
		if out := strings.TrimSpace(b.String()); out != "" {
			return msg + "\n" + out
		}
	}
	return msg
}

type boundedBuffer struct {
	mu    sync.Mutex
	limit int
	buf   []byte
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if over := len(b.buf) - b.limit; over > 0 {
		b.buf = b.buf[over:]
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
