//go:build openvino && openvino_genai

package ovsession

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// literalSourceTokenizer retains the tokenizer identity protected by the worker's model-store allocation.
type literalSourceTokenizer struct {
	kind     string
	format   string
	controls map[string]int
}

func loadLiteralSourceTokenizer(modelDir, companionDir string) (literalSourceTokenizer, error) {
	manifestPath := filepath.Join(filepath.Dir(companionDir), LiteralSourceTokenizerManifestFilename)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return literalSourceTokenizer{}, literalSourceTokenizerUnavailable(modelDir)
		}
		return literalSourceTokenizer{}, fmt.Errorf("read OpenVINO literal-source tokenizer manifest %q: %w", manifestPath, err)
	}
	var manifest literalSourceManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return literalSourceTokenizer{}, fmt.Errorf("decode OpenVINO literal-source tokenizer manifest %q: %w", manifestPath, err)
	}
	if err := ValidateLiteralSourceTokenizerForOperation(modelDir, companionDir, manifest.Kind, manifest.Format); err != nil {
		return literalSourceTokenizer{}, err
	}
	data, err = os.ReadFile(filepath.Join(modelDir, "tokenizer.json"))
	if err != nil {
		return literalSourceTokenizer{}, fmt.Errorf("read OpenVINO source control tokens: %w", err)
	}
	var tokenizer tokenizerIdentityFile
	if err := json.Unmarshal(data, &tokenizer); err != nil {
		return literalSourceTokenizer{}, fmt.Errorf("decode OpenVINO source control tokens: %w", err)
	}
	result := literalSourceTokenizer{kind: manifest.Kind, format: manifest.Format, controls: make(map[string]int, len(manifest.ControlTokens))}
	for _, token := range manifest.ControlTokens {
		id, err := sourceControlTokenID(tokenizer, token)
		if err != nil {
			return literalSourceTokenizer{}, err
		}
		result.controls[token] = id
	}
	return result, nil
}

func sourceControlTokenID(tokenizer tokenizerIdentityFile, token string) (int, error) {
	var sourceID *int
	for _, added := range tokenizer.AddedTokens {
		if added.Content != token {
			continue
		}
		if sourceID != nil {
			return 0, fmt.Errorf("OpenVINO control token %q has duplicate source entries", token)
		}
		if added.ID == nil || *added.ID < 0 {
			return 0, fmt.Errorf("OpenVINO control token %q has no valid source token ID", token)
		}
		sourceID = added.ID
	}
	if sourceID == nil {
		return 0, fmt.Errorf("OpenVINO control token %q is not an added token in the source tokenizer", token)
	}
	if vocabID, found := tokenizer.Model.Vocab[token]; found {
		var id int
		if err := json.Unmarshal(vocabID, &id); err != nil {
			return 0, fmt.Errorf("decode OpenVINO source vocabulary ID for control token %q: %w", token, err)
		}
		if id != *sourceID {
			return 0, fmt.Errorf("OpenVINO control token %q has inconsistent source vocabulary IDs %d and %d", token, id, *sourceID)
		}
	}
	return *sourceID, nil
}

// ensureLiteralSourceTokenizer requires s.mu while initializing the session's immutable code vocabulary.
func (s *GenAISession) ensureLiteralSourceTokenizer() error {
	if s.codeTokenizer.controls != nil {
		return nil
	}
	tokenizer, err := loadLiteralSourceTokenizer(s.modelDir, s.literalSourceTokenizerDir)
	if err != nil {
		return err
	}
	s.codeTokenizer = tokenizer
	return nil
}
