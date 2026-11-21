package workspacetriggers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libevents"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/google/uuid"
)

const ListenerOwner = "workspace-trigger"

type listenerTarget struct {
	Workflow         string `json:"workflow,omitempty"`
	WorkflowRevision string `json:"workflowRevision,omitempty"`
	OnDone           string `json:"onDone,omitempty"`
	Target           string `json:"target,omitempty"`
	SessionID        string `json:"sessionId,omitempty"`
	SessionMode      string `json:"sessionMode,omitempty"`
	Root             string `json:"root"`
	AgentName        string `json:"agentName"`
	HITLPolicyName   string `json:"hitlPolicyName"`
	Prompt           string `json:"prompt"`
}

type listenerMeta struct {
	CreatedBy  string    `json:"createdBy,omitempty"`
	TopicGlob  string    `json:"topicGlob,omitempty"`
	Name       string    `json:"name"`
	PathGlob   string    `json:"pathGlob"`
	DebounceMs int       `json:"debounceMs"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type Store struct {
	DB  libdb.DBManager
	Now func() time.Time
}

func (s Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s Store) listeners(scope string) (*libevents.ListenerStore, error) {
	return libevents.NewListenerStore(runtimetypes.EventConsumerConfig, scope)
}

func toListener(t Trigger) (*libevents.Listener, error) {
	target, err := json.Marshal(listenerTarget{Workflow: t.Workflow, WorkflowRevision: t.WorkflowRevision, OnDone: t.OnDone, Target: t.Target, SessionID: t.SessionID, SessionMode: t.SessionMode, Root: t.Root, AgentName: t.AgentName, HITLPolicyName: t.HITLPolicyName, Prompt: t.Prompt})
	if err != nil {
		return nil, fmt.Errorf("workspacetriggers: encode target: %w", err)
	}
	meta, err := json.Marshal(listenerMeta{CreatedBy: t.CreatedBy, TopicGlob: t.TopicGlob, Name: t.Name, PathGlob: t.PathGlob, DebounceMs: t.DebounceMs, Enabled: t.Enabled, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt})
	if err != nil {
		return nil, fmt.Errorf("workspacetriggers: encode metadata: %w", err)
	}
	filters := map[string]map[string]string{}
	if t.PathGlob != "" {
		for _, e := range t.Events {
			filters[e] = map[string]string{"path": t.PathGlob}
		}
	}
	return &libevents.Listener{
		ID:             t.ID,
		Kind:           libevents.ListenerKindStart,
		Target:         string(target),
		Owner:          ListenerOwner,
		Types:          append([]string(nil), t.Events...),
		ContextFilters: filters,
		Metadata:       string(meta),
	}, nil
}

func fromListener(l *libevents.Listener) (Trigger, error) {
	var target listenerTarget
	if err := json.Unmarshal([]byte(l.Target), &target); err != nil {
		return Trigger{}, fmt.Errorf("workspacetriggers: trigger %s target: %w", l.ID, err)
	}
	var meta listenerMeta
	if err := json.Unmarshal([]byte(l.Metadata), &meta); err != nil {
		return Trigger{}, fmt.Errorf("workspacetriggers: trigger %s metadata: %w", l.ID, err)
	}
	return Trigger{
		Workflow: target.Workflow, WorkflowRevision: target.WorkflowRevision, CreatedBy: meta.CreatedBy, OnDone: target.OnDone, ID: l.ID, Target: target.Target, SessionID: target.SessionID, SessionMode: target.SessionMode,
		Root:     target.Root,
		Name:     meta.Name,
		Events:   append([]string(nil), l.Types...),
		PathGlob: meta.PathGlob, TopicGlob: meta.TopicGlob,
		DebounceMs:     meta.DebounceMs,
		AgentName:      target.AgentName,
		HITLPolicyName: target.HITLPolicyName,
		Prompt:         target.Prompt,
		Enabled:        meta.Enabled,
		CreatedAt:      meta.CreatedAt,
		UpdatedAt:      meta.UpdatedAt,
	}, nil
}

func (s Store) List(ctx context.Context, scope string) ([]Trigger, error) {
	store, err := s.listeners(scope)
	if err != nil {
		return nil, err
	}
	exec := s.DB.WithoutTransaction()
	out := []Trigger{}
	var afterCreated time.Time
	afterID := ""
	for {
		page, err := store.ListListeners(ctx, exec, afterCreated, afterID, libevents.MaxListenerLimit)
		if err != nil {
			return nil, fmt.Errorf("workspacetriggers: list: %w", err)
		}
		for _, l := range page {
			if l.Owner != ListenerOwner {
				continue
			}
			t, err := fromListener(l)
			if err != nil {
				return nil, err
			}
			out = append(out, t)
		}
		if len(page) < libevents.MaxListenerLimit {
			break
		}
		last := page[len(page)-1]
		afterCreated, afterID = last.CreatedAt, last.ID
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s Store) Get(ctx context.Context, scope, id string) (Trigger, error) {
	store, err := s.listeners(scope)
	if err != nil {
		return Trigger{}, err
	}
	l, err := store.GetListener(ctx, s.DB.WithoutTransaction(), id)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, libdb.ErrNotFound) {
		return Trigger{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return Trigger{}, fmt.Errorf("workspacetriggers: get: %w", err)
	}
	if l.Owner != ListenerOwner {
		return Trigger{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return fromListener(l)
}

func (s Store) nameTaken(ctx context.Context, scope, name, exceptID string) (bool, error) {
	list, err := s.List(ctx, scope)
	if err != nil {
		return false, err
	}
	for _, t := range list {
		if t.ID != exceptID && strings.EqualFold(t.Name, name) {
			return true, nil
		}
	}
	return false, nil
}

func (s Store) Create(ctx context.Context, scope string, t *Trigger) error {
	if err := t.Normalize(); err != nil {
		return err
	}
	if taken, err := s.nameTaken(ctx, scope, t.Name, ""); err != nil {
		return err
	} else if taken {
		return fmt.Errorf("%w: %s", ErrNameTaken, t.Name)
	}
	now := s.now()
	t.ID = "trg_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	t.CreatedAt, t.UpdatedAt = now, now
	return s.write(ctx, scope, *t, false)
}

func (s Store) Update(ctx context.Context, scope string, t *Trigger) error {
	current, err := s.Get(ctx, scope, t.ID)
	if err != nil {
		return err
	}
	if err := t.Normalize(); err != nil {
		return err
	}
	if taken, err := s.nameTaken(ctx, scope, t.Name, t.ID); err != nil {
		return err
	} else if taken {
		return fmt.Errorf("%w: %s", ErrNameTaken, t.Name)
	}
	t.CreatedBy = current.CreatedBy
	t.Root = current.Root
	t.CreatedAt = current.CreatedAt
	t.UpdatedAt = s.now()
	return s.write(ctx, scope, *t, true)
}

func (s Store) write(ctx context.Context, scope string, t Trigger, replace bool) error {
	store, err := s.listeners(scope)
	if err != nil {
		return err
	}
	l, err := toListener(t)
	if err != nil {
		return err
	}
	tx, commit, release, err := s.DB.WithTransaction(ctx)
	if err != nil {
		return fmt.Errorf("workspacetriggers: write: %w", err)
	}
	defer func() { _ = release() }()
	if replace {
		if err := store.DeleteListener(ctx, tx, t.ID); err != nil {
			return fmt.Errorf("workspacetriggers: replace %s: %w", t.ID, err)
		}
	}
	if err := store.AppendListener(ctx, tx, l); err != nil {
		return fmt.Errorf("workspacetriggers: write %s: %w", t.ID, err)
	}
	if err := commit(ctx); err != nil {
		return fmt.Errorf("workspacetriggers: write commit: %w", err)
	}
	return nil
}

func (s Store) Delete(ctx context.Context, scope, id string) error {
	if _, err := s.Get(ctx, scope, id); err != nil {
		return err
	}
	store, err := s.listeners(scope)
	if err != nil {
		return err
	}
	err = store.DeleteListener(ctx, s.DB.WithoutTransaction(), id)
	if errors.Is(err, libdb.ErrNotFound) {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return fmt.Errorf("workspacetriggers: delete: %w", err)
	}
	return nil
}

func (s Store) Page(ctx context.Context, scope, cursor string, limit int) ([]Trigger, string, error) {
	rows, err := s.DB.WithoutTransaction().QueryContext(ctx, `SELECT l.id,l.target,l.metadata,t.event_type FROM
 (SELECT workspace_id,id,target,metadata FROM event_listeners WHERE workspace_id=$1 AND owner=$2 AND id>$3 ORDER BY id LIMIT $4) l
 LEFT JOIN event_listener_topics t ON t.workspace_id=l.workspace_id AND t.listener_id=l.id ORDER BY l.id,t.event_type`, scope, ListenerOwner, cursor, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []Trigger{}
	for rows.Next() {
		var l libevents.Listener
		var typ sql.NullString
		if err := rows.Scan(&l.ID, &l.Target, &l.Metadata, &typ); err != nil {
			return nil, "", err
		}
		if len(out) == 0 || out[len(out)-1].ID != l.ID {
			if len(out) == limit {
				return out, out[len(out)-1].ID, nil
			}
			t, err := fromListener(&l)
			if err != nil {
				return nil, "", err
			}
			out = append(out, t)
		}
		if typ.Valid {
			out[len(out)-1].Events = append(out[len(out)-1].Events, typ.String)
		}
	}
	return out, "", rows.Err()
}

func (s Store) MatchPage(ctx context.Context, scope, eventType, subject, cursor string, limit int) ([]Trigger, string, error) {
	out := []Trigger{}
	for {
		rules, next, err := s.Page(ctx, scope, cursor, 100)
		if err != nil {
			return nil, "", err
		}
		matches, err := Matching(rules, eventType, subject)
		if err != nil {
			return nil, "", err
		}
		out = append(out, matches...)
		if len(out) > limit {
			return out[:limit], out[limit-1].ID, nil
		}
		if next == "" {
			return out, "", nil
		}
		cursor = next
	}
}

func (s Store) EachPage(ctx context.Context, scope string, visit func([]Trigger) error) error {
	cursor := ""
	for {
		rules, next, err := s.Page(ctx, scope, cursor, 200)
		if err != nil {
			return err
		}
		if err := visit(rules); err != nil {
			return err
		}
		if next == "" {
			return nil
		}
		cursor = next
	}
}
