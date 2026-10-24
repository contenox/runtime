package taskengine

import (
	"testing"

	libmodelprovider "github.com/contenox/runtime/runtime/modelrepo"
	"github.com/stretchr/testify/require"
)

func TestUnit_ProviderMessagesFromEngine_ThreadsAudio(t *testing.T) {
	t.Parallel()

	prelude := []Message{
		{Role: "system", Content: "you are helpful"},
	}
	wav := []byte{'R', 'I', 'F', 'F'}
	messages := []Message{
		{
			Role:    "user",
			Content: "what is said in this recording?",
			Audio: []AudioPart{
				{Data: wav, MimeType: "audio/wav"},
			},
		},
	}

	got := providerMessagesFromEngine(prelude, messages)
	require.Len(t, got, 2)

	require.Equal(t, "system", got[0].Role)
	require.Empty(t, got[0].Audio)

	require.Equal(t, "user", got[1].Role)
	require.Equal(t, "what is said in this recording?", got[1].Content)
	require.Len(t, got[1].Audio, 1)
	require.Equal(t, wav, got[1].Audio[0].Data)
	require.Equal(t, "audio/wav", got[1].Audio[0].MimeType)

	require.True(t, libmodelprovider.MessagesHaveAudio(got))
}

func TestUnit_ProviderMessagesFromEngine_TextOnlyStaysAudioFree(t *testing.T) {
	t.Parallel()

	got := providerMessagesFromEngine(nil, []Message{
		{Role: "user", Content: "plain text"},
	})
	require.Len(t, got, 1)
	require.Nil(t, got[0].Audio)
	require.False(t, libmodelprovider.MessagesHaveAudio(got))
}
