package mcpworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/localtools"
	"github.com/contenox/runtime/runtime/localtools/mcpoauth"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

func SubjectExecute(name string) string { return "mcp." + name + ".execute" }

func SubjectListTools(name string) string { return "mcp." + name + ".list-tools" }

const (
	SubjectCreated = "mcp.servers.created"
	SubjectDeleted = "mcp.servers.deleted"

	listToolsFailureCooldown = 30 * time.Second
)

type MCPToolRequest struct {
	SessionID string         `json:"session_id,omitempty"`
	Tool      string         `json:"tool"`
	Args      map[string]any `json:"args"`
}

type MCPToolReply struct {
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

type MCPToolListReply struct {
	Tools []runtimetypes.MCPTool `json:"tools"`
	Error string                 `json:"error,omitempty"`
}

type MCPDeletedEvent struct {
	Name string `json:"name"`
}

type poolEntry struct {
	pool       *localtools.MCPSessionPool
	lastAccess time.Time
}

type worker struct {
	serverName             string
	cfg                    localtools.MCPServerConfig
	pools                  map[string]*poolEntry
	mu                     sync.Mutex
	cancelFn               context.CancelFunc
	listToolsCooldownUntil time.Time
	listToolsLastError     string
}

type Manager struct {
	mu        sync.Mutex
	workers   map[string]*worker
	db        runtimetypes.Store
	messenger libbus.Messenger
	tracker   libtracker.ActivityTracker
	rootCtx   context.Context
}

func New(ctx context.Context, db runtimetypes.Store, messenger libbus.Messenger, tracker libtracker.ActivityTracker) (*Manager, error) {
	if tracker == nil {
		tracker = libtracker.NoopTracker{}
	}
	_, report, end := tracker.Start(ctx, "new", "mcp_manager")
	defer end()

	m := &Manager{
		workers:   make(map[string]*worker),
		db:        db,
		messenger: messenger,
		tracker:   tracker,
		rootCtx:   ctx,
	}

	servers, err := db.ListMCPServers(ctx, nil, 1000)
	if err != nil {
		return nil, fmt.Errorf("mcpworker: load servers from DB: %w", err)
	}
	for _, srv := range servers {
		if runtimetypes.IsACPManagedMCPServerName(srv.Name) {
			report("skip_acp_scoped", map[string]any{"name": srv.Name})
			continue
		}
		if err := m.StartWorker(ctx, srv); err != nil {
			report("start_failed", map[string]any{"name": srv.Name, "error": err.Error()})
		}
	}
	return m, nil
}

func (m *Manager) getOrCreatePool(ctx context.Context, w *worker, chatSessionID string) *localtools.MCPSessionPool {
	if chatSessionID == "" {
		chatSessionID = "default"
	}

	w.mu.Lock()
	if entry, ok := w.pools[chatSessionID]; ok {
		entry.lastAccess = time.Now().UTC()
		w.mu.Unlock()
		return entry.pool
	}
	w.mu.Unlock()

	kvKey := fmt.Sprintf("mcp_session:%s:%s", w.serverName, chatSessionID)
	var priorMcpID string
	var raw json.RawMessage
	if err := m.db.GetKV(ctx, kvKey, &raw); err == nil {
		_ = json.Unmarshal(raw, &priorMcpID)
	}

	poolCfg := w.cfg
	poolCfg.MCPSessionID = priorMcpID
	poolCfg.Tracker = m.tracker
	poolCfg.OnSessionID = func(newID string) {
		go func(id string) {
			pctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if id == "" {
				_ = m.db.DeleteKV(pctx, kvKey)
			} else {
				b, _ := json.Marshal(id)
				_ = m.db.SetKV(pctx, kvKey, b)
			}
		}(newID)
	}

	pool := localtools.NewMCPSessionPool(poolCfg)

	w.mu.Lock()
	defer w.mu.Unlock()
	if existing, ok := w.pools[chatSessionID]; ok {
		return existing.pool
	}
	w.pools[chatSessionID] = &poolEntry{pool: pool, lastAccess: time.Now().UTC()}
	return pool
}

func (w *worker) listToolsCooldownError(now time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.listToolsCooldownUntil.IsZero() || !now.Before(w.listToolsCooldownUntil) {
		return nil
	}
	return fmt.Errorf("mcp %q: list-tools skipped; provider is cooling down until %s after previous failure: %s",
		w.serverName,
		w.listToolsCooldownUntil.Format(time.RFC3339),
		w.listToolsLastError,
	)
}

func (w *worker) markListToolsFailure(err error, now time.Time) {
	if err == nil {
		return
	}
	w.mu.Lock()
	w.listToolsLastError = err.Error()
	w.listToolsCooldownUntil = now.Add(listToolsFailureCooldown)
	w.mu.Unlock()
}

func (w *worker) clearListToolsFailure() {
	w.mu.Lock()
	w.listToolsLastError = ""
	w.listToolsCooldownUntil = time.Time{}
	w.mu.Unlock()
}

func (m *Manager) StartWorker(ctx context.Context, srv *runtimetypes.MCPServer) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.workers[srv.Name]; ok {
		existing.cancelFn()
		existing.mu.Lock()
		for _, e := range existing.pools {
			_ = e.pool.Close()
		}
		existing.mu.Unlock()
		delete(m.workers, srv.Name)
	}

	root := m.rootCtx
	if root == nil {
		root = context.Background()
	}
	workerCtx, workerCancel := context.WithCancel(root)
	w := &worker{
		serverName: srv.Name,
		cfg:        mcpServerToConfig(srv, m.db),
		pools:      make(map[string]*poolEntry),
		cancelFn:   workerCancel,
	}

	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				w.mu.Lock()
				now := time.Now()
				for sid, entry := range w.pools {
					if now.Sub(entry.lastAccess) > 30*time.Minute {
						_ = entry.pool.Close()
						delete(w.pools, sid)
						_, report, end := m.tracker.Start(workerCtx, "evict", "mcp_pool", "server", srv.Name, "session", sid)
						report("pool_evicted", map[string]any{"server": srv.Name, "session": sid})
						end()
					}
				}
				w.mu.Unlock()
			}
		}
	}()

	if _, err := m.messenger.Serve(workerCtx, SubjectExecute(srv.Name), func(ctx context.Context, data []byte) ([]byte, error) {
		var req MCPToolRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return errorReply(fmt.Errorf("mcpworker: decode request: %w", err))
		}

		pool := m.getOrCreatePool(ctx, w, req.SessionID)
		result, err := pool.CallTool(ctx, req.Tool, req.Args)
		if err != nil {
			return errorReply(err)
		}
		return okReply(result)
	}); err != nil {
		workerCancel()
		return fmt.Errorf("mcpworker: serve execute %q: %w", srv.Name, err)
	}

	if _, err := m.messenger.Serve(workerCtx, SubjectListTools(srv.Name), func(ctx context.Context, data []byte) ([]byte, error) {
		var req MCPToolRequest
		if len(data) > 0 {
			_ = json.Unmarshal(data, &req)
		}

		if cooldownErr := w.listToolsCooldownError(time.Now().UTC()); cooldownErr != nil {
			_, report, end := m.tracker.Start(ctx, "list_tools_skipped", "mcp_server", "name", srv.Name)
			report("cooldown", cooldownErr.Error())
			end()
			return errorListReply(cooldownErr)
		}

		pool := m.getOrCreatePool(ctx, w, req.SessionID)
		tools, err := pool.ListTools(ctx)
		if err != nil {
			w.markListToolsFailure(err, time.Now().UTC())
			return errorListReply(err)
		}
		w.clearListToolsFailure()
		var out []runtimetypes.MCPTool
		for _, t := range tools {
			raw, _ := json.Marshal(t.InputSchema)
			out = append(out, runtimetypes.MCPTool{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: raw,
			})
		}
		return json.Marshal(MCPToolListReply{Tools: out})
	}); err != nil {
		workerCancel()
		return fmt.Errorf("mcpworker: serve list-tools %q: %w", srv.Name, err)
	}

	m.workers[srv.Name] = w
	_, reportWorkerStart, endWorkerStart := m.tracker.Start(ctx, "start", "mcp_worker", "name", srv.Name, "transport", string(srv.Transport))
	reportWorkerStart("worker_started", map[string]any{"name": srv.Name, "transport": string(srv.Transport)})
	endWorkerStart()
	return nil
}

func (m *Manager) StopWorker(ctx context.Context, name string) {
	_, report, end := m.tracker.Start(ctx, "stop", "mcp_worker", "name", name)
	defer end()
	m.mu.Lock()
	defer m.mu.Unlock()
	if w, ok := m.workers[name]; ok {
		w.cancelFn()
		w.mu.Lock()
		for _, e := range w.pools {
			_ = e.pool.Close()
		}
		w.mu.Unlock()
		delete(m.workers, name)
		report("worker_stopped", map[string]any{"name": name})
	}
}

func (m *Manager) StopAll() {
	ctx := m.rootCtx
	_, report, end := m.tracker.Start(ctx, "stop_all", "mcp_manager")
	defer end()
	m.mu.Lock()
	names := make([]string, 0, len(m.workers))
	for name := range m.workers {
		names = append(names, name)
	}
	m.mu.Unlock()
	for _, name := range names {
		m.StopWorker(ctx, name)
	}
	report("all_workers_stopped", map[string]any{"count": len(names)})
}
func (m *Manager) ActiveWorkers() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, 0, len(m.workers))
	for n := range m.workers {
		names = append(names, n)
	}
	return names
}

func (m *Manager) WatchEvents(ctx context.Context) error {
	_, _, end := m.tracker.Start(ctx, "watch_events", "mcp_manager")
	defer end()
	createdCh := make(chan []byte, 8)
	deletedCh := make(chan []byte, 8)

	if _, err := m.messenger.Stream(ctx, SubjectCreated, createdCh); err != nil {
		return fmt.Errorf("mcpworker: watch created: %w", err)
	}
	if _, err := m.messenger.Stream(ctx, SubjectDeleted, deletedCh); err != nil {
		return fmt.Errorf("mcpworker: watch deleted: %w", err)
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case data, ok := <-createdCh:
				if !ok {
					return
				}
				var srv runtimetypes.MCPServer
				if err := json.Unmarshal(data, &srv); err != nil {
					_, report, end := m.tracker.Start(ctx, "watch", "mcp_event", "event", "created")
					report("decode_error", map[string]any{"error": err.Error()})
					end()
					continue
				}
				if err := m.StartWorker(ctx, &srv); err != nil {
					_, report, end := m.tracker.Start(ctx, "watch", "mcp_event", "event", "created", "name", srv.Name)
					report("start_worker_failed", map[string]any{"name": srv.Name, "error": err.Error()})
					end()
				}
			case data, ok := <-deletedCh:
				if !ok {
					return
				}
				var ev MCPDeletedEvent
				if err := json.Unmarshal(data, &ev); err != nil {
					_, report, end := m.tracker.Start(ctx, "watch", "mcp_event", "event", "deleted")
					report("decode_error", map[string]any{"error": err.Error()})
					end()
					continue
				}
				m.StopWorker(ctx, ev.Name)
			}
		}
	}()
	return nil
}

func mcpServerToConfig(srv *runtimetypes.MCPServer, store runtimetypes.Store) localtools.MCPServerConfig {
	cfg := localtools.MCPServerConfig{
		Name:           srv.Name,
		Transport:      localtools.MCPTransport(srv.Transport),
		Command:        srv.Command,
		Args:           srv.Args,
		URL:            srv.URL,
		ConnectTimeout: time.Duration(srv.ConnectTimeoutSeconds) * time.Second,
	}
	if srv.AuthType != "" {
		cfg.Auth = &localtools.MCPAuthConfig{
			Type:          localtools.MCPAuthType(srv.AuthType),
			Token:         srv.AuthToken,
			APIKeyFromEnv: srv.AuthEnvKey,
		}
	}
	if localtools.MCPAuthType(srv.AuthType) == localtools.MCPAuthOAuth && store != nil {
		cfg.OAuth = &localtools.MCPOAuthConfig{
			TokenStore:   mcpoauth.NewKVTokenStore(store),
			ClientID:     srv.OAuthClientID,
			ClientSecret: srv.ResolveOAuthClientSecret(),
		}
	}
	return cfg
}

func okReply(result any) ([]byte, error) {
	return json.Marshal(MCPToolReply{Result: result})
}

func errorReply(err error) ([]byte, error) {
	return json.Marshal(MCPToolReply{Error: err.Error()})
}

func errorListReply(err error) ([]byte, error) {
	return json.Marshal(MCPToolListReply{Error: err.Error()})
}

func DecodeToolReply(data []byte) (any, error) {
	var reply MCPToolReply
	if err := json.Unmarshal(data, &reply); err != nil {
		return nil, fmt.Errorf("mcpworker: decode tool reply: %w", err)
	}
	if reply.Error != "" {
		return nil, errors.New(reply.Error)
	}
	return reply.Result, nil
}

func DecodeListToolsReply(data []byte) ([]runtimetypes.MCPTool, error) {
	var reply MCPToolListReply
	if err := json.Unmarshal(data, &reply); err != nil {
		return nil, fmt.Errorf("mcpworker: decode list-tools reply: %w", err)
	}
	if reply.Error != "" {
		return nil, errors.New(reply.Error)
	}
	return reply.Tools, nil
}
