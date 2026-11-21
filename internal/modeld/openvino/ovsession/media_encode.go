package ovsession

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"

	"github.com/contenox/runtime/internal/transport"
)

func EncodeMedia(ctx context.Context, rgb []byte, s transport.MediaSettings) ([]byte, string, error) {
	if err := s.Validate(); err != nil {
		return nil, "", err
	}
	if len(rgb) != s.Width*s.Height*s.Frames*3 {
		return nil, "", errors.New("incomplete generated frames")
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if s.Frames > 1 {
		// LTX samples both endpoints. The frame at t=duration belongs to the next shot.
		s.Frames--
		data, err := encodeMP4(ctx, rgb[:s.Width*s.Height*s.Frames*3], s)
		return data, "video/mp4", err
	}
	picture := image.NewRGBA(image.Rect(0, 0, s.Width, s.Height))
	for from, to := 0, 0; from < len(rgb); from, to = from+3, to+4 {
		copy(picture.Pix[to:to+3], rgb[from:from+3])
		picture.Pix[to+3] = 255
	}
	var encoded bytes.Buffer
	err := png.Encode(&encoded, picture)
	return encoded.Bytes(), "image/png", err
}
