// Package diffusion identifies the component graphs of native OpenVINO media exports.
package diffusion

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/contenox/runtime/internal/transport"
)

// Pipeline identifies a complete OpenVINO GenAI export. Language and
// vision-language models use a root IR; diffusion exports use component IRs.
func Pipeline(dir string) (string, error) {
	f, err := os.Open(filepath.Join(dir, "model_index.json"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	var index struct {
		Class string `json:"_class_name"`
	}
	if err := json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&index); err != nil {
		return "", fmt.Errorf("%w: invalid model_index.json: %v", transport.ErrModelNotFound, err)
	}
	denoiser := ""
	switch index.Class {
	case "StableDiffusionPipeline":
		denoiser = "unet"
	case "LTXPipeline":
		denoiser = "transformer"
	default:
		return "", nil
	}
	required := []string{"scheduler/scheduler_config.json"}
	for _, component := range []string{"text_encoder", denoiser, "vae_decoder", "vae_encoder"} {
		for _, file := range []string{"config.json", "openvino_model.xml", "openvino_model.bin"} {
			required = append(required, filepath.Join(component, file))
		}
	}
	for _, file := range []string{"openvino_tokenizer.xml", "openvino_tokenizer.bin", "openvino_detokenizer.xml", "openvino_detokenizer.bin"} {
		required = append(required, filepath.Join("tokenizer", file))
	}
	for _, file := range required {
		info, err := os.Lstat(filepath.Join(dir, file))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return "", fmt.Errorf("%w: %s requires a nonempty %s", transport.ErrModelNotFound, index.Class, file)
		}
	}
	return index.Class, nil
}
