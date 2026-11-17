package owner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/contenox/runtime/internal/modeld/slot"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/internal/transport/grpc"
	"github.com/contenox/runtime/liblease"
	"github.com/contenox/runtime/libtracker"
)

// ChildConfig identifies a private worker and its owning process and lease.
type ChildConfig struct {
	DataRoot   string
	ModelsDir  string
	Backend    string
	Instance   string
	ReadyFile  string
	OwnerLease string
	IdleTTL    time.Duration
	Tracker    libtracker.ActivityTracker
	Parent     io.Reader
}

// ServeChild hosts a slot until its parent exits or loses its lease.
// The worker process must exit when this returns; native calls may still be unwinding.
func ServeChild(ctx context.Context, cfg ChildConfig, backend transport.Service) error {
	epoch, _, valid := strings.Cut(cfg.Instance, "/")
	if ctx == nil || backend == nil || cfg.Parent == nil || cfg.Tracker == nil || !valid || epoch == "" || cfg.OwnerLease == "" || cfg.ReadyFile == "" || cfg.DataRoot == "" || cfg.ModelsDir == "" || cfg.Backend == "" {
		return errors.New("worker requires complete owner, backend, model store and parent-lifetime settings")
	}
	if err := childOwnerAlive(cfg.OwnerLease, epoch); err != nil {
		return err
	}
	record, err := liblease.Inspect(cfg.OwnerLease)
	if err != nil {
		return err
	}
	if record.Meta[EndpointMetaKey] == "" {
		return errors.New("worker owner has no device-claim endpoint")
	}
	client, err := grpc.DialLeader(record.Meta[EndpointMetaKey], epoch)
	if err != nil {
		return err
	}
	defer client.Close()
	claims := &workerClaims{client: client, epoch: epoch, instance: cfg.Instance, claims: map[string]transport.DeviceClaim{}}
	managed, ok := backend.(transport.ManagedBackend)
	if !ok {
		return errors.New("native backend does not support owner device claims; update modeld")
	}
	backend, err = managed.WithDeviceClaims(claims)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(context.Canceled)
	go func() {
		_, err := io.Copy(io.Discard, cfg.Parent)
		cancel(errors.Join(errors.New("worker parent connection closed"), err))
	}()
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := childOwnerAlive(cfg.OwnerLease, epoch); err != nil {
					cancel(err)
					return
				}
				if err := claims.check(ctx); err != nil {
					cancel(err)
					return
				}
			}
		}
	}()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	ownerRoot := filepath.Dir(cfg.OwnerLease)
	worker := slot.New(backend, slot.WithOwner(cfg.Instance), slot.WithBackend(cfg.Backend), slot.WithIdleTTL(cfg.IdleTTL), slot.WithDataRoot(cfg.DataRoot), slot.WithSnapshotRoot(ownerRoot), slot.WithModelsDir(cfg.ModelsDir), slot.WithTracker(cfg.Tracker), slot.WithSnapshotContext(ctx))
	worker.StartReaper(ctx)
	raw, err := json.Marshal(transport.WorkerReady{PID: os.Getpid(), Endpoint: listener.Addr().String(), Instance: cfg.Instance, Backend: cfg.Backend})
	if err != nil {
		return err
	}
	if err := os.WriteFile(cfg.ReadyFile+".tmp", raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(cfg.ReadyFile+".tmp", cfg.ReadyFile); err != nil {
		return err
	}
	err = grpc.Serve(ctx, listener, &managedSlot{Service: worker, lifetime: ctx, client: client}, cfg.Instance, cfg.Backend, grpc.WithTracker(cfg.Tracker), grpc.WithLocalSnapshots(ownerRoot))
	return errors.Join(err, context.Cause(ctx))
}

func childOwnerAlive(path, epoch string) error {
	record, err := liblease.Inspect(path)
	if err != nil {
		return err
	}
	if record.InstanceID != epoch || !time.Now().Add(250*time.Millisecond).Before(record.ExpiresAt()) {
		return transport.ErrStaleFence
	}
	return nil
}
