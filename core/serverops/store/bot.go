package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/contenox/runtime-mvp/libs/libdb"
)

func (s *store) CreateBot(ctx context.Context, bot *Bot) error {
	now := time.Now().UTC()
	bot.CreatedAt = now
	bot.UpdatedAt = now

	_, err := s.Exec.ExecContext(ctx, `
        INSERT INTO bots
        (id, name, user_id, bot_type, state, job_type, task_chain_id, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		bot.ID,
		bot.Name,
		bot.UserID,
		bot.BotType,
		bot.State,
		bot.JobType,
		bot.TaskChainID,
		bot.CreatedAt,
		bot.UpdatedAt,
	)
	return err
}

func (s *store) GetBot(ctx context.Context, id string) (*Bot, error) {
	var bot Bot
	err := s.Exec.QueryRowContext(ctx, `
        SELECT id, name, user_id, bot_type, state, job_type, task_chain_id, created_at, updated_at
        FROM bots
        WHERE id = $1`,
		id,
	).Scan(
		&bot.ID,
		&bot.Name,
		&bot.UserID,
		&bot.BotType,
		&bot.State,
		&bot.JobType,
		&bot.TaskChainID,
		&bot.CreatedAt,
		&bot.UpdatedAt,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, libdb.ErrNotFound
	}
	return &bot, err
}

func (s *store) GetBotByName(ctx context.Context, name string) (*Bot, error) {
	var bot Bot
	err := s.Exec.QueryRowContext(ctx, `
        SELECT id, name, user_id, bot_type, state, job_type, task_chain_id, created_at, updated_at
        FROM bots
        WHERE name = $1`,
		name,
	).Scan(
		&bot.ID,
		&bot.Name,
		&bot.UserID,
		&bot.BotType,
		&bot.State,
		&bot.JobType,
		&bot.TaskChainID,
		&bot.CreatedAt,
		&bot.UpdatedAt,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, libdb.ErrNotFound
	}
	return &bot, err
}

func (s *store) UpdateBot(ctx context.Context, bot *Bot) error {
	now := time.Now().UTC()
	bot.UpdatedAt = now

	_, err := s.Exec.ExecContext(ctx, `
        UPDATE bots
        SET name = $1, user_id = $2, bot_type = $3, state = $4, job_type = $5, task_chain_id = $6, updated_at = $7
        WHERE id = $8`,
		bot.Name,
		bot.UserID,
		bot.BotType,
		bot.State,
		bot.JobType,
		bot.TaskChainID,
		bot.UpdatedAt,
		bot.ID,
	)
	return err
}

func (s *store) DeleteBot(ctx context.Context, id string) error {
	result, err := s.Exec.ExecContext(ctx, `
        DELETE FROM bots
        WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("failed to delete bot: %w", err)
	}
	return checkRowsAffected(result)
}

func (s *store) ListBots(ctx context.Context) ([]*Bot, error) {
	rows, err := s.Exec.QueryContext(ctx, `
        SELECT id, name, user_id, bot_type, state, job_type, task_chain_id, created_at, updated_at
        FROM bots
        ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query bots: %w", err)
	}
	defer rows.Close()

	var bots []*Bot
	for rows.Next() {
		var bot Bot
		if err := rows.Scan(
			&bot.ID,
			&bot.Name,
			&bot.UserID,
			&bot.BotType,
			&bot.State,
			&bot.JobType,
			&bot.TaskChainID,
			&bot.CreatedAt,
			&bot.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan bot: %w", err)
		}
		bots = append(bots, &bot)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return bots, nil
}

func (s *store) ListBotsByUser(ctx context.Context, userID string) ([]*Bot, error) {
	rows, err := s.Exec.QueryContext(ctx, `
        SELECT id, name, user_id, bot_type, state, job_type, task_chain_id, created_at, updated_at
        FROM bots
        WHERE user_id = $1
        ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query bots by user: %w", err)
	}
	defer rows.Close()

	var bots []*Bot
	for rows.Next() {
		var bot Bot
		if err := rows.Scan(
			&bot.ID,
			&bot.Name,
			&bot.UserID,
			&bot.BotType,
			&bot.State,
			&bot.JobType,
			&bot.TaskChainID,
			&bot.CreatedAt,
			&bot.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan bot: %w", err)
		}
		bots = append(bots, &bot)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return bots, nil
}
