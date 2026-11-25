package acpsvc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/agentservice"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/missiontools"
	"github.com/contenox/runtime/runtime/nativeturn"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/contenox/runtime/runtime/turninput"
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
	recordContext     func(context.Context, taskengine.TaskEvent)
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
	if tr.recordContext != nil && (ev.Kind == taskengine.TaskEventContextShift || ev.Kind == taskengine.TaskEventTokenUsage) {
		tr.recordContext(ctx, ev)
	}
	switch ev.Kind {
	case taskengine.TaskEventContextShift:
		if ev.ContextShift != nil {
			tr.emit(ctx, contextShiftNotification(sid, ev))
		}
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
		if taskengine.TaskHandler(ev.TaskHandler) == taskengine.HandleRoute && ev.Transition != "" {
			tr.emit(ctx, routeNotification(sid, ev))
			return
		}
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

func (t *Transport) handleSteer(ctx context.Context, params json.RawMessage) (json.RawMessage, *libacp.Error) {
	var p struct {
		libacp.PromptRequest
		Text string `json:"text"` // Earlier clients sent a single text field.
	}
	if json.Unmarshal(params, &p) != nil || p.SessionID == "" {
		return nil, libacp.InvalidParams("sessionId and prompt are required")
	}
	if p.Prompt == nil && strings.TrimSpace(p.Text) != "" {
		p.Prompt = []libacp.ContentBlock{libacp.NewTextContent(p.Text)}
	}
	sess, ok := t.sessionFor(p.SessionID)
	if !ok {
		return nil, libacp.InvalidParams("unknown session")
	}
	if _, native := sess.driver.(*nativeDriver); !native || t.deps.NativeTurns == nil {
		return nil, libacp.InvalidParams("steering requires a native chat")
	}
	content, rpcErr := t.prepareUserInput(ctx, p.PromptRequest, sess)
	if rpcErr != nil {
		return nil, rpcErr
	}
	var notice *libacp.SessionUpdate
	if update, dropped := droppedContentNotice(content.dropped, content.reasons); dropped {
		notice = &update
	}
	queued, err := t.deps.NativeTurns.Enqueue(p.SessionID, content.Message, notice)
	if err != nil {
		return nil, libacp.InvalidParams(err.Error())
	}
	receipt := map[string]any{"queued": queued}
	if queued && notice != nil {
		receipt["_meta"] = notice.Meta
	}
	return mustJSON(receipt), nil
}

func (d *nativeDriver) promptViaRegistry(ctx context.Context, sid libacp.SessionID, sess *sessionEntry, content nativeUserInput) (libacp.PromptResponse, error) {
	t := d.t
	policy, _ := ctx.Value(eventTurnPolicy{}).(string)

	viewer := newNativeTurnViewer(t, sid)
	var turn *nativeturn.Turn
	var err error
	if policy != "" {
		turn, _, err = t.deps.NativeTurns.StartIfIdle(sid, func(turnCtx context.Context, emit func(context.Context, libacp.SessionNotification)) nativeturn.Result {
			turnCtx = context.WithValue(turnCtx, eventTurnPolicy{}, policy)
			turnCtx = runtimetypes.WithEventHop(turnCtx, runtimetypes.EventHopFromContext(ctx))
			return d.runNativeTurn(turnCtx, sid, sess, content, nil, emit)
		}, viewer)
	} else {
		turn, _, err = t.deps.NativeTurns.StartInteractive(sid, func(turnCtx context.Context, emit func(context.Context, libacp.SessionNotification), first *turninput.Message, pending turninput.Pending) nativeturn.Result {
			input := content
			if first != nil {
				input = nativeUserInput{Message: *first}
			}
			return d.runNativeTurn(turnCtx, sid, sess, input, pending, emit)
		}, viewer)
	}
	if err != nil {
		if policy != "" {
			return libacp.PromptResponse{}, err
		}
		return libacp.PromptResponse{StopReason: libacp.StopReasonCancelled}, nil
	}

	select {
	case <-turn.Done():
		turn.Detach(viewer.ID())
		resp, err := nativeResultToResponse(turn.Result())
		return withDroppedContentMeta(resp, content.dropped, content.reasons), err
	case <-ctx.Done():
		turn.Detach(viewer.ID())
		if policy != "" {
			turn.Cancel()
		}
		return libacp.PromptResponse{StopReason: libacp.StopReasonCancelled}, nil
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

func (d *nativeDriver) runNativeTurn(turnCtx context.Context, sid libacp.SessionID, sess *sessionEntry, input nativeUserInput, pending turninput.Pending, emit func(context.Context, libacp.SessionNotification)) nativeturn.Result {
	t := d.t

	turnCtx = libtracker.WithNewRequestID(turnCtx)
	reqID, _ := turnCtx.Value(libtracker.ContextKeyRequestID).(string)

	reportErr, reportChange, end := t.tracker().Start(turnCtx, "prompt", "acp_session",
		"session_id", string(sid), "prompt_blocks", input.BlockCount)
	defer end()

	// Journaled ahead of anything the turn produces.
	announceDroppedContent(turnCtx, sid, input.dropped, input.reasons, emit)

	policyName := t.resolveSessionHITLPolicy(sess)
	if eventPolicy, ok := turnCtx.Value(eventTurnPolicy{}).(string); ok {
		policyName = eventPolicy
	}
	if policyName != "" {
		turnCtx = hitlservice.WithPolicyName(turnCtx, policyName)
	}
	if sess.MissionID != "" {
		turnCtx = missiontools.WithMissionID(turnCtx, sess.MissionID)
	}
	if sess.FiredMissions && sess.InternalSessionID != "" {
		turnCtx = missiontools.WithParentSessionID(turnCtx, sess.InternalSessionID)
	}

	translator := newNativeEventTranslator(emit, sess.effectiveTokenLimit)
	translator.recordContext = func(ctx context.Context, ev taskengine.TaskEvent) { t.recordContextEvent(ctx, sess, ev) }
	rawCh := make(chan []byte, 64)
	var drainEvents func()
	if bus := t.deps.Engine.Bus; bus != nil && reqID != "" {
		sub, err := bus.Stream(turnCtx, taskengine.TaskEventRequestSubject(reqID), rawCh)
		if err != nil {
			subErr, _, subEnd := t.tracker().Start(turnCtx, "subscribe", "acp_event_stream",
				"session_id", string(sid), "request_id", reqID)
			subErr(err)
			subEnd()
		} else {
			translateDone := make(chan struct{})
			go func() {
				defer close(translateDone)
				for payload := range rawCh {
					translator.publish(turnCtx, sid, payload)
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
	if err := requireDefaultModel(templateVars); err != nil {
		if drainEvents != nil {
			drainEvents()
		}
		reportErr(err)
		return nativeturn.Result{Err: err}
	}
	var toolsAllowlist []string
	if t.deps.DB != nil {
		var err error
		toolsAllowlist, err = t.runtimeToolsAllowlist(turnCtx, runtimetypes.New(t.deps.DB.WithoutTransaction()), sess)
		if err != nil {
			if drainEvents != nil {
				drainEvents()
			}
			reportErr(err)
			return nativeturn.Result{Err: err}
		}
	}

	resp, err := d.agent.Prompt(turnCtx, agentservice.PromptRequest{
		SessionID:      sess.InternalSessionID,
		InputMessageID: input.ID,
		PendingInput:   pending,
		Input:          input.Text,
		Images:         input.Images,
		Audio:          input.Audio,
		Chain:          t.deps.ChainRegistry.Default(),
		TemplateVars:   templateVars,
		ToolsAllowlist: toolsAllowlist,
		ContextLength:  t.sessionContextLength(turnCtx, sess),
	})

	if drainEvents != nil {
		drainEvents()
	}

	if err != nil {
		cancelled := errors.Is(err, context.Canceled) || errors.Is(turnCtx.Err(), context.Canceled)
		if cancelled {
			reportChange(string(sid), map[string]any{
				"stop_reason":           string(libacp.StopReasonCancelled),
				"request_id":            reqID,
				"dropped_content_kinds": input.dropped,
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

	d.emitSessionInfo(turnCtx, sid, sess, emit)

	reportChange(string(sid), map[string]any{
		"stop_reason":           string(stopReason),
		"request_id":            reqID,
		"dropped_content_kinds": input.dropped,
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
