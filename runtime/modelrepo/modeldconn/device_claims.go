package modeldconn

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/modeld/devicelease"
	"github.com/contenox/runtime/internal/modeld/owner"
	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/liblease"
)

type workerDevice struct {
	done    chan struct{}
	holder  *owner.Owner
	claim   transport.DeviceClaim
	workers map[*workerEntry]string
}

// ClaimDevices holds physical devices for resident children, including between client sessions.
// Worker records outlive an expired owner so a successor cannot overlap stale native allocations.
func (m *Workers) ClaimDevices(ctx context.Context, request transport.DeviceClaimRequest) (result transport.DeviceClaimResult, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	defer func() { err = errors.Join(err, m.releaseUnusedDevicesLocked()) }()
	result = transport.DeviceClaimResult{Claims: []transport.DeviceClaim{}}
	if !m.ownerManaged || m.closed || m.ctx.Err() != nil || request.OwnerInstance != m.owner {
		return result, transport.ErrStaleFence
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := checkDeviceClaim(ctx, transport.DeviceClaim{LeasePath: filepath.Join(m.root, "modeld.lease"), Instance: m.owner}); err != nil {
		return result, err
	}
	var entry *workerEntry
	for _, candidate := range m.entries {
		if candidate.handle.status.Instance == request.WorkerInstance && !candidate.draining && candidate.err == nil {
			entry = candidate
			break
		}
	}
	if entry == nil || request.WorkerInstance == "" {
		return result, transport.ErrStaleFence
	}
	select {
	case <-entry.handle.done:
		return result, transport.ErrStaleFence
	default:
	}
	raw, err := os.ReadFile(filepath.Join(entry.directory, "ready.json"))
	if err != nil {
		return result, err
	}
	var ready transport.WorkerReady
	if err := json.Unmarshal(raw, &ready); err != nil {
		return result, err
	}
	if ready.Instance != request.WorkerInstance || ready.PID <= 0 {
		return result, transport.ErrStaleFence
	}
	resources, ok := m.scheduler.(admission.ResourceService)
	if !ok {
		return result, errors.New("owner cannot reserve native resources; " + remedy.UpdateLocal)
	}
	identity, err := allocationIdentity(entry.spec, request.Config)
	if err != nil {
		return result, err
	}
	request.Resources.WorkerID = m.owner + "/" + request.WorkerInstance
	request.Resources.OwnerEpoch = request.WorkerInstance
	request.Resources.Identity = identity
	if err := resources.Reserve(ctx, request.Resources); err != nil {
		return result, err
	}
	ids := map[string]bool{}
	for _, device := range request.Devices {
		if device.ID == "" || device.Type == "" || device.Type == "cpu" {
			return result, errors.New("device claim requires resolved physical accelerator identities")
		}
		ids[device.ID] = true
	}
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	for _, id := range ordered {
		device, err := m.claimDeviceLocked(ctx, id, entry, ready)
		if err != nil {
			return result, err
		}
		result.Claims = append(result.Claims, device.claim)
	}
	return result, nil
}

func allocationIdentity(spec WorkerSpec, resolved transport.Config) (string, error) {
	resolved.AdmissionPermit = admission.Permit{}
	resolved.Execution = nil
	resolved.SessionNamespace = ""
	if resolved.Operation == "autocomplete" || resolved.Operation == "next-edit" {
		resolved.Operation = "chat"
	}
	identity := struct {
		Backend       string
		Digest        string
		Configuration string
		Config        transport.Config
	}{spec.Backend, spec.Digest, spec.ConfigurationDigest, resolved}
	raw, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}

func (m *Workers) claimDeviceLocked(ctx context.Context, id string, entry *workerEntry, ready transport.WorkerReady) (*workerDevice, error) {
	if m.devices == nil {
		m.devices = map[string]*workerDevice{}
	}
	device := m.devices[id]
	if device == nil {
		path, err := devicelease.Path(id)
		if err != nil {
			return nil, err
		}
		holder, err := owner.Join(context.Background(), owner.Config{LeasePath: path, TTL: 30 * time.Second, Meta: map[string]string{"data_root": m.root, "owner_epoch": m.owner, "device_id": id}})
		if err != nil {
			return nil, err
		}
		if !holder.IsOwner() {
			return nil, fmt.Errorf("%w: accelerator %s is held by another modeld owner; wait for its workers to stop, or "+remedy.ReplanPlacement, transport.ErrDeviceBusy, id)
		}
		if err := previousDeviceWorkers(ctx, path); err != nil {
			return nil, errors.Join(err, holder.Release())
		}
		device = &workerDevice{done: make(chan struct{}), holder: holder, claim: transport.DeviceClaim{DeviceID: id, LeasePath: path, Instance: holder.InstanceID()}, workers: map[*workerEntry]string{}}
		m.devices[id] = device
		go m.watchDevice(device)
	}
	if err := checkDeviceClaim(ctx, device.claim); err != nil {
		return nil, err
	}
	if _, exists := device.workers[entry]; !exists {
		dir := device.claim.LeasePath + ".workers"
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		path := filepath.Join(dir, fmt.Sprintf("%x.json", sha256.Sum256([]byte(ready.Instance))))
		if err := writeDeviceWorker(path, ready); err != nil {
			return nil, err
		}
		device.workers[entry] = path
	}
	if err := checkDeviceClaim(ctx, device.claim); err != nil {
		return nil, err
	}
	return device, nil
}

func previousDeviceWorkers(ctx context.Context, path string) error {
	paths, err := filepath.Glob(filepath.Join(path+".workers", "*.json"))
	if err != nil {
		return err
	}
	for _, recordPath := range paths {
		raw, err := os.ReadFile(recordPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		var worker transport.WorkerReady
		if err := json.Unmarshal(raw, &worker); err != nil {
			return err
		}
		alive, err := workerProcessAlive(ctx, worker)
		if err != nil {
			return err
		}
		if alive {
			return fmt.Errorf("%w: predecessor worker %s still holds accelerator allocations; wait for its termination before retrying; "+remedy.StopIdleWorker, transport.ErrDeviceBusy, worker.Instance)
		}
		if err := os.Remove(recordPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func writeDeviceWorker(path string, worker transport.WorkerReady) error {
	raw, err := json.Marshal(worker)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".worker-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(raw); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := file.Sync(); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func checkDeviceClaim(ctx context.Context, claim transport.DeviceClaim) error {
	record, err := liblease.InspectContext(ctx, claim.LeasePath)
	if err != nil {
		return err
	}
	if record.InstanceID != claim.Instance || !time.Now().Add(250*time.Millisecond).Before(record.ExpiresAt()) {
		return transport.ErrStaleFence
	}
	return nil
}

func (m *Workers) watchDevice(device *workerDevice) {
	select {
	case <-device.done:
		return
	case <-device.holder.Lost():
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for entry := range device.workers {
		entry.err = fmt.Errorf("device ownership lost: %w", device.holder.LostErr())
		entry.draining = true
		entry.cancel()
	}
}

func (m *Workers) releaseDevicesLocked(entry *workerEntry) error {
	var errs []error
	for _, device := range m.devices {
		path, held := device.workers[entry]
		if !held {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
			continue
		}
		delete(device.workers, entry)
	}
	return errors.Join(errors.Join(errs...), m.releaseUnusedDevicesLocked())
}

func (m *Workers) releaseUnusedDevicesLocked() error {
	var errs []error
	for id, device := range m.devices {
		if len(device.workers) == 0 {
			close(device.done)
			if err := device.holder.Release(); err != nil {
				errs = append(errs, err)
			}
			delete(m.devices, id)
		}
	}
	return errors.Join(errs...)
}
