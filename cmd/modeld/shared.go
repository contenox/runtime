package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/modeld/capacity"
	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/internal/modeld/owner"
	"github.com/contenox/runtime/internal/modeld/supervisor"
	"github.com/contenox/runtime/internal/transport/grpc"
	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/clikv"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

func runSharedOwner(ctx, ownerCtx context.Context, listener net.Listener, holder *owner.Owner, root, modelsDir string, tracker libtracker.ActivityTracker) (err error) {
	defer func() { err = errors.Join(err, holder.Release()) }()
	defer listener.Close()
	stateDir := filepath.Join(root, "owner")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(stateDir, "admission.db"), runtimetypes.SchemaSQLite)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	bus := libbus.NewSQLite(db.WithoutTransaction())
	defer func() { err = errors.Join(err, bus.Close()) }()
	report := func(err error) {
		reportErr, _, end := tracker.Start(context.Background(), "schedule", "modeld_owner")
		reportErr(err)
		end()
	}
	policy := admission.DefaultConfig()
	var configured string
	readErr := runtimetypes.New(db.WithoutTransaction()).GetKV(ctx, clikv.Prefix+"inference.modeld.admission", &configured)
	if readErr != nil && !errors.Is(readErr, libdbexec.ErrNotFound) {
		return readErr
	}
	if configured != "" {
		if err := json.Unmarshal([]byte(configured), &policy); err != nil {
			return err
		}
	}
	scheduler, err := admission.NewEventScheduler(ctx, db, bus, policy, report)
	if err != nil {
		return err
	}
	reapCtx, stopReap := context.WithTimeout(ctx, 5*time.Second)
	err = modeldconn.WaitForPreviousWorkers(reapCtx, root, holder.InstanceID())
	stopReap()
	if err != nil {
		return err
	}
	previous, err := scheduler.Snapshot(ctx)
	if err != nil {
		return err
	}
	for _, worker := range previous.Workers {
		if err := scheduler.Update(ctx, admission.Worker{ID: worker.ID, OwnerEpoch: worker.OwnerEpoch, Draining: true, Stopped: true}); err != nil {
			return err
		}
	}
	serveCtx, cancel := context.WithCancel(ownerCtx)
	defer cancel()
	workers, err := modeldconn.NewOwnerWorkers(serveCtx, root, holder.InstanceID(), scheduler)
	if err != nil {
		return err
	}
	// Keep lease and admission probes available while idle children save
	// their last checkpoint. Loss of the lease still cancels serveCtx directly.
	stopSignal := context.AfterFunc(ctx, func() {
		_ = workers.Close()
		cancel()
	})
	defer stopSignal()
	service, err := supervisor.New(workers, modelstore.Dir(root, modelsDir), holder.InstanceID(), nativeInventory)
	if err != nil {
		return errors.Join(err, workers.Close())
	}
	schedulerDone := make(chan error, 1)
	go func() { schedulerDone <- scheduler.Run(serveCtx) }()
	err = grpc.Serve(serveCtx, listener, service, holder.InstanceID(), "shared", grpc.WithTracker(tracker), grpc.WithLocalSnapshots(root))
	cancel()
	err = errors.Join(err, service.Close(), <-schedulerDone)
	return err
}

func serveChild(root, modelsDir, backend, instance, readyFile, leasePath string, policy capacity.Policy, idle time.Duration, tracker libtracker.ActivityTracker) error {
	service, actual, err := selectWorkerBackend(backend, policy, tracker)
	if err != nil {
		return err
	}
	if actual != backend {
		return fmt.Errorf("worker backend %q differs from requested %q", actual, backend)
	}
	return owner.ServeChild(context.Background(), owner.ChildConfig{
		DataRoot: root, ModelsDir: modelsDir, Backend: backend, Instance: instance,
		ReadyFile: readyFile, OwnerLease: leasePath, IdleTTL: idle, Tracker: tracker, Parent: os.Stdin,
	}, service)
}
