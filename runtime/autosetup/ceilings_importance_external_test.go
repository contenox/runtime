package autosetup_test

import (
	"strings"
	"testing"

	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/autosetup"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/stretchr/testify/require"
)

const gpuTotal int64 = 10 << 30

func ceilingHost(ceilings map[string]float64) autosetup.NativeHost {
	host := nativeHost("native", 64<<30, 60<<30, &transport.MemoryPool{ID: "gpu", Kind: "accelerator", TotalBytes: gpuTotal, AvailableBytes: 9 << 30, State: "available"})
	host.Resources.Ceilings = ceilings
	return host
}

func evaluateNative(t *testing.T, artifacts []modelregistry.ModelDescriptor, host autosetup.NativeHost, ceilings []autosetup.PoolCeiling, importance map[string]autosetup.Importance, roles ...string) (autosetup.Plan, map[string]autosetup.RolePlan) {
	t.Helper()
	input := planningInput()
	input.Configuration.Providers = append(input.Configuration.Providers, inferencesetup.ProviderConnection{ID: "native", Name: "native", Selector: "connection:native", Type: "modeld", URL: "local", Revision: "captured-native"})
	input.Artifacts = artifacts
	input.Hosts = []autosetup.NativeHost{host}
	constraints := []autosetup.RoleConstraint{}
	for _, role := range roles {
		value := constraint(role, "native")
		value.Importance = importance[role]
		constraints = append(constraints, value)
	}
	plan, err := autosetup.Evaluate(autosetup.RecommendationRequest{Mode: "local", Constraints: constraints, Ceilings: ceilings}, input)
	require.NoError(t, err)
	planned := map[string]autosetup.RolePlan{}
	for _, role := range plan.Roles {
		planned[role.Constraint.Role] = role
	}
	return plan, planned
}

func poolBytes(candidate *autosetup.RoleCandidate, poolID string) int64 {
	for _, pool := range candidate.Pools {
		if pool.PoolID == poolID {
			return pool.Bytes
		}
	}
	return 0
}

const hostOnlyOverhead = 0.01

func TestUnit_PlannerReportsARoleAboveItsDeviceCeilingAsUnresolved(t *testing.T) {
	artifacts := []modelregistry.ModelDescriptor{chatArtifact("chat-capable", 8, 3<<30, 32, 2048)}
	_, unbounded := evaluateNative(t, artifacts, ceilingHost(map[string]float64{"host": hostOnlyOverhead}), nil, nil, "primary")
	require.NotNil(t, unbounded["primary"].Selected)
	require.Equal(t, "gpu", unbounded["primary"].Selected.Pools[0].PoolID)

	plan, bounded := evaluateNative(t, artifacts, ceilingHost(map[string]float64{"host": hostOnlyOverhead, "gpu": 0.1}), nil, nil, "primary")
	chat := bounded["primary"]
	require.Equal(t, "unresolved", chat.State)
	require.Nil(t, chat.Selected)
	require.Contains(t, strings.Join(chat.Repairs, "\n"), "device ceiling for pool gpu")
	require.Equal(t, []autosetup.PoolCeiling{{ConnectionID: "native", PoolID: "gpu", Share: 0.1}, {ConnectionID: "native", PoolID: "host", Share: hostOnlyOverhead}}, plan.Ceilings)

	plan, previewed := evaluateNative(t, artifacts, ceilingHost(map[string]float64{"host": hostOnlyOverhead}), []autosetup.PoolCeiling{{ConnectionID: "native", PoolID: "gpu", Share: 0.1}}, nil, "primary")
	require.Equal(t, "unresolved", previewed["primary"].State, "a requested ceiling previews the plan under limits not yet saved")
	require.Len(t, plan.Ceilings, 2)
}

func TestUnit_PlannerRolesOnOnePoolShareItsDeviceCeiling(t *testing.T) {
	artifacts := []modelregistry.ModelDescriptor{completionArtifact("fim-sufficient", 3, 1646573056, 28, 1024), chatArtifact("chat-capable", 8, 3<<30, 32, 2048)}
	_, unbounded := evaluateNative(t, artifacts, ceilingHost(map[string]float64{"host": hostOnlyOverhead}), nil, nil, "autocomplete", "primary")
	completionBytes, chatBytes := poolBytes(unbounded["autocomplete"].Selected, "gpu"), poolBytes(unbounded["primary"].Selected, "gpu")
	require.Positive(t, completionBytes)
	require.Positive(t, chatBytes)
	share := float64(completionBytes+chatBytes-1) / float64(gpuTotal)

	require.GreaterOrEqual(t, int64(share*float64(gpuTotal)), max(completionBytes, chatBytes), "either role alone fits under the ceiling")

	_, shared := evaluateNative(t, artifacts, ceilingHost(map[string]float64{"host": hostOnlyOverhead, "gpu": share}), nil, nil, "autocomplete", "primary")
	require.NotNil(t, shared["autocomplete"].Selected)
	require.Equal(t, "unresolved", shared["primary"].State)
	repairs := strings.Join(shared["primary"].Repairs, "\n")
	require.Contains(t, repairs, "device ceiling for pool gpu")
	require.Contains(t, repairs, "already planned there")
}

func TestUnit_PlannerImportanceSelectsTheLargerOrSmallerQualifiedChat(t *testing.T) {
	artifacts := []modelregistry.ModelDescriptor{chatArtifact("chat-small", 4, 2<<30, 32, 1024), chatArtifact("chat-large", 8, 4<<30, 32, 2048)}
	host := ceilingHost(map[string]float64{"host": hostOnlyOverhead})
	_, high := evaluateNative(t, artifacts, host, nil, map[string]autosetup.Importance{"primary": autosetup.ImportanceHigh}, "primary")
	require.Equal(t, "chat-large", high["primary"].Selected.Route.Model)
	_, low := evaluateNative(t, artifacts, host, nil, map[string]autosetup.Importance{"primary": autosetup.ImportanceLow}, "primary")
	require.Equal(t, "chat-small", low["primary"].Selected.Route.Model)
}

func TestUnit_PlannerLowImportanceCompletionYieldsItsPoolToHighImportanceChat(t *testing.T) {
	artifacts := []modelregistry.ModelDescriptor{completionArtifact("fim-sufficient", 3, 1646573056, 28, 1024), chatArtifact("chat-capable", 8, 3<<30, 32, 2048)}
	_, unbounded := evaluateNative(t, artifacts, ceilingHost(map[string]float64{"host": hostOnlyOverhead}), nil, nil, "autocomplete", "primary")
	share := float64(poolBytes(unbounded["autocomplete"].Selected, "gpu")+poolBytes(unbounded["primary"].Selected, "gpu")-1) / float64(gpuTotal)
	host := ceilingHost(map[string]float64{"host": hostOnlyOverhead, "gpu": share})

	_, documented := evaluateNative(t, artifacts, host, nil, nil, "autocomplete", "primary")
	require.NotNil(t, documented["autocomplete"].Selected)
	require.Equal(t, "unresolved", documented["primary"].State)

	plan, weighted := evaluateNative(t, artifacts, host, nil, map[string]autosetup.Importance{"autocomplete": autosetup.ImportanceLow, "primary": autosetup.ImportanceHigh}, "autocomplete", "primary")
	require.NotNil(t, weighted["primary"].Selected)
	require.Equal(t, "unresolved", weighted["autocomplete"].State)
	require.Equal(t, "primary", plan.Roles[0].Constraint.Role, "high importance consumes the pool first")
}

func TestUnit_PlannerRefusesAnUnknownImportanceAndAnInvalidCeiling(t *testing.T) {
	input := planningInput()
	input.Configuration.Providers = append(input.Configuration.Providers, inferencesetup.ProviderConnection{ID: "native", Name: "native", Selector: "connection:native", Type: "modeld", URL: "local"})
	bad := constraint("primary", "native")
	bad.Importance = "urgent"
	_, err := autosetup.Evaluate(autosetup.RecommendationRequest{Mode: "local", Constraints: []autosetup.RoleConstraint{bad}}, input)
	require.ErrorContains(t, err, "urgent")
	for _, ceiling := range []autosetup.PoolCeiling{{ConnectionID: "native", PoolID: "gpu", Share: 0}, {ConnectionID: "native", PoolID: "gpu", Share: 1.5}, {ConnectionID: "first", PoolID: "gpu", Share: 0.5}} {
		_, err = autosetup.Evaluate(autosetup.RecommendationRequest{Mode: "local", Ceilings: []autosetup.PoolCeiling{ceiling}}, input)
		require.Error(t, err, ceiling)
	}
	require.Equal(t, []string{"high", "normal", "low"}, autosetup.ImportanceIDs())
}
