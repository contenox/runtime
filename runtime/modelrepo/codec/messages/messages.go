package messages

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/contenox/runtime/runtime/modelrepo"
)

const DefaultMaxTokens = 4096

type Request struct {
	Model         string          `json:"model,omitempty"`
	MaxTokens     int             `json:"max_tokens"`
	System        any             `json:"system,omitempty"`
	Messages      []wireMessage   `json:"messages"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	StopSequences []string        `json:"stop_sequences,omitempty"`
	Tools         []wireTool      `json:"tools,omitempty"`
	ToolChoice    *ToolChoice     `json:"tool_choice,omitempty"`
	Thinking      *ThinkingConfig `json:"thinking,omitempty"`
	OutputConfig  *OutputConfig   `json:"output_config,omitempty"`
	Stream        bool            `json:"stream,omitempty"`
}

type ToolChoice struct {
	Type                   string `json:"type"`
	Name                   string `json:"name,omitempty"`
	DisableParallelToolUse bool   `json:"disable_parallel_tool_use,omitempty"`
}

type CacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

const MaxCacheBreakpoints = 4

const ThinkingBlocksMetaKey = "anthropic_thinking_blocks"

type ThinkingConfig struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
	Display      string `json:"display,omitempty"`
}

const MinThinkingBudget = 1024

type OutputConfig struct {
	Effort string        `json:"effort,omitempty"`
	Format *OutputFormat `json:"format,omitempty"`
}

type OutputFormat struct {
	Type   string `json:"type"`
	Schema any    `json:"schema"`
}

type wireMessage struct {
	Role    string      `json:"role"`
	Content []wireBlock `json:"content"`
}

type wireBlock struct {
	Type         string           `json:"type"`
	Text         string           `json:"text,omitempty"`
	Source       *wireImageSource `json:"source,omitempty"`
	ID           string           `json:"id,omitempty"`
	Name         string           `json:"name,omitempty"`
	Input        json.RawMessage  `json:"input,omitempty"`
	ToolUseID    string           `json:"tool_use_id,omitempty"`
	Content      any              `json:"content,omitempty"`
	IsError      bool             `json:"is_error,omitempty"`
	Thinking     string           `json:"thinking,omitempty"`
	Signature    string           `json:"signature,omitempty"`
	Data         string           `json:"data,omitempty"`
	CacheControl *CacheControl    `json:"cache_control,omitempty"`
}

type wireImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type wireTool struct {
	Name         string        `json:"name"`
	Description  string        `json:"description,omitempty"`
	InputSchema  any           `json:"input_schema,omitempty"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

func Build(messages []modelrepo.Message, cfg *modelrepo.ChatConfig) (Request, map[string]string, error) {
	req := Request{MaxTokens: DefaultMaxTokens}
	nameMap := make(map[string]string)
	origToSanitized := make(map[string]string)

	if cfg != nil {
		if cfg.MaxTokens != nil && *cfg.MaxTokens > 0 {
			req.MaxTokens = *cfg.MaxTokens
		}
		req.Temperature = cfg.Temperature
		req.TopP = cfg.TopP
		req.StopSequences = cfg.StopSequences

		seen := map[string]int{}
		for _, t := range cfg.Tools {
			if strings.ToLower(t.Type) != "function" || t.Function == nil {
				continue
			}
			orig := t.Function.Name
			name := sanitizeToolName(orig)
			if name == "" {
				name = "tool"
			}
			name = uniquifyToolName(seen, name)
			nameMap[name] = orig
			origToSanitized[orig] = name

			req.Tools = append(req.Tools, wireTool{
				Name:        name,
				Description: t.Function.Description,
				InputSchema: wireInputSchema(t.Function.Parameters),
			})
		}
		// Providers reject a tool_choice that names no tools, so the choice
		// only travels with the definitions it applies to.
		if len(req.Tools) > 0 {
			req.ToolChoice = wireToolChoice(cfg)
			if cfg.ToolChoice != nil && cfg.ToolChoice.Tool != "" && req.ToolChoice != nil {
				declared := origToSanitized[cfg.ToolChoice.Tool]
				if declared == "" {
					declared = sanitizeToolName(cfg.ToolChoice.Tool)
				}
				req.ToolChoice.Name = declared
			}
		}
		req.OutputConfig = wireOutputFormat(cfg)
	}

	var systemParts []string
	var srcIdx []int
	for i, m := range messages {
		before := len(req.Messages)
		switch m.Role {
		case "system":
			if m.Content != "" {
				systemParts = append(systemParts, m.Content)
			}
		case "tool":
			result, err := toolResultBlock(m)
			if err != nil {
				return Request{}, nil, err
			}
			req.Messages = append(req.Messages, wireMessage{
				Role:    "user",
				Content: []wireBlock{result},
			})
		case "assistant", "model":
			var blocks []wireBlock
			blocks = append(blocks, thinkingBlocksFromToolCalls(m.ToolCalls)...)
			if m.Content != "" {
				blocks = append(blocks, wireBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				name := tc.Function.Name
				if san, ok := origToSanitized[name]; ok {
					name = san
				} else {
					name = sanitizeToolName(name)
					if name == "" {
						name = "tool"
					}
				}

				input := json.RawMessage(tc.Function.Arguments)
				if len(strings.TrimSpace(tc.Function.Arguments)) == 0 {
					input = json.RawMessage("{}")
				}
				blocks = append(blocks, wireBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  name,
					Input: input,
				})
			}
			if len(blocks) > 0 {
				req.Messages = append(req.Messages, wireMessage{Role: "assistant", Content: blocks})
			}
		default:
			if len(m.Images) > 0 {
				blocks, err := imageContentBlocks(m)
				if err != nil {
					return Request{}, nil, err
				}
				req.Messages = append(req.Messages, wireMessage{
					Role:    "user",
					Content: blocks,
				})
			} else if m.Content != "" {
				req.Messages = append(req.Messages, wireMessage{
					Role:    "user",
					Content: []wireBlock{{Type: "text", Text: m.Content}},
				})
			}
		}
		for range req.Messages[before:] {
			srcIdx = append(srcIdx, i)
		}
	}
	if len(systemParts) > 0 {
		req.System = strings.Join(systemParts, "\n\n")
	}
	if cfg != nil {
		applyCacheHints(&req, cfg.CacheHints, srcIdx)
	}
	return req, nameMap, nil
}

func ImageMediaTypes() []string {
	return []string{"image/jpeg", "image/png", "image/gif", "image/webp"}
}

func acceptsImageMediaType(mediaType string) bool {
	for _, accepted := range ImageMediaTypes() {
		if mediaType == accepted {
			return true
		}
	}
	return false
}

func imageBlock(img modelrepo.ImagePart) (wireBlock, error) {
	if !acceptsImageMediaType(img.MimeType) {
		return wireBlock{}, fmt.Errorf("messages: image media type %q is not accepted (accepted: %s)",
			img.MimeType, strings.Join(ImageMediaTypes(), ", "))
	}
	return wireBlock{
		Type: "image",
		Source: &wireImageSource{
			Type:      "base64",
			MediaType: img.MimeType,
			Data:      base64.StdEncoding.EncodeToString(img.Data),
		},
	}, nil
}

func toolResultBlock(m modelrepo.Message) (wireBlock, error) {
	block := wireBlock{
		Type:      "tool_result",
		ToolUseID: m.ToolCallID,
		Content:   m.Content,
		IsError:   m.ToolResultError,
	}
	if len(m.Images) == 0 {
		return block, nil
	}
	blocks := make([]wireBlock, 0, len(m.Images)+1)
	if m.Content != "" {
		blocks = append(blocks, wireBlock{Type: "text", Text: m.Content})
	}
	for _, img := range m.Images {
		rendered, err := imageBlock(img)
		if err != nil {
			return wireBlock{}, err
		}
		blocks = append(blocks, rendered)
	}
	block.Content = blocks
	return block, nil
}

func wireOutputFormat(cfg *modelrepo.ChatConfig) *OutputConfig {
	if cfg == nil || cfg.Format == nil || cfg.Format.Mode != modelrepo.ResponseFormatJSONSchema {
		return nil
	}
	return &OutputConfig{Format: &OutputFormat{Type: "json_schema", Schema: cfg.Format.Schema}}
}

func wireToolChoice(cfg *modelrepo.ChatConfig) *ToolChoice {
	if cfg == nil {
		return nil
	}
	parallelDisabled := cfg.ParallelToolCalls != nil && !*cfg.ParallelToolCalls
	if cfg.ToolChoice == nil && !parallelDisabled {
		return nil
	}
	choice := &ToolChoice{Type: "auto"}
	if cfg.ToolChoice != nil {
		switch cfg.ToolChoice.Mode {
		case modelrepo.ToolChoiceNone:
			choice.Type = "none"
		case modelrepo.ToolChoiceRequired:
			choice.Type = "any"
		case modelrepo.ToolChoiceAuto:
			choice.Type = "auto"
		}
		choice.Name = cfg.ToolChoice.Tool
		if choice.Name != "" {
			choice.Type = "tool"
		}
	}
	choice.DisableParallelToolUse = parallelDisabled
	return choice
}

func wireInputSchema(parameters any) any {
	if parameters == nil {
		return map[string]any{"type": "object"}
	}
	return parameters
}

func applyCacheHints(req *Request, hints *modelrepo.CacheHints, srcIdx []int) {
	if hints == nil {
		return
	}
	cc := func() *CacheControl {
		c := &CacheControl{Type: "ephemeral"}
		if hints.TTL >= time.Hour {
			c.TTL = "1h"
		}
		return c
	}
	placed := 0
	if hints.StableTools && len(req.Tools) > 0 && placed < MaxCacheBreakpoints {
		req.Tools[len(req.Tools)-1].CacheControl = cc()
		placed++
	}
	if hints.StableSystem && placed < MaxCacheBreakpoints {
		if sys, ok := req.System.(string); ok && sys != "" {
			req.System = []wireBlock{{Type: "text", Text: sys, CacheControl: cc()}}
			placed++
		}
	}
	if hints.StableHistoryLen > 0 && placed < MaxCacheBreakpoints {
		last := -1
		for j, src := range srcIdx {
			if src < hints.StableHistoryLen {
				last = j
			}
		}
		if last >= 0 && last < len(req.Messages) {
			if blocks := req.Messages[last].Content; len(blocks) > 0 {
				if t := blocks[len(blocks)-1].Type; t != "thinking" && t != "redacted_thinking" {
					blocks[len(blocks)-1].CacheControl = cc()
				}
			}
		}
	}
}

func thinkingBlocksFromToolCalls(toolCalls []modelrepo.ToolCall) []wireBlock {
	for _, tc := range toolCalls {
		raw := tc.ProviderMeta[ThinkingBlocksMetaKey]
		if raw == "" {
			continue
		}
		var blocks []wireBlock
		if err := json.Unmarshal([]byte(raw), &blocks); err != nil {
			return nil
		}
		return blocks
	}
	return nil
}

func StripThinkingBlocks(req *Request) {
	if req == nil {
		return
	}
	msgs := req.Messages[:0]
	for _, m := range req.Messages {
		blocks := m.Content[:0]
		for _, b := range m.Content {
			if b.Type == "thinking" || b.Type == "redacted_thinking" {
				continue
			}
			blocks = append(blocks, b)
		}
		m.Content = blocks
		if len(m.Content) == 0 {
			continue
		}
		msgs = append(msgs, m)
	}
	req.Messages = msgs
}

func imageContentBlocks(m modelrepo.Message) ([]wireBlock, error) {
	blocks := make([]wireBlock, 0, len(m.Images)+1)
	if m.Content != "" {
		blocks = append(blocks, wireBlock{Type: "text", Text: m.Content})
	}
	for _, img := range m.Images {
		rendered, err := imageBlock(img)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, rendered)
	}
	return blocks, nil
}

type Response struct {
	Role         string          `json:"role"`
	Content      []responseBlock `json:"content"`
	StopReason   string          `json:"stop_reason"`
	StopSequence string          `json:"stop_sequence"`
	StopDetails  *stopDetails    `json:"stop_details"`
	Usage        *wireUsage      `json:"usage"`
}

type stopDetails struct {
	Type        string `json:"type"`
	Category    string `json:"category"`
	Explanation string `json:"explanation"`
}

func (d *stopDetails) RefusalReason() string {
	if d == nil {
		return ""
	}
	switch {
	case d.Explanation != "" && d.Category != "":
		return d.Category + ": " + d.Explanation
	case d.Explanation != "":
		return d.Explanation
	default:
		return d.Category
	}
}

type wireUsage struct {
	InputTokens              int                 `json:"input_tokens"`
	OutputTokens             int                 `json:"output_tokens"`
	CacheCreationInputTokens int                 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int                 `json:"cache_read_input_tokens"`
	CacheCreation            *cacheCreationUsage `json:"cache_creation"`
	OutputTokensDetails      *outputTokenDetails `json:"output_tokens_details"`
}

type cacheCreationUsage struct {
	Ephemeral5mInputTokens int `json:"ephemeral_5m_input_tokens"`
	Ephemeral1hInputTokens int `json:"ephemeral_1h_input_tokens"`
}

type outputTokenDetails struct {
	ThinkingTokens int `json:"thinking_tokens"`
}

func NormalizeUsage(u wireUsage) modelrepo.TokenUsage {
	written := u.CacheCreationInputTokens
	if u.CacheCreation != nil {
		written = u.CacheCreation.Ephemeral5mInputTokens + u.CacheCreation.Ephemeral1hInputTokens
	}
	prompt := u.InputTokens + u.CacheReadInputTokens + written
	usage := modelrepo.TokenUsage{
		PromptTokens:     prompt,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      prompt + u.OutputTokens,
		CacheReadTokens:  u.CacheReadInputTokens,
		CacheWriteTokens: written,
	}
	if u.OutputTokensDetails != nil {
		usage.ThinkingTokens = u.OutputTokensDetails.ThinkingTokens
	}
	return usage
}

func normalizeUsage(u wireUsage) modelrepo.TokenUsage { return NormalizeUsage(u) }

type responseBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	Signature string          `json:"signature"`
	Data      string          `json:"data"`
}

func DecodeResponse(raw []byte, nameMap map[string]string) (modelrepo.ChatResult, error) {
	var resp Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return modelrepo.ChatResult{}, fmt.Errorf("messages: decode response: %w", err)
	}
	if resp.StopReason == "refusal" {
		return modelrepo.ChatResult{}, &modelrepo.RefusalError{
			Provider: "anthropic",
			Code:     resp.StopDetails.category(),
			Text:     resp.StopDetails.RefusalReason(),
		}
	}
	var text, thinking strings.Builder
	var toolCalls []modelrepo.ToolCall
	var thinkingBlocks []wireBlock
	for _, b := range resp.Content {
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
		case "thinking":
			thinking.WriteString(b.Thinking)
			thinkingBlocks = append(thinkingBlocks, wireBlock{Type: "thinking", Thinking: b.Thinking, Signature: b.Signature})
		case "redacted_thinking":
			thinkingBlocks = append(thinkingBlocks, wireBlock{Type: "redacted_thinking", Data: b.Data})
		case "tool_use":
			args := string(b.Input)
			if strings.TrimSpace(args) == "" {
				args = "{}"
			}
			name := b.Name
			if orig, ok := nameMap[name]; ok && orig != "" {
				name = orig
			}
			toolCalls = append(toolCalls, newToolCall(b.ID, name, args))
		}
	}
	attachThinkingBlocks(toolCalls, thinkingBlocks)
	role := resp.Role
	if role == "" {
		role = "assistant"
	}
	var usage *modelrepo.TokenUsage
	if resp.Usage != nil {
		u := NormalizeUsage(*resp.Usage)
		usage = &u
	}
	return modelrepo.ChatResult{
		Message: modelrepo.Message{
			Role:     role,
			Content:  text.String(),
			Thinking: thinking.String(),
		},
		ToolCalls:    toolCalls,
		Usage:        usage,
		FinishReason: resp.StopReason,
	}, nil
}

func (d *stopDetails) category() string {
	if d == nil {
		return ""
	}
	return d.Category
}

func attachThinkingBlocks(toolCalls []modelrepo.ToolCall, blocks []wireBlock) {
	if len(toolCalls) == 0 || len(blocks) == 0 {
		return
	}
	raw, err := json.Marshal(blocks)
	if err != nil {
		return
	}
	if toolCalls[0].ProviderMeta == nil {
		toolCalls[0].ProviderMeta = map[string]string{}
	}
	toolCalls[0].ProviderMeta[ThinkingBlocksMetaKey] = string(raw)
}

func newToolCall(id, name, args string) modelrepo.ToolCall {
	tc := modelrepo.ToolCall{ID: id, Type: "function"}
	tc.Function.Name = name
	tc.Function.Arguments = args
	return tc
}

type streamEvent struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
		Data string `json:"data"`
	} `json:"content_block"`
	Delta struct {
		Type         string       `json:"type"`
		Text         string       `json:"text"`
		Thinking     string       `json:"thinking"`
		PartialJSON  string       `json:"partial_json"`
		Signature    string       `json:"signature"`
		StopReason   string       `json:"stop_reason"`
		StopSequence string       `json:"stop_sequence"`
		StopDetails  *stopDetails `json:"stop_details"`
	} `json:"delta"`
	Message struct {
		Usage wireUsage `json:"usage"`
	} `json:"message"`
	Usage wireUsage `json:"usage"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type StreamDecoder struct {
	nameMap      map[string]string
	stopReason   string
	stopSequence string
	stopDetails  *stopDetails
	usage        wireUsage
	sawUsage     bool
	terminated   bool

	openThinking     *wireBlock
	thinkingBlocks   []wireBlock
	thinkingAttached bool
}

func NewStreamDecoder(nameMap map[string]string) *StreamDecoder {
	return &StreamDecoder{nameMap: nameMap}
}

func (d *StreamDecoder) DecodeLine(payload []byte) ([]*modelrepo.StreamParcel, error) {
	var ev streamEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		return nil, fmt.Errorf("messages: decode stream event: %w", err)
	}
	switch ev.Type {
	case "error":
		return nil, fmt.Errorf("messages: in-stream error event: %s: %s", ev.Error.Type, ev.Error.Message)
	case "message_start":
		d.recordUsage(ev.Message.Usage)
		return nil, nil
	case "message_delta":
		if ev.Delta.StopReason != "" {
			d.stopReason = ev.Delta.StopReason
		}
		if ev.Delta.StopSequence != "" {
			d.stopSequence = ev.Delta.StopSequence
		}
		if ev.Delta.StopDetails != nil {
			d.stopDetails = ev.Delta.StopDetails
		}
		d.recordUsage(ev.Usage)
		return nil, nil
	case "content_block_start":
		switch ev.ContentBlock.Type {
		case "tool_use":
			name := ev.ContentBlock.Name
			if orig, ok := d.nameMap[name]; ok && orig != "" {
				name = orig
			}
			delta := &modelrepo.ToolCallDelta{
				Index: ev.Index,
				ID:    ev.ContentBlock.ID,
				Type:  "function",
				Name:  name,
			}
			if meta := d.takeThinkingBlocksMeta(); meta != "" {
				delta.ProviderMeta = map[string]string{ThinkingBlocksMetaKey: meta}
			}
			return []*modelrepo.StreamParcel{{ToolCall: delta}}, nil
		case "thinking":
			d.openThinking = &wireBlock{Type: "thinking"}
		case "redacted_thinking":
			d.thinkingBlocks = append(d.thinkingBlocks, wireBlock{Type: "redacted_thinking", Data: ev.ContentBlock.Data})
		}
		return nil, nil
	case "content_block_delta":
		switch ev.Delta.Type {
		case "text_delta":
			if ev.Delta.Text != "" {
				return []*modelrepo.StreamParcel{{Data: ev.Delta.Text}}, nil
			}
		case "thinking_delta":
			if ev.Delta.Thinking != "" {
				if d.openThinking != nil {
					d.openThinking.Thinking += ev.Delta.Thinking
				}
				return []*modelrepo.StreamParcel{{Thinking: ev.Delta.Thinking}}, nil
			}
		case "signature_delta":
			if d.openThinking != nil {
				d.openThinking.Signature += ev.Delta.Signature
			}
		case "input_json_delta":
			if ev.Delta.PartialJSON != "" {
				return []*modelrepo.StreamParcel{{ToolCall: &modelrepo.ToolCallDelta{
					Index:        ev.Index,
					ArgsFragment: ev.Delta.PartialJSON,
				}}}, nil
			}
		}
		return nil, nil
	case "content_block_stop":
		if d.openThinking != nil {
			d.thinkingBlocks = append(d.thinkingBlocks, *d.openThinking)
			d.openThinking = nil
		}
		return nil, nil
	case "message_stop":
		d.terminated = true
		return nil, nil
	default:
		return nil, nil
	}
}

func (d *StreamDecoder) takeThinkingBlocksMeta() string {
	if d.thinkingAttached || len(d.thinkingBlocks) == 0 {
		return ""
	}
	raw, err := json.Marshal(d.thinkingBlocks)
	if err != nil {
		return ""
	}
	d.thinkingAttached = true
	return string(raw)
}

func (d *StreamDecoder) recordUsage(u wireUsage) {
	if u.InputTokens == 0 && u.OutputTokens == 0 &&
		u.CacheCreationInputTokens == 0 && u.CacheReadInputTokens == 0 {
		return
	}
	d.sawUsage = true
	if u.InputTokens != 0 {
		d.usage.InputTokens = u.InputTokens
	}
	if u.OutputTokens != 0 {
		d.usage.OutputTokens = u.OutputTokens
	}
	if u.CacheCreationInputTokens != 0 {
		d.usage.CacheCreationInputTokens = u.CacheCreationInputTokens
	}
	if u.CacheReadInputTokens != 0 {
		d.usage.CacheReadInputTokens = u.CacheReadInputTokens
	}
	if u.OutputTokensDetails != nil {
		d.usage.OutputTokensDetails = u.OutputTokensDetails
	}
}

func (d *StreamDecoder) Finish() *modelrepo.StreamParcel {
	term := &modelrepo.StreamTerminal{
		FinishReason: d.stopReason,
		Terminated:   d.terminated,
		Refusal:      d.stopDetails.RefusalReason(),
		StopSequence: d.stopSequence,
	}
	if d.sawUsage {
		u := NormalizeUsage(d.usage)
		term.Usage = &u
	}
	return &modelrepo.StreamParcel{Terminal: term}
}

func sanitizeToolName(in string) string {
	if in == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range in {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_-")
	if len(out) > 128 {
		out = out[:128]
	}
	return out
}

func uniquifyToolName(seen map[string]int, name string) string {
	if _, ok := seen[name]; !ok {
		seen[name] = 1
		return name
	}
	i := seen[name]
	for {
		suffix := fmt.Sprintf("_%d", i)
		base := name
		if len(base)+len(suffix) > 128 {
			base = base[:128-len(suffix)]
		}
		candidate := base + suffix
		if _, ok := seen[candidate]; !ok {
			seen[name] = i + 1
			seen[candidate] = 1
			return candidate
		}
		i++
	}
}
