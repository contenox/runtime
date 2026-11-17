package ovsession_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/contenox/runtime/internal/modeld/openvino/ovsession"
)

type sourceToolchain struct {
	Python   string             `json:"python"`
	Packages sourceToolPackages `json:"packages"`
}

type sourceToolPackages struct {
	OpenVINO          string `json:"openvino"`
	OpenVINOTokenizer string `json:"openvino-tokenizers"`
	Transformers      string `json:"transformers"`
	Tokenizers        string `json:"tokenizers"`
}

type sourceManifestPayload struct {
	Version                      int             `json:"version"`
	Kind                         string          `json:"kind"`
	Format                       string          `json:"format"`
	FormatVersion                int             `json:"format_version"`
	SourceTokenizerSHA256        string          `json:"source_tokenizer_sha256"`
	SourceTokenizerConfigSHA256  string          `json:"source_tokenizer_config_sha256"`
	SourceSpecialTokensMapSHA256 string          `json:"source_special_tokens_map_sha256"`
	CompanionTokenizerSHA256     string          `json:"companion_tokenizer_sha256"`
	CompanionConfigSHA256        string          `json:"companion_config_sha256"`
	TokenizerIRSHA256            string          `json:"tokenizer_ir_sha256"`
	TokenizerWeightsSHA256       string          `json:"tokenizer_weights_sha256"`
	ControlTokens                []string        `json:"control_tokens"`
	RemovedTokens                []string        `json:"removed_tokens"`
	PreparationScriptSHA256      string          `json:"preparation_script_sha256"`
	Toolchain                    sourceToolchain `json:"toolchain"`
}

func TestUnit_LiteralSourceTokenizerValidatesModelAndCompanionIdentity(t *testing.T) {
	modelDir := t.TempDir()
	companionDir := filepath.Join(modelDir, "literal-source-tokenizer")
	if err := os.Mkdir(companionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	source := []byte(`{"model":{"type":"BPE","vocab":{}},"added_tokens":[]}`)
	companion := []byte(`{"model":{"type":"BPE","vocab":{}},"added_tokens":[]}`)
	writeSourceAssets(t, modelDir, companionDir, source, companion, "fim", "starcoder", 1,
		[]string{"<|endoftext|>", "<fim_prefix>", "<fim_suffix>", "<fim_middle>", "<fim_pad>"},
		[]string{"<fim_middle>", "<fim_pad>", "<fim_prefix>", "<fim_suffix>", "<|endoftext|>"})

	if err := ovsession.ValidateLiteralSourceTokenizer(modelDir, companionDir, "starcoder"); err != nil {
		t.Fatalf("validate companion: %v", err)
	}
	if err := ovsession.ValidateLiteralSourceTokenizer(modelDir, companionDir, "qwen2.5-coder"); err == nil {
		t.Fatal("format mismatch passed validation")
	}
	if err := os.WriteFile(filepath.Join(companionDir, "openvino_tokenizer.bin"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ovsession.ValidateLiteralSourceTokenizer(modelDir, companionDir, "starcoder"); err == nil {
		t.Fatal("changed companion weights passed validation")
	}
}

func TestUnit_LiteralSourceTokenizerMissingReturnsRepairRoute(t *testing.T) {
	modelDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(modelDir, "tokenizer.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := ovsession.ValidateLiteralSourceTokenizer(modelDir, filepath.Join(modelDir, "literal-source-tokenizer"), "starcoder")
	if err == nil || !strings.Contains(err.Error(), "reacquire the model bundle with `/local pull <model>`") {
		t.Fatalf("missing companion error = %v", err)
	}
}

func TestUnit_NextEditLiteralTokenizerValidatesControlsAndSpecialTokens(t *testing.T) {
	modelDir := t.TempDir()
	companionDir := filepath.Join(modelDir, "literal-source-tokenizer")
	if err := os.Mkdir(companionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	source := []byte(`{"model":{"type":"BPE","vocab":{"<|file_sep|>":10}},"added_tokens":[{"id":11,"content":"<|secret|>","special":true}]}`)
	companion := []byte(`{"model":{"type":"BPE","vocab":{}},"added_tokens":[]}`)
	writeSourceAssets(t, modelDir, companionDir, source, companion, "next-edit", "sweep-v2", 2,
		[]string{"<|file_sep|>", "<|endoftext|>"},
		[]string{"<|endoftext|>", "<|file_sep|>", "<|secret|>"})
	if err := ovsession.ValidateLiteralSourceTokenizerForOperation(modelDir, companionDir, "next-edit", "sweep-v2"); err != nil {
		t.Fatalf("validate next-edit companion: %v", err)
	}
}

func writeSourceAssets(t *testing.T, modelDir, companionDir string, source, companion []byte, kind, format string, version int, controls, removed []string) {
	t.Helper()
	for path, data := range map[string][]byte{
		filepath.Join(modelDir, "tokenizer.json"):             source,
		filepath.Join(modelDir, "tokenizer_config.json"):      []byte("{}\n"),
		filepath.Join(companionDir, "tokenizer.json"):         companion,
		filepath.Join(companionDir, "tokenizer_config.json"):  []byte("{}\n"),
		filepath.Join(companionDir, "openvino_tokenizer.xml"): []byte("ir"),
		filepath.Join(companionDir, "openvino_tokenizer.bin"): []byte("weights"),
	} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	payload := sourceManifestPayload{
		Version: 1, Kind: kind, Format: format, FormatVersion: version,
		SourceTokenizerSHA256: fileHash(source), SourceTokenizerConfigSHA256: fileHash([]byte("{}\n")),
		CompanionTokenizerSHA256: fileHash(companion), CompanionConfigSHA256: fileHash([]byte("{}\n")),
		TokenizerIRSHA256: fileHash([]byte("ir")), TokenizerWeightsSHA256: fileHash([]byte("weights")),
		ControlTokens: controls, RemovedTokens: removed, PreparationScriptSHA256: fileHash([]byte("script")),
		Toolchain: sourceToolchain{Python: "3.13.0", Packages: sourceToolPackages{OpenVINO: "2026.2.0", OpenVINOTokenizer: "2026.2.0.0", Transformers: "5.0.0", Tokenizers: "0.22.2"}},
	}
	identityBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	manifestBytes, err := json.Marshal(struct {
		sourceManifestPayload
		Identity string `json:"identity"`
	}{payload, fileHash(identityBytes)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "contenox-literal-source-tokenizer.json"), manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
}

func fileHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
