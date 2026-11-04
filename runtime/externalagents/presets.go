package externalagents

import (
	"fmt"
	"path/filepath"
	"runtime"
)

const PresetClaudeCode = "claude-code"

type Preset struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	AgentName   string   `json:"agentName"`
	Package     string   `json:"package"`
	Version     string   `json:"version"`
	MinNode     int      `json:"minNode"`
	Modes       []string `json:"modes"`
	Homepage    string   `json:"homepage"`
	entry       string
	nativeEntry string
	configEnv   string
	configDir   string
	configNeeds string
	env         map[string]string
	sessionMeta string
	unofficial  []string
}

var presets = []Preset{
	{
		ID:          PresetClaudeCode,
		Title:       "Claude Code",
		AgentName:   "claude-code",
		Package:     "@agentclientprotocol/claude-agent-acp",
		Version:     "0.100.0",
		MinNode:     22,
		Modes:       []string{"default", "plan"},
		Homepage:    "https://github.com/agentclientprotocol/claude-agent-acp",
		entry:       filepath.Join("node_modules", "@agentclientprotocol", "claude-agent-acp", "dist", "index.js"),
		nativeEntry: claudeNativeEntry(),
		configEnv:   "CLAUDE_CONFIG_DIR",
		configDir:   ".claude",
		configNeeds: "Claude's sign-in, settings and session history",
		env:         claudeEnv(),
		sessionMeta: claudeSessionMeta(),
		unofficial:  []string{"claude-ai-login"},
	},
}

func claudeNativeEntry() string {
	exe := "claude"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	return filepath.Join("node_modules", "@anthropic-ai", fmt.Sprintf("claude-agent-sdk-%s-%s", nodePlatform(runtime.GOOS), nodeArch(runtime.GOARCH)), exe)
}

func nodePlatform(goos string) string {
	if goos == "windows" {
		return "win32"
	}
	return goos
}

func nodeArch(goarch string) string {
	switch goarch {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	}
	return goarch
}

func Presets() []Preset {
	out := make([]Preset, len(presets))
	copy(out, presets)
	return out
}

func presetByID(id string) (Preset, bool) {
	for _, p := range presets {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}

func claudeEnv() map[string]string {
	if runtime.GOOS == "windows" {
		return map[string]string{"CLAUDE_CODE_USE_POWERSHELL_TOOL": "1"}
	}
	return nil
}

func claudeSessionMeta() string {
	if runtime.GOOS == "windows" {
		return `{"claudeCode":{"options":{"disallowedTools":["Bash"],"allowDangerouslySkipPermissions":false}}}`
	}
	return `{"claudeCode":{"options":{"allowDangerouslySkipPermissions":false}}}`
}

func (p Preset) isUnofficialMethod(id string) bool {
	for _, u := range p.unofficial {
		if u == id {
			return true
		}
	}
	return false
}
