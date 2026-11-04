package acpsvc

import (
	"context"

	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/agentoptions"
)

const configCategoryMode = agentoptions.CategoryMode

func isModeOption(o libacp.SessionConfigOption) bool {
	return agentoptions.IsMode(o, AgentModeConfigOptionID)
}

func modeAllowed(allowed []string, value string) bool {
	return agentoptions.Allowed(allowed, value)
}

func restrictModes(opts []libacp.SessionConfigOption, allowed []string) []libacp.SessionConfigOption {
	return agentoptions.RestrictModes(opts, allowed, AgentModeConfigOptionID)
}

func modeOptionOf(opts []libacp.SessionConfigOption, configID string) (libacp.SessionConfigOption, bool) {
	for _, o := range opts {
		if o.ID == configID && isModeOption(o) {
			return o, true
		}
	}
	return libacp.SessionConfigOption{}, false
}

func hasCategory(opts []libacp.SessionConfigOption, category string) bool {
	for _, o := range opts {
		if o.Category == category {
			return true
		}
	}
	return false
}

func (d *externalDriver) enforceStartMode(ctx context.Context, sess *sessionEntry, bridge *externalBridge) {
	allowed := bridge.allowedModesSnapshot()
	if len(allowed) == 0 {
		return
	}
	for _, o := range bridge.rawConfigOptionsSurface() {
		if !isModeOption(o) || modeAllowed(allowed, o.CurrentValue) {
			continue
		}
		_ = d.setAgentConfigOption(ctx, sess, o.ID, libacp.SessionConfigOptionValue{Str: allowed[0]})
		return
	}
}
