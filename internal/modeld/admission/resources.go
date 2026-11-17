package admission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"sort"
	"strconv"

	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/internal/settings"
	"github.com/contenox/runtime/runtime/clikv"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

// ErrCapacity means the reviewed allocation exceeds an owner resource pool.
var ErrCapacity = errors.New("admission resources unavailable")

// PoolAllocation identifies one physical memory pool and its proposed reservation.
// AvailableBytes is the observed free memory before this allocation; LimitBytes
// bounds the sum of the owner's resident allocations independently of that observation.
// TotalBytes is the pool's physical size, which a device ceiling is a share of.
type PoolAllocation struct {
	PoolID         string `json:"poolId"`
	Bytes          int64  `json:"bytes"`
	LimitBytes     int64  `json:"limitBytes"`
	AvailableBytes int64  `json:"availableBytes"`
	TotalBytes     int64  `json:"totalBytes,omitempty"`
}

// Allocation retains resident resources until its fenced worker has stopped.
// Identity matches a reviewed completion protection; it never implies shared weights.
type Allocation struct {
	CanYield    bool             `json:"canYield"`
	WorkerID    string           `json:"workerId"`
	OwnerEpoch  string           `json:"ownerEpoch"`
	Identity    string           `json:"identity"`
	Pools       []PoolAllocation `json:"pools"`
	Threads     int              `json:"threads"`
	ThreadLimit int              `json:"threadLimit"`
}

// Protection reserves a reviewed completion route before its worker is resident.
type Protection struct {
	ClientID string           `json:"clientId,omitempty"`
	Identity string           `json:"identity"`
	Pools    []PoolAllocation `json:"pools"`
}

// Resources reports resident allocations, configured completion reservations and
// the device ceilings bounding each pool, as pool id to a share of its total bytes.
type Resources struct {
	Allocations []Allocation       `json:"allocations"`
	Protections []Protection       `json:"protections"`
	Ceilings    map[string]float64 `json:"ceilings,omitempty"`
}

// ResourceService accounts for resident resources through the existing admission projection.
type ResourceService interface {
	Prepare(context.Context, Allocation) error
	Reserve(context.Context, Allocation) error
	Protect(context.Context, []Protection) error
	ProtectClient(context.Context, string, []Protection) error
}

// ProtectClient replaces only one connected client's reservations; resident allocations remain owned by their workers.
func (s *EventScheduler) ProtectClient(ctx context.Context, clientID string, protections []Protection) error {
	if clientID == "" {
		return errors.New("admission: completion protection requires a client identity")
	}
	seen := map[string]bool{}
	for _, protection := range protections {
		if protection.Identity == "" || seen[protection.Identity] {
			return errors.New("admission: unique completion identities required")
		}
		seen[protection.Identity] = true
		if err := validatePools(protection.Pools); err != nil {
			return err
		}
	}
	return s.transact(ctx, func(t *eventTransaction) error {
		next := make([]Protection, 0, len(t.state.Protections)+len(protections))
		for _, current := range t.state.Protections {
			if current.ClientID != clientID {
				next = append(next, current)
			}
		}
		for _, protection := range protections {
			protection.ClientID = clientID
			next = append(next, protection)
		}
		sort.Slice(next, func(i, j int) bool {
			if next[i].ClientID != next[j].ClientID {
				return next[i].ClientID < next[j].ClientID
			}
			return next[i].Identity < next[j].Identity
		})
		if reflect.DeepEqual(next, t.state.Protections) {
			return nil
		}
		for _, protection := range protections {
			if err := validateCeilings(protection.Pools, t.state.Ceilings); err != nil {
				return err
			}
		}
		if err := checkResources(t.state.Allocations, next, t.state.Ceilings); err != nil {
			return err
		}
		return t.append("client_completion_protection", admissionChange{Protections: &next})
	})
}

// Prepare registers backend-resolved compute requirements without claiming resident memory.
func (s *EventScheduler) Prepare(ctx context.Context, allocation Allocation) error {
	if allocation.WorkerID == "" || allocation.OwnerEpoch == "" || allocation.Identity == "" || allocation.Threads <= 0 || allocation.ThreadLimit <= 0 || allocation.Threads > allocation.ThreadLimit {
		return errors.New("admission: complete resolved allocation required")
	}
	if err := validatePools(allocation.Pools); err != nil {
		return err
	}
	return s.transact(ctx, func(t *eventTransaction) error {
		worker, exists := t.state.Workers[allocation.WorkerID]
		if !exists {
			return ErrWorkerNotFound
		}
		if err := requestFailure(worker, Request{OwnerEpoch: allocation.OwnerEpoch}); err != nil {
			return err
		}
		if resident, exists := t.state.Allocations[allocation.WorkerID]; exists && resident.Identity != allocation.Identity {
			return ErrWorkerBusy
		}
		if err := validateCeilings(allocation.Pools, t.state.Ceilings); err != nil {
			return err
		}
		return t.append("resources_prepared", admissionChange{Plan: &allocation})
	})
}

func (t *eventTransaction) computeAllocation(workerID string) (Allocation, bool) {
	if resident, exists := t.state.Allocations[workerID]; exists {
		return resident, true
	}
	planned, exists := t.state.Plans[workerID]
	return planned, exists
}

func validatePools(pools []PoolAllocation) error {
	if len(pools) == 0 {
		return errors.New("admission: allocation requires physical memory pools")
	}
	seen := map[string]bool{}
	for _, pool := range pools {
		if pool.PoolID == "" || seen[pool.PoolID] || pool.Bytes < 0 || pool.LimitBytes <= 0 || pool.AvailableBytes < 0 {
			return errors.New("admission: invalid or repeated memory pool")
		}
		if pool.Bytes > pool.LimitBytes {
			return fmt.Errorf("%w: pool %s needs %d bytes, limit %d; "+remedy.FitRole, ErrCapacity, pool.PoolID, pool.Bytes, pool.LimitBytes)
		}
		seen[pool.PoolID] = true
	}
	return nil
}

func validateCeilings(pools []PoolAllocation, ceilings map[string]float64) error {
	for _, pool := range pools {
		share, bounded := ceilings[pool.PoolID]
		if !bounded {
			continue
		}
		limit, err := ceilingLimit(ceilings, pool.PoolID, pool.LimitBytes, pool.TotalBytes)
		if err != nil {
			return err
		}
		if pool.Bytes > limit {
			return fmt.Errorf("%w: pool %s needs %d bytes, its device ceiling of %s of %d bytes allows %d; %s, or "+remedy.FitRole, ErrCapacity, pool.PoolID, pool.Bytes, ceilingPercent(share), pool.TotalBytes, limit, remedy.RaiseCeiling(pool.PoolID))
		}
	}
	return nil
}

// ceilingLimit bounds a pool's limit by its device ceiling; a ceiling on a pool
// whose total is unknown is refused rather than ignored.
func ceilingLimit(ceilings map[string]float64, poolID string, limit, total int64) (int64, error) {
	share, bounded := ceilings[poolID]
	if !bounded {
		return limit, nil
	}
	if total <= 0 {
		return 0, fmt.Errorf("%w: pool %s has a device ceiling of %s but its allocation reports no total memory; update local inference", ErrCapacity, poolID, ceilingPercent(share))
	}
	return min(limit, int64(share*float64(total))), nil
}

func ceilingPercent(share float64) string {
	return strconv.FormatFloat(share*100, 'f', -1, 64) + "%"
}

func allocationBytes(allocation Allocation, poolID string) int64 {
	for _, pool := range allocation.Pools {
		if pool.PoolID == poolID {
			return pool.Bytes
		}
	}
	return 0
}

func resourceUsage(allocations map[string]Allocation, protections []Protection) (map[string]int64, map[string]int64, map[string]int64) {
	used, limits, totals := map[string]int64{}, map[string]int64{}, map[string]int64{}
	add := func(pool PoolAllocation, bytes int64) {
		used[pool.PoolID] += bytes
		if previous, ok := limits[pool.PoolID]; !ok || pool.LimitBytes < previous {
			limits[pool.PoolID] = pool.LimitBytes
		}
		totals[pool.PoolID] = max(totals[pool.PoolID], pool.TotalBytes)
	}
	for _, allocation := range allocations {
		for _, pool := range allocation.Pools {
			add(pool, pool.Bytes)
		}
	}
	for _, protection := range protections {
		for _, pool := range protection.Pools {
			resident := int64(0)
			for _, allocation := range allocations {
				if allocation.Identity == protection.Identity {
					resident = max(resident, allocationBytes(allocation, pool.PoolID))
				}
			}
			add(pool, max(pool.Bytes-resident, 0))
		}
	}
	return used, limits, totals
}

func checkResources(allocations map[string]Allocation, protections []Protection, ceilings map[string]float64) error {
	used, limits, totals := resourceUsage(allocations, protections)
	for pool, bytes := range used {
		limit, err := ceilingLimit(ceilings, pool, limits[pool], totals[pool])
		if err != nil {
			return err
		}
		if share, bounded := ceilings[pool]; bounded && limit < limits[pool] && bytes > limit {
			return fmt.Errorf("%w: pool %s needs %d bytes including resident workers and completion protection, its device ceiling of %s of %d bytes allows %d; %s, "+remedy.StopIdleWorker+", or "+remedy.FitRole, ErrCapacity, pool, bytes, ceilingPercent(share), totals[pool], limit, remedy.RaiseCeiling(pool))
		}
		if bytes > limits[pool] {
			return fmt.Errorf("%w: pool %s needs %d bytes including resident workers and completion protection, limit %d; "+remedy.StopIdleWorker+", or "+remedy.FitRole, ErrCapacity, pool, bytes, limits[pool])
		}
	}
	return nil
}

// Reserve atomically claims resolved memory before native allocation. Releasing a
// request permit does not release this reservation; only confirmed worker stop does.
func (s *EventScheduler) Reserve(ctx context.Context, allocation Allocation) error {
	if allocation.WorkerID == "" || allocation.OwnerEpoch == "" || allocation.Identity == "" || allocation.Threads <= 0 || allocation.ThreadLimit <= 0 || allocation.Threads > allocation.ThreadLimit {
		return errors.New("admission: complete worker identity and valid thread bounds required")
	}
	if err := validatePools(allocation.Pools); err != nil {
		return err
	}
	return s.transact(ctx, func(t *eventTransaction) error {
		worker, exists := t.state.Workers[allocation.WorkerID]
		if !exists {
			return ErrWorkerNotFound
		}
		if worker.OwnerEpoch != allocation.OwnerEpoch || worker.Stopped {
			return ErrOwnerEpoch
		}
		if worker.Draining {
			return ErrDraining
		}
		if planned, exists := t.state.Plans[allocation.WorkerID]; exists && planned.Identity != allocation.Identity {
			return errors.New("resolved native settings differ from admission preparation; " + remedy.ReplanRole)
		}
		previous, resident := t.state.Allocations[allocation.WorkerID]
		if resident && (previous.Identity != allocation.Identity || previous.OwnerEpoch != allocation.OwnerEpoch) {
			return ErrWorkerBusy
		}
		for _, pool := range allocation.Pools {
			if pool.Bytes-allocationBytes(previous, pool.PoolID) > pool.AvailableBytes {
				return fmt.Errorf("%w: pool %s has %d bytes free; "+remedy.StopIdleWorker+", or "+remedy.FitRole, ErrCapacity, pool.PoolID, pool.AvailableBytes)
			}
		}
		if resident {
			for _, pool := range previous.Pools {
				if allocationBytes(allocation, pool.PoolID) < pool.Bytes {
					return errors.New("admission: a resident reservation cannot shrink before confirmed termination")
				}
			}
		}
		allocations := make(map[string]Allocation, len(t.state.Allocations)+1)
		for id, current := range t.state.Allocations {
			allocations[id] = current
		}
		allocations[allocation.WorkerID] = allocation
		if err := validateCeilings(allocation.Pools, t.state.Ceilings); err != nil {
			return err
		}
		if err := checkResources(allocations, t.state.Protections, t.state.Ceilings); err != nil {
			return err
		}
		used, _, _ := resourceUsage(allocations, t.state.Protections)
		residentBytes, _, _ := resourceUsage(t.state.Allocations, []Protection{})
		for _, pool := range allocation.Pools {
			if required := used[pool.PoolID] - residentBytes[pool.PoolID]; required > pool.AvailableBytes {
				return fmt.Errorf("%w: pool %s needs %d free bytes including completion protection, has %d; "+remedy.StopIdleWorker+", or "+remedy.FitRole, ErrCapacity, pool.PoolID, required, pool.AvailableBytes)
			}
		}
		if resident && reflect.DeepEqual(previous, allocation) {
			return nil
		}
		if err := t.append("resources_reserved", admissionChange{Allocation: &allocation}); err != nil {
			return err
		}
		active := map[string]bool{}
		for _, request := range t.state.Requests {
			if request.Permit != nil && !request.Suspended && request.Request.WorkerID != allocation.WorkerID {
				active[request.Request.WorkerID] = true
			}
		}
		if !t.computeFits(allocation.WorkerID, active) {
			for id, request := range t.state.Requests {
				if request.Permit != nil && request.Request.WorkerID == allocation.WorkerID {
					request.Suspended = true
					if err := t.append("allocation_waiting", admissionChange{ID: id, Request: &request}); err != nil {
						return err
					}
				}
			}
		}
		return s.dispatch(t)
	})
}

// ConfigureCeilings replaces the device ceilings, as pool id to a share of the
// pool's total bytes. A ceiling that resident workers and completion protection
// already exceed is refused naming the pool; the empty map removes every ceiling.
func (s *EventScheduler) ConfigureCeilings(ctx context.Context, ceilings map[string]float64) error {
	if ceilings == nil {
		return errors.New("admission: device ceilings require a map; an empty map removes every ceiling")
	}
	for poolID, share := range ceilings {
		if err := settings.ValidateDeviceCeiling(poolID, share); err != nil {
			return err
		}
	}
	next := maps.Clone(ceilings)
	return s.transact(ctx, func(t *eventTransaction) error {
		if maps.Equal(next, t.state.Ceilings) {
			return nil
		}
		if err := checkResources(t.state.Allocations, t.state.Protections, next); err != nil {
			return err
		}
		raw, err := json.Marshal(next)
		if err != nil {
			return err
		}
		if err := clikv.SetString(ctx, runtimetypes.New(t.exec), settings.DeviceCeilings, string(raw)); err != nil {
			return err
		}
		if err := t.append("device_ceilings_changed", admissionChange{Ceilings: &next}); err != nil {
			return err
		}
		return s.dispatch(t)
	})
}

// Protect replaces reviewed completion reservations without evicting resident workers.
func (s *EventScheduler) Protect(ctx context.Context, protections []Protection) error {
	return s.ProtectClient(ctx, "owner-policy", protections)
}

func (t *eventTransaction) resourceSnapshot() Resources {
	result := Resources{Allocations: []Allocation{}, Protections: append([]Protection{}, t.state.Protections...)}
	if len(t.state.Ceilings) > 0 {
		result.Ceilings = maps.Clone(t.state.Ceilings)
	}
	for _, allocation := range t.state.Allocations {
		result.Allocations = append(result.Allocations, allocation)
	}
	sort.Slice(result.Allocations, func(i, j int) bool { return result.Allocations[i].WorkerID < result.Allocations[j].WorkerID })
	return result
}

func (t *eventTransaction) threadsFit(workerID string, active map[string]bool) bool {
	allocation, resolved := t.computeAllocation(workerID)
	if !resolved {
		return true
	}
	threads, limit := allocation.Threads, allocation.ThreadLimit
	for id := range active {
		if current, ok := t.computeAllocation(id); ok {
			threads += current.Threads
			limit = min(limit, current.ThreadLimit)
		}
	}
	return threads <= limit
}
