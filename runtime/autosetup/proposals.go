package autosetup

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"time"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/modeld/capacity"
	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/internal/settings"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
)

// RoleConstraint selects one recommendation policy without changing the live assignment.
type RoleConstraint struct {
	Role               string                          `json:"role"`
	Policy             string                          `json:"policy"`
	ConnectionID       string                          `json:"connectionId,omitempty"`
	AllowedConnections []string                        `json:"allowedConnections"`
	Inputs             inferencesetup.InputRequirement `json:"inputs"`
	ExecutionMode      string                          `json:"executionMode"`
	Importance         Importance                      `json:"importance,omitempty"`
}

// PoolCeiling bounds what local inference may hold on one connection's memory
// pool, as a share of the pool's total bytes (0 < Share <= 1).
type PoolCeiling struct {
	ConnectionID string  `json:"connectionId"`
	PoolID       string  `json:"poolId"`
	Share        float64 `json:"share"`
}

// RecommendationRequest chooses local, cloud or mixed candidates and independent role constraints.
// Ceilings replace, per connection and pool, the device ceilings the connection's
// owner reports, so a plan can be previewed under limits not yet saved.
type RecommendationRequest struct {
	Mode        string           `json:"mode"`
	Origin      string           `json:"origin"`
	Constraints []RoleConstraint `json:"constraints"`
	Ceilings    []PoolCeiling    `json:"ceilings,omitempty"`
}

// PoolEstimate scopes a reservation to its owner's host and physical memory pool.
// CeilingBytes is the pool's device ceiling in bytes, absent when the pool has none.
type PoolEstimate struct {
	Host           string `json:"host"`
	PoolID         string `json:"poolId"`
	Bytes          int64  `json:"bytes"`
	AvailableBytes int64  `json:"availableBytes"`
	CeilingBytes   int64  `json:"ceilingBytes,omitempty"`
}

// RoleCandidate describes an editable route, its prerequisites and conservative allocation estimate.
type RoleCandidate struct {
	CompletionMode    string                         `json:"completionMode,omitempty"`
	ThreadLimit       int                            `json:"threadLimit"`
	Route             inferencesetup.RouteProposal   `json:"route"`
	ConnectionID      string                         `json:"connectionId"`
	Endpoint          string                         `json:"endpoint"`
	Native            bool                           `json:"native"`
	Installed         bool                           `json:"installed"`
	SharedMemory      bool                           `json:"sharedMemory"`
	Artifact          *modelregistry.ModelDescriptor `json:"artifact,omitempty"`
	Pools             []PoolEstimate                 `json:"pools"`
	ContextTokens     int                            `json:"contextTokens"`
	Scheduling        string                         `json:"scheduling"`
	Reason            string                         `json:"reason"`
	Verified          bool                           `json:"verified"`
	ReviewError       string                         `json:"reviewError,omitempty"`
	ExecutionSettings modeldconn.WorkerOptions       `json:"executionSettings"`
}

// RolePlan keeps one role's result available even when another role is unresolved.
type RolePlan struct {
	Constraint   RoleConstraint  `json:"constraint"`
	State        string          `json:"state"`
	Selected     *RoleCandidate  `json:"selected,omitempty"`
	Alternatives []RoleCandidate `json:"alternatives"`
	Repairs      []string        `json:"repairs"`
}

// NativeHost is an endpoint-scoped inventory and owner-confirmed residency observation.
type NativeHost struct {
	ConnectionID string              `json:"connectionId"`
	Inventory    transport.Inventory `json:"inventory"`
	Resources    admission.Resources `json:"resources"`
	Error        string              `json:"error,omitempty"`
}

// PlanningInput contains configuration, artifact contracts and host observations without credentials.
type PlanningInput struct {
	Configuration inferencesetup.Configuration
	Artifacts     []modelregistry.ModelDescriptor
	Hosts         []NativeHost
}

// Recommend reads independent connection observations before evaluating role constraints.
func (s *Service) Recommend(ctx context.Context, setup *inferencesetup.Service, request RecommendationRequest) (Plan, error) {
	if setup == nil {
		return Plan{}, errors.New("recommendations require inference configuration")
	}
	configuration, err := setup.Configuration(ctx)
	if err != nil {
		return Plan{}, err
	}
	artifacts, err := modelregistry.NewCurated().List(ctx)
	if err != nil {
		return Plan{}, err
	}
	input := PlanningInput{Configuration: configuration, Artifacts: artifacts}
	for _, connection := range configuration.Providers {
		if !nativeConnection(connection) || request.Mode == "cloud" {
			continue
		}
		host := NativeHost{ConnectionID: connection.ID}
		probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		address := connection.URL
		if address == modeldconn.LocalSentinel {
			address, err = modeldconn.LocalEndpointAddr(probeCtx)
		}
		if err == nil {
			var client modeldconn.EndpointClient
			client, err = modeldconn.Endpoint(probeCtx, "recommendation:"+connection.ID, address)
			if err == nil {
				host.Inventory, err = client.Inventory(probeCtx)
				if err == nil {
					var snapshot admission.Snapshot
					snapshot, err = client.Admission(probeCtx)
					host.Resources = snapshot.Resources
				}
			}
		}
		cancel()
		if err != nil {
			host.Error = err.Error()
			err = nil
		}
		input.Hosts = append(input.Hosts, host)
	}
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	plan, err := Evaluate(request, input)
	if err != nil {
		return Plan{}, err
	}
	for index := range plan.Roles {
		role := &plan.Roles[index]
		if role.State == "preserved" || role.State == "inherited" {
			continue
		}
		for alternative := range role.Alternatives {
			candidate := &role.Alternatives[alternative]
			if !candidate.Installed {
				continue
			}
			selected := role.Selected != nil && reflect.DeepEqual(role.Selected.Route, candidate.Route)
			reviewed, err := setup.Review(ctx, []inferencesetup.RouteProposal{candidate.Route})
			if err != nil {
				candidate.ReviewError = err.Error()
				if selected {
					role.State = "unresolved"
					copy := *candidate
					role.Selected = &copy
				}
				role.Repairs = append(role.Repairs, err.Error())
				continue
			}
			candidate.Route = reviewed[0]
			if selected {
				copy := *candidate
				role.Selected = &copy
			}
		}
	}
	plan.Fingerprint, err = planFingerprint(plan)
	return plan, err
}

func nativeConnection(connection inferencesetup.ProviderConnection) bool {
	return connection.Type == "modeld" || connection.Type == "local"
}

// idleReleaseTTL is how long a proposed native worker keeps its model after the
// last request. Long enough to keep a warm prefix through a working session, short
// enough that the device returns to the developer's other work when they stop.
// Contenox: Configure Local Model Workers replaces it per role.
const idleReleaseTTL = "20m"

// roleOrder is the documented planning order: completion protection is ranked
// before Chat capacity and retrieval.
func roleOrder() []string {
	return []string{inferencesetup.RoleAutocomplete, inferencesetup.RoleNextEdit, inferencesetup.RoleChat, inferencesetup.RoleRecovery, inferencesetup.RoleEmbedding}
}

// Evaluate ranks completion protection before Chat capacity and retrieval while accounting for each host pool independently.
func Evaluate(request RecommendationRequest, input PlanningInput) (Plan, error) {
	if !slices.Contains([]string{"local", "cloud", "mixed"}, request.Mode) {
		return Plan{}, errors.New("choose local, cloud or mixed recommendations")
	}
	if !slices.Contains([]string{"", "us", "eu", "china", "gus"}, request.Origin) {
		return Plan{}, errors.New("unknown model developer region")
	}
	constraints := map[string]RoleConstraint{}
	for _, constraint := range request.Constraints {
		if !slices.Contains(roleOrder(), constraint.Role) || constraints[constraint.Role].Role != "" || !slices.Contains([]string{"preserve", "local", "connection", "allowed"}, constraint.Policy) {
			return Plan{}, errors.New("each role requires one preserve, local, connection or allowed-connections constraint")
		}
		if constraint.Policy == "connection" && constraint.ConnectionID == "" || constraint.Policy == "allowed" && len(constraint.AllowedConnections) == 0 {
			return Plan{}, errors.New("select the connections allowed for this role")
		}
		if constraint.ExecutionMode != "protected" && constraint.ExecutionMode != "serialized" {
			return Plan{}, errors.New("select protected or serialized native execution")
		}
		if _, err := importanceRule(constraint.Importance); err != nil {
			return Plan{}, err
		}
		constraints[constraint.Role] = constraint
	}
	ceilings, err := planningCeilings(request, input)
	if err != nil {
		return Plan{}, err
	}
	current := map[string]inferencesetup.RoleStatus{}
	for _, role := range input.Configuration.Roles {
		current[role.Role] = role
	}
	planningOrder := make([]string, 0, len(roleOrder()))
	for _, role := range roleOrder() {
		_, explicit := constraints[role]
		if role != inferencesetup.RoleNextEdit || current[role].Enabled || explicit {
			planningOrder = append(planningOrder, role)
		}
	}
	plan := Plan{Mode: request.Mode, Roles: []RolePlan{}, Models: []modelregistry.ModelDescriptor{}, Assignments: []PlannedRole{}, Ceilings: ceilings}
	for _, role := range planningOrder {
		constraint, supplied := constraints[role]
		if !supplied {
			constraint = RoleConstraint{Role: role, Policy: "allowed", Inputs: current[role].Inputs, ExecutionMode: "protected"}
			for _, connection := range input.Configuration.Providers {
				constraint.AllowedConnections = append(constraint.AllowedConnections, connection.ID)
			}
			if current[role].AssignmentState == "assigned" || current[role].Inherited {
				constraint.Policy = "preserve"
			}
		}
		if constraint.Inputs == "" {
			constraint.Inputs = inferencesetup.InputText
		}
		constraints[role] = constraint
	}
	sort.SliceStable(planningOrder, func(i, j int) bool {
		left, _ := importanceRule(constraints[planningOrder[i]].Importance)
		right, _ := importanceRule(constraints[planningOrder[j]].Importance)
		return left.Rank < right.Rank
	})
	plan.Baseline = preservedReservations(input, current, constraints)
	preserved := []RolePlan{}
	for _, name := range planningOrder {
		if constraints[name].Policy == "preserve" && !current[name].Inherited && current[name].Model != "" {
			preserved = append(preserved, RolePlan{Constraint: constraints[name], State: "preserved", Selected: preservedCandidate(input, current[name])})
		}
	}
	used := map[string]int64{}
	for _, pool := range plan.Baseline {
		used[pool.Host+"\n"+pool.PoolID] += pool.Bytes
	}
	for _, role := range planningOrder {
		constraint := constraints[role]
		if !slices.Contains([]inferencesetup.InputRequirement{inferencesetup.InputText, inferencesetup.InputImages, inferencesetup.InputAudio, inferencesetup.InputImagesAndAudio}, constraint.Inputs) || role != inferencesetup.RoleChat && role != inferencesetup.RoleRecovery && constraint.Inputs != inferencesetup.InputText {
			return Plan{}, errors.New("required media inputs apply to Chat and Recovery")
		}
		result := RolePlan{Constraint: constraint, State: "unresolved", Alternatives: []RoleCandidate{}, Repairs: []string{}}
		rule, err := importanceRule(constraint.Importance)
		if err != nil {
			return Plan{}, err
		}
		if constraint.Policy == "preserve" {
			if current[role].Inherited {
				result.State = "inherited"
			} else if current[role].Model != "" && current[role].Provider != "" {
				result.State = "preserved"
				result.Selected = preservedCandidate(input, current[role])
				if result.Selected.ReviewError != "" {
					result.Repairs = append(result.Repairs, result.Selected.ReviewError)
				}
			} else {
				result.Repairs = append(result.Repairs, "Choose a connection or recommendation policy for this unconfigured role.")
			}
			plan.Roles = append(plan.Roles, result)
			continue
		}
		for _, connection := range input.Configuration.Providers {
			if !allowsConnection(constraint, connection, request.Mode) {
				continue
			}
			candidates, repairs := connectionCandidates(input, current[role], constraint, rule, ceilings, connection, request.Origin)
			result.Alternatives = append(result.Alternatives, candidates...)
			result.Repairs = append(result.Repairs, repairs...)
		}
		sort.SliceStable(result.Alternatives, func(i, j int) bool {
			a, b := result.Alternatives[i], result.Alternatives[j]
			if role == inferencesetup.RoleAutocomplete && (a.CompletionMode == modelregistry.CompletionFIM) != (b.CompletionMode == modelregistry.CompletionFIM) {
				return a.CompletionMode == modelregistry.CompletionFIM
			}
			if a.Verified != b.Verified {
				return a.Verified
			}
			if role == inferencesetup.RoleAutocomplete && a.Native != b.Native {
				return a.Native
			}
			if rule.Choice != ChoiceDocumented {
				if a.ContextTokens != b.ContextTokens {
					return (a.ContextTokens > b.ContextTokens) == (rule.Choice == ChoiceLargest)
				}
				if poolCost(a) != poolCost(b) {
					return (poolCost(a) > poolCost(b)) == (rule.Choice == ChoiceLargest)
				}
			}
			if role == inferencesetup.RoleChat || role == inferencesetup.RoleRecovery {
				if a.ContextTokens != b.ContextTokens {
					return a.ContextTokens > b.ContextTokens
				}
			}
			if a.Native && b.Native {
				if a.Route.Settings.Target.Mode != b.Route.Settings.Target.Mode {
					return a.Route.Settings.Target.Mode == transport.PlacementAccelerator
				}
				if a.ContextTokens != b.ContextTokens {
					return a.ContextTokens > b.ContextTokens
				}
				if a.Route.Settings.Target.Mode == transport.PlacementAccelerator && a.SharedMemory != b.SharedMemory {
					return !a.SharedMemory
				}
				if poolCost(a) != poolCost(b) {
					return poolCost(a) < poolCost(b)
				}
			}
			return a.Route.Provider+"/"+a.Route.Model < b.Route.Provider+"/"+b.Route.Model
		})
		ceilingRepairs := []string{}
		for _, candidate := range result.Alternatives {
			if fits, repair := candidateFits(candidate, used); !fits {
				if repair != "" && !slices.Contains(ceilingRepairs, repair) {
					ceilingRepairs = append(ceilingRepairs, repair)
				}
				continue
			}
			compatible := schedulingFits(candidate, plan.Roles) && schedulingFits(candidate, preserved)
			if !compatible && constraint.ExecutionMode != "serialized" {
				continue
			}
			selected := candidate
			if !compatible {
				selected.Scheduling = "serialized"
			}
			result.Selected, result.State = &selected, "review"
			if !candidate.Installed {
				result.State = "acquire"
			}
			for _, pool := range candidate.Pools {
				used[pool.Host+"\n"+pool.PoolID] += pool.Bytes
			}
			plan.Assignments = append(plan.Assignments, PlannedRole{Role: role, Provider: candidate.Route.Provider, Model: candidate.Route.Model})
			break
		}
		if result.Selected == nil {
			result.Repairs = append(result.Repairs, ceilingRepairs...)
			result.Repairs = append(result.Repairs, "Choose another connection, a smaller qualified model, another device, or explicitly review serialized execution.")
		}
		plan.Roles = append(plan.Roles, result)
	}
	plan.Fingerprint, err = planFingerprint(plan)
	return plan, err
}

// planningCeilings merges the ceilings each native owner reports with the
// request's replacements, ordered by connection and pool.
func planningCeilings(request RecommendationRequest, input PlanningInput) ([]PoolCeiling, error) {
	merged := map[[2]string]float64{}
	for _, host := range input.Hosts {
		for poolID, share := range host.Resources.Ceilings {
			merged[[2]string{host.ConnectionID, poolID}] = share
		}
	}
	replaced := map[[2]string]bool{}
	for _, ceiling := range request.Ceilings {
		key := [2]string{ceiling.ConnectionID, ceiling.PoolID}
		if replaced[key] {
			return nil, fmt.Errorf("device ceiling for pool %s on %s is given twice", ceiling.PoolID, ceiling.ConnectionID)
		}
		replaced[key] = true
		known := false
		for _, connection := range input.Configuration.Providers {
			known = known || connection.ID == ceiling.ConnectionID && nativeConnection(connection)
		}
		if !known {
			return nil, fmt.Errorf("device ceiling names %q, which is not a native connection", ceiling.ConnectionID)
		}
		if err := settings.ValidateDeviceCeiling(ceiling.PoolID, ceiling.Share); err != nil {
			return nil, err
		}
		merged[key] = ceiling.Share
	}
	result := make([]PoolCeiling, 0, len(merged))
	for key, share := range merged {
		result = append(result, PoolCeiling{ConnectionID: key[0], PoolID: key[1], Share: share})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ConnectionID != result[j].ConnectionID {
			return result[i].ConnectionID < result[j].ConnectionID
		}
		return result[i].PoolID < result[j].PoolID
	})
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

func allowsConnection(constraint RoleConstraint, connection inferencesetup.ProviderConnection, mode string) bool {
	if constraint.Policy == "connection" {
		return connection.ID == constraint.ConnectionID
	}
	if mode == "cloud" && nativeConnection(connection) || mode == "local" && (!nativeConnection(connection) || connection.URL != modeldconn.LocalSentinel) {
		return false
	}
	switch constraint.Policy {
	case "local":
		return nativeConnection(connection) && connection.URL == modeldconn.LocalSentinel
	case "connection":
		return connection.ID == constraint.ConnectionID
	case "allowed":
		return slices.Contains(constraint.AllowedConnections, connection.ID)
	}
	return false
}

func poolCost(candidate RoleCandidate) int64 {
	var total int64
	for _, pool := range candidate.Pools {
		total += pool.Bytes
	}
	return total
}

// restrictedLicense reports the license that keeps a curated artifact out of
// automatic acquisition. Such an artifact stays reachable by hand and is
// reported rather than dropped, so a device it could serve is never left
// unexplained.
func restrictedLicense(artifact modelregistry.ModelDescriptor, origin string) string {
	if !artifact.Curated || artifact.AutoAcquirable() {
		return ""
	}
	if origin != "" && artifact.Origin != origin {
		return ""
	}
	return artifact.License
}

// candidateFits bounds each pool by min(AvailableBytes, CeilingBytes) less what
// the plan already holds there. When only the device ceiling excludes the
// candidate it returns the repair naming that ceiling and pool.
func candidateFits(candidate RoleCandidate, used map[string]int64) (bool, string) {
	if candidate.ReviewError != "" {
		return false, ""
	}
	for _, pool := range candidate.Pools {
		held := used[pool.Host+"\n"+pool.PoolID]
		if pool.Bytes > pool.AvailableBytes-held {
			return false, ""
		}
	}
	for _, pool := range candidate.Pools {
		held := used[pool.Host+"\n"+pool.PoolID]
		if pool.CeilingBytes > 0 && pool.Bytes > pool.CeilingBytes-held {
			return false, fmt.Sprintf("%s needs %d bytes on pool %s, but the device ceiling for pool %s allows %d bytes and %d are already planned there; raise the device ceiling for %s, or choose a smaller model or another device.", candidate.Route.Model, pool.Bytes, pool.PoolID, pool.PoolID, pool.CeilingBytes, held, pool.PoolID)
		}
	}
	return true, ""
}

func schedulingFits(candidate RoleCandidate, roles []RolePlan) bool {
	if !candidate.Native {
		return true
	}
	for _, role := range roles {
		if role.Selected == nil || role.Constraint.Role == candidate.Route.Role {
			continue
		}
		if candidate.Route.Role == inferencesetup.RoleAutocomplete && !completionFits(*role.Selected, candidate) {
			return false
		}
		if role.Constraint.Role == inferencesetup.RoleAutocomplete && !completionFits(candidate, *role.Selected) {
			return false
		}
	}
	return true
}

func completionFits(candidate, completion RoleCandidate) bool {
	if !candidate.Native || !completion.Native || candidate.Endpoint != completion.Endpoint {
		return true
	}
	left, right := candidate.ExecutionSettings, completion.ExecutionSettings
	if candidate.Route.Model == completion.Route.Model && left.Backend == right.Backend && left.Target.Normalize() == right.Target.Normalize() &&
		(left.Context == 0 || right.Context == 0 || left.Context == right.Context) &&
		(left.Threads == 0 || right.Threads == 0 || left.Threads == right.Threads) &&
		(left.Batch == 0 || right.Batch == 0 || left.Batch == right.Batch) &&
		(left.KVCacheType == "" || right.KVCacheType == "" || left.KVCacheType == right.KVCacheType) {
		return false
	}
	if candidate.Scheduling == "cooperative" || candidate.Scheduling == "batch-boundary" {
		return true
	}
	for _, pool := range candidate.Pools {
		for _, reserved := range completion.Pools {
			if pool.PoolID == reserved.PoolID && pool.PoolID != "host" {
				return false
			}
		}
	}
	return left.Threads > 0 && right.Threads > 0 && candidate.ThreadLimit > 0 && left.Threads+right.Threads <= candidate.ThreadLimit
}

func connectionCandidates(input PlanningInput, current inferencesetup.RoleStatus, constraint RoleConstraint, rule ImportanceRule, ceilings []PoolCeiling, connection inferencesetup.ProviderConnection, origin string) ([]RoleCandidate, []string) {
	results := []RoleCandidate{}
	base := inferencesetup.RouteProposal{Assignment: inferencesetup.Assignment{Role: constraint.Role, Provider: connection.Selector, Inputs: constraint.Inputs}, RoleRevision: current.Revision, ConnectionRevision: connection.Revision, SettingsChange: inferencesetup.PreserveSettings}
	if !nativeConnection(connection) {
		for _, model := range input.Configuration.Models {
			if model.Provider != connection.Selector || !supportsRole(model, constraint) {
				continue
			}
			route := base
			route.Model = model.Name
			results = append(results, RoleCandidate{CompletionMode: model.CompletionMode, Route: route, ConnectionID: connection.ID, Endpoint: connection.URL, Installed: true, Pools: []PoolEstimate{}, ContextTokens: model.ContextTokens, Scheduling: "external", Verified: current.State == "available" && current.Provider == model.Provider && current.Model == model.Name, Reason: "Production role verification required; no local model allocation"})
		}
		return results, nil
	}
	var host *NativeHost
	for index := range input.Hosts {
		if input.Hosts[index].ConnectionID == connection.ID {
			host = &input.Hosts[index]
			break
		}
	}
	if host == nil {
		return results, []string{connection.Name + ": no native runtime inventory is available for this connection; " + remedy.PrepareLocal + "."}
	}
	if host.Error != "" {
		return results, []string{connection.Name + ": " + host.Error}
	}
	repairs := []string{}
	hostCeilings := map[string]float64{}
	for _, ceiling := range ceilings {
		if ceiling.ConnectionID == connection.ID {
			hostCeilings[ceiling.PoolID] = ceiling.Share
		}
	}
	for _, artifact := range input.Artifacts {
		if !artifact.Curated || origin != "" && artifact.Origin != origin {
			continue
		}
		if constraint.Inputs == inferencesetup.InputAudio || constraint.Inputs == inferencesetup.InputImagesAndAudio {
			continue
		}
		if constraint.Inputs == inferencesetup.InputImages && !artifact.Vision {
			continue
		}
		contextTokens := rule.MinChatContextTokens
		switch constraint.Role {
		case inferencesetup.RoleAutocomplete:
			if !artifact.SupportsCompletion() {
				continue
			}
			contextTokens = artifact.Completion.ContextTokens
		case inferencesetup.RoleNextEdit:
			if !artifact.SupportsNextEdit() {
				continue
			}
			contextTokens = artifact.NextEdit.ContextTokens
		case inferencesetup.RoleEmbedding:
			if !artifact.SupportsRetrieval() {
				continue
			}
			contextTokens = artifact.Embedding.ContextTokens
		default:
			if !artifact.SupportsAgenticTools() {
				continue
			}
		}
		for _, backend := range host.Inventory.Backends {
			if backend.Backend != artifact.BackendType() || backend.State != "available" {
				continue
			}
			operation := "chat"
			if constraint.Role == inferencesetup.RoleAutocomplete && artifact.Completion.Mode == modelregistry.CompletionFIM {
				operation = "autocomplete"
			}
			if constraint.Role == inferencesetup.RoleNextEdit {
				operation = "next-edit"
			}
			if constraint.Role == inferencesetup.RoleEmbedding {
				operation = "embedding"
			}
			if constraint.Inputs == inferencesetup.InputImages {
				operation = "vision"
			}
			if !slices.Contains(backend.Operations, operation) {
				continue
			}
			for _, device := range backend.Devices {
				if device.State != "available" || !slices.Contains(device.Operations, operation) || device.Type != "cpu" && device.IdentityState != "stable" {
					continue
				}
				if license := restrictedLicense(artifact, origin); license != "" {
					message := fmt.Sprintf("%s: %s can serve this device but is not downloaded automatically because its %s license is not cleared for automatic acquisition; %s to use it.", connection.Name, artifact.Label(), license, remedy.PullAndAssign(artifact.Name, constraint.Role, connection.Name))
					if !slices.Contains(repairs, message) {
						repairs = append(repairs, message)
					}
					continue
				}
				if !artifact.AutoEligible {
					continue
				}
				contexts := []int{contextTokens}
				if constraint.Role == inferencesetup.RoleChat || constraint.Role == inferencesetup.RoleRecovery {
					for _, tokens := range []int{65536, 131072, artifact.AutoContextLimit} {
						if tokens > contextTokens && tokens <= artifact.AutoContextLimit && !slices.Contains(contexts, tokens) {
							contexts = append(contexts, tokens)
						}
					}
				}
				for _, tokens := range contexts {
					candidate, err := nativeCandidate(base, connection, *host, backend, device, artifact, tokens, rule, hostCeilings)
					if err != nil {
						message := connection.Name + ": " + err.Error()
						if !slices.Contains(repairs, message) {
							repairs = append(repairs, message)
						}
						continue
					}
					candidate.Route.Settings.SchedulingMode = constraint.ExecutionMode
					candidate.ExecutionSettings.SchedulingMode = constraint.ExecutionMode
					for _, model := range input.Configuration.Models {
						if model.Provider == connection.Selector && model.Name == artifact.Name && model.NativeBackend == artifact.BackendType() {
							candidate.Installed = true
						}
					}
					results = append(results, candidate)
				}
			}
		}
	}
	return results, repairs
}

func supportsRole(model inferencesetup.AvailableModel, constraint RoleConstraint) bool {
	if (constraint.Inputs == inferencesetup.InputImages || constraint.Inputs == inferencesetup.InputImagesAndAudio) && !model.Vision {
		return false
	}
	if (constraint.Inputs == inferencesetup.InputAudio || constraint.Inputs == inferencesetup.InputImagesAndAudio) && !model.Audio {
		return false
	}
	switch constraint.Role {
	case inferencesetup.RoleEmbedding:
		return model.Embed
	case inferencesetup.RoleNextEdit:
		return model.NextEdit
	case inferencesetup.RoleAutocomplete:
		return model.Autocomplete && model.CompletionMode == modelregistry.CompletionFIM || model.Chat && model.CompletionMode == modelregistry.CompletionInstruct || model.Prompt && model.CompletionMode == modelregistry.CompletionPrompt
	default:
		return model.Chat
	}
}

func nativeCandidate(route inferencesetup.RouteProposal, connection inferencesetup.ProviderConnection, host NativeHost, backend transport.BackendInventory, device transport.DeviceInfo, artifact modelregistry.ModelDescriptor, tokens int, rule ImportanceRule, ceilings map[string]float64) (RoleCandidate, error) {
	pools := map[string]transport.MemoryPool{}
	for _, pool := range host.Inventory.Pools {
		pools[pool.ID] = pool
	}
	pool, ram := pools[device.PoolID], pools["host"]
	if pool.State != "available" || ram.State != "available" || host.Inventory.HostThreads <= 0 {
		return RoleCandidate{}, errors.New("native resource telemetry is incomplete")
	}
	policy, err := capacity.ResolvePolicy(capacity.DefaultPolicy(), capacity.DeviceSnapshot{Kind: device.Type, TotalBytes: pool.TotalBytes, FreeBytes: pool.AvailableBytes}, capacity.DeviceSnapshot{Kind: "system", TotalBytes: ram.TotalBytes, FreeBytes: ram.AvailableBytes})
	if err != nil {
		return RoleCandidate{}, err
	}
	budget := capacity.Resolve(capacity.Params{FreeBytes: pool.AvailableBytes, UserLimitBytes: policy.MaxResidentBytes, MinFreeBytes: policy.MinFreeBytes}).UsableBytes
	if !artifact.AutoKVProfile.Valid() || tokens > artifact.AutoContextLimit {
		return RoleCandidate{}, errors.New("native model allocation contract is incomplete")
	}
	bytes := artifact.ContextMemoryBytes(tokens, "f16")
	if bytes > budget {
		return RoleCandidate{}, errors.New("native allocation exceeds the pool budget")
	}
	target := transport.ExecutionTarget{Mode: transport.PlacementAccelerator, DeviceID: device.ID}
	if device.Type == "cpu" {
		target = transport.ExecutionTarget{Mode: transport.PlacementCPU}
	}
	route.Model, route.SettingsChange = artifact.Name, inferencesetup.ReplaceSettings
	route.Settings = modeldconn.WorkerOptions{Target: target, Backend: backend.Backend, Context: tokens, KVCacheType: "f16", Memory: fmt.Sprintf("%dB", int64(float64(bytes)/(1-rule.HeadroomFrac))+1), Reserve: "auto", ColdMemory: "0%", Headroom: "auto", IdleTTL: idleReleaseTTL}
	if backend.Scheduling.ThreadControl {
		route.Settings.Threads = max(1, host.Inventory.HostThreads/2)
		if route.Role == inferencesetup.RoleAutocomplete || route.Role == inferencesetup.RoleEmbedding {
			route.Settings.Threads = max(1, host.Inventory.HostThreads/4)
		}
	}
	if route.Role == inferencesetup.RoleAutocomplete {
		route.Settings.Residency = "protected"
	}
	result := RoleCandidate{CompletionMode: artifact.Completion.Mode, ThreadLimit: host.Inventory.HostThreads, Route: route, ConnectionID: connection.ID, Endpoint: connection.URL, Native: true, SharedMemory: device.SharedMemory, Artifact: &artifact, ContextTokens: tokens, Scheduling: "serialized", Reason: "Curated operation contract; metadata and production verification required", Pools: []PoolEstimate{{Host: connection.URL, PoolID: device.PoolID, Bytes: bytes, AvailableBytes: budget, CeilingBytes: ceilingBytes(ceilings, pool)}}}
	if backend.Scheduling.CooperativeYield {
		result.Scheduling = "cooperative"
	}
	if route.Role == inferencesetup.RoleEmbedding {
		result.Scheduling = "batch-boundary"
		result.Reason = "Dedicated retrieval contract; active embedding batches finish before completion can acquire the shared device"
	}
	result.ExecutionSettings = route.Settings
	if device.PoolID != "host" {
		result.Pools = append(result.Pools, PoolEstimate{Host: connection.URL, PoolID: "host", Bytes: capacity.NativeHostOverheadBytes, AvailableBytes: max(0, ram.AvailableBytes-policy.MinFreeBytes), CeilingBytes: ceilingBytes(ceilings, ram)})
	} else {
		result.Pools[0].Bytes += capacity.NativeHostOverheadBytes
	}
	return result, nil
}

func ceilingBytes(ceilings map[string]float64, pool transport.MemoryPool) int64 {
	share, bounded := ceilings[pool.ID]
	if !bounded {
		return 0
	}
	return max(int64(share*float64(pool.TotalBytes)), 1)
}
