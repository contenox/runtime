package modelstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDiffusionInventoryRequiresAllComponentWeights(t *testing.T) {
	for _, tc := range []struct{ class, denoiser string }{{"StableDiffusionPipeline", "unet"}, {"LTXPipeline", "transformer"}} {
		t.Run(tc.class, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "local-export")
			write := func(name, content string) {
				file := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write("model_index.json", `{"_class_name":"`+tc.class+`"}`)
			write("scheduler/scheduler_config.json", "{}")
			for _, part := range []string{"text_encoder", tc.denoiser, "vae_encoder", "vae_decoder"} {
				for _, name := range []string{"config.json", "openvino_model.xml", "openvino_model.bin"} {
					write(part+"/"+name, "test data")
				}
			}
			for _, name := range []string{"openvino_tokenizer.xml", "openvino_tokenizer.bin", "openvino_detokenizer.xml"} {
				write("tokenizer/"+name, "test data")
			}
			if _, err := Resolve(root, "local-export", "openvino", ""); !errors.Is(err, ErrModelNotFound) {
				t.Fatalf("incomplete export is available: %v", err)
			}
			write("tokenizer/openvino_detokenizer.bin", "test data")
			write("tokenizer/openvino_tokenizer.xml", "<net><layers/></net>")
			if tc.class == "LTXPipeline" {
				write("transformer/openvino_model.xml", `<net><layers><layer name="timestep" type="Parameter"><data shape="?"/></layer></layers></net>`)
			}
			admin := NewNodeAdmin(root)
			inventory, err := admin.ListModels(context.Background())
			if err != nil || len(inventory) != 1 {
				t.Fatalf("list complete export: %v, %v", inventory, err)
			}
			model := inventory[0]
			if model.MediaError != "" || model.Pipeline != tc.class || model.Type != "openvino" || model.SizeBytes == 0 {
				t.Fatalf("wrong inventory: %+v", model)
			}
			if got, err := Resolve(root, model.Name, model.Type, model.Digest); err != nil || got != dir {
				t.Fatalf("resolve installed export: %q, %v", got, err)
			}
			write(tc.denoiser+"/openvino_model.bin", "changed weights")
			if _, err := Resolve(root, model.Name, model.Type, model.Digest); !errors.Is(err, ErrDigestMismatch) {
				t.Fatalf("changed weights accepted: %v", err)
			}
		})
	}
}
