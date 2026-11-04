package acpsvc

import (
	"context"
	"encoding/json"
	"strings"

	libacp "github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/agentoptions"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

const ExtMethodAgentConfigOptions = "_contenox/agents/configOptions"

type agentConfigOptionsParams struct {
	Agent string `json:"agent"`
	Cwd   string `json:"cwd,omitempty"`
}

func (t *Transport) handleAgentConfigOptions(ctx context.Context, params json.RawMessage) (json.RawMessage, *libacp.Error) {
	var p agentConfigOptionsParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, libacp.NewErrorf(libacp.ErrInvalidParams, "invalid params: %v", err)
		}
	}
	p.Agent = strings.TrimSpace(p.Agent)
	if p.Agent == "" {
		return nil, libacp.NewError(libacp.ErrInvalidParams, "agent is required")
	}
	var opts []libacp.SessionConfigOption
	if t.deps.DB != nil {
		opts = agentoptions.Load(ctx, runtimetypes.New(t.deps.DB.WithoutTransaction()), p.Agent)
		if _, cfg, err := t.resolveExternalAgent(ctx, p.Agent); err == nil {
			opts = agentoptions.RestrictModes(opts, cfg.Modes, AgentModeConfigOptionID)
		}
	}
	out := make([]libacp.SessionConfigOption, 0, len(opts)+1)
	for _, o := range opts {
		if o.ID != configIDHITLPolicy {
			out = append(out, o)
		}
	}
	out = append(out, t.hitlPolicyConfigOption(&sessionEntry{WorkspaceID: t.workspaceID(), Cwd: strings.TrimSpace(p.Cwd)}))
	return mustJSON(workspaceConfigOptionsPayload{ConfigOptions: out}), nil
}
