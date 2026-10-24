package bedrock

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/reasoning"
)

type converseModel struct {
	name            string
	maxOutputTokens int
	thinking        *modelrepo.ThinkingTypes
	promptCache     *modelrepo.PromptCacheContract
}

const bedrockMinimumThinkingBudget = 1024

const reasoningContinuationProvider = "bedrock"

type bedrockReasoningItem struct {
	Text      string `json:"text,omitempty"`
	Signature string `json:"signature,omitempty"`
	Redacted  string `json:"redacted,omitempty"`
}

type bedrockReasoningContinuation struct {
	Reasoning []bedrockReasoningItem `json:"reasoning,omitempty"`
}

type bedrockThinkingPlan struct {
	fields  map[string]any
	budget  int
	enabled bool
	manual  bool
}

type bedrockToolPlan struct {
	catalog []types.Tool
	choice  *modelrepo.ToolChoice
}

func buildConverseInput(m converseModel, messages []modelrepo.Message, cfg *modelrepo.ChatConfig) (*bedrockruntime.ConverseInput, map[string]string, error) {
	if err := modelrepo.RefuseAudioInput("bedrock", m.name, messages); err != nil {
		return nil, nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, nil, fmt.Errorf("bedrock model %s: %w", m.name, err)
	}
	if cfg != nil && cfg.Shift != nil {
		return nil, nil, fmt.Errorf("bedrock model %s cannot shift its context window: the Converse API defines no overflow control", m.name)
	}
	if cfg != nil && cfg.Truncate != nil {
		return nil, nil, fmt.Errorf("bedrock model %s cannot truncate its context window: the Converse API defines no overflow control", m.name)
	}

	names := newToolNameTable()
	tools, err := planBedrockTools(m, cfg, names)
	if err != nil {
		return nil, nil, err
	}
	ceiling := bedrockOutputCeiling(m, cfg)
	plan, err := planBedrockThinking(m, cfg, ceiling)
	if err != nil {
		return nil, nil, err
	}
	if plan.manual && cfg.ToolChoice != nil &&
		(cfg.ToolChoice.Mode == modelrepo.ToolChoiceRequired || cfg.ToolChoice.Tool != "") {
		return nil, nil, fmt.Errorf("bedrock model %s cannot force tool use with a caller-set thinking budget: the vendor documents forced tool use as incompatible with thinking.type enabled", m.name)
	}

	in := &bedrockruntime.ConverseInput{ModelId: aws.String(m.name)}
	hasTools := len(tools.catalog) > 0

	var system []types.SystemContentBlock
	var msgs []types.Message

	appendBlocks := func(role types.ConversationRole, blocks []types.ContentBlock) {
		if len(blocks) == 0 {
			return
		}
		if n := len(msgs); n > 0 && msgs[n-1].Role == role {
			msgs[n-1].Content = append(msgs[n-1].Content, blocks...)
			return
		}
		msgs = append(msgs, types.Message{Role: role, Content: blocks})
	}

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			if msg.Content != "" {
				system = append(system, &types.SystemContentBlockMemberText{Value: msg.Content})
			}
		case "tool":
			if !hasTools {
				appendBlocks(types.ConversationRoleUser, []types.ContentBlock{
					&types.ContentBlockMemberText{Value: fmt.Sprintf("[tool result %s]\n%s", msg.ToolCallID, msg.Content)},
				})
				continue
			}
			appendBlocks(types.ConversationRoleUser, []types.ContentBlock{
				&types.ContentBlockMemberToolResult{Value: types.ToolResultBlock{
					ToolUseId: aws.String(msg.ToolCallID),
					Content:   []types.ToolResultContentBlock{&types.ToolResultContentBlockMemberText{Value: msg.Content}},
				}},
			})
		case "assistant", "model":
			replayed, err := replayReasoningBlocks(m, msg)
			if err != nil {
				return nil, nil, err
			}
			blocks := replayed
			if msg.Content != "" {
				blocks = append(blocks, &types.ContentBlockMemberText{Value: msg.Content})
			}
			for _, tc := range msg.ToolCalls {
				if !hasTools {
					blocks = append(blocks, &types.ContentBlockMemberText{
						Value: fmt.Sprintf("[tool call %s: %s(%s)]", tc.ID, tc.Function.Name, tc.Function.Arguments),
					})
					continue
				}
				wireName := names.wire(tc.Function.Name)
				blocks = append(blocks, &types.ContentBlockMemberToolUse{Value: types.ToolUseBlock{
					ToolUseId: aws.String(tc.ID),
					Name:      aws.String(wireName),
					Input:     jsonStringToDocument(tc.Function.Arguments),
				}})
			}
			appendBlocks(types.ConversationRoleAssistant, blocks)
		default:
			var blocks []types.ContentBlock
			if msg.Content != "" {
				blocks = append(blocks, &types.ContentBlockMemberText{Value: msg.Content})
			}
			for _, img := range msg.Images {
				format, ok := imageFormatFromMime(img.MimeType)
				if !ok {
					continue
				}
				blocks = append(blocks, &types.ContentBlockMemberImage{Value: types.ImageBlock{
					Format: format,
					Source: &types.ImageSourceMemberBytes{Value: img.Data},
				}})
			}
			appendBlocks(types.ConversationRoleUser, blocks)
		}
	}

	in.Messages = msgs
	if len(system) > 0 {
		in.System = system
	}

	if cfg != nil {
		ic, err := bedrockInferenceConfig(m, cfg, plan, ceiling)
		if err != nil {
			return nil, nil, err
		}
		if ic != nil {
			in.InferenceConfig = ic
		}

		if len(tools.catalog) > 0 {
			in.ToolConfig = &types.ToolConfiguration{Tools: tools.catalog}
			if err := applyBedrockToolChoice(m, tools, names, in.ToolConfig); err != nil {
				return nil, nil, err
			}
		}
		if err := applyBedrockResponseFormat(m, cfg, in); err != nil {
			return nil, nil, err
		}

		if plan.fields != nil {
			in.AdditionalModelRequestFields = document.NewLazyDocument(plan.fields)
		}
		if len(cfg.StopSequences) > 0 {
			in.AdditionalModelResponseFieldPaths = []string{"/stop_sequence"}
		}

		if err := applyBedrockCachePoints(m, in, cfg.CacheHints); err != nil {
			return nil, nil, err
		}
	}

	return in, names.reverse(), nil
}

func planBedrockTools(m converseModel, cfg *modelrepo.ChatConfig, names *toolNameTable) (bedrockToolPlan, error) {
	if cfg == nil {
		return bedrockToolPlan{}, nil
	}
	if cfg.ToolChoice != nil && cfg.ToolChoice.Mode == modelrepo.ToolChoiceNone {
		return bedrockToolPlan{}, nil
	}
	plan := bedrockToolPlan{choice: cfg.ToolChoice}
	for _, t := range cfg.Tools {
		if strings.ToLower(t.Type) != "function" || t.Function == nil {
			continue
		}
		spec := types.ToolSpecification{
			Name:        aws.String(names.wire(t.Function.Name)),
			InputSchema: &types.ToolInputSchemaMemberJson{Value: document.NewLazyDocument(t.Function.Parameters)},
		}
		if desc := strings.TrimSpace(t.Function.Description); desc != "" {
			spec.Description = aws.String(desc)
		}
		plan.catalog = append(plan.catalog, &types.ToolMemberToolSpec{Value: spec})
	}
	if plan.choice != nil && plan.choice.Tool != "" {
		if _, declared := names.declared(plan.choice.Tool); !declared {
			return bedrockToolPlan{}, fmt.Errorf("bedrock model %s cannot force the tool %q: the request declares no such tool", m.name, plan.choice.Tool)
		}
	}
	if cfg.ParallelToolCalls != nil && !*cfg.ParallelToolCalls && len(plan.catalog) > 0 {
		return bedrockToolPlan{}, fmt.Errorf("bedrock model %s cannot disable parallel tool calls: the Converse API defines no such control", m.name)
	}
	return plan, nil
}

func bedrockOutputCeiling(m converseModel, cfg *modelrepo.ChatConfig) int {
	if cfg != nil && cfg.MaxTokens != nil && *cfg.MaxTokens > 0 {
		effective, _ := modelrepo.ClampMaxOutputTokens(*cfg.MaxTokens, m.maxOutputTokens)
		return effective
	}
	return m.maxOutputTokens
}

func bedrockInferenceConfig(m converseModel, cfg *modelrepo.ChatConfig, plan bedrockThinkingPlan, ceiling int) (*types.InferenceConfiguration, error) {
	ic := &types.InferenceConfiguration{}
	set := false
	if ceiling > 0 && (plan.manual || (cfg.MaxTokens != nil && *cfg.MaxTokens > 0)) {
		const maxInt32 = int64(1<<31 - 1)
		effective := int64(ceiling)
		if effective > maxInt32 {
			effective = maxInt32
		}
		v := int32(effective)
		ic.MaxTokens = &v
		set = true
	}
	if err := applyBedrockSampling(m, cfg, ic, plan); err != nil {
		return nil, err
	}
	if ic.Temperature != nil || ic.TopP != nil {
		set = true
	}
	if len(cfg.StopSequences) > 0 {
		ic.StopSequences = append([]string(nil), cfg.StopSequences...)
		set = true
	}
	if !set {
		return nil, nil
	}
	return ic, nil
}

const bedrockSamplingTopPFloor = 0.95

func applyBedrockSampling(m converseModel, cfg *modelrepo.ChatConfig, ic *types.InferenceConfiguration, plan bedrockThinkingPlan) error {
	if bedrockStripsSamplingParams(m.thinking) {
		return nil
	}
	if !plan.enabled {
		if cfg.Temperature != nil {
			if *cfg.Temperature < 0 || *cfg.Temperature > 1 {
				return fmt.Errorf("bedrock model %s accepts a temperature between 0 and 1, got %v", m.name, *cfg.Temperature)
			}
			v := float32(*cfg.Temperature)
			ic.Temperature = &v
		}
		if cfg.TopP != nil {
			v := float32(*cfg.TopP)
			ic.TopP = &v
		}
		return nil
	}
	if cfg.TopP != nil && *cfg.TopP >= bedrockSamplingTopPFloor && *cfg.TopP <= 1 {
		v := float32(*cfg.TopP)
		ic.TopP = &v
	}
	return nil
}

func bedrockStripsSamplingParams(declared *modelrepo.ThinkingTypes) bool {
	return declared != nil && declared.Adaptive && !declared.Enabled
}

func applyBedrockToolChoice(m converseModel, plan bedrockToolPlan, names *toolNameTable, config *types.ToolConfiguration) error {
	if plan.choice == nil {
		return nil
	}
	switch plan.choice.Mode {
	case modelrepo.ToolChoiceAuto:
		config.ToolChoice = &types.ToolChoiceMemberAuto{Value: types.AutoToolChoice{}}
	case modelrepo.ToolChoiceRequired:
		if plan.choice.Tool == "" {
			config.ToolChoice = &types.ToolChoiceMemberAny{Value: types.AnyToolChoice{}}
			return nil
		}
		wire, _ := names.declared(plan.choice.Tool)
		config.ToolChoice = &types.ToolChoiceMemberTool{Value: types.SpecificToolChoice{Name: aws.String(wire)}}
	default:
		return fmt.Errorf("bedrock model %s cannot express tool choice %q", m.name, plan.choice.Mode)
	}
	return nil
}

func applyBedrockResponseFormat(m converseModel, cfg *modelrepo.ChatConfig, in *bedrockruntime.ConverseInput) error {
	if cfg.Format == nil {
		return nil
	}
	switch cfg.Format.Mode {
	case modelrepo.ResponseFormatText:
		return nil
	case modelrepo.ResponseFormatJSONSchema:
		raw, err := json.Marshal(cfg.Format.Schema)
		if err != nil {
			return fmt.Errorf("bedrock model %s: encode response schema: %w", m.name, err)
		}
		def := types.JsonSchemaDefinition{Schema: aws.String(string(raw))}
		if name := strings.TrimSpace(cfg.Format.Name); name != "" {
			def.Name = aws.String(name)
		}
		in.OutputConfig = &types.OutputConfig{TextFormat: &types.OutputFormat{
			Type:      types.OutputFormatTypeJsonSchema,
			Structure: &types.OutputFormatStructureMemberJsonSchema{Value: def},
		}}
		return nil
	default:
		return fmt.Errorf("bedrock model %s cannot be constrained to %q: the Converse API defines only the json_schema output format", m.name, cfg.Format.Mode)
	}
}

func planBedrockThinking(m converseModel, cfg *modelrepo.ChatConfig, ceiling int) (bedrockThinkingPlan, error) {
	alwaysThinking := m.thinking != nil && m.thinking.Adaptive && !m.thinking.Disabled
	if cfg == nil || cfg.Think == nil {
		return bedrockThinkingPlan{enabled: alwaysThinking}, nil
	}
	level, ok, err := reasoning.NormalizeOptional(*cfg.Think)
	if err != nil {
		return bedrockThinkingPlan{}, err
	}
	if !ok || level == reasoning.Auto {
		return bedrockThinkingPlan{enabled: alwaysThinking}, nil
	}
	if m.thinking == nil {
		return bedrockThinkingPlan{}, fmt.Errorf("bedrock model %s has no declared thinking controls, so think=%s cannot be expressed; %s", m.name, level, reasoningEffortRemedy(false))
	}
	if level == reasoning.Off {
		if !m.thinking.Disabled {
			return bedrockThinkingPlan{}, fmt.Errorf("bedrock model %s cannot turn thinking off: its declared thinking types do not include a disabled control; %s", m.name, reasoningEffortRemedy(true))
		}
		return bedrockThinkingPlan{fields: map[string]any{"thinking": map[string]any{"type": "disabled"}}}, nil
	}
	if m.thinking.Adaptive {
		effort, ok := bedrockEffort(level)
		if !ok {
			return bedrockThinkingPlan{}, fmt.Errorf("bedrock model %s cannot express think=%s as an effort level; %s", m.name, level, reasoningEffortRemedy(true))
		}
		if !m.thinking.SupportsEffort(effort) {
			return bedrockThinkingPlan{}, fmt.Errorf("bedrock model %s does not declare the %s effort level (declared: %s); %s", m.name, effort, strings.Join(m.thinking.EffortLevels, ", "), reasoningEffortRemedy(true))
		}
		return bedrockThinkingPlan{
			fields: map[string]any{
				"thinking":      map[string]any{"type": "adaptive"},
				"output_config": map[string]any{"effort": effort},
			},
			enabled: true,
		}, nil
	}
	if m.thinking.Enabled {
		budget, err := bedrockThinkingBudgetFor(m, level, ceiling)
		if err != nil {
			return bedrockThinkingPlan{}, err
		}
		return bedrockThinkingPlan{
			fields:  map[string]any{"thinking": map[string]any{"type": "enabled", "budget_tokens": budget}},
			budget:  budget,
			enabled: true,
			manual:  true,
		}, nil
	}
	return bedrockThinkingPlan{}, fmt.Errorf("bedrock model %s declares no thinking control a request can turn on; %s", m.name, reasoningEffortRemedy(true))
}

func reasoningEffortRemedy(declared bool) string {
	lead, level := "set the reasoning effort to auto, which sends no thinking control", "auto"
	if declared {
		lead, level = "choose a reasoning effort the model declares, or auto, which sends no thinking control", "<level>"
	}
	return fmt.Sprintf("%s: `/default %s %s` in the chat or `contenox-cli config set %s %s`, then start a new Contenox invocation", lead, "inference.reasoning.effort", level, "inference.reasoning.effort", level)
}

func thinkDeclarationRefusal() string {
	return "a Bedrock request carries a thinking control only from thinking types the model declares, and a reasoning declaration declares none; " + reasoningEffortRemedy(false)
}

func bedrockThinkingBudgetFor(m converseModel, level string, ceiling int) (int, error) {
	if ceiling <= 0 {
		return 0, fmt.Errorf("bedrock model %s needs an output ceiling to place a %s thinking budget below: its connection facts declare no maximum output tokens, so a think=%s request cannot state max tokens", m.name, level, level)
	}
	if ceiling <= bedrockMinimumThinkingBudget {
		return 0, fmt.Errorf("bedrock model %s cannot carry a thinking budget under a %d-token output ceiling: the vendor's smallest budget is %d tokens", m.name, ceiling, bedrockMinimumThinkingBudget)
	}
	budget := bedrockThinkingBudget(level)
	if budget >= ceiling {
		budget = ceiling - 1
	}
	return budget, nil
}

func bedrockEffort(level string) (string, bool) {
	switch level {
	case reasoning.Minimal, reasoning.Low:
		return reasoning.Low, true
	case reasoning.Medium:
		return reasoning.Medium, true
	case reasoning.High:
		return reasoning.High, true
	case reasoning.XHigh:
		return reasoning.XHigh, true
	case reasoning.Max:
		return reasoning.Max, true
	default:
		return "", false
	}
}

func bedrockThinkingBudget(level string) int {
	switch level {
	case reasoning.Medium:
		return 2048
	case reasoning.High:
		return 4096
	case reasoning.XHigh:
		return 8192
	case reasoning.Max:
		return 16384
	default:
		return bedrockMinimumThinkingBudget
	}
}

func replayReasoningBlocks(m converseModel, msg modelrepo.Message) ([]types.ContentBlock, error) {
	cont := msg.Continuation
	if cont == nil || cont.Provider != reasoningContinuationProvider {
		return nil, nil
	}
	if bedrockBaseModelID(cont.Model) != bedrockBaseModelID(m.name) {
		return nil, fmt.Errorf("bedrock: reasoning for model %s cannot be replayed to %s", cont.Model, m.name)
	}
	var decoded bedrockReasoningContinuation
	if err := json.Unmarshal(cont.Items, &decoded); err != nil {
		return nil, fmt.Errorf("bedrock: decode reasoning continuation: %w", err)
	}
	blocks := make([]types.ContentBlock, 0, len(decoded.Reasoning))
	for _, item := range decoded.Reasoning {
		if item.Redacted != "" {
			raw, err := base64.StdEncoding.DecodeString(item.Redacted)
			if err != nil {
				return nil, fmt.Errorf("bedrock: decode redacted reasoning: %w", err)
			}
			blocks = append(blocks, &types.ContentBlockMemberReasoningContent{
				Value: &types.ReasoningContentBlockMemberRedactedContent{Value: raw},
			})
			continue
		}
		if item.Text == "" && item.Signature == "" {
			continue
		}
		text := types.ReasoningTextBlock{}
		if item.Text != "" {
			text.Text = aws.String(item.Text)
		}
		if item.Signature != "" {
			text.Signature = aws.String(item.Signature)
		}
		blocks = append(blocks, &types.ContentBlockMemberReasoningContent{
			Value: &types.ReasoningContentBlockMemberReasoningText{Value: text},
		})
	}
	return blocks, nil
}

func reasoningContinuation(m converseModel, items []bedrockReasoningItem) *modelrepo.Continuation {
	if len(items) == 0 {
		return nil
	}
	raw, err := json.Marshal(bedrockReasoningContinuation{Reasoning: items})
	if err != nil {
		return nil
	}
	return &modelrepo.Continuation{Provider: reasoningContinuationProvider, Model: m.name, Items: raw}
}

func applyBedrockCachePoints(m converseModel, in *bedrockruntime.ConverseInput, hints *modelrepo.CacheHints) error {
	if in == nil || hints == nil || m.promptCache == nil {
		return nil
	}
	ttl, err := bedrockCacheTTL(m, hints.TTL)
	if err != nil {
		return err
	}
	cp := types.CachePointBlock{Type: types.CachePointTypeDefault, Ttl: ttl}
	if hints.StableTools && m.promptCache.SupportsField(modelrepo.PromptCacheFieldTools) && in.ToolConfig != nil && len(in.ToolConfig.Tools) > 0 {
		in.ToolConfig.Tools = append(in.ToolConfig.Tools, &types.ToolMemberCachePoint{Value: cp})
	}
	if hints.StableSystem && m.promptCache.SupportsField(modelrepo.PromptCacheFieldSystem) && len(in.System) > 0 {
		in.System = append(in.System, &types.SystemContentBlockMemberCachePoint{Value: cp})
	}
	if hints.StableHistoryLen > 0 && m.promptCache.SupportsField(modelrepo.PromptCacheFieldMessages) {
		if hints.StableHistoryLen > len(in.Messages) {
			return fmt.Errorf("bedrock model %s: the declared stable history of %d messages exceeds the %d messages sent", m.name, hints.StableHistoryLen, len(in.Messages))
		}
		idx := hints.StableHistoryLen - 1
		in.Messages[idx].Content = append(in.Messages[idx].Content, &types.ContentBlockMemberCachePoint{Value: cp})
	}
	return nil
}

func bedrockCacheTTL(m converseModel, lifetime time.Duration) (types.CacheTTL, error) {
	if lifetime < time.Hour {
		return "", nil
	}
	if !m.promptCache.SupportsTTL(modelrepo.PromptCacheTTLOneHour) {
		return "", fmt.Errorf("bedrock model %s does not declare the %s prompt-cache lifetime", m.name, modelrepo.PromptCacheTTLOneHour)
	}
	return types.CacheTTLOneHour, nil
}

func decodeConverse(m converseModel, out *bedrockruntime.ConverseOutput, toOriginal map[string]string) (modelrepo.ChatResult, error) {
	if out == nil || out.Output == nil {
		return modelrepo.ChatResult{}, fmt.Errorf("bedrock: empty converse output")
	}
	msgOut, ok := out.Output.(*types.ConverseOutputMemberMessage)
	if !ok {
		return modelrepo.ChatResult{}, fmt.Errorf("bedrock: unexpected converse output type %T", out.Output)
	}

	stopReason := string(out.StopReason)
	var text, thinking strings.Builder
	var toolCalls []modelrepo.ToolCall
	var reasoning []bedrockReasoningItem
	for _, cb := range msgOut.Value.Content {
		switch v := cb.(type) {
		case *types.ContentBlockMemberText:
			text.WriteString(v.Value)
		case *types.ContentBlockMemberReasoningContent:
			switch r := v.Value.(type) {
			case *types.ReasoningContentBlockMemberReasoningText:
				thinking.WriteString(aws.ToString(r.Value.Text))
				reasoning = append(reasoning, bedrockReasoningItem{
					Text:      aws.ToString(r.Value.Text),
					Signature: aws.ToString(r.Value.Signature),
				})
			case *types.ReasoningContentBlockMemberRedactedContent:
				reasoning = append(reasoning, bedrockReasoningItem{Redacted: base64.StdEncoding.EncodeToString(r.Value)})
			}
		case *types.ContentBlockMemberToolUse:
			name := aws.ToString(v.Value.Name)
			if orig, ok := toOriginal[name]; ok {
				name = orig
			}
			toolCalls = append(toolCalls, newToolCall(
				aws.ToString(v.Value.ToolUseId),
				name,
				documentToJSONString(v.Value.Input),
			))
		}
	}

	switch types.StopReason(stopReason) {
	case types.StopReasonGuardrailIntervened, types.StopReasonContentFiltered:
		return modelrepo.ChatResult{}, &modelrepo.RefusalError{Provider: "bedrock", Code: stopReason, Text: strings.TrimSpace(text.String())}
	case types.StopReasonModelContextWindowExceeded:
		return modelrepo.ChatResult{}, fmt.Errorf("%w: bedrock model %s reported %s", modelrepo.ErrContextLengthExceeded, m.name, stopReason)
	case types.StopReasonMalformedModelOutput, types.StopReasonMalformedToolUse:
		return modelrepo.ChatResult{}, fmt.Errorf("bedrock model %s returned %s", m.name, stopReason)
	}

	if text.Len() == 0 && len(toolCalls) == 0 && thinking.Len() == 0 {
		if stopReason == "" {
			return modelrepo.ChatResult{}, errors.New("bedrock: converse output carried no stop reason")
		}
	}
	return modelrepo.ChatResult{
		Message: modelrepo.Message{
			Role:         "assistant",
			Content:      text.String(),
			Thinking:     thinking.String(),
			Continuation: reasoningContinuation(m, reasoning),
		},
		ToolCalls:    toolCalls,
		Usage:        usageFromConverse(out.Usage),
		FinishReason: stopReason,
	}, nil
}

func usageFromConverse(u *types.TokenUsage) *modelrepo.TokenUsage {
	if u == nil {
		return nil
	}
	read := int(aws.ToInt32(u.CacheReadInputTokens))
	write := int(aws.ToInt32(u.CacheWriteInputTokens))
	prompt := int(aws.ToInt32(u.InputTokens)) + read + write
	completion := int(aws.ToInt32(u.OutputTokens))
	return &modelrepo.TokenUsage{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      prompt + completion,
		CacheReadTokens:  read,
		CacheWriteTokens: write,
	}
}

func newToolCall(id, name, args string) modelrepo.ToolCall {
	tc := modelrepo.ToolCall{ID: id, Type: "function"}
	tc.Function.Name = name
	tc.Function.Arguments = args
	return tc
}

func imageFormatFromMime(mime string) (types.ImageFormat, bool) {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/png":
		return types.ImageFormatPng, true
	case "image/jpeg", "image/jpg":
		return types.ImageFormatJpeg, true
	case "image/gif":
		return types.ImageFormatGif, true
	case "image/webp":
		return types.ImageFormatWebp, true
	default:
		return "", false
	}
}

type toolNameTable struct {
	byWire     map[string]string
	byOriginal map[string]string
	taken      map[string]bool
}

func newToolNameTable() *toolNameTable {
	return &toolNameTable{
		byWire:     map[string]string{},
		byOriginal: map[string]string{},
		taken:      map[string]bool{},
	}
}

func (t *toolNameTable) wire(original string) string {
	if name, ok := t.byOriginal[original]; ok {
		return name
	}
	base := sanitizeToolName(original)
	name := base
	for i := 2; t.taken[name]; i++ {
		suffix := fmt.Sprintf("_%d", i)
		trimmed := base
		if len(trimmed)+len(suffix) > 64 {
			trimmed = trimmed[:64-len(suffix)]
		}
		name = trimmed + suffix
	}
	t.taken[name] = true
	t.byOriginal[original] = name
	t.byWire[name] = original
	return name
}

func (t *toolNameTable) declared(original string) (string, bool) {
	name, ok := t.byOriginal[original]
	return name, ok
}

func (t *toolNameTable) reverse() map[string]string {
	out := make(map[string]string, len(t.byWire))
	for wire, original := range t.byWire {
		out[wire] = original
	}
	return out
}

func sanitizeToolName(in string) string {
	if in == "" {
		return "tool"
	}
	var b strings.Builder
	for _, r := range in {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	s := b.String()
	s = strings.Trim(s, "_-")
	if len(s) > 64 {
		s = s[:64]
		s = strings.TrimRight(s, "_-")
	}
	if s == "" {
		return "tool"
	}
	return s
}
