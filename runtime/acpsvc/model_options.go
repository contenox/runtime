package acpsvc

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	libacp "github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/statetype"
)

// Model option encoding.
//
//	any/MODEL                     any connection serving MODEL (sentinel provider "any")
//	connection:BACKEND_ID/MODEL   exactly that connection
//	TYPE_OR_NAME/MODEL            legacy; accepted on input and mapped to one of the above
//
// MODEL may itself contain "/": only the first "/" after the provider prefix
// separates provider from model. Bare values are never accepted.
const (
	anyModelGroupID   = runtimestate.AnyProvider
	anyModelGroupName = "Any provider"
	connectionPrefix  = "connection:"
)

func servedModelName(p statetype.ModelPullStatus) string {
	if m := strings.TrimSpace(p.Model); m != "" {
		return m
	}
	return strings.TrimSpace(p.Name)
}

func servesModel(p statetype.ModelPullStatus) bool { return p.CanChat || p.CanPrompt }

// reachableConnections returns the connections without an error, ordered by name.
func reachableConnections(states []statetype.BackendRuntimeState) []statetype.BackendRuntimeState {
	var out []statetype.BackendRuntimeState
	for _, st := range states {
		if strings.TrimSpace(st.Error) != "" || strings.TrimSpace(st.Backend.Type) == "" {
			continue
		}
		out = append(out, st)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Backend.Name+"\x00"+out[i].Backend.ID) < strings.ToLower(out[j].Backend.Name+"\x00"+out[j].Backend.ID)
	})
	return out
}

func connectionValue(backendID, model string) string {
	return connectionPrefix + backendID + "/" + model
}

func anyValue(model string) string { return anyModelGroupID + "/" + model }

// splitModelValue splits a canonical or legacy value into provider and model.
// Bare values (no provider) are rejected.
func splitModelValue(value string) (provider, model string, ok bool) {
	value = strings.TrimSpace(value)
	rest := value
	prefixLen := 0
	if strings.HasPrefix(value, connectionPrefix) {
		prefixLen = len(connectionPrefix)
		rest = value[prefixLen:]
	}
	i := strings.Index(rest, "/")
	if i <= 0 || i == len(rest)-1 {
		return "", "", false
	}
	return strings.TrimSpace(value[:prefixLen+i]), strings.TrimSpace(rest[i+1:]), true
}

func sameModelName(a, b string) bool {
	return a == b || modelrepo.SameModel(a, b)
}

// serving lists the reachable connections that serve the model and match the
// provider selector (any or empty = all, connection:ID, or a legacy type/name).
func serving(states []statetype.BackendRuntimeState, provider, model string) []statetype.BackendRuntimeState {
	provider = strings.TrimSpace(provider)
	var out []statetype.BackendRuntimeState
	for _, st := range reachableConnections(states) {
		if provider != "" && !runtimestate.MatchesBackend(provider, st.Backend) {
			continue
		}
		for _, p := range st.PulledModels {
			if servesModel(p) && sameModelName(servedModelName(p), model) {
				out = append(out, st)
				break
			}
		}
	}
	return out
}

// canonicalModelSelection maps a stored provider/model pair (default type or
// name, connection selector, "any", or empty) to the option value it shows,
// or ok=false when no reachable connection serves the model.
func canonicalModelSelection(states []statetype.BackendRuntimeState, provider, model string) (value string, ok bool) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", false
	}
	matches := serving(states, provider, model)
	switch len(matches) {
	case 0:
		return "", false
	case 1:
		if !runtimestate.IsAnyProvider(provider) {
			return connectionValue(matches[0].Backend.ID, exactServedName(matches[0], model)), true
		}
	}
	return anyValue(exactServedName(matches[0], model)), true
}

func exactServedName(st statetype.BackendRuntimeState, model string) string {
	for _, p := range st.PulledModels {
		if servesModel(p) && sameModelName(servedModelName(p), model) {
			return servedModelName(p)
		}
	}
	return model
}

// resolveModelValue turns an option value (canonical or legacy) into the
// session provider selector and model name.
func resolveModelValue(states []statetype.BackendRuntimeState, value string) (provider, model string, ok bool) {
	prov, mod, split := splitModelValue(value)
	if !split {
		return "", "", false
	}
	canonical, found := canonicalModelSelection(states, prov, mod)
	if !found {
		return "", "", false
	}
	cp, cm, _ := splitModelValue(canonical)
	return cp, cm, true
}

func modelValueMeta(p statetype.ModelPullStatus) json.RawMessage {
	m := map[string]any{}
	if p.ContextLength > 0 {
		m["contenox.contextLength"] = p.ContextLength
	}
	if p.CanThink {
		m["contenox.canThink"] = true
	}
	if p.CanVision {
		m["contenox.canVision"] = true
	}
	if len(m) == 0 {
		return nil
	}
	return mustJSON(m)
}

// buildModelOptions builds the grouped model option: an "Any provider" group
// first (one value per distinct chat model; its context length is the minimum
// across serving connections), then one group per reachable connection.
// Models no reachable connection serves are never listed.
func buildModelOptions(states []statetype.BackendRuntimeState) libacp.SessionConfigValues {
	conns := reachableConnections(states)
	var groups []libacp.SessionConfigGroup

	type anyEntry struct {
		name string
		meta statetype.ModelPullStatus
		min  int
	}
	anyModels := map[string]*anyEntry{}
	var anyOrder []string

	for _, st := range conns {
		var opts []libacp.SessionConfigValue
		seen := map[string]bool{}
		for _, p := range st.PulledModels {
			name := servedModelName(p)
			if name == "" || !servesModel(p) || seen[name] {
				continue
			}
			seen[name] = true
			opts = append(opts, libacp.SessionConfigValue{
				Value:       connectionValue(st.Backend.ID, name),
				Name:        modelConfigDisplayName(name),
				Description: describePulledModel(p),
				Meta:        modelValueMeta(p),
			})
			key := strings.ToLower(name)
			e := anyModels[key]
			if e == nil {
				anyModels[key] = &anyEntry{name: name, meta: p, min: p.ContextLength}
				anyOrder = append(anyOrder, key)
			} else if p.ContextLength > 0 && (e.min == 0 || p.ContextLength < e.min) {
				e.min = p.ContextLength
			}
		}
		if len(opts) == 0 {
			continue
		}
		sort.SliceStable(opts, func(i, j int) bool { return strings.ToLower(opts[i].Name) < strings.ToLower(opts[j].Name) })
		name := strings.TrimSpace(st.Backend.Name)
		if name == "" {
			name = st.Backend.Type
		}
		groups = append(groups, libacp.SessionConfigGroup{
			Group:   st.Backend.ID,
			Name:    name,
			Options: opts,
			Meta: mustJSON(map[string]any{
				"contenox.providerType": modelrepo.CanonicalBackendType(st.Backend.Type),
				"contenox.health":       "ok",
			}),
		})
	}
	if len(groups) == 0 {
		return libacp.NewSessionConfigValues(nil)
	}

	anyOpts := make([]libacp.SessionConfigValue, 0, len(anyOrder))
	for _, key := range anyOrder {
		e := anyModels[key]
		meta := e.meta
		meta.ContextLength = e.min
		anyOpts = append(anyOpts, libacp.SessionConfigValue{
			Value:       anyValue(e.name),
			Name:        modelConfigDisplayName(e.name),
			Description: describePulledModel(meta),
			Meta:        modelValueMeta(meta),
		})
	}
	sort.SliceStable(anyOpts, func(i, j int) bool { return strings.ToLower(anyOpts[i].Name) < strings.ToLower(anyOpts[j].Name) })
	all := append([]libacp.SessionConfigGroup{{Group: anyModelGroupID, Name: anyModelGroupName, Options: anyOpts}}, groups...)
	return libacp.NewGroupedSessionConfigValues(all)
}

func (t *Transport) modelConfigOption(ctx context.Context, sess *sessionEntry) libacp.SessionConfigOption {
	states := t.runtimeStates(ctx)
	current, _ := canonicalModelSelection(states,
		sess.providerOrDefault(t.provider()), sess.modelOrDefault(t.model()))
	return libacp.SessionConfigOption{
		ID:           configIDModel,
		Name:         "Model",
		Category:     configCategoryModel,
		Type:         configTypeSelect,
		CurrentValue: current,
		Options:      buildModelOptions(states),
	}
}
