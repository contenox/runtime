package llmresolver

import (
	"github.com/contenox/runtime/libtracker"
)

type Request struct {
	ProviderTypes     []string
	ModelNames        []string
	ContextLength     int
	FitContextToModel bool
	RequiresVision    bool
	RequiresAudio     bool
	Tracker           libtracker.ActivityTracker
}

type EmbedRequest struct {
	ModelName    string
	ProviderType string
	Tracker      libtracker.ActivityTracker
}
