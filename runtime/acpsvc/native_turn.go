package acpsvc

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/agentservice"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/missiontools"
	"github.com/contenox/runtime/runtime/nativeturn"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
)

type nativeTurnViewer struct {
	t   *Transport
	id  string
	sid libacp.SessionID
}

func newNativeTurnViewer(t *Transport, sid libacp.SessionID) *nativeTurnViewer {
	return &nativeTurnViewer{t: t, id: newSessionID("native-view"), sid: sid}
}

func (v *nativeTurnViewer) ID() string { return v.id }

func (v *nativeTurnViewer) Deliver(ctx context.Context, ev nativeturn.Event) error {
	n := ev.Update
	if id := n.Update.ToolCallID; id != "" &&
		(n.Update.SessionUpdate == libacp.SessionUpdateToolCall || n.Update.SessionUpdate == libacp.SessionUpdateToolCallUpdate) &&
		v.t.isPermissionPending(n.SessionID, id) {
		return nil
	}
	v.t.sendUpdate(ctx, n)
	return nil
}

type nativeEventTranslator struct {
	emit              func(ctx context.Context, n libacp.SessionNotification)
	tokenSizeFallback func() int
	seq               map[string]int
	open              map[string]int
}

func newNativeEventTranslator(emit func(ctx context.Context, n libacp.SessionNotification), tokenSizeFallback func() int) *nativeEventTranslator {
	return &nativeEventTranslator{
		emit:              emit,
		tokenSizeFallback: tokenSizeFallback,
		seq:               make(map[string]int),
		open:              make(map[string]int),
	}
}

func (tr *nativeEventTranslator) publish(ctx context.Context, sid libacp.SessionID, payload []byte) {
	var ev taskengine.TaskEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		return
	}
	switch ev.Kind {
	case taskengine.TaskEventStepChunk:
		if !taskengine.IsAssistantProseHandler(ev.TaskHandler) {
			return
		}
		if ev.Content != "" {
			tr.emit(ctx, libacp.SessionNotification{SessionID: sid, Update: libacp.NewAgentMessageChunk(ev.Content)})
		}
		if ev.Thinking != "" {
			tr.emit(ctx, libacp.SessionNotification{SessionID: sid, Update: libacp.NewAgentThoughtChunk(ev.Thinking)})
		}
	case taskengine.TaskEventStepStarted:
		if taskengine.IsToolBearingHandler(ev.TaskHandler) {
			return
		}
		tr.emit(ctx, toolCallNotification(sid, ev, libacp.ToolCallStatusInProgress))
	case taskengine.TaskEventStepCompleted:
		if taskengine.IsToolBearingHandler(ev.TaskHandler) {
			return
		}
		tr.emit(ctx, toolCallNotification(sid, ev, libacp.ToolCallStatusCompleted))
	case taskengine.TaskEventStepFailed:
		if taskengine.IsToolBearingHandler(ev.TaskHandler) {
			return
		}
		tr.emit(ctx, toolCallNotification(sid, ev, libacp.ToolCallStatusFailed))
	case taskengine.TaskEventPrint:
		if ev.Content != "" {
			tr.emit(ctx, libacp.SessionNotification{SessionID: sid, Update: libacp.NewAgentMessageChunk(ev.Content)})
		}
	case taskengine.TaskEventToolCallPending:
		id := resolveToolCallWireID(tr.seq, tr.open, sid, ev, false)
		tr.emit(ctx, toolCallPendingNotification(sid, ev, id))
	case taskengine.TaskEventToolCall:
		id := resolveToolCallWireID(tr.seq, tr.open, sid, ev, true)
		tr.emit(ctx, toolCallUpdateNotification(sid, ev, id))
		if note, ok := planUpdateNotification(sid, ev); ok {
			tr.emit(ctx, note)
		}
	case taskengine.TaskEventTokenUsage:
		used := ev.TokenUsed
		size := ev.TokenSize
		if size <= 0 && tr.tokenSizeFallback != nil {
			if eff := tr.tokenSizeFallback(); eff > 0 {
				size = eff
			}
		}
		if size > 0 || used > 0 {
			tr.emit(ctx, libacp.SessionNotification{
				SessionID: sid,
				Update: libacp.SessionUpdate{
					SessionUpdate: libacp.SessionUpdateUsageUpdate,
					Used:          used,
					Size:          size,
				},
			})
		}
	}
}

func (d *nativeDriver) promptViaRegistry(ctx context.Context, req libacp.PromptRequest, sess *sessionEntry, input string, images []taskengine.ImagePart, audio []taskengine.AudioPart, droppedWhy droppedReasons, droppedContentKinds []string) (libacp.PromptResponse, error) {
	t := d.t
	_ = ctx

	viewer := newNativeTurnViewer(t, req.SessionID)
	turnFn := func(turnCtx context.Context, emit func(context.Context, libacp.SessionNotification)) nativeturn.Result {
		return d.runNativeTurn(turnCtx, req, sess, input, images, audio, droppedWhy, droppedContentKinds, emit)
	}

	turn, _, err := t.deps.NativeTurns.Start(req.SessionID, turnFn, viewer)
	if err != nil {
		return libacp.PromptResponse{StopReason: libacp.StopReasonCancelled}, nil
	}

	select {
	case <-turn.Done():
		turn.Detach(viewer.ID())
		resp, err := nativeResultToResponse(turn.Result())
		return withDroppedContentMeta(resp, droppedContentKinds, droppedWhy), err
	case <-t.connCtx.Done():
		turn.Detach(viewer.ID())
		return libacp.PromptResponse{StopReason: libacp.StopReasonCancelled}, nil
	}
}

func nativeResultToResponse(res nativeturn.Result) (libacp.PromptResponse, error) {
	if res.Err != nil {
		return libacp.PromptResponse{StopReason: res.StopReason}, libacp.InternalError(res.Err.Error())
	}
	return libacp.PromptResponse{StopReason: res.StopReason}, nil
}

func (d *nativeDriver) runNativeTurn(turnCtx context.Context, req libacp.PromptRequest, sess *sessionEntry, input string, images []taskengine.ImagePart, audio []taskengine.AudioPart, droppedWhy droppedReasons, droppedContentKinds []string, emit func(context.Context, libacp.SessionNotification)) nativeturn.Result {
	t := d.t

	turnCtx = libtracker.WithNewRequestID(turnCtx)
	reqID, _ := turnCtx.Value(libtracker.ContextKeyRequestID).(string)

	reportErr, reportChange, end := t.tracker().Start(turnCtx, "prompt", "acp_session",
		"session_id", string(req.SessionID), "prompt_blocks", len(req.Prompt))
	defer end()

	// Journaled ahead of anything the turn produces.
	announceDroppedContent(turnCtx, req.SessionID, droppedContentKinds, droppedWhy, emit)

	if policyName := t.resolveSessionHITLPolicy(sess); policyName != "" {
		turnCtx = hitlservice.WithPolicyName(turnCtx, policyName)
	}
	if sess.MissionID != "" {
		turnCtx = missiontools.WithMissionID(turnCtx, sess.MissionID)
	}
	if sess.FiredMissions && sess.InternalSessionID != "" {
		turnCtx = missiontools.WithParentSessionID(turnCtx, sess.InternalSessionID)
	}

	translator := newNativeEventTranslator(emit, sess.effectiveTokenLimit)
	rawCh := make(chan []byte, 64)
	var drainEvents func()
	if bus := t.deps.Engine.Bus; bus != nil && reqID != "" {
		sub, err := bus.Stream(turnCtx, taskengine.TaskEventRequestSubject(reqID), rawCh)
		if err != nil {
			subErr, _, subEnd := t.tracker().Start(turnCtx, "subscribe", "acp_event_stream",
				"session_id", string(req.SessionID), "request_id", reqID)
			subErr(err)
			subEnd()
		} else {
			translateDone := make(chan struct{})
			go func() {
				defer close(translateDone)
				for payload := range rawCh {
					translator.publish(turnCtx, req.SessionID, payload)
				}
			}()
			drainEvents = func() {
				_ = sub.Unsubscribe()
				close(rawCh)
				<-translateDone
			}
		}
	}

	templateVars := t.chainTemplateVars(sess)
	templateVars["think"] = sess.think()
	var toolsAllowlist []string
	if t.deps.DB != nil {
		var err error
		toolsAllowlist, err = t.runtimeToolsAllowlist(turnCtx, runtimetypes.New(t.deps.DB.WithoutTransaction()), sess.McpServerNames)
		if err != nil {
			if drainEvents != nil {
				drainEvents()
			}
			reportErr(err)
			return nativeturn.Result{Err: err}
		}
	}

	contextLen := sess.effectiveTokenLimit()
	if contextLen == 0 {
		currentModel := sess.modelOrDefault(t.model())
		for _, state := range t.runtimeStates(turnCtx) {
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
			for _, state := range t.runtimeStates(turnCtx) {
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

	resp, err := d.agent.Prompt(turnCtx, agentservice.PromptRequest{
		SessionID:      sess.InternalSessionID,
		Input:          input,
		Images:         images,
		Audio:          audio,
		Chain:          t.deps.ChainRegistry.Default(),
		TemplateVars:   templateVars,
		ToolsAllowlist: toolsAllowlist,
		ContextLength:  contextLen,
	})

	if drainEvents != nil {
		drainEvents()
	}

	if err != nil {
		cancelled := errors.Is(err, context.Canceled) || errors.Is(turnCtx.Err(), context.Canceled)
		if cancelled {
			reportChange(string(req.SessionID), map[string]any{
				"stop_reason":           string(libacp.StopReasonCancelled),
				"request_id":            reqID,
				"dropped_content_kinds": droppedContentKinds,
			})
			return nativeturn.Result{StopReason: libacp.StopReasonCancelled}
		}
		reportErr(err)
		if resp != nil {
			return nativeturn.Result{StopReason: mapStopReason(resp.StopReason), Err: err}
		}
		return nativeturn.Result{Err: err}
	}

	stopReason := mapStopReason(resp.StopReason)
	if errors.Is(turnCtx.Err(), context.Canceled) {
		stopReason = libacp.StopReasonCancelled
	}

	d.emitSessionInfo(turnCtx, req.SessionID, sess, emit)

	reportChange(string(req.SessionID), map[string]any{
		"stop_reason":           string(stopReason),
		"request_id":            reqID,
		"dropped_content_kinds": droppedContentKinds,
	})
	return nativeturn.Result{StopReason: stopReason}
}

func (d *nativeDriver) emitSessionInfo(ctx context.Context, sid libacp.SessionID, sess *sessionEntry, emit func(context.Context, libacp.SessionNotification)) {
	update := libacp.SessionUpdate{
		SessionUpdate: libacp.SessionUpdateSessionInfo,
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	if title := d.t.sessionInfoTitle(ctx, sess.InternalSessionID); title != "" {
		update.Title = title
	}
	emit(ctx, libacp.SessionNotification{SessionID: sid, Update: update})
}

func (t *Transport) reattachNativeTurn(ctx context.Context, sid libacp.SessionID) {
	if t.deps.NativeTurns == nil {
		return
	}
	viewer := newNativeTurnViewer(t, sid)
	turn, ok, err := t.deps.NativeTurns.AttachIfRunning(ctx, sid, viewer)
	if err != nil || !ok {
		return
	}
	go func() {
		select {
		case <-t.connCtx.Done():
		case <-turn.Done():
		}
		turn.Detach(viewer.ID())
	}()
}
