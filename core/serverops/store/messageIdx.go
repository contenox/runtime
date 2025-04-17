package store

import (
	"context"
	"fmt"
)

func (s *store) CreateMessageIndex(ctx context.Context, id string, identity string) error {
	if _, err := s.Exec.ExecContext(ctx, `
		INSERT INTO message_indices(id, identity)
		VALUES ($1, $2)`,
		id,
		identity,
	); err != nil {
		return fmt.Errorf("failed to create message index: %w", err)
	}
	return nil
}

func (s *store) DeleteMessageIndex(ctx context.Context, id string, identity string) error {
	if _, err := s.Exec.ExecContext(ctx, `
		DELETE FROM message_indices
		WHERE id = $1 AND identity = $2`,
		id,
		identity,
	); err != nil {
		return fmt.Errorf("failed to delete message index: %w", err)
	}

	return nil
}

func (s *store) ListMessageStreams(ctx context.Context, identity string) ([]string, error) {
	rows, err := s.Exec.QueryContext(ctx, `
		SELECT id
		FROM message_indices
		WHERE identity = $1`,
		identity,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query message indices: %w", err)
	}
	defer rows.Close()

	streams := []string{}
	for rows.Next() {
		var stream string
		if err := rows.Scan(
			&stream,
		); err != nil {
			return nil, fmt.Errorf("failed to scan message indices: %w", err)
		}
		streams = append(streams, stream)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return streams, nil
}

func (s *store) ListMessageIndices(ctx context.Context, identity string) ([]string, error) {
	rows, err := s.Exec.QueryContext(ctx, `
		SELECT id
		FROM message_indices
		WHERE identity = $1`,
		identity,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query message indices: %w", err)
	}
	defer rows.Close()

	streams := []string{}
	for rows.Next() {
		var stream string
		if err := rows.Scan(
			&stream,
		); err != nil {
			return nil, fmt.Errorf("failed to scan message indices: %w", err)
		}
		streams = append(streams, stream)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return streams, nil
}
