//go:build openvino && openvino_genai

package ovsession_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/contenox/runtime/internal/modeld/openvino/ovsession"
)

func TestSystem_OpenVINOGenAI_StarCoderControlsAndLiteralSource(t *testing.T) {
	modelDir := os.Getenv("CONTENOX_OPENVINO_FIM_MODEL")
	if modelDir == "" {
		t.Skip("CONTENOX_OPENVINO_FIM_MODEL is not set")
	}
	session, err := ovsession.NewGenAI(modelDir, ovsession.GenAIConfig{
		Device:                    "CPU",
		LiteralSourceTokenizerDir: filepath.Join(modelDir, ovsession.LiteralSourceTokenizerDirectoryName),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Errorf("close OpenVINO GenAI session: %v", err)
		}
	})
	if err := session.ValidateLiteralSourceTokenizer("fim", "starcoder"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	controls := map[string]int{"<|endoftext|>": 0, "<fim_prefix>": 1, "<fim_suffix>": 3, "<fim_middle>": 2, "<fim_pad>": 4}
	controlIDs := make(map[int]struct{}, len(controls))
	for control, expectedID := range controls {
		ids, err := session.TokenizeMode(ctx, control, false, ovsession.TokenizeControl)
		if err != nil {
			t.Fatalf("tokenize StarCoder control %q: %v", control, err)
		}
		if !slices.Equal(ids, []int{expectedID}) {
			t.Fatalf("StarCoder control %q produced IDs %v, want [%d]", control, ids, expectedID)
		}
		controlIDs[ids[0]] = struct{}{}
	}
	if _, err := session.TokenizeMode(ctx, "ordinary source", false, ovsession.TokenizeControl); err == nil {
		t.Fatal("control mode accepted ordinary source text")
	}
	if _, err := session.TokenizeMode(ctx, "<|fim_prefix|>", false, ovsession.TokenizeControl); err == nil {
		t.Fatal("control mode accepted another model's FIM profile")
	}
	if err := session.ValidateLiteralSourceTokenizer("fim", "qwen2.5-coder"); err == nil {
		t.Fatal("cached companion identity accepted another FIM profile")
	}
	for source, expected := range map[string][]int{
		"!=": {6917}, "!!": {3226},
		"if (!value) { return a != b; }": {344, 1445, 872, 46, 320, 461, 331, 882, 342, 64, 339},
		"雪😀\r\n":                         {7042, 141, 36570, 246, 455},
	} {
		ids, err := session.TokenizeMode(ctx, source, false, ovsession.TokenizeLiteralSource)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(ids, expected) {
			t.Fatalf("literal source %q changed source vocabulary IDs: got %v, want %v", source, ids, expected)
		}
	}
	for _, literal := range []string{"!", "<fim_prefix>", "<fim_middle>", "<|endoftext|>", "<repo_name>"} {
		ids, err := session.TokenizeMode(ctx, literal, false, ovsession.TokenizeLiteralSource)
		if err != nil {
			t.Fatalf("tokenize literal source %q: %v", literal, err)
		}
		if len(ids) == 0 {
			t.Fatalf("literal source %q produced no IDs", literal)
		}
		for _, id := range ids {
			if _, isControl := controlIDs[id]; isControl {
				t.Fatalf("literal source %q emitted StarCoder control ID %d", literal, id)
			}
		}
	}
}
