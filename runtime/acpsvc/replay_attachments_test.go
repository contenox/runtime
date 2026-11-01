package acpsvc

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

func TestLoopback_ReplayRestoresTheUsersImagesAndAudio(t *testing.T) {
	h := newLoopbackHarness(t)
	png := []byte{0x89, 'P', 'N', 'G'}
	wav := []byte("RIFF")

	h.tr.replayMessages(context.Background(), "sess-1", []taskengine.Message{
		{Role: "user", Content: "what is this?", Images: []taskengine.ImagePart{{Data: png, MimeType: "image/png"}}},
		{Role: "user", Audio: []taskengine.AudioPart{{Data: wav, MimeType: "audio/wav"}}},
	}, false)

	var got []libacp.SessionUpdate
	deadline := time.After(2 * time.Second)
	for len(got) < 3 {
		select {
		case n := <-h.lc.updates:
			got = append(got, n.Update)
		case <-deadline:
			t.Fatalf("expected 3 replayed user chunks, got %d", len(got))
		}
	}

	require.Equal(t, "what is this?", got[0].Content.Text)
	require.Equal(t, "replay-0", got[0].MessageID)

	require.Equal(t, libacp.SessionUpdateUserMessageChunk, got[1].SessionUpdate)
	require.Equal(t, "replay-0", got[1].MessageID, "the image belongs to the message it was sent with")
	require.Equal(t, "image/png", got[1].Content.MimeType)
	require.Equal(t, base64.StdEncoding.EncodeToString(png), got[1].Content.Data)

	require.Equal(t, "replay-1", got[2].MessageID, "an attachment-only message is not skipped")
	require.Equal(t, "audio/wav", got[2].Content.MimeType)
	require.Equal(t, base64.StdEncoding.EncodeToString(wav), got[2].Content.Data)
}
