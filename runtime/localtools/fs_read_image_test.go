package localtools_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/contenox/runtime/runtime/localtools"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

func TestUnit_LocalFSTools_ReadImageAttachesAPNG(t *testing.T) {
	root := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	require.NoError(t, os.WriteFile(filepath.Join(root, "shot.png"), buf.Bytes(), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hello"), 0o644))

	h := localtools.NewLocalFSTools(root, nil)
	res, dt, err := h.Exec(context.Background(), time.Now(), map[string]any{"path": "shot.png"}, false, &taskengine.ToolsCall{ToolName: "read_image"})
	require.NoError(t, err)
	require.Equal(t, taskengine.DataTypeString, dt)
	out, ok := res.(*taskengine.ToolImageResult)
	require.True(t, ok)
	require.Contains(t, out.Text, "image/png")
	require.Contains(t, out.Text, "3x2")
	require.Len(t, out.Images, 1)
	require.Equal(t, buf.Bytes(), out.Images[0].Data)

	_, _, err = h.Exec(context.Background(), time.Now(), map[string]any{"path": "notes.txt"}, false, &taskengine.ToolsCall{ToolName: "read_image"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a PNG, JPEG, GIF or WebP image")
}

func TestUnit_LocalFSTools_ReadImageKeepsToTheAttachLimitAndTheReadCap(t *testing.T) {
	root := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	big := append(append([]byte{}, buf.Bytes()...), make([]byte, taskengine.MaxToolResultImageBytes)...)
	require.NoError(t, os.WriteFile(filepath.Join(root, "big.png"), big, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "small.png"), buf.Bytes(), 0o644))

	h := localtools.NewLocalFSTools(root, nil)
	_, _, err := h.Exec(context.Background(), time.Now(), map[string]any{"path": "big.png"}, false, &taskengine.ToolsCall{ToolName: "read_image"})
	require.Error(t, err, "an image the engine would drop is refused up front")
	require.Contains(t, err.Error(), "images up to 5 MiB can be attached")

	capped := taskengine.WithToolsArgs(context.Background(), localtools.LocalFSToolsName, map[string]string{"_max_read_bytes": "10"})
	_, _, err = h.Exec(capped, time.Now(), map[string]any{"path": "small.png"}, false, &taskengine.ToolsCall{ToolName: "read_image"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "read cap")
	require.Contains(t, err.Error(), "_max_read_bytes")
}
