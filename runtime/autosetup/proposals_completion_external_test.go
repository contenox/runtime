package autosetup_test

import (
	"strings"
	"testing"

	"github.com/contenox/runtime/internal/modeld/capacity"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/autosetup"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/stretchr/testify/require"
)

func completionArtifact(name string, vramGB int, weights int64, layers int, perLayerKV int64) modelregistry.ModelDescriptor {
	return modelregistry.ModelDescriptor{
		Name: name, Backend: "llama", Curated: true, AutoEligible: true, License: "apache-2.0",
		SizeBytes: weights, RecommendedVRAMGB: vramGB, AutoContextLimit: 32768,
		AutoKVProfile: capacity.LayerKVProfile{GlobalLayers: layers, PerLayerKVBytes: perLayerKV},
		Completion:    modelregistry.CompletionProfile{Mode: modelregistry.CompletionFIM, FIMFormat: "qwen2.5-coder", ContextTokens: 8192, Evidence: "test contract"},
	}
}

func chatArtifact(name string, vramGB int, weights int64, layers int, perLayerKV int64) modelregistry.ModelDescriptor {
	return modelregistry.ModelDescriptor{
		Name: name, Backend: "llama", Curated: true, AutoEligible: true, License: "apache-2.0",
		SizeBytes: weights, RecommendedVRAMGB: vramGB, AutoContextLimit: 32768,
		ToolProtocol: "llama:common_chat_tool_parser", AgenticTier: modelregistry.AgenticTierAgentic,
		AutoKVProfile: capacity.LayerKVProfile{GlobalLayers: layers, PerLayerKVBytes: perLayerKV},
	}
}

func nativeHost(id string, hostTotal, hostFree int64, accelerators ...*transport.MemoryPool) autosetup.NativeHost {
	operations := []string{"autocomplete", "next-edit", "chat", "embedding"}
	pools := []transport.MemoryPool{{ID: "host", Kind: "host", TotalBytes: hostTotal, AvailableBytes: hostFree, State: "available"}}
	devices := []transport.DeviceInfo{{ID: "cpu", Type: "cpu", State: "available", IdentityState: "stable", PoolID: "host", Operations: operations}}
	for _, accelerator := range accelerators {
		pools = append(pools, *accelerator)
		devices = append(devices, transport.DeviceInfo{ID: accelerator.ID, Type: "gpu", State: "available", IdentityState: "stable", PoolID: accelerator.ID, Operations: operations, SharedMemory: accelerator.Kind == "igpu"})
	}
	return autosetup.NativeHost{ConnectionID: id, Inventory: transport.Inventory{HostThreads: 16, Pools: pools,
		Backends: []transport.BackendInventory{{Backend: "llama", State: "available", Operations: operations,
			Scheduling: transport.SchedulingCapabilities{CooperativeYield: true, ThreadControl: true}, Devices: devices}}}}
}

func planRoles(t *testing.T, artifacts []modelregistry.ModelDescriptor, host autosetup.NativeHost, roles ...string) map[string]autosetup.RolePlan {
	t.Helper()
	input := planningInput()
	input.Configuration.Providers = append(input.Configuration.Providers, inferencesetup.ProviderConnection{ID: "native", Name: "native", Selector: "connection:native", Type: "modeld", URL: "local", Revision: "captured-native"})
	input.Artifacts = artifacts
	input.Hosts = []autosetup.NativeHost{host}
	constraints := []autosetup.RoleConstraint{}
	for _, role := range roles {
		constraints = append(constraints, constraint(role, "native"))
	}
	plan, err := autosetup.Evaluate(autosetup.RecommendationRequest{Mode: "local", Constraints: constraints}, input)
	require.NoError(t, err)
	planned := map[string]autosetup.RolePlan{}
	for _, role := range plan.Roles {
		planned[role.Constraint.Role] = role
	}
	return planned
}

func completionPlan(t *testing.T, artifacts []modelregistry.ModelDescriptor, host autosetup.NativeHost) autosetup.RolePlan {
	t.Helper()
	return planRoles(t, artifacts, host, "autocomplete")["autocomplete"]
}

func TestUnit_PlannerKeepsCompletionAtTheSufficientTier(t *testing.T) {
	sufficient := completionArtifact("fim-sufficient", 3, 1646573056, 28, 1024)
	oversized := completionArtifact("fim-oversized", 8, 4683073824, 28, 2048)
	large := &transport.MemoryPool{ID: "gpu", Kind: "accelerator", TotalBytes: 32 << 30, AvailableBytes: 30 << 30, State: "available"}
	role := completionPlan(t, []modelregistry.ModelDescriptor{sufficient, oversized}, nativeHost("native", 64<<30, 60<<30, large))
	require.NotNil(t, role.Selected)
	require.Equal(t, "fim-sufficient", role.Selected.Route.Model, "a larger device must not escalate the completion tier")
	offered := []string{}
	for _, alternative := range role.Alternatives {
		offered = append(offered, alternative.Route.Model)
	}
	require.Contains(t, offered, "fim-oversized", "the larger tier stays reachable by hand")
}

func TestUnit_PlannerKeepsTheDeviceAvailableForTheOtherRoles(t *testing.T) {
	accelerator := &transport.MemoryPool{ID: "gpu", Kind: "accelerator", TotalBytes: 10 << 30, AvailableBytes: 9 << 30, State: "available"}
	roles := planRoles(t,
		[]modelregistry.ModelDescriptor{
			completionArtifact("fim-sufficient", 3, 1646573056, 28, 1024),
			completionArtifact("fim-oversized", 8, 4683073824, 28, 2048),
			chatArtifact("chat-capable", 8, 3<<30, 32, 2048),
		},
		nativeHost("native", 64<<30, 60<<30, accelerator),
		"autocomplete", "primary")
	completion, chat := roles["autocomplete"], roles["primary"]
	require.NotNil(t, completion.Selected)
	require.Equal(t, "fim-sufficient", completion.Selected.Route.Model)
	acceleratorBytes := int64(0)
	for _, pool := range completion.Selected.Pools {
		if pool.PoolID == "gpu" {
			acceleratorBytes += pool.Bytes
		}
	}
	require.Less(t, acceleratorBytes, int64(2500<<20), "the sufficient tier must not fill the device")
	require.Equal(t, "20m", completion.Selected.ExecutionSettings.IdleTTL, "a proposed worker must give the device back after it goes idle")
	require.NotNil(t, chat.Selected)
	require.Equal(t, transport.PlacementAccelerator, chat.Selected.Route.Settings.Target.Mode,
		"the oversized completion tier would push chat off the accelerator entirely")
}

func TestUnit_PlannerPrefersDedicatedAcceleratorMemory(t *testing.T) {
	shared := &transport.MemoryPool{ID: "igpu", Kind: "igpu", TotalBytes: 32 << 30, AvailableBytes: 30 << 30, State: "available"}
	dedicated := &transport.MemoryPool{ID: "dgpu", Kind: "accelerator", TotalBytes: 8 << 30, AvailableBytes: 7 << 30, State: "available"}
	role := completionPlan(t, []modelregistry.ModelDescriptor{
		completionArtifact("fim-sufficient", 3, 1646573056, 28, 1024),
	}, nativeHost("native", 64<<30, 60<<30, shared, dedicated))
	require.NotNil(t, role.Selected)
	require.False(t, role.Selected.SharedMemory)
	require.Equal(t, "dgpu", role.Selected.Pools[0].PoolID)
}

func openvinoCompletionHost(id string, shared bool) autosetup.NativeHost {
	host := nativeHost(id, 64<<30, 60<<30)
	operations := []string{"autocomplete", "next-edit", "chat", "embedding"}
	kind := "gpu"
	if shared {
		kind = "igpu"
	}
	host.Inventory.Pools = append(host.Inventory.Pools, transport.MemoryPool{ID: "ov-device", Kind: kind, TotalBytes: 16 << 30, AvailableBytes: 14 << 30, State: "available"})
	host.Inventory.Backends = []transport.BackendInventory{{Backend: "openvino", State: "available", Operations: operations,
		Scheduling: transport.SchedulingCapabilities{CooperativeYield: true, ThreadControl: true},
		Devices:    []transport.DeviceInfo{{ID: "ov-device", Type: kind, State: "available", IdentityState: "stable", PoolID: "ov-device", Operations: operations, SharedMemory: shared}}}}
	return host
}

func TestUnit_PlannerUsesAnOpenVINOAcceleratorForCompletion(t *testing.T) {
	artifact := completionArtifact("ov-fim", 6, 3828926634, 32, 2048)
	artifact.Backend = "openvino"
	role := completionPlan(t, []modelregistry.ModelDescriptor{artifact}, openvinoCompletionHost("native", false))
	require.NotNil(t, role.Selected)
	require.Equal(t, "ov-fim", role.Selected.Route.Model)
	require.Equal(t, transport.PlacementAccelerator, role.Selected.Route.Settings.Target.Mode)
	require.False(t, role.Selected.SharedMemory)
}

func TestUnit_PlannerReportsARestrictedLicenseInsteadOfDroppingIt(t *testing.T) {
	artifact := completionArtifact("ov-fim-restricted", 6, 3828926634, 32, 2048)
	artifact.Backend = "openvino"
	artifact.DisplayLabel = "Restricted OpenVINO FIM (INT4)"
	artifact.License = "bigcode-openrail-m"
	artifact.AutoEligible = false
	role := completionPlan(t, []modelregistry.ModelDescriptor{artifact}, openvinoCompletionHost("native", false))
	require.Nil(t, role.Selected)
	repairs := strings.Join(role.Repairs, "\n")
	require.Contains(t, repairs, "Restricted OpenVINO FIM (INT4)")
	require.Contains(t, repairs, "bigcode-openrail-m")
}
