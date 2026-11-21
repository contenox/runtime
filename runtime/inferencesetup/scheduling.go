package inferencesetup

import (
	"context"
	"errors"
	"fmt"

	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/modelworkers"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

type schedulingRole struct {
	assignment  Assignment
	options     modeldconn.WorkerOptions
	endpoints   map[string]bool
	connections int
	changed     bool
}

func (s *Service) validateScheduling(ctx context.Context, store runtimetypes.Store, assignments []Assignment, verifications []Verification) error {
	connections, err := store.ListAllBackends(ctx)
	if err != nil {
		return err
	}
	changedNative := false
	for _, assignment := range assignments {
		for _, connection := range connections {
			if runtimestate.MatchesBackend(assignment.Provider, *connection) && (connection.Type == "modeld" || connection.Type == "local") {
				changedNative = true
			}
		}
	}
	if !changedNative {
		return nil
	}
	roles := map[string]schedulingRole{}
	for _, name := range RoleIDs() {
		enabled, err := readRoleEnabled(ctx, store, name)
		if err != nil {
			return err
		}
		if !enabled {
			continue
		}
		assignment, err := s.readAssignment(ctx, store, name)
		if err != nil {
			return err
		}
		role := schedulingRole{assignment: assignment, endpoints: map[string]bool{}}
		for _, proposed := range assignments {
			if proposed.Role == name {
				role.assignment, role.changed = proposed, true
			}
		}
		if role.assignment.Provider == "" || role.assignment.Model == "" {
			continue
		}
		for _, connection := range connections {
			if runtimestate.MatchesBackend(role.assignment.Provider, *connection) && (connection.Type == "modeld" || connection.Type == "local") {
				role.endpoints[connection.BaseURL] = true
				role.connections++
			}
		}
		if len(role.endpoints) == 0 {
			continue
		}
		var proposed bool
		role.options, proposed = modeldconn.ProposedWorkerOptions(ctx, name)
		if !proposed {
			role.options, err = modelworkers.ReadAssignedOptions(ctx, store, name)
			if err != nil {
				return err
			}
		}
		roles[name] = role
	}
	completion, configured := roles[RoleAutocomplete]
	if !configured {
		return nil
	}
	for _, name := range []string{RoleNextEdit, RoleChat, RoleRecovery, RoleEmbedding, RoleImage, RoleVideo} {
		role, configured := roles[name]
		sharedHost := false
		for endpoint := range role.endpoints {
			sharedHost = sharedHost || completion.endpoints[endpoint]
		}
		if !configured || !sharedHost || !role.changed && !completion.changed {
			continue
		}
		if role.options.SchedulingMode == "serialized" || completion.options.SchedulingMode == "serialized" {
			continue
		}
		if role.options.SchedulingMode != "protected" && completion.options.SchedulingMode != "protected" {
			continue
		}
		if role.connections != 1 || completion.connections != 1 {
			return errors.New("select an unambiguous native connection before reviewing completion protection")
		}
		completionEvidence, err := s.schedulingEvidence(ctx, store, completion, verifications)
		if err != nil {
			return err
		}
		roleEvidence, err := s.schedulingEvidence(ctx, store, role, verifications)
		if err != nil {
			return err
		}
		for _, completed := range completionEvidence {
			for _, other := range roleEvidence {
				if !protectedScheduling(completed, other, name) {
					return fmt.Errorf("%s and Autocomplete cannot provide the reviewed completion protection; assign a smaller model to one of them or free memory on the device", name)
				}
			}
		}
	}
	return nil
}

func (s *Service) schedulingEvidence(ctx context.Context, store runtimetypes.Store, role schedulingRole, proposed []Verification) ([]transport.SessionExecution, error) {
	var evidence Verification
	if role.changed {
		for _, verification := range proposed {
			if verification.Role == role.assignment.Role {
				evidence = verification
			}
		}
	} else {
		if err := store.GetKV(ctx, verificationKey(role.assignment.Role), &evidence); err != nil && !errors.Is(err, libdbexec.ErrNotFound) {
			return nil, err
		}
		fingerprint, err := s.fingerprint(ctx, store, role.assignment)
		if err != nil {
			return nil, err
		}
		if evidence.Fingerprint != fingerprint {
			return nil, fmt.Errorf("verify %s before reviewing completion protection", role.assignment.Role)
		}
	}
	if len(evidence.NativeExecutions) == 0 {
		return nil, fmt.Errorf("verify %s with native allocation evidence before reviewing completion protection", role.assignment.Role)
	}
	return evidence.NativeExecutions, nil
}

func protectedScheduling(completion, other transport.SessionExecution, role string) bool {
	left, right := completion.Capacity.Allocation, other.Capacity.Allocation
	if left == nil || right == nil || left.Resources.Identity == "" || right.Resources.Identity == "" || len(left.Resources.Pools) == 0 || len(right.Resources.Pools) == 0 {
		return false
	}
	if left.Resources.Identity == right.Resources.Identity {
		return false
	}
	if right.Resources.CanYield || role == RoleEmbedding {
		return true
	}
	for _, pool := range left.Resources.Pools {
		for _, reserved := range right.Resources.Pools {
			if pool.PoolID == reserved.PoolID && pool.PoolID != "host" {
				return false
			}
		}
	}
	return left.Resources.Threads > 0 && right.Resources.Threads > 0 && left.Resources.Threads+right.Resources.Threads <= min(left.Resources.ThreadLimit, right.Resources.ThreadLimit)
}
