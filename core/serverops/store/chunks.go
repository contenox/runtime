package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/contenox/contenox/libs/libdb"
)

func (s *store) CreateChunkIndex(ctx context.Context, chunk *ChunkIndex) error {
	_, err := s.Exec.ExecContext(ctx, `
        INSERT INTO chunks_idx
        (id, vector_id, vector_store, resource_id, resource_type, embedding_model)
        VALUES ($1, $2, $3, $4, $5, $6)`,
		chunk.ID, chunk.VectorID, chunk.VectorStore,
		chunk.ResourceID, chunk.ResourceType, chunk.EmbeddingModel,
	)
	return err
}

func (s *store) GetChunkIndexByID(ctx context.Context, id string) (*ChunkIndex, error) {
	var chunk ChunkIndex
	err := s.Exec.QueryRowContext(ctx, `
        SELECT id, vector_id, vector_store, resource_id, resource_type, embedding_model
        FROM chunks_idx WHERE id = $1`, id,
	).Scan(
		&chunk.ID, &chunk.VectorID, &chunk.VectorStore,
		&chunk.ResourceID, &chunk.ResourceType, &chunk.EmbeddingModel,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, libdb.ErrNotFound
	}
	return &chunk, err
}

func (s *store) UpdateChunkIndex(ctx context.Context, chunk *ChunkIndex) error {
	result, err := s.Exec.ExecContext(ctx, `
        UPDATE chunks_idx SET
        vector_id = $2, vector_store = $3,
        resource_id = $4, resource_type = $5, embedding_model = $6
        WHERE id = $1`,
		chunk.ID, chunk.VectorID, chunk.VectorStore,
		chunk.ResourceID, chunk.ResourceType, chunk.EmbeddingModel,
	)
	if err != nil {
		return fmt.Errorf("failed to update chunk index: %w", err)
	}
	return checkRowsAffected(result)
}

func (s *store) DeleteChunkIndex(ctx context.Context, id string) error {
	result, err := s.Exec.ExecContext(ctx, `
        DELETE FROM chunks_idx WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete chunk index: %w", err)
	}
	return checkRowsAffected(result)
}

func (s *store) ListChunkIndicesByVectorID(ctx context.Context, vectorID string) ([]*ChunkIndex, error) {
	rows, err := s.Exec.QueryContext(ctx, `
        SELECT id, vector_id, vector_store, resource_id, resource_type, embedding_model
        FROM chunks_idx WHERE vector_id = $1`, vectorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chunks []*ChunkIndex
	for rows.Next() {
		var chunk ChunkIndex
		if err := rows.Scan(
			&chunk.ID, &chunk.VectorID, &chunk.VectorStore,
			&chunk.ResourceID, &chunk.ResourceType, &chunk.EmbeddingModel,
		); err != nil {
			return nil, err
		}
		chunks = append(chunks, &chunk)
	}
	return chunks, rows.Err()
}

func (s *store) ListChunkIndicesByResource(ctx context.Context, resourceID, resourceType string) ([]*ChunkIndex, error) {
	rows, err := s.Exec.QueryContext(ctx, `
        SELECT id, vector_id, vector_store, resource_id, resource_type, embedding_model
        FROM chunks_idx
        WHERE resource_id = $1 AND resource_type = $2`,
		resourceID, resourceType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chunks []*ChunkIndex
	for rows.Next() {
		var chunk ChunkIndex
		if err := rows.Scan(
			&chunk.ID, &chunk.VectorID, &chunk.VectorStore,
			&chunk.ResourceID, &chunk.ResourceType, &chunk.EmbeddingModel,
		); err != nil {
			return nil, err
		}
		chunks = append(chunks, &chunk)
	}
	return chunks, rows.Err()
}
