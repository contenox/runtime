package workspacetriggers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/eventlog"
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
	rows, err := store.ListEventFirings(ctx, runtimetypes.EventFiringFilter{Limit: limit})
	if err != nil {
		return nil, err
	}
	triggers, err := h.Store.List(ctx, scope)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, t := range triggers {
		names[t.ID] = t.Name
	}
	out := make([]Firing, 0, len(rows))
	for _, f := range rows {
		rec, err := h.started(ctx, scope, f)
		if err != nil {
			return nil, err
		}
		name, ok := names[f.TriggerName]
		if !ok {
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

func (h History) Running(ctx context.Context, scope string) (map[string]bool, error) {
	store, err := h.firingStore(scope)
	if err != nil {
		return nil, err
	}
	rows, err := store.ListEventFirings(ctx, runtimetypes.EventFiringFilter{Status: runtimetypes.EventFiringStatusRunning, Limit: runtimetypes.MaxEventFiringLimit})
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, f := range rows {
		out[f.TriggerName] = true
	}
	return out, nil
}
