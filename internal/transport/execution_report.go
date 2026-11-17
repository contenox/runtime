package transport

import "github.com/contenox/runtime/internal/modeld/admission"

// CopyExecutionReport returns an independent report without client identity or admission metadata.
func CopyExecutionReport(execution SessionExecution) *SessionExecution {
	execution.Requested = reportConfig(execution.Requested)
	execution.Resolved = reportConfig(execution.Resolved)
	if execution.Capacity.Allocation != nil {
		allocation := CopyAllocationReport(*execution.Capacity.Allocation)
		execution.Capacity.Allocation = &allocation
	}
	execution.Capacity.Devices = append([]DeviceInfo(nil), execution.Capacity.Devices...)
	for i := range execution.Capacity.Devices {
		execution.Capacity.Devices[i].Operations = append([]string(nil), execution.Capacity.Devices[i].Operations...)
	}
	return &execution
}

// CopyAllocationReport copies normalized resources without request or session identity.
func CopyAllocationReport(allocation NativeAllocation) NativeAllocation {
	allocation.Config = reportConfig(allocation.Config)
	allocation.Devices = append([]DeviceInfo(nil), allocation.Devices...)
	for index := range allocation.Devices {
		allocation.Devices[index].Operations = append([]string(nil), allocation.Devices[index].Operations...)
	}
	allocation.Resources.Pools = append([]admission.PoolAllocation(nil), allocation.Resources.Pools...)
	allocation.Resources.WorkerID, allocation.Resources.OwnerEpoch = "", ""
	return allocation
}

func reportConfig(config Config) Config {
	config.AdmissionPermit = admission.Permit{}
	config.SessionNamespace = ""
	config.Execution = nil
	config.TensorSplit = append([]float32(nil), config.TensorSplit...)
	return config
}
