package workspacetriggers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/eventlog"
	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/workspaceevents"
)

type History struct {
	DB    libdb.DBManager
	Log   eventlog.Service
	Store Store
	Now   func() time.Time
}

func (h History) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h History) firingStore(scope string) (runtimetypes.EventFiringStore, error) {
	return runtimetypes.NewEventFiringStore(h.DB.WithoutTransaction(), scope)
}

type startRecord struct {
	Target      string `json:"target,omitempty"`
	OnDone      string `json:"onDone,omitempty"`
	Hop         int    `json:"hop,omitempty"`
	MissionID   string `json:"missionId"`
	TriggerName string `json:"triggerName"`
}

func (h History) started(ctx context.Context, scope string, f runtimetypes.EventFiring) (startRecord, error) {
	from := f.CreatedAt.Add(-time.Minute)
	to := h.now().Add(time.Minute)
	events, err := h.Log.GetEventsBySubject(ctx, scope, workspaceevents.TypeTriggerStarted, from, to, f.RequestID, 1)
	if err != nil {
		return startRecord{}, fmt.Errorf("workspacetriggers: start record of %s: %w", f.RequestID, err)
	}
	if len(events) == 0 {
		return startRecord{}, nil
	}
	var rec startRecord
	if err := json.Unmarshal(events[0].Data, &rec); err != nil {
		return startRecord{}, fmt.Errorf("workspacetriggers: start record of %s: %w", f.RequestID, err)
	}
	return rec, nil
}

func (h History) Firings(ctx context.Context, scope string, limit int) ([]Firing, error) {
	store, err := h.firingStore(scope)
	if err != nil {
		return nil, err
	}
	return h.list(ctx, scope, store, runtimetypes.EventFiringFilter{Limit: limit})
}
func (h History) list(ctx context.Context, scope string, store runtimetypes.EventFiringStore, filter runtimetypes.EventFiringFilter) ([]Firing, error) {
	rows, err := store.ListEventFirings(ctx, filter)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	out := make([]Firing, 0, len(rows))
	for _, f := range rows {
		rec, err := h.started(ctx, scope, f)
		if err != nil {
			return nil, err
		}
		name, ok := names[f.TriggerName]
		if !ok {
			rule, e := h.Store.Get(ctx, scope, f.TriggerName)
			if e != nil && !errors.Is(e, ErrNotFound) {
				return nil, e
			}
			name = rule.Name
			names[f.TriggerName] = name
		}
		if name == "" {
			name = rec.TriggerName
		}
		out = append(out, Firing{
			TriggerID:   f.TriggerName,
			TriggerName: name,
			NID:         f.NID,
			Status:      f.Status,
			Error:       f.Error,
			MissionID:   rec.MissionID,
			CreatedAt:   f.CreatedAt,
			UpdatedAt:   f.UpdatedAt,
		})
	}
	return out, nil
}

func (h History) Running(ctx context.Context, scope string, rules []Trigger) (map[string]bool, error) {
	out := map[string]bool{}
	if len(rules) == 0 {
		return out, nil
	}
	args := []any{scope}
	keys := make([]string, 0, len(rules))
	for _, t := range rules {
		args = append(args, t.ID)
		keys = append(keys, fmt.Sprintf("$%d", len(args)))
	}
	rows, err := h.DB.WithoutTransaction().QueryContext(ctx, "SELECT DISTINCT trigger_name FROM event_firings WHERE workspace_id=$1 AND status='running' AND trigger_name IN ("+strings.Join(keys, ",")+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

type Waiting struct {
	NextCursor string   `json:"nextCursor,omitempty"`
	Parked     []Firing `json:"parked"`
	Active     []Firing `json:"active"`
	Asks       []string `json:"pendingApprovalIds"`
}

func (h History) Waiting(ctx context.Context, scope string) (Waiting, error) {
	return h.WaitingPage(ctx, scope, "", 100)
}

func (h History) Page(ctx context.Context, scope, cursor string, limit int, nid int64, waiting bool) ([]Firing, string, error) {
	filter := runtimetypes.EventFiringFilter{Limit: limit + 1, ExactNID: nid, Waiting: waiting}
	if cursor != "" {
		n, key, ok := strings.Cut(cursor, "/")
		v, e := strconv.ParseInt(n, 10, 64)
		if !ok || key == "" || e != nil || v < 1 {
			return nil, "", fmt.Errorf("%w: invalid firing cursor", ErrInvalidTrigger)
		}
		filter.BeforeNID = v
		filter.AfterTrigger = key
	}
	store, err := h.firingStore(scope)
	if err != nil {
		return nil, "", err
	}
	rows, err := h.list(ctx, scope, store, filter)
	if err != nil {
		return nil, "", err
	}
	if len(rows) > limit {
		last := rows[limit-1]
		return rows[:limit], fmt.Sprintf("%d/%s", last.NID, last.TriggerID), nil
	}
	return rows, "", nil
}

func (h History) WaitingPage(ctx context.Context, scope, cursor string, limit int) (Waiting, error) {
	out := Waiting{Parked: []Firing{}, Active: []Firing{}, Asks: []string{}}
	rows, next, err := h.Page(ctx, scope, cursor, limit, 0, true)
	if err != nil {
		return out, err
	}
	out.NextCursor = next
	missions := missionservice.New(h.DB)
	for _, f := range rows {
		if f.Status == runtimetypes.EventFiringStatusRunning && f.MissionID != "" {
			reports, e := missions.ListReports(ctx, f.MissionID, 1)
			if e != nil {
				return out, e
			}
			if len(reports) > 0 && reports[0].Kind == missionservice.ReportKindBlocker {
				f.Status = "parked"
				f.Error = reports[0].Summary
			}
		}
		if f.Status == "parked" {
			out.Parked = append(out.Parked, f)
		} else {
			out.Active = append(out.Active, f)
		}
	}
	approvals := runtimetypes.New(h.DB.WithoutTransaction())
	seen := map[string]bool{}
	for _, f := range rows {
		if f.Status != runtimetypes.EventFiringStatusRunning {
			continue
		}
		var rows []*runtimetypes.HITLApproval
		if f.MissionID != "" {
			rows, err = approvals.ListHITLApprovalsForMission(ctx, f.MissionID, 100)
		} else {
			rule, e := h.Store.Get(ctx, scope, f.TriggerID)
			if e == nil && rule.SessionID != "" {
				rows, err = approvals.ListHITLApprovalsForSession(ctx, rule.SessionID, 100)
			}
		}
		if err != nil {
			return out, err
		}
		for _, row := range rows {
			if row.State == runtimetypes.HITLApprovalPending && !seen[row.ID] {
				seen[row.ID] = true
				out.Asks = append(out.Asks, row.ID)
			}
		}
	}
	return out, nil
}
