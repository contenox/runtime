package chainagents

import (
	"encoding/json"
	"os"
	"slices"

	"github.com/contenox/runtime/runtime/missiontools"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
)

func MissionCapable(agent *runtimetypes.Agent) bool {
	if agent == nil || agent.Kind != runtimetypes.AgentKindChain {
		return false
	}
	cfg, err := agent.ChainConfig()
	if err != nil || cfg.Path == "" {
		return false
	}
	data, err := os.ReadFile(cfg.Path)
	if err != nil {
		return false
	}
	var chain taskengine.TaskChainDefinition
	if err := json.Unmarshal(data, &chain); err != nil {
		return false
	}
	if shippedAgentChains[chain.ID] || shippedAgentChains[agent.Name] {
		return false
	}
	return ChainGrantsMissionTools(&chain)
}

func ChainGrantsMissionTools(chain *taskengine.TaskChainDefinition) bool {
	if chain == nil {
		return false
	}
	for _, task := range chain.Tasks {
		if task.ExecuteConfig == nil || len(task.ExecuteConfig.Tools) == 0 {
			continue
		}
		if slices.Contains(task.ExecuteConfig.Tools, missiontools.ToolsProviderName) {
			return true
		}
	}
	return false
}

func WithMissionCapability(agents []*runtimetypes.Agent) []*runtimetypes.Agent {
	for _, a := range agents {
		if a != nil {
			a.MissionCapable = MissionCapable(a)
		}
	}
	return agents
}
