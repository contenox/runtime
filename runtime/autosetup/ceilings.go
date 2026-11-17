package autosetup

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
	"time"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/settings"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
)

const hostProbeTimeout = 8 * time.Second

// RoleMemory is one assigned role and the bytes its verified placement holds on
// a memory pool; Bytes is zero when the placement has not been verified.
type RoleMemory struct {
	Role     string `json:"role"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Bytes    int64  `json:"bytes"`
}

// CeilingResult is what a ceiling change leaves in force on a native
// connection. Unfit lists the roles whose verified placements together hold
// more of the pool than the new limit, and Unchecked the roles on the
// connection whose placement is not verified, so their fit is unknown.
type CeilingResult struct {
	Ceilings   []PoolCeiling `json:"ceilings"`
	Connection string        `json:"connection"`
	PoolID     string        `json:"poolId"`
	PoolLabel  string        `json:"poolLabel"`
	LimitBytes int64         `json:"limitBytes"`
	TotalBytes int64         `json:"totalBytes"`
	HeldBytes  int64         `json:"heldBytes"`
	Unfit      []RoleMemory  `json:"unfit"`
	Unchecked  []RoleMemory  `json:"unchecked"`
}

// DeviceView is one device a native connection reports.
type DeviceView struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Type        string   `json:"type"`
	Backend     string   `json:"backend"`
	State       string   `json:"state"`
	Error       string   `json:"error,omitempty"`
	Operations  []string `json:"operations"`
	MemoryBytes int64    `json:"memoryBytes"`
}

// PoolView is one memory pool a native connection reports, with the devices
// that draw on it and the ceiling its owner enforces.
type PoolView struct {
	ID             string       `json:"id"`
	Label          string       `json:"label"`
	Kind           string       `json:"kind"`
	State          string       `json:"state"`
	Error          string       `json:"error,omitempty"`
	TotalBytes     int64        `json:"totalBytes"`
	AvailableBytes int64        `json:"availableBytes"`
	Share          float64      `json:"share"`
	Bounded        bool         `json:"bounded"`
	Devices        []DeviceView `json:"devices"`
}

// HostDevices is what the planner sees on one native connection.
type HostDevices struct {
	ConnectionID string       `json:"connectionId"`
	Connection   string       `json:"connection"`
	Endpoint     string       `json:"endpoint"`
	Error        string       `json:"error,omitempty"`
	HostThreads  int          `json:"hostThreads"`
	Pools        []PoolView   `json:"pools"`
	Unpooled     []DeviceView `json:"unpooledDevices"`
}

func observeHost(ctx context.Context, connection inferencesetup.ProviderConnection, label string) (NativeHost, string, error) {
	host := NativeHost{ConnectionID: connection.ID}
	probeCtx, cancel := context.WithTimeout(ctx, hostProbeTimeout)
	defer cancel()
	address := connection.URL
	if address == modeldconn.LocalSentinel {
		var err error
		if address, err = modeldconn.LocalEndpointAddr(probeCtx); err != nil {
			return host, address, err
		}
	}
	client, err := modeldconn.Endpoint(probeCtx, label+":"+connection.ID, address)
	if err != nil {
		return host, address, err
	}
	if host.Inventory, err = client.Inventory(probeCtx); err != nil {
		return host, address, err
	}
	snapshot, err := client.Admission(probeCtx)
	if err != nil {
		return host, address, err
	}
	host.Resources = snapshot.Resources
	return host, address, nil
}

func nativeConnections(configuration inferencesetup.Configuration) []inferencesetup.ProviderConnection {
	var native []inferencesetup.ProviderConnection
	for _, connection := range configuration.Providers {
		if nativeConnection(connection) {
			native = append(native, connection)
		}
	}
	return native
}

// Devices reads the devices and memory pools the planner sees on every native
// connection, with the ceiling each pool carries. A connection that cannot be
// read is reported with its error rather than omitted.
func (s *Service) Devices(ctx context.Context, setup *inferencesetup.Service) ([]HostDevices, error) {
	if setup == nil {
		return nil, errors.New("device listing requires inference configuration")
	}
	configuration, err := setup.Configuration(ctx)
	if err != nil {
		return nil, err
	}
	hosts := []HostDevices{}
	for _, connection := range nativeConnections(configuration) {
		view := HostDevices{ConnectionID: connection.ID, Connection: connection.Name, Endpoint: connection.URL, Pools: []PoolView{}, Unpooled: []DeviceView{}}
		host, _, err := observeHost(ctx, connection, "devices")
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			view.Error = err.Error()
			hosts = append(hosts, view)
			continue
		}
		view.HostThreads = host.Inventory.HostThreads
		pooled := map[string]bool{}
		for _, pool := range host.Inventory.Pools {
			pooled[pool.ID] = true
			share, bounded := host.Resources.Ceilings[pool.ID]
			entry := PoolView{ID: pool.ID, Kind: pool.Kind, State: pool.State, Error: pool.Error, TotalBytes: pool.TotalBytes, AvailableBytes: pool.AvailableBytes, Share: share, Bounded: bounded, Devices: []DeviceView{}}
			for _, backend := range host.Inventory.Backends {
				for _, device := range backend.Devices {
					if device.PoolID == pool.ID {
						entry.Devices = append(entry.Devices, deviceView(backend, device))
					}
				}
			}
			entry.Label = poolLabel(pool, entry.Devices)
			view.Pools = append(view.Pools, entry)
		}
		for _, backend := range host.Inventory.Backends {
			for _, device := range backend.Devices {
				if !pooled[device.PoolID] {
					view.Unpooled = append(view.Unpooled, deviceView(backend, device))
				}
			}
		}
		hosts = append(hosts, view)
	}
	return hosts, nil
}

func deviceView(backend transport.BackendInventory, device transport.DeviceInfo) DeviceView {
	label := device.Description
	if label == "" {
		label = device.Name
	}
	if label == "" {
		label = device.ID
	}
	return DeviceView{ID: device.ID, Label: label, Type: device.Type, Backend: backend.Backend, State: device.State, Error: device.Error, Operations: device.Operations, MemoryBytes: device.MemoryTotal}
}

func poolLabel(pool transport.MemoryPool, devices []DeviceView) string {
	if pool.Kind == "host" {
		return "system memory"
	}
	for _, device := range devices {
		if device.Type != "cpu" && device.Label != "" {
			return device.Label
		}
	}
	return pool.ID
}

// ConfigureCeiling sets one memory pool's device ceiling on a native
// connection's owner and reports what it leaves in force. Lowering a ceiling
// never requires the connection's roles to be verified: the owner enforces the
// new limit on its next admission, and the result names the roles whose
// verified placements no longer fit and the roles whose fit is unknown. The
// owner refuses a limit below what its loaded workers already hold.
func (s *Service) ConfigureCeiling(ctx context.Context, setup *inferencesetup.Service, ceiling PoolCeiling) (CeilingResult, error) {
	if setup == nil {
		return CeilingResult{}, errors.New("device ceilings require inference configuration")
	}
	if err := settings.ValidateDeviceCeiling(ceiling.PoolID, ceiling.Share); err != nil {
		return CeilingResult{}, err
	}
	configuration, err := setup.Configuration(ctx)
	if err != nil {
		return CeilingResult{}, err
	}
	var connection *inferencesetup.ProviderConnection
	for index := range configuration.Providers {
		if configuration.Providers[index].ID == ceiling.ConnectionID && nativeConnection(configuration.Providers[index]) {
			connection = &configuration.Providers[index]
		}
	}
	if connection == nil {
		return CeilingResult{}, fmt.Errorf("device ceiling names %q, which is not a native connection", ceiling.ConnectionID)
	}
	host, address, err := observeHost(ctx, *connection, "ceiling")
	if err != nil {
		return CeilingResult{}, err
	}
	var pool transport.MemoryPool
	for _, observed := range host.Inventory.Pools {
		if observed.ID == ceiling.PoolID && observed.State == "available" {
			pool = observed
		}
	}
	if pool.TotalBytes <= 0 {
		return CeilingResult{}, fmt.Errorf("%s reports no available memory pool %s; choose a pool from its device list", connection.Name, ceiling.PoolID)
	}
	limit := int64(ceiling.Share * float64(pool.TotalBytes))
	result := CeilingResult{Connection: connection.Name, PoolID: pool.ID, LimitBytes: limit, TotalBytes: pool.TotalBytes, Unfit: []RoleMemory{}, Unchecked: []RoleMemory{}}
	var devices []DeviceView
	for _, backend := range host.Inventory.Backends {
		for _, device := range backend.Devices {
			if device.PoolID == pool.ID {
				devices = append(devices, deviceView(backend, device))
			}
		}
	}
	result.PoolLabel = poolLabel(pool, devices)
	input := PlanningInput{Configuration: configuration, Hosts: []NativeHost{host}}
	holders := []RoleMemory{}
	for _, role := range configuration.Roles {
		if role.Inherited || role.Model == "" {
			continue
		}
		candidate := preservedCandidate(input, role)
		if candidate.ConnectionID != connection.ID || !candidate.Native {
			continue
		}
		entry := RoleMemory{Role: role.Role, Name: role.Name, Provider: role.Provider, Model: role.Model}
		if candidate.ReviewError != "" {
			result.Unchecked = append(result.Unchecked, entry)
			continue
		}
		for _, held := range candidate.Pools {
			if held.PoolID == pool.ID {
				entry.Bytes += held.Bytes
			}
		}
		if entry.Bytes > 0 {
			result.HeldBytes += entry.Bytes
			holders = append(holders, entry)
		}
	}
	if result.HeldBytes > limit {
		result.Unfit = holders
	}
	sort.SliceStable(result.Unfit, func(i, j int) bool { return result.Unfit[i].Bytes > result.Unfit[j].Bytes })
	next := maps.Clone(host.Resources.Ceilings)
	if next == nil {
		next = map[string]float64{}
	}
	next[ceiling.PoolID] = ceiling.Share
	probeCtx, cancel := context.WithTimeout(ctx, hostProbeTimeout)
	defer cancel()
	client, err := modeldconn.Endpoint(probeCtx, "ceiling:"+connection.ID, address)
	if err != nil {
		return CeilingResult{}, err
	}
	if err := client.ConfigureCeilings(probeCtx, next); err != nil {
		if errors.Is(err, admission.ErrCapacity) {
			return CeilingResult{}, fmt.Errorf("the models loaded on %s right now already hold more of %s than %d%% allows; stop them or choose a higher limit: %w", connection.Name, result.PoolLabel, int(ceiling.Share*100+0.5), err)
		}
		return CeilingResult{}, err
	}
	host.Resources.Ceilings = next
	result.Ceilings, err = planningCeilings(RecommendationRequest{}, PlanningInput{Hosts: []NativeHost{host}})
	if err != nil {
		return CeilingResult{}, err
	}
	return result, nil
}
