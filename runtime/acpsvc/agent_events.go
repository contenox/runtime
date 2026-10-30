package acpsvc

import "sync"

// ExtNotifyAgentDeclarationsChanged is pushed to every connected client after a
// compile pass: the folder's files or their problems changed, so the app should
// re-read `_contenox/agents/problems`.
const ExtNotifyAgentDeclarationsChanged = "_contenox/agents/changed"

var (
	agentListenerMu sync.Mutex
	agentListeners  = map[*Transport]struct{}{}
)

// listenAgentDeclarations registers this transport for change notifications and
// returns the unregister func, mirroring watchBackendState's lifetime.
func (t *Transport) listenAgentDeclarations() func() {
	agentListenerMu.Lock()
	agentListeners[t] = struct{}{}
	agentListenerMu.Unlock()
	return func() {
		agentListenerMu.Lock()
		delete(agentListeners, t)
		agentListenerMu.Unlock()
	}
}

// NotifyAgentDeclarationsChanged tells every connected client that the
// declaration folder was recompiled.
func NotifyAgentDeclarationsChanged() {
	agentListenerMu.Lock()
	list := make([]*Transport, 0, len(agentListeners))
	for t := range agentListeners {
		list = append(list, t)
	}
	agentListenerMu.Unlock()
	for _, t := range list {
		if t.conn == nil {
			continue
		}
		_ = t.conn.SendExtNotification(ExtNotifyAgentDeclarationsChanged, nil)
	}
}
