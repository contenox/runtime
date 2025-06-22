package modelprovider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/contenox/contenox/core/serverops"
)

// vLLMProvider implements the Provider interface for vLLM models
type vLLMProvider struct {
	Name           string
	ID             string
	ContextLength  int
	SupportsChat   bool
	SupportsEmbed  bool
	SupportsStream bool
	SupportsPrompt bool
	Backends       []string // Base URLs to vLLM instances (e.g., "http://vllm-server:8000/v1")
}

// NewVLLMModelProvider creates a new vLLM model provider
func NewVLLMModelProvider(modelName string, backends []string, opts ...VLLMOption) Provider {
	// Determine capabilities based on model type
	baseModel := parsevLLMModelName(modelName)
	contextLength := vllmContextLengths[baseModel]
	canChat := strings.Contains(baseModel, "instruct") ||
		strings.Contains(baseModel, "chat") ||
		vllmDefaultChatModels[baseModel]

	p := &vLLMProvider{
		Name:           modelName,
		ID:             "vllm:" + modelName,
		ContextLength:  contextLength,
		SupportsChat:   canChat,
		SupportsEmbed:  false, // vLLM doesn't support embeddings by default
		SupportsStream: false, // Skipped for now
		SupportsPrompt: true,  // Always support prompt execution
		Backends:       backends,
	}

	// Apply options
	for _, opt := range opts {
		opt(p)
	}

	return p
}

func (p *vLLMProvider) GetBackendIDs() []string {
	return p.Backends
}

func (p *vLLMProvider) ModelName() string {
	return p.Name
}

func (p *vLLMProvider) GetID() string {
	return p.ID
}

func (p *vLLMProvider) GetContextLength() int {
	return p.ContextLength
}

func (p *vLLMProvider) CanChat() bool {
	return p.SupportsChat
}

func (p *vLLMProvider) CanEmbed() bool {
	return p.SupportsEmbed
}

func (p *vLLMProvider) CanStream() bool {
	return p.SupportsStream
}

func (p *vLLMProvider) CanPrompt() bool {
	return p.SupportsPrompt
}

func (p *vLLMProvider) GetChatConnection(ctx context.Context, backendID string) (serverops.LLMChatClient, error) {
	if !p.CanChat() {
		return nil, fmt.Errorf("provider %s (model %s) does not support chat", p.GetID(), p.ModelName())
	}

	// Validate backend URL
	if _, err := url.Parse(backendID); err != nil {
		return nil, fmt.Errorf("invalid backend URL '%s': %w", backendID, err)
	}

	return NewVLLMChatClient(ctx, backendID, p.ModelName(), http.DefaultClient)
}

func (p *vLLMProvider) GetPromptConnection(ctx context.Context, backendID string) (serverops.LLMPromptExecClient, error) {
	if !p.CanPrompt() {
		return nil, fmt.Errorf("provider %s (model %s) does not support prompting", p.GetID(), p.ModelName())
	}

	// Validate backend URL
	if _, err := url.Parse(backendID); err != nil {
		return nil, fmt.Errorf("invalid backend URL '%s': %w", backendID, err)
	}

	return NewVLLMPromptClient(ctx, backendID, p.ModelName(), http.DefaultClient)
}

func (p *vLLMProvider) GetEmbedConnection(ctx context.Context, backendID string) (serverops.LLMEmbedClient, error) {
	return nil, fmt.Errorf("embedding not supported by vLLM provider")
}

func (p *vLLMProvider) GetStreamConnection(ctx context.Context, backendID string) (serverops.LLMStreamClient, error) {
	return nil, fmt.Errorf("streaming not implemented for vLLM provider")
}

// Helper function to extract base model name
func parsevLLMModelName(modelName string) string {
	// Remove organization prefix if present
	if parts := strings.Split(modelName, "/"); len(parts) > 1 {
		modelName = parts[1]
	}

	// Remove specific suffixes
	modelName = strings.ReplaceAll(modelName, "-AWQ", "")
	modelName = strings.ReplaceAll(modelName, "-GPTQ", "")
	modelName = strings.ReplaceAll(modelName, "-4bit", "")
	modelName = strings.ReplaceAll(modelName, "-fp16", "")

	// Remove version numbers
	if idx := strings.LastIndex(modelName, ":"); idx != -1 {
		modelName = modelName[:idx]
	}

	return modelName
}

// Context length mappings for vLLM models
var vllmContextLengths = map[string]int{
	"llama2":         4096,
	"llama3":         8192,
	"llama3-70b":     8192,
	"mistral":        32768,
	"mixtral":        32768,
	"phi":            2048,
	"phi2":           2048,
	"phi3":           128000,
	"codellama":      16384,
	"gemma":          8192,
	"qwen":           32768,
	"qwen2":          32768,
	"yi":             4096,
	"deepseek":       4096,
	"openhermes":     4096,
	"zephyr":         32768,
	"dolphin":        32768,
	"neural-chat":    32768,
	"starling":       32768,
	"notux":          32768,
	"yi-200k":        200000,
	"qwen-100k":      100000,
	"qwen2-100k":     100000,
	"mistral-100k":   100000,
	"mixtral-100k":   100000,
	"llama3-100k":    100000,
	"llama3-400k":    400000,
	"codellama-100k": 100000,
	"deepseek-100k":  100000,
}

// Models that should default to chat support
var vllmDefaultChatModels = map[string]bool{
	"llama2":         true,
	"llama3":         true,
	"mistral":        true,
	"mixtral":        true,
	"phi":            true,
	"phi2":           true,
	"phi3":           true,
	"gemma":          true,
	"qwen":           true,
	"qwen2":          true,
	"yi":             true,
	"deepseek":       true,
	"openhermes":     true,
	"zephyr":         true,
	"dolphin":        true,
	"neural-chat":    true,
	"starling":       true,
	"notux":          true,
	"yi-200k":        true,
	"qwen-100k":      true,
	"qwen2-100k":     true,
	"mistral-100k":   true,
	"mixtral-100k":   true,
	"llama3-100k":    true,
	"llama3-400k":    true,
	"codellama-100k": true,
	"deepseek-100k":  true,
}

// Configuration options for vLLM provider
type VLLMOption func(*vLLMProvider)

func WithVLLMChatSupport(supports bool) VLLMOption {
	return func(p *vLLMProvider) {
		p.SupportsChat = supports
	}
}

func WithVLLMEmbedSupport(supports bool) VLLMOption {
	return func(p *vLLMProvider) {
		p.SupportsEmbed = supports
	}
}

func WithVLLMStreamSupport(supports bool) VLLMOption {
	return func(p *vLLMProvider) {
		p.SupportsStream = supports
	}
}

func WithVLLMContextLength(length int) VLLMOption {
	return func(p *vLLMProvider) {
		p.ContextLength = length
	}
}

func WithVLLMPromptSupport(supports bool) VLLMOption {
	return func(p *vLLMProvider) {
		p.SupportsPrompt = supports
	}
}
