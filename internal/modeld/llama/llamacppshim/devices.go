package llamacppshim

// Device describes a ggml backend device registered in the linked runtime.
type Device struct {
	ID          string
	Index       int
	Name        string
	Description string
	Type        string
	MemoryFree  uint64
	MemoryTotal uint64
}

// DeduplicateDevices retains the first registered GPU for each nonempty physical
// identity, matching llama.cpp's device selection. Native indexes stay unchanged;
// devices without a physical identity and non-GPU devices remain distinct.
func DeduplicateDevices(devices []Device) []Device {
	seen := make(map[string]bool)
	out := make([]Device, 0, len(devices))
	for _, device := range devices {
		if device.Type == "gpu" && device.ID != "" {
			if seen[device.ID] {
				continue
			}
			seen[device.ID] = true
		}
		out = append(out, device)
	}
	return out
}
