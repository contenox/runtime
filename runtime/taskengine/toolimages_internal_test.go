package taskengine

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnit_KeepNewestToolImages_LeavesUserImagesAndTheInputAlone(t *testing.T) {
	img := []ImagePart{{Data: []byte{1}, MimeType: "image/png"}}
	in := []Message{
		{Role: "user", Content: "this", Images: img},
		{Role: "tool", Content: "old", Images: img},
		{Role: "tool", Content: "new", Images: img},
	}
	out := keepNewestToolImages(in, 1)
	require.Len(t, out[0].Images, 1, "a user's own attachment is never trimmed")
	require.Empty(t, out[1].Images)
	require.Contains(t, out[1].Content, "1 earlier image(s)")
	require.Len(t, out[2].Images, 1)
	require.Len(t, in[1].Images, 1, "the caller's history is not changed")
	require.Equal(t, "old", in[1].Content)
}
