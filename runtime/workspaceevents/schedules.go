package workspaceevents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

type Schedule struct {
	CreatedBy string          `json:"createdBy,omitempty"`
	ID        string          `json:"id"`
	Topic     string          `json:"topic"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	At        *time.Time      `json:"at,omitempty"`
	Every     string          `json:"every,omitempty"`
	Cron      string          `json:"cron,omitempty"`
	Timezone  string          `json:"timezone,omitempty"`
	Next      time.Time       `json:"next"`
	Enabled   bool            `json:"enabled"`
}

var ErrSchedule = errors.New("invalid schedule")

type Schedules struct{ DB libdb.DBManager }

func ParseScheduleInterval(value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil || d < time.Second {
		return 0, fmt.Errorf("%w: use an interval of at least one second, such as 30s, 15m, 1h30m or 24h", ErrSchedule)
	}
	return d, nil
}

func (s Schedule) next(now time.Time) (time.Time, error) {
	modes := 0
	if s.At != nil {
		modes++
	}
	if s.Every != "" {
		modes++
	}
	if s.Cron != "" {
		modes++
	}
	if modes != 1 {
		return time.Time{}, fmt.Errorf("%w: choose one of at, every or cron", ErrSchedule)
	}
	if s.At != nil {
		return s.At.UTC(), nil
	}
	if s.Every != "" {
		d, err := ParseScheduleInterval(s.Every)
		if err != nil {
			return time.Time{}, err
		}
		return now.Add(d), nil
	}
	zone := s.Timezone
	if zone == "" {
		zone = "UTC"
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: unknown timezone %q", ErrSchedule, zone)
	}
	if len(strings.Fields(s.Cron)) != 5 {
		return time.Time{}, fmt.Errorf("%w: use a five-field cron expression", ErrSchedule)
	}
	expr, err := cron.ParseStandard(s.Cron)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %v", ErrSchedule, err)
	}
	next := expr.Next(now.In(loc))
	if next.IsZero() {
		return next, fmt.Errorf("%w: cron has no upcoming occurrence", ErrSchedule)
	}
	return next.UTC(), nil
}
func (s Schedules) Create(ctx context.Context, scope string, req Schedule, now time.Time) (Schedule, error) {
	if !topicName.MatchString(req.Topic) || len(req.Topic) > MaxSubjectBytes {
		return Schedule{}, fmt.Errorf("%w: invalid topic", ErrSchedule)
	}
	data, err := normalizeData(req.Payload)
	if err != nil {
		return Schedule{}, fmt.Errorf("%w: %v", ErrSchedule, err)
	}
	next, err := req.next(now)
	if err != nil {
		return Schedule{}, err
	}
	if !next.After(now) {
		return Schedule{}, fmt.Errorf("%w: choose a future time", ErrSchedule)
	}
	req.ID = uuid.NewString()
	req.Payload = data
	req.Enabled = true
	req.Next = next.UTC().Truncate(time.Millisecond)
	raw, err := json.Marshal(req)
	if err != nil {
		return Schedule{}, err
	}
	_, err = s.DB.WithoutTransaction().ExecContext(ctx, "INSERT INTO workspace_schedules(id,scope,definition,due_ms,enabled) VALUES($1,$2,$3,$4,$5)", req.ID, scope, string(raw), req.Next.UnixMilli(), true)
	return req, err
}
func (s Schedules) rows(ctx context.Context, scope string, due *time.Time, cursor string, limit int) ([]Schedule, error) {
	query := "SELECT definition,due_ms,enabled FROM workspace_schedules WHERE scope=$1"
	args := []any{scope}
	if due != nil {
		query += " AND enabled AND due_ms<=$2"
		args = append(args, due.UnixMilli())
	}
	if limit > 0 {
		query += " AND id>$2 ORDER BY id LIMIT $3"
		args = append(args, cursor, limit)
	} else {
		query += " ORDER BY due_ms,id"
	}
	if due != nil {
		query += " LIMIT 100"
	}
	rows, err := s.DB.WithoutTransaction().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Schedule{}
	for rows.Next() {
		var raw string
		var ms int64
		var enabled bool
		if err := rows.Scan(&raw, &ms, &enabled); err != nil {
			return nil, err
		}
		var row Schedule
		if err := json.Unmarshal([]byte(raw), &row); err != nil {
			return nil, err
		}
		row.Next = time.UnixMilli(ms).UTC()
		row.Enabled = enabled
		out = append(out, row)
	}
	return out, rows.Err()
}
func (s Schedules) List(ctx context.Context, scope string) ([]Schedule, error) {
	return s.rows(ctx, scope, nil, "", 0)
}
func (s Schedules) Change(ctx context.Context, scope, id string, enabled *bool) error {
	query := "DELETE FROM workspace_schedules WHERE scope=$1 AND id=$2"
	args := []any{scope, id}
	if enabled != nil {
		query = "UPDATE workspace_schedules SET enabled=$3 WHERE scope=$1 AND id=$2"
		args = append(args, *enabled)
	}
	result, err := s.DB.WithoutTransaction().ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return fmt.Errorf("%w: schedule not found in project", ErrSchedule)
	}
	return err
}

func (s Schedules) Drain(ctx context.Context, scope string, now time.Time) ([]Event, error) {
	due, err := s.rows(ctx, scope, &now, "", 0)
	if err != nil || len(due) == 0 {
		return nil, err
	}
	if err = runtimetypes.NewEventStore(s.DB).EnsureEventPartitionExists(ctx, now); err != nil {
		return nil, err
	}
	var out []Event
	for _, schedule := range due {
		e, ok, err := s.fire(ctx, scope, schedule, now)
		if err != nil {
			return out, err
		}
		if ok {
			out = append(out, e)
		}
	}
	return out, nil
}
func (s Schedules) fire(ctx context.Context, scope string, schedule Schedule, now time.Time) (Event, bool, error) {
	next, err := schedule.next(now)
	if err != nil {
		return Event{}, false, err
	}
	tx, commit, release, err := s.DB.WithTransaction(ctx)
	if err != nil {
		return Event{}, false, err
	}
	defer func() { _ = release() }()
	query := "UPDATE workspace_schedules SET due_ms=$4 WHERE scope=$1 AND id=$2 AND enabled AND due_ms=$3"
	args := []any{scope, schedule.ID, schedule.Next.UnixMilli()}
	if schedule.At != nil {
		query = "DELETE FROM workspace_schedules WHERE scope=$1 AND id=$2 AND enabled AND due_ms=$3"
	} else {
		args = append(args, next.UnixMilli())
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return Event{}, false, err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return Event{}, false, err
	}
	raw, err := json.Marshal(topicRecord{Payload: schedule.Payload})
	if err != nil {
		return Event{}, false, err
	}
	e := runtimetypes.Event{WorkspaceID: scope, Type: TypeEventFired, Source: SourceSchedule, Subject: schedule.Topic, Data: raw, Time: now}
	if err = runtimetypes.AppendEventInTransaction(ctx, tx, &e); err != nil {
		return Event{}, false, err
	}
	if err = commit(ctx); err != nil {
		return Event{}, false, err
	}
	return fromStored(e), true, nil
}

func (s Schedules) Page(ctx context.Context, scope, cursor string, limit int) ([]Schedule, string, error) {
	rows, err := s.rows(ctx, scope, nil, cursor, limit+1)
	if err != nil {
		return nil, "", err
	}
	if len(rows) > limit {
		return rows[:limit], rows[limit-1].ID, nil
	}
	return rows, "", nil
}
