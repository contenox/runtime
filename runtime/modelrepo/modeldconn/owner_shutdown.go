package modeldconn

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/modeldprobe"
)

// CloseIdle fences new acquisitions and stops children only if no request retains a worker.
func (m *Workers) CloseIdle(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	if !m.ownerManaged {
		m.mu.Unlock()
		return errors.New("idle owner shutdown requires an owner-managed worker pool")
	}
	entries := make([]*workerEntry, 0, len(m.entries)+len(m.retired))
	for _, entry := range m.entries {
		entries = append(entries, entry)
	}
	entries = append(entries, m.retired...)
	for _, entry := range entries {
		if entry.refs != 0 {
			m.mu.Unlock()
			return fmt.Errorf("%w: close active conversations and wait for native requests in every connected IDE before restarting this owner", transport.ErrModelBusy)
		}
	}
	m.closed = true
	m.cancel()
	m.mu.Unlock()
	m.wg.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	var failures []error
	for _, entry := range entries {
		failures = append(failures, entry.stopErr)
	}
	return errors.Join(failures...)
}

// RestartOwner restarts the local owner after its idle-only shutdown releases the lease.
func (m *Workers) RestartOwner(ctx context.Context) error {
	if !m.shared {
		return errors.New("owner restart requires a shared runtime client")
	}
	ctx = WithDataRoot(ctx, m.root)
	probe := modeldprobe.New(m.root)
	observed := probe.Detect()
	if observed.State == modeldprobe.StateRunning {
		client, err := m.sharedEndpoint(ctx)
		if err != nil {
			return fmt.Errorf("the existing owner cannot confirm idle shutdown; close its clients and update the running owner before retrying: %w", err)
		}
		if err := client.ShutdownOwner(ctx, transport.OwnerShutdownRequest{Fence: transport.Fence{OwnerInstanceID: client.InstanceID}}); err != nil {
			return err
		}
		CloseEndpoint(m.owner)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			current := probe.Detect()
			if current.State != modeldprobe.StateRunning || current.Instance != observed.Instance {
				break
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("waiting for the idle owner to release its lease: %w", ctx.Err())
			case <-ticker.C:
			}
		}
	}
	_, err := readyDaemonWithProbe(ctx, probe)
	return err
}
