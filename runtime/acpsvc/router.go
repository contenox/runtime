package acpsvc

import (
	"context"
	"errors"
	"sync"

	"github.com/contenox/runtime/libacp"

	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

var ErrNoBoundSession = errors.New("acpsvc: no ACP transport bound to contenox session")

type SessionRouter struct {
	mu       sync.RWMutex
	bindings map[string]*Transport
}

func NewSessionRouter() *SessionRouter {
	return &SessionRouter{bindings: make(map[string]*Transport)}
}

func (r *SessionRouter) bind(contenoxSessionID string, t *Transport) {
	if r == nil || contenoxSessionID == "" || t == nil {
		return
	}
	r.mu.Lock()
	r.bindings[contenoxSessionID] = t
	r.mu.Unlock()
}

func (r *SessionRouter) unbind(contenoxSessionID string, t *Transport) {
	if r == nil || contenoxSessionID == "" {
		return
	}
	r.mu.Lock()
	if r.bindings[contenoxSessionID] == t {
		delete(r.bindings, contenoxSessionID)
	}
	r.mu.Unlock()
}

func (r *SessionRouter) transportFor(contenoxSessionID string) (*Transport, bool) {
	if r == nil || contenoxSessionID == "" {
		return nil, false
	}
	r.mu.RLock()
	t, ok := r.bindings[contenoxSessionID]
	r.mu.RUnlock()
	return t, ok
}

func (r *SessionRouter) AskApproval(ctx context.Context, req hitlservice.ApprovalRequest) (bool, error) {
	contenoxSessionID, _ := ctx.Value(runtimetypes.SessionIDContextKey).(string)
	t, ok := r.transportFor(contenoxSessionID)
	if !ok {
		return false, ErrNoBoundSession
	}
	return t.AskApproval(ctx, req)
}

func (r *SessionRouter) DeliverToContenoxSession(ctx context.Context, contenoxSessionID string, n libacp.SessionNotification) error {
	t, ok := r.transportFor(contenoxSessionID)
	if !ok {
		return ErrSessionNotLive
	}
	return t.DeliverToContenoxSession(ctx, contenoxSessionID, n)
}
