package acpsvc

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	libacp "github.com/contenox/runtime/libacp"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/agentinstance"
	"github.com/contenox/runtime/runtime/enginesvc"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/nativeturn"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/shellsession"
	"github.com/contenox/runtime/runtime/statetype"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/contenox/runtime/runtime/vfs"
)

type Deps struct {
	Engine             *enginesvc.Engine
	DB                 libdb.DBManager
	ChainRegistry      *ChainRegistry
	DefaultModel       string
	DefaultProvider    string
	DefaultAltModel    string
	DefaultAltProvider string
	DefaultMaxTokens   string
	DefaultThink       string

	// LiveDefaults, when set, is consulted (TTL-cached) so defaults saved by
	// another process or the HTTP API take effect without a restart. Non-empty
	// fields override the static Default* values above, which remain the fallback.
	LiveDefaults func(ctx context.Context) LiveDefaults

	WorkspaceID string
	ContenoxDir string

	WorkspaceRoots *vfs.Factory

	ShellSessions shellsession.Manager

	KnownPolicies         []string
	HITLDefaultPolicyName string

	UpdateBanner string

	EnvSetup *EnvSetupSpec

	SessionRouter *SessionRouter

	// DecisionRecorder, when set, receives every interactive approval decision
	// answered through session/request_permission, so hitl_approvals keeps an
	// audit row for it. Optional: nil disables recording.
	DecisionRecorder hitlservice.DecisionRecorder

	Instances agentinstance.Manager

	NativeTurns *nativeturn.Registry

	Fleet MissionDispatcher

	Agents MissionAgentResolver

	MissionForwarded *MissionForwardConfig
}

// LiveDefaults is a snapshot of the persisted default-* configuration.
// Empty fields mean "not configured" and fall back to the startup values.
type LiveDefaults struct {
	Model       string
	Provider    string
	AltModel    string
	AltProvider string
	MaxTokens   string
	Think       string
}

// defaultsTTL bounds how often the live defaults hit the DB.
const defaultsTTL = 2 * time.Second

type MissionForwardConfig struct {
	Reachable func() bool
	TargetURL func() string
}

type EnvSetupSpec struct {
	Vars     []libacp.AuthEnvVar
	Complete func(ctx context.Context) error
}

type sessionEntry struct {
	mu                  sync.Mutex
	WorkspaceID         string
	Cwd                 string
	InternalSessionID   string
	McpServerNames      []string
	Provider            string
	Model               string
	Think               string
	EffectiveTokenLimit int
	HITLPolicy          string

	MissionID string

	FiredMissions bool

	driver sessionDriver
}

type sessionDriver interface {
	Prompt(ctx context.Context, req libacp.PromptRequest, sess *sessionEntry) (libacp.PromptResponse, error)
	ConfigOptions(ctx context.Context, sess *sessionEntry) []libacp.SessionConfigOption
	SetConfigOption(ctx context.Context, sess *sessionEntry, configID string, value libacp.SessionConfigOptionValue) error
	AvailableCommands() []libacp.AvailableCommand
	AgentName() string
	Close() error
}

type Transport struct {
	deps         Deps
	conn         *libacp.AgentSideConnection
	connectionID string

	connCtx    context.Context
	connCancel context.CancelFunc

	initMu     sync.Mutex
	clientInfo *libacp.Implementation
	clientCaps libacp.ClientCapabilities

	sessionMu sync.Mutex
	// statesFn overrides the engine backend state source (tests).
	statesFn        func() []statetype.BackendRuntimeState
	sessions        map[libacp.SessionID]*sessionEntry
	contenoxToACPID map[string]libacp.SessionID

	cfgMu              sync.Mutex
	defaultModel       string
	defaultProvider    string
	defaultAltModel    string
	defaultAltProvider string
	defaultMaxTokens   string
	defaultThink       string
	defaultsAt         time.Time

	permMu      sync.Mutex
	permPending map[string]struct{}

	toolCallMu     sync.Mutex
	toolCallStatus map[string]libacp.ToolCallStatus
	toolCallSeq    map[string]int
	toolCallOpen   map[string]int

	bannerMu      sync.Mutex
	pendingBanner string

	termSubMu sync.Mutex
	termSubs  map[libacp.SessionID]func()

	promptCancelMu sync.Mutex
	promptCancels  map[libacp.SessionID]*inflightPrompt
}

type inflightPrompt struct {
	cancel context.CancelFunc
}

func permKey(sid libacp.SessionID, toolCallID string) string {
	return string(sid) + "\x00" + toolCallID
}

func (t *Transport) markPermissionPending(sid libacp.SessionID, toolCallID string) {
	t.permMu.Lock()
	if t.permPending == nil {
		t.permPending = make(map[string]struct{})
	}
	t.permPending[permKey(sid, toolCallID)] = struct{}{}
	t.permMu.Unlock()
}

func (t *Transport) clearPermissionPending(sid libacp.SessionID, toolCallID string) {
	t.permMu.Lock()
	delete(t.permPending, permKey(sid, toolCallID))
	t.permMu.Unlock()
}

func (t *Transport) sendToolCallUpdateGuarded(ctx context.Context, sid libacp.SessionID, toolCallID string, notif libacp.SessionNotification) {
	t.permMu.Lock()
	defer t.permMu.Unlock()
	if _, pending := t.permPending[permKey(sid, toolCallID)]; pending {
		return
	}
	t.sendUpdate(ctx, notif)
}

func (t *Transport) isPermissionPending(sid libacp.SessionID, toolCallID string) bool {
	t.permMu.Lock()
	defer t.permMu.Unlock()
	_, pending := t.permPending[permKey(sid, toolCallID)]
	return pending
}

func New(deps Deps) libacp.AgentFactory {
	return func(conn *libacp.AgentSideConnection) libacp.Agent {
		connCtx, connCancel := context.WithCancel(context.Background())
		t := &Transport{
			deps:               deps,
			conn:               conn,
			connectionID:       newSessionID("conn"),
			connCtx:            connCtx,
			connCancel:         connCancel,
			sessions:           make(map[libacp.SessionID]*sessionEntry),
			contenoxToACPID:    make(map[string]libacp.SessionID),
			toolCallStatus:     make(map[string]libacp.ToolCallStatus),
			defaultModel:       deps.DefaultModel,
			defaultProvider:    deps.DefaultProvider,
			defaultAltModel:    deps.DefaultAltModel,
			defaultAltProvider: deps.DefaultAltProvider,
			defaultMaxTokens:   deps.DefaultMaxTokens,
			defaultThink:       deps.DefaultThink,
			pendingBanner:      deps.UpdateBanner,
			termSubs:           make(map[libacp.SessionID]func()),
			promptCancels:      make(map[libacp.SessionID]*inflightPrompt),
		}
		conn.SetExtRequestHandler(t.handleExtRequest)
		stopWatch := t.watchBackendState()
		go func() {
			<-conn.Closed()
			stopWatch()
			connCancel()
			t.releaseSessionRouting()
			t.unsubscribeAllTerminals()
		}()
		return t
	}
}

func (t *Transport) takeBanner() string {
	t.bannerMu.Lock()
	defer t.bannerMu.Unlock()
	b := t.pendingBanner
	t.pendingBanner = ""
	return b
}

// refreshDefaults reloads the effective defaults from Deps.LiveDefaults at most
// once per defaultsTTL. The DB config is the source of truth: /model and
// /provider persist before updating memory, so a reload never loses them.
func (t *Transport) refreshDefaults() {
	if t.deps.LiveDefaults == nil {
		return
	}
	t.cfgMu.Lock()
	fresh := !t.defaultsAt.IsZero() && time.Since(t.defaultsAt) < defaultsTTL
	if !fresh {
		// Claim the refresh so concurrent callers don't stampede the DB.
		t.defaultsAt = time.Now()
	}
	t.cfgMu.Unlock()
	if fresh {
		return
	}
	parent := t.connCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	d := t.deps.LiveDefaults(ctx)
	pick := func(live, static string) string {
		if live != "" {
			return live
		}
		return static
	}
	t.cfgMu.Lock()
	defer t.cfgMu.Unlock()
	t.defaultModel = pick(d.Model, t.deps.DefaultModel)
	t.defaultProvider = pick(d.Provider, t.deps.DefaultProvider)
	t.defaultAltModel = pick(d.AltModel, t.deps.DefaultAltModel)
	t.defaultAltProvider = pick(d.AltProvider, t.deps.DefaultAltProvider)
	t.defaultMaxTokens = pick(d.MaxTokens, t.deps.DefaultMaxTokens)
	t.defaultThink = pick(d.Think, t.deps.DefaultThink)
}

// invalidateDefaults forces the next read to reload live defaults.
func (t *Transport) invalidateDefaults() {
	t.cfgMu.Lock()
	t.defaultsAt = time.Time{}
	t.cfgMu.Unlock()
}

func (t *Transport) model() string {
	t.refreshDefaults()
	t.cfgMu.Lock()
	defer t.cfgMu.Unlock()
	return t.defaultModel
}

func (t *Transport) provider() string {
	t.refreshDefaults()
	t.cfgMu.Lock()
	defer t.cfgMu.Unlock()
	return t.defaultProvider
}

func (t *Transport) altModel() string {
	t.refreshDefaults()
	t.cfgMu.Lock()
	defer t.cfgMu.Unlock()
	return t.defaultAltModel
}

func (t *Transport) altProvider() string {
	t.refreshDefaults()
	t.cfgMu.Lock()
	defer t.cfgMu.Unlock()
	return t.defaultAltProvider
}

func (t *Transport) maxTokens() string {
	t.refreshDefaults()
	t.cfgMu.Lock()
	defer t.cfgMu.Unlock()
	return t.defaultMaxTokens
}

func (t *Transport) chainTemplateVars(sess *sessionEntry) map[string]string {
	vars := map[string]string{
		"model":    sess.modelOrDefault(t.model()),
		"provider": sess.providerOrDefault(t.provider()),
	}
	if vars["model"] != "" {
		vars["default_model"] = vars["model"]
	}
	if vars["provider"] != "" {
		vars["default_provider"] = vars["provider"]
	}
	if altModel := t.altModel(); altModel != "" {
		vars["alt_model"] = altModel
	}
	if altProvider := t.altProvider(); altProvider != "" {
		vars["alt_provider"] = altProvider
	}
	if maxTokens := t.maxTokens(); maxTokens != "" {
		vars["max_tokens"] = maxTokens
	}
	// The project folder is the session's, not the runtime process's: one
	// runtime can serve several projects.
	if cwd := strings.TrimSpace(sess.Cwd); cwd != "" {
		vars["cwd"] = cwd
	}
	return vars
}

func (t *Transport) setModel(v string) {
	t.cfgMu.Lock()
	t.defaultModel = v
	t.defaultsAt = time.Time{}
	t.cfgMu.Unlock()
}

func (t *Transport) setProvider(v string) {
	t.cfgMu.Lock()
	t.defaultProvider = v
	t.defaultsAt = time.Time{}
	t.cfgMu.Unlock()
}

func (t *Transport) setMaxTokens(v string) {
	t.cfgMu.Lock()
	t.defaultMaxTokens = v
	t.defaultsAt = time.Time{}
	t.cfgMu.Unlock()
}

func (t *Transport) thinkDefault() string {
	t.refreshDefaults()
	t.cfgMu.Lock()
	defer t.cfgMu.Unlock()
	if t.defaultThink == "" {
		return "high"
	}
	return t.defaultThink
}

func (s *sessionEntry) think() string {
	if s == nil {
		return "high"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Think == "" {
		return "high"
	}
	return s.Think
}

func (s *sessionEntry) setThink(v string) {
	s.mu.Lock()
	s.Think = v
	s.mu.Unlock()
}

func (s *sessionEntry) hitlPolicy() string {
	if s == nil {
		return hitlPolicyDefaultValue
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.HITLPolicy == "" {
		return hitlPolicyDefaultValue
	}
	return s.HITLPolicy
}

func (s *sessionEntry) setHITLPolicy(v string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.HITLPolicy = v
	s.mu.Unlock()
}

func (s *sessionEntry) providerOrDefault(defaultProvider string) string {
	if s == nil {
		return defaultProvider
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Provider == "" {
		return defaultProvider
	}
	return s.Provider
}

func (s *sessionEntry) modelOrDefault(defaultModel string) string {
	if s == nil {
		return defaultModel
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Model == "" {
		return defaultModel
	}
	return s.Model
}

func (s *sessionEntry) effectiveTokenLimit() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.EffectiveTokenLimit
}

func (s *sessionEntry) setEffectiveTokenLimit(v int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.EffectiveTokenLimit = v
}

func (s *sessionEntry) setModelSelection(provider, model string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.Provider = provider
	s.Model = model
	s.mu.Unlock()
}

func (t *Transport) acpSessionForContenoxID(contenoxSessionID string) (libacp.SessionID, bool) {
	t.sessionMu.Lock()
	defer t.sessionMu.Unlock()
	sid, ok := t.contenoxToACPID[contenoxSessionID]
	return sid, ok
}

func (t *Transport) bindContenoxSession(contenoxSessionID string, sid libacp.SessionID) {
	t.contenoxToACPID[contenoxSessionID] = sid
	t.deps.SessionRouter.bind(contenoxSessionID, t)
}

func (t *Transport) unbindContenoxSession(contenoxSessionID string) {
	delete(t.contenoxToACPID, contenoxSessionID)
	t.deps.SessionRouter.unbind(contenoxSessionID, t)
}

func (t *Transport) releaseSessionRouting() {
	if t.deps.SessionRouter == nil {
		return
	}
	t.sessionMu.Lock()
	defer t.sessionMu.Unlock()
	for _, e := range t.sessions {
		t.deps.SessionRouter.unbind(e.InternalSessionID, t)
	}
	for contenoxSessionID := range t.contenoxToACPID {
		t.deps.SessionRouter.unbind(contenoxSessionID, t)
	}
}

func (t *Transport) registerPromptCancel(sid libacp.SessionID, cancel context.CancelFunc) *inflightPrompt {
	reg := &inflightPrompt{cancel: cancel}
	t.promptCancelMu.Lock()
	if t.promptCancels == nil {
		t.promptCancels = make(map[libacp.SessionID]*inflightPrompt)
	}
	prev := t.promptCancels[sid]
	t.promptCancels[sid] = reg
	t.promptCancelMu.Unlock()
	if prev != nil {
		prev.cancel()
	}
	return reg
}

func (t *Transport) unregisterPromptCancel(sid libacp.SessionID, reg *inflightPrompt) {
	t.promptCancelMu.Lock()
	if cur, ok := t.promptCancels[sid]; ok && cur == reg {
		delete(t.promptCancels, sid)
	}
	t.promptCancelMu.Unlock()
}

func (t *Transport) cancelInflightPrompt(sid libacp.SessionID) bool {
	t.promptCancelMu.Lock()
	reg, ok := t.promptCancels[sid]
	t.promptCancelMu.Unlock()
	if !ok {
		return false
	}
	reg.cancel()
	return true
}

func (t *Transport) Cancel(ctx context.Context, req libacp.CancelNotification) error {
	_, reportChange, end := t.tracker().Start(ctx, "cancel", "acp_session", "session_id", string(req.SessionID))
	defer end()
	cancelled := t.cancelInflightPrompt(req.SessionID)
	if t.deps.NativeTurns != nil && t.deps.NativeTurns.Cancel(req.SessionID) {
		cancelled = true
	}
	reportChange(string(req.SessionID), map[string]any{"cancelled_inflight": cancelled})
	return nil
}

func (t *Transport) clientIdentity() *libacp.Implementation {
	t.initMu.Lock()
	defer t.initMu.Unlock()
	return t.clientInfo
}

func (t *Transport) getClientCaps() libacp.ClientCapabilities {
	t.initMu.Lock()
	defer t.initMu.Unlock()
	return t.clientCaps
}

func (t *Transport) workspaceID() string {
	return t.deps.WorkspaceID
}

func (t *Transport) sendUpdate(ctx context.Context, notif libacp.SessionNotification) {
	if t.conn == nil {
		return
	}
	notif = t.normalizeToolCallNotification(notif)
	kind := string(notif.Update.SessionUpdate)
	kv := []any{"kind", kind, "session_id", string(notif.SessionID)}
	if notif.Update.ToolCallID != "" {
		kv = append(kv, "tool_call_id", notif.Update.ToolCallID)
	}
	if notif.Update.Status != "" {
		kv = append(kv, "status", string(notif.Update.Status))
	}
	reportErr, _, end := t.tracker().Start(ctx, "send", "acp_session_update", kv...)
	defer end()
	if err := t.conn.SessionUpdate(notif); err != nil {
		reportErr(err)
	}
}

func (t *Transport) normalizeToolCallNotification(notif libacp.SessionNotification) libacp.SessionNotification {
	upd := &notif.Update
	if upd.ToolCallID == "" {
		return notif
	}
	if upd.SessionUpdate != libacp.SessionUpdateToolCall && upd.SessionUpdate != libacp.SessionUpdateToolCallUpdate {
		return notif
	}

	t.toolCallMu.Lock()
	defer t.toolCallMu.Unlock()
	if t.toolCallStatus == nil {
		t.toolCallStatus = make(map[string]libacp.ToolCallStatus)
	}

	key := permKey(notif.SessionID, upd.ToolCallID)
	previousStatus, seen := t.toolCallStatus[key]
	if upd.SessionUpdate == libacp.SessionUpdateToolCallUpdate && !seen {
		upd.SessionUpdate = libacp.SessionUpdateToolCall
		if upd.Title == "" {
			upd.Title = upd.ToolCallID
		}
		if upd.Kind == "" {
			upd.Kind = libacp.ToolKindOther
		}
	}

	if seen && toolCallStatusRank(upd.Status) < toolCallStatusRank(previousStatus) {
		upd.Status = previousStatus
	}
	t.toolCallStatus[key] = upd.Status
	return notif
}

func (t *Transport) clearToolCallState(sid libacp.SessionID) {
	t.toolCallMu.Lock()
	defer t.toolCallMu.Unlock()
	prefix := string(sid) + "\x00"
	for key := range t.toolCallStatus {
		if strings.HasPrefix(key, prefix) {
			delete(t.toolCallStatus, key)
		}
	}
	for key := range t.toolCallSeq {
		if strings.HasPrefix(key, prefix) {
			delete(t.toolCallSeq, key)
		}
	}
	for key := range t.toolCallOpen {
		if strings.HasPrefix(key, prefix) {
			delete(t.toolCallOpen, key)
		}
	}
}

func (t *Transport) toolCallWireID(sid libacp.SessionID, ev taskengine.TaskEvent, closes bool) string {
	t.toolCallMu.Lock()
	defer t.toolCallMu.Unlock()
	if t.toolCallSeq == nil {
		t.toolCallSeq = make(map[string]int)
	}
	if t.toolCallOpen == nil {
		t.toolCallOpen = make(map[string]int)
	}
	return resolveToolCallWireID(t.toolCallSeq, t.toolCallOpen, sid, ev, closes)
}

func resolveToolCallWireID(seq, open map[string]int, sid libacp.SessionID, ev taskengine.TaskEvent, closes bool) string {
	if ev.ApprovalID != "" {
		return ev.ApprovalID
	}
	base := fallbackToolCallID(ev)
	if base == "" {
		return ""
	}
	key := permKey(sid, ev.TaskID+"\x1f"+base)
	if closes {
		if n, ok := open[key]; ok {
			delete(open, key)
			return invocationToolCallID(base, n)
		}
		seq[key]++
		return invocationToolCallID(base, seq[key])
	}
	seq[key]++
	open[key] = seq[key]
	return invocationToolCallID(base, seq[key])
}

func invocationToolCallID(base string, n int) string {
	if n <= 1 {
		return base
	}
	return base + "#" + strconv.Itoa(n)
}

func toolCallStatusRank(status libacp.ToolCallStatus) int {
	switch status {
	case libacp.ToolCallStatusPending:
		return 1
	case libacp.ToolCallStatusInProgress:
		return 2
	case libacp.ToolCallStatusCompleted, libacp.ToolCallStatusFailed:
		return 3
	default:
		return 0
	}
}

func (t *Transport) tracker() libtracker.ActivityTracker {
	if t.deps.Engine != nil && t.deps.Engine.Tracker != nil {
		return t.deps.Engine.Tracker
	}
	return libtracker.NoopTracker{}
}

func ReadConfigValue(ctx context.Context, db libdb.DBManager, key string) string {
	store := runtimetypes.New(db.WithoutTransaction())
	return strings.TrimSpace(clikv.Read(ctx, store, key))
}

func resolveACPSessionID(ctx context.Context, t *Transport) libacp.SessionID {
	contenoxSessionID := sessionIDFromCtx(ctx)
	if contenoxSessionID == "" {
		return ""
	}
	acpSID, _ := t.acpSessionForContenoxID(contenoxSessionID)
	return acpSID
}

func sessionIDFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(runtimetypes.SessionIDContextKey).(string)
	return v
}

func (t *Transport) sendInitialUsageUpdate(ctx context.Context, sid libacp.SessionID) {
	t.sessionMu.Lock()
	sess, hasSess := t.sessions[sid]
	t.sessionMu.Unlock()

	if hasSess && sess != nil {
		if eff := sess.effectiveTokenLimit(); eff > 0 {
			t.sendUpdate(ctx, libacp.SessionNotification{
				SessionID: sid,
				Update: libacp.SessionUpdate{
					SessionUpdate: libacp.SessionUpdateUsageUpdate,
					Size:          eff,
				},
			})
			return
		}
	}

	preferredModel := t.model()
	t.sessionMu.Lock()
	if entry, ok := t.sessions[sid]; ok && entry != nil {
		preferredModel = entry.modelOrDefault(t.model())
	}
	t.sessionMu.Unlock()

	for _, state := range t.runtimeStates(ctx) {
		for _, pulled := range state.PulledModels {
			if preferredModel != "" && pulled.Model == preferredModel && pulled.ContextLength > 0 {
				t.sendUpdate(ctx, libacp.SessionNotification{
					SessionID: sid,
					Update: libacp.SessionUpdate{
						SessionUpdate: libacp.SessionUpdateUsageUpdate,
						Size:          pulled.ContextLength,
					},
				})
				return
			}
		}
	}
	for _, state := range t.runtimeStates(ctx) {
		for _, pulled := range state.PulledModels {
			if pulled.ContextLength > 0 && (pulled.CanChat || pulled.CanPrompt) {
				t.sendUpdate(ctx, libacp.SessionNotification{
					SessionID: sid,
					Update: libacp.SessionUpdate{
						SessionUpdate: libacp.SessionUpdateUsageUpdate,
						Size:          pulled.ContextLength,
					},
				})
				return
			}
		}
	}
}
