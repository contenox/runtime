package transport

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Each inference role selects one model. Still and moving images can use different architectures.
const (
	ImageRole          = "image-generation"
	VideoRole          = "video-generation"
	MinMediaDimension  = 128
	MaxMediaDimension  = 768
	MediaDimensionStep = 32
	MaxMediaFPS        = 30
	MaxMediaPixels     = MaxMediaDimension * MaxMediaDimension * 33
	videoFrameGroup    = 8
)

func MediaRoleForPipeline(pipeline string) string {
	switch pipeline {
	case "StableDiffusionPipeline":
		return ImageRole
	case "LTXPipeline":
		return VideoRole
	}
	return ""
}

func (s MediaSettings) Role() string {
	if s.Frames > 1 {
		return VideoRole
	}
	return ImageRole
}

// MediaSettings describes one bounded diffusion inference operation. Pixel
// dimensions and frame count are explicit so both ends can bound allocations.
type MediaSettings struct {
	Prompt string `json:"prompt"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Frames int    `json:"frames"`
	FPS    int    `json:"fps"`
	Steps  int    `json:"steps"`
	Seed   uint32 `json:"seed"`
}

func (s MediaSettings) InferenceFrames() int {
	outputFrames := s.Frames - 1
	groups := (outputFrames + videoFrameGroup - 1) / videoFrameGroup
	return groups*videoFrameGroup + 1
}

func (s MediaSettings) Validate() error {
	if strings.TrimSpace(s.Prompt) == "" || len(s.Prompt) > 4000 || strings.ContainsRune(s.Prompt, 0) {
		return errors.New("enter a scene description of up to 4000 bytes")
	}
	// The SD PNDM scheduler reads the penultimate timestep. The LTX flow
	// scheduler also needs two timesteps to compute a finite terminal shift.
	if s.Steps < 2 || s.Steps > 80 {
		return errors.New("sampling steps must be between 2 and 80")
	}
	for _, dimension := range []int{s.Width, s.Height} {
		if dimension < MinMediaDimension || dimension > MaxMediaDimension || dimension%MediaDimensionStep != 0 {
			return fmt.Errorf("media dimensions must be multiples of %d between %d and %d", MediaDimensionStep, MinMediaDimension, MaxMediaDimension)
		}
	}
	if s.Frames == 1 {
		return nil
	}
	if s.FPS < 1 || s.FPS > MaxMediaFPS || s.Frames < 2 {
		return fmt.Errorf("video requires at least one output frame and 1–%d frames per second", MaxMediaFPS)
	}
	frameBudget := MaxMediaPixels / (s.Width * s.Height)
	maxOutputFrames := (frameBudget - 1) / videoFrameGroup * videoFrameGroup
	if s.Frames-1 > maxOutputFrames {
		return fmt.Errorf("at %dx%d the render budget allows up to %d output frames", s.Width, s.Height, maxOutputFrames)
	}
	return nil
}

type MediaRequest struct {
	OpenSessionRequest
	Settings MediaSettings
}

type MediaResult struct {
	Data      []byte            `json:"data"`
	MIME      string            `json:"mime"`
	Execution *SessionExecution `json:"execution,omitempty"`
}

// MediaRenderer is an optional, independently negotiated worker operation.
type MediaRenderer interface {
	RenderMedia(context.Context, MediaRequest) (MediaResult, error)
}
