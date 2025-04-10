package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/js402/CATE/libs/libdb"
)

func (s *store) CreateFile(ctx context.Context, file *File) error {
	now := time.Now().UTC()
	file.CreatedAt = now
	file.UpdatedAt = now

	_, err := s.Exec.ExecContext(ctx, `
        INSERT INTO files
        (id, path, type, meta, blobs_id, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		file.ID,
		file.Path,
		file.Type,
		file.Meta,
		file.BlobsID,
		file.CreatedAt,
		file.UpdatedAt,
	)
	return err
}

func (s *store) GetFileByID(ctx context.Context, id string) (*File, error) {
	var file File
	err := s.Exec.QueryRowContext(ctx, `
        SELECT id, path, type, meta, blobs_id, created_at, updated_at
        FROM files
        WHERE id = $1`,
		id,
	).Scan(
		&file.ID,
		&file.Path,
		&file.Type,
		&file.Meta,
		&file.BlobsID,
		&file.CreatedAt,
		&file.UpdatedAt,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, libdb.ErrNotFound
	}
	return &file, err
}

func (s *store) GetFilesByPath(ctx context.Context, path string) ([]File, error) {
	rows, err := s.Exec.QueryContext(ctx, `
        SELECT id, path, type, meta, blobs_id, created_at, updated_at
        FROM files
        WHERE path = $1`,
		path,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, libdb.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []File
	for rows.Next() {
		var file File
		if err := rows.Scan(
			&file.ID,
			&file.Path,
			&file.Type,
			&file.Meta,
			&file.BlobsID,
			&file.CreatedAt,
			&file.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan message: %w", err)
		}
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return files, nil
}

func (s *store) UpdateFile(ctx context.Context, file *File) error {
	file.UpdatedAt = time.Now().UTC()

	result, err := s.Exec.ExecContext(ctx, `
        UPDATE files
        SET path = $2,
            type = $3,
            meta = $4,
            blobs_id = $5,
            updated_at = $6
        WHERE id = $1`,
		file.ID,
		file.Path,
		file.Type,
		file.Meta,
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

func (s *store) ListAllPaths(ctx context.Context) ([]string, error) {
	rows, err := s.Exec.QueryContext(ctx, `
        SELECT DISTINCT path FROM files
    `)
	if err != nil {
		return nil, fmt.Errorf("failed to list paths: %w", err)
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, fmt.Errorf("failed to scan path: %w", err)
		}
		paths = append(paths, path)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}
	return paths, nil
}
