//go:build windows && openvino && openvino_genai

package ovsession

import (
	"bytes"
	"flag"
	"os"
	"testing"

	"github.com/contenox/runtime/internal/transport"
)

var codecSample = flag.String("codec-sample", "", "optional destination for independent MP4 decoder verification")

func TestWindowsMediaFoundationEncoding(t *testing.T) {
	settings := transport.MediaSettings{Prompt: "encoder test pattern", Width: 128, Height: 128, Frames: 17, FPS: 8, Steps: 2}
	frames := make([]byte, settings.Width*settings.Height*settings.Frames*3)
	for frame := 0; frame < settings.Frames; frame++ {
		for y := 0; y < settings.Height; y++ {
			for x := 0; x < settings.Width; x++ {
				i := 3 * ((frame*settings.Height+y)*settings.Width + x)
				frames[i] = byte(x + frame*8)
				frames[i+1] = byte(y)
			}
		}
	}
	data, mime, err := EncodeMedia(t.Context(), frames, settings)
	if err != nil {
		t.Fatal(err)
	}
	if mime != "video/mp4" || !bytes.Contains(data, []byte("ftyp")) || !bytes.Contains(data, []byte("moov")) {
		t.Fatal("incomplete MP4")
	}
	if *codecSample != "" {
		if err := os.WriteFile(*codecSample, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
