package store_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/js402/cate/core/serverops/store"
	"github.com/js402/cate/libs/libdb"
	"github.com/stretchr/testify/require"
)

func TestCreateAndGetChunkIndex(t *testing.T) {
	ctx, s := store.SetupStore(t)

	chunk := &store.ChunkIndex{
		ID:             uuid.NewString(),
		VectorID:       uuid.NewString(),
		VectorStore:    "vald",
		ResourceID:     uuid.NewString(),
		ResourceType:   "document",
		EmbeddingModel: "myllm",
	}

	err := s.CreateChunkIndex(ctx, chunk)
	require.NoError(t, err)

	got, err := s.GetChunkIndexByID(ctx, chunk.ID)
	require.NoError(t, err)
	require.Equal(t, chunk.VectorID, got.VectorID)
	require.Equal(t, chunk.VectorStore, got.VectorStore)
	require.Equal(t, chunk.ResourceID, got.ResourceID)
	require.Equal(t, chunk.ResourceType, got.ResourceType)
	require.Equal(t, chunk.EmbeddingModel, got.EmbeddingModel)
}

func TestUpdateChunkIndex(t *testing.T) {
	ctx, s := store.SetupStore(t)

	chunk := &store.ChunkIndex{
		ID:             uuid.NewString(),
		VectorID:       uuid.NewString(),
		VectorStore:    "vald",
		ResourceID:     uuid.NewString(),
		ResourceType:   "document",
		EmbeddingModel: "myllm",
	}
	s.CreateChunkIndex(ctx, chunk)

	// Update fields
	chunk.VectorID = uuid.NewString()
	chunk.ResourceType = "image"

	err := s.UpdateChunkIndex(ctx, chunk)
	require.NoError(t, err)

	updated, err := s.GetChunkIndexByID(ctx, chunk.ID)
	require.NoError(t, err)
	require.Equal(t, chunk.VectorID, updated.VectorID)
	require.Equal(t, chunk.ResourceType, updated.ResourceType)
	require.Equal(t, chunk.EmbeddingModel, updated.EmbeddingModel)
}

func TestDeleteChunkIndex(t *testing.T) {
	ctx, s := store.SetupStore(t)

	chunk := &store.ChunkIndex{
		ID:       uuid.NewString(),
		VectorID: uuid.NewString(),
	}
	s.CreateChunkIndex(ctx, chunk)

	err := s.DeleteChunkIndex(ctx, chunk.ID)
	require.NoError(t, err)

	_, err = s.GetChunkIndexByID(ctx, chunk.ID)
	require.ErrorIs(t, err, libdb.ErrNotFound)
}

func TestListChunkIndicesByVectorID(t *testing.T) {
	ctx, s := store.SetupStore(t)

	vectorID := uuid.NewString()

	// Create 3 chunks with same vector ID
	for range 3 {
		chunk := &store.ChunkIndex{
			ID:          uuid.NewString(),
			VectorID:    vectorID,
			VectorStore: "vald",
		}
		s.CreateChunkIndex(ctx, chunk)
	}

	// Create another chunk with different vector ID
	otherChunk := &store.ChunkIndex{
		ID:          uuid.NewString(),
		VectorID:    uuid.NewString(),
		VectorStore: "vald",
	}
	s.CreateChunkIndex(ctx, otherChunk)

	chunks, err := s.ListChunkIndicesByVectorID(ctx, vectorID)
	require.NoError(t, err)
	require.Len(t, chunks, 3)
	for _, c := range chunks {
		require.Equal(t, vectorID, c.VectorID)
	}
}

func TestListChunkIndicesByResource(t *testing.T) {
	ctx, s := store.SetupStore(t)

	targetResourceID := uuid.NewString()
	targetType := "document"

	// Create matching resources
	for range 2 {
		chunk := &store.ChunkIndex{
			ID:             uuid.NewString(),
			ResourceID:     targetResourceID,
			ResourceType:   targetType,
			EmbeddingModel: "myllm",
		}
		s.CreateChunkIndex(ctx, chunk)
	}

	// Create non-matching resources
	s.CreateChunkIndex(ctx, &store.ChunkIndex{
		ID:             uuid.NewString(),
		ResourceID:     uuid.NewString(),
		ResourceType:   targetType,
		EmbeddingModel: "myllm",
	})
	s.CreateChunkIndex(ctx, &store.ChunkIndex{
		ID:             uuid.NewString(),
		ResourceID:     targetResourceID,
		ResourceType:   "image",
		EmbeddingModel: "myllm",
	})

	chunks, err := s.ListChunkIndicesByResource(ctx, targetResourceID, targetType)
	require.NoError(t, err)
	require.Len(t, chunks, 2)
	for _, c := range chunks {
		require.Equal(t, targetResourceID, c.ResourceID)
		require.Equal(t, targetType, c.ResourceType)
	}
}

func TestGetNonExistentChunkIndex(t *testing.T) {
	ctx, s := store.SetupStore(t)

	_, err := s.GetChunkIndexByID(ctx, uuid.NewString())
	require.ErrorIs(t, err, libdb.ErrNotFound)
}

func TestUpdateNonExistentChunkIndex(t *testing.T) {
	ctx, s := store.SetupStore(t)

	chunk := &store.ChunkIndex{ID: uuid.NewString()}
	err := s.UpdateChunkIndex(ctx, chunk)
	require.Error(t, err)
	require.ErrorIs(t, err, libdb.ErrNotFound)
}

func TestDeleteNonExistentChunkIndex(t *testing.T) {
	ctx, s := store.SetupStore(t)

	err := s.DeleteChunkIndex(ctx, uuid.NewString())
	require.Error(t, err)
	require.ErrorIs(t, err, libdb.ErrNotFound)
}

func TestDuplicateChunkIndexID(t *testing.T) {
	ctx, s := store.SetupStore(t)

	id := uuid.NewString()
	chunk1 := &store.ChunkIndex{ID: id}
	chunk2 := &store.ChunkIndex{ID: id}

	err := s.CreateChunkIndex(ctx, chunk1)
	require.NoError(t, err)

	err = s.CreateChunkIndex(ctx, chunk2)
	require.Error(t, err)
	require.ErrorIs(t, err, libdb.ErrUniqueViolation)
}

func TestEmptyListings(t *testing.T) {
	ctx, s := store.SetupStore(t)

	t.Run("ByVectorID", func(t *testing.T) {
		chunks, err := s.ListChunkIndicesByVectorID(ctx, uuid.NewString())
		require.NoError(t, err)
		require.Empty(t, chunks)
	})

	t.Run("ByResource", func(t *testing.T) {
		chunks, err := s.ListChunkIndicesByResource(ctx, uuid.NewString(), "doc")
		require.NoError(t, err)
		require.Empty(t, chunks)
	})
}
