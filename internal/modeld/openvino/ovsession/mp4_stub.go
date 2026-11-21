//go:build !windows || !openvino || !openvino_genai

package ovsession

import (
	"context"

	"github.com/contenox/runtime/internal/transport"
)

func encodeMP4(ctx context.Context, rgb []byte, s transport.MediaSettings) ([]byte, error) {
	return encodePortableMP4(ctx, rgb, s)
}
