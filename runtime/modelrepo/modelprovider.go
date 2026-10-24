package modelrepo

import "context"

type Provider interface {
	GetBackendIDs() []string
	ModelName() string
	GetID() string
	GetType() string
	GetContextLength() int
	GetMaxOutputTokens() int
	CanChat() bool
	CanEmbed() bool
	CanStream() bool
	CanPrompt() bool
	CanAutocomplete() bool
	CanNextEdit() bool
	CanThink() bool
	CanVision() bool
	CanAudio() bool
	GetChatConnection(ctx context.Context, backendID string) (LLMChatClient, error)
	GetPromptConnection(ctx context.Context, backendID string) (LLMPromptExecClient, error)
	GetAutocompleteConnection(ctx context.Context, backendID string) (LLMAutocompleteClient, error)
	GetNextEditConnection(ctx context.Context, backendID string) (LLMNextEditClient, error)
	GetEmbedConnection(ctx context.Context, backendID string) (LLMEmbedClient, error)
	GetStreamConnection(ctx context.Context, backendID string) (LLMStreamClient, error)
}

type CapabilityConfig struct {
	CompletionMode   string
	FIMFormat        string
	CanAutocomplete  bool
	NextEditFormat   string
	CanNextEdit      bool
	ContextLength    int
	MaxOutputTokens  int
	CanChat          bool
	CanEmbed         bool
	CanStream        bool
	CanPrompt        bool
	CanThink         bool
	CanVision        bool
	CanAudio         bool
	AudioExtension   bool
	SessionExtension bool
	ThinkingTypes    *ThinkingTypes
	Dialect          string
	PromptCache      *PromptCacheContract
}

type PromptCacheContract struct {
	Fields []string `json:"fields,omitempty"`
	TTLs   []string `json:"ttls,omitempty"`
}

const (
	PromptCacheFieldTools    = "tools"
	PromptCacheFieldSystem   = "system"
	PromptCacheFieldMessages = "messages"
)

const (
	PromptCacheTTLFiveMinutes = "5m"
	PromptCacheTTLOneHour     = "1h"
)

func (c *PromptCacheContract) SupportsField(field string) bool {
	if c == nil {
		return false
	}
	for _, f := range c.Fields {
		if f == field {
			return true
		}
	}
	return false
}

func (c *PromptCacheContract) SupportsTTL(ttl string) bool {
	if c == nil {
		return false
	}
	for _, t := range c.TTLs {
		if t == ttl {
			return true
		}
	}
	return false
}
