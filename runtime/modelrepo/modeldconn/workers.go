package modeldconn

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/internal/modeldinstall"
	"github.com/contenox/runtime/internal/transport"
	transportgrpc "github.com/contenox/runtime/internal/transport/grpc"
	"github.com/contenox/runtime/runtime/modeldprobe"
	"github.com/google/uuid"
)

// WorkerSpec selects the native artifact and reviewed allocation settings.
type WorkerSpec = transport.WorkerSpec

// WorkerStatus reports the shared owner's managed worker state.
type WorkerStatus = transport.WorkerStatus

type workerHandle struct {
	status modeldprobe.Status
	done   <-chan struct{}
	stop   func() error
	result func() error
}

type workerEntry struct {
	prepareMu     sync.Mutex
	resolved      *transport.NativeAllocation
	directory     string
	configuration []byte
	spec          WorkerSpec
	ready         chan struct{}
	stopped       chan struct{}
	cancel        context.CancelFunc
	handle        workerHandle
	err           error
	stopErr       error
	refs          int
	draining      bool
}

// Workers routes clients to the shared owner or manages that owner's native children.
type Workers struct {
	normalized      map[string]*workerEntry
	aliases         map[string]*workerEntry
	devices         map[string]*workerDevice
	mu              sync.Mutex
	installMu       sync.Mutex
	ctx             context.Context
	cancel          context.CancelFunc
	root            string
	owner           string
	entries         map[string]*workerEntry
	options         map[string]WorkerOptions
	loadOptions     func(context.Context, string) (WorkerOptions, error)
	loadPolicy      func(context.Context) (admission.Config, bool, error)
	loadCompletion  func(context.Context, string) (CompletionPlan, error)
	retired         []*workerEntry
	closed          bool
	wg              sync.WaitGroup
	launch          func(context.Context, WorkerSpec, string) (workerHandle, error)
	scheduler       admission.Service
	ownerManaged    bool
	shared          bool
	clientEndpoints map[string]bool
}

// NewWorkers creates a lazy worker manager sharing models under root.
func NewWorkers(ctx context.Context, root string) *Workers {
	ctx, cancel := context.WithCancel(ctx)
	if root == "" {
		root = DataRoot()
	}
	m := &Workers{ctx: ctx, cancel: cancel, root: root, owner: uuid.NewString(), entries: make(map[string]*workerEntry), options: make(map[string]WorkerOptions)}
	m.launch = m.launchProcess
	m.loadOptions = func(context.Context, string) (WorkerOptions, error) { return WorkerOptions{}, nil }
	m.loadPolicy = func(context.Context) (admission.Config, bool, error) { return admission.Config{}, false, nil }
	m.loadCompletion = func(context.Context, string) (CompletionPlan, error) {
		return CompletionPlan{Protection: transport.CompletionProtection{State: "unconfigured"}}, nil
	}
	return m
}

// NewConfiguredWorkers reads the requested role's settings when local execution is acquired.
func NewConfiguredWorkers(ctx context.Context, root string, load func(context.Context, string) (WorkerOptions, error)) (*Workers, error) {
	if ctx == nil {
		return nil, errors.New("worker context is required")
	}
	if load == nil {
		return nil, errors.New("worker settings source is required")
	}
	m := NewWorkers(ctx, root)
	m.loadOptions = load
	m.shared = true
	return m, nil
}

// NewOwnerWorkers creates the shared owner's child pool using the existing admission service.
func NewOwnerWorkers(ctx context.Context, root, epoch string, scheduler admission.Service) (*Workers, error) {
	if ctx == nil || root == "" || epoch == "" || scheduler == nil {
		return nil, errors.New("owner workers require context, data root, epoch and admission")
	}
	m := NewWorkers(ctx, root)
	m.owner, m.ownerManaged, m.scheduler = epoch, true, scheduler
	return m, nil
}

// SetScheduler supplies admission shared by the intended worker domain before use.
func (m *Workers) SetScheduler(scheduler admission.Service) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scheduler = scheduler
}

// Scheduler returns the shared admission service used by this manager.
func (m *Workers) Scheduler() admission.Service {
	if m.shared {
		return sharedScheduler{m}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.scheduler
}

// Acquire retains a role's worker until release, coalescing concurrent startup.
func (m *Workers) Acquire(ctx context.Context, spec WorkerSpec) (ModeldTarget, func() error, error) {
	if err := ctx.Err(); err != nil {
		return ModeldTarget{}, nil, err
	}
	if !validWorkerRole(spec.Role) {
		return ModeldTarget{}, nil, fmt.Errorf("modeld: invalid worker role %q", spec.Role)
	}
	if spec.Model == "" || (spec.Backend != "llama" && spec.Backend != "openvino") {
		return ModeldTarget{}, nil, fmt.Errorf("modeld: worker requires a model and llama/openvino backend")
	}
	options, err := m.roleOptions(ctx, spec.Role)
	if err != nil {
		return ModeldTarget{}, nil, err
	}
	if options.Backend != "" && options.Backend != spec.Backend {
		return ModeldTarget{}, nil, fmt.Errorf("model %s requires %s, but %s worker is configured for %s", spec.Model, spec.Backend, spec.Role, options.Backend)
	}
	if spec.Config.Target.Mode == "" {
		spec.Config.Target = options.Target.Normalize()
	}
	if err := spec.Config.Target.Validate(); err != nil {
		return ModeldTarget{}, nil, err
	}
	spec.Config = (transport.Execution{Context: options.Context, Threads: options.Threads, Batch: options.Batch, KVCacheType: options.KVCacheType}).ApplySettings(spec.Config)
	if spec.Memory == "" {
		spec.Memory = options.Memory
	}
	if spec.Reserve == "" {
		spec.Reserve = options.Reserve
	}
	if spec.ColdMemory == "" {
		spec.ColdMemory = options.ColdMemory
	}
	if spec.IdleTTL == "" {
		spec.IdleTTL = options.IdleTTL
	}
	if spec.Headroom == "" {
		spec.Headroom = options.Headroom
	}
	if spec.ComputeReserve == "" {
		spec.ComputeReserve = options.ComputeReserve
	}
	if spec.MinHotContext == "" {
		spec.MinHotContext = options.MinHotContext
	}
	if err := (WorkerOptions{Backend: spec.Backend, Memory: spec.Memory, Reserve: spec.Reserve, ColdMemory: spec.ColdMemory, IdleTTL: spec.IdleTTL, Headroom: spec.Headroom, ComputeReserve: spec.ComputeReserve, MinHotContext: spec.MinHotContext}).Validate(); err != nil {
		return ModeldTarget{}, nil, err
	}
	if m.shared {
		return m.acquireShared(ctx, spec)
	}
	var configuration []byte
	if m.ownerManaged {
		var err error
		if spec.Digest == "" {
			path, err := modelstore.Resolve(filepath.Join(m.root, "models"), spec.Model, spec.Backend, "")
			if err != nil {
				return ModeldTarget{}, nil, err
			}
			spec.Digest, err = modelstore.ArtifactDigest(path, spec.Backend)
			if err != nil {
				return ModeldTarget{}, nil, err
			}
		}
		configuration, err = workerConfiguration(m.root)
		if err != nil {
			return ModeldTarget{}, nil, err
		}
		spec.ConfigurationDigest = fmt.Sprintf("%x", sha256.Sum256(configuration))
	}
	identity := spec
	if m.ownerManaged {
		identity.Role = ""
		identity.Config.SessionNamespace = ""
		identity.Config.Execution = nil
		identity.Config.AdmissionPermit = admission.Permit{}
		if identity.Config.Operation == "autocomplete" || identity.Config.Operation == "next-edit" {
			identity.Config.Operation = "chat"
		}
	}
	b, err := json.Marshal(identity)
	if err != nil {
		return ModeldTarget{}, nil, err
	}
	key := fmt.Sprintf("%s-%x", identity.Role, sha256.Sum256(b))
	m.mu.Lock()
	if m.closed || m.ctx.Err() != nil {
		m.mu.Unlock()
		return ModeldTarget{}, nil, errors.New("modeld worker manager closed")
	}
	e := m.entries[key]
	if e == nil {
		e = m.aliases[key]
	}
	if e != nil && e.handle.done != nil {
		select {
		case <-e.handle.done:
			e.draining = true
			e = nil
		default:
		}
	}
	if e == nil || e.draining {
		if previous := m.entries[key]; previous != nil {
			m.retired = append(m.retired, previous)
		}
		for _, old := range m.entries {
			if !m.ownerManaged && old.spec.Role == spec.Role {
				old.draining = true
				if old.refs == 0 {
					old.cancel()
				}
			}
		}
		workerCtx, cancel := context.WithCancel(m.ctx)
		e = &workerEntry{spec: spec, configuration: configuration, ready: make(chan struct{}), stopped: make(chan struct{}), cancel: cancel}
		m.entries[key] = e
		m.wg.Add(1)
		go m.start(workerCtx, key, e)
	}
	e.refs++
	m.mu.Unlock()
	var once sync.Once
	release := func() error { once.Do(func() { m.release(e) }); return nil }
	select {
	case <-ctx.Done():
		release()
		return ModeldTarget{}, nil, ctx.Err()
	case <-e.ready:
	}
	m.mu.Lock()
	st, startErr := e.handle.status, e.err
	m.mu.Unlock()
	if startErr != nil {
		release()
		return ModeldTarget{}, nil, startErr
	}
	if err := ctx.Err(); err != nil {
		release()
		return ModeldTarget{}, nil, err
	}
	var allocation *transport.NativeAllocation
	if m.ownerManaged {
		prepared, resolved, err := m.prepare(ctx, key, e, spec)
		if err != nil {
			release()
			return ModeldTarget{}, nil, err
		}
		if prepared != e {
			release()
			e = prepared
			once = sync.Once{}
		}
		allocation = &resolved
		m.mu.Lock()
		st = e.handle.status
		m.mu.Unlock()
	}
	target := ModeldTarget{BackendID: m.owner + "/" + st.Instance, Endpoint: st.Endpoint, Instance: st.Instance, Allocation: allocation}
	m.mu.Lock()
	scheduler := m.scheduler
	m.mu.Unlock()
	if scheduler == nil {
		return target, release, nil
	}
	if err := scheduler.Register(ctx, admission.Worker{ID: target.BackendID, OwnerEpoch: st.Instance, Ready: true}); err != nil {
		release()
		return ModeldTarget{}, nil, err
	}
	if allocation != nil {
		resources, ok := scheduler.(admission.ResourceService)
		if !ok {
			release()
			return ModeldTarget{}, nil, errors.New("owner cannot prepare native resources; update local inference")
		}
		allocation.Resources.WorkerID, allocation.Resources.OwnerEpoch = target.BackendID, target.Instance
		if err := resources.Prepare(ctx, allocation.Resources); err != nil {
			release()
			return ModeldTarget{}, nil, err
		}
	}
	waitCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(m.ctx, cancel)
	defer func() { stop(); cancel() }()
	qos := admissionOptions(ctx)
	permit, err := scheduler.Acquire(waitCtx, admission.Request{WorkerID: target.BackendID, OwnerEpoch: st.Instance, Priority: qos.Priority, RequesterID: qos.RequesterID, Supersede: qos.Supersede})
	if err != nil {
		release()
		return ModeldTarget{}, nil, err
	}
	m.mu.Lock()
	invalid := e.err
	if m.closed || m.ctx.Err() != nil {
		invalid = errors.New("modeld worker manager closed")
	}
	select {
	case <-e.handle.done:
		invalid = errors.New("modeld worker exited while waiting for admission")
	default:
	}
	m.mu.Unlock()
	if invalid != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := scheduler.Release(cleanupCtx, permit)
		release()
		return ModeldTarget{}, nil, errors.Join(invalid, err)
	}
	var finishOnce sync.Once
	var finishErr error
	target.Permit = permit
	return target, func() error {
		finishOnce.Do(func() {
			releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			finishErr = scheduler.Release(releaseCtx, permit)
			release()
		})
		return finishErr
	}, nil
}

func (m *Workers) start(ctx context.Context, key string, e *workerEntry) {
	defer m.wg.Done()
	defer close(e.stopped)
	m.mu.Lock()
	e.directory = filepath.Join(m.root, "workers", m.owner, key+"-"+uuid.NewString())
	m.mu.Unlock()
	h, err := m.launchConfigured(ctx, e, e.directory)
	m.mu.Lock()
	e.handle, e.err = h, err
	close(e.ready)
	if err != nil && m.entries[key] == e {
		delete(m.entries, key)
	}
	m.mu.Unlock()
	if err != nil {
		return
	}
	select {
	case <-ctx.Done():
	case <-h.done:
		if ctx.Err() == nil {
			m.mu.Lock()
			e.err = fmt.Errorf("modeld %s exited unexpectedly", e.spec.Role)
			if h.result != nil {
				e.err = fmt.Errorf("%w: %v", e.err, h.result())
			}
			m.mu.Unlock()
		}
	}
	stopErr := h.stop()
	m.mu.Lock()
	if stopErr == nil {
		stopErr = m.releaseDevicesLocked(e)
	}
	scheduler := m.scheduler
	m.mu.Unlock()
	if scheduler != nil {
		updateCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		updateErr := scheduler.Update(updateCtx, admission.Worker{ID: m.owner + "/" + h.status.Instance, OwnerEpoch: h.status.Instance, Draining: true, Stopped: stopErr == nil})
		cancel()
		if updateErr != nil && !errors.Is(updateErr, admission.ErrWorkerNotFound) {
			stopErr = errors.Join(stopErr, updateErr)
		}
	}
	CloseEndpoint(m.owner + "/" + h.status.Instance)
	m.mu.Lock()
	if stopErr != nil {
		e.err = stopErr
	}
	e.stopErr = stopErr
	e.draining = true
	m.mu.Unlock()
}

// Retire stops an owned worker and waits for confirmed termination and admission cleanup.
func (m *Workers) Retire(ctx context.Context, target ModeldTarget) error {
	if m.shared {
		return nil
	}
	m.mu.Lock()
	var found *workerEntry
	for _, e := range m.entries {
		if m.owner+"/"+e.handle.status.Instance == target.BackendID && e.handle.status.Instance == target.Instance {
			found = e
			break
		}
	}
	if found == nil {
		for _, e := range m.retired {
			if m.owner+"/"+e.handle.status.Instance == target.BackendID && e.handle.status.Instance == target.Instance {
				found = e
				break
			}
		}
	}
	if found == nil {
		m.mu.Unlock()
		return fmt.Errorf("modeld worker is not owned by this runtime")
	}
	found.draining = true
	found.cancel()
	m.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-found.stopped:
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return found.stopErr
}

// StopIdle stops a selected idle worker without interrupting another client's requests.
func (m *Workers) StopIdle(ctx context.Context, instance string) error {
	if instance == "" {
		return errors.New("select a worker to stop")
	}
	if m.shared {
		client, err := m.sharedEndpoint(ctx)
		if err != nil {
			return err
		}
		return client.UnloadModel(ctx, transport.UnloadModelRequest{Fence: transport.Fence{OwnerInstanceID: client.InstanceID}, WorkerInstance: instance})
	}
	m.mu.Lock()
	var selected *workerEntry
	for _, entry := range m.entries {
		if entry.handle.status.Instance == instance {
			selected = entry
			break
		}
	}
	if selected == nil {
		m.mu.Unlock()
		return admission.ErrWorkerNotFound
	}
	if selected.refs != 0 {
		m.mu.Unlock()
		return transport.ErrModelBusy
	}
	selected.draining = true
	selected.cancel()
	m.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-selected.stopped:
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if selected.stopErr == nil {
		for key, entry := range m.entries {
			if entry == selected {
				delete(m.entries, key)
			}
		}
	}
	return selected.stopErr
}

func (m *Workers) release(e *workerEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e.refs--
	if e.refs == 0 {
		select {
		case <-e.ready:
			if e.draining {
				e.cancel()
			}
		default:
			e.draining = true
			e.cancel()
		}
	}
}

// Snapshot returns current worker lifecycle state without network I/O.
func (m *Workers) Snapshot() []WorkerStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]WorkerStatus, 0, len(m.entries))
	for _, e := range m.entries {
		s := WorkerStatus{Spec: e.spec, State: "starting", Endpoint: e.handle.status.Endpoint, Instance: e.handle.status.Instance, References: e.refs}
		for id, device := range m.devices {
			if _, held := device.workers[e]; held {
				s.Devices = append(s.Devices, id)
			}
		}
		select {
		case <-e.ready:
			s.State = "ready"
		default:
		}
		if e.draining {
			s.State = "draining"
		}
		if e.err != nil {
			s.State = "failed"
			s.Error = e.err.Error()
		}
		out = append(out, s)
	}
	return out
}

// Status combines process lifecycle with the shared scheduler's queued/running state.
func (m *Workers) Status(ctx context.Context) ([]WorkerStatus, error) {
	if m.shared {
		client, err := m.sharedEndpoint(ctx)
		if err != nil {
			return nil, err
		}
		status, err := client.Status(ctx)
		return status.Workers, err
	}
	out := m.Snapshot()
	m.mu.Lock()
	scheduler, owner := m.scheduler, m.owner
	m.mu.Unlock()
	if scheduler == nil {
		return nil, fmt.Errorf("modeld admission scheduler unavailable")
	}
	snapshot, err := scheduler.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	for i := range out {
		for _, worker := range snapshot.Workers {
			if worker.ID != owner+"/"+out[i].Instance {
				continue
			}
			out[i].QueuedForeground = worker.QueuedForeground
			out[i].QueuedBackground = worker.QueuedBackground
			if out[i].State == "ready" {
				if worker.CancellationRequested {
					out[i].State = "cancelling"
				} else if worker.Active != nil {
					out[i].State = "running"
				} else if worker.QueuedForeground+worker.QueuedBackground > 0 {
					out[i].State = "queued"
				}
			}
		}
	}
	return out, nil
}

// Close closes this client's local and remote sessions or stops an owner's child pool.
// Closing a client never stops its detached owners or another client's work.
func (m *Workers) Close() error {
	if m.shared {
		m.mu.Lock()
		m.closed = true
		m.cancel()
		keys := make([]string, 0, len(m.clientEndpoints)+1)
		keys = append(keys, m.owner)
		for key := range m.clientEndpoints {
			keys = append(keys, key)
		}
		m.mu.Unlock()
		for _, key := range keys {
			CloseEndpoint(key)
		}
		return nil
	}
	m.mu.Lock()
	m.closed = true
	m.cancel()
	m.mu.Unlock()
	m.wg.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	var errs []error
	for _, e := range m.entries {
		if e.err != nil {
			errs = append(errs, e.err)
		}
	}
	for _, e := range m.retired {
		if e.err != nil {
			errs = append(errs, e.err)
		}
	}
	return errors.Join(errs...)
}

func (m *Workers) launchProcess(ctx context.Context, spec WorkerSpec, root string) (workerHandle, error) {
	binary, err := m.ensureWorkerBinary(ctx, spec.Backend)
	if err != nil {
		return workerHandle{}, err
	}
	args := workerServeArgs(spec, root, filepath.Join(m.root, modelstore.DefaultSubdir))
	instance, readyFile := "", ""
	if m.ownerManaged {
		if err := os.MkdirAll(root, 0o700); err != nil {
			return workerHandle{}, err
		}
		instance, readyFile = m.owner+"/"+uuid.NewString(), filepath.Join(root, "ready.json")
		args[0] = "worker"
		args = append(args, "--worker-instance", instance, "--ready-file", readyFile, "--owner-lease", filepath.Join(m.root, "modeld.lease"))
	}
	cmd := modeldinstall.CommandContext(context.Background(), binary, args...)
	cmd.Env = workerEnvironment(cmd.Environ(), root)
	cmd.Stderr = os.Stderr
	var parentPipe io.WriteCloser
	if m.ownerManaged {
		parentPipe, err = cmd.StdinPipe()
		if err != nil {
			return workerHandle{}, err
		}
	}
	if err := cmd.Start(); err != nil {
		if parentPipe != nil {
			_ = parentPipe.Close()
		}
		return workerHandle{}, fmt.Errorf("start modeld %s: %w", spec.Role, err)
	}
	done := make(chan struct{})
	var exitErr error
	go func() {
		exitErr = cmd.Wait()
		if parentPipe != nil {
			_ = parentPipe.Close()
		}
		close(done)
	}()
	var stopOnce sync.Once
	var processStopErr error
	stop := func() error {
		stopOnce.Do(func() {
			if parentPipe != nil {
				_ = parentPipe.Close()
			}
			select {
			case <-done:
				return
			default:
			}
			if err := cmd.Process.Signal(os.Interrupt); err == nil {
				select {
				case <-done:
					return
				case <-time.After(2 * time.Second):
				}
			}
			if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				// On Windows TerminateProcess can report access denied while an
				// exited process is still being reaped. Wait confirms termination.
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					processStopErr = err
				}
				return
			}
			<-done
		})
		return processStopErr
	}
	readyCtx, cancel := context.WithTimeout(ctx, ensureReadyWindow)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	probe := modeldprobe.New(root)
	for {
		var st modeldprobe.Status
		if m.ownerManaged {
			st, err = ownedWorkerStatus(readyCtx, readyFile, instance, spec.Backend)
			if err != nil {
				return workerHandle{}, errors.Join(err, stop())
			}
		} else {
			st = probe.Probe(readyCtx)
		}
		if st.State == modeldprobe.StateRunning {
			stopWorker := stop
			if m.ownerManaged {
				stopWorker = func() error {
					// The parent pipe is the child's hard lifetime fence. Give a
					// healthy, idle slot its ordinary unload before closing it.
					owner := modeldprobe.New(m.root).Detect()
					if owner.State == modeldprobe.StateRunning && owner.Instance == m.owner {
						unloadBeforeStop(st)
					}
					return stop()
				}
			}
			return workerHandle{status: st, done: done, stop: stopWorker, result: func() error { return exitErr }}, nil
		}
		select {
		case <-readyCtx.Done():
			_ = stop()
			return workerHandle{}, readyCtx.Err()
		case <-done:
			return workerHandle{}, fmt.Errorf("modeld %s exited before readiness: %v", spec.Role, exitErr)
		case <-ticker.C:
		}
	}
}

func unloadBeforeStop(status modeldprobe.Status) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	client, err := transportgrpc.DialLeader(status.Endpoint, status.Instance)
	if err != nil {
		return
	}
	defer client.Close()
	// Unload refuses a busy slot. Retiring a failed or busy child must still
	// complete even when it cannot produce a checkpoint.
	_ = client.UnloadModel(ctx, transport.UnloadModelRequest{})
}

func (m *Workers) ensureWorkerBinary(ctx context.Context, backend string) (string, error) {
	m.installMu.Lock()
	defer m.installMu.Unlock()
	if os.Getenv("CONTENOX_MODELD_BIN") != "" {
		binary := modeldprobe.New(m.root).Detect().Binary
		if binary == "" {
			return "", modeldprobe.ErrNotInstalled
		}
		if err := modeldinstall.ValidateBinary(ctx, binary, backend); err != nil {
			return "", fmt.Errorf("configured modeld is incompatible: %w", err)
		}
		return binary, nil
	}
	installed, err := modeldinstall.EnsureInstalled(ctx, backend, modeldinstall.Options{DataRoot: m.root})
	if err != nil {
		return "", fmt.Errorf("ensure compatible modeld: %w", err)
	}
	return installed.LauncherPath, nil
}

func workerServeArgs(spec WorkerSpec, root, modelsDir string) []string {
	args := []string{"serve", "--data-root", root, "--models-dir", modelsDir, "--backend", spec.Backend}
	for _, setting := range [][2]string{{"--mem-max", spec.Memory}, {"--mem-reserve", spec.Reserve}, {"--mem-cold", spec.ColdMemory}, {"--idle-ttl", spec.IdleTTL}, {"--mem-headroom", spec.Headroom}, {"--compute-reserve", spec.ComputeReserve}} {
		if setting[1] != "" {
			args = append(args, setting[0], setting[1])
		}
	}
	if spec.MinHotContext != "" && spec.MinHotContext != "auto" {
		args = append(args, "--min-hot-context", spec.MinHotContext)
	}
	return args
}

func workerEnvironment(environ []string, root string) []string {
	envOut := make([]string, 0, len(environ)+1)
	for _, env := range environ {
		if !strings.HasPrefix(env, "CONTENOX_DATA_ROOT=") {
			envOut = append(envOut, env)
		}
	}
	return append(envOut, "CONTENOX_DATA_ROOT="+root)
}
