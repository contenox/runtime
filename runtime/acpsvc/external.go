package acpsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	libacp "github.com/contenox/runtime/libacp"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/agenthost"
	"github.com/contenox/runtime/runtime/agentinstance"
	"github.com/contenox/runtime/runtime/agentregistryservice"
	"github.com/contenox/runtime/runtime/chatservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/contenox/runtime/runtime/version"
)

const AgentMetaKey = "contenox.agent"

const AgentModeConfigOptionID = "contenox.agent-mode"

func syntheticModeOption(m *libacp.SessionModeState) (libacp.SessionConfigOption, bool) {
	if m == nil || len(m.AvailableModes) == 0 {
		return libacp.SessionConfigOption{}, false
	}
	values := make([]libacp.SessionConfigValue, 0, len(m.AvailableModes))
	for _, mode := range m.AvailableModes {
		values = append(values, libacp.SessionConfigValue{
			Value:       mode.ID,
			Name:        mode.Name,
			Description: mode.Description,
		})
	}
	return libacp.SessionConfigOption{
		ID:           AgentModeConfigOptionID,
		Name:         "Mode",
		Type:         libacp.SessionConfigOptionTypeSelect,
		CurrentValue: m.CurrentModeID,
		Options:      libacp.NewSessionConfigValues(values),
	}, true
}

const AgentModelConfigOptionID = "contenox.agent-model"

func syntheticModelOption(m *libacp.SessionModelState) (libacp.SessionConfigOption, bool) {
	if m == nil || len(m.AvailableModels) == 0 {
		return libacp.SessionConfigOption{}, false
	}
	values := make([]libacp.SessionConfigValue, 0, len(m.AvailableModels))
	for _, model := range m.AvailableModels {
		values = append(values, libacp.SessionConfigValue{
			Value:       model.ID,
			Name:        model.Name,
			Description: model.Description,
		})
	}
	return libacp.SessionConfigOption{
		ID:           AgentModelConfigOptionID,
		Name:         "Model",
		Type:         libacp.SessionConfigOptionTypeSelect,
		CurrentValue: m.CurrentModelID,
		Options:      libacp.NewSessionConfigValues(values),
	}, true
}

const externalKillGrace = 2 * time.Second

func parseAgentMeta(meta json.RawMessage) string {
	if len(meta) == 0 {
		return ""
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(meta, &m) != nil {
		return ""
	}
	raw, ok := m[AgentMetaKey]
	if !ok {
		return ""
	}
	var name string
	if json.Unmarshal(raw, &name) != nil {
		return ""
	}
	return strings.TrimSpace(name)
}

func agentMetaJSON(name string) json.RawMessage {
	return mustJSON(map[string]any{AgentMetaKey: name})
}

type sessionAgentRecord struct {
	Agent string `json:"agent"`
}

const acpSessionAgentKVPrefix = "acp:session_agent:"

const (
	acpSessionAgentCommandsKVPrefix      = "acp:session_agent_commands:"
	acpSessionAgentConfigOptionsKVPrefix = "acp:session_agent_configoptions:"
)

func (t *Transport) persistSessionAgent(ctx context.Context, store runtimetypes.Store, sid libacp.SessionID, agentName string) {
	if agentName == "" {
		return
	}
	raw, err := json.Marshal(sessionAgentRecord{Agent: agentName})
	if err != nil {
		return
	}
	if err := store.SetKV(ctx, acpSessionAgentKVPrefix+string(sid), raw); err != nil {
		reportErr, _, end := t.tracker().Start(ctx, "persist_agent", "acp_session", "session_id", string(sid))
		reportErr(err)
		end()
	}
}

func (t *Transport) readSessionAgent(ctx context.Context, store runtimetypes.Store, sid libacp.SessionID) string {
	var rec sessionAgentRecord
	if err := store.GetKV(ctx, acpSessionAgentKVPrefix+string(sid), &rec); err != nil {
		return ""
	}
	return rec.Agent
}

const acpSessionInstanceKVPrefix = "acp:session_instance:"

type sessionInstanceRecord struct {
	InstanceID string `json:"instanceId"`
}

func (t *Transport) persistSessionInstance(ctx context.Context, sid libacp.SessionID, instanceID string) {
	if t.deps.DB == nil || instanceID == "" {
		return
	}
	raw, err := json.Marshal(sessionInstanceRecord{InstanceID: instanceID})
	if err != nil {
		return
	}
	store := runtimetypes.New(t.deps.DB.WithoutTransaction())
	if err := store.SetKV(ctx, acpSessionInstanceKVPrefix+string(sid), raw); err != nil {
		reportErr, _, end := t.tracker().Start(ctx, "persist_instance", "acp_session", "session_id", string(sid))
		reportErr(err)
		end()
	}
}

func (t *Transport) readSessionInstance(ctx context.Context, store runtimetypes.Store, sid libacp.SessionID) string {
	var rec sessionInstanceRecord
	if err := store.GetKV(ctx, acpSessionInstanceKVPrefix+string(sid), &rec); err != nil {
		return ""
	}
	return rec.InstanceID
}

const acpSessionDownstreamKVPrefix = "acp:session_downstream:"

type sessionDownstreamRecord struct {
	DownstreamID string `json:"downstreamId"`
}

func (t *Transport) persistSessionDownstream(ctx context.Context, sid libacp.SessionID, downstreamID libacp.SessionID) {
	if t.deps.DB == nil || downstreamID == "" {
		return
	}
	raw, err := json.Marshal(sessionDownstreamRecord{DownstreamID: string(downstreamID)})
	if err != nil {
		return
	}
	store := runtimetypes.New(t.deps.DB.WithoutTransaction())
	if err := store.SetKV(ctx, acpSessionDownstreamKVPrefix+string(sid), raw); err != nil {
		reportErr, _, end := t.tracker().Start(ctx, "persist_downstream", "acp_session", "session_id", string(sid))
		reportErr(err)
		end()
	}
}

func (t *Transport) readSessionDownstream(ctx context.Context, store runtimetypes.Store, sid libacp.SessionID) libacp.SessionID {
	var rec sessionDownstreamRecord
	if err := store.GetKV(ctx, acpSessionDownstreamKVPrefix+string(sid), &rec); err != nil {
		return ""
	}
	return libacp.SessionID(rec.DownstreamID)
}

func (t *Transport) persistSessionAgentCommands(ctx context.Context, sid libacp.SessionID, cmds []libacp.AvailableCommand) {
	if t.deps.DB == nil {
		return
	}
	raw, err := json.Marshal(cmds)
	if err != nil {
		return
	}
	store := runtimetypes.New(t.deps.DB.WithoutTransaction())
	if err := store.SetKV(ctx, acpSessionAgentCommandsKVPrefix+string(sid), raw); err != nil {
		reportErr, _, end := t.tracker().Start(ctx, "persist_agent_commands", "acp_session", "session_id", string(sid))
		reportErr(err)
		end()
	}
}

func (t *Transport) persistSessionAgentConfigOptions(ctx context.Context, sid libacp.SessionID, opts []libacp.SessionConfigOption) {
	if t.deps.DB == nil {
		return
	}
	raw, err := json.Marshal(opts)
	if err != nil {
		return
	}
	store := runtimetypes.New(t.deps.DB.WithoutTransaction())
	if err := store.SetKV(ctx, acpSessionAgentConfigOptionsKVPrefix+string(sid), raw); err != nil {
		reportErr, _, end := t.tracker().Start(ctx, "persist_agent_config_options", "acp_session", "session_id", string(sid))
		reportErr(err)
		end()
	}
}

func (t *Transport) readSessionAgentCommands(ctx context.Context, store runtimetypes.Store, sid libacp.SessionID) []libacp.AvailableCommand {
	var cmds []libacp.AvailableCommand
	if err := store.GetKV(ctx, acpSessionAgentCommandsKVPrefix+string(sid), &cmds); err != nil {
		return nil
	}
	return cmds
}

func (t *Transport) readSessionAgentConfigOptions(ctx context.Context, store runtimetypes.Store, sid libacp.SessionID) []libacp.SessionConfigOption {
	var opts []libacp.SessionConfigOption
	if err := store.GetKV(ctx, acpSessionAgentConfigOptionsKVPrefix+string(sid), &opts); err != nil {
		return nil
	}
	return opts
}

const acpSessionHITLPolicyKVPrefix = "acp:session_hitl_policy:"

type sessionHITLPolicyRecord struct {
	Policy string `json:"policy"`
}

func (t *Transport) persistSessionHITLPolicy(ctx context.Context, sid libacp.SessionID, policy string) {
	if t.deps.DB == nil {
		return
	}
	raw, err := json.Marshal(sessionHITLPolicyRecord{Policy: policy})
	if err != nil {
		return
	}
	store := runtimetypes.New(t.deps.DB.WithoutTransaction())
	if err := store.SetKV(ctx, acpSessionHITLPolicyKVPrefix+string(sid), raw); err != nil {
		reportErr, _, end := t.tracker().Start(ctx, "persist_hitl_policy", "acp_session", "session_id", string(sid))
		reportErr(err)
		end()
	}
}

func (t *Transport) readSessionHITLPolicy(ctx context.Context, store runtimetypes.Store, sid libacp.SessionID) string {
	var rec sessionHITLPolicyRecord
	if err := store.GetKV(ctx, acpSessionHITLPolicyKVPrefix+string(sid), &rec); err != nil {
		return ""
	}
	return rec.Policy
}

type externalBridge struct {
	libacp.UnimplementedClient

	upstreamID libacp.SessionID

	viewerID string

	relayMu         sync.Mutex
	relayT          *Transport
	relaySuppressed bool
	relayHeld       bool
	relayQueue      []libacp.SessionUpdate
	mgr             agentinstance.Manager
	instanceID      string
	detachOnce      sync.Once

	mu      sync.Mutex
	capture *externalTurnCapture

	downstreamID libacp.SessionID

	bound          bool
	cachedCommands []libacp.AvailableCommand

	configOptions        []libacp.SessionConfigOption
	configReceived       bool
	configOptionsPending bool

	modeState     *libacp.SessionModeState
	modeReceived  bool
	pendingModeID string

	modelState *libacp.SessionModelState

	termMu    sync.Mutex
	terminals map[string]*bridgeTerminal
}

func newExternalBridge(t *Transport, upstreamID libacp.SessionID, bound bool) *externalBridge {
	b := &externalBridge{upstreamID: upstreamID, bound: bound, viewerID: "acp-bridge-" + uuid.NewString()}
	b.attach(t)
	return b
}

func (b *externalBridge) ID() string { return b.viewerID }

func (b *externalBridge) bindInstance(mgr agentinstance.Manager, instanceID string) {
	b.relayMu.Lock()
	b.mgr = mgr
	b.instanceID = instanceID
	b.relayMu.Unlock()
}

func (b *externalBridge) suppressReplay() {
	b.relayMu.Lock()
	b.relaySuppressed = true
	b.relayMu.Unlock()
}

func (b *externalBridge) resumeRelay() {
	b.relayMu.Lock()
	b.relaySuppressed = false
	b.relayMu.Unlock()
}

func (b *externalBridge) holdRelay() {
	b.relayMu.Lock()
	b.relayHeld = true
	b.relayMu.Unlock()
}

func (b *externalBridge) releaseRelay(ctx context.Context) {
	for {
		b.relayMu.Lock()
		if len(b.relayQueue) == 0 {
			b.relayHeld = false
			b.relayMu.Unlock()
			return
		}
		batch := b.relayQueue
		b.relayQueue = nil
		t := b.relayT
		suppressed := b.relaySuppressed
		b.relayMu.Unlock()
		if t == nil || suppressed {
			continue
		}
		for _, upd := range batch {
			t.relayExternalUpdate(ctx, b.upstreamID, upd)
		}
	}
}

func (b *externalBridge) attach(t *Transport) {
	b.relayMu.Lock()
	b.relayT = t
	b.relayMu.Unlock()
	go func() {
		<-t.connCtx.Done()
		b.detachFrom(t)
		b.detachViewer()
	}()
}

func (b *externalBridge) detachFrom(t *Transport) {
	b.relayMu.Lock()
	if b.relayT == t {
		b.relayT = nil
	}
	b.relayMu.Unlock()
}

func (b *externalBridge) detachViewer() {
	b.detachOnce.Do(func() {
		b.relayMu.Lock()
		mgr, instanceID := b.mgr, b.instanceID
		b.relayMu.Unlock()
		if mgr == nil || instanceID == "" {
			return
		}
		_ = mgr.Detach(instanceID, b.downstream(), b.viewerID)
	})
}

func (b *externalBridge) transport() *Transport {
	b.relayMu.Lock()
	defer b.relayMu.Unlock()
	return b.relayT
}

func (b *externalBridge) relayUpstream(ctx context.Context, upd libacp.SessionUpdate) {
	b.relayMu.Lock()
	if b.relayHeld && !b.relaySuppressed {
		b.relayQueue = append(b.relayQueue, upd)
		b.relayMu.Unlock()
		return
	}
	t := b.relayT
	suppressed := b.relaySuppressed
	b.relayMu.Unlock()
	if t == nil || suppressed {
		return
	}
	t.relayExternalUpdate(ctx, b.upstreamID, upd)
}

func (b *externalBridge) Deliver(ctx context.Context, n libacp.SessionNotification) error {
	return b.SessionUpdate(ctx, n)
}

func (b *externalBridge) setDownstreamID(id libacp.SessionID) {
	b.mu.Lock()
	b.downstreamID = id
	b.mu.Unlock()
}

func (b *externalBridge) downstream() libacp.SessionID {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.downstreamID
}

func (b *externalBridge) SessionUpdate(ctx context.Context, n libacp.SessionNotification) error {
	if n.Update.SessionUpdate == libacp.SessionUpdateAvailableCommands {
		b.mu.Lock()
		b.cachedCommands = n.Update.AvailableCommands
		relay := b.bound
		b.mu.Unlock()
		b.persistCommands(ctx, n.Update.AvailableCommands)
		if relay {
			b.relayUpstream(ctx, n.Update)
		}
		return nil
	}
	if n.Update.SessionUpdate == libacp.SessionUpdateConfigOption {
		b.mu.Lock()
		b.configReceived = true
		b.configOptions = n.Update.ConfigOptions
		relay := b.bound
		if !relay {
			b.configOptionsPending = true
		}
		b.mu.Unlock()
		merged := b.configOptionsSurface()
		b.persistConfigOptions(ctx, merged)
		if relay {
			b.relayUpstream(ctx, libacp.SessionUpdate{
				SessionUpdate: libacp.SessionUpdateConfigOption,
				ConfigOptions: merged,
			})
		}
		return nil
	}
	if n.Update.SessionUpdate == libacp.SessionUpdateCurrentMode {
		b.mu.Lock()
		b.modeReceived = true
		if b.modeState != nil {
			b.modeState.CurrentModeID = n.Update.CurrentModeID
		} else {
			b.pendingModeID = n.Update.CurrentModeID
		}
		relay := b.bound
		if !relay {
			b.configOptionsPending = true
		}
		b.mu.Unlock()
		merged := b.configOptionsSurface()
		b.persistConfigOptions(ctx, merged)
		if relay {
			b.relayUpstream(ctx, libacp.SessionUpdate{
				SessionUpdate: libacp.SessionUpdateConfigOption,
				ConfigOptions: merged,
			})
		}
		return nil
	}
	b.relayUpstream(ctx, n.Update)
	b.captureForHistory(n.Update)
	return nil
}

func (b *externalBridge) captureForHistory(u libacp.SessionUpdate) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.capture == nil {
		return
	}
	switch u.SessionUpdate {
	case libacp.SessionUpdateAgentMessageChunk:
		if c := u.Content; c != nil && c.Type == string(libacp.ContentKindText) {
			b.capture.addText(c.Text)
		}
	case libacp.SessionUpdateAgentThoughtChunk:
		if c := u.Content; c != nil && c.Type == string(libacp.ContentKindText) {
			b.capture.addThinking(c.Text)
		}
	case libacp.SessionUpdateToolCall, libacp.SessionUpdateToolCallUpdate:
		b.capture.addToolUpdate(u)
	}
}

func (b *externalBridge) markBound(ctx context.Context) {
	b.mu.Lock()
	if b.bound {
		b.mu.Unlock()
		return
	}
	b.bound = true
	cmds := b.cachedCommands
	flushConfig := b.configOptionsPending
	b.mu.Unlock()
	var configOpts []libacp.SessionConfigOption
	if flushConfig {
		configOpts = b.configOptionsSurface()
	}
	if cmds != nil {
		b.relayUpstream(ctx, libacp.SessionUpdate{
			SessionUpdate:     libacp.SessionUpdateAvailableCommands,
			AvailableCommands: cmds,
		})
	}
	if flushConfig {
		b.relayUpstream(ctx, libacp.SessionUpdate{
			SessionUpdate: libacp.SessionUpdateConfigOption,
			ConfigOptions: configOpts,
		})
	}
}

func (b *externalBridge) seedConfigOptions(opts []libacp.SessionConfigOption) {
	b.mu.Lock()
	if !b.configReceived {
		b.configOptions = opts
	}
	b.mu.Unlock()
}

func (b *externalBridge) applyConfigOptions(opts []libacp.SessionConfigOption) {
	b.mu.Lock()
	b.configReceived = true
	b.configOptions = opts
	b.mu.Unlock()
}

func (b *externalBridge) seedModes(m *libacp.SessionModeState) {
	if m == nil {
		return
	}
	b.mu.Lock()
	if b.modeState == nil {
		cp := *m
		if b.modeReceived && b.pendingModeID != "" {
			cp.CurrentModeID = b.pendingModeID
		}
		b.modeState = &cp
	}
	b.mu.Unlock()
}

func (b *externalBridge) applyMode(modeID string) {
	b.mu.Lock()
	b.modeReceived = true
	if b.modeState != nil {
		b.modeState.CurrentModeID = modeID
	} else {
		b.pendingModeID = modeID
	}
	b.mu.Unlock()
}

func (b *externalBridge) seedModels(m *libacp.SessionModelState) {
	if m == nil {
		return
	}
	b.mu.Lock()
	if b.modelState == nil {
		cp := *m
		b.modelState = &cp
	}
	b.mu.Unlock()
}

func (b *externalBridge) applyModel(modelID string) {
	b.mu.Lock()
	if b.modelState != nil {
		b.modelState.CurrentModelID = modelID
	}
	b.mu.Unlock()
}

func (b *externalBridge) buildConfigOptionsLocked() []libacp.SessionConfigOption {
	modeOpt, hasMode := syntheticModeOption(b.modeState)
	modelOpt, hasModel := syntheticModelOption(b.modelState)
	if !hasMode && !hasModel {
		return b.configOptions
	}
	out := make([]libacp.SessionConfigOption, 0, len(b.configOptions)+2)
	if hasMode {
		out = append(out, modeOpt)
	}
	if hasModel {
		out = append(out, modelOpt)
	}
	out = append(out, b.configOptions...)
	return out
}

func (b *externalBridge) snapshotConfigOptions() []libacp.SessionConfigOption {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buildConfigOptionsLocked()
}

func (b *externalBridge) configOptionsSurface() []libacp.SessionConfigOption {
	b.relayMu.Lock()
	mgr, instanceID := b.mgr, b.instanceID
	b.relayMu.Unlock()
	if mgr == nil || instanceID == "" {
		return b.snapshotConfigOptions()
	}
	opts, err := mgr.SessionConfigOptions(instanceID, b.downstream())
	if err != nil {
		return nil
	}
	return opts
}

func (b *externalBridge) persistCommands(ctx context.Context, cmds []libacp.AvailableCommand) {
	if t := b.transport(); t != nil {
		t.persistSessionAgentCommands(ctx, b.upstreamID, cmds)
	}
}

func (b *externalBridge) persistConfigOptions(ctx context.Context, opts []libacp.SessionConfigOption) {
	if t := b.transport(); t != nil {
		t.persistSessionAgentConfigOptions(ctx, b.upstreamID, opts)
	}
}

func (b *externalBridge) RequestPermission(ctx context.Context, req libacp.RequestPermissionRequest) (libacp.RequestPermissionResponse, error) {
	t := b.transport()
	if t == nil || t.conn == nil {
		return libacp.RequestPermissionResponse{}, libacp.InternalError("acpsvc: no upstream connection to relay permission to")
	}
	req.SessionID = b.upstreamID
	return t.conn.RequestPermission(ctx, req)
}

func (b *externalBridge) beginCapture() {
	b.mu.Lock()
	b.capture = &externalTurnCapture{}
	b.mu.Unlock()
}

func (b *externalBridge) finishCapture() []externalCaptureSegment {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.capture == nil {
		return nil
	}
	segs := b.capture.segments
	b.capture = nil
	return segs
}

func (t *Transport) relayExternalUpdate(ctx context.Context, upstreamID libacp.SessionID, upd libacp.SessionUpdate) {
	if t.conn == nil {
		return
	}
	if upd.SessionUpdate == libacp.SessionUpdateConfigOption {
		upd.ConfigOptions = t.externalConfigOptionsForRelay(upstreamID, upd.ConfigOptions)
	}
	reportErr, _, end := t.tracker().Start(ctx, "relay", "acp_session_update",
		"session_id", string(upstreamID), "kind", string(upd.SessionUpdate))
	defer end()
	if err := t.conn.SessionUpdate(libacp.SessionNotification{SessionID: upstreamID, Update: upd}); err != nil {
		reportErr(err)
	}
}

func (t *Transport) externalConfigOptionsForRelay(sid libacp.SessionID, downstream []libacp.SessionConfigOption) []libacp.SessionConfigOption {
	sess, ok := t.sessionFor(sid)
	if !ok {
		return downstream
	}
	out := make([]libacp.SessionConfigOption, 0, len(downstream)+1)
	out = append(out, downstream...)
	out = append(out, t.hitlPolicyConfigOption(sess))
	return out
}

type externalDriver struct {
	t         *Transport
	agentName string

	upstreamID libacp.SessionID

	mu           sync.Mutex
	conn         *libacp.ClientSideConnection
	handle       *agenthost.Handle
	instanceID   string
	downstreamID libacp.SessionID
	bridge       *externalBridge
}

func (d *externalDriver) AgentName() string { return d.agentName }

func (d *externalDriver) AvailableCommands() []libacp.AvailableCommand { return nil }

func (d *externalDriver) ConfigOptions(_ context.Context, sess *sessionEntry) []libacp.SessionConfigOption {
	d.mu.Lock()
	bridge := d.bridge
	d.mu.Unlock()
	var base []libacp.SessionConfigOption
	if bridge != nil {
		base = bridge.configOptionsSurface()
	}
	out := make([]libacp.SessionConfigOption, 0, len(base)+1)
	out = append(out, base...)
	out = append(out, d.t.hitlPolicyConfigOption(sess))
	return out
}

func (d *externalDriver) SetConfigOption(ctx context.Context, sess *sessionEntry, configID string, value libacp.SessionConfigOptionValue) error {
	if configID == configIDHITLPolicy {
		if err := d.t.setSessionConfigOption(ctx, sess, configID, value.AsString()); err != nil {
			return err
		}
		d.t.persistSessionHITLPolicy(ctx, d.upstreamID, sess.hitlPolicy())
		return nil
	}

	d.mu.Lock()
	conn, instanceID, downstreamID, bridge := d.conn, d.instanceID, d.downstreamID, d.bridge
	d.mu.Unlock()
	if bridge == nil {
		return libacp.NewError(libacp.ErrInvalidParams, "external agent session is not active")
	}
	if instanceID != "" {
		if err := d.t.deps.Instances.SetConfigOption(ctx, instanceID, downstreamID, configID, value); err != nil {
			return err
		}
		bridge.persistConfigOptions(ctx, bridge.configOptionsSurface())
		return nil
	}
	if conn == nil {
		return libacp.NewError(libacp.ErrInvalidParams, "external agent session is not active")
	}
	if configID == AgentModeConfigOptionID {
		if _, err := conn.SetSessionMode(ctx, libacp.SetSessionModeRequest{
			SessionID: downstreamID,
			ModeID:    value.AsString(),
		}); err != nil {
			return err
		}
		bridge.applyMode(value.AsString())
		bridge.persistConfigOptions(ctx, bridge.snapshotConfigOptions())
		return nil
	}
	if configID == AgentModelConfigOptionID {
		if _, err := conn.SetSessionModel(ctx, libacp.SetSessionModelRequest{
			SessionID: downstreamID,
			ModelID:   value.AsString(),
		}); err != nil {
			return err
		}
		bridge.applyModel(value.AsString())
		bridge.persistConfigOptions(ctx, bridge.snapshotConfigOptions())
		return nil
	}
	resp, err := conn.SetSessionConfigOption(ctx, libacp.SetSessionConfigOptionRequest{
		SessionID: downstreamID,
		ConfigID:  configID,
		Value:     value,
	})
	if err != nil {
		return err
	}
	bridge.applyConfigOptions(resp.ConfigOptions)
	bridge.persistConfigOptions(ctx, bridge.snapshotConfigOptions())
	return nil
}

func (d *externalDriver) Close() error {
	d.mu.Lock()
	handle := d.handle
	bridge := d.bridge
	instanceID := d.instanceID
	d.handle = nil
	d.conn = nil
	d.bridge = nil
	d.downstreamID = ""
	d.mu.Unlock()
	if bridge != nil {
		bridge.closeAllTerminals()
	}
	if instanceID != "" {
		if bridge != nil {
			bridge.detachViewer()
			bridge.detachFrom(d.t)
		}
		return nil
	}
	if handle != nil {
		return handle.Close()
	}
	return nil
}

type storeMcpResolver struct {
	store runtimetypes.Store
}

func (r storeMcpResolver) GetByName(ctx context.Context, name string) (*runtimetypes.MCPServer, error) {
	return r.store.GetMCPServerByName(ctx, name)
}

func (t *Transport) resolveExternalAgent(ctx context.Context, name string) (*runtimetypes.Agent, *runtimetypes.ExternalACPConfig, error) {
	if t.deps.DB == nil {
		return nil, nil, libacp.InternalError("acpsvc: no database configured for external agents")
	}
	reg := agentregistryservice.New(t.deps.DB)
	agent, err := agentregistryservice.ResolveForSpawn(ctx, reg, name)
	if err != nil {
		if errors.Is(err, agentregistryservice.ErrAgentDisabled) {
			return nil, nil, libacp.NewErrorf(libacp.ErrInvalidParams, "%v", err)
		}
		if errors.Is(err, libdb.ErrNotFound) {
			return nil, nil, libacp.NewErrorf(libacp.ErrInvalidParams, "unknown contenox.agent %q", name)
		}
		return nil, nil, libacp.InternalError(fmt.Sprintf("acpsvc: resolve agent %q: %v", name, err))
	}
	if agent.Kind == runtimetypes.AgentKindChain {
		return agent, &runtimetypes.ExternalACPConfig{}, nil
	}
	cfg, err := agent.ExternalACPConfig()
	if err != nil {
		return nil, nil, libacp.NewErrorf(libacp.ErrInvalidParams, "contenox.agent %q: %v", name, err)
	}
	return agent, cfg, nil
}

func filterMcpForCaps(servers []libacp.McpServer, caps libacp.McpCapabilities) ([]libacp.McpServer, []string) {
	kept := make([]libacp.McpServer, 0, len(servers))
	var dropped []string
	for _, srv := range servers {
		switch srv.Kind() {
		case libacp.McpServerKindHTTP:
			if !caps.HTTP {
				dropped = append(dropped, srv.Name)
				continue
			}
		case libacp.McpServerKindSSE:
			if !caps.SSE {
				dropped = append(dropped, srv.Name)
				continue
			}
		}
		kept = append(kept, srv)
	}
	return kept, dropped
}

type externalAttach struct {
	conn         *libacp.ClientSideConnection
	handle       *agenthost.Handle
	instanceID   string
	downstreamID libacp.SessionID
	bridge       *externalBridge
}

func (ea *externalAttach) teardown(t *Transport) {
	if ea.handle != nil {
		_ = ea.handle.Close()
	}
	if ea.instanceID != "" && t.deps.Instances != nil {
		_ = t.deps.Instances.Stop(ea.instanceID)
	}
}

func (t *Transport) resolveMcpAllowlist(ctx context.Context, cfg *runtimetypes.ExternalACPConfig, agentName string) ([]libacp.McpServer, error) {
	store := runtimetypes.New(t.deps.DB.WithoutTransaction())
	servers, err := agenthost.ResolveForwardedMcpServers(ctx, storeMcpResolver{store: store}, cfg.McpServers)
	if err != nil {
		return nil, libacp.InternalError(fmt.Sprintf("acpsvc: resolve mcp allowlist for agent %q: %v", agentName, err))
	}
	return servers, nil
}

func (t *Transport) openInstanceSession(ctx context.Context, instanceID string, bridge *externalBridge, cfg *runtimetypes.ExternalACPConfig, cwd, agentName string) (libacp.SessionID, error) {
	mcpServers, err := t.resolveMcpAllowlist(ctx, cfg, agentName)
	if err != nil {
		return "", err
	}
	downstreamID, err := t.deps.Instances.OpenSession(ctx, instanceID, agentinstance.SessionSpec{
		Cwd:        cwd,
		McpServers: mcpServers,
		Terminal:   false,
	})
	if err != nil {
		return "", libacp.InternalError(fmt.Sprintf("acpsvc: open session on agent %q instance: %v", agentName, err))
	}
	bridge.setDownstreamID(downstreamID)
	bridge.persistConfigOptions(ctx, bridge.configOptionsSurface())
	return downstreamID, nil
}

func (t *Transport) initExternalConn(ctx context.Context, conn *libacp.ClientSideConnection, bridge *externalBridge, cfg *runtimetypes.ExternalACPConfig, cwd, agentName string, terminalCapable bool) (libacp.SessionID, error) {
	mcpServers, err := t.resolveMcpAllowlist(ctx, cfg, agentName)
	if err != nil {
		return "", err
	}

	clientCaps := libacp.ClientCapabilities{}
	if terminalCapable {
		clientCaps.Terminal = true
	}
	init, err := conn.Initialize(ctx, libacp.InitializeRequest{
		ProtocolVersion:    libacp.ProtocolVersion,
		ClientCapabilities: clientCaps,
		ClientInfo:         &libacp.Implementation{Name: "contenox", Version: version.Get()},
	})
	if err != nil {
		return "", libacp.InternalError(fmt.Sprintf("acpsvc: initialize agent %q: %v", agentName, err))
	}
	if init.ProtocolVersion != libacp.ProtocolVersion {
		return "", libacp.InternalError(fmt.Sprintf("acpsvc: agent %q negotiated unsupported protocol version %d", agentName, init.ProtocolVersion))
	}

	forwarded, _ := filterMcpForCaps(mcpServers, init.AgentCapabilities.McpCapabilities)
	if forwarded == nil {
		forwarded = []libacp.McpServer{}
	}
	downstream, err := conn.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        cwd,
		McpServers: forwarded,
	})
	if err != nil {
		return "", libacp.InternalError(fmt.Sprintf("acpsvc: session/new against agent %q: %v", agentName, err))
	}
	bridge.seedConfigOptions(downstream.ConfigOptions)
	bridge.seedModes(downstream.Modes)
	bridge.seedModels(downstream.Models)
	bridge.setDownstreamID(downstream.SessionID)
	bridge.persistConfigOptions(ctx, bridge.snapshotConfigOptions())
	return downstream.SessionID, nil
}

func (t *Transport) bringUpExternal(ctx context.Context, upstreamID libacp.SessionID, cwd, agentName string, bound bool) (*externalAttach, error) {
	agent, cfg, err := t.resolveExternalAgent(ctx, agentName)
	if err != nil {
		return nil, err
	}
	bridge := newExternalBridge(t, upstreamID, bound)

	if t.deps.Instances != nil {
		instanceID, err := t.deps.Instances.StartResolved(ctx, agent, cwd)
		if err != nil {
			return nil, libacp.InternalError(fmt.Sprintf("acpsvc: start agent %q instance: %v", agentName, err))
		}
		bridge.bindInstance(t.deps.Instances, instanceID)
		downstreamID, err := t.openInstanceSession(ctx, instanceID, bridge, cfg, cwd, agentName)
		if err != nil {
			_ = t.deps.Instances.Stop(instanceID)
			return nil, err
		}
		if _, err := t.deps.Instances.Attach(ctx, instanceID, downstreamID, bridge); err != nil {
			_ = t.deps.Instances.Stop(instanceID)
			return nil, libacp.InternalError(fmt.Sprintf("acpsvc: attach to agent %q instance: %v", agentName, err))
		}
		return &externalAttach{instanceID: instanceID, downstreamID: downstreamID, bridge: bridge}, nil
	}

	if agent.Kind == runtimetypes.AgentKindChain {
		return nil, libacp.NewErrorf(libacp.ErrInvalidParams,
			"contenox.agent %q is a chain agent, which this transport cannot run: chain units are spawned by the fleet manager (run them under `contenox serve`)", agentName)
	}
	spawnCfg := *cfg
	if spawnCfg.Cwd == "" {
		spawnCfg.Cwd = cwd
	}
	host := &agenthost.ExternalACPAgent{Config: spawnCfg, KillGrace: externalKillGrace}
	handle, err := host.Connect(t.connCtx, bridge)
	if err != nil {
		return nil, libacp.InternalError(fmt.Sprintf("acpsvc: spawn agent %q: %v", agentName, err))
	}
	downstreamID, err := t.initExternalConn(ctx, handle.Conn, bridge, cfg, cwd, agentName, t.deps.ShellSessions != nil)
	if err != nil {
		_ = handle.Close()
		return nil, err
	}
	return &externalAttach{conn: handle.Conn, handle: handle, downstreamID: downstreamID, bridge: bridge}, nil
}

type externalTarget struct {
	conn         *libacp.ClientSideConnection
	instanceID   string
	downstreamID libacp.SessionID
	bridge       *externalBridge
}

func (d *externalDriver) ensureAttached(ctx context.Context, upstreamID libacp.SessionID, sess *sessionEntry) (*externalTarget, error) {
	d.mu.Lock()
	if d.bridge != nil {
		tgt := &externalTarget{conn: d.conn, instanceID: d.instanceID, downstreamID: d.downstreamID, bridge: d.bridge}
		d.mu.Unlock()
		return tgt, nil
	}
	instanceID, downstreamID := d.instanceID, d.downstreamID
	d.mu.Unlock()

	if d.t.deps.Instances != nil && instanceID != "" && downstreamID != "" {
		if st, err := d.t.deps.Instances.Get(instanceID); err == nil && st.State == agentinstance.StateRunning {
			bridge := newExternalBridge(d.t, upstreamID, true)
			bridge.setDownstreamID(downstreamID)
			bridge.suppressReplay()
			bridge.bindInstance(d.t.deps.Instances, instanceID)
			if _, err := d.t.deps.Instances.Attach(ctx, instanceID, downstreamID, bridge); err == nil {
				return d.commitReattach(instanceID, downstreamID, bridge), nil
			}
			bridge.detachFrom(d.t)
		}
	}

	sess.mu.Lock()
	cwd := sess.Cwd
	sess.mu.Unlock()

	att, err := d.t.bringUpExternal(ctx, upstreamID, cwd, d.agentName, true)
	if err != nil {
		return nil, err
	}
	return d.commitBringUp(ctx, upstreamID, att)
}

func (d *externalDriver) commitReattach(instanceID string, downstreamID libacp.SessionID, bridge *externalBridge) *externalTarget {
	bridge.resumeRelay()
	d.mu.Lock()
	if d.bridge != nil {
		won := &externalTarget{conn: d.conn, instanceID: d.instanceID, downstreamID: d.downstreamID, bridge: d.bridge}
		d.mu.Unlock()
		bridge.detachViewer()
		bridge.detachFrom(d.t)
		return won
	}
	d.bridge = bridge
	d.instanceID = instanceID
	d.downstreamID = downstreamID
	d.mu.Unlock()
	return &externalTarget{instanceID: instanceID, downstreamID: downstreamID, bridge: bridge}
}

func (d *externalDriver) commitBringUp(ctx context.Context, upstreamID libacp.SessionID, att *externalAttach) (*externalTarget, error) {
	d.mu.Lock()
	if d.bridge != nil {
		won := &externalTarget{conn: d.conn, instanceID: d.instanceID, downstreamID: d.downstreamID, bridge: d.bridge}
		d.mu.Unlock()
		att.teardown(d.t)
		return won, nil
	}
	d.conn = att.conn
	d.handle = att.handle
	d.instanceID = att.instanceID
	d.downstreamID = att.downstreamID
	d.bridge = att.bridge
	d.mu.Unlock()
	if att.instanceID != "" {
		d.t.persistSessionInstance(ctx, upstreamID, att.instanceID)
		d.t.persistSessionDownstream(ctx, upstreamID, att.downstreamID)
	}
	if opts := att.bridge.configOptionsSurface(); len(opts) > 0 {
		d.t.relayExternalUpdate(ctx, upstreamID, libacp.SessionUpdate{
			SessionUpdate: libacp.SessionUpdateConfigOption,
			ConfigOptions: opts,
		})
	}
	return &externalTarget{conn: att.conn, instanceID: att.instanceID, downstreamID: att.downstreamID, bridge: att.bridge}, nil
}

func (d *externalDriver) promptDownstream(ctx context.Context, tgt *externalTarget, prompt []libacp.ContentBlock) (libacp.StopReason, error) {
	if tgt.instanceID != "" {
		return d.t.deps.Instances.Prompt(ctx, tgt.instanceID, tgt.downstreamID, prompt)
	}
	resp, err := tgt.conn.Prompt(ctx, libacp.PromptRequest{SessionID: tgt.downstreamID, Prompt: prompt})
	if err != nil {
		return "", err
	}
	return resp.StopReason, nil
}

func (d *externalDriver) cancelDownstream(tgt *externalTarget) {
	if tgt.instanceID != "" {
		_ = d.t.deps.Instances.Cancel(tgt.instanceID, tgt.downstreamID)
		return
	}
	_ = tgt.conn.CancelPrompt(tgt.downstreamID)
}

func (d *externalDriver) Prompt(ctx context.Context, req libacp.PromptRequest, sess *sessionEntry) (libacp.PromptResponse, error) {
	t := d.t
	reportErr, reportChange, end := t.tracker().Start(ctx, "prompt", "acp_external_session", "session_id", string(req.SessionID))
	defer end()

	tgt, err := d.ensureAttached(ctx, req.SessionID, sess)
	if err != nil {
		reportErr(err)
		return libacp.PromptResponse{}, err
	}

	var cancelOnce sync.Once
	cancel := func() {
		cancelOnce.Do(func() { d.cancelDownstream(tgt) })
	}
	promptReg := t.registerPromptCancel(req.SessionID, cancel)
	defer t.unregisterPromptCancel(req.SessionID, promptReg)

	tgt.bridge.beginCapture()
	stopReason, promptErr := d.promptDownstream(ctx, tgt, req.Prompt)
	captured := tgt.bridge.finishCapture()

	userText, _ := libacp.FlattenContent(req.Prompt)
	t.persistExternalTurn(ctx, sess.InternalSessionID, userText, captured)

	if promptErr != nil {
		if errors.Is(promptErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			reportChange(string(req.SessionID), map[string]any{"stop_reason": string(libacp.StopReasonCancelled)})
			return libacp.PromptResponse{StopReason: libacp.StopReasonCancelled}, nil
		}
		reportErr(promptErr)
		return libacp.PromptResponse{}, libacp.InternalError(promptErr.Error())
	}

	libacp.AfterResponse(ctx, func() {
		update := libacp.SessionUpdate{
			SessionUpdate: libacp.SessionUpdateSessionInfo,
			UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
		}
		if title := t.sessionInfoTitle(ctx, sess.InternalSessionID); title != "" {
			update.Title = title
		}
		t.sendUpdate(ctx, libacp.SessionNotification{SessionID: req.SessionID, Update: update})
	})

	reportChange(string(req.SessionID), map[string]any{"stop_reason": string(stopReason)})
	return libacp.PromptResponse{StopReason: stopReason}, nil
}

type externalTurnCapture struct {
	segments  []externalCaptureSegment
	toolIndex map[string]int
}

type externalCaptureSegment struct {
	kind string
	text string
	tool *externalToolRecord
}

type externalToolRecord struct {
	ToolCallID  string                    `json:"toolCallId"`
	Title       string                    `json:"title,omitempty"`
	Kind        libacp.ToolKind           `json:"kind,omitempty"`
	Status      libacp.ToolCallStatus     `json:"status,omitempty"`
	RawInput    json.RawMessage           `json:"rawInput,omitempty"`
	RawOutput   json.RawMessage           `json:"rawOutput,omitempty"`
	ToolContent []libacp.ToolCallContent  `json:"toolContent,omitempty"`
	Locations   []libacp.ToolCallLocation `json:"locations,omitempty"`
}

func (c *externalTurnCapture) addText(s string) {
	if s == "" {
		return
	}
	if n := len(c.segments); n > 0 && c.segments[n-1].kind == "text" {
		c.segments[n-1].text += s
		return
	}
	c.segments = append(c.segments, externalCaptureSegment{kind: "text", text: s})
}

func (c *externalTurnCapture) addThinking(s string) {
	if s == "" {
		return
	}
	if n := len(c.segments); n > 0 && c.segments[n-1].kind == "thinking" {
		c.segments[n-1].text += s
		return
	}
	c.segments = append(c.segments, externalCaptureSegment{kind: "thinking", text: s})
}

func (c *externalTurnCapture) addToolUpdate(u libacp.SessionUpdate) {
	if u.ToolCallID == "" {
		return
	}
	if c.toolIndex == nil {
		c.toolIndex = make(map[string]int)
	}
	idx, ok := c.toolIndex[u.ToolCallID]
	if !ok {
		c.segments = append(c.segments, externalCaptureSegment{kind: "tool", tool: &externalToolRecord{ToolCallID: u.ToolCallID}})
		idx = len(c.segments) - 1
		c.toolIndex[u.ToolCallID] = idx
	}
	rec := c.segments[idx].tool
	if u.Title != "" {
		rec.Title = u.Title
	}
	if u.Kind != "" {
		rec.Kind = u.Kind
	}
	if u.Status != "" {
		rec.Status = u.Status
	}
	if len(u.RawInput) > 0 {
		rec.RawInput = u.RawInput
	}
	if len(u.RawOutput) > 0 {
		rec.RawOutput = u.RawOutput
	}
	if len(u.ToolContent) > 0 {
		rec.ToolContent = u.ToolContent
	}
	if len(u.Locations) > 0 {
		rec.Locations = u.Locations
	}
}

func externalTurnMessages(userText string, segments []externalCaptureSegment, base time.Time) []taskengine.Message {
	var msgs []taskengine.Message
	seq := 0
	stamp := func() time.Time {
		ts := base.Add(time.Duration(seq) * time.Millisecond)
		seq++
		return ts
	}
	if strings.TrimSpace(userText) != "" {
		msgs = append(msgs, taskengine.Message{ID: uuid.NewString(), Role: "user", Content: userText, Timestamp: stamp()})
	}

	var pendingText, pendingThinking strings.Builder
	flushAssistant := func() {
		if pendingText.Len() == 0 && pendingThinking.Len() == 0 {
			return
		}
		msgs = append(msgs, taskengine.Message{
			ID:        uuid.NewString(),
			Role:      "assistant",
			Content:   pendingText.String(),
			Thinking:  pendingThinking.String(),
			Timestamp: stamp(),
		})
		pendingText.Reset()
		pendingThinking.Reset()
	}

	for _, seg := range segments {
		switch seg.kind {
		case "text":
			pendingText.WriteString(seg.text)
		case "thinking":
			pendingThinking.WriteString(seg.text)
		case "tool":
			if seg.tool == nil {
				continue
			}
			flushAssistant()
			payload, err := json.Marshal(seg.tool)
			if err != nil {
				continue
			}
			msgs = append(msgs, taskengine.Message{
				ID:         uuid.NewString(),
				Role:       "tool",
				ToolCallID: seg.tool.ToolCallID,
				Content:    string(payload),
				Timestamp:  stamp(),
			})
		}
	}
	flushAssistant()
	return msgs
}

func (t *Transport) persistExternalTurn(ctx context.Context, internalSessionID, userText string, segments []externalCaptureSegment) {
	if t.deps.DB == nil || internalSessionID == "" {
		return
	}
	msgs := externalTurnMessages(userText, segments, time.Now().UTC())
	if len(msgs) == 0 {
		return
	}
	cleanCtx := context.WithoutCancel(ctx)
	mgr := chatservice.NewManager(t.workspaceID())
	if err := mgr.PersistDiff(cleanCtx, t.deps.DB.WithoutTransaction(), internalSessionID, msgs); err != nil {
		reportErr, _, end := t.tracker().Start(cleanCtx, "persist", "acp_external_history", "session_id", internalSessionID)
		reportErr(err)
		end()
	}
}

func (t *Transport) markExternalIfPersisted(ctx context.Context, store runtimetypes.Store, sid libacp.SessionID, entry *sessionEntry) {
	name := t.readSessionAgent(ctx, store, sid)
	if name == "" {
		return
	}
	ed := &externalDriver{t: t, agentName: name, upstreamID: sid}
	if t.deps.Instances != nil {
		ed.instanceID = t.readSessionInstance(ctx, store, sid)
		ed.downstreamID = t.readSessionDownstream(ctx, store, sid)
	}
	entry.driver = ed
	if policy := t.readSessionHITLPolicy(ctx, store, sid); policy != "" {
		entry.setHITLPolicy(policy)
	}
}

func (t *Transport) reloadedConfigOptions(ctx context.Context, store runtimetypes.Store, sid libacp.SessionID, entry *sessionEntry) []libacp.SessionConfigOption {
	if _, ok := entry.driver.(*externalDriver); ok {
		downstream := t.readSessionAgentConfigOptions(ctx, store, sid)
		return append(downstream, t.hitlPolicyConfigOption(entry))
	}
	return t.sessionConfigOptions(ctx, entry)
}

func (t *Transport) reemitExternalCommandMenu(ctx context.Context, store runtimetypes.Store, sid libacp.SessionID) {
	cmds := t.readSessionAgentCommands(ctx, store, sid)
	if len(cmds) == 0 {
		return
	}
	libacp.AfterResponse(ctx, func() {
		t.relayExternalUpdate(ctx, sid, libacp.SessionUpdate{
			SessionUpdate:     libacp.SessionUpdateAvailableCommands,
			AvailableCommands: cmds,
		})
	})
}
