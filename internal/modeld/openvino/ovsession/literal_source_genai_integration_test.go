//go:build openvino && openvino_genai

package ovsession_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/internal/modeld/openvino/ovsession"
)

func TestSystem_OpenVINOGenAI_LiteralSourceTokenizerAvoidsFIMControlIDs(t *testing.T) {
	modelDir := os.Getenv("CONTENOX_OPENVINO_LITERAL_SOURCE_TEST_MODEL")
	if modelDir == "" {
		t.Skip("CONTENOX_OPENVINO_LITERAL_SOURCE_TEST_MODEL is not set")
	}
	companionDir := filepath.Join(modelDir, "literal-source-tokenizer")
	session, err := ovsession.NewGenAI(modelDir, ovsession.GenAIConfig{Device: "CPU", LiteralSourceTokenizerDir: companionDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Errorf("close OpenVINO GenAI session: %v", err)
		}
	})
	if err := session.ValidateLiteralSourceTokenizer("fim", "qwen2.5-coder"); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	literal := "left <|fim_prefix|> middle <|fim_middle|> right <|im_end|>"
	literalIDs, err := session.TokenizeMode(ctx, literal, false, ovsession.TokenizeLiteralSource)
	if err != nil {
		t.Fatal(err)
	}
	modelIDs, err := session.TokenizeMode(ctx, literal, false, ovsession.TokenizeModel)
	if err != nil {
		t.Fatal(err)
	}
	if equalTokenIDs(literalIDs, modelIDs) {
		t.Fatal("literal-source tokenization did not separate FIM controls from model tokenization")
	}
	for _, control := range []string{"<|fim_prefix|>", "<|fim_suffix|>", "<|fim_middle|>", "<|endoftext|>", "<|fim_pad|>", "<|repo_name|>", "<|file_sep|>", "<|im_end|>"} {
		controlIDs, err := session.TokenizeMode(ctx, control, false, ovsession.TokenizeControl)
		if err != nil {
			t.Fatalf("tokenize control %q: %v", control, err)
		}
		if len(controlIDs) != 1 {
			t.Fatalf("control %q produced %d IDs", control, len(controlIDs))
		}
		for _, literalID := range literalIDs {
			if literalID == controlIDs[0] {
				t.Fatalf("literal source emitted control ID %d for %q", literalID, control)
			}
		}
	}
	for _, source := range []string{"!", "!=", "!!", "if (!value) { return a != b; }", "雪😀\r\n"} {
		literal, err := session.TokenizeMode(ctx, source, false, ovsession.TokenizeLiteralSource)
		if err != nil {
			t.Fatal(err)
		}
		model, err := session.TokenizeMode(ctx, source, false, ovsession.TokenizeModel)
		if err != nil {
			t.Fatal(err)
		}
		if !equalTokenIDs(literal, model) {
			t.Fatalf("ordinary source %q changed token IDs: literal=%v model=%v", source, literal, model)
		}
	}
}

func equalTokenIDs(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
