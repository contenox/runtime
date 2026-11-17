package autosetup_test

import (
	"testing"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/modeld/capacity"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/autosetup"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/stretchr/testify/require"
)

func planningInput() autosetup.PlanningInput {
	configuration := inferencesetup.Configuration{}
	for _, role := range []string{"primary", "alternative", "autocomplete", "embedding"} {
		configuration.Roles = append(configuration.Roles, inferencesetup.RoleStatus{Assignment: inferencesetup.Assignment{Role: role, Inputs: inferencesetup.InputText}, Revision: "captured-" + role, AssignmentState: "unconfigured"})
	}
	for _, id := range []string{"first", "second"} {
		configuration.Providers = append(configuration.Providers, inferencesetup.ProviderConnection{ID: id, Selector: "connection:" + id, Type: "openai", URL: "https://" + id + ".example", Revision: "connection-" + id})
		configuration.Models = append(configuration.Models, inferencesetup.AvailableModel{Provider: "connection:" + id, Name: "same-name", Chat: true, Embed: true, CompletionMode: "instruct", ContextTokens: 65536, Vision: id == "second", Audio: id == "second"})
	}
	return autosetup.PlanningInput{Configuration: configuration}
}

func constraint(role, connection string) autosetup.RoleConstraint {
	return autosetup.RoleConstraint{Role: role, Policy: "connection", ConnectionID: connection, Inputs: inferencesetup.InputText, ExecutionMode: "protected"}
}

func TestUnit_PlannerCloudConnectionsAndMediaAreIndependent(t *testing.T) {
	input := planningInput()
	chat := constraint("primary", "second")
	chat.Inputs = inferencesetup.InputImagesAndAudio
	plan, err := autosetup.Evaluate(autosetup.RecommendationRequest{Mode: "cloud", Constraints: []autosetup.RoleConstraint{chat, constraint("autocomplete", "first"), constraint("embedding", "second"), constraint("alternative", "first")}}, input)
	require.NoError(t, err)
	require.Len(t, plan.Roles, 4)
	for _, role := range plan.Roles {
		require.Equal(t, "review", role.State)
		require.NotNil(t, role.Selected)
		require.Empty(t, role.Selected.Pools)
		require.False(t, role.Selected.Native)
		require.Equal(t, "connection:"+role.Constraint.ConnectionID, role.Selected.Route.Provider)
		require.Equal(t, "same-name", role.Selected.Route.Model)
		require.Equal(t, "captured-"+role.Constraint.Role, role.Selected.Route.RoleRevision)
	}
	chat.ConnectionID = "first"
	plan, err = autosetup.Evaluate(autosetup.RecommendationRequest{Mode: "cloud", Constraints: []autosetup.RoleConstraint{chat}}, input)
	require.NoError(t, err)
	for _, role := range plan.Roles {
		if role.Constraint.Role == "primary" {
			require.Equal(t, "unresolved", role.State)
			require.NotEmpty(t, role.Repairs)
		} else {
			require.Equal(t, "review", role.State)
		}
	}
}

func nativePlanningHost(id, endpoint string) (inferencesetup.ProviderConnection, autosetup.NativeHost) {
	connection := inferencesetup.ProviderConnection{ID: id, Name: id, Selector: "connection:" + id, Type: "modeld", URL: endpoint, Revision: "revision-" + id}
	host := autosetup.NativeHost{ConnectionID: id, Inventory: transport.Inventory{HostThreads: 8,
		Pools: []transport.MemoryPool{{ID: "host", Kind: "host", TotalBytes: 16 << 30, AvailableBytes: 12 << 30, State: "available"}, {ID: "same-gpu", Kind: "accelerator", TotalBytes: 8 << 30, AvailableBytes: 6 << 30, State: "available"}},
		Backends: []transport.BackendInventory{{Backend: "llama", State: "available", Operations: []string{"chat", "embedding"}, Scheduling: transport.SchedulingCapabilities{CooperativeYield: true, ThreadControl: true}, Devices: []transport.DeviceInfo{
			{ID: "cpu", Type: "cpu", State: "available", IdentityState: "stable", PoolID: "host", Operations: []string{"chat", "embedding"}},
			{ID: "same-gpu", Type: "gpu", State: "available", IdentityState: "stable", PoolID: "same-gpu", Operations: []string{"chat", "embedding"}},
		}}},
	}}
	return connection, host
}

func TestUnit_PlannerAccountsForHostDomainsAndRetainsResolvedRoles(t *testing.T) {
	input := planningInput()
	input.Artifacts = []modelregistry.ModelDescriptor{{Name: "completion", Backend: "llama", License: "apache-2.0", Curated: true, AutoEligible: true, SizeBytes: 1 << 30, AutoContextLimit: 65536, AutoKVProfile: capacity.LayerKVProfile{GlobalLayers: 1, PerLayerKVBytes: 2048}, Completion: modelregistry.CompletionProfile{Mode: "instruct", ContextTokens: 4096, Evidence: "test contract"}},
		{Name: "retrieval", Backend: "llama", License: "apache-2.0", Curated: true, AutoEligible: true, SizeBytes: 1 << 30, AutoContextLimit: 32768, AutoKVProfile: capacity.LayerKVProfile{GlobalLayers: 1, PerLayerKVBytes: 1024}, Embedding: modelregistry.EmbeddingProfile{Pooling: "mean", ContextTokens: 1024, Dimension: 64, Normalize: true, Evidence: "test contract"}}}
	for _, id := range []string{"local", "remote"} {
		endpoint := "remote.example:4321"
		if id == "local" {
			endpoint = "local"
		}
		connection, host := nativePlanningHost(id, endpoint)
		input.Configuration.Providers = append(input.Configuration.Providers, connection)
		input.Hosts = append(input.Hosts, host)
	}
	request := autosetup.RecommendationRequest{Mode: "mixed", Constraints: []autosetup.RoleConstraint{constraint("autocomplete", "local"), constraint("embedding", "remote"), constraint("primary", "first"), constraint("alternative", "second")}}
	plan, err := autosetup.Evaluate(request, input)
	require.NoError(t, err)
	for _, role := range plan.Roles {
		require.NotEqual(t, "unresolved", role.State)
		if role.Selected.Native {
			require.Equal(t, "acquire", role.State)
			require.Equal(t, "same-gpu", role.Selected.Route.Settings.Target.DeviceID)
			for _, pool := range role.Selected.Pools {
				require.Equal(t, role.Selected.Endpoint, pool.Host)
			}
		}
	}
	input.Hosts[1].Error = "remote owner requires update"
	plan, err = autosetup.Evaluate(request, input)
	require.NoError(t, err)
	for _, role := range plan.Roles {
		if role.Constraint.Role == "embedding" {
			require.Equal(t, "unresolved", role.State)
			require.Contains(t, role.Repairs[0], "requires update")
		} else {
			require.NotEqual(t, "unresolved", role.State)
		}
	}
}

func TestUnit_PlannerPreservesIndependentRecoveryAndInheritedRequirements(t *testing.T) {
	input := planningInput()
	input.Configuration.Roles[0].Assignment = inferencesetup.Assignment{Role: "primary", Provider: "connection:second", Model: "same-name", Inputs: inferencesetup.InputImagesAndAudio}
	input.Configuration.Roles[0].AssignmentState = "assigned"
	input.Configuration.Roles[1].Inherited = true
	input.Configuration.Roles[1].Inputs = inferencesetup.InputImagesAndAudio
	plan, err := autosetup.Evaluate(autosetup.RecommendationRequest{Mode: "mixed"}, input)
	require.NoError(t, err)
	for _, role := range plan.Roles {
		if role.Constraint.Role == "primary" {
			require.Equal(t, "preserved", role.State)
			require.Equal(t, inferencesetup.InputImagesAndAudio, role.Selected.Route.Inputs)
		}
		if role.Constraint.Role == "alternative" {
			require.Equal(t, "inherited", role.State)
			require.Equal(t, inferencesetup.InputImagesAndAudio, role.Constraint.Inputs)
		}
	}
	input.Configuration.Roles[1].Inherited = false
	input.Configuration.Roles[1].AssignmentState = "assigned"
	input.Configuration.Roles[1].Provider, input.Configuration.Roles[1].Model = "connection:first", "same-name"
	plan, err = autosetup.Evaluate(autosetup.RecommendationRequest{Mode: "mixed"}, input)
	require.NoError(t, err)
	for _, role := range plan.Roles {
		if role.Constraint.Role == "alternative" {
			require.Equal(t, "preserved", role.State)
			require.Equal(t, "connection:first", role.Selected.Route.Provider)
		}
	}
}

func TestUnit_PlannerChecksPreservedChatBeforeProtectingCompletion(t *testing.T) {
	for _, scenario := range []struct {
		name, chatModel, mode, state, scheduling string
		canYield, cpu                            bool
	}{
		{name: "non-yielding shared device", chatModel: "chat", mode: "protected", state: "unresolved"},
		{name: "cooperative distinct worker", chatModel: "chat", canYield: true, mode: "protected", state: "acquire", scheduling: "cooperative"},
		{name: "same loaded worker remains serial", chatModel: "completion", canYield: true, mode: "protected", state: "unresolved"},
		{name: "reviewed serialization", chatModel: "completion", canYield: true, mode: "serialized", state: "acquire", scheduling: "serialized"},
		{name: "independent CPU execution", chatModel: "chat", cpu: true, mode: "protected", state: "acquire", scheduling: "cooperative"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			input := planningInput()
			connection, host := nativePlanningHost("local", "local")
			if !scenario.cpu {
				host.Inventory.Backends[0].Devices = host.Inventory.Backends[0].Devices[1:]
			}
			input.Configuration.Providers = append(input.Configuration.Providers, connection)
			input.Hosts = []autosetup.NativeHost{host}
			input.Artifacts = []modelregistry.ModelDescriptor{{Name: "completion", Backend: "llama", License: "apache-2.0", Curated: true, AutoEligible: true, SizeBytes: 1 << 30, AutoContextLimit: 65536, AutoKVProfile: capacity.LayerKVProfile{GlobalLayers: 1, PerLayerKVBytes: 2048}, Completion: modelregistry.CompletionProfile{Mode: "instruct", ContextTokens: 4096, Evidence: "verified contract"}}}
			input.Configuration.Roles[0] = inferencesetup.RoleStatus{
				Assignment:      inferencesetup.Assignment{Role: "primary", Provider: connection.Selector, Model: scenario.chatModel, Inputs: inferencesetup.InputText},
				AssignmentState: "assigned", ConnectionID: connection.ID, State: "available",
				Verification: &inferencesetup.Verification{NativeExecutions: []transport.SessionExecution{{Backend: "llama", ModelName: scenario.chatModel,
					Resolved: transport.Config{Target: transport.ExecutionTarget{Mode: transport.PlacementAccelerator, DeviceID: "same-gpu"}, NumCtx: 4096, NumThreads: 2, KVCacheType: "f16"},
					Capacity: transport.ModelInfo{Allocation: &transport.NativeAllocation{Resources: admission.Allocation{Identity: "preserved", CanYield: scenario.canYield, Pools: []admission.PoolAllocation{{PoolID: "same-gpu", Bytes: 1 << 30}, {PoolID: "host", Bytes: 64 << 20}}}}},
				}}},
			}
			completion := constraint("autocomplete", "local")
			completion.ExecutionMode = scenario.mode
			plan, err := autosetup.Evaluate(autosetup.RecommendationRequest{Mode: "mixed", Constraints: []autosetup.RoleConstraint{completion}}, input)
			require.NoError(t, err)
			result := plan.Roles[0]
			require.Equal(t, scenario.state, result.State)
			if scenario.scheduling != "" {
				require.Equal(t, scenario.mode, result.Selected.Route.Settings.SchedulingMode)
				require.Equal(t, scenario.scheduling, result.Selected.Scheduling)
				if scenario.cpu {
					require.Equal(t, transport.PlacementCPU, result.Selected.Route.Settings.Target.Mode)
				}
			}
		})
	}
}

func TestUnit_PlannerAccountsForEnabledNextEditAndSkipsDisabledNextEdit(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled assignment is ignored", true: "enabled preserved allocation protects completion"}[enabled], func(t *testing.T) {
			input := planningInput()
			connection, host := nativePlanningHost("local", "local")
			if enabled {
				host.Inventory.Backends[0].Devices = host.Inventory.Backends[0].Devices[1:]
			}
			input.Configuration.Providers = append(input.Configuration.Providers, connection)
			input.Hosts = []autosetup.NativeHost{host}
			input.Artifacts = []modelregistry.ModelDescriptor{{Name: "completion", Backend: "llama", License: "apache-2.0", Curated: true, AutoEligible: true, SizeBytes: 1 << 30, AutoContextLimit: 65536,
				AutoKVProfile: capacity.LayerKVProfile{GlobalLayers: 1, PerLayerKVBytes: 2048}, Completion: modelregistry.CompletionProfile{Mode: "instruct", ContextTokens: 4096, Evidence: "verified contract"}}}
			settings := transport.Config{Target: transport.ExecutionTarget{Mode: transport.PlacementAccelerator, DeviceID: "same-gpu"}, NumCtx: 4096, NumThreads: 2, KVCacheType: "f16"}
			input.Configuration.Roles = append(input.Configuration.Roles, inferencesetup.RoleStatus{
				Assignment: inferencesetup.Assignment{Role: inferencesetup.RoleNextEdit, Provider: connection.Selector, Model: "completion", Inputs: inferencesetup.InputText},
				Enabled:    enabled, AssignmentState: "assigned", ConnectionID: connection.ID, State: "available",
				Verification: &inferencesetup.Verification{NativeExecutions: []transport.SessionExecution{{Backend: "llama", ModelName: "completion", Resolved: settings,
					Capacity: transport.ModelInfo{Allocation: &transport.NativeAllocation{Resources: admission.Allocation{Identity: "next-edit-worker", Pools: []admission.PoolAllocation{{PoolID: "same-gpu", Bytes: 1 << 30}, {PoolID: "host", Bytes: 64 << 20}}}}}}}},
			})
			request := autosetup.RecommendationRequest{Mode: "mixed", Constraints: []autosetup.RoleConstraint{constraint("autocomplete", "local")}}
			plan, err := autosetup.Evaluate(request, input)
			require.NoError(t, err)
			var nextEdit *autosetup.RolePlan
			var completion *autosetup.RolePlan
			for index := range plan.Roles {
				role := &plan.Roles[index]
				if role.Constraint.Role == inferencesetup.RoleNextEdit {
					nextEdit = role
				}
				if role.Constraint.Role == inferencesetup.RoleAutocomplete {
					completion = role
				}
			}
			require.NotNil(t, completion)
			if enabled {
				require.NotNil(t, nextEdit)
				require.Equal(t, "preserved", nextEdit.State)
				require.Equal(t, "unresolved", completion.State)
			} else {
				require.Nil(t, nextEdit)
				require.Equal(t, "acquire", completion.State)
			}
		})
	}
}

func TestUnit_PlannerOnlyIncludesDisabledNextEditWhenExplicitlyConstrained(t *testing.T) {
	input := planningInput()
	input.Configuration.Models = append(input.Configuration.Models, inferencesetup.AvailableModel{Provider: "connection:first", Name: "edit-model", NextEdit: true, NextEditFormat: "sweep-v1", ContextTokens: 8192})
	plan, err := autosetup.Evaluate(autosetup.RecommendationRequest{Mode: "cloud", Constraints: []autosetup.RoleConstraint{constraint(inferencesetup.RoleNextEdit, "first")}}, input)
	require.NoError(t, err)
	var nextEdit *autosetup.RolePlan
	for index := range plan.Roles {
		if plan.Roles[index].Constraint.Role == inferencesetup.RoleNextEdit {
			nextEdit = &plan.Roles[index]
		}
	}
	require.NotNil(t, nextEdit)
	require.Equal(t, "review", nextEdit.State)
	require.Equal(t, "edit-model", nextEdit.Selected.Route.Model)
	require.Contains(t, plan.Assignments, autosetup.PlannedRole{Role: inferencesetup.RoleNextEdit, Provider: "connection:first", Model: "edit-model"})
}
