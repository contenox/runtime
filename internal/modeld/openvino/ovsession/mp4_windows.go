//go:build windows && openvino && openvino_genai

package ovsession

/*
#cgo LDFLAGS: -lmfplat -lmfreadwrite -lmfuuid -lole32
#include "media.h"
*/
import "C"

import (
	"context"
	"fmt"
	"os"
	"runtime/cgo"
	"unsafe"

	"github.com/contenox/runtime/internal/transport"
	"golang.org/x/sys/windows"
)

func encodeMP4(ctx context.Context, rgb []byte, s transport.MediaSettings) ([]byte, error) {
	file, err := os.CreateTemp("", "contenox-video-*.mp4")
	if err != nil {
		return nil, err
	}
	defer os.Remove(file.Name())
	if err = file.Close(); err != nil {
		return nil, err
	}
	path, err := windows.UTF16PtrFromString(file.Name())
	if err != nil {
		return nil, err
	}
	handle := cgo.NewHandle(ctx)
	defer handle.Delete()
	hr := C.cx_ov_encode_mp4((*C.uint16_t)(unsafe.Pointer(path)), (*C.uint8_t)(unsafe.Pointer(&rgb[0])),
		C.size_t(len(rgb)), nativeMediaSettings(s), C.uintptr_t(handle))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if hr != 0 {
		return nil, fmt.Errorf("Windows H.264 encoder failed (HRESULT 0x%08x)", uint32(hr))
	}
	return os.ReadFile(file.Name())
}
