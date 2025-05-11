package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func (s *store) AppendMessages(ctx context.Context, messages ...*Message) error {
	if len(messages) == 0 {
		return nil
	}

	now := time.Now().UTC()
	valueStrings := make([]string, 0, len(messages))
	valueArgs := make([]any, 0, len(messages)*4)

	for i, msg := range messages {
		msg.AddedAt = now
		valueStrings = append(valueStrings, fmt.Sprintf("($%d, $%d, $%d, $%d)", i*4+1, i*4+2, i*4+3, i*4+4))
		valueArgs = append(valueArgs, msg.ID, msg.IDX, msg.Payload, msg.AddedAt)
	}

	stmt := fmt.Sprintf(`
		INSERT INTO messages (id, idx_id, payload, added_at)
		VALUES %s`,
		// Join all placeholders
		strings.Join(valueStrings, ","),
	)

	_, err := s.Exec.ExecContext(ctx, stmt, valueArgs...)
	return err
}

func (s *store) DeleteMessages(ctx context.Context, stream string) error {
	result, err := s.Exec.ExecContext(ctx, `
		DELETE FROM messages
		WHERE idx_id = $1`,
		stream,
	)

	if err != nil {
		return fmt.Errorf("failed to delete messages: %w", err)
	}

	return checkRowsAffected(result)
}

func (s *store) ListMessages(ctx context.Context, stream string) ([]*Message, error) {
	rows, err := s.Exec.QueryContext(ctx, `
		SELECT id, idx_id, payload, added_at
		FROM messages
		WHERE idx_id = $1
		ORDER BY added_at ASC`,
		stream,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query messages: %w", err)
	}
	defer rows.Close()

	models := []*Message{}
	for rows.Next() {
		var model Message
		if err := rows.Scan(
			&model.ID,
			&model.IDX,
			&model.Payload,
			&model.AddedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan messages: %w", err)
		}
		models = append(models, &model)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return models, nil
}
