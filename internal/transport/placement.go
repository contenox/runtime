package transport

import (
	"fmt"

	"github.com/contenox/runtime/internal/remedy"
)

// PlacementMode distinguishes automatic placement from a reviewed CPU or accelerator selection.
type PlacementMode string

const (
	// PlacementAutomatic preserves the backend's configured automatic placement.
	PlacementAutomatic PlacementMode = "automatic"
	// PlacementCPU disables accelerator execution.
	PlacementCPU PlacementMode = "cpu"
	// PlacementAccelerator binds execution to the selected physical accelerator.
	PlacementAccelerator PlacementMode = "accelerator"
)

// ExecutionTarget selects physical placement independently of the model and backend.
type ExecutionTarget struct {
	Mode     PlacementMode `json:"mode"`
	DeviceID string        `json:"device_id,omitempty"`
}

// Normalize preserves legacy automatic placement without interpreting a missing device as CPU.
func (t ExecutionTarget) Normalize() ExecutionTarget {
	if t.Mode == "" {
		t.Mode = PlacementAutomatic
	}
	return t
}

// Validate rejects ambiguous placement selections before acquisition or inference.
func (t ExecutionTarget) Validate() error {
	t = t.Normalize()
	switch t.Mode {
	case PlacementAutomatic, PlacementCPU:
		if t.DeviceID != "" {
			return fmt.Errorf("%s placement must not specify an accelerator identity", t.Mode)
		}
	case PlacementAccelerator:
		if t.DeviceID == "" {
			return fmt.Errorf("accelerator placement requires a physical device identity; " + remedy.ReplanPlacement)
		}
	default:
		return fmt.Errorf("unsupported execution placement %q", t.Mode)
	}
	return nil
}
