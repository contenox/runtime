// Package modeldconn is the runtime's client seam to the modeld / embedded daemon:
// it resolves the current lease leader (via modeldprobe), dials it over the gRPC
// transport, and opens sessions.
package modeldconn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/modeld/snapshotauth"
	"github.com/contenox/runtime/internal/modeldinstall"
	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/internal/transport"
	transportgrpc "github.com/contenox/runtime/internal/transport/grpc"
	"github.com/contenox/runtime/runtime/modeldprobe"
	"github.com/google/uuid"
)

// dataRoot is the contenox data root the probe inspects. It defaults to the
// standard location and is overridable (tests, non-default installs).
var dataRoot string

// SetDataRoot overrides the data root used to locate the owner lease.
func SetDataRoot(root string) {
	dataRoot = root
	serveableMu.Lock()
	serveableBackend, serveableSeenAt = "", time.Time{}
	serveableMu.Unlock()
}

// DataRoot returns the configured local daemon data directory.
func DataRoot() string {
	if dataRoot != "" {
		return dataRoot
	}
	return modeldprobe.DefaultDataRoot()
}

func detector() *modeldprobe.Detector { return modeldprobe.New(dataRoot) }

const (
	snapshotDirEnv     = "CONTENOX_WARM_SNAPSHOT_DIR"
	snapshotDisableEnv = "CONTENOX_WARM_SNAPSHOT_DISABLE"
	snapshotSubdir     = "modeld-snapshots"
)

// SnapshotDir resolves the on-disk directory a backend's warm cache should
// persist durable session snapshots to.
func SnapshotDir(backend string) string {
	if os.Getenv(snapshotDisableEnv) != "" {
		return ""
	}
	root := os.Getenv(snapshotDirEnv)
	if root == "" {
		d := dataRoot
		if d == "" {
			d = modeldprobe.DefaultDataRoot()
		}
		root = filepath.Join(d, snapshotSubdir)
	}
	return filepath.Join(root, backend)
}

// Available is the cheap, offline check: is a model daemon currently holding a fresh lease?
func Available() bool { return detector().Detect().State == modeldprobe.StateRunning }

// Backend is the cheap, offline check for the inference backend the running owner serves.
func Backend() string {
	st := detector().Detect()
	if st.State != modeldprobe.StateRunning {
		return ""
	}
	return st.Backend
}

const serveableGraceWindow = 60 * time.Second

var (
	serveableMu      sync.Mutex
	serveableBackend string
	serveableSeenAt  time.Time
)

// ServeableBackend reports the inference backend modeld can serve, smoothed over
// brief lease gaps so local-model capability does not flap during a daemon restart.
func ServeableBackend() string { return serveableFrom(Backend(), time.Now()) }

func serveableFrom(live string, now time.Time) string {
	serveableMu.Lock()
	defer serveableMu.Unlock()
	if live != "" {
		serveableBackend, serveableSeenAt = live, now
		return live
	}
	if serveableBackend != "" && now.Sub(serveableSeenAt) <= serveableGraceWindow {
		return serveableBackend
	}
	serveableBackend = ""
	return ""
}

var (
	autoMu       sync.Mutex
	autoDisabled bool
	autoSpawned  *exec.Cmd
	autoDone     chan struct{}
	autoStarting chan struct{}
)

// SetDisabled turns automatic daemon startup on or off for this process.
func SetDisabled(disabled bool) {
	autoMu.Lock()
	autoDisabled = disabled
	autoMu.Unlock()
}

func autostartDisabled() bool {
	autoMu.Lock()
	defer autoMu.Unlock()
	return autoDisabled
}

// StopAutoStarted aborts an unfinished daemon startup. Ready shared owners outlive clients.
func StopAutoStarted() {
	autoMu.Lock()
	cmd := autoSpawned
	done := autoDone
	autoSpawned = nil
	autoDone = nil
	autoMu.Unlock()
	if cmd != nil && cmd.Process != nil {
		if err := cmd.Process.Signal(os.Interrupt); err == nil && done != nil {
			select {
			case <-done:
				return
			case <-time.After(2 * time.Second):
			}
		}
		_ = cmd.Process.Kill()
		if done != nil {
			select {
			case <-done:
			case <-time.After(time.Second):
			}
		}
	}
}

// ensureReadyWindow bounds how long EnsureDaemon waits for a freshly spawned
// daemon to answer its health probe.
const ensureReadyWindow = 5 * time.Second

// EnsureDaemon connects to the shared owner, starting it when no live owner holds the lease.
func EnsureDaemon(ctx context.Context) error {
	if autostartDisabled() {
		return nil
	}
	return ensureDaemon(ctx, modeldprobe.New(DataRootFromContext(ctx)))
}

// daemonProbe is the slice of the Detector EnsureDaemon depends on, so tests
// can stub the daemon state without touching the real lease.
type daemonProbe interface {
	Probe(context.Context) modeldprobe.Status
	Detect() modeldprobe.Status
	LauncherArgs(binary string) []string
}

func ensureDaemon(ctx context.Context, p daemonProbe) error {
	_, err := ensureDaemonStatus(ctx, p)
	return err
}

func readyDaemon(ctx context.Context) (modeldprobe.Status, error) {
	return readyDaemonWithProbe(ctx, modeldprobe.New(DataRootFromContext(ctx)))
}

func readyDaemonWithProbe(ctx context.Context, p daemonProbe) (modeldprobe.Status, error) {
	if err := ctx.Err(); err != nil {
		return modeldprobe.Status{}, err
	}
	if autostartDisabled() {
		st := p.Probe(ctx)
		return st, st.Err()
	}
	return ensureDaemonStatus(ctx, p)
}

func ensureDaemonStatus(ctx context.Context, p daemonProbe) (modeldprobe.Status, error) {
	for {
		if err := ctx.Err(); err != nil {
			return modeldprobe.Status{}, err
		}
		autoMu.Lock()
		if autoStarting == nil {
			autoStarting = make(chan struct{})
			autoMu.Unlock()
			break
		}
		pending := autoStarting
		autoMu.Unlock()
		select {
		case <-ctx.Done():
			return modeldprobe.Status{}, ctx.Err()
		case <-pending:
		}
	}
	defer func() { autoMu.Lock(); close(autoStarting); autoStarting = nil; autoMu.Unlock() }()

	st := p.Probe(ctx)
	if st.State == modeldprobe.StateRunning {
		return st, nil
	}

	stDetect := p.Detect()
	if stDetect.State == modeldprobe.StateRunning {
		return st, fmt.Errorf("the existing modeld owner is unreachable or incompatible; close its clients, then "+remedy.UpdateLocal+": %w", st.Err())
	}
	if stDetect.Binary == "" {
		return st, modeldprobe.ErrNotInstalled
	}

	StopAutoStarted()
	args := p.LauncherArgs(stDetect.Binary)
	cmd := modeldinstall.CommandContext(context.Background(), stDetect.Binary, args...)
	cmd.Env = append(cmd.Environ(), "CONTENOX_DATA_ROOT="+DataRootFromContext(ctx))
	if os.Getenv("CONTENOX_MODELD_BACKEND") == "" {
		backend, err := modeldprobe.PreferredBackend(DataRootFromContext(ctx))
		if err != nil {
			return st, fmt.Errorf("read managed modeld backend: %w", err)
		}
		if backend != "" {
			cmd.Env = append(cmd.Env, "CONTENOX_MODELD_BACKEND="+backend)
		}
	}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	modeldinstall.Detach(cmd)
	if err := cmd.Start(); err != nil {
		return st, fmt.Errorf("autostart model daemon: %w", err)
	}
	done := make(chan struct{})
	autoMu.Lock()
	autoSpawned, autoDone = cmd, done
	autoMu.Unlock()

	ready := false
	defer func() {
		if ready {
			autoMu.Lock()
			if autoSpawned == cmd {
				autoSpawned, autoDone = nil, nil
			}
			autoMu.Unlock()
		}
		if !ready {
			autoMu.Lock()
			if autoSpawned == cmd {
				autoSpawned = nil
			}
			autoMu.Unlock()
			_ = cmd.Process.Kill()
		}
	}()
	exited := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		close(done)
		autoMu.Lock()
		if autoSpawned == cmd {
			autoSpawned = nil
		}
		autoMu.Unlock()
		exited <- err
	}()

	deadline := time.Now().Add(ensureReadyWindow)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return st, ctx.Err()
		case err := <-exited:
			st = p.Probe(ctx)
			if st.State == modeldprobe.StateRunning {
				ready = true
				return st, nil
			}
			if err == nil {
				return st, fmt.Errorf("autostart model daemon: exited before readiness")
			}
			return st, fmt.Errorf("autostart model daemon: exited during startup: %w", err)
		case <-time.After(100 * time.Millisecond):
		}
		st = p.Probe(ctx)
		if st.State == modeldprobe.StateRunning {
			ready = true
			return st, nil
		}
	}
	return st, st.Err()
}

var (
	mu     sync.Mutex
	client *transportgrpc.Client
	key    string
)

func dial(endpoint, instance string) (*transportgrpc.Client, error) {
	k := endpoint + "|" + instance
	mu.Lock()
	defer mu.Unlock()
	if client != nil && key == k {
		return client, nil
	}
	if client != nil {
		_ = client.Close()
		client = nil
	}
	c, err := transportgrpc.DialLeader(endpoint, instance)
	if err != nil {
		return nil, err
	}
	client, key = c, k
	return c, nil
}

// ModeldTarget describes a specific modeld to talk to (local lease or remote).
type ModeldTarget struct {
	// leaseRoot is set only on targets resolved from this Runtime's local owner lease.
	leaseRoot string
	// standaloneConfig carries role options directly to a single-slot daemon,
	// which has no shared supervisor to apply an execution specification.
	standaloneConfig *transport.Config
	Allocation       *transport.NativeAllocation
	Permit           admission.Permit
	BackendID        string
	Endpoint         string
	Instance         string
	Execution        *transport.Execution
}

// ModelRef is the typed model handle the runtime passes to modeld.
type ModelRef struct {
	Name     string
	Type     string
	Digest   string
	Path     string
	Adapters []transport.AdapterSpec
}

// OpenSessionTarget opens using the target.
func OpenSessionTarget(ctx context.Context, target ModeldTarget, ref ModelRef, cfg transport.Config) (transport.Session, error) {
	if target.standaloneConfig != nil {
		cfg = *target.standaloneConfig
	}
	if target.Execution != nil {
		cfg.Execution = target.Execution
	}
	if cfg.SessionNamespace == "" {
		cfg.SessionNamespace = uuid.NewString()
	}
	if target.Endpoint == "" {
		if workers := WorkersFromContext(ctx); workers != nil {
			managed, release, err := workers.Acquire(ctx, WorkerSpec{Role: WorkerRole(ctx), Model: ref.Name, Backend: ref.Type, Digest: ref.Digest, Config: cfg})
			if err != nil {
				return nil, err
			}
			sess, err := OpenSessionTarget(ctx, managed, ref, cfg)
			if err != nil {
				stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				stopErr := workers.Retire(stopCtx, managed)
				cancel()
				if stopErr != nil {
					return nil, errors.Join(err, stopErr)
				}
				return nil, errors.Join(err, release())
			}
			return &workerSession{Session: sess, release: release, retire: func() error {
				stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				return workers.Retire(stopCtx, managed)
			}}, nil
		}
	}
	if target.Endpoint == "" {
		return OpenSession(ctx, ref, cfg)
	}
	ec, err := executionEndpoint(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("modeld target %s: %w", target.BackendID, err)
	}
	if ec.Backend == "shared" && cfg.Execution == nil {
		cfg.Execution, err = requestExecution(ctx, WorkerRole(ctx), ref.Type)
		if err != nil {
			return nil, err
		}
	}
	if err := protectExecution(ctx, target.Endpoint, &cfg); err != nil {
		return nil, err
	}
	req := openRequest(ec.InstanceID, ref, cfg)
	openCtx := snapshotContext(ctx, target.leaseRoot, target.Endpoint, ec.InstanceID, cfg.SessionNamespace)
	sess, err := ec.OpenSession(openCtx, req)
	if errors.Is(err, transport.ErrModelSwitchRequired) || errors.Is(err, transport.ErrModelNotActive) {
		if _, loadErr := ec.LoadModel(ctx, loadRequest(ec.InstanceID, ref, cfg)); loadErr != nil {
			return nil, loadErr
		}
		return ec.OpenSession(openCtx, req)
	}
	return sess, err
}

// DescribeTarget is the targeted equivalent of Describe.
func DescribeTarget(ctx context.Context, target ModeldTarget, ref ModelRef, cfg transport.Config) (transport.ModelInfo, error) {
	if target.Execution != nil {
		cfg.Execution = target.Execution
	}
	if target.Endpoint == "" {
		return Describe(ctx, ref, cfg)
	}
	ec, err := executionEndpoint(ctx, target)
	if err != nil {
		return transport.ModelInfo{}, fmt.Errorf("modeld target %s: %w", target.BackendID, err)
	}
	if ec.Backend == "shared" && cfg.Execution == nil {
		cfg.Execution, err = requestExecution(ctx, "inspection", ref.Type)
		if err != nil {
			return transport.ModelInfo{}, err
		}
	}
	return ec.Describe(ctx, transport.OpenSessionRequest{
		Fence:     transport.Fence{OwnerInstanceID: ec.InstanceID},
		ModelName: ref.Name,
		Type:      ref.Type,
		Digest:    ref.Digest,
		Path:      ref.Path,
		Config:    cfg,
		Adapters:  ref.Adapters,
	})
}

// EmbedTarget is the targeted equivalent of Embed.
func EmbedTarget(ctx context.Context, target ModeldTarget, ref ModelRef, cfg transport.Config, text string) (transport.EmbedResult, error) {
	if target.standaloneConfig != nil {
		cfg = *target.standaloneConfig
	}
	if target.Execution != nil {
		cfg.Execution = target.Execution
	}
	if target.Endpoint == "" {
		if workers := WorkersFromContext(ctx); workers != nil {
			managed, release, err := workers.Acquire(ctx, WorkerSpec{Role: "embedding", Model: ref.Name, Backend: ref.Type, Digest: ref.Digest, Config: cfg})
			if err != nil {
				return transport.EmbedResult{}, err
			}
			result, err := EmbedTarget(ctx, managed, ref, cfg, text)
			if err != nil {
				stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				stopErr := workers.Retire(stopCtx, managed)
				cancel()
				if stopErr != nil {
					return result, errors.Join(err, stopErr)
				}
			}
			return result, errors.Join(err, release())
		}
	}
	if target.Endpoint == "" {
		return Embed(ctx, ref, cfg, text)
	}
	ec, err := executionEndpoint(ctx, target)
	if err != nil {
		return transport.EmbedResult{}, fmt.Errorf("modeld target %s: %w", target.BackendID, err)
	}
	if ec.Backend == "shared" && cfg.Execution == nil {
		cfg.Execution, err = requestExecution(ctx, "embedding", ref.Type)
		if err != nil {
			return transport.EmbedResult{}, err
		}
	}
	if err := protectExecution(ctx, target.Endpoint, &cfg); err != nil {
		return transport.EmbedResult{}, err
	}
	return ec.Embed(ctx, transport.EmbedRequest{
		Fence:     transport.Fence{OwnerInstanceID: ec.InstanceID},
		ModelName: ref.Name,
		Type:      ref.Type,
		Digest:    ref.Digest,
		Path:      ref.Path,
		Config:    cfg,
		Text:      text,
	})
}

// RenderMediaTarget runs a bounded diffusion request on the model owner or its admitted worker.
func RenderMediaTarget(ctx context.Context, target ModeldTarget, request transport.MediaRequest) (transport.MediaResult, error) {
	if err := request.Settings.Validate(); err != nil {
		return transport.MediaResult{}, err
	}
	ctx = WithWorkerRole(ctx, request.Settings.Role())
	if target.Endpoint == "" {
		status, err := readyDaemon(ctx)
		if err != nil {
			return transport.MediaResult{}, err
		}
		target.Endpoint = status.Endpoint
	}
	client, err := executionEndpoint(ctx, target)
	if err != nil {
		return transport.MediaResult{}, err
	}
	if target.Execution != nil {
		request.Config.Execution = target.Execution
	}
	if client.Backend == "shared" && request.Config.Execution == nil {
		request.Config.Execution, err = requestExecution(ctx, request.Settings.Role(), request.Type)
		if err != nil {
			return transport.MediaResult{}, err
		}
	}
	if err := protectExecution(ctx, target.Endpoint, &request.Config); err != nil {
		return transport.MediaResult{}, err
	}
	result, err := client.RenderMedia(ctx, request)
	if err == nil {
		err = RecordExecution(ctx, ModelRef{Name: request.ModelName, Type: request.Type, Digest: request.Digest}, result.Execution)
	}
	return result, err
}

// ListModelsTarget lists models on the target.
func ListModelsTarget(ctx context.Context, target ModeldTarget) ([]transport.NodeModel, error) {
	if target.Endpoint == "" {
		st, err := readyDaemon(ctx)
		if err != nil {
			return nil, err
		}
		ec, err := Endpoint(ctx, "local", st.Endpoint)
		if err != nil {
			return nil, err
		}
		return ec.ListModels(ctx)
	}
	ec, err := Endpoint(ctx, target.BackendID, target.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("modeld target %s: %w", target.BackendID, err)
	}
	return ec.ListModels(ctx)
}

// PushModelTarget pushes a model stream to the target.
func PushModelTarget(ctx context.Context, target ModeldTarget, manifest transport.PushManifest, r io.Reader) (transport.PushResult, error) {
	if target.Endpoint == "" {
		st, err := readyDaemon(ctx)
		if err != nil {
			return transport.PushResult{}, err
		}
		ec, err := Endpoint(ctx, "local", st.Endpoint)
		if err != nil {
			return transport.PushResult{}, err
		}
		return ec.PushModel(ctx, manifest, r)
	}
	ec, err := Endpoint(ctx, target.BackendID, target.Endpoint)
	if err != nil {
		return transport.PushResult{}, fmt.Errorf("modeld target %s: %w", target.BackendID, err)
	}
	return ec.PushModel(ctx, manifest, r)
}

// OpenSession opens a session, auto-starting the local daemon if necessary.
func OpenSession(ctx context.Context, ref ModelRef, cfg transport.Config) (transport.Session, error) {
	if cfg.SessionNamespace == "" {
		cfg.SessionNamespace = uuid.NewString()
	}
	st, err := readyDaemon(ctx)
	if err != nil {
		return nil, err
	}
	if st.Backend == "shared" && cfg.Execution == nil {
		cfg.Execution, err = requestExecution(ctx, WorkerRole(ctx), ref.Type)
		if err != nil {
			return nil, err
		}
	}
	c, err := dial(st.Endpoint, st.Instance)
	if err != nil {
		return nil, err
	}
	if err := protectExecution(ctx, LocalSentinel, &cfg); err != nil {
		return nil, err
	}
	req := openRequest(st.Instance, ref, cfg)
	openCtx := snapshotContext(ctx, DataRootFromContext(ctx), st.Endpoint, st.Instance, cfg.SessionNamespace)
	sess, err := c.OpenSession(openCtx, req)
	if errors.Is(err, transport.ErrModelSwitchRequired) || errors.Is(err, transport.ErrModelNotActive) {
		if _, loadErr := c.LoadModel(ctx, loadRequest(st.Instance, ref, cfg)); loadErr != nil {
			return nil, loadErr
		}
		return c.OpenSession(openCtx, req)
	}
	return sess, err
}

// Status returns the live modeld status.
func Status(ctx context.Context) (transport.DaemonStatus, error) {
	st := modeldprobe.New(DataRootFromContext(ctx)).Probe(ctx)
	if st.State != modeldprobe.StateRunning {
		return transport.DaemonStatus{}, st.Err()
	}
	c, err := dial(st.Endpoint, st.Instance)
	if err != nil {
		return transport.DaemonStatus{}, err
	}
	return c.Status(ctx)
}

// LoadModel explicitly activates the single modeld slot.
func LoadModel(ctx context.Context, ref ModelRef, cfg transport.Config, expectedGeneration ...uint64) (transport.ActiveModel, error) {
	st, err := readyDaemon(ctx)
	if err != nil {
		return transport.ActiveModel{}, err
	}
	c, err := dial(st.Endpoint, st.Instance)
	if err != nil {
		return transport.ActiveModel{}, err
	}
	req := loadRequest(st.Instance, ref, cfg)
	if len(expectedGeneration) > 0 {
		req.ExpectedGeneration = expectedGeneration[0]
	}
	return c.LoadModel(ctx, req)
}

// StopModel unloads the named model using the observed owner and worker or slot identity.
func StopModel(ctx context.Context, name string) error {
	st := modeldprobe.New(DataRootFromContext(ctx)).Probe(ctx)
	if st.State != modeldprobe.StateRunning {
		return st.Err()
	}
	c, err := dial(st.Endpoint, st.Instance)
	if err != nil {
		return err
	}
	status, err := c.Status(ctx)
	if err != nil {
		return err
	}
	if status.Backend == "shared" {
		var instances []string
		for _, worker := range status.Workers {
			if worker.Spec.Model != name {
				continue
			}
			if worker.Instance == "" || worker.References > 0 || worker.QueuedForeground > 0 || worker.QueuedBackground > 0 {
				return fmt.Errorf("model %q: %w", name, transport.ErrModelBusy)
			}
			instances = append(instances, worker.Instance)
		}
		if len(instances) == 0 {
			return fmt.Errorf("model %q is not resident", name)
		}
		for _, instance := range instances {
			if err := c.UnloadModel(ctx, transport.UnloadModelRequest{
				Fence:          transport.Fence{OwnerInstanceID: st.Instance},
				WorkerInstance: instance,
			}); err != nil {
				return err
			}
		}
		return nil
	}
	if status.Active == nil || status.Active.ModelName != name {
		return fmt.Errorf("model %q is not resident", name)
	}
	return c.UnloadModel(ctx, transport.UnloadModelRequest{
		Fence:              transport.Fence{OwnerInstanceID: st.Instance},
		ExpectedGeneration: status.Active.Generation,
	})
}

// UnloadModel releases the active modeld slot.
func UnloadModel(ctx context.Context, expectedGeneration uint64) error {
	st := modeldprobe.New(DataRootFromContext(ctx)).Probe(ctx)
	if st.State != modeldprobe.StateRunning {
		return st.Err()
	}
	c, err := dial(st.Endpoint, st.Instance)
	if err != nil {
		return err
	}
	return c.UnloadModel(ctx, transport.UnloadModelRequest{
		Fence:              transport.Fence{OwnerInstanceID: st.Instance},
		ExpectedGeneration: expectedGeneration,
	})
}

type persistedNamespaceKey struct{}

// WithPersistedNamespace marks the cache key of a Runtime-owned persisted session.
func WithPersistedNamespace(ctx context.Context, namespace string) context.Context {
	return context.WithValue(ctx, persistedNamespaceKey{}, namespace)
}

func snapshotContext(ctx context.Context, leaseRoot, endpoint, instance, namespace string) context.Context {
	persisted, _ := ctx.Value(persistedNamespaceKey{}).(string)
	if leaseRoot == "" || namespace == "" || persisted != namespace {
		return ctx
	}
	addr, err := netip.ParseAddrPort(endpoint)
	if err != nil || !addr.Addr().IsLoopback() {
		return ctx
	}
	return transportgrpc.WithSnapshotProof(ctx, snapshotauth.Proof(leaseRoot, namespace, instance))
}

func openRequest(owner string, ref ModelRef, cfg transport.Config) transport.OpenSessionRequest {
	return transport.OpenSessionRequest{
		Fence:     transport.Fence{OwnerInstanceID: owner},
		ModelName: ref.Name,
		Type:      ref.Type,
		Digest:    ref.Digest,
		Path:      ref.Path,
		Config:    cfg,
		Adapters:  ref.Adapters,
	}
}

func loadRequest(owner string, ref ModelRef, cfg transport.Config) transport.LoadModelRequest {
	return transport.LoadModelRequest{
		Fence:     transport.Fence{OwnerInstanceID: owner},
		ModelName: ref.Name,
		Type:      ref.Type,
		Digest:    ref.Digest,
		Path:      ref.Path,
		Config:    cfg,
		Adapters:  ref.Adapters,
	}
}

// Describe returns model capabilities from metadata.
func Describe(ctx context.Context, ref ModelRef, cfg transport.Config) (transport.ModelInfo, error) {
	st, err := readyDaemon(ctx)
	if err != nil {
		return transport.ModelInfo{}, err
	}
	if st.Backend == "shared" && cfg.Execution == nil {
		cfg.Execution, err = requestExecution(ctx, "inspection", ref.Type)
		if err != nil {
			return transport.ModelInfo{}, err
		}
	}
	c, err := dial(st.Endpoint, st.Instance)
	if err != nil {
		return transport.ModelInfo{}, err
	}
	return c.Describe(ctx, transport.OpenSessionRequest{
		Fence:     transport.Fence{OwnerInstanceID: st.Instance},
		ModelName: ref.Name,
		Type:      ref.Type,
		Digest:    ref.Digest,
		Path:      ref.Path,
		Config:    cfg,
		Adapters:  ref.Adapters,
	})
}

// Embed computes embeddings for text.
func Embed(ctx context.Context, ref ModelRef, cfg transport.Config, text string) (transport.EmbedResult, error) {
	st, err := readyDaemon(ctx)
	if err != nil {
		return transport.EmbedResult{}, err
	}
	if st.Backend == "shared" && cfg.Execution == nil {
		cfg.Execution, err = requestExecution(ctx, "embedding", ref.Type)
		if err != nil {
			return transport.EmbedResult{}, err
		}
	}
	c, err := dial(st.Endpoint, st.Instance)
	if err != nil {
		return transport.EmbedResult{}, err
	}
	if err := protectExecution(ctx, LocalSentinel, &cfg); err != nil {
		return transport.EmbedResult{}, err
	}
	return c.Embed(ctx, transport.EmbedRequest{
		Fence:     transport.Fence{OwnerInstanceID: st.Instance},
		ModelName: ref.Name,
		Type:      ref.Type,
		Digest:    ref.Digest,
		Path:      ref.Path,
		Config:    cfg,
		Text:      text,
	})
}
