package agentinstance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/agenthost"
	"github.com/contenox/runtime/runtime/agentregistryservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/google/uuid"
)

const defaultKillGrace = 2 * time.Second

const (
	ChainACPSubcommand = "acp"
	ChainPathEnvVar    = "CONTENOX_ACP_CHAIN_PATH"
)

var ErrNotFound = errors.New("agentinstance: instance not found")

type EventKind string

const (
	EventStateChange      EventKind = "state_change"
	EventAttach           EventKind = "attach"
	EventDetach           EventKind = "detach"
	EventUnsupervisedDeny EventKind = "unsupervised_permission"
)

type Event struct {
	Kind       EventKind        `json:"kind"`
	InstanceID string           `json:"instanceId"`
	AgentID    string           `json:"agentId"`
	AgentName  string           `json:"agentName"`
	State      string           `json:"state,omitempty"`
	SessionID  libacp.SessionID `json:"sessionId,omitempty"`
	ViewerID   string           `json:"viewerId,omitempty"`
	Controller bool             `json:"controller,omitempty"`
	Time       time.Time        `json:"time"`
}

type UnattendedPermission struct {
	InstanceID string
	AgentID    string
	AgentName  string
	SessionID  libacp.SessionID
	Request    libacp.RequestPermissionRequest
}

type PermissionFallback func(ctx context.Context, req UnattendedPermission) (libacp.RequestPermissionResponse, error)

type EventSink func(Event)

type FleetEntry struct {
	AgentID   string           `json:"agentId"`
	AgentName string           `json:"agentName"`
	Kind      string           `json:"kind"`
	Instances []InstanceStatus `json:"instances"`
}

func (e FleetEntry) Running() bool { return len(e.Instances) > 0 }

type Manager interface {
	Start(ctx context.Context, agentName, cwd string) (instanceID string, err error)

	StartResolved(ctx context.Context, agent *runtimetypes.Agent, cwd string) (instanceID string, err error)

	Attach(ctx context.Context, instanceID string, sessionID libacp.SessionID, viewer Viewer) (controllerGranted bool, err error)

	Detach(instanceID string, sessionID libacp.SessionID, viewerID string) error

	List(ctx context.Context) ([]FleetEntry, error)

	Get(instanceID string) (InstanceStatus, error)

	OpenSession(ctx context.Context, instanceID string, spec SessionSpec) (libacp.SessionID, error)

	Prompt(ctx context.Context, instanceID string, sessionID libacp.SessionID, prompt []libacp.ContentBlock) (libacp.StopReason, error)

	DeliverToSession(ctx context.Context, sessionID libacp.SessionID, n libacp.SessionNotification) error

	Cancel(instanceID string, sessionID libacp.SessionID) error

	CloseSession(instanceID string, sessionID libacp.SessionID) error

	SetConfigOption(ctx context.Context, instanceID string, sessionID libacp.SessionID, configID string, value libacp.SessionConfigOptionValue) error

	SessionConfigOptions(instanceID string, sessionID libacp.SessionID) ([]libacp.SessionConfigOption, error)

	AvailableCommands(instanceID string, sessionID libacp.SessionID) ([]libacp.AvailableCommand, error)

	Stop(instanceID string) error

	Close() error
}

type Option func(*manager)

func WithStderr(w io.Writer) Option { return func(m *manager) { m.stderr = w } }

func WithKillGrace(d time.Duration) Option { return func(m *manager) { m.killGrace = d } }

func WithJournalSize(n int) Option { return func(m *manager) { m.journalSize = n } }

func WithRestart(limit int) Option {
	return func(m *manager) {
		m.restartEnabled = true
		m.restartLimit = limit
	}
}

func WithEventSink(sink EventSink) Option { return func(m *manager) { m.sink = sink } }

func WithPermissionFallback(fn PermissionFallback) Option {
	return func(m *manager) { m.permissionFallback = fn }
}

func WithSelfExecutable(path string) Option {
	return func(m *manager) { m.selfExecutable = path }
}

type manager struct {
	agents      agentregistryservice.Service
	stderr      io.Writer
	killGrace   time.Duration
	journalSize int

	selfExecutable string

	restartEnabled     bool
	restartLimit       int
	sink               EventSink
	permissionFallback PermissionFallback

	rootCtx    context.Context
	rootCancel context.CancelFunc

	mu        sync.Mutex
	instances map[string]*instance
	closed    bool
}

func New(agents agentregistryservice.Service, opts ...Option) Manager {
	rootCtx, rootCancel := context.WithCancel(context.Background())
	m := &manager{
		agents:      agents,
		stderr:      io.Discard,
		killGrace:   defaultKillGrace,
		journalSize: defaultJournalSize,
		rootCtx:     rootCtx,
		rootCancel:  rootCancel,
		instances:   make(map[string]*instance),
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

var _ Manager = (*manager)(nil)

func (m *manager) emit(ev Event) {
	if m.sink != nil {
		m.sink(ev)
	}
}

func (m *manager) Start(ctx context.Context, agentName, cwd string) (string, error) {
	if agentName == "" {
		return "", fmt.Errorf("agentinstance: agentName is required")
	}
	agent, err := m.agents.GetByName(ctx, agentName)
	if err != nil {
		return "", fmt.Errorf("agentinstance: resolve agent %q: %w", agentName, err)
	}
	return m.StartResolved(ctx, agent, cwd)
}

func (m *manager) StartResolved(ctx context.Context, agent *runtimetypes.Agent, cwd string) (string, error) {
	_ = ctx
	if agent == nil {
		return "", fmt.Errorf("agentinstance: agent is required")
	}
	switch agent.Kind {
	case runtimetypes.AgentKindExternalACP:
		cfg, err := agent.ExternalACPConfig()
		if err != nil {
			return "", fmt.Errorf("agentinstance: agent %q: %w", agent.Name, err)
		}
		if cfg.Cwd == "" {
			cfg.Cwd = cwd
		}
		spawner := &agenthost.ExternalACPAgent{
			Config:    *cfg,
			Stderr:    m.stderr,
			KillGrace: m.killGrace,
		}
		return m.bringUp(agent, spawner)
	case runtimetypes.AgentKindChain:
		spawner, err := m.chainSpawner(agent, cwd)
		if err != nil {
			return "", err
		}
		return m.bringUp(agent, spawner)
	default:
		return "", fmt.Errorf("agentinstance: agent %q has unsupported kind %q", agent.Name, agent.Kind)
	}
}

func (m *manager) chainSpawner(agent *runtimetypes.Agent, cwd string) (agenthost.Agent, error) {
	cfg, err := agent.ChainConfig()
	if err != nil {
		return nil, fmt.Errorf("agentinstance: agent %q: %w", agent.Name, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("agentinstance: agent %q: %w", agent.Name, err)
	}
	self := m.selfExecutable
	if self == "" {
		self, err = os.Executable()
		if err != nil {
			return nil, fmt.Errorf("agentinstance: agent %q: resolve this executable to run its chain: %w", agent.Name, err)
		}
	}
	return &agenthost.ExternalACPAgent{
		Config: runtimetypes.ExternalACPConfig{
			Transport: runtimetypes.ExternalACPTransportStdio,
			Command:   self,
			Args:      []string{ChainACPSubcommand},
			Cwd:       cwd,
			Env:       map[string]string{ChainPathEnvVar: cfg.Path},
		},
		SelfSpawn: true,
		Stderr:    m.stderr,
		KillGrace: m.killGrace,
	}, nil
}

func (m *manager) bringUp(agent *runtimetypes.Agent, spawner agenthost.Agent) (string, error) {
	id := uuid.NewString()
	inst := newInstance(instanceConfig{
		id:             id,
		agentID:        agent.ID,
		agentName:      agent.Name,
		kind:           agent.Kind,
		rootCtx:        m.rootCtx,
		spawner:        spawner,
		journalSize:    m.journalSize,
		restartEnabled: m.restartEnabled,
		restartLimit:   m.restartLimit,
		onState: func(state string) {
			m.emit(Event{Kind: EventStateChange, InstanceID: id, AgentID: agent.ID, AgentName: agent.Name, State: state, Time: time.Now().UTC()})
		},
		onAttach: func(sessionID libacp.SessionID, viewerID string, controller bool) {
			m.emit(Event{Kind: EventAttach, InstanceID: id, AgentID: agent.ID, AgentName: agent.Name, SessionID: sessionID, ViewerID: viewerID, Controller: controller, Time: time.Now().UTC()})
		},
		onDetach: func(sessionID libacp.SessionID, viewerID string) {
			m.emit(Event{Kind: EventDetach, InstanceID: id, AgentID: agent.ID, AgentName: agent.Name, SessionID: sessionID, ViewerID: viewerID, Time: time.Now().UTC()})
		},
		onUnsupervisedDeny: func(sessionID libacp.SessionID) {
			m.emit(Event{Kind: EventUnsupervisedDeny, InstanceID: id, AgentID: agent.ID, AgentName: agent.Name, SessionID: sessionID, Time: time.Now().UTC()})
		},
		onUnsupervisedRequest: m.unattendedPermissionAnswerer(id, agent),
	})

	if err := inst.start(); err != nil {
		return "", fmt.Errorf("agentinstance: start agent %q: %w", agent.Name, err)
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		_ = inst.stop()
		return "", fmt.Errorf("agentinstance: manager is closed")
	}
	m.instances[id] = inst
	m.mu.Unlock()
	return id, nil
}

func (m *manager) unattendedPermissionAnswerer(instanceID string, agent *runtimetypes.Agent) func(context.Context, libacp.RequestPermissionRequest) (libacp.RequestPermissionResponse, error) {
	fn := m.permissionFallback
	if fn == nil {
		return nil
	}
	agentID, agentName := agent.ID, agent.Name
	return func(ctx context.Context, req libacp.RequestPermissionRequest) (libacp.RequestPermissionResponse, error) {
		return fn(ctx, UnattendedPermission{
			InstanceID: instanceID,
			AgentID:    agentID,
			AgentName:  agentName,
			SessionID:  req.SessionID,
			Request:    req,
		})
	}
}

func (m *manager) Attach(ctx context.Context, instanceID string, sessionID libacp.SessionID, viewer Viewer) (bool, error) {
	m.mu.Lock()
	inst, ok := m.instances[instanceID]
	m.mu.Unlock()
	if !ok {
		return false, fmt.Errorf("agentinstance: %q: %w", instanceID, ErrNotFound)
	}
	return inst.attach(ctx, sessionID, viewer)
}

func (m *manager) Detach(instanceID string, sessionID libacp.SessionID, viewerID string) error {
	m.mu.Lock()
	inst, ok := m.instances[instanceID]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("agentinstance: %q: %w", instanceID, ErrNotFound)
	}
	return inst.detach(sessionID, viewerID)
}

func (m *manager) Get(instanceID string) (InstanceStatus, error) {
	m.mu.Lock()
	inst, ok := m.instances[instanceID]
	m.mu.Unlock()
	if !ok {
		return InstanceStatus{}, fmt.Errorf("agentinstance: %q: %w", instanceID, ErrNotFound)
	}
	return inst.status(), nil
}

func (m *manager) instance(instanceID string) (*instance, error) {
	m.mu.Lock()
	inst, ok := m.instances[instanceID]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("agentinstance: %q: %w", instanceID, ErrNotFound)
	}
	return inst, nil
}

func (m *manager) OpenSession(ctx context.Context, instanceID string, spec SessionSpec) (libacp.SessionID, error) {
	inst, err := m.instance(instanceID)
	if err != nil {
		return "", err
	}
	return inst.openSession(ctx, spec)
}

func (m *manager) Prompt(ctx context.Context, instanceID string, sessionID libacp.SessionID, prompt []libacp.ContentBlock) (libacp.StopReason, error) {
	inst, err := m.instance(instanceID)
	if err != nil {
		return "", err
	}
	return inst.promptSession(ctx, sessionID, prompt)
}

func (m *manager) DeliverToSession(ctx context.Context, sessionID libacp.SessionID, n libacp.SessionNotification) error {
	if sessionID == "" {
		return fmt.Errorf("agentinstance: sessionID is required")
	}
	m.mu.Lock()
	insts := make([]*instance, 0, len(m.instances))
	for _, inst := range m.instances {
		insts = append(insts, inst)
	}
	m.mu.Unlock()

	for _, inst := range insts {
		if inst.deliverToSession(ctx, sessionID, n) {
			return nil
		}
	}
	return fmt.Errorf("agentinstance: session %q: %w", sessionID, ErrNotFound)
}

func (m *manager) Cancel(instanceID string, sessionID libacp.SessionID) error {
	inst, err := m.instance(instanceID)
	if err != nil {
		return err
	}
	return inst.cancelSession(sessionID)
}

func (m *manager) CloseSession(instanceID string, sessionID libacp.SessionID) error {
	inst, err := m.instance(instanceID)
	if err != nil {
		return err
	}
	return inst.closeSession(sessionID)
}

func (m *manager) SetConfigOption(ctx context.Context, instanceID string, sessionID libacp.SessionID, configID string, value libacp.SessionConfigOptionValue) error {
	inst, err := m.instance(instanceID)
	if err != nil {
		return err
	}
	return inst.setConfigOption(ctx, sessionID, configID, value)
}

func (m *manager) SessionConfigOptions(instanceID string, sessionID libacp.SessionID) ([]libacp.SessionConfigOption, error) {
	inst, err := m.instance(instanceID)
	if err != nil {
		return nil, err
	}
	return inst.sessionConfigOptions(sessionID), nil
}

func (m *manager) AvailableCommands(instanceID string, sessionID libacp.SessionID) ([]libacp.AvailableCommand, error) {
	inst, err := m.instance(instanceID)
	if err != nil {
		return nil, err
	}
	return inst.availableCommands(sessionID), nil
}

func (m *manager) SessionAgentText(instanceID string, sessionID libacp.SessionID) (string, bool) {
	inst, err := m.instance(instanceID)
	if err != nil {
		return "", false
	}
	return inst.agentText(sessionID)
}

func (m *manager) SessionJournal(instanceID string, sessionID libacp.SessionID) ([]libacp.SessionNotification, string, bool) {
	inst, err := m.instance(instanceID)
	if err != nil {
		return nil, "", false
	}
	return inst.sessionJournal(sessionID)
}

func (m *manager) List(ctx context.Context) ([]FleetEntry, error) {
	m.mu.Lock()
	live := make([]*instance, 0, len(m.instances))
	for _, inst := range m.instances {
		live = append(live, inst)
	}
	m.mu.Unlock()

	byAgent := make(map[string][]InstanceStatus)
	for _, inst := range live {
		st := inst.status()
		byAgent[st.AgentID] = append(byAgent[st.AgentID], st)
	}

	declared, err := m.listDeclared(ctx)
	if err != nil {
		return nil, fmt.Errorf("agentinstance: list declared agents: %w", err)
	}

	entries := make([]FleetEntry, 0, len(declared))
	seen := make(map[string]bool, len(declared))
	for _, a := range declared {
		seen[a.ID] = true
		entries = append(entries, FleetEntry{
			AgentID:   a.ID,
			AgentName: a.Name,
			Kind:      a.Kind,
			Instances: byAgent[a.ID],
		})
	}
	for agentID, insts := range byAgent {
		if seen[agentID] {
			continue
		}
		entries = append(entries, FleetEntry{
			AgentID:   agentID,
			AgentName: insts[0].AgentName,
			Kind:      insts[0].Kind,
			Instances: insts,
		})
	}
	return entries, nil
}

func (m *manager) listDeclared(ctx context.Context) ([]*runtimetypes.Agent, error) {
	const page = 200
	var all []*runtimetypes.Agent
	var cursor *time.Time
	for {
		batch, err := m.agents.List(ctx, cursor, page)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < page {
			break
		}
		last := batch[len(batch)-1].CreatedAt
		if cursor != nil && !last.Before(*cursor) {
			break
		}
		cursor = &last
	}
	return all, nil
}

func (m *manager) Stop(instanceID string) error {
	m.mu.Lock()
	inst, ok := m.instances[instanceID]
	if ok {
		delete(m.instances, instanceID)
	}
	m.mu.Unlock()
	if !ok {
		return nil
	}
	return inst.stop()
}

func (m *manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	insts := make([]*instance, 0, len(m.instances))
	for id, inst := range m.instances {
		insts = append(insts, inst)
		delete(m.instances, id)
	}
	m.mu.Unlock()

	var errs []error
	for _, inst := range insts {
		if err := inst.stop(); err != nil {
			errs = append(errs, err)
		}
	}
	m.rootCancel()
	return errors.Join(errs...)
}
