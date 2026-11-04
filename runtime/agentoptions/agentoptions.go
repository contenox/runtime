package agentoptions

import (
	"context"
	"encoding/json"

	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

const kvPrefix = "acp:agent_config_options:"

const CategoryMode = "mode"

const CategoryModel = "model"

type Record struct {
	ConfigOptions []libacp.SessionConfigOption `json:"configOptions"`
}

func Save(ctx context.Context, store runtimetypes.Store, agent string, opts []libacp.SessionConfigOption) error {
	if agent == "" || len(opts) == 0 {
		return nil
	}
	raw, err := json.Marshal(Record{ConfigOptions: opts})
	if err != nil {
		return err
	}
	return store.SetKV(ctx, kvPrefix+agent, raw)
}

func Load(ctx context.Context, store runtimetypes.Store, agent string) []libacp.SessionConfigOption {
	var rec Record
	if err := store.GetKV(ctx, kvPrefix+agent, &rec); err != nil {
		return nil
	}
	return rec.ConfigOptions
}

func IsMode(o libacp.SessionConfigOption, syntheticModeID string) bool {
	return o.ID == syntheticModeID || o.Category == CategoryMode
}

func Allowed(allowed []string, value string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, a := range allowed {
		if a == value {
			return true
		}
	}
	return false
}

func RestrictModes(opts []libacp.SessionConfigOption, allowed []string, syntheticModeID string) []libacp.SessionConfigOption {
	if len(allowed) == 0 || len(opts) == 0 {
		return opts
	}
	out := make([]libacp.SessionConfigOption, 0, len(opts))
	for _, o := range opts {
		if !IsMode(o, syntheticModeID) {
			out = append(out, o)
			continue
		}
		filtered := o
		if len(o.Options.Groups) > 0 {
			groups := make([]libacp.SessionConfigGroup, 0, len(o.Options.Groups))
			for _, g := range o.Options.Groups {
				g.Options = keepAllowed(g.Options, allowed)
				if len(g.Options) > 0 {
					groups = append(groups, g)
				}
			}
			filtered.Options = libacp.NewGroupedSessionConfigValues(groups)
		} else {
			filtered.Options = libacp.NewSessionConfigValues(keepAllowed(o.Options.Values, allowed))
		}
		out = append(out, filtered)
	}
	return out
}

func keepAllowed(values []libacp.SessionConfigValue, allowed []string) []libacp.SessionConfigValue {
	out := make([]libacp.SessionConfigValue, 0, len(values))
	for _, v := range values {
		if Allowed(allowed, v.Value) {
			out = append(out, v)
		}
	}
	return out
}
