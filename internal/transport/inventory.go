package transport

// SchedulingCapabilities reports native boundaries available to the owner scheduler.
type SchedulingCapabilities struct {
	SingleActiveRequest bool     `json:"single_active_request"`
	CooperativeYield    bool     `json:"cooperative_yield"`
	ThreadControl       bool     `json:"thread_control"`
	YieldOperations     []string `json:"yield_operations"`
	Limitations         []string `json:"limitations"`
}

// MemoryPool identifies a physical allocation pool; devices sharing host memory reference the same pool.
type MemoryPool struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"`
	TotalBytes     int64  `json:"total_bytes"`
	AvailableBytes int64  `json:"available_bytes"`
	State          string `json:"state"`
	Error          string `json:"error,omitempty"`
}

// BackendInventory reports native availability independently of any installed or resident model.
type BackendInventory struct {
	Scheduling    SchedulingCapabilities `json:"scheduling"`
	Backend       string                 `json:"backend"`
	State         string                 `json:"state"`
	Error         string                 `json:"error,omitempty"`
	RuntimeDigest string                 `json:"runtime_digest"`
	Operations    []string               `json:"operations"`
	Devices       []DeviceInfo           `json:"devices"`
}

// Inventory reports supported native execution and physical resource pools without loading a model.
type Inventory struct {
	HostThreads int                `json:"host_threads"`
	Backends    []BackendInventory `json:"backends"`
	Pools       []MemoryPool       `json:"pools"`
}
