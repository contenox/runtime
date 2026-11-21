package localaiapi

import (
	"bytes"
	"mime/multipart"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStageModelFiles(t *testing.T) {
	type file struct{ name, data string }
	for _, tc := range []struct {
		name, engine string
		files        []file
		valid        bool
	}{
		{"GGUF", "llama", []file{{"model.gguf", "GGUF\x03\x00\x00\x00weights"}}, true},
		{"HTML response", "llama", []file{{"model.gguf", "<html>login</html>"}}, false},
		{"truncated header", "llama", []file{{"model.gguf", "GG"}}, false},
		{"duplicate part", "llama", []file{{"model.gguf", "GGUF1234"}, {"model.gguf", "GGUF1234"}}, false},
		{"OpenVINO model", "openvino", []file{{"openvino_model.xml", "<net/>"}, {"openvino_model.bin", "weights"}}, true},
		{"missing weights", "openvino", []file{{"openvino_model.xml", "<net/>"}}, false},
		{"empty weights", "openvino", []file{{"openvino_model.xml", "<net/>"}, {"openvino_model.bin", ""}}, false},
		{"nested filename", "openvino", []file{{"nested/config.json", "{}"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			for _, part := range tc.files {
				field, err := writer.CreateFormFile(part.name, part.name)
				require.NoError(t, err)
				_, err = field.Write([]byte(part.data))
				require.NoError(t, err)
			}
			require.NoError(t, writer.Close())
			var copied int64
			err := stageModelFiles(dir, tc.engine, multipart.NewReader(&body, writer.Boundary()), func(n int64) { copied += n })
			if !tc.valid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			var expected int
			for _, part := range tc.files {
				saved, err := os.ReadFile(filepath.Join(dir, part.name))
				require.NoError(t, err)
				require.Equal(t, part.data, string(saved))
				expected += len(saved)
			}
			require.EqualValues(t, expected, copied)
		})
	}
}
