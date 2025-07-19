package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/libs/libdb"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTelegramFrontendCRUD(t *testing.T) {
	ctx, dbManager := store.SetupStore(t)
	userID := uuid.NewString()

	// Create test user once
	user := &store.User{
		ID:           userID,
		FriendlyName: "Telegram Bot Owner",
		Email:        "botadmin@example.com",
		Subject:      "bot-owner-subject",
	}
	// Create the test user in each transaction
	require.NoError(t, dbManager.CreateUser(ctx, user))

	tests := []struct {
		name     string
		testFunc func(t *testing.T, s store.Store)
	}{
		{
			name: "Create and Get Telegram Frontend",
			testFunc: func(t *testing.T, s store.Store) {
				frontend := &store.TelegramFrontend{
					ID:           uuid.NewString(),
					UserID:       userID,
					ChatChain:    "default-chain",
					Description:  "Test Bot",
					BotToken:     "test-token-123",
					SyncInterval: 60,
					Status:       "active",
					LastOffset:   0,
					LastError:    "",
					CreatedAt:    time.Now().UTC(),
					UpdatedAt:    time.Now().UTC(),
				}

				err := s.CreateTelegramFrontend(ctx, frontend)
				require.NoError(t, err)

				fetched, err := s.GetTelegramFrontend(ctx, frontend.ID)
				require.NoError(t, err)
				assert.Equal(t, frontend.ID, fetched.ID)
				assert.Equal(t, frontend.UserID, fetched.UserID)
				assert.Equal(t, frontend.ChatChain, fetched.ChatChain)
				assert.Equal(t, frontend.BotToken, fetched.BotToken)
				assert.Equal(t, frontend.SyncInterval, fetched.SyncInterval)
				assert.Equal(t, frontend.Status, fetched.Status)
				assert.WithinDuration(t, frontend.CreatedAt, fetched.CreatedAt, time.Second)
				assert.WithinDuration(t, frontend.UpdatedAt, fetched.UpdatedAt, time.Second)
				assert.Nil(t, fetched.LastHeartbeat)
				assert.Empty(t, fetched.LastError)
			},
		},
		{
			name: "Update Telegram Frontend",
			testFunc: func(t *testing.T, s store.Store) {
				id := uuid.NewString()
				frontend := &store.TelegramFrontend{
					ID:           id,
					UserID:       userID,
					ChatChain:    "default-chain",
					Description:  "Test Bot",
					BotToken:     uuid.NewString(),
					SyncInterval: 60,
					Status:       "active",
					LastOffset:   0,
					LastError:    "",
					CreatedAt:    time.Now().UTC(),
					UpdatedAt:    time.Now().UTC(),
				}

				// Create initial record
				err := s.CreateTelegramFrontend(ctx, frontend)
				require.NoError(t, err)

				// Update fields
				updated := *frontend
				updated.Description = "Updated Description"
				updated.SyncInterval = 120
				updated.Status = "paused"
				updated.LastOffset = 42
				updated.LastError = "test error"
				updated.LastHeartbeat = &[]time.Time{time.Now().UTC()}[0]

				err = s.UpdateTelegramFrontend(ctx, &updated)
				require.NoError(t, err)

				fetched, err := s.GetTelegramFrontend(ctx, id)
				require.NoError(t, err)
				assert.Equal(t, updated.Description, fetched.Description)
				assert.Equal(t, updated.SyncInterval, fetched.SyncInterval)
				assert.Equal(t, updated.Status, fetched.Status)
				assert.Equal(t, updated.LastOffset, fetched.LastOffset)
				assert.Equal(t, updated.LastError, fetched.LastError)
				assert.WithinDuration(t, updated.LastHeartbeat.UTC(), *fetched.LastHeartbeat, time.Second)
				assert.WithinDuration(t, updated.UpdatedAt, fetched.UpdatedAt, time.Second)
			},
		},
		{
			name: "List Telegram Frontends by User",
			testFunc: func(t *testing.T, s store.Store) {
				// Create multiple frontends for the same user
				id1 := uuid.NewString()
				frontend1 := &store.TelegramFrontend{
					ID:           id1,
					UserID:       userID,
					ChatChain:    "chain1",
					BotToken:     "token1",
					SyncInterval: 60,
					Status:       "active",
				}

				id2 := uuid.NewString()
				frontend2 := &store.TelegramFrontend{
					ID:           id2,
					UserID:       userID,
					ChatChain:    "chain2",
					BotToken:     "token2",
					SyncInterval: 120,
					Status:       "paused",
				}

				// Create another frontend for a different user (shouldn't appear in results)
				otherUserID := uuid.NewString()
				otherFrontend := &store.TelegramFrontend{
					ID:           uuid.NewString(),
					UserID:       otherUserID,
					ChatChain:    "other-chain",
					BotToken:     "other-token",
					SyncInterval: 30,
					Status:       "active",
				}

				// Create users
				require.NoError(t, s.CreateUser(ctx, &store.User{
					ID:           otherUserID,
					FriendlyName: "Other User",
					Email:        "other@example.com",
					Subject:      "other-subject",
				}))

				// Create all frontends
				require.NoError(t, s.CreateTelegramFrontend(ctx, frontend1))
				require.NoError(t, s.CreateTelegramFrontend(ctx, frontend2))
				require.NoError(t, s.CreateTelegramFrontend(ctx, otherFrontend))

				// Test listing
				frontends, err := s.ListTelegramFrontendsByUser(ctx, otherUserID)
				require.NoError(t, err)
				assert.Len(t, frontends, 1)

				// Verify all returned frontends belong to the user
				for _, f := range frontends {
					assert.Equal(t, otherUserID, f.UserID)
				}
			},
		},
		{
			name: "Create Fails with Duplicate Bot Token",
			testFunc: func(t *testing.T, s store.Store) {
				id1 := uuid.NewString()
				frontend1 := &store.TelegramFrontend{
					ID:           id1,
					UserID:       userID,
					ChatChain:    "chain1",
					BotToken:     "test-token",
					SyncInterval: 60,
					Status:       "active",
				}

				id2 := uuid.NewString()
				frontend2 := &store.TelegramFrontend{
					ID:           id2,
					UserID:       userID,
					ChatChain:    "chain2",
					BotToken:     "test-token", // Duplicate token
					SyncInterval: 120,
					Status:       "paused",
				}

				// First create should succeed
				err := s.CreateTelegramFrontend(ctx, frontend1)
				require.NoError(t, err)

				// Second create should fail
				err = s.CreateTelegramFrontend(ctx, frontend2)
				assert.Error(t, err)
			},
		},
		{
			name: "Get Nonexistent Frontend",
			testFunc: func(t *testing.T, s store.Store) {
				_, err := s.GetTelegramFrontend(ctx, uuid.NewString())
				assert.True(t, errors.Is(err, libdb.ErrNotFound))
			},
		},
	}

	// Run each test in its own transaction
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Run the test
			tt.testFunc(t, dbManager)
		})
	}
}
