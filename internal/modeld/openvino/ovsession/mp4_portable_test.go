package ovsession

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"testing"

	"github.com/contenox/runtime/internal/transport"
)

func TestPortableMP4Samples(t *testing.T) {
	s := transport.MediaSettings{Width: 128, Height: 128, Frames: 8, FPS: 8}
	rgb := make([]byte, s.Width*s.Height*s.Frames*3)
	for f := 0; f < s.Frames; f++ {
		for y := 0; y < s.Height; y++ {
			for x := 0; x < s.Width; x++ {
				i := 3 * ((f*s.Height+y)*s.Width + x)
				rgb[i], rgb[i+1], rgb[i+2] = byte(x+f*12), byte(y), byte(f*20)
			}
		}
	}
	data, err := encodePortableMP4(t.Context(), rgb, s)
	if err != nil {
		t.Fatal(err)
	}
	table := func(name string) []byte {
		i := bytes.LastIndex(data, []byte(name))
		if i < 4 {
			t.Fatalf("missing %s", name)
		}
		end := i - 4 + int(binary.BigEndian.Uint32(data[i-4:]))
		if end > len(data) || end < i+4 {
			t.Fatalf("invalid %s size", name)
		}
		return data[i+4 : end]
	}
	stsz, stco, stts := table("stsz"), table("stco"), table("stts")
	if count := binary.BigEndian.Uint32(stsz[8:]); count != uint32(s.Frames) || binary.BigEndian.Uint32(stts[8:]) != count || binary.BigEndian.Uint32(stts[12:]) != 1 {
		t.Fatal("unexpected sample count or duration")
	}
	offset := int(binary.BigEndian.Uint32(stco[8:]))
	for i := 0; i < s.Frames; i++ {
		size := int(binary.BigEndian.Uint32(stsz[12+4*i:]))
		if size != 4+int(binary.BigEndian.Uint32(data[offset:])) || data[offset+4] != 0x65 {
			t.Fatalf("invalid AVC sample %d", i)
		}
		offset += size
	}
	if string(data[offset+4:offset+8]) != "moov" {
		t.Fatal("chunk does not end at movie header")
	}
	if dest := os.Getenv("CONTENOX_TEST_PORTABLE_MP4"); dest != "" {
		if err := os.WriteFile(dest, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := encodePortableMP4(ctx, rgb, s); !errors.Is(err, context.Canceled) {
		t.Fatal("expected cancellation")
	}
}
