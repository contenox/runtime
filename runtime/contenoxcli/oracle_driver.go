package contenoxcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/agentservice"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/contenox/runtime/runtime/oracletools"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
)

type oracleResolver struct {
	hitl     hitlservice.Service
	missions missionservice.Service
	store    runtimetypes.Store
	out      io.Writer
	approves bool
}

var _ oracletools.Resolver = oracleResolver{}

type oracleBoundedService interface {
	hitlservice.Service
	AnswerAsAgentBounded(ctx context.Context, askID, agentName, text string, max int) error
	RespondAsAgentBounded(ctx context.Context, askID, agentName string, approved bool, guidance string, max int) error
}

func (r oracleResolver) refuse(askID, reason string) error {
	if r.out != nil {
		fmt.Fprintf(r.out, "oracle: verdict refused for ask %s: %s\n", askID, reason)
	}
	return &oracletools.RefusedError{Reason: reason}
}

func (r oracleResolver) Answer(ctx context.Context, askID, text string) error {
	row, err := r.store.GetHITLApproval(ctx, askID)
	if err != nil {
		if errors.Is(err, libdb.ErrNotFound) {
			return r.refuse(askID, fmt.Sprintf("ask %s no longer exists", askID))
		}
		return fmt.Errorf("read ask %s: %w", askID, err)
	}
	if !hitlservice.IsAttentionAsk(row) {
		return r.refuse(askID, fmt.Sprintf("ask %s is a gated tool call, not a question", askID))
	}
	if row.MissionID == nil || strings.TrimSpace(*row.MissionID) == "" {
		return r.refuse(askID, fmt.Sprintf("ask %s belongs to no subagent, so no envelope bounds it", askID))
	}
	m, err := r.missions.Get(ctx, *row.MissionID)
	if err != nil || m == nil {
		return r.refuse(askID, fmt.Sprintf("read subagent %s: %v", *row.MissionID, err))
	}
	bounds, err := r.hitl.AttentionBoundsFor(ctx, m.HITLPolicyName)
	if err != nil {
		return r.refuse(askID, "the subagent's envelope could not be read")
	}
	if !bounds.AllowAgentAnswers {
		return r.refuse(askID, "the subagent's envelope does not allow agent answers")
	}
	used, err := r.hitl.AgentAnswerCount(ctx, *row.MissionID)
	if err != nil {
		return r.refuse(askID, "prior agent answers could not be counted")
	}
	cap := bounds.EffectiveMaxAgentAnswers()
	if used >= cap {
		return r.refuse(askID, fmt.Sprintf("the subagent's agent-answer bound (%d) is spent", cap))
	}
	bounded, ok := r.hitl.(oracleBoundedService)
	if !ok {
		return r.refuse(askID, "this runtime cannot record a bounded agent answer")
	}
	err = bounded.AnswerAsAgentBounded(ctx, askID, oracleAgentName, text, cap)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, hitlservice.ErrAgentAnswerBoundSpent):
		return r.refuse(askID, fmt.Sprintf("the subagent's agent-answer bound (%d) is spent", cap))
	case errors.Is(err, hitlservice.ErrApprovalNotFound),
		errors.Is(err, hitlservice.ErrApprovalAlreadyResolved),
		errors.Is(err, hitlservice.ErrApprovalExpired):
		return r.refuse(askID, err.Error())
	default:
		return err
	}
}

func (r oracleResolver) Decide(ctx context.Context, askID string, approve bool, guidance string) error {
	row, err := r.store.GetHITLApproval(ctx, askID)
	if err != nil {
		if errors.Is(err, libdb.ErrNotFound) {
			return r.refuse(askID, fmt.Sprintf("ask %s no longer exists", askID))
		}
		return fmt.Errorf("read ask %s: %w", askID, err)
	}
	if hitlservice.IsAttentionAsk(row) {
		return r.refuse(askID, fmt.Sprintf("ask %s is a question, not a gated tool call", askID))
	}
	if row.MissionID == nil || strings.TrimSpace(*row.MissionID) == "" {
		return r.refuse(askID, fmt.Sprintf("ask %s belongs to no subagent, so no envelope bounds it", askID))
	}
	m, err := r.missions.Get(ctx, *row.MissionID)
	if err != nil || m == nil {
		return r.refuse(askID, fmt.Sprintf("read subagent %s: %v", *row.MissionID, err))
	}
	bounds, err := r.hitl.AttentionBoundsFor(ctx, m.HITLPolicyName)
	if err != nil {
		return r.refuse(askID, "the subagent's envelope could not be read")
	}
	allowApprovals := bounds.AllowAgentApprovals || (m.ParentSessionID == "" && r.approves)
	if !allowApprovals {
		return r.refuse(askID, "the subagent's envelope does not allow agent-decided tool calls")
	}
	bounded, ok := r.hitl.(oracleBoundedService)
	if !ok {
		return r.refuse(askID, "this runtime cannot record a bounded agent approval")
	}
	err = bounded.RespondAsAgentBounded(ctx, askID, oracleAgentName, approve, guidance, bounds.EffectiveMaxAgentApprovals())
	switch {
	case err == nil:
		return nil
	case errors.Is(err, hitlservice.ErrAgentApprovalBoundSpent):
		return r.refuse(askID, fmt.Sprintf("the subagent's agent-approval bound (%d) is spent", bounds.EffectiveMaxAgentApprovals()))
	case errors.Is(err, hitlservice.ErrApprovalNotFound),
		errors.Is(err, hitlservice.ErrApprovalAlreadyResolved),
		errors.Is(err, hitlservice.ErrApprovalExpired):
		return r.refuse(askID, err.Error())
	default:
		return err
	}
}

type oracleDriver struct {
	agent         agentservice.Agent
	chain         *taskengine.TaskChainDefinition
	chainRef      string
	policy        string
	templateVars  map[string]string
	contextLength int
	approves      bool
	missions      missionservice.Service
	out           io.Writer

	mu       sync.Mutex
	inFlight map[string]bool
}

var _ hitlservice.Adjudicator = (*oracleDriver)(nil)

func (d *oracleDriver) Adjudicate(ctx context.Context, ask hitlservice.Adjudication) {
	if ask.AskID == "" || ask.MissionID == "" {
		return
	}
	if ask.Kind == hitlservice.AskKindPermission && !d.approves {
		return
	}
	if !d.claim(ask.AskID) {
		return
	}
	defer d.release(ask.AskID)

	input, err := json.Marshal(newOracleInput(ask, d.intentOf(ctx, ask.MissionID)))
	if err != nil {
		return
	}
	binding := oracletools.NewAskBinding(ask.AskID, bindingKind(ask.Kind), string(input))
	runCtx := hitlservice.WithPolicyName(ctx, d.policyName())
	runCtx = oracletools.WithBinding(runCtx, binding)

	d.tracef("oracle: reviewing %s ask %s (subagent %s): %s", ask.Kind, ask.AskID, ask.MissionID, askHeadline(ask))
	start := time.Now()
	_, runErr := d.agent.Prompt(runCtx, agentservice.PromptRequest{
		Input:         string(input),
		InputValue:    string(input),
		InputType:     taskengine.DataTypeString,
		Chain:         d.chain,
		ChainRef:      d.chainRef,
		TemplateVars:  d.templateVars,
		ContextLength: d.contextLength,
	})
	elapsed := time.Since(start).Round(time.Millisecond)

	switch binding.Outcome() {
	case oracletools.OutcomeAnswered:
		d.tracef("oracle: answered ask %s in %s: %q", ask.AskID, elapsed, binding.Answer())
	case oracletools.OutcomeApproved:
		d.tracef("oracle: APPROVED %s.%s for subagent %s in %s", ask.ToolsName, ask.ToolName, ask.MissionID, elapsed)
	case oracletools.OutcomeDenied:
		d.tracef("oracle: DENIED %s.%s for subagent %s in %s: %s", ask.ToolsName, ask.ToolName, ask.MissionID, elapsed, binding.Guidance())
	case oracletools.OutcomeWait:
		d.tracef("oracle: WAIT for ask %s (%s) - it stays with a human", ask.AskID, elapsed)
	default:
		if runErr != nil {
			d.tracef("oracle: chain error for ask %s (%s): %v - it stays with a human", ask.AskID, elapsed, runErr)
		} else {
			d.tracef("oracle: no verdict for ask %s within the chain budgets (%s) - it stays with a human", ask.AskID, elapsed)
		}
	}
}

func (d *oracleDriver) intentOf(ctx context.Context, missionID string) string {
	if d.missions == nil {
		return ""
	}
	m, err := d.missions.Get(ctx, missionID)
	if err != nil || m == nil {
		return ""
	}
	return m.Intent
}

func (d *oracleDriver) policyName() string {
	if d.policy == "" {
		return oracleDefaultPolicyName
	}
	return d.policy
}

func (d *oracleDriver) claim(askID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.inFlight == nil {
		d.inFlight = map[string]bool{}
	}
	if d.inFlight[askID] {
		return false
	}
	d.inFlight[askID] = true
	return true
}

func (d *oracleDriver) release(askID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.inFlight, askID)
}

func (d *oracleDriver) tracef(format string, args ...any) {
	if d.out != nil {
		fmt.Fprintf(d.out, format+"\n", args...)
	}
}

func bindingKind(k hitlservice.AskKind) oracletools.AskKind {
	if k == hitlservice.AskKindAttention {
		return oracletools.AskKindAttention
	}
	return oracletools.AskKindPermission
}

func askHeadline(ask hitlservice.Adjudication) string {
	if ask.Kind == hitlservice.AskKindAttention {
		return ask.Summary
	}
	return fmt.Sprintf("%s.%s %s", ask.ToolsName, ask.ToolName, ask.ArgsSummary)
}

type oracleInput struct {
	AskID       string `json:"askId"`
	Kind        string `json:"kind"`
	MissionID   string `json:"missionId"`
	AgentName   string `json:"agentName,omitempty"`
	Intent      string `json:"intent,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Detail      string `json:"detail,omitempty"`
	ToolsName   string `json:"toolsName,omitempty"`
	ToolName    string `json:"toolName,omitempty"`
	ArgsSummary string `json:"argsSummary,omitempty"`
	Diff        string `json:"diff,omitempty"`
	PolicyName  string `json:"policyName,omitempty"`
	OnTimeout   string `json:"onTimeout,omitempty"`
}

func newOracleInput(ask hitlservice.Adjudication, intent string) oracleInput {
	return oracleInput{
		AskID:       ask.AskID,
		Kind:        string(ask.Kind),
		MissionID:   ask.MissionID,
		AgentName:   ask.AgentName,
		Intent:      intent,
		Summary:     ask.Summary,
		Detail:      ask.Detail,
		ToolsName:   ask.ToolsName,
		ToolName:    ask.ToolName,
		ArgsSummary: ask.ArgsSummary,
		Diff:        ask.Diff,
		PolicyName:  ask.PolicyName,
		OnTimeout:   ask.OnTimeout,
	}
}
