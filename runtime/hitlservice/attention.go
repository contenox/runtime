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
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/google/uuid"
)

const (
	AttentionToolsName = "mission"
	AttentionToolName  = "mission_ask_attention"
)

var ErrAttentionUnanswered = errors.New("hitlservice: attention ask went unanswered")

type AttentionRequest struct {
	Summary    string
	Detail     string
	MissionID  string
	InstanceID string
	SessionID  string
	AgentName  string

	OnRaised func(askID string)
}

func IsAttentionAsk(row *runtimetypes.HITLApproval) bool {
	return row != nil && row.ToolsName == AttentionToolsName && row.ToolName == AttentionToolName
}

func AnswerOf(row *runtimetypes.HITLApproval) string {
	if row == nil || len(row.Resolution) == 0 {
		return ""
	}
	var res approvalResolution
	if err := json.Unmarshal(row.Resolution, &res); err != nil || res.Answer == nil {
		return ""
	}
	return *res.Answer
}

func (s *service) RequestAttention(ctx context.Context, req AttentionRequest, sink taskengine.TaskEventSink) (string, error) {
	if s.approvals == nil {
		return "", fmt.Errorf("hitlservice: durable approval store not configured; pass a runtimetypes.Store-backed store to New/NewWithDefaultPolicy")
	}
	summary := strings.TrimSpace(req.Summary)
	if summary == "" {
		return "", fmt.Errorf("hitlservice: attention ask requires a summary")
	}

	askID := uuid.NewString()
	now := time.Now().UTC()
	timeout := s.ceiling()

	row := &runtimetypes.HITLApproval{
		ID:          askID,
		ToolsName:   AttentionToolsName,
		ToolName:    AttentionToolName,
		ArgsSummary: summary,
		OnTimeout:   string(ActionDeny),
		State:       runtimetypes.HITLApprovalPending,
		InstanceID:  req.InstanceID,
		SessionID:   req.SessionID,
		AgentName:   req.AgentName,
		CreatedAt:   now,
		ExpiresAt:   now.Add(timeout),
	}
	if detail := strings.TrimSpace(req.Detail); detail != "" {
		row.Diff = &detail
	}
	if req.MissionID != "" {
		missionID := req.MissionID
		row.MissionID = &missionID
	}
	if err := s.approvals.CreateHITLApproval(ctx, row); err != nil {
		return "", fmt.Errorf("hitlservice: persist attention ask: %w", err)
	}

	if req.OnRaised != nil {
		req.OnRaised(askID)
	}

	ch := make(chan answer, 1)
	s.mu.Lock()
	s.pending[askID] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, askID)
		s.mu.Unlock()
	}()

	if sink != nil {
		ev := taskengine.NewTaskEvent(ctx, taskengine.TaskEventApprovalRequested)
		ev.ApprovalID = askID
		ev.HookName = AttentionToolsName
		ev.ToolName = AttentionToolName
		ev.ApprovalArgs = map[string]any{"summary": summary, "detail": req.Detail}
		_ = sink.PublishTaskEvent(ctx, ev)
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	poll := time.NewTicker(attentionPollInterval)
	defer poll.Stop()

	for {
		select {
		case ans := <-ch:
			if !ans.approved || strings.TrimSpace(ans.text) == "" {
				return "", ErrAttentionUnanswered
			}
			return ans.text, nil
		case <-poll.C:
			row, err := s.approvals.GetHITLApproval(ctx, askID)
			if err != nil || row.State == runtimetypes.HITLApprovalPending {
				continue
			}
			if text := AnswerOf(row); strings.TrimSpace(text) != "" {
				return text, nil
			}
			return "", ErrAttentionUnanswered
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", ErrAttentionUnanswered
		}
	}
}

const attentionPollInterval = time.Second

func (s *service) Answer(ctx context.Context, askID, text string) error {
	return s.answerAttention(ctx, askID, text, "")
}

func (s *service) answerAttention(ctx context.Context, askID, text, by string) error {
	if s.approvals == nil {
		return fmt.Errorf("hitlservice: durable approval store not configured; pass a runtimetypes.Store-backed store to New/NewWithDefaultPolicy")
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("hitlservice: an attention answer cannot be empty")
	}
	row, err := s.approvals.GetHITLApproval(ctx, askID)
	if err != nil {
		if errors.Is(err, libdb.ErrNotFound) {
			return ErrApprovalNotFound
		}
		return fmt.Errorf("hitlservice: look up ask %s: %w", askID, err)
	}
	if !IsAttentionAsk(row) {
		return fmt.Errorf("hitlservice: ask %s is a permission request (%s.%s), which is answered approve/deny, not with text",
			askID, row.ToolsName, row.ToolName)
	}

	now := time.Now().UTC()
	if err := s.approvals.ResolveHITLApproval(ctx, askID, runtimetypes.HITLApprovalApproved, marshalAttentionResolution(text, by), now); err != nil {
		if !errors.Is(err, libdb.ErrNotFound) {
			return fmt.Errorf("hitlservice: resolve ask %s: %w", askID, err)
		}
		current, getErr := s.approvals.GetHITLApproval(ctx, askID)
		if getErr != nil {
			return fmt.Errorf("hitlservice: look up ask %s: %w", askID, getErr)
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
		case ch <- answer{approved: true, text: text}:
		default:
		}
	}
	return nil
}

const answeredByAgent = "agent"

func (s *service) AnswerAsAgent(ctx context.Context, askID, text string) error {
	return s.answerAttention(ctx, askID, text, answeredByAgent)
}

func (s *service) PendingAttentionAsks(ctx context.Context, missionID string) ([]*runtimetypes.HITLApproval, error) {
	if s.approvals == nil {
		return nil, fmt.Errorf("hitlservice: durable approval store not configured")
	}
	rows, err := s.approvals.ListHITLApprovalsForMission(ctx, missionID, missionAskScanLimit)
	if err != nil {
		return nil, fmt.Errorf("hitlservice: list asks for mission %s: %w", missionID, err)
	}
	out := make([]*runtimetypes.HITLApproval, 0, len(rows))
	for _, row := range rows {
		if IsAttentionAsk(row) && row.State == runtimetypes.HITLApprovalPending {
			out = append(out, row)
		}
	}
	return out, nil
}

func (s *service) AgentAnswerCount(ctx context.Context, missionID string) (int, error) {
	if s.approvals == nil {
		return 0, fmt.Errorf("hitlservice: durable approval store not configured")
	}
	rows, err := s.approvals.ListHITLApprovalsForMission(ctx, missionID, missionAskScanLimit)
	if err != nil {
		return 0, fmt.Errorf("hitlservice: list asks for mission %s: %w", missionID, err)
	}
	count := 0
	for _, row := range rows {
		if !IsAttentionAsk(row) || len(row.Resolution) == 0 {
			continue
		}
		var res approvalResolution
		if json.Unmarshal(row.Resolution, &res) != nil {
			continue
		}
		if res.AnsweredBy != nil && *res.AnsweredBy == answeredByAgent {
			count++
		}
	}
	return count, nil
}

const missionAskScanLimit = 200
