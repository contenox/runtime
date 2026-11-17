package modeldconn

import (
	"context"
	"fmt"
	"sync"

	transportgrpc "github.com/contenox/runtime/internal/transport/grpc"
)

// LocalSentinel is the backend BaseURL value meaning "this modeld backend row
// is the local daemon" — reach it via the lease (LocalEndpointAddr), not a
// stored address.
const LocalSentinel = "local"

// LocalEndpointAddr resolves the current local owner, starting modeld if enabled.
func LocalEndpointAddr(ctx context.Context) (string, error) {
	st, err := readyDaemon(ctx)
	if err != nil {
		return "", err
	}
	return st.Endpoint, nil
}

// EndpointHealth reports what a node's Health RPC returned, cached alongside
// its connection.
type EndpointHealth struct {
	InstanceID string
	Backend    string // "llama" | "openvino" | "none"
	Ready      bool
}

// EndpointClient is a fenced connection to one modeld node (local or remote),
// reached by address rather than lease discovery.
type EndpointClient struct {
	*transportgrpc.Client
	EndpointHealth
}

type endpointEntry struct {
	address  string
	client   *transportgrpc.Client
	instance string
}

var (
	endpointsMu sync.Mutex
	endpoints   = map[string]*endpointEntry{}
)

// Endpoint dials (or reuses a cached connection to) the modeld node
// identified by backendID at addr.
func Endpoint(ctx context.Context, backendID, addr string) (EndpointClient, error) {
	if entry, ok := cachedEndpoint(backendID); ok {
		if health, err := entry.client.Health(ctx); entry.address == addr && err == nil && health.Ready && health.InstanceID == entry.instance {
			return EndpointClient{Client: entry.client, EndpointHealth: EndpointHealth{
				InstanceID: health.InstanceID, Backend: health.Backend, Ready: health.Ready,
			}}, nil
		}
		dropEndpoint(backendID, entry.client)
	}

	probe, err := transportgrpc.DialLeader(addr, "")
	if err != nil {
		return EndpointClient{}, fmt.Errorf("modeldconn: dial %s: %w", addr, err)
	}
	health, err := probe.Health(ctx)
	if err != nil {
		_ = probe.Close()
		return EndpointClient{}, fmt.Errorf("modeldconn: health probe %s: %w", addr, err)
	}
	if !health.Ready || health.InstanceID == "" {
		_ = probe.Close()
		return EndpointClient{}, fmt.Errorf("modeldconn: node %s reported not ready", addr)
	}

	_ = probe.Close()
	fenced, err := transportgrpc.DialLeader(addr, health.InstanceID)
	if err != nil {
		return EndpointClient{}, fmt.Errorf("modeldconn: dial %s: %w", addr, err)
	}

	endpointsMu.Lock()
	if existing := endpoints[backendID]; existing != nil && existing.address == addr && existing.instance == health.InstanceID {
		endpointsMu.Unlock()
		_ = fenced.Close()
		return EndpointClient{Client: existing.client, EndpointHealth: EndpointHealth{InstanceID: health.InstanceID, Backend: health.Backend, Ready: health.Ready}}, nil
	}
	endpoints[backendID] = &endpointEntry{client: fenced, instance: health.InstanceID, address: addr}
	endpointsMu.Unlock()

	return EndpointClient{Client: fenced, EndpointHealth: EndpointHealth{
		InstanceID: health.InstanceID, Backend: health.Backend, Ready: health.Ready,
	}}, nil
}

func cachedEndpoint(backendID string) (*endpointEntry, bool) {
	endpointsMu.Lock()
	defer endpointsMu.Unlock()
	entry, ok := endpoints[backendID]
	return entry, ok
}

func dropEndpoint(backendID string, stale *transportgrpc.Client) {
	endpointsMu.Lock()
	entry, ok := endpoints[backendID]
	if ok && entry.client == stale {
		delete(endpoints, backendID)
	}
	endpointsMu.Unlock()
	_ = stale.Close()
}

// CloseEndpoint drops and closes a cached endpoint connection.
func CloseEndpoint(backendID string) {
	endpointsMu.Lock()
	entry, ok := endpoints[backendID]
	delete(endpoints, backendID)
	endpointsMu.Unlock()
	if ok {
		_ = entry.client.Close()
	}
}
