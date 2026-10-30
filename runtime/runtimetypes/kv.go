package runtimetypes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
)

func (s *store) SetKV(ctx context.Context, key string, value json.RawMessage) error {
	return s.setKVScoped(ctx, "", key, value)
}

func (s *store) SetWorkspaceKV(ctx context.Context, workspaceID string, key string, value json.RawMessage) error {
	return s.setKVScoped(ctx, workspaceID, key, value)
}

func (s *store) setKVScoped(ctx context.Context, workspaceID string, key string, value json.RawMessage) error {
	now := time.Now().UTC()
	_, err := s.Exec.ExecContext(ctx, `
		INSERT INTO kv (key, workspace_id, value, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (key, workspace_id) DO UPDATE
		SET value = $3, updated_at = $5`,
		key, workspaceID, value, now, now,
	)
	return err
}

func (s *store) UpdateKV(ctx context.Context, key string, value json.RawMessage) error {
	now := time.Now().UTC()
	result, err := s.Exec.ExecContext(ctx, `
        UPDATE kv
        SET value = $2, updated_at = $3
        WHERE key = $1 AND workspace_id = ''`,
		key, value, now,
	)
	if err != nil {
		return fmt.Errorf("failed to update key-value pair: %w", err)
	}
	return checkRowsAffected(result)
}

func (s *store) GetKV(ctx context.Context, key string, out interface{}) error {
	return s.getKVScoped(ctx, "", key, out)
}

func (s *store) GetWorkspaceKV(ctx context.Context, workspaceID string, key string, out interface{}) error {
	return s.getKVScoped(ctx, workspaceID, key, out)
}

func (s *store) getKVScoped(ctx context.Context, workspaceID string, key string, out interface{}) error {
	var kv KV
	var rawValue []byte
	err := s.Exec.QueryRowContext(ctx, `
		SELECT key, value, created_at, updated_at
		FROM kv
		WHERE key = $1 AND workspace_id = $2`,
		key, workspaceID,
	).Scan(&kv.Key, &rawValue, &kv.CreatedAt, &kv.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return libdb.ErrNotFound
	}
	if err != nil {
		return err
	}
	kv.Value = json.RawMessage(rawValue)
	return json.Unmarshal(kv.Value, out)
}

func (s *store) DeleteKV(ctx context.Context, key string) error {
	return s.deleteKVScoped(ctx, "", key)
}

func (s *store) DeleteWorkspaceKV(ctx context.Context, workspaceID string, key string) error {
	return s.deleteKVScoped(ctx, workspaceID, key)
}

func (s *store) deleteKVScoped(ctx context.Context, workspaceID string, key string) error {
	result, err := s.Exec.ExecContext(ctx, `
		DELETE FROM kv
		WHERE key = $1 AND workspace_id = $2`,
		key, workspaceID,
	)
	if err != nil {
		return fmt.Errorf("failed to delete key-value pair: %w", err)
	}
	return checkRowsAffected(result)
}

func (s *store) ListKV(ctx context.Context, createdAtCursor *time.Time, limit int, afterID ...string) ([]*KV, error) {
	if limit > MAXLIMIT {
		return nil, ErrLimitParamExceeded
	}
	cond, args := keysetBefore("created_at", "key", createdAtCursor, afterID, 1)
	args = append(args, limit)
	rows, err := s.Exec.QueryContext(ctx, `
        SELECT key, value, created_at, updated_at
        FROM kv`+whereAnd("workspace_id = ''", cond)+`
        ORDER BY created_at DESC, key DESC
        LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query key-value pairs: %w", err)
	}
	defer rows.Close()

	var kvs []*KV
	for rows.Next() {
		var kv KV
		var rawValue []byte
		if err := rows.Scan(&kv.Key, &rawValue, &kv.CreatedAt, &kv.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan key-value pair: %w", err)
		}
		kv.Value = json.RawMessage(rawValue)
		kvs = append(kvs, &kv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}
	return kvs, nil
}

func (s *store) ListKVPrefix(ctx context.Context, prefix string, createdAtCursor *time.Time, limit int, afterID ...string) ([]*KV, error) {
	cond, cargs := keysetBefore("created_at", "key", createdAtCursor, afterID, 2)
	args := append([]any{prefix}, cargs...)
	args = append(args, limit)
	rows, err := s.Exec.QueryContext(ctx, `
        SELECT key, value, created_at, updated_at
        FROM kv`+whereAnd("workspace_id = ''", "key LIKE $1 || '%'", cond)+`
        ORDER BY created_at DESC, key DESC
        LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query key-value pairs with prefix: %w", err)
	}
	defer rows.Close()

	var kvs []*KV
	for rows.Next() {
		var kv KV
		var rawValue []byte
		if err := rows.Scan(&kv.Key, &rawValue, &kv.CreatedAt, &kv.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan key-value pair: %w", err)
		}
		kv.Value = json.RawMessage(rawValue)
		kvs = append(kvs, &kv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}
	return kvs, nil
}

func (s *store) EstimateKVCount(ctx context.Context) (int64, error) {
	return s.estimateCount(ctx, "kv")
}
