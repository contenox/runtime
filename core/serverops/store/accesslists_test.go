package store_test

import (
	"testing"
	"time"

	"github.com/contenox/contenox/core/serverops/store"
	"github.com/contenox/contenox/libs/libdb"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestUnit_AccessEntry_CreatesAndFetchesByIDAndIdentityResource(t *testing.T) {
	ctx, s := store.SetupStore(t)
	user := &store.User{
		ID:           uuid.NewString(),
		Email:        "user@example.com",
		Subject:      "user|123",
		FriendlyName: "Test User",
	}
	require.NoError(t, s.CreateUser(ctx, user))

	// Create new entry
	entry := &store.AccessEntry{
		ID:           uuid.NewString(),
		Identity:     "user|123",
		Resource:     "project:456",
		ResourceType: "project",
		Permission:   store.PermissionManage,
	}
	err := s.CreateAccessEntry(ctx, entry)
	require.NoError(t, err)
	require.NotEmpty(t, entry.ID)
	require.NotEmpty(t, entry.CreatedAt)
	require.NotEmpty(t, entry.UpdatedAt)

	// Retrieve by ID
	fetched, err := s.GetAccessEntryByID(ctx, entry.ID)
	require.NoError(t, err)
	require.Equal(t, entry.ID, fetched.ID)
	require.Equal(t, "user|123", fetched.Identity)
	require.Equal(t, "project:456", fetched.Resource)
	require.Equal(t, "project", fetched.ResourceType)
	require.Equal(t, store.PermissionManage, fetched.Permission)
	require.WithinDuration(t, entry.CreatedAt, fetched.CreatedAt, time.Second)
	require.WithinDuration(t, entry.UpdatedAt, fetched.UpdatedAt, time.Second)

	// Retrieve by identity and resource
	fetchedEntries, err := s.GetAccessEntriesByIdentityAndResource(ctx, "user|123", "project:456")
	require.NoError(t, err)
	require.Len(t, fetchedEntries, 1)
	require.Equal(t, entry.ID, fetchedEntries[0].ID)
	require.Equal(t, "user|123", fetchedEntries[0].Identity)
	require.Equal(t, "project:456", fetchedEntries[0].Resource)
	require.Equal(t, "project", fetchedEntries[0].ResourceType)
	require.Equal(t, store.PermissionManage, fetchedEntries[0].Permission)
	require.WithinDuration(t, entry.CreatedAt, fetchedEntries[0].CreatedAt, time.Second)
	require.WithinDuration(t, entry.UpdatedAt, fetchedEntries[0].UpdatedAt, time.Second)
}

func TestUnit_AccessEntry_UpdatesFieldsCorrectly(t *testing.T) {
	ctx, s := store.SetupStore(t)
	user := &store.User{
		ID:           uuid.NewString(),
		Email:        "user@example.com",
		Subject:      "user|123",
		FriendlyName: "Test User",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	require.NoError(t, s.CreateUser(ctx, user))

	// Create initial entry
	entry := &store.AccessEntry{
		ID:           uuid.NewString(),
		Identity:     "user|123",
		Resource:     "project:456",
		ResourceType: "project",
		Permission:   store.PermissionEdit,
	}
	require.NoError(t, s.CreateAccessEntry(ctx, entry))

	// Update entry
	entry.Permission = store.PermissionManage
	entry.Resource = "project:789"
	require.NoError(t, s.UpdateAccessEntry(ctx, entry))

	// Verify changes
	updated, err := s.GetAccessEntryByID(ctx, entry.ID)
	require.NoError(t, err)
	require.Equal(t, store.PermissionManage, updated.Permission)
	require.Equal(t, "project:789", updated.Resource)
	require.True(t, updated.UpdatedAt.After(entry.CreatedAt))
}

func TestUnit_AccessEntry_DeletesSuccessfully(t *testing.T) {
	ctx, s := store.SetupStore(t)
	user := &store.User{
		ID:           uuid.NewString(),
		Email:        "user@example.com",
		Subject:      "user|123",
		FriendlyName: "Test User",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	require.NoError(t, s.CreateUser(ctx, user))

	// Create entry
	entry := &store.AccessEntry{
		ID:           uuid.NewString(),
		Identity:     "user|123",
		Resource:     "project:456",
		ResourceType: "server",
		Permission:   1,
	}
	require.NoError(t, s.CreateAccessEntry(ctx, entry))

	// Delete entry
	err := s.DeleteAccessEntry(ctx, entry.ID)
	require.NoError(t, err)

	// Verify deletion
	_, err = s.GetAccessEntryByID(ctx, entry.ID)
	require.ErrorIs(t, err, libdb.ErrNotFound)
}

func TestUnit_AccessEntry_BulkDeletesByIdentity(t *testing.T) {
	ctx, s := store.SetupStore(t)
	user := &store.User{
		ID:           uuid.NewString(),
		Email:        "user@example.com",
		Subject:      "user|123",
		FriendlyName: "Test User",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	require.NoError(t, s.CreateUser(ctx, user))

	// Create entry
	entry := &store.AccessEntry{
		ID:           uuid.NewString(),
		Identity:     "user|123",
		Resource:     "project:456",
		ResourceType: "server",
		Permission:   1,
	}
	require.NoError(t, s.CreateAccessEntry(ctx, entry))
	// Create entry
	entry = &store.AccessEntry{
		ID:           uuid.NewString(),
		Identity:     "user|123",
		Resource:     "project:457",
		ResourceType: "server",
		Permission:   1,
	}
	require.NoError(t, s.CreateAccessEntry(ctx, entry))
	ae, err := s.GetAccessEntriesByIdentity(ctx, "user|123")
	require.NoError(t, err)
	require.Len(t, ae, 2)
	// Delete entry
	err = s.DeleteAccessEntriesByIdentity(ctx, "user|123")
	require.NoError(t, err)

	// Verify deletion
	ae, err = s.GetAccessEntriesByIdentity(ctx, "user|123")
	require.Len(t, ae, 0)
	require.NoError(t, err)
}

func TestUnit_AccessEntry_BulkDeletesByResource(t *testing.T) {
	ctx, s := store.SetupStore(t)
	user := &store.User{
		ID:           uuid.NewString(),
		Email:        "user@example.com",
		Subject:      "user|123",
		FriendlyName: "Test User",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	require.NoError(t, s.CreateUser(ctx, user))

	// Create entry
	entry := &store.AccessEntry{
		ID:         uuid.NewString(),
		Identity:   "user|123",
		Resource:   "project:456",
		Permission: 1,
	}
	require.NoError(t, s.CreateAccessEntry(ctx, entry))
	// Create entry
	entry = &store.AccessEntry{
		ID:           uuid.NewString(),
		Identity:     "user|123",
		Resource:     "project:457",
		ResourceType: "server",
		Permission:   1,
	}
	require.NoError(t, s.CreateAccessEntry(ctx, entry))
	// Delete entry
	err := s.DeleteAccessEntriesByResource(ctx, "project:456")
	require.NoError(t, err)

	// Verify deletion
	ae, err := s.GetAccessEntriesByIdentity(ctx, "user|123")
	require.Len(t, ae, 1)
	require.NoError(t, err)
}

func TestUnit_AccessEntry_ListReturnsOrderedByCreationTime(t *testing.T) {
	ctx, s := store.SetupStore(t)
	beforeCreated := time.Now().UTC()
	user := &store.User{
		ID:           uuid.NewString(),
		Email:        "user@example.com",
		Subject:      "user|1",
		FriendlyName: "Test User",
	}
	require.NoError(t, s.CreateUser(ctx, user))
	user = &store.User{
		ID:           uuid.NewString(),
		Email:        "user@example2.com",
		Subject:      "user|2",
		FriendlyName: "Test User",
	}
	require.NoError(t, s.CreateUser(ctx, user))

	// Create two entries with delay
	entry1 := &store.AccessEntry{ID: uuid.NewString(), Identity: "user|1", Resource: "res1", Permission: 1}
	require.NoError(t, s.CreateAccessEntry(ctx, entry1))

	time.Sleep(10 * time.Millisecond)

	entry2 := &store.AccessEntry{ID: uuid.NewString(), Identity: "user|2", Resource: "res2", Permission: 2}
	require.NoError(t, s.CreateAccessEntry(ctx, entry2))

	// Verify order (newest first)
	entries, err := s.ListAccessEntries(ctx, time.Now().UTC())
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.Equal(t, entry2.ID, entries[0].ID)
	require.Equal(t, entry1.ID, entries[1].ID)
	entries, err = s.ListAccessEntries(ctx, beforeCreated)
	require.NoError(t, err)
	require.Len(t, entries, 0)
}

func TestUnit_AccessEntry_FetchesAllForGivenIdentity(t *testing.T) {
	ctx, s := store.SetupStore(t)
	user := &store.User{
		ID:           uuid.NewString(),
		Email:        "user@example.com",
		Subject:      "user|123",
		FriendlyName: "Test User",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	require.NoError(t, s.CreateUser(ctx, user))
	user = &store.User{
		ID:           uuid.NewString(),
		Email:        "user@example2.com",
		Subject:      "user|456",
		FriendlyName: "Test User",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	require.NoError(t, s.CreateUser(ctx, user))

	// Create test entries
	entries := []*store.AccessEntry{
		{ID: uuid.NewString(), Identity: "user|123", Resource: "res1", ResourceType: "server", Permission: 1},
		{ID: uuid.NewString(), Identity: "user|123", Resource: "res2", ResourceType: "server", Permission: 2},
		{ID: uuid.NewString(), Identity: "user|456", Resource: "res1", ResourceType: "server", Permission: 2},
	}

	for _, e := range entries {
		require.NoError(t, s.CreateAccessEntry(ctx, e))
	}

	// Get by identity
	results, err := s.GetAccessEntriesByIdentity(ctx, "user|123")
	require.NoError(t, err)
	require.Len(t, results, 2)
}

func TestUnit_AccessEntry_UpdateFailsIfNotFound(t *testing.T) {
	ctx, s := store.SetupStore(t)

	entry := &store.AccessEntry{
		ID: uuid.NewString(),
	}
	err := s.UpdateAccessEntry(ctx, entry)
	require.ErrorIs(t, err, libdb.ErrNotFound)
}

func TestUnit_AccessEntry_DeleteFailsIfNotFound(t *testing.T) {
	ctx, s := store.SetupStore(t)

	err := s.DeleteAccessEntry(ctx, uuid.NewString())
	require.ErrorIs(t, err, libdb.ErrNotFound)
}

func TestUnit_AccessEntry_CreateFailsOnDuplicateKey(t *testing.T) {
	ctx, s := store.SetupStore(t)
	user := &store.User{
		ID:           uuid.NewString(),
		Email:        "user@example.com",
		Subject:      "user|123",
		FriendlyName: "Test User",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	require.NoError(t, s.CreateUser(ctx, user))
	entry := &store.AccessEntry{
		ID:           uuid.NewString(),
		Identity:     "user|123",
		Resource:     "project:456",
		ResourceType: "server",
		Permission:   1,
	}
	require.NoError(t, s.CreateAccessEntry(ctx, entry))

	// Attempt duplicate
	err := s.CreateAccessEntry(ctx, entry)
	require.Error(t, err)
	require.ErrorIs(t, err, libdb.ErrUniqueViolation)
}
