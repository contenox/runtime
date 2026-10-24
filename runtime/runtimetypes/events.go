package runtimetypes

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
)

var (
	ErrEventTypeRequired         = errors.New("event_log: event type is required")
	ErrInvalidEventParameter     = errors.New("event_log: invalid parameter")
	ErrEventMissingRequiredField = errors.New("event_log: missing required field")
	ErrEventTooOld               = errors.New("event_log: event is too old")
	ErrEventTooNew               = errors.New("event_log: event is too new")
)

const EventAcceptanceWindow = 10 * time.Minute

const MaxEventListLimit = 1000

const eventPartitionPrefix = "event_log_"

type Event struct {
	NID         int64           `json:"nid"`
	WorkspaceID string          `json:"workspace_id"`
	Type        string          `json:"type"`
	Source      string          `json:"source,omitempty"`
	Subject     string          `json:"subject,omitempty"`
	Time        time.Time       `json:"time"`
	Data        json.RawMessage `json:"data"`
	Hop         int             `json:"hop"`
}

type EventPartition struct {
	Period    string
	TableName string
	CreatedAt time.Time
}

type EventStore interface {
	AppendEvent(ctx context.Context, event *Event) error
	ListEventsSince(ctx context.Context, workspaceID string, afterNID int64, limit int) ([]Event, error)
	ListRecentEvents(ctx context.Context, workspaceID string, beforeNID int64, limit int) ([]Event, error)
	GetEventsByType(ctx context.Context, workspaceID, eventType string, from, to time.Time, limit int) ([]Event, error)
	GetEventsBySource(ctx context.Context, workspaceID, eventType string, from, to time.Time, eventSource string, limit int) ([]Event, error)
	GetEventsBySubject(ctx context.Context, workspaceID, eventType string, from, to time.Time, subject string, limit int) ([]Event, error)
	GetEventTypesInRange(ctx context.Context, workspaceID string, from, to time.Time, limit int) ([]string, error)
	DeleteEventsByTypeInRange(ctx context.Context, workspaceID, eventType string, from, to time.Time) error
	EnsureEventPartitionExists(ctx context.Context, ts time.Time) error
	ListEventPartitions(ctx context.Context) ([]EventPartition, error)
	PruneEventPartitionsBefore(ctx context.Context, t time.Time) ([]string, error)
}

type eventStore struct {
	db  libdb.DBManager
	now func() time.Time
}

type EventStoreOption func(*eventStore)

func WithEventClock(now func() time.Time) EventStoreOption {
	return func(s *eventStore) {
		if now != nil {
			s.now = now
		}
	}
}

func NewEventStore(db libdb.DBManager, opts ...EventStoreOption) EventStore {
	if db == nil {
		panic("SERVER BUG: runtimetypes.NewEventStore called with nil db")
	}
	s := &eventStore{db: db, now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

type eventHopContextKey struct{}

func WithEventHop(ctx context.Context, hop int) context.Context {
	if hop <= 0 {
		return ctx
	}
	return context.WithValue(ctx, eventHopContextKey{}, hop)
}

func EventHopFromContext(ctx context.Context) int {
	hop, _ := ctx.Value(eventHopContextKey{}).(int)
	return hop
}

func EventPeriodKey(t time.Time) string {
	return t.UTC().Format("20060102")
}

func eventPartitionTableName(period string) string {
	return eventPartitionPrefix + period
}

func validEventPartitionTableName(name string) bool {
	digits, ok := strings.CutPrefix(name, eventPartitionPrefix)
	if !ok || len(digits) != 8 {
		return false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func (s *eventStore) EnsureEventPartitionExists(ctx context.Context, ts time.Time) error {
	period := EventPeriodKey(ts)
	table := eventPartitionTableName(period)
	if !validEventPartitionTableName(table) {
		return fmt.Errorf("%w: invalid partition table %q", ErrInvalidEventParameter, table)
	}
	exec := s.db.WithoutTransaction()
	if err := execCreateIfNotExists(ctx, exec, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			nid          INTEGER PRIMARY KEY,
			workspace_id TEXT NOT NULL DEFAULT '',
			type         TEXT NOT NULL,
			source       TEXT NOT NULL DEFAULT '',
			subject      TEXT NOT NULL DEFAULT '',
			time         TIMESTAMP NOT NULL,
			data         TEXT NOT NULL DEFAULT '{}',
			hop          INT NOT NULL DEFAULT 0
		)
	`, table)); err != nil {
		return fmt.Errorf("event_log: create partition %s: %w", table, err)
	}
	for _, stmt := range []string{
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%s_ws_nid ON %s(workspace_id, nid)`, table, table),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%s_ws_type ON %s(workspace_id, type)`, table, table),
	} {
		if err := execCreateIfNotExists(ctx, exec, stmt); err != nil {
			return fmt.Errorf("event_log: index partition %s: %w", table, err)
		}
	}
	if _, err := exec.ExecContext(ctx, `
		INSERT INTO event_partitions (period, table_name, created_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (period) DO NOTHING
	`, period, table, s.now().UTC()); err != nil {
		return fmt.Errorf("event_log: register partition %s: %w", table, err)
	}
	return nil
}

func (s *eventStore) AppendEvent(ctx context.Context, event *Event) error {
	if err := s.prepareEvent(ctx, event); err != nil {
		return err
	}
	if err := s.EnsureEventPartitionExists(ctx, event.Time); err != nil {
		return err
	}
	exec, commit, release, err := s.db.WithTransaction(ctx)
	if err != nil {
		return fmt.Errorf("event_log: append tx: %w", err)
	}
	defer func() { _ = release() }()
	if err := appendPreparedEvent(ctx, exec, event); err != nil {
		return err
	}
	if err := commit(ctx); err != nil {
		event.NID = 0
		return fmt.Errorf("event_log: append commit: %w", err)
	}
	return nil
}

func (s *eventStore) prepareEvent(ctx context.Context, event *Event) error {
	if event == nil {
		return fmt.Errorf("%w: event cannot be nil", ErrInvalidEventParameter)
	}
	event.Type = strings.TrimSpace(event.Type)
	if event.Type == "" {
		return ErrEventTypeRequired
	}
	if strings.TrimSpace(event.WorkspaceID) == "" {
		return fmt.Errorf("%w: workspace_id is required", ErrEventMissingRequiredField)
	}
	now := s.now().UTC()
	if event.Time.IsZero() {
		event.Time = now
	}
	if event.Time.Before(now.Add(-EventAcceptanceWindow)) {
		return ErrEventTooOld
	}
	if event.Time.After(now.Add(EventAcceptanceWindow)) {
		return ErrEventTooNew
	}
	if event.Data == nil {
		event.Data = json.RawMessage(`{}`)
	}
	if event.Hop == 0 {
		event.Hop = EventHopFromContext(ctx)
	}
	if event.Hop < 0 {
		return fmt.Errorf("%w: hop must be non-negative", ErrInvalidEventParameter)
	}
	return nil
}

func AppendEventInTransaction(ctx context.Context, exec libdb.Exec, event *Event) error {
	s := &eventStore{now: time.Now}
	if err := s.prepareEvent(ctx, event); err != nil {
		return err
	}
	return appendPreparedEvent(ctx, exec, event)
}

func appendPreparedEvent(ctx context.Context, exec libdb.Exec, event *Event) error {
	table := eventPartitionTableName(EventPeriodKey(event.Time))
	if _, err := exec.ExecContext(ctx, `UPDATE event_nid_seq SET last_nid = last_nid + 1 WHERE id = 1`); err != nil {
		return fmt.Errorf("event_log: bump nid: %w", err)
	}
	var nid int64
	if err := exec.QueryRowContext(ctx, `SELECT last_nid FROM event_nid_seq WHERE id = 1`).Scan(&nid); err != nil {
		return fmt.Errorf("event_log: read nid: %w", err)
	}
	if _, err := exec.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (nid, workspace_id, type, source, subject, time, data, hop)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, table), nid, event.WorkspaceID, event.Type, event.Source, event.Subject, event.Time, string(event.Data), event.Hop); err != nil {
		return fmt.Errorf("event_log: append: %w", err)
	}
	event.NID = nid
	return nil
}

func (s *eventStore) ListEventPartitions(ctx context.Context) ([]EventPartition, error) {
	rows, err := s.db.WithoutTransaction().QueryContext(ctx, `
		SELECT period, table_name, created_at FROM event_partitions ORDER BY period ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("event_partitions: list: %w", err)
	}
	defer rows.Close()
	parts := []EventPartition{}
	for rows.Next() {
		var p EventPartition
		if err := rows.Scan(&p.Period, &p.TableName, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("event_partitions: scan row: %w", err)
		}
		parts = append(parts, p)
	}
	return parts, rows.Err()
}

func (s *eventStore) eventPartitionsInRange(ctx context.Context, from, to time.Time) ([]EventPartition, error) {
	parts, err := s.ListEventPartitions(ctx)
	if err != nil {
		return nil, err
	}
	fromKey, toKey := EventPeriodKey(from), EventPeriodKey(to)
	inRange := []EventPartition{}
	for _, p := range parts {
		if p.Period >= fromKey && p.Period <= toKey {
			inRange = append(inRange, p)
		}
	}
	return inRange, nil
}

const eventColumns = "nid, workspace_id, type, source, subject, time, data, hop"

func (s *eventStore) ListEventsSince(ctx context.Context, workspaceID string, afterNID int64, limit int) ([]Event, error) {
	if err := requireEventWorkspace(workspaceID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > MaxEventListLimit {
		limit = MaxEventListLimit
	}
	parts, err := s.ListEventPartitions(ctx)
	if err != nil {
		return nil, err
	}
	exec := s.db.WithoutTransaction()
	events := []Event{}
	for _, p := range parts {
		if !validEventPartitionTableName(p.TableName) {
			return nil, fmt.Errorf("%w: registry names invalid table %q", ErrInvalidEventParameter, p.TableName)
		}
		var maxNID *int64
		if err := exec.QueryRowContext(ctx,
			fmt.Sprintf(`SELECT MAX(nid) FROM %s`, p.TableName)).Scan(&maxNID); err != nil {
			return nil, fmt.Errorf("event_log: max nid of %s: %w", p.TableName, err)
		}
		if maxNID == nil || *maxNID <= afterNID {
			continue
		}
		rows, err := exec.QueryContext(ctx, fmt.Sprintf(`
			SELECT %s FROM %s
			WHERE workspace_id = $1 AND nid > $2
			ORDER BY nid ASC
			LIMIT $3
		`, eventColumns, p.TableName), workspaceID, afterNID, limit)
		if err != nil {
			return nil, fmt.Errorf("event_log: list since %d in %s: %w", afterNID, p.TableName, err)
		}
		batch, err := scanEventRows(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, batch...)
	}
	sort.Slice(events, func(i, j int) bool { return events[i].NID < events[j].NID })
	if len(events) > limit {
		events = events[:limit]
	}
	return events, nil
}

func (s *eventStore) ListRecentEvents(ctx context.Context, workspaceID string, beforeNID int64, limit int) ([]Event, error) {
	if err := requireEventWorkspace(workspaceID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > MaxEventListLimit {
		limit = MaxEventListLimit
	}
	parts, err := s.ListEventPartitions(ctx)
	if err != nil {
		return nil, err
	}
	exec := s.db.WithoutTransaction()
	events := []Event{}
	for _, p := range parts {
		if !validEventPartitionTableName(p.TableName) {
			return nil, fmt.Errorf("%w: registry names invalid table %q", ErrInvalidEventParameter, p.TableName)
		}
		rows, err := exec.QueryContext(ctx, fmt.Sprintf(`
			SELECT %s FROM %s
			WHERE workspace_id = $1 AND ($2 = 0 OR nid < $2)
			ORDER BY nid DESC
			LIMIT $3
		`, eventColumns, p.TableName), workspaceID, beforeNID, limit)
		if err != nil {
			return nil, fmt.Errorf("event_log: list recent in %s: %w", p.TableName, err)
		}
		batch, err := scanEventRows(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, batch...)
	}
	sort.Slice(events, func(i, j int) bool { return events[i].NID > events[j].NID })
	if len(events) > limit {
		events = events[:limit]
	}
	return events, nil
}

func (s *eventStore) queryEventRange(ctx context.Context, from, to time.Time, limit int, where string, args ...any) ([]Event, error) {
	if limit <= 0 || limit > MaxEventListLimit {
		limit = MaxEventListLimit
	}
	parts, err := s.eventPartitionsInRange(ctx, from, to)
	if err != nil {
		return nil, err
	}
	exec := s.db.WithoutTransaction()
	events := []Event{}
	for _, p := range parts {
		if !validEventPartitionTableName(p.TableName) {
			return nil, fmt.Errorf("%w: registry names invalid table %q", ErrInvalidEventParameter, p.TableName)
		}
		query := fmt.Sprintf(`SELECT %s FROM %s WHERE %s ORDER BY nid DESC LIMIT %d`,
			eventColumns, p.TableName, where, limit)
		rows, err := exec.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("event_log: query %s: %w", p.TableName, err)
		}
		batch, err := scanEventRows(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, batch...)
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].Time.Equal(events[j].Time) {
			return events[i].NID > events[j].NID
		}
		return events[i].Time.After(events[j].Time)
	})
	if len(events) > limit {
		events = events[:limit]
	}
	return events, nil
}

func (s *eventStore) GetEventsByType(ctx context.Context, workspaceID, eventType string, from, to time.Time, limit int) ([]Event, error) {
	if err := requireEventWorkspace(workspaceID); err != nil {
		return nil, err
	}
	return s.queryEventRange(ctx, from, to, limit,
		`workspace_id = $1 AND type = $2 AND time BETWEEN $3 AND $4`,
		workspaceID, eventType, from, to)
}

func (s *eventStore) GetEventsBySource(ctx context.Context, workspaceID, eventType string, from, to time.Time, eventSource string, limit int) ([]Event, error) {
	if err := requireEventWorkspace(workspaceID); err != nil {
		return nil, err
	}
	return s.queryEventRange(ctx, from, to, limit,
		`workspace_id = $1 AND type = $2 AND source = $3 AND time BETWEEN $4 AND $5`,
		workspaceID, eventType, eventSource, from, to)
}

func (s *eventStore) GetEventsBySubject(ctx context.Context, workspaceID, eventType string, from, to time.Time, subject string, limit int) ([]Event, error) {
	if err := requireEventWorkspace(workspaceID); err != nil {
		return nil, err
	}
	return s.queryEventRange(ctx, from, to, limit,
		`workspace_id = $1 AND type = $2 AND subject = $3 AND time BETWEEN $4 AND $5`,
		workspaceID, eventType, subject, from, to)
}

func (s *eventStore) GetEventTypesInRange(ctx context.Context, workspaceID string, from, to time.Time, limit int) ([]string, error) {
	if err := requireEventWorkspace(workspaceID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > MaxEventListLimit {
		limit = MaxEventListLimit
	}
	parts, err := s.eventPartitionsInRange(ctx, from, to)
	if err != nil {
		return nil, err
	}
	exec := s.db.WithoutTransaction()
	seen := map[string]bool{}
	for _, p := range parts {
		if !validEventPartitionTableName(p.TableName) {
			return nil, fmt.Errorf("%w: registry names invalid table %q", ErrInvalidEventParameter, p.TableName)
		}
		rows, err := exec.QueryContext(ctx, fmt.Sprintf(`
			SELECT DISTINCT type FROM %s
			WHERE workspace_id = $1 AND time BETWEEN $2 AND $3
		`, p.TableName), workspaceID, from, to)
		if err != nil {
			return nil, fmt.Errorf("event_log: types in %s: %w", p.TableName, err)
		}
		for rows.Next() {
			var et string
			if err := rows.Scan(&et); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("event_log: scan type: %w", err)
			}
			seen[et] = true
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	types := make([]string, 0, len(seen))
	for et := range seen {
		types = append(types, et)
	}
	sort.Strings(types)
	if len(types) > limit {
		types = types[:limit]
	}
	return types, nil
}

func (s *eventStore) DeleteEventsByTypeInRange(ctx context.Context, workspaceID, eventType string, from, to time.Time) error {
	if err := requireEventWorkspace(workspaceID); err != nil {
		return err
	}
	if from.After(to) {
		return fmt.Errorf("%w: invalid time range: from %v is after to %v", ErrInvalidEventParameter, from, to)
	}
	parts, err := s.eventPartitionsInRange(ctx, from, to)
	if err != nil {
		return err
	}
	exec := s.db.WithoutTransaction()
	for _, p := range parts {
		if !validEventPartitionTableName(p.TableName) {
			return fmt.Errorf("%w: registry names invalid table %q", ErrInvalidEventParameter, p.TableName)
		}
		if _, err := exec.ExecContext(ctx, fmt.Sprintf(`
			DELETE FROM %s
			WHERE workspace_id = $1 AND type = $2 AND time BETWEEN $3 AND $4
		`, p.TableName), workspaceID, eventType, from, to); err != nil {
			return fmt.Errorf("event_log: delete %q in %s: %w", eventType, p.TableName, err)
		}
	}
	return nil
}

func (s *eventStore) PruneEventPartitionsBefore(ctx context.Context, t time.Time) ([]string, error) {
	cutoff := EventPeriodKey(t)
	parts, err := s.ListEventPartitions(ctx)
	if err != nil {
		return nil, err
	}
	dropped := []string{}
	for _, p := range parts {
		if p.Period >= cutoff {
			continue
		}
		if !validEventPartitionTableName(p.TableName) {
			return dropped, fmt.Errorf("%w: registry names invalid table %q", ErrInvalidEventParameter, p.TableName)
		}
		exec, commit, release, err := s.db.WithTransaction(ctx)
		if err != nil {
			return dropped, fmt.Errorf("event_log: prune tx: %w", err)
		}
		if _, err := exec.ExecContext(ctx, `DELETE FROM event_partitions WHERE period = $1`, p.Period); err != nil {
			_ = release()
			return dropped, fmt.Errorf("event_log: deregister partition %s: %w", p.Period, err)
		}
		if _, err := exec.ExecContext(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS %s`, p.TableName)); err != nil {
			_ = release()
			return dropped, fmt.Errorf("event_log: drop partition %s: %w", p.TableName, err)
		}
		if err := commit(ctx); err != nil {
			_ = release()
			return dropped, fmt.Errorf("event_log: prune commit %s: %w", p.Period, err)
		}
		_ = release()
		dropped = append(dropped, p.Period)
	}
	return dropped, nil
}

func execCreateIfNotExists(ctx context.Context, exec libdb.Exec, stmt string) error {
	_, err := exec.ExecContext(ctx, stmt)
	if err == nil {
		return nil
	}
	if errors.Is(err, libdb.ErrUniqueViolation) || strings.Contains(err.Error(), "already exists") {
		return nil
	}
	return err
}

func requireEventWorkspace(workspaceID string) error {
	if strings.TrimSpace(workspaceID) == "" {
		return fmt.Errorf("%w: workspace_id is required", ErrEventMissingRequiredField)
	}
	return nil
}

func scanEventRows(rows *sql.Rows) ([]Event, error) {
	defer rows.Close()
	events := []Event{}
	for rows.Next() {
		var e Event
		var data string
		if err := rows.Scan(&e.NID, &e.WorkspaceID, &e.Type, &e.Source, &e.Subject, &e.Time, &data, &e.Hop); err != nil {
			return nil, fmt.Errorf("event_log: scan row: %w", err)
		}
		e.Data = json.RawMessage(data)
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("event_log: rows error: %w", err)
	}
	return events, nil
}
