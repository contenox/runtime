package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/stretchr/testify/require"
)

func TestUnit_OpenAIChat_RefusesAudioBeforeWire(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("audio-bearing request must be refused before any HTTP call")
	}))
	defer srv.Close()

	client := newGPT5ChatClient(t, srv.URL)
	msgs := []modelrepo.Message{
		{Role: "user", Content: "transcribe", Audio: []modelrepo.AudioPart{{Data: []byte{1, 2}, MimeType: "audio/wav"}}},
	}

	_, err := client.Chat(context.Background(), msgs)
	require.ErrorIs(t, err, modelrepo.ErrAudioNotSupported)
	require.Contains(t, err.Error(), "openai")
	require.Contains(t, err.Error(), "gpt-5")

	streamClient := &OpenAIStreamClient{openAIClient: client.openAIClient}
	_, err = streamClient.Stream(context.Background(), msgs)
	require.ErrorIs(t, err, modelrepo.ErrAudioNotSupported)
}

func TestUnit_OpenAIChat_EncodesAudioWhenCapabilityPresent(t *testing.T) {
	t.Parallel()

	var got struct {
		Messages []struct {
			Content []struct {
				Type       string `json:"type"`
				InputAudio *struct {
					Data   string `json:"data"`
					Format string `json:"format"`
				} `json:"input_audio"`
			} `json:"content"`
		} `json:"messages"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %s, want /chat/completions", r.URL.Path)
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],"usage":{}}`)
	}))
	defer srv.Close()

	client := &OpenAIChatClient{openAIClient: openAIClient{
		baseURL:    srv.URL,
		apiKey:     "key",
		httpClient: http.DefaultClient,
		modelName:  "o1-mini",
		canAudio:   true,
		tracker:    libtracker.NoopTracker{},
	}}
	audio := []byte{1, 2, 3}
	_, err := client.Chat(context.Background(), []modelrepo.Message{
		{Role: "user", Content: "transcribe", Audio: []modelrepo.AudioPart{{Data: audio, MimeType: "audio/wav"}}},
	})
	require.NoError(t, err)
	require.Len(t, got.Messages, 1)
	require.Len(t, got.Messages[0].Content, 2)
	part := got.Messages[0].Content[1]
	require.Equal(t, "input_audio", part.Type)
	require.NotNil(t, part.InputAudio)
	require.Equal(t, "wav", part.InputAudio.Format)
	require.Equal(t, base64.StdEncoding.EncodeToString(audio), part.InputAudio.Data)
}
