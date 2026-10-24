package missiontools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/contenox/runtime/runtime/taskengine"
)

const (
	ToolNameListMissions = "mission_list"
	ToolNameAnswer       = "mission_answer"
)

type SupervisorStore interface {
	MissionsFiredBy(ctx context.Context, parentSessionID string, limit int) ([]*missionservice.Mission, error)
	ListReports(ctx context.Context, missionID string, limit int) ([]*missionservice.Report, error)
}

type PendingAsk struct {
	AskID     string `json:"askId"`
	MissionID string `json:"missionId"`
	Question  string `json:"question"`
	Detail    string `json:"detail,omitempty"`
	AskedAt   string `json:"askedAt,omitempty"`
}

type AttentionResolver interface {
	PendingAsks(ctx context.Context, missionID string) ([]PendingAsk, error)
	AnswerAsAgent(ctx context.Context, askID, text string) error
}

func WithParentSessionID(ctx context.Context, contenoxSessionID string) context.Context {
	if strings.TrimSpace(contenoxSessionID) == "" {
		return ctx
	}
	return context.WithValue(ctx, parentSessionCtxKey{}, contenoxSessionID)
}

type parentSessionCtxKey struct{}

func ParentSessionIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(parentSessionCtxKey{}).(string)
	return id
}

func supervisorTools() []taskengine.Tool {
	return []taskengine.Tool{listMissionsToolSchema(), answerToolSchema()}
}

func listMissionsToolSchema() taskengine.Tool {
	return taskengine.Tool{
		Type: "function",
		Function: taskengine.FunctionTool{
			Name: ToolNameListMissions,
			Description: "List the missions YOU fired from this session — their intent, status, last heartbeat, latest reports, and any QUESTION a unit is currently waiting on you to answer. " +
				"Call it when you need to know what your subagents are doing, before answering the user about them, or to get the `askId` you need for " + ToolNameAnswer + ".",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

func answerToolSchema() taskengine.Tool {
	return taskengine.Tool{
		Type: "function",
		Function: taskengine.FunctionTool{
			Name: ToolNameAnswer,
			Description: "Answer a QUESTION one of your own mission units is blocked on. The unit is parked waiting for this: your text becomes the result of the tool call it asked with, and it continues immediately. " +
				"Get `askId` from " + ToolNameListMissions + " (or from the question you were just shown). Answer only what you actually know — if the answer needs the user, ask THEM first and relay it; a confident wrong answer sends the unit down the wrong path.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"askId": map[string]any{
						"type":        "string",
						"description": "The id of the question to answer.",
					},
					"answer": map[string]any{
						"type":        "string",
						"description": "The answer, in your own words — what the unit needs to proceed.",
					},
				},
				"required": []string{"askId", "answer"},
			},
		},
	}
}

func (p *provider) execListMissions(ctx context.Context, parentSessionID string) (any, taskengine.DataType, error) {
	if p.supervisor == nil {
		return nil, taskengine.DataTypeAny, fmt.Errorf("missiontools: mission supervision is not wired in this process")
	}
	missions, err := p.supervisor.MissionsFiredBy(ctx, parentSessionID, supervisorMissionLimit)
	if err != nil {
		return nil, taskengine.DataTypeAny, fmt.Errorf("missiontools: list fired missions: %w", err)
	}
	out := make([]map[string]any, 0, len(missions))
	for _, m := range missions {
		entry := map[string]any{
			"missionId": m.ID,
			"agentName": m.AgentName,
			"intent":    m.Intent,
			"status":    string(m.Status),
		}
		if m.StatusReason != "" {
			entry["statusReason"] = m.StatusReason
		}
		if m.LastHeartbeat != nil {
			entry["lastHeartbeat"] = m.LastHeartbeat.UTC().Format(time.RFC3339)
		}
		if reports, err := p.supervisor.ListReports(ctx, m.ID, supervisorReportLimit); err == nil && len(reports) > 0 {
			rs := make([]map[string]any, 0, len(reports))
			for _, r := range reports {
				rs = append(rs, map[string]any{"kind": string(r.Kind), "summary": r.Summary})
			}
			entry["reports"] = rs
		}
		if p.resolver != nil {
			if asks, err := p.resolver.PendingAsks(ctx, m.ID); err == nil && len(asks) > 0 {
				entry["waitingOnYou"] = asks
			}
		}
		out = append(out, entry)
	}
	raw, err := json.Marshal(map[string]any{"missions": out})
	if err != nil {
		return nil, taskengine.DataTypeAny, fmt.Errorf("missiontools: encode fired missions: %w", err)
	}
	return string(raw), taskengine.DataTypeString, nil
}

const (
	supervisorMissionLimit = 20
	supervisorReportLimit  = 5
)

func (p *provider) execAnswer(ctx context.Context, parentSessionID string, input any, call *taskengine.ToolsCall) (any, taskengine.DataType, error) {
	if p.resolver == nil || p.supervisor == nil {
		return nil, taskengine.DataTypeAny, fmt.Errorf("missiontools: mission supervision is not wired in this process")
	}
	askID := strings.TrimSpace(argString(input, call, "askId"))
	answer := strings.TrimSpace(argString(input, call, "answer"))
	if askID == "" || answer == "" {
		return nil, taskengine.DataTypeAny, fmt.Errorf("missiontools: %s requires askId and answer", ToolNameAnswer)
	}

	missions, err := p.supervisor.MissionsFiredBy(ctx, parentSessionID, supervisorMissionLimit)
	if err != nil {
		return nil, taskengine.DataTypeAny, fmt.Errorf("missiontools: resolve your missions: %w", err)
	}
	for _, m := range missions {
		asks, err := p.resolver.PendingAsks(ctx, m.ID)
		if err != nil {
			continue
		}
		for _, ask := range asks {
			if ask.AskID != askID {
				continue
			}
			if err := p.resolver.AnswerAsAgent(ctx, askID, answer); err != nil {
				return nil, taskengine.DataTypeAny, fmt.Errorf("missiontools: answer %s: %w", askID, err)
			}
			return fmt.Sprintf("answered %s — unit %q has your reply and continues", askID, m.AgentName), taskengine.DataTypeString, nil
		}
	}
	return nil, taskengine.DataTypeAny, fmt.Errorf("missiontools: %q is not a question your missions are currently waiting on (already answered, expired, or not yours)", askID)
}
