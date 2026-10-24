package ollama_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/ollama"
	"github.com/stretchr/testify/require"
)

var mp3WithID3 = []byte{0x49, 0x44, 0x33, 0x04, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}

func audioPeer(t *testing.T) (*httptest.Server, func() []byte) {
	t.Helper()
	bodies := make(chan []byte, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		bodies <- body
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte(`{"model":"m","message":{"role":"assistant","content":"ok"},"done":true,"done_reason":"stop"}` + "\n"))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []byte {
		select {
		case body := <-bodies:
			return body
		default:
			return nil
		}
	}
}

func audioProvider(srv *httptest.Server) modelrepo.Provider {
	return ollama.NewOllamaProvider("m", []string{srv.URL}, srv.Client(), modelrepo.CapabilityConfig{
		CanChat: true, CanStream: true, CanAudio: true, AudioExtension: true,
	}, "", nil)
}

func TestUnit_Chat_SendsAnyOfTheSupportedAudioFormats(t *testing.T) {
	srv, sent := audioPeer(t)
	conn, err := audioProvider(srv).GetChatConnection(context.Background(), srv.URL)
	require.NoError(t, err)

	_, err = conn.Chat(context.Background(), []modelrepo.Message{
		{Role: "user", Content: "transcribe this", Audio: []modelrepo.AudioPart{{Data: mp3WithID3, MimeType: "audio/mpeg"}}},
	})
	require.NoError(t, err)

	var body struct {
		Messages []struct {
			Audios []string `json:"audios"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(sent(), &body))
	require.Equal(t, []string{base64.StdEncoding.EncodeToString(mp3WithID3)}, body.Messages[0].Audios,
		"the bytes cross as they are: the peer names the format from them")
}

func TestUnit_Chat_RefusesAudioBytesThatAreNoSupportedFormatBeforeTheWire(t *testing.T) {
	srv, sent := audioPeer(t)
	conn, err := audioProvider(srv).GetChatConnection(context.Background(), srv.URL)
	require.NoError(t, err)

	_, err = conn.Chat(context.Background(), []modelrepo.Message{
		{Role: "user", Content: "transcribe this", Audio: []modelrepo.AudioPart{{Data: []byte("plain text"), MimeType: "audio/wav"}}},
	})
	require.ErrorIs(t, err, modelrepo.ErrUnsupportedAudioMime)
	require.Contains(t, err.Error(), "audio/flac", "the refusal lists what would have been accepted")
	require.Nil(t, sent(), "nothing reached the peer")

	_, err = conn.Chat(context.Background(), []modelrepo.Message{
		{Role: "user", Content: "transcribe this", Audio: []modelrepo.AudioPart{{Data: mp3WithID3, MimeType: "audio/aac"}}},
	})
	require.ErrorIs(t, err, modelrepo.ErrUnsupportedAudioMime, "an unsupported declared type is refused as before")
	require.Nil(t, sent())
}

func TestUnit_Stream_RefusesAudioBytesThatAreNoSupportedFormatBeforeTheWire(t *testing.T) {
	srv, sent := audioPeer(t)
	conn, err := audioProvider(srv).GetStreamConnection(context.Background(), srv.URL)
	require.NoError(t, err)

	stream, err := conn.Stream(context.Background(), []modelrepo.Message{
		{Role: "user", Content: "transcribe this", Audio: []modelrepo.AudioPart{{Data: []byte{}, MimeType: "audio/wav"}}},
	})
	require.ErrorIs(t, err, modelrepo.ErrUnsupportedAudioMime)
	require.Nil(t, stream)
	require.Nil(t, sent(), "nothing reached the peer")
}
