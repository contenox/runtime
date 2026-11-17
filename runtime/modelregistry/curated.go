package modelregistry

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/contenox/runtime/internal/schemafile"
	"github.com/contenox/runtime/runtime/modelcontract"
)

// CuratedModelsSchemaFile is the validated schema holding the curated catalog.
const CuratedModelsSchemaFile = "curated_models.json"

// AgenticTierAgentic marks a model evidenced to run a multi-step tool loop;
// automatic selection picks nothing below it.
const AgenticTierAgentic = "agentic"

// AgenticTierToolChat marks a model that calls tools but is not evidenced to
// sustain a loop: fine for a smoke test or a one-shot question, not for an
// agent working a task.
const AgenticTierToolChat = "tool-chat"

// AgenticTierNone marks a model whose template the runtime cannot read calls
// from, whatever its card claims.
const AgenticTierNone = "none"

//go:embed curated_models.json
var curatedModelsSchema []byte

type curatedCatalog struct {
	Models map[string]ModelDescriptor `json:"models"`
}

var curatedModels = loadCuratedModels()

func loadCuratedModels() map[string]ModelDescriptor {
	catalog := schemafile.MustDecode[curatedCatalog](CuratedModelsSchemaFile, curatedModelsSchema)
	if len(catalog.Models) == 0 {
		panic(fmt.Sprintf("%s: declares no curated model", CuratedModelsSchemaFile))
	}
	for name, descriptor := range catalog.Models {
		if err := ValidateDescriptor(name, descriptor); err != nil {
			panic(fmt.Sprintf("%s: %v", CuratedModelsSchemaFile, err))
		}
	}
	return catalog.Models
}

// ValidateDescriptor rejects a catalog entry that is incomplete for the roles it claims.
func ValidateDescriptor(name string, descriptor ModelDescriptor) error {
	if descriptor.Name != name {
		return fmt.Errorf("model %s declares the identity %q", name, descriptor.Name)
	}
	if descriptor.SizeBytes <= 0 {
		return fmt.Errorf("model %s declares no artifact size", name)
	}
	switch descriptor.BackendType() {
	case "llama":
		if descriptor.SourceURL == "" {
			return fmt.Errorf("model %s declares no source URL", name)
		}
	case "openvino":
		if descriptor.Repo == "" {
			return fmt.Errorf("model %s declares no OpenVINO repository", name)
		}
	default:
		return fmt.Errorf("model %s declares unsupported backend %q", name, descriptor.Backend)
	}
	if descriptor.Completion.Mode != "" {
		if err := validateCompletionProfile(name, descriptor.Completion); err != nil {
			return err
		}
	}
	if descriptor.NextEdit.Format != "" {
		if err := validateNextEditProfile(name, descriptor.NextEdit); err != nil {
			return err
		}
	}
	if descriptor.Embedding.Pooling != "" || descriptor.Embedding.Dimension > 0 || descriptor.Embedding.ContextTokens > 0 {
		if err := validateEmbeddingProfile(name, descriptor.Embedding); err != nil {
			return err
		}
	}
	return nil
}

func validateCompletionProfile(name string, profile CompletionProfile) error {
	if profile.ContextTokens <= 0 || strings.TrimSpace(profile.Evidence) == "" {
		return fmt.Errorf("model %s declares completion mode %q without a context and its curation evidence", name, profile.Mode)
	}
	switch profile.Mode {
	case CompletionFIM:
		if _, err := modelcontract.ResolveFIM(profile.FIMFormat); err != nil {
			return fmt.Errorf("model %s claims native FIM completion: %w", name, err)
		}
	case CompletionInstruct, CompletionPrompt:
		if profile.FIMFormat != "" {
			return fmt.Errorf("model %s declares instructed completion mode %q with native FIM format %q", name, profile.Mode, profile.FIMFormat)
		}
	default:
		return fmt.Errorf("model %s declares unsupported completion mode %q", name, profile.Mode)
	}
	return nil
}

func validateNextEditProfile(name string, profile NextEditProfile) error {
	if profile.ContextTokens <= 0 || strings.TrimSpace(profile.Evidence) == "" {
		return fmt.Errorf("model %s declares edit format %q without a context and its curation evidence", name, profile.Format)
	}
	declared, err := modelcontract.ResolveNextEdit(profile.Format)
	if err != nil {
		return fmt.Errorf("model %s claims edit prediction: %w", name, err)
	}
	if profile.ContextTokens > declared.ContextTokens {
		return fmt.Errorf("model %s declares %d edit context tokens above the %d its format %q supports", name, profile.ContextTokens, declared.ContextTokens, profile.Format)
	}
	return nil
}

func validateEmbeddingProfile(name string, profile EmbeddingProfile) error {
	switch profile.Pooling {
	case "last", "mean", "cls":
	default:
		return fmt.Errorf("model %s declares retrieval pooling %q", name, profile.Pooling)
	}
	if profile.ContextTokens <= 0 || profile.Dimension <= 0 {
		return fmt.Errorf("model %s declares retrieval without a context (%d) and dimension (%d)", name, profile.ContextTokens, profile.Dimension)
	}
	if !profile.Normalize {
		return fmt.Errorf("model %s declares retrieval without normalizing its embeddings", name)
	}
	if strings.TrimSpace(profile.Evidence) == "" {
		return fmt.Errorf("model %s declares retrieval without its curation evidence", name)
	}
	return nil
}

type familyMapping struct {
	CanonicalName string
	Substrings    []string
}

var defaultFamilies = []familyMapping{
	// OpenVINO aliases first so backend-specific repo ids keep the -ov target.
	{CanonicalName: "qwen2.5-coder-14b-ov", Substrings: []string{"openvino/qwen2.5-coder-14", "qwen2.5-coder-14b-instruct-int4-ov", "qwen2.5-coder-14b-ov"}},
	{CanonicalName: "qwen2.5-coder-7b-ov", Substrings: []string{"openvino/qwen2.5-coder-7", "qwen2.5-coder-7b-instruct-int4-ov", "qwen2.5-coder-7b-ov"}},
	{CanonicalName: "qwen2.5-coder-3b-ov", Substrings: []string{"openvino/qwen2.5-coder-3", "qwen2.5-coder-3b-instruct-int4-ov", "qwen2.5-coder-3b-ov"}},
	{CanonicalName: "qwen2.5-coder-1.5b-ov", Substrings: []string{"openvino/qwen2.5-coder-1.5", "qwen2.5-coder-1.5b-instruct-int4-ov", "qwen2.5-coder-1.5b-ov"}},
	{CanonicalName: "qwen2.5-coder-0.5b-ov", Substrings: []string{"openvino/qwen2.5-coder-0.5", "qwen2.5-coder-0.5b-instruct-int4-ov", "qwen2.5-coder-0.5b-ov"}},
	{CanonicalName: "tinyllama-1.1b-chat-v1.0-int4-ov", Substrings: []string{"openvino/tinyllama-1.1b-chat-v1.0-int4-ov", "tinyllama-1.1b-chat-v1.0-int4-ov"}},
	{CanonicalName: "qwen3-coder-30b-a3b-ov", Substrings: []string{"openvino/qwen3-coder-30", "qwen3-coder-30b-a3b-instruct-int4-ov", "qwen3-coder-30b-a3b-ov"}},
	{CanonicalName: "qwen3-30b-ov", Substrings: []string{"openvino/qwen3-30", "qwen3-30b-a3b-int4-ov", "qwen3-30b-ov"}},
	{CanonicalName: "qwen3-14b-ov", Substrings: []string{"openvino/qwen3-14", "qwen3-14b-int4-ov", "qwen3-14b-ov"}},
	{CanonicalName: "qwen3-8b-ov", Substrings: []string{"openvino/qwen3-8", "qwen3-8b-int4-ov", "qwen3-8b-ov"}},
	{CanonicalName: "qwen3-4b-ov", Substrings: []string{"openvino/qwen3-4", "qwen3-4b-int4-ov", "qwen3-4b-ov"}},
	{CanonicalName: "phi-4-mini-ov", Substrings: []string{"openvino/phi-4-mini", "phi-4-mini-instruct-int4-ov", "phi-4-mini-ov"}},
	// Gemma 4 OpenVINO alias before the llama gemma mappings: the "gemma-4"/
	// "gemma" catch-alls below would otherwise route the multimodal OV repo id
	// to the llama text entry.
	{CanonicalName: "gemma4-e4b-ov", Substrings: []string{"openvino/gemma-4-e4b", "gemma-4-e4b-it-int4-ov", "gemma4-e4b-ov"}},
	{CanonicalName: "internvl2-1b-ov", Substrings: []string{"openvino/internvl2-1b", "internvl2-1b-int4-ov", "internvl2-1b-ov", "internvl2", "internvl"}},
	{CanonicalName: "deepseek-r1-distill-qwen-7b-ov", Substrings: []string{"openvino/deepseek-r1-distill-qwen-7", "deepseek-r1-distill-qwen-7b-int4-ov", "deepseek-r1-distill-qwen-7b-ov"}},
	{CanonicalName: "gpt-oss-20b-ov", Substrings: []string{"openvino/gpt-oss-20b", "gpt-oss-20b-int4-ov", "gpt-oss-20b-ov"}},
	// Qwen 3 (checked before 2.5 to avoid substring collision).
	{CanonicalName: "qwen3-coder-30b-a3b", Substrings: []string{"qwen3-coder-30", "qwen3-coder", "qwen-coder"}},
	{CanonicalName: "qwen3-30b", Substrings: []string{"qwen3-30", "qwen3:30"}},
	{CanonicalName: "qwen3-14b", Substrings: []string{"qwen3-14", "qwen3:14"}},
	{CanonicalName: "qwen3-8b", Substrings: []string{"qwen3-8", "qwen3:8"}},
	{CanonicalName: "qwen3-4b", Substrings: []string{"qwen3-4", "qwen3:4"}},
	{CanonicalName: "qwen3-8b", Substrings: []string{"qwen3"}},
	// Coding-specialized models.
	{CanonicalName: "qwen2.5-coder-32b", Substrings: []string{"qwen2.5-coder-32", "qwen25-coder-32", "qwen2.5-coder:32"}},
	{CanonicalName: "qwen2.5-coder-14b", Substrings: []string{"qwen2.5-coder-14", "qwen25-coder-14", "qwen2.5-coder:14"}},
	{CanonicalName: "qwen2.5-coder-7b", Substrings: []string{"qwen2.5-coder-7", "qwen25-coder-7", "qwen2.5-coder:7"}},
	{CanonicalName: "qwen2.5-coder-3b", Substrings: []string{"qwen2.5-coder-3", "qwen25-coder-3", "qwen2.5-coder:3"}},
	{CanonicalName: "qwen2.5-coder-1.5b", Substrings: []string{"qwen2.5-coder-1.5", "qwen25-coder-1.5", "qwen2.5-coder:1.5"}},
	{CanonicalName: "qwen2.5-coder-0.5b", Substrings: []string{"qwen2.5-coder-0.5", "qwen25-coder-0.5", "qwen2.5-coder"}},
	{CanonicalName: "devstral-small-2507", Substrings: []string{"devstral-small-2507", "devstral-small", "devstral"}},
	{CanonicalName: "codestral-22b", Substrings: []string{"codestral-22", "codestral"}},
	{CanonicalName: "deepseek-coder-v2-lite", Substrings: []string{"deepseek-coder-v2-lite", "deepseek-coder-v2", "deepseek-coder"}},
	{CanonicalName: "starcoder2-7b-instruct", Substrings: []string{"starcoder2-7b-instruct", "starcoder2-7", "starcoder2"}},
	// Gemma 4.
	{CanonicalName: "gemma4-26b-a4b", Substrings: []string{"gemma-4-26", "gemma4-26", "gemma4:26"}},
	{CanonicalName: "gemma4-12b", Substrings: []string{"gemma-4-12", "gemma4-12", "gemma4:12"}},
	{CanonicalName: "gemma4-e2b", Substrings: []string{"gemma-4-e2", "gemma4-e2", "gemma4:e2"}},
	{CanonicalName: "gemma4-e4b", Substrings: []string{"gemma-4-e4", "gemma4-e4", "gemma4:e4", "gemma-4", "gemma4", "gemma"}},
	// Phi 4.
	{CanonicalName: "phi-4-mini", Substrings: []string{"phi-4-mini", "phi4-mini", "phi4mini", "phi-4", "phi4"}},
	// DeepSeek.
	{CanonicalName: "deepseek-r1-0528-qwen3-8b", Substrings: []string{"deepseek-r1-0528", "deepseek-r1-qwen3", "deepseek-r1", "deepseek"}},
	// OpenAI open-weight.
	{CanonicalName: "gpt-oss-20b", Substrings: []string{"openai/gpt-oss-20b", "openai_gpt-oss-20b", "gpt-oss-20b", "gpt-oss", "openai-oss", "openai oss"}},
	// Granite.
	{CanonicalName: "granite-3.2-8b", Substrings: []string{"granite-3.2-8", "granite-3.1", "granite3.", "granite"}},
}

const defaultFallback = "qwen3-4b"
