//go:build llamanode && llamacpp_direct

package llamasession_test

import (
	"os"
	"testing"

	"github.com/contenox/runtime/internal/modeld/contextasm"
	"github.com/contenox/runtime/internal/modeld/llama"
	"github.com/contenox/runtime/internal/modeld/llama/llamasession"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/modelcontract"
	"github.com/stretchr/testify/require"
)

func TestSystem_CodeManifestRejectsChangedContractAndChatSuffix(t *testing.T) {
	path := os.Getenv("CONTENOX_LLAMA_FIM_GGUF")
	if path == "" {
		t.Skip("CONTENOX_LLAMA_FIM_GGUF is not set")
	}
	session, err := llamasession.New(path, llama.Config{Target: transport.ExecutionTarget{Mode: transport.PlacementCPU}, NumCtx: 512, NumBatch: 32, NumThreads: 1, DisableBOS: true})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	input := modelcontract.AutocompleteInput{Format: "qwen2.5-coder", Prefix: "return ", Suffix: "\n"}
	format, err := modelcontract.ResolveFIM(input.Format)
	require.NoError(t, err)
	text := format.Render(input.Prefix, input.Suffix)
	fingerprint, err := modelcontract.FIMFingerprint(input.Format)
	require.NoError(t, err)
	manifest, err := contextasm.BuildSplitManifest(text, "", nil, contextasm.ManifestIdentity{ProfileID: "autocomplete:" + input.Format, PromptFormat: input.Format, PromptTemplateDigest: fingerprint})
	require.NoError(t, err)
	stale := manifest
	stale.PromptTemplateDigest = "previous-contract"
	_, err = session.EnsurePrefix(t.Context(), llama.PrefixInput{Text: text, Manifest: stale, Autocomplete: &input})
	require.ErrorIs(t, err, contextasm.ErrManifestMismatch)
	_, err = session.EnsurePrefix(t.Context(), llama.PrefixInput{Text: text, Manifest: manifest, Autocomplete: &input})
	require.NoError(t, err)
	_, err = session.PrefillSuffix(t.Context(), llama.SuffixInput{Text: "user\nhello", Manifest: manifest})
	require.ErrorContains(t, err, "chat suffixes are not supported")
}
