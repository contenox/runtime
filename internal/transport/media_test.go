package transport

import "testing"

func TestMediaFrameBudgetIncludesModelPadding(t *testing.T) {
	s := MediaSettings{Prompt: "lake", Steps: 2, Width: 768, Height: 768, FPS: 7, Frames: 32}
	if err := s.Validate(); err != nil || s.InferenceFrames() != 33 {
		t.Fatalf("valid padded render: %v", err)
	}
	for _, frames := range []int{34, 24*60*60*7 + 1, int(^uint(0) >> 1)} {
		s.Frames = frames
		if s.Validate() == nil {
			t.Fatalf("accepted %d frames at maximum size", frames)
		}
	}
}

func TestMediaRejectsSingleStep(t *testing.T) {
	settings := MediaSettings{Prompt: "cup", Width: 256, Height: 256, Frames: 1, Steps: 1}
	if settings.Validate() == nil {
		t.Fatal("PNDM needs at least two timesteps")
	}
	settings.Frames, settings.FPS = 9, 8
	if settings.Validate() == nil {
		t.Fatal("LTX needs two timesteps for the terminal shift")
	}
	settings.Steps = 2
	if err := settings.Validate(); err != nil {
		t.Fatal(err)
	}
}
