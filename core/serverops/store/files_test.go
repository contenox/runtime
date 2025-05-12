package store_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/js402/cate/core/serverops/store"
	"github.com/js402/cate/libs/libdb"
	"github.com/stretchr/testify/require"
)

// TestCreateAndGetFile verifies that a file can be created and retrieved by its ID.
func TestCreateAndGetFile(t *testing.T) {
	ctx, s := store.SetupStore(t)

	// Create a new file
	file := &store.File{
		ID:      uuid.NewString(),
		Type:    "text/plain",
		Meta:    []byte(`{"description": "Test file"}`),
		BlobsID: uuid.NewString(),
	}

	err := s.CreateFile(ctx, file)
	require.NoError(t, err)
	require.NotZero(t, file.CreatedAt)
	require.NotZero(t, file.UpdatedAt)

	// Retrieve the file by ID
	retrieved, err := s.GetFileByID(ctx, file.ID)
	require.NoError(t, err)
	require.Equal(t, file.ID, retrieved.ID)
	require.Equal(t, file.Type, retrieved.Type)
	require.Equal(t, file.Meta, retrieved.Meta)
	require.Equal(t, file.BlobsID, retrieved.BlobsID)
	require.WithinDuration(t, file.CreatedAt, retrieved.CreatedAt, time.Second)
	require.WithinDuration(t, file.UpdatedAt, retrieved.UpdatedAt, time.Second)
}

// TestUpdateFile verifies that a file's fields can be updated.
func TestUpdateFile(t *testing.T) {
	ctx, s := store.SetupStore(t)

	// Create a file to update.
	file := &store.File{
		ID:      uuid.NewString(),
		Type:    "text/plain",
		Meta:    []byte(`{"description": "Old description"}`),
		BlobsID: uuid.NewString(),
	}
	require.NoError(t, s.CreateFile(ctx, file))

	// Update file fields.
	file.Type = "application/json"
	file.Meta = []byte(`{"description": "New description"}`)
	file.BlobsID = uuid.NewString()

	// Call update.
	require.NoError(t, s.UpdateFile(ctx, file))

	// Retrieve the file and verify the changes.
	updated, err := s.GetFileByID(ctx, file.ID)
	require.NoError(t, err)
	require.Equal(t, "application/json", updated.Type)
	require.Equal(t, file.Meta, updated.Meta)
	require.Equal(t, file.BlobsID, updated.BlobsID)
	require.True(t, updated.UpdatedAt.After(updated.CreatedAt))
}

// TestDeleteFile verifies that a file can be deleted.
func TestDeleteFile(t *testing.T) {
	ctx, s := store.SetupStore(t)

	// Create a file to delete.
	file := &store.File{
		ID:      uuid.NewString(),
		Type:    "text/plain",
		Meta:    []byte(`{"description": "To be deleted"}`),
		BlobsID: uuid.NewString(),
	}
	require.NoError(t, s.CreateFile(ctx, file))

	// Delete the file.
	require.NoError(t, s.DeleteFile(ctx, file.ID))

	// Attempt to retrieve the deleted file.
	_, err := s.GetFileByID(ctx, file.ID)
	require.ErrorIs(t, err, libdb.ErrNotFound)
}

// TestGetFileByIDNotFound verifies that retrieving a non-existent file returns an appropriate error.
func TestGetFileByIDNotFound(t *testing.T) {
	ctx, s := store.SetupStore(t)

	// Attempt to get a file that doesn't exist.
	_, err := s.GetFileByID(ctx, uuid.NewString())
	require.ErrorIs(t, err, libdb.ErrNotFound)
}

func TestListAll(t *testing.T) {
	ctx, s := store.SetupStore(t)

	// Initially, there should be no file files.
	files, err := s.ListFiles(ctx)
	require.NoError(t, err)
	require.Len(t, files, 0)

	// Insert several files with various paths.
	file1 := &store.File{
		ID:      uuid.NewString(),
		Type:    "text/plain",
		Meta:    []byte(`{"description": "File one"}`),
		BlobsID: uuid.NewString(),
	}
	file2 := &store.File{
		ID:      uuid.NewString(),
		Type:    "text/plain",
		Meta:    []byte(`{"description": "File two"}`),
		BlobsID: uuid.NewString(),
	}
	// Duplicate path with file1.
	file3 := &store.File{
		ID:      uuid.NewString(),
		Type:    "application/json",
		Meta:    []byte(`{"description": "Another file at path one"}`),
		BlobsID: uuid.NewString(),
	}

	require.NoError(t, s.CreateFile(ctx, file1))
	require.NoError(t, s.CreateFile(ctx, file2))
	require.NoError(t, s.CreateFile(ctx, file3))

	// List all.
	files, err = s.ListFiles(ctx)
	require.NoError(t, err)
	require.Len(t, files, 3)
}

func TestCreateAndGetFileNameID(t *testing.T) {
	ctx, s := store.SetupStore(t)

	id := uuid.NewString()
	parentID := uuid.NewString()
	name := "example.txt"

	err := s.CreateFileNameID(ctx, id, parentID, name)
	require.NoError(t, err)

	gotName, err := s.GetFileNameByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, name, gotName)

	gotParentID, err := s.GetFileParentID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, parentID, gotParentID)
}

func TestUpdateFileNameByID(t *testing.T) {
	ctx, s := store.SetupStore(t)

	id := uuid.NewString()
	parentID := uuid.NewString()
	initialName := "initial.txt"
	newName := "updated.txt"

	require.NoError(t, s.CreateFileNameID(ctx, id, parentID, initialName))

	err := s.UpdateFileNameByID(ctx, id, newName)
	require.NoError(t, err)

	gotName, err := s.GetFileNameByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, newName, gotName)
}

func TestDeleteFileNameID(t *testing.T) {
	ctx, s := store.SetupStore(t)

	id := uuid.NewString()
	parentID := uuid.NewString()
	name := "todelete.txt"

	require.NoError(t, s.CreateFileNameID(ctx, id, parentID, name))

	require.NoError(t, s.DeleteFileNameID(ctx, id))

	_, err := s.GetFileNameByID(ctx, id)
	require.ErrorIs(t, err, libdb.ErrNotFound)
}

func TestListFileIDsByParentID(t *testing.T) {
	ctx, s := store.SetupStore(t)

	parentID := uuid.NewString()
	id1 := uuid.NewString()
	id2 := uuid.NewString()

	require.NoError(t, s.CreateFileNameID(ctx, id1, parentID, "a.txt"))
	require.NoError(t, s.CreateFileNameID(ctx, id2, parentID, "b.txt"))

	ids, err := s.ListFileIDsByParentID(ctx, parentID)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{id1, id2}, ids)
}

func TestListFileIDsByEmptyParentID(t *testing.T) {
	ctx, s := store.SetupStore(t)

	id1 := uuid.NewString()
	id2 := uuid.NewString()

	require.NoError(t, s.CreateFileNameID(ctx, id1, "", "a.txt"))
	require.NoError(t, s.CreateFileNameID(ctx, id2, "", "b.txt"))

	ids, err := s.ListFileIDsByParentID(ctx, "")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{id1, id2}, ids)
}

func TestListFileIDsByName(t *testing.T) {
	ctx, s := store.SetupStore(t)

	parentID := uuid.NewString()
	uniqueName := "unique.txt"
	id := uuid.NewString()

	require.NoError(t, s.CreateFileNameID(ctx, id, parentID, uniqueName))

	ids, err := s.ListFileIDsByName(ctx, parentID, uniqueName)
	require.NoError(t, err)
	require.NotEmpty(t, ids)
	require.Contains(t, ids, id)
}
