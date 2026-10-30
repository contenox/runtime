package acpsvc

import (
	"context"
	"encoding/json"
	"strings"

	libacp "github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/agentdecl"
	"github.com/contenox/runtime/runtime/homedir"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/shellsession"
)

const (
	TerminalOutputMetaKey = "contenox.terminalOutput"

	TerminalOutputUpdateKind libacp.SessionUpdateKind = "_contenox.terminalOutput"

	extMethodTerminalRun = "_contenox/terminal/run"
)

type terminalOutputPayload struct {
	SessionID string `json:"sessionId"`
	Offset    int64  `json:"offset"`
	Chunk     string `json:"chunk"`
	Reset     bool   `json:"reset,omitempty"`
}

type terminalRunParams struct {
	SessionID string `json:"sessionId"`
	Command   string `json:"command"`
}

type terminalRunResult struct {
	Offset  int64  `json:"offset"`
	Started bool   `json:"started,omitempty"`
	Output  string `json:"output,omitempty"`
}

func (t *Transport) handleExtRequest(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, *libacp.Error) {
	switch method {
	case ExtMethodWorkspaceConfigOptions:
		return t.handleWorkspaceConfigOptions(ctx)
	case extMethodTerminalRun:
		return t.handleTerminalRun(ctx, params)
	case ExtMethodEnvelopesList:
		return t.handleEnvelopesList()
	case ExtMethodAgentProblems:
		return t.handleAgentProblems()
	default:
		return nil, libacp.MethodNotFound(method)
	}
}

func (t *Transport) handleTerminalRun(ctx context.Context, params json.RawMessage) (json.RawMessage, *libacp.Error) {
	if t.deps.ShellSessions == nil {
		return nil, libacp.NewError(libacp.ErrMethodNotFound, "shell sessions are not enabled on this server")
	}
	var p terminalRunParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, libacp.NewErrorf(libacp.ErrInvalidParams, "invalid params: %v", err)
		}
	}
	sid := libacp.SessionID(p.SessionID)
	if sid == "" {
		return nil, libacp.NewError(libacp.ErrInvalidParams, "sessionId is required")
	}
	if strings.TrimSpace(p.Command) == "" {
		return nil, libacp.NewError(libacp.ErrInvalidParams, "command is required")
	}
	entry, ok := t.sessionFor(sid)
	if !ok {
		return nil, libacp.NewErrorf(libacp.ErrInvalidParams, "unknown session %q", sid)
	}
	t.subscribeTerminal(sid, entry.InternalSessionID)

	runCtx := context.WithValue(ctx, runtimetypes.SessionIDContextKey, entry.InternalSessionID)
	res, err := t.deps.ShellSessions.Run(runCtx, entry.InternalSessionID, p.Command)
	if err != nil {
		return nil, libacp.InternalError(err.Error())
	}
	out, mErr := json.Marshal(terminalRunResult{Offset: res.Offset, Started: res.Started, Output: res.Snapshot})
	if mErr != nil {
		return nil, libacp.InternalError(mErr.Error())
	}
	return out, nil
}

func (t *Transport) subscribeTerminal(sid libacp.SessionID, internalID string) {
	if t.deps.ShellSessions == nil || internalID == "" {
		return
	}
	cancel := t.deps.ShellSessions.Subscribe(internalID, func(c shellsession.Chunk) {
		t.sendTerminalChunk(sid, c)
	})
	t.termSubMu.Lock()
	if prev, ok := t.termSubs[sid]; ok {
		prev()
	}
	t.termSubs[sid] = cancel
	t.termSubMu.Unlock()
}

func (t *Transport) ensureTerminalSubscribed(sid libacp.SessionID, internalID string) {
	if t.deps.ShellSessions == nil || internalID == "" {
		return
	}
	t.termSubMu.Lock()
	_, exists := t.termSubs[sid]
	t.termSubMu.Unlock()
	if exists {
		return
	}
	t.subscribeTerminal(sid, internalID)
}

func (t *Transport) unsubscribeTerminal(sid libacp.SessionID) {
	t.termSubMu.Lock()
	cancel := t.termSubs[sid]
	delete(t.termSubs, sid)
	t.termSubMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (t *Transport) closeTerminal(sid libacp.SessionID, entry *sessionEntry) {
	t.unsubscribeTerminal(sid)
	if t.deps.ShellSessions != nil && entry != nil && entry.InternalSessionID != "" {
		t.deps.ShellSessions.Kill(entry.InternalSessionID)
	}
}

func (t *Transport) unsubscribeAllTerminals() {
	t.termSubMu.Lock()
	cancels := make([]func(), 0, len(t.termSubs))
	for _, c := range t.termSubs {
		cancels = append(cancels, c)
	}
	t.termSubs = make(map[libacp.SessionID]func())
	t.termSubMu.Unlock()
	for _, c := range cancels {
		c()
	}
}

func (t *Transport) sendTerminalChunk(sid libacp.SessionID, c shellsession.Chunk) {
	if c.Data == "" && !c.Reset {
		return
	}
	payload := terminalOutputPayload{
		SessionID: string(sid),
		Offset:    c.Offset,
		Chunk:     c.Data,
		Reset:     c.Reset,
	}
	meta := mustJSON(map[string]any{TerminalOutputMetaKey: payload})
	t.sendUpdate(context.Background(), libacp.SessionNotification{
		SessionID: sid,
		Update: libacp.SessionUpdate{
			SessionUpdate: TerminalOutputUpdateKind,
			Meta:          meta,
		},
	})
}

const ExtMethodEnvelopesList = "_contenox/envelopes/list"

type envelopeListing struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (t *Transport) handleEnvelopesList() (json.RawMessage, *libacp.Error) {
	cfg, err := agentdecl.Shipped()
	if err != nil {
		return nil, libacp.InternalError(err.Error())
	}
	if home, homeErr := homedir.Dir(); homeErr == nil {
		if root, rootErr := agentdecl.LocalRoot(home); rootErr == nil {
			if layered, loadErr := agentdecl.Load(context.Background(), root); loadErr == nil {
				cfg = layered
			}
		}
	}
	names := cfg.EnvelopeNames()
	out := make([]envelopeListing, 0, len(names))
	for _, name := range names {
		env, err := cfg.ResolveEnvelope(name)
		if err != nil {
			return nil, libacp.InternalError(err.Error())
		}
		out = append(out, envelopeListing{Name: name, Description: env.Description})
	}
	raw, err := json.Marshal(map[string]any{"envelopes": out})
	if err != nil {
		return nil, libacp.InternalError(err.Error())
	}
	return raw, nil
}

// ExtMethodAgentProblems reports what the last declaration sync could not do:
// a refused or ignored file, or a field this build does not carry. The app shows
// it instead of leaving it in the log.
const ExtMethodAgentProblems = "_contenox/agents/problems"

func (t *Transport) handleAgentProblems() (json.RawMessage, *libacp.Error) {
	problems := agentdecl.LastProblems()
	if problems == nil {
		problems = []agentdecl.Problem{}
	}
	payload := map[string]any{"problems": problems}
	if t.deps.ContenoxDir != "" {
		payload["dir"] = agentdecl.AgentsDir(t.deps.ContenoxDir)
		files, err := agentdecl.AgentFiles(t.deps.ContenoxDir)
		if err != nil {
			return nil, libacp.InternalError(err.Error())
		}
		if files == nil {
			files = []agentdecl.AgentFileStatus{}
		}
		payload["files"] = files
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, libacp.InternalError(err.Error())
	}
	return raw, nil
}
