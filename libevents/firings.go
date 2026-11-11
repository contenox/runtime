package libevents

import (
	"context"
	"fmt"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
)

const (
	FiringStatusRunning = "running"
	FiringStatusOK      = "ok"
	FiringStatusError   = "error"
	FiringStatusRefused = "refused"
)

const (
	DefaultFiringLimit = 50
	MaxFiringLimit     = 1000
)

type Firing struct {
	Scope       string
	TriggerName string
	NID         int64
	Status      string
	Error       string
	RequestID   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (f Firing) Stranded(now time.Time, staleClaim time.Duration) bool {
	return f.Status == FiringStatusRunning && now.UTC().Sub(f.UpdatedAt) > staleClaim
}

type FiringFilter struct {
	SinceNID int64

	Status string

	TriggerName string

	Limit int
}

type FiringStore struct {
	cfg        Config
	scope      string
	staleClaim time.Duration
	now        func() time.Time
}

type FiringStoreOption func(*FiringStore)

func WithClock(now func() time.Time) FiringStoreOption {
	return func(s *FiringStore) {
		if now != nil {
			s.now = now
		}
	}
}

func NewFiringStore(cfg Config, scope string, staleClaim time.Duration, opts ...FiringStoreOption) (*FiringStore, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if staleClaim <= 0 {
		return nil, fmt.Errorf("libevents: stale-claim bound must be positive, got %v", staleClaim)
	}
	s := &FiringStore{cfg: cfg, scope: scope, staleClaim: staleClaim, now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

func (s *FiringStore) StaleClaim() time.Duration { return s.staleClaim }

func (s *FiringStore) BeginFiring(ctx context.Context, exec libdb.Exec, triggerName string, nid int64, requestID string) (bool, error) {
	now := s.now().UTC()
	res, err := exec.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (%s, trigger_name, nid, status, error, request_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, '', $5, $6, $6)
		ON CONFLICT (%s, trigger_name, nid) DO NOTHING`,
		s.cfg.table("firings"), s.cfg.ScopeColumn, s.cfg.ScopeColumn),
		s.scope, triggerName, nid, FiringStatusRunning, requestID, now)
	if err != nil {
		return false, fmt.Errorf("libevents: begin firing %q/%d: %w", triggerName, nid, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 1 {
		return true, nil
	}
	return s.reclaimFiring(ctx, exec, triggerName, nid, requestID, now)
}

func (s *FiringStore) reclaimFiring(ctx context.Context, exec libdb.Exec, triggerName string, nid int64, requestID string, now time.Time) (bool, error) {
	res, err := exec.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s SET request_id = $4, error = '', updated_at = $5
		WHERE %s = $1 AND trigger_name = $2 AND nid = $3
		  AND status = $6 AND updated_at < $7`,
		s.cfg.table("firings"), s.cfg.ScopeColumn),
		s.scope, triggerName, nid, requestID, now, FiringStatusRunning, now.Add(-s.staleClaim))
	if err != nil {
		return false, fmt.Errorf("libevents: reclaim firing %q/%d: %w", triggerName, nid, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

func (s *FiringStore) FinishFiring(ctx context.Context, exec libdb.Exec, triggerName string, nid int64, status, errMsg string) error {
	_, err := exec.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s SET status = $4, error = $5, updated_at = $6
		WHERE %s = $1 AND trigger_name = $2 AND nid = $3`,
		s.cfg.table("firings"), s.cfg.ScopeColumn),
		s.scope, triggerName, nid, status, errMsg, s.now().UTC())
	if err != nil {
		return fmt.Errorf("libevents: finish firing %q/%d: %w", triggerName, nid, err)
	}
	return nil
}

func (s *FiringStore) ResetFiring(ctx context.Context, exec libdb.Exec, triggerName string, nid int64) (bool, error) {
	now := s.now().UTC()
	res, err := exec.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s SET status = $4, error = '', updated_at = $5
		WHERE %s = $1 AND trigger_name = $2 AND nid = $3
		  AND (status != $4 OR updated_at < $6)`,
		s.cfg.table("firings"), s.cfg.ScopeColumn),
		s.scope, triggerName, nid, FiringStatusRunning,
		now.Add(-s.staleClaim-time.Second), now.Add(-s.staleClaim))
	if err != nil {
		return false, fmt.Errorf("libevents: reset firing %q/%d: %w", triggerName, nid, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

func (s *FiringStore) ListFirings(ctx context.Context, exec libdb.Exec, f FiringFilter) ([]Firing, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultFiringLimit
	}
	if limit > MaxFiringLimit {
		limit = MaxFiringLimit
	}
	args := []any{s.scope}
	where := s.cfg.ScopeColumn + " = $1"
	if f.SinceNID > 0 {
		args = append(args, f.SinceNID)
		where += fmt.Sprintf(" AND nid > $%d", len(args))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		where += fmt.Sprintf(" AND status = $%d", len(args))
	}
	if f.TriggerName != "" {
		args = append(args, f.TriggerName)
		where += fmt.Sprintf(" AND trigger_name = $%d", len(args))
	}
	args = append(args, limit)
	rows, err := exec.QueryContext(ctx, fmt.Sprintf(`
		SELECT %s, trigger_name, nid, status, COALESCE(error, ''), request_id, created_at, updated_at
		FROM %s
		WHERE %s
		ORDER BY nid DESC, trigger_name
		LIMIT $%d`,
		s.cfg.ScopeColumn, s.cfg.table("firings"), where, len(args)), args...)
	if err != nil {
		return nil, fmt.Errorf("libevents: list firings: %w", err)
	}
	defer rows.Close()
	firings := []Firing{}
	for rows.Next() {
		var out Firing
		if err := rows.Scan(&out.Scope, &out.TriggerName, &out.NID, &out.Status, &out.Error, &out.RequestID, &out.CreatedAt, &out.UpdatedAt); err != nil {
			return nil, fmt.Errorf("libevents: scan firing: %w", err)
		}
		firings = append(firings, out)
	}
	return firings, rows.Err()
}
