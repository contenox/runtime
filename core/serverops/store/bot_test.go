package store_test

import (
	"testing"
	"time"

	libdb "github.com/contenox/dbexec"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestAppendAndGetBot(t *testing.T) {
	ctx, s := store.SetupStore(t)

	// Test empty list initially
	bots, err := s.ListBots(ctx)
	require.NoError(t, err)
	require.Empty(t, bots)

	// Create and append a bot
	botID := uuid.NewString()
	userID := uuid.NewString()
	bot := &store.Bot{
		ID:          botID,
		Name:        "test-bot",
		UserID:      userID,
		BotType:     "github",
		JobType:     "comment_processor",
		TaskChainID: "chain1",
	}
	err = s.CreateBot(ctx, bot)
	require.NoError(t, err)
	require.NotZero(t, bot.CreatedAt)
	require.NotZero(t, bot.UpdatedAt)

	// Test GetBot by ID
	retrieved, err := s.GetBot(ctx, botID)
	require.NoError(t, err)
	require.Equal(t, botID, retrieved.ID)
	require.Equal(t, "test-bot", retrieved.Name)
	require.Equal(t, userID, retrieved.UserID)
	require.Equal(t, "github", retrieved.BotType)
	require.Equal(t, "comment_processor", retrieved.JobType)
	require.Equal(t, "chain1", retrieved.TaskChainID)
	require.WithinDuration(t, bot.CreatedAt, retrieved.CreatedAt, time.Second)
	require.WithinDuration(t, bot.UpdatedAt, retrieved.UpdatedAt, time.Second)

	// Test GetBotByName
	retrievedByName, err := s.GetBotByName(ctx, "test-bot")
	require.NoError(t, err)
	require.Equal(t, botID, retrievedByName.ID)
}

func TestUpdateBot(t *testing.T) {
	ctx, s := store.SetupStore(t)

	// Create initial bot
	bot := &store.Bot{
		ID:          uuid.NewString(),
		Name:        "update-test",
		UserID:      uuid.NewString(),
		BotType:     "old-type",
		JobType:     "old-job",
		TaskChainID: "old-chain",
	}
	require.NoError(t, s.CreateBot(ctx, bot))

	// Update bot fields
	originalCreatedAt := bot.CreatedAt
	updatedBot := &store.Bot{
		ID:          bot.ID,
		Name:        "updated-name",
		UserID:      bot.UserID,
		BotType:     "new-type",
		JobType:     "new-job",
		TaskChainID: "new-chain",
	}
	require.NoError(t, s.UpdateBot(ctx, updatedBot))

	// Verify update
	updated, err := s.GetBot(ctx, bot.ID)
	require.NoError(t, err)
	require.Equal(t, "updated-name", updated.Name)
	require.Equal(t, "new-type", updated.BotType)
	require.Equal(t, "new-job", updated.JobType)
	require.Equal(t, "new-chain", updated.TaskChainID)
	require.Equal(t, bot.UserID, updated.UserID)
	require.Equal(t, originalCreatedAt, updated.CreatedAt)
	require.True(t, updated.UpdatedAt.After(originalCreatedAt))
}

func TestDeleteBot(t *testing.T) {
	ctx, s := store.SetupStore(t)

	// Create bot to delete
	bot := &store.Bot{
		ID:     uuid.NewString(),
		Name:   "delete-test",
		UserID: uuid.NewString(),
	}
	require.NoError(t, s.CreateBot(ctx, bot))

	// Delete bot
	require.NoError(t, s.DeleteBot(ctx, bot.ID))

	// Verify deletion
	_, err := s.GetBot(ctx, bot.ID)
	require.ErrorIs(t, err, libdb.ErrNotFound)

	_, err = s.GetBotByName(ctx, "delete-test")
	require.ErrorIs(t, err, libdb.ErrNotFound)
}

func TestListBotsOrder(t *testing.T) {
	ctx, s := store.SetupStore(t)

	// Create multiple bots in sequence
	bot1 := &store.Bot{
		ID:     uuid.NewString(),
		Name:   "bot1",
		UserID: uuid.NewString(),
	}
	bot2 := &store.Bot{
		ID:     uuid.NewString(),
		Name:   "bot2",
		UserID: uuid.NewString(),
	}
	require.NoError(t, s.CreateBot(ctx, bot1))
	time.Sleep(10 * time.Millisecond) // Ensure different timestamps
	require.NoError(t, s.CreateBot(ctx, bot2))

	// Test list order (should be descending by creation time)
	bots, err := s.ListBots(ctx)
	require.NoError(t, err)
	require.Len(t, bots, 2)
	require.Equal(t, "bot2", bots[0].Name)
	require.Equal(t, "bot1", bots[1].Name)
	require.True(t, bots[0].CreatedAt.Before(bots[1].CreatedAt))
}

func TestAppendDuplicateBotName(t *testing.T) {
	ctx, s := store.SetupStore(t)

	// Create first bot
	bot1 := &store.Bot{
		ID:     uuid.NewString(),
		Name:   "duplicate",
		UserID: uuid.NewString(),
	}
	require.NoError(t, s.CreateBot(ctx, bot1))

	// Try to create another bot with same name
	bot2 := &store.Bot{
		ID:     uuid.NewString(),
		Name:   "duplicate",
		UserID: uuid.NewString(),
	}
	err := s.CreateBot(ctx, bot2)
	require.Error(t, err)
}

func TestGetBotNotFound(t *testing.T) {
	ctx, s := store.SetupStore(t)

	// Attempt to get non-existent bot
	_, err := s.GetBot(ctx, uuid.NewString())
	require.ErrorIs(t, err, libdb.ErrNotFound)
}

func TestGetBotByNameNotFound(t *testing.T) {
	ctx, s := store.SetupStore(t)

	// Attempt to get non-existent bot by name
	_, err := s.GetBotByName(ctx, "non-existent")
	require.ErrorIs(t, err, libdb.ErrNotFound)
}

func TestDeleteNonExistentBot(t *testing.T) {
	ctx, s := store.SetupStore(t)

	// Attempt to delete non-existent bot
	err := s.DeleteBot(ctx, uuid.NewString())
	require.ErrorIs(t, err, libdb.ErrNotFound)
}

func TestListBotsEmpty(t *testing.T) {
	ctx, s := store.SetupStore(t)

	// Verify empty list initially
	bots, err := s.ListBots(ctx)
	require.NoError(t, err)
	require.Empty(t, bots)
}

func TestListBotsByUser(t *testing.T) {
	ctx, s := store.SetupStore(t)

	// Create user-specific bots
	user1 := uuid.NewString()
	user2 := uuid.NewString()

	bot1 := &store.Bot{
		ID:     uuid.NewString(),
		Name:   "user1-bot1",
		UserID: user1,
	}
	bot2 := &store.Bot{
		ID:     uuid.NewString(),
		Name:   "user1-bot2",
		UserID: user1,
	}
	bot3 := &store.Bot{
		ID:     uuid.NewString(),
		Name:   "user2-bot1",
		UserID: user2,
	}

	require.NoError(t, s.CreateBot(ctx, bot1))
	require.NoError(t, s.CreateBot(ctx, bot2))
	require.NoError(t, s.CreateBot(ctx, bot3))

	// Test listing by user
	user1Bots, err := s.ListBotsByUser(ctx, user1)
	require.NoError(t, err)
	require.Len(t, user1Bots, 2)
	names := []string{user1Bots[0].Name, user1Bots[1].Name}
	require.ElementsMatch(t, []string{"user1-bot1", "user1-bot2"}, names)

	user2Bots, err := s.ListBotsByUser(ctx, user2)
	require.NoError(t, err)
	require.Len(t, user2Bots, 1)
	require.Equal(t, "user2-bot1", user2Bots[0].Name)
}
