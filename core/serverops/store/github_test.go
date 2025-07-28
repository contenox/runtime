package store_test

import (
	"testing"
	"time"

	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/libs/libdb"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestGitHubRepoCRUD(t *testing.T) {
	ctx, s := store.SetupStore(t)

	// Create test data
	userID := uuid.New().String()
	repo := &store.GitHubRepo{
		ID:          uuid.New().String(),
		UserID:      userID,
		BotUserName: "bot-user",
		Owner:       "test-owner",
		RepoName:    "test-repo",
		AccessToken: "test-token",
	}

	// Test Create
	t.Run("Create", func(t *testing.T) {
		err := s.CreateGitHubRepo(ctx, repo)
		require.NoError(t, err)
		require.NotZero(t, repo.CreatedAt)
		require.NotZero(t, repo.UpdatedAt)
	})

	// Test Get
	t.Run("Get", func(t *testing.T) {
		retrieved, err := s.GetGitHubRepo(ctx, repo.ID)
		require.NoError(t, err)
		require.Equal(t, repo.ID, retrieved.ID)
		require.Equal(t, repo.UserID, retrieved.UserID)
		require.Equal(t, repo.BotUserName, retrieved.BotUserName)
		require.Equal(t, repo.Owner, retrieved.Owner)
		require.Equal(t, repo.RepoName, retrieved.RepoName)
		require.Equal(t, repo.AccessToken, retrieved.AccessToken)
		require.WithinDuration(t, repo.CreatedAt, retrieved.CreatedAt, time.Second)
		require.WithinDuration(t, repo.UpdatedAt, retrieved.UpdatedAt, time.Second)
	})

	// Test List by user
	t.Run("ListByUser", func(t *testing.T) {
		// Create second repo for same user
		repo2 := &store.GitHubRepo{
			ID:          uuid.New().String(),
			UserID:      userID,
			BotUserName: "bot-user",
			Owner:       "another-owner",
			RepoName:    "another-repo",
			AccessToken: "another-token",
		}
		err := s.CreateGitHubRepo(ctx, repo2)
		require.NoError(t, err)

		repos, err := s.ListGitHubRepos(ctx)
		require.NoError(t, err)
		require.Len(t, repos, 2)
		require.Equal(t, repo2.ID, repos[0].ID) // Should be first due to DESC order
		require.Equal(t, repo.ID, repos[1].ID)
	})

	// Test Delete
	t.Run("Delete", func(t *testing.T) {
		err := s.DeleteGitHubRepo(ctx, repo.ID)
		require.NoError(t, err)

		_, err = s.GetGitHubRepo(ctx, repo.ID)
		require.ErrorIs(t, err, libdb.ErrNotFound)
	})

	// Test error cases
	t.Run("Errors", func(t *testing.T) {
		t.Run("GetNonExistent", func(t *testing.T) {
			_, err := s.GetGitHubRepo(ctx, "non-existent-id")
			require.ErrorIs(t, err, libdb.ErrNotFound)
		})

		t.Run("DeleteNonExistent", func(t *testing.T) {
			err := s.DeleteGitHubRepo(ctx, "non-existent-id")
			require.ErrorIs(t, err, libdb.ErrNotFound)
		})
	})
}
