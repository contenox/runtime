package modeldconn

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/internal/transport"
)

func (m *Workers) prepare(ctx context.Context, key string, entry *workerEntry, spec WorkerSpec) (*workerEntry, transport.NativeAllocation, error) {
	entry.prepareMu.Lock()
	defer entry.prepareMu.Unlock()
	if entry.resolved == nil {
		m.mu.Lock()
		status := entry.handle.status
		m.mu.Unlock()
		path, err := modelstore.Resolve(filepath.Join(m.root, "models"), spec.Model, spec.Backend, spec.Digest)
		if err != nil {
			return nil, transport.NativeAllocation{}, err
		}
		info, err := DescribeTarget(ctx, ModeldTarget{BackendID: m.owner + "/" + status.Instance, Endpoint: status.Endpoint, Instance: status.Instance}, ModelRef{Name: spec.Model, Type: spec.Backend, Digest: spec.Digest, Path: path}, spec.Config)
		if err != nil {
			return nil, transport.NativeAllocation{}, err
		}
		if info.Allocation == nil {
			return nil, transport.NativeAllocation{}, errors.New("worker cannot describe normalized resource requirements; " + remedy.UpdateLocal)
		}
		identity, err := allocationIdentity(spec, info.Allocation.Config)
		if err != nil {
			return nil, transport.NativeAllocation{}, err
		}
		info.Allocation.Resources.Identity = identity
		entry.resolved = info.Allocation
	}
	resolved := *entry.resolved
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.ctx.Err() != nil {
		return nil, resolved, errors.New("modeld worker manager closed during preparation")
	}
	if m.normalized == nil {
		m.normalized = map[string]*workerEntry{}
		m.aliases = map[string]*workerEntry{}
	}
	identity := resolved.Resources.Identity
	previous := m.normalized[identity]
	if previous != nil && previous != entry {
		select {
		case <-previous.stopped:
			previous = nil
		default:
		}
	}
	if previous != nil && previous != entry && !previous.draining && previous.err == nil {
		previous.refs++
		m.aliases[key] = previous
		if m.entries[key] == entry {
			delete(m.entries, key)
			m.retired = append(m.retired, entry)
		}
		entry.draining = true
		entry.cancel()
		return previous, *previous.resolved, nil
	}
	if entry.draining || entry.err != nil {
		return nil, resolved, fmt.Errorf("prepared worker is no longer available: %w", errors.Join(entry.err, transport.ErrStaleFence))
	}
	entry.spec.Config = resolved.Config
	m.normalized[identity] = entry
	return entry, resolved, nil
}
