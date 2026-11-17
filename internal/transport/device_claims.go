package transport

import (
	"context"

	"github.com/contenox/runtime/internal/modeld/admission"
)

// NativeAllocation reports normalized placement and all resident resource pools before loading.
type NativeAllocation struct {
	Devices   []DeviceInfo         `json:"devices"`
	Config    Config               `json:"config"`
	Resources admission.Allocation `json:"resources"`
}

// DeviceClaimRequest identifies a managed worker and every accelerator it will allocate on.
type DeviceClaimRequest struct {
	OwnerInstance  string               `json:"ownerInstance"`
	WorkerInstance string               `json:"workerInstance"`
	Devices        []DeviceInfo         `json:"devices"`
	Config         Config               `json:"config"`
	Resources      admission.Allocation `json:"resources"`
}

// DeviceClaim identifies an owner-held physical device lease in the worker's host namespace.
type DeviceClaim struct {
	DeviceID  string `json:"deviceId"`
	LeasePath string `json:"leasePath"`
	Instance  string `json:"instance"`
}

// DeviceClaimResult carries the fencing identities the worker must observe until it exits.
type DeviceClaimResult struct {
	Claims []DeviceClaim `json:"claims"`
}

// DeviceClaimant protects resolved native allocations before a managed backend opens them.
type DeviceClaimant interface {
	ClaimDevices(context.Context, NativeAllocation) error
}

// ManagedBackend binds native allocation to the owning worker's device claimant.
type ManagedBackend interface {
	WithDeviceClaims(DeviceClaimant) (Service, error)
}
