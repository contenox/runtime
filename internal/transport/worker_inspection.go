package transport

// WorkerInspectionRequest selects an existing worker owned by the addressed daemon epoch.
type WorkerInspectionRequest struct {
	Fence
	WorkerInstance string `json:"workerInstance"`
}

// WorkerInspection reports residency and capacity through the owner without exposing a child connection.
type WorkerInspection struct {
	Status        DaemonStatus `json:"status"`
	Capacity      *ModelInfo   `json:"capacity,omitempty"`
	CapacityError string       `json:"capacity_error,omitempty"`
}
