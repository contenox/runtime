// Package turninput defines the handoff between a live conversation and a model call.
package turninput

type Message struct {
	BlockCount int // Number of ACP content blocks before projecting resources into text.
	ID         string
	Text       string
	Images     []ImagePart
	Audio      []AudioPart
}

type ImagePart struct {
	Data     []byte `json:"data"`
	MimeType string `json:"mime_type" example:"image/png"`
}

type AudioPart struct {
	Data     []byte `json:"data"`
	MimeType string `json:"mime_type" example:"audio/wav"`
}

// Pending snapshots queued messages in arrival order. Calling delivered
// acknowledges this batch only, leaving subsequent arrivals for the next call.
type Pending func() (messages []Message, delivered func())
