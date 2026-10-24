package contenoxcli

import (
	"context"
	"errors"
	"fmt"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/acpsvc"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/contenox/runtime/runtime/missiontools"
)

var (
	missionToolsProviderName = missiontools.ToolsProviderName
	missionAnswerToolName    = missiontools.ToolNameAnswer
)

type sessionPrompter interface {
	PromptContenoxSession(ctx context.Context, contenoxSessionID, text string) error
}

type agentAnswerOffer struct {
	hitl     hitlservice.Service
	missions missionservice.Service
	prompter sessionPrompter
	tracker  libtracker.ActivityTracker
}

func (a agentAnswerOffer) OfferToSupervisingAgent(ctx context.Context, ev missionservice.AttentionAskedEvent) error {
	if a.prompter == nil || a.hitl == nil || a.missions == nil || ev.ParentSessionID == "" {
		return nil
	}
	reportErr, reportChange, end := a.tracker.Start(ctx, "offer", "mission_agent_answer",
		"mission_id", ev.MissionID, "ask_id", ev.AskID)
	defer end()

	m, err := a.missions.Get(ctx, ev.MissionID)
	if err != nil || m == nil {
		reportChange("declined", "mission_unreadable")
		return nil
	}
	bounds, err := a.hitl.AttentionBoundsFor(ctx, m.HITLPolicyName)
	if err != nil {
		reportChange("declined", "envelope_unreadable")
		return nil
	}
	if !bounds.AllowAgentAnswers {
		reportChange("declined", "envelope_forbids")
		return nil
	}
	used, err := a.hitl.AgentAnswerCount(ctx, ev.MissionID)
	if err != nil {
		reportChange("declined", "cap_uncountable")
		return nil
	}
	if cap := bounds.EffectiveMaxAgentAnswers(); used >= cap {
		reportChange("declined", fmt.Sprintf("cap_reached(%d/%d)", used, cap))
		return nil
	}

	if err := a.prompter.PromptContenoxSession(ctx, ev.ParentSessionID, agentAnswerPrompt(ev)); err != nil {
		switch {
		case errors.Is(err, acpsvc.ErrSessionBusy):
			reportChange("declined", "session_busy")
		case errors.Is(err, acpsvc.ErrSessionNotLive):
			reportChange("declined", "session_not_live")
		default:
			reportErr(err)
		}
		return nil
	}
	reportChange("offered", "prompted_supervising_agent")
	return nil
}

func agentAnswerPrompt(ev missionservice.AttentionAskedEvent) string {
	unit := ev.AgentName
	if unit == "" {
		unit = "a mission unit"
	}
	detail := ""
	if ev.Detail != "" {
		detail = "\n\nIts detail: " + ev.Detail
	}
	return fmt.Sprintf(
		"Your mission unit %q (mission %s, intent %q) is BLOCKED waiting on an answer from you.\n\n"+
			"Its question: %s%s\n\n"+
			"If you know the answer from this conversation, give it by calling %s.%s with askId %q — the unit is parked on that call and continues the moment you answer. "+
			"If you do NOT know, do not guess: say so briefly to the user and let them answer it themselves.",
		unit, ev.MissionID, ev.Intent, ev.Summary, detail,
		missionToolsProviderName, missionAnswerToolName, ev.AskID,
	)
}
