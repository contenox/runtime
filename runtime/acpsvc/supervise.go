package acpsvc

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"

	libacp "github.com/contenox/runtime/libacp"
)

var ErrSessionBusy = fmt.Errorf("acpsvc: the session already has a turn in flight")

func (t *Transport) PromptContenoxSession(ctx context.Context, contenoxSessionID, text string) error {
	sid, ok := t.acpSessionForContenoxID(contenoxSessionID)
	if !ok {
		return ErrSessionNotLive
	}
	if t.hasInflightPrompt(sid) {
		return ErrSessionBusy
	}
	_, err := t.Prompt(ctx, libacp.PromptRequest{
		SessionID: sid,
		Prompt:    []libacp.ContentBlock{libacp.NewTextContent(text)},
	})
	return err
}

func (t *Transport) hasInflightPrompt(sid libacp.SessionID) bool {
	t.promptCancelMu.Lock()
	defer t.promptCancelMu.Unlock()
	_, running := t.promptCancels[sid]
	return running
}

func (r *SessionRouter) PromptContenoxSession(ctx context.Context, contenoxSessionID, text string) error {
	tr, ok := r.transportFor(contenoxSessionID)
	if !ok {
		return ErrSessionNotLive
	}
	return tr.PromptContenoxSession(ctx, contenoxSessionID, text)
}

type eventTurnPolicy struct{}

func (r *SessionRouter) EventSessions(root string) map[string]string {
	r.mu.RLock()
	bindings := maps.Clone(r.bindings)
	r.mu.RUnlock()
	out := map[string]string{}
	for id, t := range bindings {
		sid, ok := t.acpSessionForContenoxID(id)
		if !ok {
			continue
		}
		entry, ok := t.sessionFor(sid)
		if !ok || t.deps.Events == nil {
			continue
		}
		if _, ok := entry.driver.(*nativeDriver); !ok {
			continue
		}
		granted, err := t.deps.Events.GrantedRoot(entry.Cwd)
		if err == nil && granted == root {
			out[string(sid)] = id
		}
	}
	return out
}
func (r *SessionRouter) PromptWorkspaceEvent(ctx context.Context, id, root, mode, policy, text string) error {
	t, ok := r.transportFor(id)
	if !ok {
		return ErrSessionNotLive
	}
	sid, ok := t.acpSessionForContenoxID(id)
	if !ok {
		return ErrSessionNotLive
	}
	entry, ok := t.sessionFor(sid)
	if !ok {
		return ErrSessionNotLive
	}
	if t.deps.Events == nil {
		return ErrSessionNotLive
	}
	granted, err := t.deps.Events.GrantedRoot(entry.Cwd)
	if err != nil || filepath.Clean(granted) != filepath.Clean(root) {
		return fmt.Errorf("the target session belongs to another project")
	}
	if _, ok := entry.driver.(*nativeDriver); !ok || t.deps.NativeTurns == nil {
		return fmt.Errorf("target requires a live native session")
	}
	if mode == "new" {
		created, err := t.NewSession(ctx, libacp.NewSessionRequest{Cwd: entry.Cwd})
		if err != nil {
			return err
		}
		sid = created.SessionID
	}
	ctx = context.WithValue(ctx, eventTurnPolicy{}, t.scopedPolicy(t.sessionWorkspaceID(entry), policy))
	resp, err := t.Prompt(ctx, libacp.PromptRequest{SessionID: sid, Prompt: []libacp.ContentBlock{libacp.NewTextContent("Workspace event\n" + text)}})
	if err != nil {
		return err
	}
	if resp.StopReason != libacp.StopReasonEndTurn {
		return fmt.Errorf("session stopped: %s", resp.StopReason)
	}
	return nil
}
