package runtimetypes

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
)

func (s *store) CreateBackend(ctx context.Context, backend *Backend) error {
	now := time.Now().UTC()
	backend.CreatedAt = now
	backend.UpdatedAt = now
	if backend.ID == "" {
		backend.ID = uuid.New().String()
	}
	_, err := s.Exec.ExecContext(ctx, `
		INSERT INTO llm_backends
		(id, name, base_url, type, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		backend.ID,
		backend.Name,
		backend.BaseURL,
		backend.Type,
		backend.CreatedAt,
		backend.UpdatedAt,
	)
	return err
}

func (s *store) CountBackendsByTypeAndURL(ctx context.Context, typ, baseURL, excludeID string) (int, error) {
	var count int
	err := s.Exec.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM llm_backends
		WHERE type = $1 AND base_url = $2 AND ($3 = '' OR id <> $3)`,
		typ, baseURL, excludeID,
	).Scan(&count)
	return count, err
}

func (s *store) GetBackend(ctx context.Context, id string) (*Backend, error) {
	var backend Backend
	err := s.Exec.QueryRowContext(ctx, `
		SELECT id, name, base_url, type, created_at, updated_at
		FROM llm_backends
		WHERE id = $1`,
		id,
	).Scan(
		&backend.ID,
		&backend.Name,
		&backend.BaseURL,
		&backend.Type,
		&backend.CreatedAt,
		&backend.UpdatedAt,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, libdb.ErrNotFound
	}
	return &backend, err
}

func (s *store) UpdateBackend(ctx context.Context, backend *Backend) error {
	backend.UpdatedAt = time.Now().UTC()

	result, err := s.Exec.ExecContext(ctx, `
		UPDATE llm_backends
		SET name = $2,
			base_url = $3,
			type = $4,
			updated_at = $5
		WHERE id = $1`,
		backend.ID,
		backend.Name,
		backend.BaseURL,
		backend.Type,
		backend.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to update backend: %w", err)
	}

	return checkRowsAffected(result)
}

func (s *store) DeleteBackend(ctx context.Context, id string) error {
	result, err := s.Exec.ExecContext(ctx, `
		DELETE FROM llm_backends
		WHERE id = $1`,
		id,
	)

	if err != nil {
		return fmt.Errorf("failed to delete backend: %w", err)
	}

	return checkRowsAffected(result)
}

func (s *store) ListAllBackends(ctx context.Context) ([]*Backend, error) {
	rows, err := s.Exec.QueryContext(ctx, `
        SELECT id, name, base_url, type, created_at, updated_at
        FROM llm_backends
        ORDER BY created_at DESC, id DESC;
    `)
	if err != nil {
		return nil, fmt.Errorf("failed to query backends: %w", err)
	}
	defer rows.Close()

	backends := []*Backend{}
	for rows.Next() {
		var backend Backend
		if err := rows.Scan(
			&backend.ID,
			&backend.Name,
			&backend.BaseURL,
			&backend.Type,
			&backend.CreatedAt,
			&backend.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan backend: %w", err)
		}
		backends = append(backends, &backend)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return backends, nil
}

func (s *store) ListBackends(ctx context.Context, createdAtCursor *time.Time, limit int) ([]*Backend, error) {
	if limit > MAXLIMIT {
		return nil, ErrLimitParamExceeded
	}
	// The first page must not filter by "created before now": a backend created
	// in the same instant as the listing would compare equal and disappear.
	const columns = `SELECT id, name, base_url, type, created_at, updated_at
        FROM llm_backends`
	const order = ` ORDER BY created_at DESC, id DESC`
	query := columns + order + ` LIMIT $1`
	args := []any{limit}
	if createdAtCursor != nil {
		query = columns + ` WHERE created_at < $1` + order + ` LIMIT $2`
		args = []any{*createdAtCursor, limit}
	}
	rows, err := s.Exec.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query backends: %w", err)
	}
	defer rows.Close()

	backends := []*Backend{}
	for rows.Next() {
		var backend Backend
		if err := rows.Scan(
			&backend.ID,
			&backend.Name,
			&backend.BaseURL,
			&backend.Type,
			&backend.CreatedAt,
			&backend.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan backend: %w", err)
		}
		backends = append(backends, &backend)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return backends, nil
}

func (s *store) GetBackendByName(ctx context.Context, name string) (*Backend, error) {
	var backend Backend
	err := s.Exec.QueryRowContext(ctx, `
		SELECT id, name, base_url, type, created_at, updated_at
		FROM llm_backends
		WHERE name = $1`,
		name,
	).Scan(
		&backend.ID,
		&backend.Name,
		&backend.BaseURL,
		&backend.Type,
		&backend.CreatedAt,
		&backend.UpdatedAt,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, libdb.ErrNotFound
	}
	return &backend, err
}

func checkRowsAffected(result sql.Result) error {
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return libdb.ErrNotFound
	}
	return nil
}

func (s *store) EstimateBackendCount(ctx context.Context) (int64, error) {
	return s.estimateCount(ctx, "llm_backends")
}

func (s *store) LockBackend(ctx context.Context, id string) error {
	result, err := s.Exec.ExecContext(ctx, `UPDATE llm_backends SET name = name WHERE id = $1`, id)
	if err != nil {
		return err
	}
	return checkRowsAffected(result)
}
