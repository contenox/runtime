//go:build openvino && openvino_genai

package ovsession

/*
#include <stdlib.h>
#include "media.h"
*/
import "C"

import (
	"context"
	"fmt"
	"runtime/cgo"
	"unsafe"

	"github.com/contenox/runtime/internal/modeld/diffusion"
	"github.com/contenox/runtime/internal/transport"
)

//export cx_ov_media_cancelled
func cx_ov_media_cancelled(handle C.uintptr_t) C.int {
	if cgo.Handle(handle).Value().(context.Context).Err() != nil {
		return 1
	}
	return 0
}

func nativeMediaSettings(settings transport.MediaSettings) C.cx_ov_media_settings {
	return C.cx_ov_media_settings{
		width: C.int(settings.Width), height: C.int(settings.Height), frames: C.int(settings.Frames),
		fps: C.int(settings.FPS), steps: C.int(settings.Steps), seed: C.uint32_t(settings.Seed),
	}
}

func RenderMedia(ctx context.Context, directory, device, tokenizerExtension string, settings transport.MediaSettings) ([]byte, error) {
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var useAdapter C.int
	if settings.Frames == 1 {
		if err := diffusion.CheckCompatibility(directory, "StableDiffusionPipeline"); err != nil {
			return nil, err
		}
	} else {
		needs, err := diffusion.NeedsLTXAdapter(directory)
		if err != nil {
			return nil, err
		}
		if needs {
			useAdapter = 1
		}
	}
	dir, target, prompt := C.CString(directory), C.CString(device), C.CString(settings.Prompt)
	tokenizers := C.CString(tokenizerExtension)
	defer C.free(unsafe.Pointer(tokenizers))
	defer C.free(unsafe.Pointer(dir))
	defer C.free(unsafe.Pointer(target))
	defer C.free(unsafe.Pointer(prompt))
	handle := cgo.NewHandle(ctx)
	defer handle.Delete()
	requestedBytes := settings.Width * settings.Height * settings.Frames * 3
	settings.Frames = settings.InferenceFrames()
	pixels := make([]byte, settings.Width*settings.Height*settings.Frames*3)
	var message [4096]C.char
	rc := C.cx_ov_render_media(dir, tokenizers, target, prompt, nativeMediaSettings(settings), useAdapter, C.uintptr_t(handle),
		(*C.uint8_t)(unsafe.Pointer(&pixels[0])), C.size_t(len(pixels)), &message[0], C.size_t(len(message)))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if rc != 0 {
		return nil, fmt.Errorf("OpenVINO media generation on %s: %s", device, C.GoString(&message[0]))
	}
	return pixels[:requestedBytes], nil
}
