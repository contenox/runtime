package modeldconn_test

import (
	"context"
	"testing"

	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/stretchr/testify/require"
)

func TestUnit_ExecutionEvidenceRequiresReviewedArtifactAndPlacement(t *testing.T) {
	cpu := transport.ExecutionTarget{Mode: transport.PlacementCPU}
	gpu := transport.ExecutionTarget{Mode: transport.PlacementAccelerator, DeviceID: "pci:0000:01:00.0"}
	automatic := transport.ExecutionTarget{Mode: transport.PlacementAutomatic}
	model := modeldconn.ModelRef{Name: "operator-model", Type: "llama", Digest: "reviewed"}
	for _, scenario := range []struct {
		name      string
		selected  transport.ExecutionTarget
		requested transport.ExecutionTarget
		resolved  transport.ExecutionTarget
		digest    string
		valid     bool
	}{
		{"cpu", cpu, cpu, cpu, "reviewed", true},
		{"gpu", gpu, gpu, gpu, "reviewed", true},
		{"automatic-cpu", automatic, automatic, cpu, "reviewed", true},
		{"automatic-gpu", automatic, automatic, gpu, "reviewed", true},
		{"substituted-gpu", cpu, cpu, gpu, "reviewed", false},
		{"substituted-request", gpu, cpu, cpu, "reviewed", false},
		{"unresolved-device", automatic, automatic, automatic, "reviewed", false},
		{"replaced-artifact", cpu, cpu, cpu, "different", false},
		{"missing-digest", cpu, cpu, cpu, "", false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx, err := modeldconn.WithWorkerOptions(t.Context(), "primary", modeldconn.WorkerOptions{Target: scenario.selected})
			require.NoError(t, err)
			ctx, capture := modeldconn.CaptureExecution(ctx)
			err = modeldconn.RecordExecution(ctx, model, &transport.SessionExecution{Backend: model.Type, ModelName: model.Name, Digest: scenario.digest,
				Requested: transport.Config{Target: scenario.requested}, Resolved: transport.Config{Target: scenario.resolved}})
			if scenario.valid {
				require.NoError(t, err)
				require.Len(t, capture.Reports(), 1)
			} else {
				require.Error(t, err)
				require.Empty(t, capture.Reports())
			}
		})
	}
}

func TestUnit_ExecutionEvidenceIsScopedAndImmutable(t *testing.T) {
	ctx, capture := modeldconn.CaptureExecution(t.Context())
	_, unrelated := modeldconn.CaptureExecution(t.Context())
	model := modeldconn.ModelRef{Name: "model", Type: "llama", Digest: "digest"}
	report := &transport.SessionExecution{Backend: model.Type, ModelName: model.Name, Digest: model.Digest,
		Requested: transport.Config{Target: transport.ExecutionTarget{Mode: transport.PlacementAutomatic}, SessionNamespace: "private-session", Execution: &transport.Execution{RequesterID: "private-editor"}},
		Resolved:  transport.Config{Target: transport.ExecutionTarget{Mode: transport.PlacementCPU}, TensorSplit: []float32{1}},
	}
	require.NoError(t, modeldconn.RecordExecution(ctx, model, report))
	report.Resolved.TensorSplit[0] = 2
	first := capture.Reports()
	require.Equal(t, float32(1), first[0].Resolved.TensorSplit[0])
	require.Empty(t, first[0].Requested.SessionNamespace)
	require.Nil(t, first[0].Requested.Execution)
	first[0].Resolved.TensorSplit[0] = 3
	require.Equal(t, float32(1), capture.Reports()[0].Resolved.TensorSplit[0])
	require.Empty(t, unrelated.Reports())
	require.Error(t, modeldconn.RecordExecution(ctx, model, nil))
	require.NoError(t, modeldconn.RecordExecution(context.Background(), model, nil))
}
