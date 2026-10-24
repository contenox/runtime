package modelrepo

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var ErrStreamTruncated = errors.New("stream ended before the provider's terminal marker")

type StreamAssembler struct {
	providerType string
	modelName    string

	content  strings.Builder
	thinking strings.Builder
	toolAcc  map[int]*streamToolAcc
	usage    TokenUsage
	sawUsage bool
	terminal *StreamTerminal
	err      error
}

type streamToolAcc struct {
	id           string
	typ          string
	name         string
	args         strings.Builder
	providerMeta map[string]string
}

type StreamResult struct {
	Continuation *Continuation
	Content      string
	Thinking     string
	ToolCalls    []ToolCall
	FinishReason string
	Refusal      string
	StopSequence string
	Usage        *TokenUsage
}

func NewStreamAssembler(providerType, modelName string) *StreamAssembler {
	return &StreamAssembler{
		providerType: providerType,
		modelName:    modelName,
		toolAcc:      map[int]*streamToolAcc{},
	}
}

func (a *StreamAssembler) violation(format string, args ...any) error {
	return fmt.Errorf("stream contract violation (provider=%s, model=%s): "+format,
		append([]any{a.providerType, a.modelName}, args...)...)
}

func (a *StreamAssembler) Consume(p *StreamParcel) error {
	if a.err != nil {
		return a.err
	}
	if p == nil {
		a.err = a.violation("nil parcel")
		return a.err
	}
	if p.Error != nil {
		a.err = fmt.Errorf("stream failed (provider=%s, model=%s): %w", a.providerType, a.modelName, p.Error)
		return a.err
	}
	if a.terminal != nil {
		a.err = a.violation("parcel received after the terminal parcel")
		return a.err
	}

	switch {
	case p.Terminal != nil:
		a.terminal = p.Terminal
		if p.Terminal.Usage != nil {
			a.mergeUsage(*p.Terminal.Usage)
		}
	case p.ToolCall != nil:
		if err := a.consumeToolCall(p.ToolCall); err != nil {
			a.err = err
			return a.err
		}
	case p.Usage != nil:
		a.mergeUsage(*p.Usage)
	default:
		a.content.WriteString(p.Data)
		a.thinking.WriteString(p.Thinking)
	}
	return nil
}

func (a *StreamAssembler) consumeToolCall(d *ToolCallDelta) error {
	acc := a.toolAcc[d.Index]
	if acc == nil {
		acc = &streamToolAcc{}
		a.toolAcc[d.Index] = acc
	}
	set := func(slot *string, field, incoming string) error {
		if incoming == "" {
			return nil
		}
		if *slot != "" && *slot != incoming {
			return a.violation("tool call %d: conflicting %s (%q vs %q)", d.Index, field, *slot, incoming)
		}
		*slot = incoming
		return nil
	}
	if err := set(&acc.id, "id", d.ID); err != nil {
		return err
	}
	if err := set(&acc.typ, "type", d.Type); err != nil {
		return err
	}
	if err := set(&acc.name, "name", d.Name); err != nil {
		return err
	}
	acc.args.WriteString(d.ArgsFragment)
	for k, v := range d.ProviderMeta {
		if acc.providerMeta == nil {
			acc.providerMeta = map[string]string{}
		}
		acc.providerMeta[k] = v
	}
	return nil
}

func (a *StreamAssembler) mergeUsage(u TokenUsage) {
	a.sawUsage = true
	a.usage.Merge(&u)
}

func (a *StreamAssembler) Result() (StreamResult, error) {
	if a.err != nil {
		return StreamResult{}, a.err
	}
	if a.terminal == nil {
		return StreamResult{}, a.violation("stream ended without a terminal parcel")
	}
	if !a.terminal.Terminated && a.terminal.FinishReason == "" {
		return StreamResult{}, fmt.Errorf("%w: %w", ErrStreamTruncated,
			a.violation("stream ended without a finish reason or the provider's terminal marker"))
	}

	indexes := make([]int, 0, len(a.toolAcc))
	for idx := range a.toolAcc {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)

	var calls []ToolCall
	for _, idx := range indexes {
		acc := a.toolAcc[idx]
		if acc.name == "" {
			return StreamResult{}, a.violation("tool call %d has no name", idx)
		}
		typ := acc.typ
		if typ == "" {
			typ = "function"
		}
		args := acc.args.String()
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		tc := ToolCall{ID: acc.id, Type: typ, ProviderMeta: acc.providerMeta}
		tc.Function.Name = acc.name
		tc.Function.Arguments = args
		calls = append(calls, tc)
	}

	res := StreamResult{
		Continuation: a.terminal.Continuation,
		Content:      a.content.String(),
		Thinking:     a.thinking.String(),
		ToolCalls:    calls,
		FinishReason: a.terminal.FinishReason,
		Refusal:      a.terminal.Refusal,
		StopSequence: a.terminal.StopSequence,
	}
	if a.sawUsage {
		u := a.usage
		res.Usage = &u
	}
	return res, nil
}
