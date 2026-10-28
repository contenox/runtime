package oracletools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/getkin/kin-openapi/openapi3"
)

const ToolsProviderName = "oracle"

type AskKind string

const (
	AskKindPermission AskKind = "permission"

	AskKindAttention AskKind = "attention"
)

const (
	verdictWait    = "wait"
	verdictAnswer  = "answer"
	verdictApprove = "approve"
	verdictDeny    = "deny"
)

const (
	verdictStateSettled = "settled"
	verdictStateOpen    = "open"
)

const (
	ToolNameSubmitVerdict = "submit_verdict"

	ToolNameVerdictState = "verdict_state"
)

type Outcome string

const (
	OutcomeNone Outcome = ""

	OutcomeWait Outcome = "wait"

	OutcomeAnswered Outcome = "answered"

	OutcomeApproved Outcome = "approved"

	OutcomeDenied Outcome = "denied"
)

type AskBinding struct {
	askID string
	kind  AskKind
	input string

	mu       sync.Mutex
	outcome  Outcome
	answer   string
	guidance string
}

func NewAskBinding(askID string, kind AskKind, input string) *AskBinding {
	return &AskBinding{askID: askID, kind: kind, input: input}
}

func (b *AskBinding) AskID() string { return b.askID }

func (b *AskBinding) Kind() AskKind { return b.kind }

func (b *AskBinding) Outcome() Outcome {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.outcome
}

func (b *AskBinding) Answer() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.answer
}

func (b *AskBinding) Guidance() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.guidance
}

func (b *AskBinding) settled() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.outcome != OutcomeNone
}

func (b *AskBinding) settle(o Outcome, answer, guidance string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.outcome = o
	b.answer = answer
	b.guidance = guidance
}

type bindingCtxKey struct{}

func WithBinding(ctx context.Context, b *AskBinding) context.Context {
	if b == nil {
		return ctx
	}
	return context.WithValue(ctx, bindingCtxKey{}, b)
}

func BindingFromContext(ctx context.Context) *AskBinding {
	b, _ := ctx.Value(bindingCtxKey{}).(*AskBinding)
	return b
}

type Resolver interface {
	Answer(ctx context.Context, askID, text string) error

	Decide(ctx context.Context, askID string, approve bool, guidance string) error
}

type RefusedError struct{ Reason string }

func (e *RefusedError) Error() string { return e.Reason }

type provider struct {
	resolver Resolver
}

func New(resolver Resolver) taskengine.ToolsRepo {
	if resolver == nil {
		panic("oracletools: resolver is required")
	}
	return &provider{resolver: resolver}
}

func (p *provider) Supports(context.Context) ([]string, error) {
	return []string{ToolsProviderName}, nil
}

func (p *provider) GetSchemasForSupportedTools(context.Context) (map[string]*openapi3.T, error) {
	schema := &openapi3.T{
		OpenAPI: "3.1.0",
		Info: &openapi3.Info{
			Title:       "Oracle Adjudication Tools",
			Description: "Submit the verdict for the one durable ask bound to this chain execution. A gated tool call takes approve, deny, or wait; a question takes answer or wait. wait always leaves it to a human. Every verdict is still bounded by the mission envelope.",
			Version:     "1.0.0",
		},
		Paths: openapi3.NewPaths(),
		Components: &openapi3.Components{
			Schemas: map[string]*openapi3.SchemaRef{
				"SubmitVerdictRequest": {
					Value: &openapi3.Schema{
						Type:       &openapi3.Types{openapi3.TypeObject},
						Properties: verdictRequestSchemaProperties(),
						Required:   verdictRequiredProperties(),
					},
				},
				"SubmitVerdictResponse": {
					Value: &openapi3.Schema{
						Type: &openapi3.Types{openapi3.TypeObject},
						Properties: map[string]*openapi3.SchemaRef{
							"accepted": {Value: &openapi3.Schema{
								Type:        &openapi3.Types{openapi3.TypeBoolean},
								Description: "True when this call settled the contract. False for every corrective outcome (malformed shape, wrong askId, a verdict this ask kind does not take, a missing answer, a refused verdict, a contract already settled) — read message and submit a corrected call.",
							}},
							"outcome": {Value: &openapi3.Schema{
								Type:        &openapi3.Types{openapi3.TypeString},
								Enum:        []any{string(OutcomeNone), string(OutcomeWait), string(OutcomeAnswered), string(OutcomeApproved), string(OutcomeDenied)},
								Description: `The contract's state after this call: "wait", "answered", "approved", "denied", or "" (still open — nothing was recorded).`,
							}},
							"message": {Value: &openapi3.Schema{
								Type:        &openapi3.Types{openapi3.TypeString},
								Description: "One line stating what happened: the acknowledgment on a settled contract, or exactly what was wrong on a corrective outcome.",
							}},
						},
						Required: []string{"accepted", "outcome", "message"},
					},
				},
				"VerdictStateResponse": {
					Value: &openapi3.Schema{
						Type: &openapi3.Types{openapi3.TypeObject},
						Properties: map[string]*openapi3.SchemaRef{
							"state": {Value: &openapi3.Schema{
								Type:        &openapi3.Types{openapi3.TypeString},
								Enum:        []any{verdictStateSettled, verdictStateOpen},
								Description: `"settled" when a verdict was recorded (the chain ends), "open" when none was.`,
							}},
							"text": {Value: &openapi3.Schema{
								Type:        &openapi3.Types{openapi3.TypeString},
								Description: "The correction to feed back to the model while the contract is open, carrying the original input; empty when settled.",
							}},
						},
						Required: []string{"state", "text"},
					},
				},
			},
		},
	}
	return map[string]*openapi3.T{ToolsProviderName: schema}, nil
}

type verdictProperty struct {
	name        string
	description string
	enum        []string
	required    bool
}

func verdictProperties() []verdictProperty {
	return []verdictProperty{
		{
			name: "verdict",
			description: `On a PERMISSION ask (a gated tool call): "approve" lets the call run, "deny" refuses it, "wait" leaves it to a human. ` +
				`On an ATTENTION ask (a question): "answer" replies in the answer property, "wait" leaves it to a human. ` +
				`Approve only what the mission's own intent already implies. When in doubt, wait.`,
			enum:     []string{verdictApprove, verdictDeny, verdictAnswer, verdictWait},
			required: true,
		},
		{
			name:        "answer",
			description: "Required when verdict is answer: one short plain sentence, on one line, that the asking unit can act on. Omitted otherwise.",
		},
		{
			name:        "guidance",
			description: "Optional on deny: one short plain sentence naming what the unit should do INSTEAD, so a refusal redirects it rather than stalling it. Ignored for any other verdict.",
		},
		{
			name:        "askId",
			description: "The askId field of the INPUT event, echoed exactly. A value that does not match the bound ask is refused.",
			required:    true,
		},
	}
}

func verdictRequestSchemaProperties() map[string]*openapi3.SchemaRef {
	out := make(map[string]*openapi3.SchemaRef, 4)
	for _, p := range verdictProperties() {
		s := &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeString}, Description: p.description}
		for _, v := range p.enum {
			s.Enum = append(s.Enum, v)
		}
		out[p.name] = &openapi3.SchemaRef{Value: s}
	}
	return out
}

func verdictRequiredProperties() []string {
	var out []string
	for _, p := range verdictProperties() {
		if p.required {
			out = append(out, p.name)
		}
	}
	return out
}

func verdictToolParameters() map[string]any {
	props := make(map[string]any, 4)
	for _, p := range verdictProperties() {
		prop := map[string]any{"type": "string", "description": p.description}
		if len(p.enum) > 0 {
			prop["enum"] = append([]string(nil), p.enum...)
		}
		props[p.name] = prop
	}
	return map[string]any{
		"type":       "object",
		"properties": props,
		"required":   verdictRequiredProperties(),
	}
}

func (p *provider) GetToolsForToolsByName(ctx context.Context, name string) ([]taskengine.Tool, error) {
	if name != ToolsProviderName {
		return nil, fmt.Errorf("unknown tools: %s", name)
	}
	if BindingFromContext(ctx) == nil {
		return []taskengine.Tool{}, nil
	}
	return []taskengine.Tool{submitVerdictToolSchema()}, nil
}

func (p *provider) Exec(ctx context.Context, _ time.Time, input any, _ bool, call *taskengine.ToolsCall) (any, taskengine.DataType, error) {
	if call == nil {
		return nil, taskengine.DataTypeAny, fmt.Errorf("oracletools: missing tools call")
	}
	binding := BindingFromContext(ctx)
	if binding == nil {
		return nil, taskengine.DataTypeAny, fmt.Errorf("oracletools: no ask is bound to this execution; %s exists only inside the oracle driver's chain run", call.ToolName)
	}
	switch call.ToolName {
	case ToolNameSubmitVerdict:
		return p.execSubmitVerdict(ctx, binding, input, call)
	case ToolNameVerdictState:
		return execVerdictState(binding), taskengine.DataTypeJSON, nil
	default:
		return nil, taskengine.DataTypeAny, fmt.Errorf("oracletools: unknown tool %q (want %s or %s)", call.ToolName, ToolNameSubmitVerdict, ToolNameVerdictState)
	}
}

func verdictResult(accepted bool, outcome Outcome, message string) map[string]any {
	return map[string]any{"accepted": accepted, "outcome": string(outcome), "message": message}
}

func corrective(format string, args ...any) (any, taskengine.DataType, error) {
	return verdictResult(false, OutcomeNone, fmt.Sprintf(format, args...)), taskengine.DataTypeJSON, nil
}

func schemaLineFor(kind AskKind) string {
	if kind == AskKindAttention {
		return `this ask is a QUESTION, so the schema is {"verdict":"answer"|"wait", "answer": string (required when verdict is "answer"), "askId": string}. Correct the call and submit the verdict via submit_verdict again.`
	}
	return `this ask is a GATED TOOL CALL, so the schema is {"verdict":"approve"|"deny"|"wait", "guidance": string (optional on "deny"), "askId": string}. Correct the call and submit the verdict via submit_verdict again.`
}

func (p *provider) execSubmitVerdict(ctx context.Context, b *AskBinding, input any, call *taskengine.ToolsCall) (any, taskengine.DataType, error) {
	if b.settled() {
		return corrective("verdict already recorded for ask %s: the contract is settled. Do not call submit_verdict again.", b.askID)
	}
	verdict := strings.ToLower(strings.TrimSpace(argString(input, call, "verdict")))
	answer := strings.TrimSpace(argString(input, call, "answer"))
	guidance := strings.TrimSpace(argString(input, call, "guidance"))
	askID := strings.TrimSpace(argString(input, call, "askId"))

	if askID != b.askID {
		return corrective("invalid askId %q: askId must be the askId field of the INPUT event, which is %q. Correct the call and submit the verdict via submit_verdict again.", askID, b.askID)
	}
	if verdict == verdictWait {
		b.settle(OutcomeWait, "", "")
		return verdictResult(true, OutcomeWait,
			fmt.Sprintf("verdict recorded for ask %s: WAIT — nothing was executed; the ask stays with a human. The contract is complete: do not call submit_verdict again.", b.askID)), taskengine.DataTypeJSON, nil
	}

	switch b.kind {
	case AskKindAttention:
		if verdict != verdictAnswer {
			return corrective("invalid verdict %q: %s", verdict, schemaLineFor(b.kind))
		}
		if answer == "" {
			return corrective(`invalid call: verdict "answer" requires a non-empty "answer" — the one short plain sentence the unit acts on. %s`, schemaLineFor(b.kind))
		}
		if err := p.resolver.Answer(ctx, b.askID, answer); err != nil {
			return p.deliveryFailure(b, err)
		}
		b.settle(OutcomeAnswered, answer, "")
		return verdictResult(true, OutcomeAnswered,
			fmt.Sprintf("verdict recorded for ask %s: ANSWER delivered; the asking unit continues with it. The contract is complete: do not call submit_verdict again.", b.askID)), taskengine.DataTypeJSON, nil

	default:
		if verdict != verdictApprove && verdict != verdictDeny {
			return corrective("invalid verdict %q: %s", verdict, schemaLineFor(b.kind))
		}
		approve := verdict == verdictApprove
		if approve {
			guidance = ""
		}
		if err := p.resolver.Decide(ctx, b.askID, approve, guidance); err != nil {
			return p.deliveryFailure(b, err)
		}
		if approve {
			b.settle(OutcomeApproved, "", "")
			return verdictResult(true, OutcomeApproved,
				fmt.Sprintf("verdict recorded for ask %s: APPROVED; the gated call runs and the unit continues. The contract is complete: do not call submit_verdict again.", b.askID)), taskengine.DataTypeJSON, nil
		}
		b.settle(OutcomeDenied, "", guidance)
		return verdictResult(true, OutcomeDenied,
			fmt.Sprintf("verdict recorded for ask %s: DENIED; the gated call is refused and the unit continues without it. The contract is complete: do not call submit_verdict again.", b.askID)), taskengine.DataTypeJSON, nil
	}
}

func (p *provider) deliveryFailure(b *AskBinding, err error) (any, taskengine.DataType, error) {
	var refused *RefusedError
	if errors.As(err, &refused) {
		return corrective("verdict refused per policy for ask %s.", b.askID)
	}
	return nil, taskengine.DataTypeAny, fmt.Errorf("oracletools: deliver verdict for ask %s: %w", b.askID, err)
}

func correctionText(b *AskBinding) string {
	return fmt.Sprintf("output rejected: submit the verdict via submit_verdict. Chat text is not a protocol action.\n\nINPUT:\n%s", b.input)
}

func execVerdictState(b *AskBinding) map[string]any {
	if b.settled() {
		return map[string]any{"state": verdictStateSettled, "text": ""}
	}
	return map[string]any{"state": verdictStateOpen, "text": correctionText(b)}
}

func submitVerdictToolSchema() taskengine.Tool {
	return taskengine.Tool{
		Type: "function",
		Function: taskengine.FunctionTool{
			Name:        ToolNameSubmitVerdict,
			Description: `Submit the verdict for the ask under review. The INPUT event's "kind" says which verdicts this ask takes: "permission" takes approve/deny/wait, "attention" takes answer/wait. Exactly one accepted call ends the review. Returns {accepted, outcome, message}: accepted false means nothing was recorded and message states what to correct.`,
			Parameters:  verdictToolParameters(),
		},
	}
}

func argString(input any, call *taskengine.ToolsCall, key string) string {
	if m, ok := input.(map[string]any); ok {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
			return fmt.Sprintf("%v", v)
		}
	}
	if call != nil && call.Args != nil {
		if v, ok := call.Args[key]; ok {
			return v
		}
	}
	return ""
}

var _ taskengine.ToolsRepo = (*provider)(nil)
