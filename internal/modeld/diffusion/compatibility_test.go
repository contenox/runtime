package diffusion

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMediaGraphCompatibility(t *testing.T) {
	dir := t.TempDir()
	write := func(path, data string) {
		file := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	tokenizer := `<net><layers><layer id="0" type="Select"/><layer id="1" type="Truncate"><input><port id="0"/><port id="1"/><port id="2"/><port id="3"/><port id="4"/><port id="5"/></input></layer></layers><edges><edge from-layer="0" to-layer="1" to-port="3"/></edges></net>`
	write("tokenizer/openvino_tokenizer.xml", tokenizer)
	transformer := `<net><layers><layer name="timestep" type="Parameter"><data shape="?,?"/></layer></layers></net>`
	write("transformer/openvino_model.xml", transformer)
	if err := CheckCompatibility(dir, "StableDiffusionPipeline"); err == nil || !strings.Contains(err.Error(), "truncation") {
		t.Fatalf("expected image tokenizer incompatibility: %v", err)
	}
	if err := CheckCompatibility(dir, "LTXPipeline"); err != nil {
		t.Fatal(err)
	}
	write("transformer/openvino_model.xml", strings.Replace(transformer, "?,?", "?,?,?", 1))
	if err := CheckCompatibility(dir, "LTXPipeline"); err == nil {
		t.Fatal("accepted a rank-three timestep")
	}
	write("transformer/openvino_model.xml", strings.Replace(transformer, "?,?", "?", 1))
	if err := CheckCompatibility(dir, "LTXPipeline"); err != nil {
		t.Fatal(err)
	}
	if needs, err := NeedsLTXAdapter(dir); err != nil || !needs {
		t.Fatalf("stateful tokenizer requires LTX adapter: needs=%v err=%v", needs, err)
	}
	write("tokenizer/openvino_tokenizer.xml", strings.Replace(tokenizer, "Select", "Const", 1))
	if needs, err := NeedsLTXAdapter(dir); err != nil || needs {
		t.Fatalf("compatible export should use Text2VideoPipeline: needs=%v err=%v", needs, err)
	}
	write("transformer/openvino_model.xml", transformer)
	if needs, err := NeedsLTXAdapter(dir); err != nil || !needs {
		t.Fatalf("per-token timesteps require LTX adapter: needs=%v err=%v", needs, err)
	}
}
