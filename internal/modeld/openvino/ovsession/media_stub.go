//go:build !openvino || !openvino_genai

package ovsession

import (
	"context"

	"github.com/contenox/runtime/internal/transport"
)

func RenderMedia(context.Context, string, string, string, transport.MediaSettings) ([]byte, error) {
	return nil, transport.ErrUnsupportedFeature
}
