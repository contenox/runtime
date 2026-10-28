package agentregistryapi

import (
	_ "embed"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	apiframework "github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/runtime/agentregistryservice"
	"github.com/contenox/runtime/runtime/chainagents"
)

type Preset struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Chain       string `json:"-"`
}

type PresetStatus struct {
	Preset
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
}

//go:embed presets/planner.json
var plannerChain string

var Presets = []Preset{
	{
		Name:        "planner",
		Title:       "Planner",
		Description: "Holds and evolves a mission's living plan. Plans only: its envelope grants mission plan/report/finish tools and withholds execution tools.",
		Chain:       plannerChain,
	},
	{
		Name:        "reviewer",
		Title:       "Reviewer",
		Description: "Reads a change and reports defects, risks and missing tests. Read-only: it inspects files and never edits them.",
		Chain: `{
  "id": "reviewer",
  "description": "Read-only reviewer: inspect a change and report defects, risks and missing tests.",
  "tasks": [
    {
      "id": "review",
      "description": "Review the work described in the mission intent.",
      "handler": "chat_completion",
      "system_instruction": "You are a REVIEWER. You read code and change descriptions and report what is wrong with them; you never edit files.\n\nWork only from what you can read: open the relevant files, follow imports and tests, and state where each finding lives (path and line). For every finding give severity (blocker, major, minor), what breaks, and the evidence. Explicitly look for: incorrect error handling, boundary and nil cases, concurrency hazards, resource leaks, security-relevant input handling, and tests that would not catch the change.\n\nDo not speculate about code you did not read. Do not apply fixes. End with a verdict: ship, ship with follow-ups, or block.",
      "execute_config": {
        "model": "{{var:alt_model|var:model}}",
        "provider": "{{var:alt_provider|var:provider}}",
        "shift": true,
        "pass_clients_tools": false,
        "tools": [],
        "max_tokens": "{{var:max_tokens|32768}}",
        "retry_policy": {
          "max_attempts": 3,
          "initial_backoff": "1s",
          "max_backoff": "10s",
          "jitter": 0.25
        }
      },
      "transition": {
        "on_failure": "",
        "branches": [
          {
            "operator": "default",
            "when": "",
            "goto": "end"
          }
        ]
      }
    }
  ],
  "token_limit": 131072
}`,
	},
	{
		Name:        "researcher",
		Title:       "Researcher",
		Description: "Answers a question from documents and the web, with sources for every claim, and separates what it verified from what it inferred.",
		Chain: `{
  "id": "researcher",
  "description": "Read-only researcher: gather evidence from files and the web and answer with sources.",
  "tasks": [
    {
      "id": "research",
      "description": "Research the question in the mission intent.",
      "handler": "chat_completion",
      "system_instruction": "You are a RESEARCHER. You answer a question from evidence, not from memory.\n\n1. Restate the question in one line and list the sub-questions that must be answered.\n2. Gather evidence: read the workspace files that bear on it, and use web_get for current external facts. Prefer primary sources (specifications, source code, official docs) over summaries.\n3. For every claim, give its source: a file path with line, or a URL. If two sources disagree, say so and explain which you trust and why.\n4. Separate VERIFIED (you read it) from INFERRED (you reasoned it) from UNKNOWN (you could not check). Never present an inference as a verified fact.\n\nEnd with a short answer, then the evidence list. You do not edit files.",
      "execute_config": {
        "model": "{{var:alt_model|var:model}}",
        "provider": "{{var:alt_provider|var:provider}}",
        "shift": true,
        "pass_clients_tools": false,
        "tools": [],
        "max_tokens": "{{var:max_tokens|32768}}",
        "retry_policy": {
          "max_attempts": 3,
          "initial_backoff": "1s",
          "max_backoff": "10s",
          "jitter": 0.25
        }
      },
      "transition": {
        "on_failure": "",
        "branches": [
          {
            "operator": "default",
            "when": "",
            "goto": "end"
          }
        ]
      }
    }
  ],
  "token_limit": 131072
}`,
	},
	{
		Name:        "triage",
		Title:       "Triage",
		Description: "Takes a failing run or report, narrows it to a cause with commands it actually ran, and says what to fix next.",
		Chain: `{
  "id": "triage",
  "description": "Triage a failure: reproduce, narrow to a cause, report evidence and the next fix.",
  "tasks": [
    {
      "id": "triage",
      "description": "Triage the failure described in the mission intent.",
      "handler": "chat_completion",
      "system_instruction": "You are a TRIAGE engineer. A run failed or a report is confusing; your job is to narrow it to a cause, not to guess.\n\n1. Reproduce: run the smallest command that shows the failure. If you cannot reproduce it, say what you tried and what that rules out.\n2. Narrow: use reads, grep and focused commands to find the first wrong value or state, and name the file and line that produces it.\n3. Record the exact commands you ran and their relevant output. A hypothesis you did not test is labelled HYPOTHESIS.\n4. Stop at the cause. Do not fix it unless the intent asks you to; propose the smallest correct fix and the test that would have caught it.\n\nIf the failure is environmental (missing tool, credential, network), say so plainly and stop.",
      "execute_config": {
        "model": "{{var:alt_model|var:model}}",
        "provider": "{{var:alt_provider|var:provider}}",
        "shift": true,
        "pass_clients_tools": false,
        "tools": [],
        "max_tokens": "{{var:max_tokens|32768}}",
        "retry_policy": {
          "max_attempts": 3,
          "initial_backoff": "1s",
          "max_backoff": "10s",
          "jitter": 0.25
        }
      },
      "transition": {
        "on_failure": "",
        "branches": [
          {
            "operator": "default",
            "when": "",
            "goto": "end"
          }
        ]
      }
    }
  ],
  "token_limit": 131072
}`,
	},
}

func AddAgentPresetRoutes(mux *http.ServeMux, agents agentregistryservice.Service, contenoxDir string) {
	if mux == nil || agents == nil {
		return
	}
	h := &presetHandler{agents: agents, dir: strings.TrimSpace(contenoxDir)}
	mux.HandleFunc("GET /agent-presets", h.list)
	mux.HandleFunc("POST /agent-presets/{name}", h.install)
}

type presetHandler struct {
	agents agentregistryservice.Service
	dir    string
}

func (h *presetHandler) list(w http.ResponseWriter, r *http.Request) {
	statuses := make([]PresetStatus, 0, len(Presets))
	for _, preset := range Presets {
		status := PresetStatus{Preset: preset}
		if h.dir != "" {
			status.Path = filepath.Join(h.dir, "agent-"+preset.Name+".json")
			status.Installed = fileExists(status.Path)
		}
		statuses = append(statuses, status)
	}
	_ = apiframework.Encode(w, r, http.StatusOK, statuses)
}

func (h *presetHandler) install(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(apiframework.GetPathParam(r, "name", "Preset name to install."))
	var preset *Preset
	for i := range Presets {
		if Presets[i].Name == name {
			preset = &Presets[i]
			break
		}
	}
	if preset == nil {
		_ = apiframework.Error(w, r, fmt.Errorf("unknown preset %q", name), apiframework.CreateOperation)
		return
	}
	if h.dir == "" {
		_ = apiframework.Error(w, r, fmt.Errorf("no chain directory is configured for this server"), apiframework.CreateOperation)
		return
	}
	if err := os.MkdirAll(h.dir, 0o750); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	path := filepath.Join(h.dir, "agent-"+preset.Name+".json")
	force := strings.EqualFold(r.URL.Query().Get("force"), "true")
	if !force && fileExists(path) {
		_ = apiframework.Encode(w, r, http.StatusOK, map[string]any{
			"preset": preset.Name, "path": path, "installed": false, "reason": "already installed; pass force=true to overwrite",
		})
		return
	}
	if err := os.WriteFile(path, []byte(preset.Chain), 0o644); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	result, err := chainagents.Discover(r.Context(), h.agents, h.dir)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	agent := map[string]any(nil)
	if registered, err := h.agents.GetByName(r.Context(), preset.Name); err == nil && registered != nil {
		agent = map[string]any{"id": registered.ID, "name": registered.Name, "kind": registered.Kind, "enabled": registered.Enabled}
	}
	_ = apiframework.Encode(w, r, http.StatusCreated, map[string]any{
		"preset": preset.Name, "path": path, "installed": true, "agent": agent,
		"created": result.Created, "updated": result.Updated,
	})
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
