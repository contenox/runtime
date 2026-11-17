package acpsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/contenox/runtime/libacp"
	"github.com/google/uuid"
)

const operationUpdateMethod = "_contenox/operation"

const (
	progressMinInterval = 500 * time.Millisecond
	progressMinPercent  = 1
)

type operationUpdate struct {
	OperationID string `json:"operationId"`
	Kind        string `json:"kind"`
	State       string `json:"state"`
	Phase       string `json:"phase,omitempty"`
	Message     string `json:"message,omitempty"`
	Current     int64  `json:"current,omitempty"`
	Total       int64  `json:"total,omitempty"`
	Unit        string `json:"unit,omitempty"`
}

type operationReporter struct {
	transport   *Transport
	operationID string
	kind        string
	chat        *chatToolCall
}

type chatProgressKey struct{}

type chatProgress struct {
	transport *Transport
	session   libacp.SessionID
}

func withChatProgress(ctx context.Context, t *Transport, session libacp.SessionID) context.Context {
	return context.WithValue(ctx, chatProgressKey{}, &chatProgress{transport: t, session: session})
}

func chatProgressFrom(ctx context.Context) *chatProgress {
	progress, _ := ctx.Value(chatProgressKey{}).(*chatProgress)
	return progress
}

func (t *Transport) withOperation(ctx context.Context, operationID, kind, message string, run func(*operationReporter) error) error {
	reporter := &operationReporter{transport: t, operationID: strings.TrimSpace(operationID), kind: kind}
	if progress := chatProgressFrom(ctx); progress != nil {
		reporter.chat = progress.begin(context.WithoutCancel(ctx), kind, message)
	}
	if err := reporter.running("prepare", message, 0, 0); err != nil {
		reporter.chat.finish(operationFailed, err.Error(), err)
		return err
	}
	err := run(reporter)
	state, terminalMessage := "completed", message+" complete"
	if err != nil {
		state, terminalMessage = operationFailed, err.Error()
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			state, terminalMessage = operationCancelled, message+" cancelled"
		}
	}
	reporter.chat.finish(state, terminalMessage, err)
	if reportErr := reporter.send(operationUpdate{State: state, Message: terminalMessage}); reportErr != nil {
		if err == nil {
			return reportErr
		}
		return errors.Join(err, reportErr)
	}
	return err
}

const (
	operationFailed    = "failed"
	operationCancelled = "cancelled"
)

func (r *operationReporter) running(phase, message string, current, total int64) error {
	if total < 0 {
		total = 0
	}
	update := operationUpdate{State: "running", Phase: phase, Message: message, Current: current, Total: total}
	if current > 0 || total > 0 {
		update.Unit = "bytes"
	}
	r.chat.update(progressText(message, current, total, update.Unit), progressPercent(current, total))
	return r.send(update)
}

func (r *operationReporter) send(update operationUpdate) error {
	if r.operationID == "" {
		return nil
	}
	if r.transport.conn == nil {
		return fmt.Errorf("operation progress connection unavailable")
	}
	update.OperationID = r.operationID
	update.Kind = r.kind
	raw, err := json.Marshal(update)
	if err != nil {
		return err
	}
	return r.transport.conn.SendExtNotification(operationUpdateMethod, raw)
}

func progressText(message string, current, total int64, unit string) string {
	if unit != "bytes" || total <= 0 {
		return message
	}
	switch {
	case total >= 1<<30:
		return fmt.Sprintf("%s — %.1f/%.1f GB", message, float64(current)/(1<<30), float64(total)/(1<<30))
	case total >= 1<<20:
		return fmt.Sprintf("%s — %d/%d MB", message, current>>20, total>>20)
	}
	return fmt.Sprintf("%s — %d/%d KB", message, current>>10, total>>10)
}

func progressPercent(current, total int64) int {
	if total <= 0 {
		return -1
	}
	return int(current * 100 / total)
}

type chatToolCall struct {
	progress *chatProgress
	ctx      context.Context
	id       string
	kind     libacp.ToolKind

	mu       sync.Mutex
	title    string
	lastSent time.Time
	lastPct  int
	started  bool
	finished bool
}

func (p *chatProgress) begin(ctx context.Context, operationKind, title string) *chatToolCall {
	call := &chatToolCall{progress: p, ctx: ctx, id: "operation-" + uuid.NewString(), kind: libacp.ToolKindOther, title: title, lastPct: -1}
	if operationKind == "model-download" {
		call.kind = libacp.ToolKindFetch
	}
	p.transport.sendUpdate(ctx, libacp.SessionNotification{SessionID: p.session, Update: libacp.SessionUpdate{
		SessionUpdate: libacp.SessionUpdateToolCall, ToolCallID: call.id, Title: title, Kind: call.kind, Status: libacp.ToolCallStatusPending,
	}})
	call.lastSent = time.Now()
	return call
}

func (c *chatToolCall) update(title string, percent int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished || c.started && title == c.title {
		return
	}
	now := time.Now()
	if c.started && percent >= 0 && now.Sub(c.lastSent) < progressMinInterval && percent-c.lastPct < progressMinPercent {
		return
	}
	c.started, c.title, c.lastSent, c.lastPct = true, title, now, percent
	c.progress.transport.sendUpdate(c.ctx, libacp.SessionNotification{SessionID: c.progress.session, Update: libacp.SessionUpdate{
		SessionUpdate: libacp.SessionUpdateToolCallUpdate, ToolCallID: c.id, Title: title, Kind: c.kind, Status: libacp.ToolCallStatusInProgress,
	}})
}

func (c *chatToolCall) finish(state, message string, cause error) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished {
		return
	}
	c.finished = true
	update := libacp.SessionUpdate{SessionUpdate: libacp.SessionUpdateToolCallUpdate, ToolCallID: c.id, Title: message, Kind: c.kind, Status: libacp.ToolCallStatusCompleted}
	if state != "completed" {
		update.Status = libacp.ToolCallStatusFailed
		update.RawOutput = json.RawMessage(jsonString(message))
		if state == operationCancelled {
			update.Meta = json.RawMessage(`{"cancelled":true}`)
		} else if cause != nil {
			update.Meta = json.RawMessage(`{"error":` + jsonString(cause.Error()) + `}`)
		}
	}
	c.progress.transport.sendUpdate(c.ctx, libacp.SessionNotification{SessionID: c.progress.session, Update: update})
}
