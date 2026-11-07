package acpsvc

import (
	"context"
	"encoding/json"
	"strings"

	libacp "github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/connectorservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

const (
	ExtMethodSessionConnectors       = "_contenox/session/connectors"
	ExtMethodSessionConnectorsSet    = "_contenox/session/connectors/set"
	ExtMethodSessionConnectorsAdd    = "_contenox/session/connectors/add"
	ExtMethodSessionConnectorsRemove = "_contenox/session/connectors/remove"
	ExtMethodSessionConnectorsTool   = "_contenox/session/connectors/tools/set"
)

type sessionConnectorsParams struct {
	SessionID string            `json:"sessionId"`
	Name      string            `json:"name,omitempty"`
	Enabled   *bool             `json:"enabled,omitempty"`
	Tool      string            `json:"tool,omitempty"`
	Server    *libacp.McpServer `json:"server,omitempty"`
}

type sessionConnectorsPayload struct {
	Connectors []connectorservice.Connector `json:"connectors"`
}

func (t *Transport) sessionConnectorsRequest(params json.RawMessage) (sessionConnectorsParams, *sessionEntry, *libacp.Error) {
	var p sessionConnectorsParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return p, nil, libacp.NewErrorf(libacp.ErrInvalidParams, "invalid params: %v", err)
		}
	}
	if strings.TrimSpace(p.SessionID) == "" {
		return p, nil, libacp.NewError(libacp.ErrInvalidParams, "sessionId is required")
	}
	if t.deps.DB == nil || t.deps.Connectors == nil {
		return p, nil, libacp.NewError(libacp.ErrMethodNotFound, "connectors are not enabled on this server")
	}
	entry, ok := t.sessionFor(libacp.SessionID(p.SessionID))
	if !ok {
		return p, nil, libacp.NewErrorf(libacp.ErrInvalidParams, "session %q is not open", p.SessionID)
	}
	return p, entry, nil
}

func (t *Transport) sessionConnectors(ctx context.Context, entry *sessionEntry) (json.RawMessage, *libacp.Error) {
	entry.mu.Lock()
	cwd, internalID := entry.Cwd, entry.InternalSessionID
	names := append([]string(nil), entry.McpServerNames...)
	entry.mu.Unlock()
	list, err := t.deps.Connectors.List(ctx, connectorservice.Filter{Cwd: cwd, SessionNames: names, Session: true, SessionID: internalID})
	if err != nil {
		return nil, libacp.InternalError(err.Error())
	}
	off := map[string]struct{}{}
	for _, n := range connectorservice.Disabled(ctx, runtimetypes.New(t.deps.DB.WithoutTransaction()), internalID) {
		off[n] = struct{}{}
	}
	for i := range list {
		_, disabled := off[list[i].Name]
		enabled := !disabled
		list[i].Enabled = &enabled
	}
	if list == nil {
		list = []connectorservice.Connector{}
	}
	return mustJSON(sessionConnectorsPayload{Connectors: list}), nil
}

func (t *Transport) handleSessionConnectors(ctx context.Context, params json.RawMessage) (json.RawMessage, *libacp.Error) {
	_, entry, aerr := t.sessionConnectorsRequest(params)
	if aerr != nil {
		return nil, aerr
	}
	return t.sessionConnectors(ctx, entry)
}

func (t *Transport) handleSessionConnectorsSet(ctx context.Context, params json.RawMessage) (json.RawMessage, *libacp.Error) {
	p, entry, aerr := t.sessionConnectorsRequest(params)
	if aerr != nil {
		return nil, aerr
	}
	if strings.TrimSpace(p.Name) == "" || p.Enabled == nil {
		return nil, libacp.NewError(libacp.ErrInvalidParams, "name and enabled are required")
	}
	entry.mu.Lock()
	internalID := entry.InternalSessionID
	entry.mu.Unlock()
	if err := connectorservice.SetEnabled(ctx, runtimetypes.New(t.deps.DB.WithoutTransaction()), internalID, p.Name, *p.Enabled); err != nil {
		return nil, libacp.InternalError(err.Error())
	}
	return t.sessionConnectors(ctx, entry)
}

func (t *Transport) handleSessionConnectorsTool(ctx context.Context, params json.RawMessage) (json.RawMessage, *libacp.Error) {
	p, entry, aerr := t.sessionConnectorsRequest(params)
	if aerr != nil {
		return nil, aerr
	}
	if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Tool) == "" || p.Enabled == nil {
		return nil, libacp.NewError(libacp.ErrInvalidParams, "name, tool and enabled are required")
	}
	entry.mu.Lock()
	internalID := entry.InternalSessionID
	entry.mu.Unlock()
	if err := connectorservice.SetSessionToolHidden(ctx, runtimetypes.New(t.deps.DB.WithoutTransaction()), internalID, p.Name, p.Tool, !*p.Enabled); err != nil {
		return nil, libacp.NewError(libacp.ErrInvalidParams, err.Error())
	}
	return t.sessionConnectors(ctx, entry)
}

func (t *Transport) handleSessionConnectorsAdd(ctx context.Context, params json.RawMessage) (json.RawMessage, *libacp.Error) {
	p, entry, aerr := t.sessionConnectorsRequest(params)
	if aerr != nil {
		return nil, aerr
	}
	if p.Server == nil {
		return nil, libacp.NewError(libacp.ErrInvalidParams, "server is required")
	}
	store := runtimetypes.New(t.deps.DB.WithoutTransaction())
	registered, err := t.registerMcpServers(ctx, store, libacp.SessionID(p.SessionID), []libacp.McpServer{*p.Server})
	if err != nil {
		return nil, libacp.NewError(libacp.ErrInvalidParams, err.Error())
	}
	entry.mu.Lock()
	for _, name := range registered {
		if !containsString(entry.McpServerNames, name) {
			entry.McpServerNames = append(entry.McpServerNames, name)
		}
	}
	entry.mu.Unlock()
	return t.sessionConnectors(ctx, entry)
}

func (t *Transport) handleSessionConnectorsRemove(ctx context.Context, params json.RawMessage) (json.RawMessage, *libacp.Error) {
	p, entry, aerr := t.sessionConnectorsRequest(params)
	if aerr != nil {
		return nil, aerr
	}
	entry.mu.Lock()
	owned := containsString(entry.McpServerNames, p.Name)
	if owned {
		kept := entry.McpServerNames[:0:0]
		for _, n := range entry.McpServerNames {
			if n != p.Name {
				kept = append(kept, n)
			}
		}
		entry.McpServerNames = kept
	}
	entry.mu.Unlock()
	if !owned {
		return nil, libacp.NewErrorf(libacp.ErrInvalidParams, "%q was not added to this chat", p.Name)
	}
	t.cleanupMcpServers(ctx, runtimetypes.New(t.deps.DB.WithoutTransaction()), []string{p.Name})
	return t.sessionConnectors(ctx, entry)
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
