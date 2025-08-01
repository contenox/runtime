package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	libdb "github.com/contenox/dbexec"
)

func (s *store) ListFileIDsByParentID(ctx context.Context, parentID string) ([]string, error) {
	rows, err := s.Exec.QueryContext(ctx, `
        SELECT id
        FROM filestree
        WHERE parent_id = $1`,
		parentID)
	if err != nil {
		return nil, fmt.Errorf("failed to list ids: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}
	return ids, nil
}

func (s *store) ListFileIDsByName(ctx context.Context, parentID, name string) ([]string, error) {
	rows, err := s.Exec.QueryContext(ctx, `
			SELECT id
        FROM filestree
        WHERE name = $1 and parent_id = $2`,
		name, parentID)
	if err != nil {
		return nil, fmt.Errorf("failed to list ids: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}
	return ids, nil
}

func (s *store) GetFileParentID(ctx context.Context, id string) (string, error) {
	var parentID *string
	err := s.Exec.QueryRowContext(ctx, `
        SELECT parent_id
        FROM filestree
        WHERE id = $1`,
		id,
	).Scan(
		&parentID,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return "", libdb.ErrNotFound
	}
	if parentID == nil {
		return "", libdb.ErrNotFound
	}
	return *parentID, err
}

func (s *store) GetFileNameByID(ctx context.Context, id string) (string, error) {
	var name *string
	err := s.Exec.QueryRowContext(ctx, `
        SELECT name
        FROM filestree
        WHERE id = $1`,
		id,
	).Scan(
		&name,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return "", libdb.ErrNotFound
	}
	if name == nil {
		return "", libdb.ErrNotFound
	}
	return *name, err
}

func (s *store) CreateFileNameID(ctx context.Context, id, parentID, name string) error {
	now := time.Now().UTC()
	_, err := s.Exec.ExecContext(ctx, `
       INSERT INTO filestree
       (id, parent_id, name, created_at, updated_at)
       VALUES ($1, $2, $3, $4, $5)`,
		id,
		parentID,
		name,
		now,
		now,
	)

	return err
}

func (s *store) DeleteFileNameID(ctx context.Context, id string) error {
	result, err := s.Exec.ExecContext(ctx, `
        DELETE FROM filestree
        WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}
	return checkRowsAffected(result)
}

func (s *store) UpdateFileNameByID(ctx context.Context, id string, name string) error {
	updatedAt := time.Now().UTC()

	result, err := s.Exec.ExecContext(ctx, `
        UPDATE filestree
        SET name = $2,
            updated_at = $3
        WHERE id = $1`,
		id,
		name,
		updatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to update file: %w", err)
	}
	return checkRowsAffected(result)
}

func (s *store) UpdateFileParentID(ctx context.Context, id string, newParentID string) error {
	updatedAt := time.Now().UTC()

	result, err := s.Exec.ExecContext(ctx, `
		UPDATE filestree
		SET parent_id = $2,
			updated_at = $3
		WHERE id = $1`,
		id,
		newParentID,
		updatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to update file parent in filestree: %w", err)
	}
	return checkRowsAffected(result)
}

func (s *store) CreateFile(ctx context.Context, file *File) error {
	now := time.Now().UTC()
	file.CreatedAt = now
	file.UpdatedAt = now
	_, err := s.Exec.ExecContext(ctx, `
        INSERT INTO files
        (id, type, meta, blobs_id, is_folder, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		file.ID,
		file.Type,
		file.Meta,
		file.BlobsID,
		file.IsFolder,
		file.CreatedAt,
		file.UpdatedAt,
	)

	return err
}

func (s *store) GetFileByID(ctx context.Context, id string) (*File, error) {
	var file File
	err := s.Exec.QueryRowContext(ctx, `
        SELECT id, type, meta, blobs_id, is_folder, created_at, updated_at
        FROM files
        WHERE id = $1`,
		id,
	).Scan(
		&file.ID,
		&file.Type,
		&file.Meta,
		&file.BlobsID,
		&file.IsFolder,
		&file.CreatedAt,
		&file.UpdatedAt,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, libdb.ErrNotFound
	}
	return &file, err
}

func (s *store) UpdateFile(ctx context.Context, file *File) error {
	file.UpdatedAt = time.Now().UTC()

	result, err := s.Exec.ExecContext(ctx, `
        UPDATE files
        SET type = $2,
            meta = $3,
            is_folder = $4,
            blobs_id = $5,
            updated_at = $6
        WHERE id = $1`,
		file.ID,
		file.Type,
		file.Meta,
		file.IsFolder,
		file.BlobsID,
		file.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to update file: %w", err)
	}
	return checkRowsAffected(result)
}

func (s *store) DeleteFile(ctx context.Context, id string) error {
	result, err := s.Exec.ExecContext(ctx, `
        DELETE FROM files
        WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}
	return checkRowsAffected(result)
}

func (s *store) ListFiles(ctx context.Context) ([]string, error) {
	rows, err := s.Exec.QueryContext(ctx, `
        SELECT id FROM files
    `)
	if err != nil {
		return nil, fmt.Errorf("failed to list ids: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}
	return ids, nil
}

func (s *store) EstimateFileCount(ctx context.Context) (int64, error) {
	var count int64
	err := s.Exec.QueryRowContext(ctx, `
		SELECT estimate_row_count('files')
	`).Scan(&count)
	return count, err
}

func (s *store) EnforceMaxFileCount(ctx context.Context, maxCount int64) error {
	count, err := s.EstimateFileCount(ctx)
	if err != nil {
		return err
	}
	if count >= maxCount {
		return fmt.Errorf("file limit reached (max 60,000)")
	}
	return nil
}
