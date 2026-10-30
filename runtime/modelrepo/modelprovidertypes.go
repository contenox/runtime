package modelrepo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/contenox/runtime/runtime/reasoning"
)

var ErrRefused = errors.New("model refused the request")

type RefusalError struct {
	Provider string
	Code     string
	Text     string
}

func (e *RefusalError) Error() string {
	s := "model refused the request"
	if e.Provider != "" {
		s += " (" + e.Provider + ")"
	}
	if e.Code != "" {
		s += " (" + e.Code + ")"
	}
	if e.Text != "" {
		s += ": " + e.Text
	}
	return s
}

func (e *RefusalError) Unwrap() error { return ErrRefused }

const ToolResultErrorKey = "tool_result_is_error"

type ThinkingTypes struct {
	Adaptive     bool     `json:"adaptive"`
	Enabled      bool     `json:"enabled"`
	Disabled     bool     `json:"disabled"`
	EffortLevels []string `json:"effortLevels,omitempty"`
}

func (t *ThinkingTypes) SupportsEffort(level string) bool {
	if t == nil {
		return false
	}
	for _, l := range t.EffortLevels {
		if l == level {
			return true
		}
	}
	return false
}

func (t *ThinkingTypes) Controls() bool {
	return t != nil && (t.Adaptive || t.Enabled || t.Disabled)
}

func (t *ThinkingTypes) Validate() error {
	if t == nil {
		return nil
	}
	for _, level := range t.EffortLevels {
		switch level {
		case reasoning.Low, reasoning.Medium, reasoning.High, reasoning.XHigh, reasoning.Max:
		default:
			return fmt.Errorf("thinking declaration names unknown effort level %q", level)
		}
	}
	return nil
}

const MaxStopSequences = 4

type ChatResult struct {
	Message      Message
	ToolCalls    []ToolCall
	Usage        *TokenUsage
	FinishReason string
	Refusal      string
}

type ToolCall struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
	ProviderMeta map[string]string `json:"provider_meta,omitempty"`
	ArgsMissing  bool              `json:"-"`
}

type ImagePart struct {
	Data     []byte `json:"data"`
	MimeType string `json:"mime_type"`
}

type AudioPart struct {
	Data     []byte `json:"data"`
	MimeType string `json:"mime_type"`
}

type Message struct {
	Continuation *Continuation `json:"continuation,omitempty"`
	Role         string        `json:"role"`
	Content      string        `json:"content"`
	Images       []ImagePart   `json:"images,omitempty"`
	Audio        []AudioPart   `json:"audio,omitempty"`
	Thinking     string        `json:"thinking,omitempty"`
	Refusal      string        `json:"refusal,omitempty"`

	ToolCalls       []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID      string     `json:"tool_call_id,omitempty"`
	ToolResultError bool       `json:"tool_result_error,omitempty"`
}

func MessagesHaveImages(messages []Message) bool {
	for _, m := range messages {
		if len(m.Images) > 0 {
			return true
		}
	}
	return false
}

func MessagesImageCount(messages []Message) int {
	count := 0
	for _, m := range messages {
		count += len(m.Images)
	}
	return count
}

func MessagesHaveAudio(messages []Message) bool {
	for _, m := range messages {
		if len(m.Audio) > 0 {
			return true
		}
	}
	return false
}

type ChatArgument interface {
	Apply(config *ChatConfig)
}

type ToolCallDelta struct {
	Index        int
	ID           string
	Type         string
	Name         string
	ArgsFragment string
	ArgsReplace  bool
	ProviderMeta map[string]string
}

type TokenUsage struct {
	PromptTokens     int
	CompletionTokens int
	ThinkingTokens   int
	TotalTokens      int
	CacheReadTokens  int
	CacheWriteTokens int
}

type StreamTerminal struct {
	Continuation *Continuation
	FinishReason string
	Usage        *TokenUsage
	Refusal      string
	StopSequence string
	Terminated   bool
}

type Continuation struct {
	Provider string          `json:"provider"`
	Model    string          `json:"model"`
	Items    json.RawMessage `json:"items"`
}

type StreamParcel struct {
	Data     string
	Thinking string
	ToolCall *ToolCallDelta
	Usage    *TokenUsage
	Terminal *StreamTerminal
	Error    error
}

type Tool struct {
	Type     string        `json:"type"`
	Function *FunctionTool `json:"function,omitempty"`
}

type FunctionTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Parameters  interface{} `json:"parameters,omitempty"`
}

type CacheHints struct {
	SessionKey       string
	StableSystem     bool
	StableTools      bool
	StableHistoryLen int
	TTL              time.Duration
}

type ToolChoiceMode string

const (
	ToolChoiceAuto     ToolChoiceMode = "auto"
	ToolChoiceNone     ToolChoiceMode = "none"
	ToolChoiceRequired ToolChoiceMode = "required"
)

type ToolChoice struct {
	Mode ToolChoiceMode
	Tool string
}

func (t ToolChoice) Validate() error {
	switch t.Mode {
	case ToolChoiceAuto, ToolChoiceNone:
		if t.Tool != "" {
			return fmt.Errorf("tool choice %q does not select a named tool", t.Mode)
		}
	case ToolChoiceRequired:
	default:
		return fmt.Errorf("invalid tool choice mode %q (valid: auto, none, required)", t.Mode)
	}
	return nil
}

type ResponseFormatMode string

const (
	ResponseFormatText       ResponseFormatMode = "text"
	ResponseFormatJSONObject ResponseFormatMode = "json_object"
	ResponseFormatJSONSchema ResponseFormatMode = "json_schema"
)

type ResponseFormat struct {
	Mode   ResponseFormatMode
	Name   string
	Schema any
	Strict bool
}

func (f ResponseFormat) Validate() error {
	switch f.Mode {
	case ResponseFormatText, ResponseFormatJSONObject:
	case ResponseFormatJSONSchema:
		if f.Schema == nil {
			return errors.New("json_schema response format requires a schema")
		}
	default:
		return fmt.Errorf("invalid response format %q (valid: text, json_object, json_schema)", f.Mode)
	}
	return nil
}

type ChatConfig struct {
	Temperature       *float64        `json:"temperature,omitempty"`
	MaxTokens         *int            `json:"max_tokens,omitempty"`
	TopP              *float64        `json:"top_p,omitempty"`
	Seed              *int            `json:"seed,omitempty"`
	Tools             []Tool          `json:"tools,omitempty"`
	Think             *string         `json:"think,omitempty"`
	StopSequences     []string        `json:"stop_sequences,omitempty"`
	ToolChoice        *ToolChoice     `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool           `json:"parallel_tool_calls,omitempty"`
	Format            *ResponseFormat `json:"response_format,omitempty"`
	Shift             *bool           `json:"shift,omitempty"`
	Truncate          *bool           `json:"truncate,omitempty"`
	CacheHints        *CacheHints     `json:"-"`
}

func (c *ChatConfig) Validate() error {
	if c == nil {
		return nil
	}
	if c.MaxTokens != nil && *c.MaxTokens <= 0 {
		return fmt.Errorf("max tokens must be positive, got %d", *c.MaxTokens)
	}
	if c.Temperature != nil && (math.IsNaN(*c.Temperature) || math.IsInf(*c.Temperature, 0)) {
		return fmt.Errorf("temperature must be a finite number, got %v", *c.Temperature)
	}
	if c.TopP != nil && (math.IsNaN(*c.TopP) || math.IsInf(*c.TopP, 0) || *c.TopP <= 0 || *c.TopP > 1) {
		return fmt.Errorf("top_p must be in (0, 1], got %v", *c.TopP)
	}
	if len(c.StopSequences) > MaxStopSequences {
		return fmt.Errorf("at most %d stop sequences are supported, got %d", MaxStopSequences, len(c.StopSequences))
	}
	for _, s := range c.StopSequences {
		if s == "" {
			return errors.New("a stop sequence must not be empty")
		}
	}
	if c.ToolChoice != nil {
		if err := c.ToolChoice.Validate(); err != nil {
			return err
		}
	}
	if c.Format != nil {
		if err := c.Format.Validate(); err != nil {
			return err
		}
	}
	if c.ParallelToolCalls != nil && c.ToolChoice != nil && c.ToolChoice.Mode == ToolChoiceNone && *c.ParallelToolCalls {
		return errors.New("parallel tool calls cannot be enabled when tool choice is none")
	}
	return nil
}

type PromptConfig struct {
	MaxTokens *int
}

type PromptArgument interface {
	Apply(*PromptConfig)
}

type WithPromptMaxTokens int

func (w WithPromptMaxTokens) Apply(config *PromptConfig) {
	maxTokens := int(w)
	config.MaxTokens = &maxTokens
}

func PromptConfigFromArgs(args ...PromptArgument) (*PromptConfig, error) {
	config := &PromptConfig{}
	for _, arg := range args {
		if arg == nil {
			return nil, errors.New("prompt argument is required")
		}
		arg.Apply(config)
	}
	if config.MaxTokens != nil && *config.MaxTokens <= 0 {
		return nil, fmt.Errorf("prompt max tokens must be positive, got %d", *config.MaxTokens)
	}
	return config, nil
}

type WithThink string

func (w WithThink) Apply(cfg *ChatConfig) {
	s := string(w)
	cfg.Think = &s
}

type WithShift struct{}

func (WithShift) Apply(cfg *ChatConfig) {
	t := true
	cfg.Shift = &t
}

type LLMChatClient interface {
	Chat(ctx context.Context, messages []Message, args ...ChatArgument) (ChatResult, error)
}

type LLMEmbedClient interface {
	Embed(ctx context.Context, prompt string) ([]float64, error)
}

type LLMStreamClient interface {
	Stream(ctx context.Context, messages []Message, args ...ChatArgument) (<-chan *StreamParcel, error)
}

type LLMPromptExecClient interface {
	Prompt(ctx context.Context, systemInstruction string, temperature float32, prompt string, args ...PromptArgument) (string, *TokenUsage, error)
}

func (u *TokenUsage) Merge(src *TokenUsage) {
	if src == nil {
		return
	}
	if src.PromptTokens != 0 {
		u.PromptTokens = src.PromptTokens
	}
	if src.CompletionTokens != 0 {
		u.CompletionTokens = src.CompletionTokens
	}
	if src.ThinkingTokens != 0 {
		u.ThinkingTokens = src.ThinkingTokens
	}
	if src.TotalTokens != 0 {
		u.TotalTokens = src.TotalTokens
	}
	if src.CacheReadTokens != 0 {
		u.CacheReadTokens = src.CacheReadTokens
	}
	if src.CacheWriteTokens != 0 {
		u.CacheWriteTokens = src.CacheWriteTokens
	}
}
