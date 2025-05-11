package store_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/js402/cate/core/serverops/store"
	"github.com/stretchr/testify/require"
)

func TestMessages(t *testing.T) {
	ctx, s := store.SetupStore(t)
	userID := uuid.NewString()

	err := s.CreateUser(ctx, &store.User{
		ID:           userID,
		FriendlyName: "John Doe",
		Email:        "admin@admin.com",
		Subject:      "my-users-id",
	})
	require.NoError(t, err)

	idxID := uuid.NewString()
	err = s.CreateMessageIndex(ctx, idxID, "my-users-id")
	require.NoError(t, err)

	tests := []struct {
		name      string
		setup     func(t *testing.T)
		stream    string
		wantCount int
		cleanup   bool
	}{
		{
			name:      "List empty messages",
			stream:    "non-existent-stream",
			wantCount: 0,
		},
		{
			name:   "Add single message",
			stream: idxID,
			setup: func(t *testing.T) {
				err := s.AppendMessages(ctx, &store.Message{
					ID:      uuid.NewString(),
					IDX:     idxID,
					Payload: []byte(`{"event":"test"}`),
				})
				require.NoError(t, err)
			},
			wantCount: 1,
			cleanup:   true,
		},
		{
			name:   "Add multiple messages",
			stream: idxID,
			setup: func(t *testing.T) {
				err := s.AppendMessages(ctx,
					&store.Message{ID: uuid.NewString(), IDX: idxID, Payload: []byte(`{"bulk":1}`)},
					&store.Message{ID: uuid.NewString(), IDX: idxID, Payload: []byte(`{"bulk":2}`)},
					&store.Message{ID: uuid.NewString(), IDX: idxID, Payload: []byte(`{"bulk":3}`)},
				)
				require.NoError(t, err)
			},
			wantCount: 3,
			cleanup:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup(t)
			}

			msgs, err := s.ListMessages(ctx, tt.stream)
			require.NoError(t, err)
			require.Len(t, msgs, tt.wantCount)

			for _, m := range msgs {
				require.WithinDuration(t, time.Now(), m.AddedAt, time.Second)
			}

			if tt.cleanup {
				require.NoError(t, s.DeleteMessages(ctx, tt.stream))
				msgs, err := s.ListMessages(ctx, tt.stream)
				require.NoError(t, err)
				require.Empty(t, msgs)
			}
		})
	}
}
