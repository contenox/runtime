//go:build openvino && openvino_genai

package ovsession

import (
	"flag"
	"os"
	"testing"

	"github.com/contenox/runtime/internal/transport"
)

var videoTestModel = flag.String("video-model", "", "installed LTX export for the native pipeline test")

func TestInstalledVideoPipeline(t *testing.T) {
	if *videoTestModel == "" {
		t.Skip("supply -video-model with a local LTX bundle")
	}
	settings := transport.MediaSettings{Prompt: "A sailboat at sea", Width: 256, Height: 256, Frames: 9, FPS: 8, Steps: 2, Seed: 42}
	pixels, err := RenderMedia(t.Context(), *videoTestModel, "CPU", os.Getenv("OPENVINO_TOKENIZERS_PATH_GENAI"), settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = EncodeMedia(t.Context(), pixels, settings); err != nil {
		t.Fatal(err)
	}
}
