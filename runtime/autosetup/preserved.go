package autosetup

import (
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/runtimestate"
)

func preservedCandidate(input PlanningInput, role inferencesetup.RoleStatus) *RoleCandidate {
	result := &RoleCandidate{Route: inferencesetup.RouteProposal{Assignment: role.Assignment, RoleRevision: role.Revision, SettingsChange: inferencesetup.PreserveSettings}, Installed: true, Verified: role.State == "available", Reason: "Keep the current assignment and settings", Pools: []PoolEstimate{}, Scheduling: "external"}
	for _, connection := range input.Configuration.Providers {
		if connection.ID != role.ConnectionID && connection.Selector != role.Provider {
			continue
		}
		result.ConnectionID, result.Endpoint, result.Native = connection.ID, connection.URL, nativeConnection(connection)
		if !result.Native {
			return result
		}
		result.Scheduling = "serialized"
		for _, host := range input.Hosts {
			if host.ConnectionID != connection.ID {
				continue
			}
			result.ThreadLimit = host.Inventory.HostThreads
			if host.Error != "" {
				result.ReviewError = host.Error
				continue
			}
			if role.Verification == nil {
				continue
			}
			for _, execution := range role.Verification.NativeExecutions {
				allocation := execution.Capacity.Allocation
				if allocation == nil {
					continue
				}
				result.ContextTokens = execution.Resolved.NumCtx
				result.ExecutionSettings = modeldconn.WorkerOptions{Backend: execution.Backend, Target: execution.Resolved.Target, Context: execution.Resolved.NumCtx, Threads: execution.Resolved.NumThreads, Batch: execution.Resolved.NumBatch, KVCacheType: execution.Resolved.KVCacheType}
				if allocation.Resources.CanYield {
					result.Scheduling = "cooperative"
				}
				for _, pool := range allocation.Resources.Pools {
					for _, observed := range host.Inventory.Pools {
						if observed.ID == pool.PoolID {
							result.Pools = append(result.Pools, PoolEstimate{Host: connection.URL, PoolID: pool.PoolID, Bytes: pool.Bytes, AvailableBytes: observed.AvailableBytes})
						}
					}
				}
				break
			}
		}
		if len(result.Pools) == 0 && result.ReviewError == "" {
			result.ReviewError = "Verify the preserved native role to establish its placement and resource reservation before assigning another local role."
		}
		return result
	}
	for _, connection := range input.Configuration.Providers {
		if nativeConnection(connection) && (role.Provider == connection.Type || role.Provider == "modeld" || role.Provider == "local" || role.Provider == runtimestate.ConnectionSelector(connection.ID)) {
			result.Native, result.Scheduling = true, "unresolved"
			result.ReviewError = "Select an unambiguous connection for the preserved native role."
			break
		}
	}
	return result
}

func preservedReservations(input PlanningInput, current map[string]inferencesetup.RoleStatus, constraints map[string]RoleConstraint) []PoolEstimate {
	reservations := []PoolEstimate{}
	protected := map[string]int64{}
	hosts := map[string]bool{}
	for _, host := range input.Hosts {
		endpoint := ""
		for _, connection := range input.Configuration.Providers {
			if connection.ID == host.ConnectionID {
				endpoint = connection.URL
				break
			}
		}
		if endpoint == "" || hosts[endpoint] {
			continue
		}
		hosts[endpoint] = true
		for _, protection := range host.Resources.Protections {
			for _, pool := range protection.Pools {
				resident := int64(0)
				for _, allocation := range host.Resources.Allocations {
					if allocation.Identity != protection.Identity {
						continue
					}
					for _, actual := range allocation.Pools {
						if actual.PoolID == pool.PoolID {
							resident = max(resident, actual.Bytes)
						}
					}
				}
				remaining := max(0, pool.Bytes-resident)
				if remaining == 0 {
					continue
				}
				for _, observed := range host.Inventory.Pools {
					if observed.ID == pool.PoolID {
						reservations = append(reservations, PoolEstimate{Host: endpoint, PoolID: pool.PoolID, Bytes: remaining, AvailableBytes: observed.AvailableBytes})
						protected[endpoint+"\n"+protection.Identity+"\n"+pool.PoolID] += remaining
					}
				}
			}
		}
	}
	for _, role := range roleOrder() {
		if constraints[role].Policy != "preserve" || current[role].Inherited || current[role].Model == "" {
			continue
		}
		candidate := preservedCandidate(input, current[role])
		if !candidate.Native {
			continue
		}
		for _, host := range input.Hosts {
			if candidate.ConnectionID != "" && host.ConnectionID != candidate.ConnectionID {
				continue
			}
			endpoint := candidate.Endpoint
			if endpoint == "" {
				for _, connection := range input.Configuration.Providers {
					if connection.ID == host.ConnectionID {
						endpoint = connection.URL
					}
				}
			}
			verification := current[role].Verification
			if verification == nil || len(verification.NativeExecutions) == 0 || len(candidate.Pools) == 0 {
				for _, pool := range host.Inventory.Pools {
					reservations = append(reservations, PoolEstimate{Host: endpoint, PoolID: pool.ID, Bytes: pool.AvailableBytes, AvailableBytes: pool.AvailableBytes})
				}
				continue
			}
			seen := map[string]bool{}
			for _, execution := range verification.NativeExecutions {
				allocation := execution.Capacity.Allocation
				if allocation == nil {
					continue
				}
				if seen[allocation.Resources.Identity] {
					continue
				}
				seen[allocation.Resources.Identity] = true
				resident := false
				for _, actual := range host.Resources.Allocations {
					if actual.WorkerID == execution.OwnerEpoch+"/"+execution.WorkerIdentity && actual.Identity == allocation.Resources.Identity {
						resident = true
						break
					}
				}
				if resident {
					continue
				}
				for _, pool := range allocation.Resources.Pools {
					for _, observed := range host.Inventory.Pools {
						if pool.PoolID == observed.ID {
							key := candidate.Endpoint + "\n" + allocation.Resources.Identity + "\n" + pool.PoolID
							covered := min(pool.Bytes, protected[key])
							protected[key] -= covered
							reservations = append(reservations, PoolEstimate{Host: candidate.Endpoint, PoolID: pool.PoolID, Bytes: pool.Bytes - covered, AvailableBytes: observed.AvailableBytes})
						}
					}
				}
			}
		}
	}
	return reservations
}
