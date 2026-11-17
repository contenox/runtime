package transport

import "github.com/contenox/runtime/internal/modeld/admission"

// CompletionProtection carries the explicit residency policy for one execution host.
type CompletionProtection struct {
	State        string                 `json:"state"`
	Reservations []admission.Protection `json:"reservations"`
}

// WorkerSpec selects a role's native process and allocation policy.
type WorkerSpec struct {
	ConfigurationDigest string `json:"configuration_digest,omitempty"`
	Digest              string `json:"digest,omitempty"`
	Config              Config `json:"config"`
	Role                string `json:"role"`
	Model               string `json:"model"`
	Backend             string `json:"backend"`
	Memory              string `json:"memory,omitempty"`
	Reserve             string `json:"reserve,omitempty"`
	ColdMemory          string `json:"cold_memory,omitempty"`
	IdleTTL             string `json:"idle_ttl,omitempty"`
	Headroom            string `json:"headroom,omitempty"`
	ComputeReserve      string `json:"compute_reserve,omitempty"`
	MinHotContext       string `json:"min_hot_context,omitempty"`
}

// WorkerStatus reports a managed process without probing unrelated workers.
type WorkerStatus struct {
	References       int        `json:"references"`
	Devices          []string   `json:"devices"`
	Spec             WorkerSpec `json:"spec"`
	Endpoint         string     `json:"endpoint,omitempty"`
	Instance         string     `json:"instance,omitempty"`
	State            string     `json:"state"`
	Error            string     `json:"error,omitempty"`
	QueuedForeground int        `json:"queued_foreground"`
	QueuedBackground int        `json:"queued_background"`
}

// Execution identifies the client request and the reviewed worker allocation settings.
// Scheduling identity is excluded from native model and cache identity by the owner.
type Execution struct {
	Context        int                  `json:"context"`
	Threads        int                  `json:"threads"`
	Batch          int                  `json:"batch"`
	KVCacheType    string               `json:"kv_cache_type"`
	ClientID       string               `json:"client_id"`
	Completion     CompletionProtection `json:"completion"`
	Target         ExecutionTarget      `json:"target"`
	Role           string               `json:"role"`
	Priority       string               `json:"priority"`
	RequesterID    string               `json:"requester_id"`
	Supersede      bool                 `json:"supersede"`
	Memory         string               `json:"memory"`
	Reserve        string               `json:"reserve"`
	ColdMemory     string               `json:"cold_memory"`
	IdleTTL        string               `json:"idle_ttl"`
	Headroom       string               `json:"headroom"`
	ComputeReserve string               `json:"compute_reserve"`
	MinHotContext  string               `json:"min_hot_context"`
}

// ApplySettings overlays reviewed allocation choices while retaining backend policy for zero-valued settings.
func (e Execution) ApplySettings(config Config) Config {
	if e.Context > 0 {
		config.NumCtx = e.Context
	}
	if e.Threads > 0 {
		config.NumThreads = e.Threads
	}
	if e.Batch > 0 {
		config.NumBatch = e.Batch
	}
	if e.KVCacheType != "" {
		config.KVCacheType = e.KVCacheType
	}
	return config
}

// WorkerReady is the private child startup handshake, never an independently discoverable owner lease.
type WorkerReady struct {
	PID      int    `json:"pid"`
	Endpoint string `json:"endpoint"`
	Instance string `json:"instance"`
	Backend  string `json:"backend"`
}
