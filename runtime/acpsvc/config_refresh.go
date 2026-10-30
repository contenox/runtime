package acpsvc

import (
	"context"
	"encoding/json"
	"time"

	libacp "github.com/contenox/runtime/libacp"
)

const (
	// ExtMethodWorkspaceConfigOptions returns the current session-less config
	// options: result {"configOptions": [...]}.
	ExtMethodWorkspaceConfigOptions = "_contenox/workspaceConfigOptions"
	// ExtNotifyWorkspaceConfigOptionsChanged is pushed (no response) whenever
	// backend state changes: params {"configOptions": [...]}.
	ExtNotifyWorkspaceConfigOptionsChanged = "_contenox/workspaceConfigOptionsChanged"

	configRefreshDebounce = 300 * time.Millisecond
)

type workspaceConfigOptionsPayload struct {
	ConfigOptions []libacp.SessionConfigOption `json:"configOptions"`
}

func (t *Transport) handleWorkspaceConfigOptions(ctx context.Context) (json.RawMessage, *libacp.Error) {
	if t.deps.Engine == nil {
		return mustJSON(workspaceConfigOptionsPayload{ConfigOptions: []libacp.SessionConfigOption{}}), nil
	}
	return mustJSON(workspaceConfigOptionsPayload{ConfigOptions: t.workspaceConfigOptions(ctx)}), nil
}

// watchBackendState re-sends config options (to every live session and, as an
// extension notification, to session-less clients) when backend state
// changes: login/logout, connection add/delete, or a reconcile that changed
// the served models. Bursts are debounced. The returned func stops watching.
func (t *Transport) watchBackendState() func() {
	if t.deps.Engine == nil || t.deps.Engine.State == nil {
		return func() {}
	}
	var timer *time.Timer
	fire := func() {
		ctx := t.connCtx
		if ctx == nil || ctx.Err() != nil {
			return
		}
		t.invalidateDefaults()
		if t.conn != nil {
			_ = t.conn.SendExtNotification(ExtNotifyWorkspaceConfigOptionsChanged,
				mustJSON(workspaceConfigOptionsPayload{ConfigOptions: t.workspaceConfigOptions(ctx)}))
		}
		t.sessionMu.Lock()
		live := make(map[libacp.SessionID]*sessionEntry, len(t.sessions))
		for sid, sess := range t.sessions {
			live[sid] = sess
		}
		t.sessionMu.Unlock()
		for sid, sess := range live {
			if sess != nil && sess.driver != nil {
				t.sendConfigOptionUpdate(ctx, sid, sess)
			}
		}
	}
	var mu chan struct{} = make(chan struct{}, 1)
	mu <- struct{}{}
	stop, err := t.deps.Engine.State.Observe(func() {
		<-mu
		defer func() { mu <- struct{}{} }()
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(configRefreshDebounce, fire)
	})
	if err != nil {
		return func() {}
	}
	return func() {
		stop()
		<-mu
		if timer != nil {
			timer.Stop()
		}
		mu <- struct{}{}
	}
}
