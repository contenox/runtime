package acpsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	libacp "github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/chatservice"
	"github.com/contenox/runtime/runtime/fleetservice"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
)

var ErrSessionNotLive = errors.New("acpsvc: no live session for that contenox id on this connection")

type MissionDispatcher interface {
	Dispatch(ctx context.Context, req fleetservice.DispatchRequest) (fleetservice.DispatchResult, error)
}

type MissionAgentResolver interface {
	GetByName(ctx context.Context, name string) (*runtimetypes.Agent, error)
}

func (t *Transport) hasMissionCapability() bool {
	return t.deps.Fleet != nil && t.deps.Agents != nil
}

func (t *Transport) handleMission(ctx context.Context, sess *sessionEntry, args string) (string, error) {
	if f := t.deps.MissionForwarded; f != nil {
		if f.Reachable != nil && !f.Reachable() {
			target := "the configured serve"
			if f.TargetURL != nil {
				if u := strings.TrimSpace(f.TargetURL()); u != "" {
					target = "the serve at " + u
				}
			}
			return "", fmt.Errorf("mission dispatch is unavailable: %s stopped answering — restart it or run `contenox serve`, then try /mission again", target)
		}
	} else if !t.hasMissionCapability() {
		return "", fmt.Errorf("mission dispatch is unavailable in this session: /mission needs a configured model and the in-process fleet. Configure a model with `contenox config set default-model …` and fire /mission from your editor session, or set CONTENOX_SERVER_URL to fire onto a running serve.")
	}
	args = strings.TrimSpace(args)
	if args == "" {
		return "", fmt.Errorf("usage: /mission <intent>   or   /mission <agent-name> <intent>")
	}

	store := runtimetypes.New(t.deps.DB.WithoutTransaction())

	agentName, intent, named := t.resolveMissionAgentAndIntent(ctx, store, args)
	if strings.TrimSpace(agentName) == "" {
		return "", fmt.Errorf("no mission agent: name one as `/mission <agent-name> <intent>`, or set a default under Settings → Missions (or `contenox config set default-mission-agent <name>`)")
	}
	policy := strings.TrimSpace(clikv.Read(ctx, store, "default-mission-policy"))
	if policy == "" {
		return "", fmt.Errorf("no mission envelope: set one under Settings → Missions (or `contenox config set default-mission-policy <policy>`) — a mission must name the HITL policy that bounds it")
	}

	res, err := t.deps.Fleet.Dispatch(ctx, fleetservice.DispatchRequest{
		AgentName:       agentName,
		Intent:          intent,
		HITLPolicyName:  policy,
		ParentSessionID: sess.InternalSessionID,
	})
	if err != nil {
		return "", err
	}

	sess.mu.Lock()
	sess.FiredMissions = true
	sid, hasSID := t.acpSessionForContenoxID(sess.InternalSessionID)
	sess.mu.Unlock()
	if hasSID {
		t.persistSessionFiredMission(ctx, store, sid)
	}

	agentRole := "default mission agent"
	if named {
		agentRole = "named agent"
	}
	tail := "Reports arrive live in this session as the mission runs; if this session has ended when one lands, it waits in the operator inbox."
	if t.deps.MissionForwarded != nil {
		tail = "Reports land in the operator inbox on that serve (read them with `contenox approvals`, or `contenox mission show`); live delivery back into this editor session is a named follow-up."
	}
	return fmt.Sprintf(
		"Mission fired at %s %q under envelope %q.\nIntent: %s\nMission %s (instance %s, session %s). %s",
		agentRole, agentName, policy, intent, res.MissionID, res.InstanceID, res.SessionID, tail,
	), nil
}

func (t *Transport) resolveMissionAgentAndIntent(ctx context.Context, store runtimetypes.Store, args string) (agentName, intent string, named bool) {
	first, rest := splitFirstToken(args)
	if rest != "" && t.deps.Agents != nil {
		if a, err := t.deps.Agents.GetByName(ctx, first); err == nil && a != nil {
			return a.Name, rest, true
		}
	}
	return strings.TrimSpace(clikv.Read(ctx, store, "default-mission-agent")), args, false
}

func (t *Transport) DeliverToContenoxSession(ctx context.Context, contenoxSessionID string, n libacp.SessionNotification) error {
	sid, ok := t.acpSessionForContenoxID(contenoxSessionID)
	if !ok {
		return ErrSessionNotLive
	}
	t.persistDeliveredReport(ctx, contenoxSessionID, n)
	n.SessionID = sid
	t.sendUpdate(ctx, n)
	return nil
}

func (t *Transport) persistDeliveredReport(ctx context.Context, contenoxSessionID string, n libacp.SessionNotification) {
	if t.deps.DB == nil || contenoxSessionID == "" {
		return
	}
	text := strings.TrimSpace(n.Update.Content.Text)
	if text == "" {
		return
	}
	msgs := []taskengine.Message{{
		ID:        uuid.NewString(),
		Role:      "assistant",
		Content:   text,
		Timestamp: time.Now().UTC(),
	}}
	cleanCtx := context.WithoutCancel(ctx)
	mgr := chatservice.NewManager(t.workspaceID())
	if err := mgr.PersistDiff(cleanCtx, t.deps.DB.WithoutTransaction(), contenoxSessionID, msgs); err != nil {
		reportErr, _, end := t.tracker().Start(cleanCtx, "persist", "acp_mission_report", "session_id", contenoxSessionID)
		reportErr(err)
		end()
	}
}

func splitFirstToken(args string) (first, rest string) {
	args = strings.TrimSpace(args)
	if i := strings.IndexFunc(args, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' }); i >= 0 {
		return args[:i], strings.TrimSpace(args[i+1:])
	}
	return args, ""
}

const acpSessionFiredKVPrefix = "acp:session_fired_missions:"

type sessionFiredRecord struct {
	Fired bool `json:"fired"`
}

func (t *Transport) persistSessionFiredMission(ctx context.Context, store runtimetypes.Store, sid libacp.SessionID) {
	raw, err := json.Marshal(sessionFiredRecord{Fired: true})
	if err != nil {
		return
	}
	if err := store.SetKV(ctx, acpSessionFiredKVPrefix+string(sid), raw); err != nil {
		reportErr, _, end := t.tracker().Start(ctx, "persist_fired_mission", "acp_session", "session_id", string(sid))
		reportErr(err)
		end()
	}
}

func (t *Transport) readSessionFiredMission(ctx context.Context, store runtimetypes.Store, sid libacp.SessionID) bool {
	var rec sessionFiredRecord
	if err := store.GetKV(ctx, acpSessionFiredKVPrefix+string(sid), &rec); err != nil {
		return false
	}
	return rec.Fired
}

const acpSessionMissionKVPrefix = "acp:session_mission:"

type sessionMissionRecord struct {
	MissionID string `json:"missionId"`
}

func (t *Transport) persistSessionMission(ctx context.Context, store runtimetypes.Store, sid libacp.SessionID, missionID string) {
	if strings.TrimSpace(missionID) == "" {
		return
	}
	raw, err := json.Marshal(sessionMissionRecord{MissionID: missionID})
	if err != nil {
		return
	}
	if err := store.SetKV(ctx, acpSessionMissionKVPrefix+string(sid), raw); err != nil {
		reportErr, _, end := t.tracker().Start(ctx, "persist_mission", "acp_session", "session_id", string(sid))
		reportErr(err)
		end()
	}
}

func (t *Transport) readSessionMission(ctx context.Context, store runtimetypes.Store, sid libacp.SessionID) string {
	var rec sessionMissionRecord
	if err := store.GetKV(ctx, acpSessionMissionKVPrefix+string(sid), &rec); err != nil {
		return ""
	}
	return rec.MissionID
}

func sessionListMeta(agentName, missionID string) json.RawMessage {
	meta := map[string]any{}
	if agentName != "" {
		meta[AgentMetaKey] = agentName
	}
	if missionID != "" {
		meta[missionservice.MissionMetaKey] = missionservice.MissionMeta{MissionID: missionID}
	}
	if len(meta) == 0 {
		return nil
	}
	return mustJSON(meta)
}
