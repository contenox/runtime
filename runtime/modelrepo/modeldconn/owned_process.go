package modeldconn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/contenox/runtime/internal/transport"
	transportgrpc "github.com/contenox/runtime/internal/transport/grpc"
	"github.com/contenox/runtime/runtime/modeldprobe"
	"github.com/shirou/gopsutil/v4/process"
)

func ownedWorkerStatus(ctx context.Context, path, instance, backend string) (modeldprobe.Status, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return modeldprobe.Status{}, nil
	}
	if err != nil {
		return modeldprobe.Status{}, err
	}
	var ready transport.WorkerReady
	if err := json.Unmarshal(raw, &ready); err != nil {
		return modeldprobe.Status{}, err
	}
	if ready.Instance != instance || ready.Backend != backend {
		return modeldprobe.Status{}, errors.New("worker startup identity mismatch")
	}
	client, err := transportgrpc.DialLeader(ready.Endpoint, instance)
	if err != nil {
		return modeldprobe.Status{}, err
	}
	health, err := client.Health(ctx)
	err = errors.Join(err, client.Close())
	if err != nil {
		return modeldprobe.Status{}, err
	}
	if !health.Ready || health.InstanceID != instance {
		return modeldprobe.Status{}, errors.New("worker startup health failed")
	}
	return modeldprobe.Status{State: modeldprobe.StateRunning, Endpoint: ready.Endpoint, Instance: instance, Backend: backend}, nil
}

// WaitForPreviousWorkers confirms earlier owner children have exited before retiring their admission grants.
// A reused PID is distinguished by the worker's unique epoch argument.
func WaitForPreviousWorkers(ctx context.Context, root, epoch string) error {
	paths, err := filepath.Glob(filepath.Join(root, "workers", "*", "*", "ready.json"))
	if err != nil {
		return err
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var ready transport.WorkerReady
		if err := json.Unmarshal(raw, &ready); err != nil {
			return err
		}
		if strings.HasPrefix(ready.Instance, epoch+"/") {
			continue
		}
		if ready.PID <= 0 || ready.Instance == "" {
			return errors.New("previous worker has no process identity; repair local inference before takeover")
		}
		for {
			alive, err := workerProcessAlive(ctx, ready)
			if err == nil && !alive {
				break
			}
			// A process exiting between enumeration and reading its command
			// line can temporarily lose its PEB on Windows. Only a successful
			// identity check can confirm that its allocation is reclaimable.
			select {
			case <-ctx.Done():
				return fmt.Errorf("cannot confirm previous worker %s exited; allocations cannot be reclaimed: %w", ready.Instance, errors.Join(ctx.Err(), err))
			case <-time.After(25 * time.Millisecond):
			}
		}
	}
	return nil
}

func workerProcessAlive(ctx context.Context, ready transport.WorkerReady) (bool, error) {
	if ready.PID <= 0 || ready.Instance == "" {
		return false, errors.New("worker has no process identity; repair local inference before takeover")
	}
	child, err := process.NewProcessWithContext(ctx, int32(ready.PID))
	if errors.Is(err, process.ErrorProcessNotRunning) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	args, err := child.CmdlineSliceWithContext(ctx)
	if errors.Is(err, process.ErrorProcessNotRunning) {
		return false, nil
	}
	if err != nil {
		alive, probeErr := process.PidExistsWithContext(ctx, int32(ready.PID))
		if probeErr == nil && !alive {
			return false, nil
		}
		return false, errors.Join(err, probeErr)
	}
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--worker-instance" && args[i+1] == ready.Instance {
			return true, nil
		}
	}
	return false, nil
}
