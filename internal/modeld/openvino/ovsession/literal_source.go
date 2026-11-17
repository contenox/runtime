package ovsession

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/runtime/modelcontract"
)

const (
	// LiteralSourceTokenizerManifestFilename is the model-root manifest name for a validated tokenizer companion.
	LiteralSourceTokenizerManifestFilename = "contenox-literal-source-tokenizer.json"
	// LiteralSourceTokenizerDirectoryName is the model-root directory containing companion tokenizer assets.
	LiteralSourceTokenizerDirectoryName = "literal-source-tokenizer"
)

type literalSourceManifest struct {
	Version                      int                    `json:"version"`
	Kind                         string                 `json:"kind"`
	Format                       string                 `json:"format"`
	FormatVersion                int                    `json:"format_version"`
	SourceTokenizerSHA256        string                 `json:"source_tokenizer_sha256"`
	SourceTokenizerConfigSHA256  string                 `json:"source_tokenizer_config_sha256"`
	SourceSpecialTokensMapSHA256 string                 `json:"source_special_tokens_map_sha256"`
	CompanionTokenizerSHA256     string                 `json:"companion_tokenizer_sha256"`
	CompanionConfigSHA256        string                 `json:"companion_config_sha256"`
	TokenizerIRSHA256            string                 `json:"tokenizer_ir_sha256"`
	TokenizerWeightsSHA256       string                 `json:"tokenizer_weights_sha256"`
	ControlTokens                []string               `json:"control_tokens"`
	RemovedTokens                []string               `json:"removed_tokens"`
	PreparationScriptSHA256      string                 `json:"preparation_script_sha256"`
	Toolchain                    literalSourceToolchain `json:"toolchain"`
	Identity                     string                 `json:"identity"`
}

type literalSourceToolchain struct {
	Python   string                    `json:"python"`
	Packages literalSourceToolPackages `json:"packages"`
}

type literalSourceToolPackages struct {
	OpenVINO          string `json:"openvino"`
	OpenVINOTokenizer string `json:"openvino-tokenizers"`
	Transformers      string `json:"transformers"`
	Tokenizers        string `json:"tokenizers"`
}

// ValidateLiteralSourceTokenizer verifies the installed literal-source tokenizer for FIM.
func ValidateLiteralSourceTokenizer(modelDir, companionDir, format string) error {
	return ValidateLiteralSourceTokenizerForOperation(modelDir, companionDir, "fim", format)
}

// ValidateLiteralSourceTokenizerForOperation verifies the installed literal-source tokenizer for a code operation.
func ValidateLiteralSourceTokenizerForOperation(modelDir, companionDir, kind, format string) error {
	version, controls, err := literalSourceContract(kind, format)
	if err != nil {
		return err
	}
	if modelDir == "" || companionDir == "" {
		return literalSourceTokenizerUnavailable(modelDir)
	}
	manifestPath := filepath.Join(filepath.Dir(companionDir), LiteralSourceTokenizerManifestFilename)
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return literalSourceTokenizerUnavailable(modelDir)
		}
		return fmt.Errorf("read OpenVINO literal-source tokenizer manifest %q: %w", manifestPath, err)
	}
	var manifest literalSourceManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return fmt.Errorf("decode OpenVINO literal-source tokenizer manifest %q: %w", manifestPath, err)
	}
	if manifest.Version != 1 || manifest.Kind != kind || manifest.Format != format || manifest.FormatVersion != version || !equalStrings(manifest.ControlTokens, controls) {
		return fmt.Errorf("OpenVINO literal-source tokenizer manifest %q does not match %s format %q; "+remedy.ReacquireModel, manifestPath, kind, format)
	}
	sourcePath := filepath.Join(modelDir, "tokenizer.json")
	companionPath := filepath.Join(companionDir, "tokenizer.json")
	sourceData, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read OpenVINO model tokenizer for %s source validation: %w", kind, err)
	}
	companionData, err := os.ReadFile(companionPath)
	if err != nil {
		return fmt.Errorf("read OpenVINO literal-source tokenizer: %w", err)
	}
	var sourceTokenizer, companionTokenizer tokenizerIdentityFile
	if err := json.Unmarshal(sourceData, &sourceTokenizer); err != nil {
		return fmt.Errorf("decode OpenVINO model tokenizer for %s source validation: %w", kind, err)
	}
	if err := json.Unmarshal(companionData, &companionTokenizer); err != nil {
		return fmt.Errorf("decode OpenVINO literal-source tokenizer: %w", err)
	}
	companionConfigPath := filepath.Join(companionDir, "tokenizer_config.json")
	companionConfig, err := os.ReadFile(companionConfigPath)
	if err != nil {
		return fmt.Errorf("read OpenVINO literal-source tokenizer config: %w", err)
	}
	tokenizerIR, err := os.ReadFile(filepath.Join(companionDir, "openvino_tokenizer.xml"))
	if err != nil {
		return fmt.Errorf("read OpenVINO literal-source tokenizer IR: %w", err)
	}
	tokenizerWeights, err := os.ReadFile(filepath.Join(companionDir, "openvino_tokenizer.bin"))
	if err != nil {
		return fmt.Errorf("read OpenVINO literal-source tokenizer weights: %w", err)
	}
	removed := expectedRemovedTokens(sourceTokenizer, controls)
	if !equalStrings(manifest.RemovedTokens, removed) || containsAnyTokenizerToken(companionTokenizer, removed) {
		return fmt.Errorf("OpenVINO literal-source tokenizer does not remove all control tokens for %s format %q; "+remedy.ReacquireModel, kind, format)
	}
	sourceHash := sha256String(sourceData)
	companionHash := sha256String(companionData)
	sourceConfigHash, err := optionalFileHash(filepath.Join(modelDir, "tokenizer_config.json"))
	if err != nil {
		return fmt.Errorf("read OpenVINO source tokenizer config: %w", err)
	}
	sourceSpecialMapHash, err := optionalFileHash(filepath.Join(modelDir, "special_tokens_map.json"))
	if err != nil {
		return fmt.Errorf("read OpenVINO source special tokens map: %w", err)
	}
	if sourceHash != manifest.SourceTokenizerSHA256 || sourceConfigHash != manifest.SourceTokenizerConfigSHA256 || sourceSpecialMapHash != manifest.SourceSpecialTokensMapSHA256 || companionHash != manifest.CompanionTokenizerSHA256 || sha256String(companionConfig) != manifest.CompanionConfigSHA256 || sha256String(tokenizerIR) != manifest.TokenizerIRSHA256 || sha256String(tokenizerWeights) != manifest.TokenizerWeightsSHA256 || manifest.Identity != literalSourceIdentity(manifest) {
		return fmt.Errorf("OpenVINO literal-source tokenizer identity does not match model %q; "+remedy.ReacquireModel, modelDir)
	}
	return nil
}

type tokenizerIdentityFile struct {
	Model struct {
		Vocab map[string]json.RawMessage `json:"vocab"`
	} `json:"model"`
	AddedTokens []struct {
		Content string `json:"content"`
		ID      *int   `json:"id"`
		Special bool   `json:"special"`
	} `json:"added_tokens"`
}

func expectedRemovedTokens(tokenizer tokenizerIdentityFile, controls []string) []string {
	removed := make(map[string]struct{}, len(controls)+len(tokenizer.AddedTokens))
	for _, token := range controls {
		removed[token] = struct{}{}
	}
	for _, token := range tokenizer.AddedTokens {
		if token.Special && token.Content != "" {
			removed[token.Content] = struct{}{}
		}
	}
	tokens := make([]string, 0, len(removed))
	for token := range removed {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	return tokens
}

func containsAnyTokenizerToken(tokenizer tokenizerIdentityFile, tokens []string) bool {
	for _, token := range tokens {
		if _, found := tokenizer.Model.Vocab[token]; found {
			return true
		}
		for _, added := range tokenizer.AddedTokens {
			if added.Content == token {
				return true
			}
		}
	}
	return false
}

func literalSourceContract(kind, format string) (int, []string, error) {
	return modelcontract.TokenizerCompanionControls(kind, format)
}

func literalSourceTokenizerUnavailable(modelDir string) error {
	return fmt.Errorf("OpenVINO native source-token encoding requires the vocabulary-compatible tokenizer companion beside model %q; "+remedy.ReacquireModel, modelDir)
}

func literalSourceIdentity(manifest literalSourceManifest) string {
	data, _ := json.Marshal(struct {
		Version                      int                    `json:"version"`
		Kind                         string                 `json:"kind"`
		Format                       string                 `json:"format"`
		FormatVersion                int                    `json:"format_version"`
		SourceTokenizerSHA256        string                 `json:"source_tokenizer_sha256"`
		SourceTokenizerConfigSHA256  string                 `json:"source_tokenizer_config_sha256"`
		SourceSpecialTokensMapSHA256 string                 `json:"source_special_tokens_map_sha256"`
		CompanionTokenizerSHA256     string                 `json:"companion_tokenizer_sha256"`
		CompanionConfigSHA256        string                 `json:"companion_config_sha256"`
		TokenizerIRSHA256            string                 `json:"tokenizer_ir_sha256"`
		TokenizerWeightsSHA256       string                 `json:"tokenizer_weights_sha256"`
		ControlTokens                []string               `json:"control_tokens"`
		RemovedTokens                []string               `json:"removed_tokens"`
		PreparationScriptSHA256      string                 `json:"preparation_script_sha256"`
		Toolchain                    literalSourceToolchain `json:"toolchain"`
	}{manifest.Version, manifest.Kind, manifest.Format, manifest.FormatVersion, manifest.SourceTokenizerSHA256, manifest.SourceTokenizerConfigSHA256, manifest.SourceSpecialTokensMapSHA256, manifest.CompanionTokenizerSHA256, manifest.CompanionConfigSHA256, manifest.TokenizerIRSHA256, manifest.TokenizerWeightsSHA256, manifest.ControlTokens, manifest.RemovedTokens, manifest.PreparationScriptSHA256, manifest.Toolchain})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func optionalFileHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return sha256String(data), nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func sha256String(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
