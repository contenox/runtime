package mediageneration

import (
	"testing"

	"github.com/contenox/runtime/internal/transport"
	"github.com/stretchr/testify/require"
)

func TestRenderSettings(t *testing.T) {
	opts := Options{Prompt: "lake"}
	image, err := opts.settings(DefaultDimension, 24)
	require.NoError(t, err)
	require.Equal(t, [3]int{384, 384, 1}, [3]int{image.Width, image.Height, image.Frames})
	width, height, fps, seconds := 512, 320, 10, 5.5
	opts.OutputOptions = OutputOptions{Width: &width, Height: &height, FPS: &fps, Seconds: &seconds}
	_, err = opts.settings(DefaultDimension, 24)
	require.Error(t, err)
	opts.Video = true
	video, err := opts.settings(DefaultDimension, 24)
	require.NoError(t, err)
	require.Equal(t, [5]int{512, 320, 10, 55, 57}, [5]int{video.Width, video.Height, video.FPS, video.Frames - 1, video.InferenceFrames()})
	for _, duration := range []float64{0, -1, 5.55, 24 * 60 * 60, 1e300} {
		seconds = duration
		_, err := opts.settings(DefaultDimension, 24)
		require.Error(t, err)
	}
}

func TestRenderRequiresExplicitModelSelection(t *testing.T) {
	_, err := Render(t.Context(), Options{Prompt: "lake", Video: true})
	require.ErrorContains(t, err, "select a model for "+transport.VideoRole)
}
