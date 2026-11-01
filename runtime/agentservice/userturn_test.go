package agentservice

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/chatservice"
	"github.com/contenox/runtime/runtime/messagestore"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

func newUserTurnTestAgent(t *testing.T) (*agent, context.Context, string) {
	t.Helper()
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "userturn.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	sessionID := "session-1"
	require.NoError(t, messagestore.New(db.WithoutTransaction(), "ws").CreateMessageIndex(ctx, sessionID, "local-user"))
	return &agent{deps: Deps{DB: db, WorkspaceID: "ws", Identity: "local-user"}}, ctx, sessionID
}

func storedMessages(t *testing.T, a *agent, ctx context.Context, sessionID string) []taskengine.Message {
	t.Helper()
	msgs, err := chatservice.NewManager("ws").ListMessages(ctx, a.deps.DB.WithoutTransaction(), sessionID)
	require.NoError(t, err)
	return msgs
}

func TestUnit_PersistUserTurn_SavesTheMessageBeforeTheModelAnswers(t *testing.T) {
	a, ctx, sessionID := newUserTurnTestAgent(t)
	img := taskengine.ImagePart{MimeType: "image/png", Data: []byte{0x89, 'P', 'N', 'G'}}
	input := taskengine.ChatHistory{Messages: []taskengine.Message{{
		ID: "u1", Role: "user", Content: "what is in this image?", Images: []taskengine.ImagePart{img}, Timestamp: time.Now().UTC(),
	}}}

	id := a.persistUserTurn(ctx, sessionID, input, "chain.json")

	require.Equal(t, "u1", id)
	msgs := storedMessages(t, a, ctx, sessionID)
	require.Len(t, msgs, 1, "a turn that never returns must not lose what the user sent")
	require.Equal(t, "what is in this image?", msgs[0].Content)
	require.Len(t, msgs[0].Images, 1, "the attachment is part of what the user sent")
}

func TestUnit_PersistUserTurn_FinalPersistDoesNotDuplicateIt(t *testing.T) {
	a, ctx, sessionID := newUserTurnTestAgent(t)
	user := taskengine.Message{ID: "u1", Role: "user", Content: "hi", Timestamp: time.Now().UTC()}
	input := taskengine.ChatHistory{Messages: []taskengine.Message{user}}
	a.persistUserTurn(ctx, sessionID, input, "")

	a.persistHistory(ctx, sessionID, input, nil, nil, "")

	require.Len(t, storedMessages(t, a, ctx, sessionID), 1)
}

func TestUnit_ForgetUserTurn_PoisonPillLeavesTheSessionAsItWas(t *testing.T) {
	a, ctx, sessionID := newUserTurnTestAgent(t)
	earlier := taskengine.Message{ID: "u0", Role: "user", Content: "earlier", Timestamp: time.Now().UTC().Add(-time.Minute)}
	reply := taskengine.Message{ID: "a0", Role: "assistant", Content: "ok", Timestamp: time.Now().UTC().Add(-time.Minute)}
	require.NoError(t, chatservice.NewManager("ws").PersistDiff(ctx, a.deps.DB.WithoutTransaction(), sessionID, []taskengine.Message{earlier, reply}))
	huge := taskengine.Message{ID: "u1", Role: "user", Content: "far too large", Timestamp: time.Now().UTC()}
	id := a.persistUserTurn(ctx, sessionID, taskengine.ChatHistory{Messages: []taskengine.Message{earlier, reply, huge}}, "")

	a.forgetUserTurn(ctx, sessionID, id)

	msgs := storedMessages(t, a, ctx, sessionID)
	require.Len(t, msgs, 2, "an input rejected to protect the session must not stay in it")
	require.Equal(t, "u0", msgs[0].ID)
	require.Equal(t, "a0", msgs[1].ID)
}

func TestUnit_PersistUserTurn_IgnoresNonUserTail(t *testing.T) {
	a, ctx, sessionID := newUserTurnTestAgent(t)
	input := taskengine.ChatHistory{Messages: []taskengine.Message{{ID: "s1", Role: "system", Content: "x"}}}

	require.Empty(t, a.persistUserTurn(ctx, sessionID, input, ""))
	require.Empty(t, storedMessages(t, a, ctx, sessionID))
}
