package bedrock

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/contenox/runtime/runtime/modelrepo"
)

type bedrockStreamClient struct{ bedrockClient }

func (c *bedrockStreamClient) Stream(ctx context.Context, messages []modelrepo.Message, args ...modelrepo.ChatArgument) (<-chan *modelrepo.StreamParcel, error) {
	in, toOriginal, err := buildConverseInput(c.model, messages, chatConfigFromArgs(args))
	if err != nil {
		return nil, err
	}
	streamIn := converseStreamInput(in)

	reportErr, reportChange, end := c.tracker.Start(ctx, "stream", "bedrock", "model", c.model.name)
	out, err := c.api.ConverseStream(ctx, streamIn)
	if err != nil {
		err = classifyBedrockError(fmt.Errorf("bedrock converse-stream (model=%s): %w", c.model.name, err))
		reportErr(err)
		end()
		return nil, err
	}

	parcels := make(chan *modelrepo.StreamParcel)
	go func() {
		defer close(parcels)
		defer end()
		stream := out.GetStream()
		defer stream.Close()

		outcome := relayConverseEvents(ctx, c.model, stream.Events(), toOriginal, parcels)
		if err := stream.Err(); err != nil {
			err = classifyBedrockError(fmt.Errorf("bedrock stream (model=%s): %w", c.model.name, err))
			reportErr(err)
			select {
			case parcels <- &modelrepo.StreamParcel{Error: err}:
			case <-ctx.Done():
			}
			return
		}
		if outcome.refusal != nil {
			reportErr(outcome.refusal)
			select {
			case parcels <- &modelrepo.StreamParcel{Error: outcome.refusal}:
			case <-ctx.Done():
			}
			return
		}
		reportChange("stream_completed", map[string]any{"chunk_count": outcome.chunks})
	}()
	return parcels, nil
}

func converseStreamInput(in *bedrockruntime.ConverseInput) *bedrockruntime.ConverseStreamInput {
	if in == nil {
		return nil
	}
	return &bedrockruntime.ConverseStreamInput{
		ModelId:                           in.ModelId,
		Messages:                          in.Messages,
		System:                            in.System,
		InferenceConfig:                   in.InferenceConfig,
		ToolConfig:                        in.ToolConfig,
		AdditionalModelRequestFields:      in.AdditionalModelRequestFields,
		AdditionalModelResponseFieldPaths: in.AdditionalModelResponseFieldPaths,
		OutputConfig:                      in.OutputConfig,
		PerformanceConfig:                 in.PerformanceConfig,
		PromptVariables:                   in.PromptVariables,
		RequestMetadata:                   in.RequestMetadata,
		ServiceTier:                       in.ServiceTier,
	}
}

type streamOutcome struct {
	refusal *modelrepo.RefusalError
	chunks  int
}

func relayConverseEvents(ctx context.Context, m converseModel, events <-chan types.ConverseStreamOutput, toOriginal map[string]string, parcels chan<- *modelrepo.StreamParcel) streamOutcome {
	outcome := streamOutcome{}
	send := func(p *modelrepo.StreamParcel) bool {
		select {
		case parcels <- p:
			return true
		case <-ctx.Done():
			return false
		}
	}

	var (
		stopReason   string
		stopSequence string
		usage        *modelrepo.TokenUsage
		content      strings.Builder
		sawTermEvent bool
	)
	reasoning := map[int]*bedrockReasoningItem{}

	for ev := range events {
		switch v := ev.(type) {
		case *types.ConverseStreamOutputMemberContentBlockStart:
			start, ok := v.Value.Start.(*types.ContentBlockStartMemberToolUse)
			if !ok {
				continue
			}
			name := aws.ToString(start.Value.Name)
			if orig, ok := toOriginal[name]; ok && orig != "" {
				name = orig
			}
			outcome.chunks++
			if !send(&modelrepo.StreamParcel{ToolCall: &modelrepo.ToolCallDelta{
				Index: int(aws.ToInt32(v.Value.ContentBlockIndex)),
				ID:    aws.ToString(start.Value.ToolUseId),
				Type:  "function",
				Name:  name,
			}}) {
				return outcome
			}

		case *types.ConverseStreamOutputMemberContentBlockDelta:
			index := int(aws.ToInt32(v.Value.ContentBlockIndex))
			switch d := v.Value.Delta.(type) {
			case *types.ContentBlockDeltaMemberText:
				if d.Value == "" {
					continue
				}
				content.WriteString(d.Value)
				outcome.chunks++
				if !send(&modelrepo.StreamParcel{Data: d.Value}) {
					return outcome
				}
			case *types.ContentBlockDeltaMemberReasoningContent:
				switch r := d.Value.(type) {
				case *types.ReasoningContentBlockDeltaMemberText:
					if r.Value == "" {
						continue
					}
					item := reasoningItemAt(reasoning, index)
					item.Text += r.Value
					outcome.chunks++
					if !send(&modelrepo.StreamParcel{Thinking: r.Value}) {
						return outcome
					}
				case *types.ReasoningContentBlockDeltaMemberSignature:
					reasoningItemAt(reasoning, index).Signature += r.Value
				case *types.ReasoningContentBlockDeltaMemberRedactedContent:
					reasoningItemAt(reasoning, index).Redacted = base64.StdEncoding.EncodeToString(r.Value)
				}
			case *types.ContentBlockDeltaMemberToolUse:
				fragment := aws.ToString(d.Value.Input)
				if fragment == "" {
					continue
				}
				outcome.chunks++
				if !send(&modelrepo.StreamParcel{ToolCall: &modelrepo.ToolCallDelta{
					Index:        index,
					ArgsFragment: fragment,
				}}) {
					return outcome
				}
			}

		case *types.ConverseStreamOutputMemberMessageStop:
			stopReason = string(v.Value.StopReason)
			stopSequence = documentString(v.Value.AdditionalModelResponseFields, "stop_sequence")
			sawTermEvent = true

		case *types.ConverseStreamOutputMemberMetadata:
			if u := usageFromConverse(v.Value.Usage); u != nil {
				usage = u
			}
		}
	}

	terminal := &modelrepo.StreamTerminal{
		FinishReason: stopReason,
		Usage:        usage,
		Terminated:   sawTermEvent,
		Continuation: reasoningContinuation(m, orderedReasoning(reasoning)),
	}
	if stopSequence != "" && types.StopReason(stopReason) == types.StopReasonStopSequence {
		terminal.StopSequence = stopSequence
	}
	if reason := types.StopReason(stopReason); reason == types.StopReasonGuardrailIntervened || reason == types.StopReasonContentFiltered {
		text := strings.TrimSpace(content.String())
		terminal.Refusal = text
		outcome.refusal = &modelrepo.RefusalError{Provider: "bedrock", Code: stopReason, Text: text}
	}
	send(&modelrepo.StreamParcel{Terminal: terminal})
	return outcome
}

func reasoningItemAt(items map[int]*bedrockReasoningItem, index int) *bedrockReasoningItem {
	item := items[index]
	if item == nil {
		item = &bedrockReasoningItem{}
		items[index] = item
	}
	return item
}

func orderedReasoning(items map[int]*bedrockReasoningItem) []bedrockReasoningItem {
	indexes := make([]int, 0, len(items))
	for index := range items {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	out := make([]bedrockReasoningItem, 0, len(indexes))
	for _, index := range indexes {
		out = append(out, *items[index])
	}
	return out
}

var _ modelrepo.LLMStreamClient = (*bedrockStreamClient)(nil)
