package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/js402/cate/core/serverops/store"
	"github.com/stretchr/testify/require"
)

func TestMessages(t *testing.T) {
	ctx, s := store.SetupStore(t)
	err := s.CreateUser(ctx, &store.User{
		ID:           uuid.NewString(),
		FriendlyName: "John Doe",
		Email:        "admin@admin.com",
		Subject:      "my-users-id",
	})
	require.NoError(t, err)
	idxID := uuid.NewString()
	err = s.CreateMessageIndex(ctx, idxID, "my-users-id")
	t.Run("list Empty", func(t *testing.T) {
		messages, err := s.ListMessages(context.Background(), "invalid-stream")
		require.NoError(t, err)
		require.Empty(t, messages)
	})
	t.Run("Add and check message", func(t *testing.T) {
		id := uuid.NewString()
		err := s.AppendMessage(ctx, &store.Message{
			ID:      id,
			IDX:     idxID,
			Payload: []byte("{}"),
		})
		require.NoError(t, err)
		messages, err := s.ListMessages(context.Background(), idxID)
		require.NoError(t, err)
		require.Len(t, messages, 1)
		require.Equal(t, id, messages[0].ID)
		require.WithinDuration(t, time.Now(), messages[0].AddedAt, time.Second)
		err = s.DeleteMessages(context.Background(), idxID)
		require.NoError(t, err)
		messages, err = s.ListMessages(context.Background(), idxID)
		require.NoError(t, err)
		require.Empty(t, messages)
	})
}
