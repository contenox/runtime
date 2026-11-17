//go:build openvino && openvino_genai

package openvino_test

import (
	"context"
	"os"
	"testing"

	"github.com/contenox/runtime/internal/modeld/contextasm"
	"github.com/contenox/runtime/internal/modeld/openvino"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/modelcontract"
)

func TestSystem_OpenVINONativeFIMRejectsAdditionalSuffix(t *testing.T) {
	modelDir := os.Getenv("CONTENOX_OPENVINO_FIM_MODEL")
	if modelDir == "" {
		t.Skip("CONTENOX_OPENVINO_FIM_MODEL is not set")
	}
	format, err := contextasm.ResolveFIM("qwen2.5-coder")
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := modelcontract.FIMFingerprint(format.ID)
	if err != nil {
		t.Fatal(err)
	}
	input := &contextasm.AutocompleteInput{Format: format.ID, Prefix: "func Add(a, b int) int { return ", Suffix: " }"}
	manifest, err := contextasm.BuildSplitManifest(format.Render(input.Prefix, input.Suffix), "", nil, contextasm.ManifestIdentity{
		ProfileID: "autocomplete:" + format.ID, ModelDigest: "qualification-model", PromptFormat: format.ID, PromptTemplateDigest: fingerprint,
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := openvino.NewService().OpenSession(t.Context(), transport.OpenSessionRequest{
		ModelName: "qwen-coder-0.5b-int4", Type: "openvino", Digest: "qualification-model", Path: modelDir,
		Config: transport.Config{Target: transport.ExecutionTarget{Mode: transport.PlacementCPU}, NumCtx: 2048},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Errorf("close OpenVINO FIM session: %v", err)
		}
	})
	if _, err := session.EnsurePrefix(context.Background(), transport.PrefixInput{Manifest: manifest, Autocomplete: input}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.PrefillSuffix(context.Background(), transport.SuffixInput{Text: "should not be accepted", Manifest: manifest}); err == nil {
		t.Fatal("FIM session accepted an additional chat suffix")
	}
}
