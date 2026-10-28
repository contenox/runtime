package hitlservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

type AskKind string

const (
	AskKindPermission AskKind = "permission"
	AskKindAttention  AskKind = "attention"
)

type Adjudication struct {
	AskID       string
	Kind        AskKind
	MissionID   string
	SessionID   string
	InstanceID  string
	AgentName   string
	Summary     string
	Detail      string
	ToolsName   string
	ToolName    string
	ArgsSummary string
	Diff        string
	PolicyName  string
	MatchedRule string
	OnTimeout   string
}

type Adjudicator interface {
	Adjudicate(ctx context.Context, ask Adjudication)
}

func (s *service) SetAdjudicator(adjudicator Adjudicator) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adjudicator = adjudicator
}

func (s *service) offerAdjudication(ctx context.Context, ask Adjudication) {
	s.mu.Lock()
	adjudicator := s.adjudicator
	s.mu.Unlock()
	if adjudicator == nil {
		return
	}
	go adjudicator.Adjudicate(context.WithoutCancel(ctx), ask)
}

func adjudicationFromApprovalRequest(askID string, req ApprovalRequest) Adjudication {
	matchedRule := ""
	if req.MatchedRule != nil {
		matchedRule = fmt.Sprintf("%d", *req.MatchedRule)
	}
	return Adjudication{
		AskID:       askID,
		Kind:        AskKindPermission,
		MissionID:   req.MissionID,
		SessionID:   req.SessionID,
		InstanceID:  req.InstanceID,
		AgentName:   req.AgentName,
		ToolsName:   req.ToolsName,
		ToolName:    req.ToolName,
		ArgsSummary: summarizeApprovalArgs(req.Args),
		Diff:        req.Diff,
		PolicyName:  req.PolicyName,
		MatchedRule: matchedRule,
		OnTimeout:   string(req.OnTimeout),
	}
}

func adjudicationFromAttentionRequest(askID string, req AttentionRequest) Adjudication {
	return Adjudication{
		AskID:      askID,
		Kind:       AskKindAttention,
		MissionID:  req.MissionID,
		SessionID:  req.SessionID,
		InstanceID: req.InstanceID,
		AgentName:  req.AgentName,
		ToolsName:  AttentionToolsName,
		ToolName:   AttentionToolName,
		Summary:    req.Summary,
		Detail:     req.Detail,
	}
}

var ErrAgentApprovalBoundSpent = errors.New("hitlservice: mission agent-approval bound spent")

func (s *service) RespondAsAgentBounded(ctx context.Context, askID, agentName string, approved bool, guidance string, max int) error {
	if s.approvals == nil {
		return fmt.Errorf("hitlservice: durable approval store not configured; pass a runtimetypes.Store-backed store to New/NewWithDefaultPolicy")
	}
	row, err := s.approvals.GetHITLApproval(ctx, askID)
	if err != nil {
		if errors.Is(err, libdb.ErrNotFound) {
			return ErrApprovalNotFound
		}
		return fmt.Errorf("hitlservice: look up ask %s: %w", askID, err)
	}
	if IsAttentionAsk(row) {
		return fmt.Errorf("hitlservice: ask %s is a question, which is answered with words, not approve/deny", askID)
	}
	if row.MissionID == nil || strings.TrimSpace(*row.MissionID) == "" {
		return fmt.Errorf("hitlservice: ask %s belongs to no mission, so no agent-approval bound can be applied to it", askID)
	}
	state := runtimetypes.HITLApprovalApproved
	if !approved {
		state = runtimetypes.HITLApprovalDenied
	}
	resolution := marshalDecidedApprovalResolution(approved, agentActor(agentName), guidance)
	now := time.Now().UTC()
	resolveErr := s.approvals.ResolveHITLApprovalWithinBound(ctx, askID, runtimetypes.AgentAnswerBound{
		MissionID:      *row.MissionID,
		ResolutionLike: agentApprovalResolutionLike,
		Max:            max,
	}, state, resolution, now)
	if resolveErr != nil {
		if !errors.Is(resolveErr, libdb.ErrNotFound) {
			return fmt.Errorf("hitlservice: resolve ask %s: %w", askID, resolveErr)
		}
		current, getErr := s.approvals.GetHITLApproval(ctx, askID)
		if getErr != nil {
			return fmt.Errorf("hitlservice: look up ask %s: %w", askID, getErr)
		}
		if current.State == runtimetypes.HITLApprovalPending {
			return ErrAgentApprovalBoundSpent
		}
		if current.State == runtimetypes.HITLApprovalExpired {
			return ErrApprovalExpired
		}
		return ErrApprovalAlreadyResolved
	}
	s.mu.Lock()
	ch, ok := s.pending[askID]
	s.mu.Unlock()
	if ok {
		select {
		case ch <- answer{approved: approved}:
		default:
		}
	}
	return nil
}

const agentApprovalResolutionLike = `%"decidedBy":%`

func marshalDecidedApprovalResolution(approved bool, by, guidance string) json.RawMessage {
	res := approvalResolution{Approved: &approved}
	if actor := strings.TrimSpace(by); actor != "" {
		res.DecidedBy = &actor
	}
	if text := strings.TrimSpace(guidance); text != "" {
		res.Guidance = &text
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func SetAdjudicator(svc Service, adjudicator Adjudicator) {
	if service, ok := svc.(*service); ok {
		service.SetAdjudicator(adjudicator)
	}
}
