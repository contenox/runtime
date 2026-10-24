package chatcompletions

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/reasoning"
)

type Request struct {
	Model               string             `json:"model"`
	Messages            []Message          `json:"messages"`
	Temperature         *float64           `json:"temperature,omitempty"`
	MaxTokens           *int               `json:"max_tokens,omitempty"`
	TopP                *float64           `json:"top_p,omitempty"`
	Seed                *int               `json:"seed,omitempty"`
	Tools               []Tool             `json:"tools,omitempty"`
	ToolChoice          string             `json:"tool_choice,omitempty"`
	Format              *ResponseFormat    `json:"response_format,omitempty"`
	Logprobs            *bool              `json:"logprobs,omitempty"`
	TopLogprobs         *int               `json:"top_logprobs,omitempty"`
	Stream              *bool              `json:"stream,omitempty"`
	StreamOptions       *StreamOptions     `json:"stream_options,omitempty"`
	ReasoningEffort     string             `json:"reasoning_effort,omitempty"`
	MaxCompletionTokens *int               `json:"max_completion_tokens,omitempty"`
	Session             string             `json:"contenox_session,omitempty"`
	N                   *int               `json:"n,omitempty"`
	Stop                json.RawMessage    `json:"stop,omitempty"`
	FrequencyPenalty    *float64           `json:"frequency_penalty,omitempty"`
	PresencePenalty     *float64           `json:"presence_penalty,omitempty"`
	LogitBias           map[string]float64 `json:"logit_bias,omitempty"`
	ParallelToolCalls   *bool              `json:"parallel_tool_calls,omitempty"`
	Modalities          []string           `json:"modalities,omitempty"`
	Store               bool               `json:"store,omitempty"`

	stopSequences      []string
	toolChoiceFunction *NamedToolChoice
}

func (r Request) MarshalJSON() ([]byte, error) {
	type plain Request
	encoded, err := json.Marshal(plain(r))
	if err != nil {
		return nil, err
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &members); err != nil {
		return nil, err
	}
	if len(r.stopSequences) > 0 {
		stops, err := json.Marshal(r.stopSequences)
		if err != nil {
			return nil, err
		}
		members["stop"] = stops
	}
	if r.toolChoiceFunction != nil {
		choice, err := json.Marshal(r.toolChoiceFunction)
		if err != nil {
			return nil, err
		}
		delete(members, "tool_choice")
		members["tool_choice"] = choice
	}
	return json.Marshal(members)
}

func (r *Request) UnmarshalJSON(data []byte) error {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		return err
	}
	raw, hasChoice := members["tool_choice"]
	if hasChoice {
		delete(members, "tool_choice")
	}
	rest, err := json.Marshal(members)
	if err != nil {
		return err
	}
	type plain Request
	var decoded plain
	if err := json.Unmarshal(rest, &decoded); err != nil {
		return err
	}
	*r = Request(decoded)
	if !hasChoice || len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var named NamedToolChoice
	if err := json.Unmarshal(raw, &named); err == nil && named.Function.Name != "" {
		r.toolChoiceFunction = &named
		r.ToolChoice = ""
		return nil
	}
	var mode string
	if err := json.Unmarshal(raw, &mode); err != nil {
		return fmt.Errorf("chatcompletions: decode tool_choice: %w", err)
	}
	r.ToolChoice = mode
	return nil
}

type NamedToolChoice struct {
	Type     string              `json:"type"`
	Function NamedToolChoiceName `json:"function"`
}

type NamedToolChoiceName struct {
	Name string `json:"name"`
}

type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type ResponseFormat struct {
	Type       string      `json:"type"`
	JSONSchema *JSONSchema `json:"json_schema,omitempty"`
}

type JSONSchema struct {
	Name   string `json:"name"`
	Schema any    `json:"schema"`
	Strict bool   `json:"strict,omitempty"`
}

type Message struct {
	Role       string     `json:"role"`
	Content    any        `json:"content"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

type ContentPart struct {
	Type       string      `json:"type"`
	Text       string      `json:"text,omitempty"`
	ImageURL   *ImageURL   `json:"image_url,omitempty"`
	InputAudio *InputAudio `json:"input_audio,omitempty"`
}

type InputAudio struct {
	Data   string `json:"data"`
	Format string `json:"format"`
}

type ImageURL struct {
	URL string `json:"url"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Tool struct {
	Type     string         `json:"type"`
	Function ToolDefinition `json:"function"`
}

type ToolDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

func Build(model string, messages []modelrepo.Message, cfg *modelrepo.ChatConfig) (Request, map[string]string) {
	req := Request{Model: model}
	if cfg != nil {
		req.Temperature = cfg.Temperature
		req.MaxTokens = cfg.MaxTokens
		req.TopP = cfg.TopP
		req.Seed = cfg.Seed
		req.ReasoningEffort = ReasoningEffort(cfg.Think)
		req.stopSequences = cfg.StopSequences
		req.ParallelToolCalls = cfg.ParallelToolCalls
		req.Format = wireResponseFormat(cfg.Format)
	}

	nameMap := make(map[string]string)
	origToSanitized := make(map[string]string)
	if cfg != nil && len(cfg.Tools) > 0 {
		seen := map[string]int{}
		tools := make([]Tool, 0, len(cfg.Tools))
		for i, t := range cfg.Tools {
			if strings.ToLower(t.Type) != "function" || t.Function == nil {
				continue
			}
			orig := t.Function.Name
			name := sanitizeToolName(orig)
			if name == "" {
				name = fmt.Sprintf("tool_%d", i)
			}
			name = uniquifyToolName(seen, name)
			nameMap[name] = orig
			origToSanitized[orig] = name
			tools = append(tools, Tool{
				Type: "function",
				Function: ToolDefinition{
					Name:        name,
					Description: t.Function.Description,
					Parameters:  t.Function.Parameters,
				},
			})
		}
		if len(tools) > 0 {
			req.Tools = tools
		}
	}
	if cfg != nil && cfg.ToolChoice != nil {
		wireToolChoice(&req, *cfg.ToolChoice, origToSanitized)
	}

	req.Messages = make([]Message, 0, len(messages))
	for _, msg := range messages {
		wm := Message{
			Role:       msg.Role,
			ToolCallID: msg.ToolCallID,
		}
		switch {
		case len(msg.Images) > 0:
			wm.Content = wireImageContent(msg)
		case msg.Content == "" && len(msg.ToolCalls) > 0:
			wm.Content = nil
		default:
			wm.Content = msg.Content
		}
		for _, tc := range msg.ToolCalls {
			name := tc.Function.Name
			if san, ok := origToSanitized[name]; ok {
				name = san
			} else {
				name = sanitizeToolName(name)
			}
			wm.ToolCalls = append(wm.ToolCalls, ToolCall{
				ID:       tc.ID,
				Type:     tc.Type,
				Function: ToolFunction{Name: name, Arguments: tc.Function.Arguments},
			})
		}
		req.Messages = append(req.Messages, wm)
	}

	return req, nameMap
}

func wireToolChoice(req *Request, choice modelrepo.ToolChoice, sanitized map[string]string) {
	if choice.Tool == "" {
		req.ToolChoice = string(choice.Mode)
		return
	}
	name := sanitized[choice.Tool]
	if name == "" {
		name = sanitizeToolName(choice.Tool)
	}
	req.toolChoiceFunction = &NamedToolChoice{
		Type:     "function",
		Function: NamedToolChoiceName{Name: name},
	}
}

func wireResponseFormat(format *modelrepo.ResponseFormat) *ResponseFormat {
	if format == nil || format.Mode == modelrepo.ResponseFormatText {
		return nil
	}
	wire := &ResponseFormat{Type: string(format.Mode)}
	if format.Mode != modelrepo.ResponseFormatJSONSchema {
		return wire
	}
	wire.JSONSchema = &JSONSchema{Name: format.Name, Schema: format.Schema}
	if format.Strict {
		wire.JSONSchema.Strict = true
	}
	return wire
}

func wireImageContent(msg modelrepo.Message) []ContentPart {
	parts := make([]ContentPart, 0, len(msg.Images)+1)
	if msg.Content != "" {
		parts = append(parts, ContentPart{Type: "text", Text: msg.Content})
	}
	for _, img := range msg.Images {
		parts = append(parts, ContentPart{
			Type:     "image_url",
			ImageURL: &ImageURL{URL: imageDataURI(img.MimeType, img.Data)},
		})
	}
	return parts
}

func imageDataURI(mimeType string, data []byte) string {
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

type Response struct {
	Choices []struct {
		Index        int         `json:"index"`
		Message      responseMsg `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage *wireUsage `json:"usage"`
}

type wireUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptTokensDetails struct {
		CachedTokens     int `json:"cached_tokens"`
		CacheWriteTokens int `json:"cache_write_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

func (u *wireUsage) neutralUsage() *modelrepo.TokenUsage {
	if u == nil {
		return nil
	}
	total := u.TotalTokens
	if total == 0 {
		total = u.PromptTokens + u.CompletionTokens
	}
	return &modelrepo.TokenUsage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		ThinkingTokens:   u.CompletionTokensDetails.ReasoningTokens,
		TotalTokens:      total,
		CacheReadTokens:  u.PromptTokensDetails.CachedTokens,
		CacheWriteTokens: u.PromptTokensDetails.CacheWriteTokens,
	}
}

type responseMsg struct {
	Role             string      `json:"role"`
	Content          wireContent `json:"content"`
	Refusal          string      `json:"refusal"`
	ReasoningContent string      `json:"reasoning_content"`
	ToolCalls        []ToolCall  `json:"tool_calls"`
}

type wireContent struct {
	parts []contentPart
}

const contentPartText = "text"

const contentPartThinking = "thinking"

type contentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Thinking []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"thinking"`
}

func (c *wireContent) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		c.parts = []contentPart{{Type: contentPartText, Text: text}}
		return nil
	}
	var parts []contentPart
	if err := json.Unmarshal(data, &parts); err != nil {
		return fmt.Errorf("chatcompletions: decode message content: %w", err)
	}
	c.parts = parts
	return nil
}

func (c wireContent) MarshalJSON() ([]byte, error) {
	return json.Marshal(c.Text())
}

func (c wireContent) Text() string {
	var b strings.Builder
	for _, part := range c.parts {
		if part.Type == contentPartText {
			b.WriteString(part.Text)
		}
	}
	return b.String()
}

func (c wireContent) Thinking() string {
	var b strings.Builder
	for _, part := range c.parts {
		if part.Type != contentPartThinking {
			continue
		}
		for _, inner := range part.Thinking {
			if inner.Type == contentPartText {
				b.WriteString(inner.Text)
			}
		}
	}
	return b.String()
}

func (c wireContent) parcels() []*modelrepo.StreamParcel {
	parcels := make([]*modelrepo.StreamParcel, 0, len(c.parts))
	for _, part := range c.parts {
		switch part.Type {
		case contentPartText:
			if part.Text != "" {
				parcels = append(parcels, &modelrepo.StreamParcel{Data: part.Text})
			}
		case contentPartThinking:
			var b strings.Builder
			for _, inner := range part.Thinking {
				if inner.Type == contentPartText {
					b.WriteString(inner.Text)
				}
			}
			if b.Len() > 0 {
				parcels = append(parcels, &modelrepo.StreamParcel{Thinking: b.String()})
			}
		}
	}
	return parcels
}

func DecodeResponse(raw []byte, nameMap map[string]string) (modelrepo.ChatResult, error) {
	var resp Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return modelrepo.ChatResult{}, fmt.Errorf("chatcompletions: decode response: %w", err)
	}
	if len(resp.Choices) == 0 {
		return modelrepo.ChatResult{}, fmt.Errorf("chatcompletions: no choices in response")
	}
	choice := resp.Choices[0]
	if refusal := firstNonEmpty(choice.Message.Refusal, refusalText(choice.FinishReason)); refusal != "" {
		return modelrepo.ChatResult{}, refusalError(refusal)
	}
	content := choice.Message.Content.Text()
	thinking := firstNonEmpty(choice.Message.ReasoningContent, choice.Message.Content.Thinking())
	if content == "" && len(choice.Message.ToolCalls) == 0 && thinking == "" {
		return modelrepo.ChatResult{}, fmt.Errorf("chatcompletions: empty content (finish_reason=%s)", choice.FinishReason)
	}
	result := modelrepo.ChatResult{
		Message: modelrepo.Message{
			Role:     choice.Message.Role,
			Content:  content,
			Thinking: thinking,
		},
		Usage:        resp.Usage.neutralUsage(),
		FinishReason: choice.FinishReason,
	}
	result.ToolCalls = decodeToolCalls(choice.Message.ToolCalls, nameMap)
	return result, nil
}

func refusalText(finishReason string) string {
	if finishReason == refusalFinishReason {
		return refusalFinishReason
	}
	return ""
}

func refusalError(text string) error {
	if text == "" || text == refusalFinishReason {
		return modelrepo.ErrRefused
	}
	return fmt.Errorf("%w: %s", modelrepo.ErrRefused, text)
}

func decodeToolCalls(in []ToolCall, nameMap map[string]string) []modelrepo.ToolCall {
	var out []modelrepo.ToolCall
	for _, tc := range in {
		name := tc.Function.Name
		if orig, ok := nameMap[name]; ok && orig != "" {
			name = orig
		}
		out = append(out, newToolCall(tc.ID, tc.Type, name, tc.Function.Arguments))
	}
	return out
}

func newToolCall(id, typ, name, args string) modelrepo.ToolCall {
	tc := modelrepo.ToolCall{ID: id, Type: typ}
	tc.Function.Name = name
	tc.Function.Arguments = args
	return tc
}

type streamChunk struct {
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role             string      `json:"role"`
			Content          wireContent `json:"content"`
			Refusal          string      `json:"refusal"`
			ReasoningContent string      `json:"reasoning_content"`
			Reasoning        string      `json:"reasoning"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *wireUsage       `json:"usage"`
	Error *streamErrorBody `json:"error"`
}

type streamErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code"`
}

func (e *streamErrorBody) providerError() error {
	code := e.Type
	status := 0
	switch v := e.Code.(type) {
	case string:
		if v != "" {
			code = v
		}
	case float64:
		if v >= 400 && v < 600 {
			status = int(v)
		}
	}
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = "the provider streamed an error without a message"
	}
	return modelrepo.ClassifyProviderError(errors.New("the provider streamed an error frame"), status, code, message)
}

const refusalFinishReason = "refusal"

type StreamDecoder struct {
	nameMap      map[string]string
	finishReason string
	usage        *modelrepo.TokenUsage
	terminated   bool
}

func NewStreamDecoder(nameMap map[string]string) *StreamDecoder {
	return &StreamDecoder{nameMap: nameMap}
}

func (d *StreamDecoder) MarkTerminated() {
	d.terminated = true
}

func (d *StreamDecoder) DecodeLine(payload []byte) ([]*modelrepo.StreamParcel, error) {
	var chunk streamChunk
	if err := json.Unmarshal(payload, &chunk); err != nil {
		return nil, fmt.Errorf("chatcompletions: decode stream chunk: %w", err)
	}
	if chunk.Error != nil {
		return []*modelrepo.StreamParcel{{Error: chunk.Error.providerError()}}, nil
	}
	if chunk.Usage != nil {
		d.usage = chunk.Usage.neutralUsage()
	}
	if len(chunk.Choices) == 0 {
		return nil, nil
	}
	choice := chunk.Choices[0]
	if choice.FinishReason != "" {
		d.finishReason = choice.FinishReason
	}

	var parcels []*modelrepo.StreamParcel
	if choice.Delta.Refusal != "" || choice.FinishReason == refusalFinishReason {
		return []*modelrepo.StreamParcel{{Error: refusalError(firstNonEmpty(choice.Delta.Refusal, choice.FinishReason))}}, nil
	}
	parcels = append(parcels, choice.Delta.Content.parcels()...)
	if thinking := firstNonEmpty(choice.Delta.Reasoning, choice.Delta.ReasoningContent); thinking != "" {
		parcels = append(parcels, &modelrepo.StreamParcel{Thinking: thinking})
	}
	for _, tc := range choice.Delta.ToolCalls {
		name := tc.Function.Name
		if orig, ok := d.nameMap[name]; ok && orig != "" {
			name = orig
		}
		parcels = append(parcels, &modelrepo.StreamParcel{ToolCall: &modelrepo.ToolCallDelta{
			Index:        tc.Index,
			ID:           tc.ID,
			Type:         tc.Type,
			Name:         name,
			ArgsFragment: tc.Function.Arguments,
		}})
	}
	return parcels, nil
}

func (d *StreamDecoder) Finish() *modelrepo.StreamParcel {
	return &modelrepo.StreamParcel{Terminal: &modelrepo.StreamTerminal{
		FinishReason: d.finishReason,
		Usage:        d.usage,
		Terminated:   d.terminated,
	}}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func ReasoningEffort(think *string) string {
	if think == nil {
		return ""
	}
	level, err := reasoning.ResolveForWire(*think)
	if err != nil || level == reasoning.Off {
		return ""
	}
	return level
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
	return strings.Trim(b.String(), "_-")
}

func uniquifyToolName(seen map[string]int, name string) string {
	if _, ok := seen[name]; !ok {
		seen[name] = 1
		return name
	}
	i := seen[name]
	for {
		candidate := fmt.Sprintf("%s_%d", name, i)
		if _, ok := seen[candidate]; !ok {
			seen[name] = i + 1
			seen[candidate] = 1
			return candidate
		}
		i++
	}
}
