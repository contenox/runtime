package missiontools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/contenox/runtime/runtime/taskengine"
)

const ToolNameStartMission = "mission_start"

type SubagentSpec struct {
	AgentName       string
	Intent          string
	HITLPolicyName  string
	ParentSessionID string
}

type SubagentHandle struct {
	MissionID string
}

type Spawner interface {
	Spawn(ctx context.Context, spec SubagentSpec) (SubagentHandle, error)
}

type SubagentWatcher interface {
	Get(ctx context.Context, id string) (*missionservice.Mission, error)
	ListReports(ctx context.Context, missionID string, limit int) ([]*missionservice.Report, error)
}

type SubagentDefaults func(ctx context.Context) (agentName, hitlPolicyName string)

const (
	defaultSubagentTimeout = 15 * time.Minute
	subagentPollInterval   = 2 * time.Second
	startedReportLimit     = 20
)

var errSubagentWaitTimeout = errors.New("subagent wait timed out")

func WithSpawner(spawner Spawner, watcher SubagentWatcher, defaults SubagentDefaults) Option {
	return func(p *provider) {
		p.spawner = spawner
		p.watcher = watcher
		p.subagentDefaults = defaults
	}
}

func WithSubagentTimeout(d time.Duration) Option {
	return func(p *provider) {
		if d > 0 {
			p.subagentTimeout = d
		}
	}
}

func (p *provider) canSpawn() bool {
	return p.spawner != nil && p.watcher != nil
}

func startMissionToolSchema() taskengine.Tool {
	return taskengine.Tool{
		Type: "function",
		Function: taskengine.FunctionTool{
			Name: ToolNameStartMission,
			Description: "Run one unit of work as a SUBAGENT and wait for it to finish. " +
				"The subagent is a fresh agent with its own context: it does not see this conversation, so `intent` must be a complete, self-contained instruction - what to do, where, and what counts as done. " +
				"Use it to execute a step of your plan, or any piece of work worth isolating. " +
				"It returns the subagent's status and reports, or a pending question for you to answer. " +
				"This waits until the subagent finishes or needs attention; read its result before starting the next step.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"intent": map[string]any{
						"type":        "string",
						"description": "The complete, self-contained instruction for the subagent. It has none of your context - state the goal, the paths, and what a finished result looks like.",
					},
					"agent": map[string]any{
						"type":        "string",
						"description": "Which declared agent to run it as. Omit to use the configured default subagent.",
					},
					"policy": map[string]any{
						"type":        "string",
						"description": "The envelope (HITL policy) bounding it. Omit to use the configured default.",
					},
				},
				"required": []string{"intent"},
			},
		},
	}
}

func (p *provider) execStartMission(ctx context.Context, parentSessionID string, input any, call *taskengine.ToolsCall) (any, taskengine.DataType, error) {
	if !p.canSpawn() {
		return nil, taskengine.DataTypeAny, fmt.Errorf("missiontools: starting a subagent is not wired in this process")
	}
	intent := strings.TrimSpace(argString(input, call, "intent"))
	if intent == "" {
		return nil, taskengine.DataTypeAny, fmt.Errorf("missiontools: %s requires an 'intent' - the self-contained instruction the subagent runs", ToolNameStartMission)
	}
	intent = strings.Join(strings.Fields(intent), " ")

	agentName := strings.TrimSpace(argString(input, call, "agent"))
	policyName := strings.TrimSpace(argString(input, call, "policy"))
	if (agentName == "" || policyName == "") && p.subagentDefaults != nil {
		defaultAgent, defaultPolicy := p.subagentDefaults(ctx)
		if agentName == "" {
			agentName = strings.TrimSpace(defaultAgent)
		}
		if policyName == "" {
			policyName = strings.TrimSpace(defaultPolicy)
		}
	}
	if agentName == "" {
		return nil, taskengine.DataTypeAny, fmt.Errorf("missiontools: %s needs an agent: name one, or set a default with `contenox-runtime config set default-mission-agent <name>`", ToolNameStartMission)
	}
	if policyName == "" {
		return nil, taskengine.DataTypeAny, fmt.Errorf("missiontools: %s needs an envelope: name one, or set a default with `contenox-runtime config set default-mission-policy <name>`", ToolNameStartMission)
	}
	if err := ctx.Err(); err != nil {
		return nil, taskengine.DataTypeAny, fmt.Errorf("missiontools: start subagent: %w", err)
	}

	handle, err := p.spawner.Spawn(ctx, SubagentSpec{
		AgentName:       agentName,
		Intent:          intent,
		HITLPolicyName:  policyName,
		ParentSessionID: parentSessionID,
	})
	if err != nil {
		return nil, taskengine.DataTypeAny, fmt.Errorf("missiontools: start subagent: %w", err)
	}

	m, waitErr := p.waitForRest(ctx, handle.MissionID)
	out := map[string]any{
		"missionId": handle.MissionID,
		"agent":     agentName,
		"intent":    intent,
	}
	if m != nil {
		out["status"] = string(m.Status)
		if m.StatusReason != "" {
			out["statusReason"] = m.StatusReason
		}
	}
	if waitErr != nil {
		if !errors.Is(waitErr, errSubagentWaitTimeout) {
			return nil, taskengine.DataTypeAny, fmt.Errorf("missiontools: wait for subagent %s: %w", handle.MissionID, waitErr)
		}
		out["status"] = "running"
		out["note"] = fmt.Sprintf("still running after %s; it keeps its record - read it later with %s", p.timeout(), ToolNameListMissions)
	}
	reports, err := p.reportSummaries(ctx, handle.MissionID)
	if err != nil {
		return nil, taskengine.DataTypeAny, fmt.Errorf("missiontools: read reports for subagent %s: %w", handle.MissionID, err)
	}
	out["reports"] = reports
	if p.resolver != nil {
		asks, err := p.resolver.PendingAsks(ctx, handle.MissionID)
		if err != nil {
			return nil, taskengine.DataTypeAny, fmt.Errorf("missiontools: read attention for subagent %s: %w", handle.MissionID, err)
		}
		if len(asks) > 0 {
			out["waitingOnYou"] = asks
		}
	}

	raw, err := json.Marshal(out)
	if err != nil {
		return nil, taskengine.DataTypeAny, fmt.Errorf("missiontools: encode subagent outcome: %w", err)
	}
	return string(raw), taskengine.DataTypeString, nil
}

func (p *provider) timeout() time.Duration {
	if p.subagentTimeout > 0 {
		return p.subagentTimeout
	}
	return defaultSubagentTimeout
}

func (p *provider) waitForRest(ctx context.Context, missionID string) (*missionservice.Mission, error) {
	timer := time.NewTimer(p.timeout())
	defer timer.Stop()
	ticker := time.NewTicker(subagentPollInterval)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		m, err := p.watcher.Get(ctx, missionID)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if err != nil {
			return nil, fmt.Errorf("read mission: %w", err)
		}
		if m == nil {
			return nil, fmt.Errorf("read mission: watcher returned no mission")
		}
		if missionservice.IsTerminalStatus(m.Status) {
			return m, nil
		}
		if p.resolver != nil {
			asks, err := p.resolver.PendingAsks(ctx, missionID)
			if err != nil {
				return nil, fmt.Errorf("read mission attention: %w", err)
			}
			if len(asks) > 0 {
				return m, nil
			}
		}
		select {
		case <-ctx.Done():
			return m, ctx.Err()
		case <-timer.C:
			if err := ctx.Err(); err != nil {
				return m, err
			}
			return m, fmt.Errorf("%w: subagent %s did not finish within %s", errSubagentWaitTimeout, missionID, p.timeout())
		case <-ticker.C:
		}
	}
}

func (p *provider) reportSummaries(ctx context.Context, missionID string) ([]map[string]any, error) {
	reports, err := p.watcher.ListReports(ctx, missionID, startedReportLimit)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(reports))
	for i := len(reports) - 1; i >= 0; i-- {
		r := reports[i]
		entry := map[string]any{"kind": string(r.Kind), "summary": r.Summary}
		if r.Detail != "" {
			entry["detail"] = r.Detail
		}
		if len(r.Refs) > 0 {
			entry["refs"] = r.Refs
		}
		if r.Handover != nil {
			entry["handover"] = r.Handover
		}
		out = append(out, entry)
	}
	return out, nil
}
