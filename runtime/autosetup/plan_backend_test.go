package autosetup

import (
	"github.com/contenox/runtime/runtime/modeldprobe"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestUnit_PlanBackendSharedAndStandaloneOwners(t *testing.T) {
	owner := modeldprobe.Status{State: modeldprobe.StateRunning, Backend: "shared"}
	require.Equal(t, "llama", planBackend("llama", "openvino", owner))
	require.Equal(t, "openvino", planBackend("", "openvino", owner))
	owner.Backend = "llama"
	require.Equal(t, "llama", planBackend("openvino", "openvino", owner))
}
