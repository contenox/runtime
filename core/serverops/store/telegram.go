package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	libdb "github.com/contenox/dbexec"
	"github.com/google/uuid"
)

func (s *store) CreateTelegramFrontend(ctx context.Context, frontend *TelegramFrontend) error {
	now := time.Now().UTC()
	frontend.CreatedAt = now
	frontend.UpdatedAt = now
	frontend.ID = uuid.NewString()
	_, err := s.Exec.ExecContext(ctx, `
        INSERT INTO telegram_frontends
        (id, user_id, chat_chain, description, bot_token, sync_interval, status, last_offset, last_heartbeat, last_error, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		frontend.ID,
		frontend.UserID,
		frontend.ChatChain,
		frontend.Description,
		frontend.BotToken,
		frontend.SyncInterval,
		frontend.Status,
		frontend.LastOffset,
		frontend.LastHeartbeat,
		frontend.LastError,
		frontend.CreatedAt,
		frontend.UpdatedAt,
	)
	return err
}

func (s *store) GetTelegramFrontend(ctx context.Context, id string) (*TelegramFrontend, error) {
	var frontend TelegramFrontend
	err := s.Exec.QueryRowContext(ctx, `
        SELECT id, user_id, chat_chain, description, bot_token, sync_interval, status, last_offset, last_heartbeat, last_error, created_at, updated_at
        FROM telegram_frontends
        WHERE id = $1`,
		id,
	).Scan(
		&frontend.ID,
		&frontend.UserID,
		&frontend.ChatChain,
		&frontend.Description,
		&frontend.BotToken,
		&frontend.SyncInterval,
		&frontend.Status,
		&frontend.LastOffset,
		&frontend.LastHeartbeat,
		&frontend.LastError,
		&frontend.CreatedAt,
		&frontend.UpdatedAt,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, libdb.ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	return &frontend, nil
}

func (s *store) UpdateTelegramFrontend(ctx context.Context, frontend *TelegramFrontend) error {
	now := time.Now().UTC()
	frontend.UpdatedAt = now

	result, err := s.Exec.ExecContext(ctx, `
        UPDATE telegram_frontends
        SET user_id = $2, chat_chain = $3, description = $4, bot_token = $5, sync_interval = $6, status = $7, last_offset = $8, last_heartbeat = $9, last_error = $10, updated_at = $11
        WHERE id = $1`,
		frontend.ID,
		frontend.UserID,
		frontend.ChatChain,
		frontend.Description,
		frontend.BotToken,
		frontend.SyncInterval,
		frontend.Status,
		frontend.LastOffset,
		frontend.LastHeartbeat,
		frontend.LastError,
		frontend.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to update telegram frontend: %w", err)
	}

	return checkRowsAffected(result)
}

func (s *store) DeleteTelegramFrontend(ctx context.Context, id string) error {
	result, err := s.Exec.ExecContext(ctx, `
        DELETE FROM telegram_frontends
        WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("failed to delete telegram frontend: %w", err)
	}

	return checkRowsAffected(result)
}

func (s *store) ListTelegramFrontends(ctx context.Context) ([]*TelegramFrontend, error) {
	rows, err := s.Exec.QueryContext(ctx, `
        SELECT id, user_id, chat_chain, description, bot_token, sync_interval, status, last_offset, last_heartbeat, last_error, created_at, updated_at
        FROM telegram_frontends
        ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query telegram frontends: %w", err)
	}
	defer rows.Close()

	var frontends []*TelegramFrontend
	for rows.Next() {
		var frontend TelegramFrontend
		if err := rows.Scan(
			&frontend.ID,
			&frontend.UserID,
			&frontend.ChatChain,
			&frontend.Description,
			&frontend.BotToken,
			&frontend.SyncInterval,
			&frontend.Status,
			&frontend.LastOffset,
			&frontend.LastHeartbeat,
			&frontend.LastError,
			&frontend.CreatedAt,
			&frontend.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan telegram frontend: %w", err)
		}
		frontends = append(frontends, &frontend)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return frontends, nil
}

func (s *store) ListTelegramFrontendsByUser(ctx context.Context, userID string) ([]*TelegramFrontend, error) {
	rows, err := s.Exec.QueryContext(ctx, `
        SELECT id, user_id, chat_chain, description, bot_token, sync_interval, status, last_offset, last_heartbeat, last_error, created_at, updated_at
        FROM telegram_frontends
        WHERE user_id = $1
        ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query telegram frontends by user: %w", err)
	}
	defer rows.Close()

	var frontends []*TelegramFrontend
	for rows.Next() {
		var frontend TelegramFrontend
		if err := rows.Scan(
			&frontend.ID,
			&frontend.UserID,
			&frontend.ChatChain,
			&frontend.Description,
			&frontend.BotToken,
			&frontend.SyncInterval,
			&frontend.Status,
			&frontend.LastOffset,
			&frontend.LastHeartbeat,
			&frontend.LastError,
			&frontend.CreatedAt,
			&frontend.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan telegram frontend: %w", err)
		}
		frontends = append(frontends, &frontend)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return frontends, nil
}
