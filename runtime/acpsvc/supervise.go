package acpsvc

import (
	"context"
	"fmt"

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
