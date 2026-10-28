package acpsvc

import (
	"context"
	"errors"
	"time"

	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/agentservice"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/missiontools"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
)

func (t *Transport) Prompt(ctx context.Context, req libacp.PromptRequest) (libacp.PromptResponse, error) {
	sess, ok := t.sessionFor(req.SessionID)
	if !ok {
		reportErr, _, end := t.tracker().Start(ctx, "prompt", "acp_session", "session_id", string(req.SessionID), "prompt_blocks", len(req.Prompt))
		defer end()
		err := libacp.NewErrorf(libacp.ErrInvalidParams, "unknown session %q", req.SessionID)
		reportErr(err)
		return libacp.PromptResponse{}, err
	}
	return sess.driver.Prompt(ctx, req, sess)
}

type nativeDriver struct {
	t     *Transport
	agent agentservice.Agent
}

func (d *nativeDriver) AgentName() string { return "" }

func (d *nativeDriver) Close() error { return nil }

func (d *nativeDriver) AvailableCommands() []libacp.AvailableCommand { return d.t.acpCommands() }

func (d *nativeDriver) ConfigOptions(ctx context.Context, sess *sessionEntry) []libacp.SessionConfigOption {
	t := d.t
	opts := []libacp.SessionConfigOption{
		t.modelConfigOption(ctx, sess),
		t.hitlPolicyConfigOption(sess),
		t.thinkConfigOption(sess),
		t.tokenLimitConfigOption(ctx, sess),
	}
	if opt, ok := t.workspaceRootConfigOption(sess); ok {
		opts = append(opts, opt)
	}
	return opts
}

func (d *nativeDriver) SetConfigOption(ctx context.Context, sess *sessionEntry, configID string, value libacp.SessionConfigOptionValue) error {
	return d.t.setSessionConfigOption(ctx, sess, configID, value.AsString())
}

func (d *nativeDriver) Prompt(ctx context.Context, req libacp.PromptRequest, sess *sessionEntry) (libacp.PromptResponse, error) {
	t := d.t
	reportErr, reportChange, end := t.tracker().Start(ctx, "prompt", "acp_session", "session_id", string(req.SessionID), "prompt_blocks", len(req.Prompt))
	defer end()

	if t.deps.ChainRegistry == nil || t.deps.ChainRegistry.Default() == nil {
		err := libacp.InternalError("no chain configured")
		reportErr(err)
		return libacp.PromptResponse{}, err
	}

	input, droppedContentKinds := libacp.FlattenContent(req.Prompt)
	if input == "" {
		err := libacp.NewError(libacp.ErrInvalidParams, "empty prompt")
		reportErr(err)
		return libacp.PromptResponse{}, err
	}

	if goal, ok := parsePlanCommand(input); ok {
		if !t.hasMissionCapability() {
			err := planPreambleForMissingFleet()
			reportErr(err)
			return libacp.PromptResponse{}, err
		}
		if goal == "" {
			reportChange(string(req.SessionID), map[string]any{"stop_reason": string(libacp.StopReasonEndTurn), "command": planCommandName})
			t.sendUpdate(ctx, libacp.SessionNotification{SessionID: req.SessionID, Update: libacp.NewAgentMessageChunk(planUsageLine)})
			t.persistCommandTurn(ctx, sess, planCommandName, "", planUsageLine)
			return libacp.PromptResponse{StopReason: libacp.StopReasonEndTurn}, nil
		}
		input = planPreamble(goal)
	}

	if name, args, ok := parseCommand(input); ok {
		cmdCtx := libtracker.WithNewRequestID(ctx)
		return t.dispatchCommand(cmdCtx, req.SessionID, sess, name, args)
	}

	if t.deps.NativeTurns != nil {
		return d.promptViaRegistry(ctx, req, sess, input, droppedContentKinds)
	}

	promptCtx := libtracker.WithNewRequestID(ctx)
	reqID, _ := promptCtx.Value(libtracker.ContextKeyRequestID).(string)

	promptCtx, cancelPrompt := context.WithCancel(promptCtx)
	promptReg := t.registerPromptCancel(req.SessionID, cancelPrompt)
	defer func() {
		t.unregisterPromptCancel(req.SessionID, promptReg)
		cancelPrompt()
	}()

	if policyName := t.resolveSessionHITLPolicy(sess); policyName != "" {
		promptCtx = hitlservice.WithPolicyName(promptCtx, policyName)
	}

	if sess.MissionID != "" {
		promptCtx = missiontools.WithMissionID(promptCtx, sess.MissionID)
	}
	if sess.FiredMissions && sess.InternalSessionID != "" {
		promptCtx = missiontools.WithParentSessionID(promptCtx, sess.InternalSessionID)
	}

	rawCh := make(chan []byte, 64)
	bus := t.deps.Engine.Bus
	if bus != nil && reqID != "" {
		sub, err := bus.Stream(promptCtx, taskengine.TaskEventRequestSubject(reqID), rawCh)
		if err != nil {
			subErr, _, subEnd := t.tracker().Start(promptCtx, "subscribe", "acp_event_stream", "session_id", string(req.SessionID), "request_id", reqID)
			subErr(err)
			subEnd()
		} else {
			translateDone := make(chan struct{})
			go func() {
				defer close(translateDone)
				t.translateEvents(promptCtx, req.SessionID, rawCh)
			}()
			defer func() {
				_ = sub.Unsubscribe()
				close(rawCh)
				<-translateDone
			}()
		}
	}

	templateVars := t.chainTemplateVars(sess)
	templateVars["think"] = sess.think()
	var toolsAllowlist []string
	if t.deps.DB != nil {
		var err error
		toolsAllowlist, err = t.runtimeToolsAllowlist(promptCtx, runtimetypes.New(t.deps.DB.WithoutTransaction()), sess.McpServerNames)
		if err != nil {
			reportErr(err)
			return libacp.PromptResponse{}, libacp.InternalError(err.Error())
		}
	}

	contextLen := sess.effectiveTokenLimit()
	if contextLen == 0 {
		currentModel := sess.modelOrDefault(t.model())
		for _, state := range t.runtimeStates(promptCtx) {
			for _, pulled := range state.PulledModels {
				if pulled.Model == currentModel && pulled.ContextLength > 0 {
					contextLen = pulled.ContextLength
					break
				}
			}
			if contextLen > 0 {
				break
			}
		}
		if contextLen == 0 {
			for _, state := range t.runtimeStates(promptCtx) {
				for _, pulled := range state.PulledModels {
					if pulled.ContextLength > 0 && (pulled.CanChat || pulled.CanPrompt) {
						contextLen = pulled.ContextLength
						break
					}
				}
				if contextLen > 0 {
					break
				}
			}
		}
	}

	resp, err := d.agent.Prompt(promptCtx, agentservice.PromptRequest{
		SessionID:      sess.InternalSessionID,
		Input:          input,
		Chain:          t.deps.ChainRegistry.Default(),
		TemplateVars:   templateVars,
		ToolsAllowlist: toolsAllowlist,
		ContextLength:  contextLen,
	})
	if err != nil {
		cancelled := errors.Is(err, context.Canceled) ||
			errors.Is(promptCtx.Err(), context.Canceled) ||
			errors.Is(ctx.Err(), context.Canceled)
		if cancelled {
			reportChange(string(req.SessionID), map[string]any{
				"stop_reason":           string(libacp.StopReasonCancelled),
				"request_id":            reqID,
				"dropped_content_kinds": droppedContentKinds,
			})
			return libacp.PromptResponse{StopReason: libacp.StopReasonCancelled}, nil
		}
		reportErr(err)
		if resp != nil {
			return libacp.PromptResponse{StopReason: mapStopReason(resp.StopReason)}, libacp.InternalError(err.Error())
		}
		return libacp.PromptResponse{}, libacp.InternalError(err.Error())
	}
	stopReason := mapStopReason(resp.StopReason)
	if errors.Is(promptCtx.Err(), context.Canceled) {
		stopReason = libacp.StopReasonCancelled
	}
	libacp.AfterResponse(ctx, func() {
		update := libacp.SessionUpdate{
			SessionUpdate: libacp.SessionUpdateSessionInfo,
			UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
		}
		if title := t.sessionInfoTitle(ctx, sess.InternalSessionID); title != "" {
			update.Title = title
		}
		t.sendUpdate(ctx, libacp.SessionNotification{
			SessionID: req.SessionID,
			Update:    update,
		})
	})
	reportChange(string(req.SessionID), map[string]any{
		"stop_reason":           string(stopReason),
		"request_id":            reqID,
		"dropped_content_kinds": droppedContentKinds,
	})
	return libacp.PromptResponse{StopReason: stopReason}, nil
}

func mapStopReason(r agentservice.StopReason) libacp.StopReason {
	switch r {
	case agentservice.StopEndTurn:
		return libacp.StopReasonEndTurn
	case agentservice.StopMaxTokens:
		return libacp.StopReasonMaxTokens
	case agentservice.StopMaxTurnRequests:
		return libacp.StopReasonMaxTurnRequests
	case agentservice.StopCancelled:
		return libacp.StopReasonCancelled
	}
	return libacp.StopReasonEndTurn
}
