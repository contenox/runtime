package modelprovider

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/contenox/contenox/core/serverops"
	"github.com/ollama/ollama/api"
)

type OllamaProvider struct {
	Name           string
	ID             string
	ContextLength  int
	SupportsChat   bool
	SupportsEmbed  bool
	SupportsStream bool
	SupportsPrompt bool
	Backends       []string // assuming that Backend IDs are urls to the instance
}

func (p *OllamaProvider) GetBackendIDs() []string {
	return p.Backends
}

func (p *OllamaProvider) ModelName() string {
	return p.Name
}

func (p *OllamaProvider) GetID() string {
	return p.ID
}

func (p *OllamaProvider) GetContextLength() int {
	return p.ContextLength
}

func (p *OllamaProvider) CanChat() bool {
	return p.SupportsChat
}

func (p *OllamaProvider) CanEmbed() bool {
	return p.SupportsEmbed
}

func (p *OllamaProvider) CanStream() bool {
	return p.SupportsStream
}

func (p *OllamaProvider) CanPrompt() bool {
	return p.SupportsPrompt
}

func (p *OllamaProvider) GetChatConnection(backendID string) (serverops.LLMChatClient, error) {
	if !p.CanChat() {
		return nil, fmt.Errorf("provider %s (model %s) does not support chat", p.GetID(), p.ModelName())
	}
	u, err := url.Parse(backendID)
	if err != nil {
		// Consider logging the error too
		return nil, fmt.Errorf("invalid backend URL '%s' for provider %s: %w", backendID, p.GetID(), err)
	}
	// TODO: Consider using a configurable http.Client with timeouts
	httpClient := http.DefaultClient
	ollamaAPIClient := api.NewClient(u, httpClient)

	// Create and return the wrapper client
	chatClient := &OllamaChatClient{
		ollamaClient: ollamaAPIClient,
		modelName:    p.ModelName(), // Use the full model name (e.g., "llama3:latest")
		backendURL:   backendID,
	}

	return chatClient, nil
}

func (p *OllamaProvider) GetEmbedConnection(backendID string) (serverops.LLMEmbedClient, error) {
	if !p.CanEmbed() {
		return nil, fmt.Errorf("provider %s (model %s) does not support embeddings", p.GetID(), p.ModelName())
	}
	u, err := url.Parse(backendID)
	if err != nil {
		return nil, fmt.Errorf("invalid backend URL '%s' for provider %s: %w", backendID, p.GetID(), err)
	}
	// TODO: Consider using a configurable http.Client with timeouts
	httpClient := http.DefaultClient
	ollamaAPIClient := api.NewClient(u, httpClient)

	embedClient := &OllamaEmbedClient{
		ollamaClient: ollamaAPIClient,
		modelName:    p.ModelName(),
		backendURL:   backendID,
	}

	return embedClient, nil
}

func (p *OllamaProvider) GetPromptConnection(backendID string) (serverops.LLMPromptExecClient, error) {
	if !p.CanPrompt() {
		return nil, fmt.Errorf("provider %s (model %s) does not support prompting", p.GetID(), p.ModelName())
	}
	u, err := url.Parse(backendID)
	if err != nil {
		return nil, fmt.Errorf("invalid backend URL '%s' for provider %s: %w", backendID, p.GetID(), err)
	}
	// TODO: Consider using a configurable http.Client with timeouts
	httpClient := http.DefaultClient
	ollamaAPIClient := api.NewClient(u, httpClient)

	promptClient := &OllamaPromptClient{
		ollamaClient: ollamaAPIClient,
		modelName:    p.ModelName(),
		backendURL:   backendID,
	}

	return promptClient, nil
}

func (p *OllamaProvider) GetStreamConnection(backendID string) (serverops.LLMStreamClient, error) {
	return nil, fmt.Errorf("unimplemented")
}

type OllamaOption func(*OllamaProvider)

func NewOllamaModelProvider(name string, backends []string, opts ...OllamaOption) Provider {
	// Define defaults based on model name
	nameForMatching := name
	if c := strings.Split(name, ":"); len(c) >= 2 && c[1] == "latest" {
		nameForMatching = c[0] // llama3:latest
	}
	context := modelContextLengths[nameForMatching]
	canChat := canChat[nameForMatching]
	canEmbed := canEmbed[nameForMatching]
	canStream := canStreaming[nameForMatching]
	canPrompt := canPrompt[nameForMatching]

	p := &OllamaProvider{
		Name:           name,
		ID:             "ollama:" + name,
		ContextLength:  context,
		SupportsChat:   canChat,
		SupportsEmbed:  canEmbed,
		SupportsStream: canStream,
		SupportsPrompt: canPrompt,
		Backends:       backends,
	}

	for _, opt := range opts {
		opt(p)
	}

	return p
}

var (
	modelContextLengths = map[string]int{
		"smollm2:135m":       100000,
		"llama2":             4096,
		"llama3":             8192,
		"llama3-70b":         8192,
		"mistral":            8192,
		"mixtral":            32768,
		"phi":                2048,
		"codellama":          16384,
		"codellama:34b-100k": 100000,
		"gemma":              8192,
		"openhermes":         4096,
		"notux":              4096,
		"llava":              8192,
		"deepseek":           8192,
		"qwen":               8192,
		"qwen2":              8192,
		"zephyr":             8192,
		"neural-chat":        8192,
		"dolphin-mixtral":    32768,
		"qwen2.5:0.5b":       4128,
		"qwen2.5:1.5b":       8192,
		"paraphrase-multilingual:278m-mpnet-base-v2-fp16": 278,
		"llama2-uncensored":   4096,
		"llama2-70b":          4096,
		"llama2-70b-chat":     4096,
		"llama3-instruct":     8192,
		"llama3-70b-instruct": 8192,
		"vicuna":              4096,  // Based on Llama2
		"guanaco":             4096,  // Based on Llama2
		"koala":               4096,  // Based on Llama
		"wizardlm":            4096,  // Based on Llama
		"airoboros":           4096,  // Llama-based
		"open-orca":           8192,  // Based on Llama3
		"yi":                  32768, // Yi Llama-based large context
	}

	modelContextLengthsFullNames = map[string]int{
		"smollm2:135m":       100000, // TODO: check if it's correct:30m
		"codellama:34b-100k": 100000,
		"mixtral-8x7b":       32768,
	}

	canChat = map[string]bool{
		"llama2": true, "llama3": true, "mistral": true,
		"mixtral": true, "phi": true, "codellama": true,
		"gemma": true, "openhermes": true, "notux": true,
		"llava": true, "deepseek": true, "qwen": true,
		"zephyr": true, "neural-chat": true, "dolphin-mixtral": true,
		"smollm2:135m": true, "qwen2.5:1.5b": true, "qwen2": true,
	}

	canEmbed = map[string]bool{
		"deepseek":              true,
		"qwen":                  true,
		"all-minilm":            true,
		"granite-embedding:30m": true,
		"nomic-embed-text":      true,
		"paraphrase-multilingual:278m-mpnet-base-v2-fp16": true,
		"llama3":              true, // TODO Check if that's correct
		"codellama":           true, // TODO Check if that's correct
		"gemma":               true, // TODO Check if that's correct
		"mistral":             true, // TODO Check if that's correct
		"llama3-instruct":     true, // TODO Check if that's correct
		"llama3-70b-instruct": true, // TODO Check if that's correct
		"open-orca":           true, // TODO Check if that's correct
		"yi":                  true, // TODO Check if that's correct
	}

	canPrompt = map[string]bool{
		"llama2":              true,
		"llama3":              true,
		"mistral":             true,
		"codellama":           true,
		"gemma":               true,
		"qwen":                true,
		"deepseek":            true,
		"vicuna":              true,
		"guanaco":             true,
		"wizardlm":            true,
		"airoboros":           true,
		"llama2-70b":          true,
		"llama2-70b-chat":     true,
		"llama3-instruct":     true,
		"llama3-70b-instruct": true,
		"open-orca":           true,
		"yi":                  true,
	}

	canStreaming = map[string]bool{
		"llama2": true, "llama3": true, "mistral": true,
		"mixtral": true, "phi": true, "codellama": true,
		"gemma": true, "openhermes": true, "notux": true,
		"llava": true, "deepseek": true, "qwen": true,
		"zephyr": true, "neural-chat": true, "dolphin-mixtral": true,
		"smollm2:135m": true, "qwen2.5:1.5b": true, "qwen2": true,
		"llama2-uncensored":   true,
		"llama2-70b":          true,
		"llama2-70b-chat":     true,
		"llama3-instruct":     true,
		"llama3-70b-instruct": true,
		"vicuna":              true,
		"guanaco":             true,
		"koala":               true,
		"wizardlm":            true,
		"airoboros":           true,
		"open-orca":           true,
		"yi":                  true,
	}
)

func WithChat(supports bool) OllamaOption {
	return func(p *OllamaProvider) {
		p.SupportsChat = supports
	}
}

func WithEmbed(supports bool) OllamaOption {
	return func(p *OllamaProvider) {
		p.SupportsEmbed = supports
	}
}

func WithPrompt(supports bool) OllamaOption {
	return func(p *OllamaProvider) {
		p.SupportsPrompt = supports
	}
}

func WithStream(supports bool) OllamaOption {
	return func(p *OllamaProvider) {
		p.SupportsStream = supports
	}
}

func WithContextLength(length int) OllamaOption {
	return func(p *OllamaProvider) {
		p.ContextLength = length
	}
}

func WithComputedContextLength(model ListModelResponse) OllamaOption {
	return func(p *OllamaProvider) {
		length, err := GetModelsMaxContextLength(model)
		if err != nil {
			baseName := parseOllamaModelName(model.Model)
			length = modelContextLengths[baseName] // fallback
		}
		p.ContextLength = length
	}
}

// GetModelsMaxContextLength returns the effective max context length with improved handling
func GetModelsMaxContextLength(model ListModelResponse) (int, error) {
	fullModelName := model.Model

	// 1. Check full model name override first
	if ctxLen, ok := modelContextLengthsFullNames[fullModelName]; ok {
		return ctxLen, nil
	}

	// 2. Try base model name
	baseModelName := parseOllamaModelName(fullModelName)
	baseCtxLen, ok := modelContextLengths[baseModelName]
	if !ok {
		return 0, fmt.Errorf("base name '%s' from model '%s'", baseModelName, fullModelName)
	}

	// 3. Apply smart adjustments based on model metadata
	adjustedCtxLen := baseCtxLen
	details := model.Details

	// Handle special cases using model metadata
	switch {
	case containsAny(details.Families, []string{"extended-context", "long-context"}):
		adjustedCtxLen = int(float64(adjustedCtxLen) * 2.0)
	case details.ParameterSize == "70B" && baseModelName == "llama3":
		// Llama3 70B uses Grouped Query Attention for better context handling
		adjustedCtxLen = 8192 // Explicit set as official context window
	case strings.Contains(details.QuantizationLevel, "4-bit"):
		// Quantized models might have reduced effective context
		adjustedCtxLen = int(float64(adjustedCtxLen) * 0.8)
	}

	// 4. Cap values based on known limits
	if maxCap, ok := modelContextLengthsFullNames[baseModelName+"-max"]; ok {
		if adjustedCtxLen > maxCap {
			adjustedCtxLen = maxCap
		}
	}

	return adjustedCtxLen, nil
}

// Helper function for slice contains check
func containsAny(slice []string, items []string) bool {
	for _, s := range slice {
		for _, item := range items {
			if strings.EqualFold(s, item) {
				return true
			}
		}
	}
	return false
}

// parseOllamaModelName extracts the base model name before the first colon
func parseOllamaModelName(modelName string) string {
	if parts := strings.SplitN(modelName, ":", 2); len(parts) > 0 {
		return parts[0]
	}
	return modelName
}

type ListModelResponse struct {
	Name       string       `json:"name"`
	Model      string       `json:"model"`
	ModifiedAt time.Time    `json:"modified_at"`
	Size       int64        `json:"size"`
	Digest     string       `json:"digest"`
	Details    ModelDetails `json:"details"`
}

type ModelDetails struct {
	ParentModel       string   `json:"parent_model"`
	Format            string   `json:"format"`
	Family            string   `json:"family"`
	Families          []string `json:"families"`
	ParameterSize     string   `json:"parameter_size"`
	QuantizationLevel string   `json:"quantization_level"`
}
