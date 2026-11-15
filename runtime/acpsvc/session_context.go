package acpsvc

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strconv"

	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
)

func shiftID(ev taskengine.TaskEvent) string {
	return "context-shift-" + ev.RequestID + "-" + ev.TaskID + "-" + strconv.FormatInt(ev.Timestamp.UnixNano(), 10)
}

func contextShiftNotification(sid libacp.SessionID, ev taskengine.TaskEvent) libacp.SessionNotification {
	shift := ev.ContextShift
	details := struct {
		*taskengine.ContextShift
		Step  string `json:"step,omitempty"`
		Model string `json:"model,omitempty"`
	}{shift, ev.TaskID, ev.ModelName}
	meta, _ := json.Marshal(map[string]any{"contenox.contextShift": details})
	return libacp.SessionNotification{SessionID: sid, Update: libacp.SessionUpdate{
		SessionUpdate: libacp.SessionUpdateToolCall,
		ToolCallID:    shiftID(ev),
		Title:         fmt.Sprintf("Context shifted - %d older messages omitted from this request (%d-token window)", shift.Removed, shift.Window),
		Status:        libacp.ToolCallStatusCompleted, Kind: libacp.ToolKindOther, Meta: meta,
	}}
}

func (t *Transport) recordContextEvent(ctx context.Context, sess *sessionEntry, ev taskengine.TaskEvent) {
	if t.deps.DB == nil || sess == nil || sess.InternalSessionID == "" {
		return
	}
	var err error
	switch ev.Kind {
	case taskengine.TaskEventContextShift:
		if ev.ContextShift == nil {
			return
		}
		var raw []byte
		raw, err = json.Marshal(ev)
		if err == nil {
			_, err = t.deps.DB.WithoutTransaction().ExecContext(ctx, `INSERT INTO session_context_shifts (session_id,at_ns,event_id,payload) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, sess.InternalSessionID, ev.Timestamp.UnixNano(), shiftID(ev), string(raw))
		}
	case taskengine.TaskEventTokenUsage:
		err = runtimetypes.New(t.deps.DB.WithoutTransaction()).SetKV(ctx, "acp:context_used:"+sess.InternalSessionID, json.RawMessage(strconv.Itoa(ev.TokenUsed)))
	default:
		return
	}
	if err != nil {
		report, _, end := t.tracker().Start(ctx, "store", "session_context")
		report(err)
		end()
	}
}

func (t *Transport) contextShiftHistory(ctx context.Context, sid libacp.SessionID) iter.Seq[taskengine.TaskEvent] {
	return func(yield func(taskengine.TaskEvent) bool) {
		sess, _ := t.sessionFor(sid)
		if t.deps.DB == nil || sess == nil {
			return
		}
		var at int64
		var id string
		const limit = 100
		for {
			page, err := t.contextShiftPage(ctx, sess.InternalSessionID, at, id, limit)
			if err != nil {
				report, _, end := t.tracker().Start(ctx, "load", "session_context_shifts")
				report(err)
				end()
				return
			}
			for _, ev := range page {
				if ev.ContextShift != nil && !yield(ev) {
					return
				}
			}
			if len(page) < limit {
				return
			}
			last := page[len(page)-1]
			at, id = last.Timestamp.UnixNano(), shiftID(last)
		}
	}
}

func (t *Transport) contextShiftPage(ctx context.Context, session string, at int64, id string, limit int) ([]taskengine.TaskEvent, error) {
	rows, err := t.deps.DB.WithoutTransaction().QueryContext(ctx, `SELECT payload FROM session_context_shifts WHERE session_id=$1 AND (at_ns,event_id)>($2,$3) ORDER BY at_ns,event_id LIMIT $4`, session, at, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var shifts []taskengine.TaskEvent
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var ev taskengine.TaskEvent
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			return nil, err
		}
		shifts = append(shifts, ev)
	}
	return shifts, rows.Err()
}
