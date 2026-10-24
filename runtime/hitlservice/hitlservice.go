package hitlservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/google/uuid"

	libdb "github.com/contenox/runtime/libdbexec"
)

type KVReader interface {
	GetKV(ctx context.Context, key string, out interface{}) error
}

type PolicyEvaluator interface {
	Evaluate(ctx context.Context, toolsName, toolName string, args map[string]any) (EvaluationResult, error)
}

type ComputeBoundsReader interface {
	ComputeBoundsFor(ctx context.Context, policyName string) (ComputeBounds, error)
}

type PolicyValidator interface {
	ValidatePolicy(ctx context.Context, policyName string) error
}

func NewPolicyValidator(src PolicySource, tenantID, fallbackPolicy string) PolicyValidator {
	return &policyValidator{src: src, tenantID: tenantID, fallback: fallbackPolicy}
}

type policyValidator struct {
	src      PolicySource
	tenantID string
	fallback string
}

func (v *policyValidator) ValidatePolicy(ctx context.Context, policyName string) error {
	name := strings.TrimSpace(policyName)
	if name == "" {
		name = v.fallback
	}
	if name == "" {
		name = defaultPolicyName
	}
	if _, err := loadPolicy(ctx, v.src, v.tenantID, name); err != nil {
		return err
	}
	return nil
}

type approvalStore interface {
	CreateHITLApproval(ctx context.Context, a *runtimetypes.HITLApproval) error
	GetHITLApproval(ctx context.Context, id string) (*runtimetypes.HITLApproval, error)
	ResolveHITLApproval(ctx context.Context, id string, state runtimetypes.HITLApprovalState, resolution json.RawMessage, resolvedAt time.Time) error
	ListExpiredHITLApprovals(ctx context.Context, asOf time.Time, limit int) ([]*runtimetypes.HITLApproval, error)
	ListHITLApprovals(ctx context.Context, state runtimetypes.HITLApprovalState, createdAtCursor *time.Time, limit int) ([]*runtimetypes.HITLApproval, error)
	ListHITLApprovalsForMission(ctx context.Context, missionID string, limit int) ([]*runtimetypes.HITLApproval, error)
}

type Service interface {
	PolicyEvaluator

	RequestApproval(ctx context.Context, req ApprovalRequest, sink taskengine.TaskEventSink) (bool, error)

	Respond(ctx context.Context, approvalID string, approved bool) error

	RequestAttention(ctx context.Context, req AttentionRequest, sink taskengine.TaskEventSink) (string, error)

	Answer(ctx context.Context, askID, text string) error

	AnswerAsAgent(ctx context.Context, askID, text string) error

	PendingAttentionAsks(ctx context.Context, missionID string) ([]*runtimetypes.HITLApproval, error)

	AttentionBoundsFor(ctx context.Context, policyName string) (AttentionBounds, error)

	AgentAnswerCount(ctx context.Context, missionID string) (int, error)

	SweepExpired(ctx context.Context) (int, error)

	ListPending(ctx context.Context, limit int) ([]*runtimetypes.HITLApproval, error)
}

var (
	ErrApprovalNotFound        = errors.New("hitlservice: approval not found")
	ErrApprovalAlreadyResolved = errors.New("hitlservice: approval already answered")
	ErrApprovalExpired         = errors.New("hitlservice: approval expired before it was answered")
)

const defaultPolicyName = "hitl-policy-default.json"

const DefaultApprovalCeiling = time.Hour

type service struct {
	src            PolicySource
	tenantID       string
	store          KVReader
	tracker        libtracker.ActivityTracker
	fallbackPolicy string
	approvals      approvalStore

	mu              sync.Mutex
	pending         map[string]chan answer
	approvalCeiling time.Duration
}

func New(src PolicySource, tenantID string, store KVReader, tracker libtracker.ActivityTracker) Service {
	return NewWithDefaultPolicy(src, tenantID, store, tracker, defaultPolicyName)
}

func NewWithDefaultPolicy(src PolicySource, tenantID string, store KVReader, tracker libtracker.ActivityTracker, fallbackPolicy string) Service {
	if tracker == nil {
		tracker = libtracker.NoopTracker{}
	}
	if strings.TrimSpace(fallbackPolicy) == "" {
		fallbackPolicy = defaultPolicyName
	}
	svc := &service{
		src:            src,
		tenantID:       tenantID,
		store:          store,
		tracker:        tracker,
		fallbackPolicy: fallbackPolicy,
		pending:        make(map[string]chan answer),
	}
	if as, ok := store.(approvalStore); ok {
		svc.approvals = as
	}
	return svc
}

func SetApprovalCeiling(svc Service, ceiling time.Duration) {
	if ceiling <= 0 {
		return
	}
	if s, ok := svc.(*service); ok {
		s.mu.Lock()
		s.approvalCeiling = ceiling
		s.mu.Unlock()
	}
}

func (s *service) ceiling() time.Duration {
	s.mu.Lock()
	d := s.approvalCeiling
	s.mu.Unlock()
	if d <= 0 {
		return DefaultApprovalCeiling
	}
	return d
}

var _ Service = (*service)(nil)
var _ ComputeBoundsReader = (*service)(nil)

func (s *service) ComputeBoundsFor(ctx context.Context, policyName string) (ComputeBounds, error) {
	policyPath := strings.TrimSpace(policyName)
	if policyPath == "" {
		policyPath = s.fallbackPolicy
	}
	if policyPath == "" {
		policyPath = defaultPolicyName
	}
	p, err := loadPolicy(ctx, s.src, s.tenantID, policyPath)
	if err != nil {
		return ComputeBounds{}, fmt.Errorf("hitlservice: load compute bounds for %q: %w", policyPath, err)
	}
	if p.Compute == nil {
		return ComputeBounds{}, nil
	}
	return *p.Compute, nil
}

const kvPrefixHITLPolicy = "cli.hitl-policy-name"

type policyNameContextKey struct{}

func WithPolicyName(ctx context.Context, policyName string) context.Context {
	policyName = strings.TrimSpace(policyName)
	if policyName == "" {
		return ctx
	}
	return context.WithValue(ctx, policyNameContextKey{}, policyName)
}

func PolicyNameFromContext(ctx context.Context) string {
	name, _ := ctx.Value(policyNameContextKey{}).(string)
	return strings.TrimSpace(name)
}

func (s *service) readActivePolicyName(ctx context.Context) string {
	var val string
	if err := s.store.GetKV(ctx, kvPrefixHITLPolicy, &val); err != nil {
		return ""
	}
	return strings.TrimSpace(val)
}

func (s *service) Evaluate(ctx context.Context, toolsName, toolName string, args map[string]any) (EvaluationResult, error) {
	reportErr, reportChange, end := s.tracker.Start(ctx, "hitl", "evaluate", "toolsName", toolsName, "toolName", toolName)
	defer end()
	policyPath := PolicyNameFromContext(ctx)
	if policyPath == "" {
		policyPath = s.readActivePolicyName(ctx)
	}
	if policyPath == "" {
		policyPath = s.fallbackPolicy
	}
	if policyPath == "" {
		policyPath = defaultPolicyName
	}
	p, err := loadPolicy(ctx, s.src, s.tenantID, policyPath)
	if err != nil {
		reportErr(fmt.Errorf("hitl: falling back to built-in default policy: %w", err))
		p = defaultPolicy()
	}
	reportChange("policy", policyPath)
	result := evaluate(p, toolsName, toolName, args)
	result.PolicyName = policyPath
	return result, nil
}

func (s *service) RequestApproval(ctx context.Context, req ApprovalRequest, sink taskengine.TaskEventSink) (bool, error) {
	if s.approvals == nil {
		return false, fmt.Errorf("hitlservice: durable approval store not configured; pass a runtimetypes.Store-backed store to New/NewWithDefaultPolicy")
	}
	approvalID := uuid.NewString()
	now := time.Now().UTC()

	onTimeout := req.OnTimeout
	if onTimeout == "" {
		onTimeout = ActionDeny
	}
	ruleTimeout := req.TimeoutS > 0
	timeoutDur := s.ceiling()
	if ruleTimeout {
		timeoutDur = time.Duration(req.TimeoutS) * time.Second
	}

	row := &runtimetypes.HITLApproval{
		ID:          approvalID,
		ToolsName:   req.ToolsName,
		ToolName:    req.ToolName,
		ArgsSummary: summarizeApprovalArgs(req.Args),
		PolicyName:  req.PolicyName,
		MatchedRule: req.MatchedRule,
		OnTimeout:   string(onTimeout),
		State:       runtimetypes.HITLApprovalPending,
		InstanceID:  req.InstanceID,
		SessionID:   req.SessionID,
		AgentName:   req.AgentName,
		CreatedAt:   now,
		ExpiresAt:   now.Add(timeoutDur),
	}
	if req.MissionID != "" {
		missionID := req.MissionID
		row.MissionID = &missionID
	}
	if req.Diff != "" {
		diff := req.Diff
		row.Diff = &diff
	}
	if err := s.approvals.CreateHITLApproval(ctx, row); err != nil {
		return false, fmt.Errorf("hitlservice: persist pending approval: %w", err)
	}

	ch := make(chan answer, 1)
	s.mu.Lock()
	s.pending[approvalID] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, approvalID)
		s.mu.Unlock()
	}()

	ev := taskengine.NewTaskEvent(ctx, taskengine.TaskEventApprovalRequested)
	ev.ApprovalID = approvalID
	ev.HookName = req.ToolsName
	ev.ToolName = req.ToolName
	ev.ApprovalArgs = req.Args
	ev.ApprovalDiff = req.Diff
	if err := sink.PublishTaskEvent(ctx, ev); err != nil {
		return false, fmt.Errorf("hitl: publish approval request: %w", err)
	}

	waitCtx := ctx
	if !ruleTimeout {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, timeoutDur)
		defer cancel()
	}

	select {
	case ans := <-ch:
		return ans.approved, nil
	case <-waitCtx.Done():
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, nil
	}
}

func (s *service) Respond(ctx context.Context, approvalID string, approved bool) error {
	if s.approvals == nil {
		return fmt.Errorf("hitlservice: durable approval store not configured; pass a runtimetypes.Store-backed store to New/NewWithDefaultPolicy")
	}
	state := runtimetypes.HITLApprovalApproved
	if !approved {
		state = runtimetypes.HITLApprovalDenied
	}
	now := time.Now().UTC()
	err := s.approvals.ResolveHITLApproval(ctx, approvalID, state, marshalApprovalResolution(approved), now)
	if err != nil {
		if !errors.Is(err, libdb.ErrNotFound) {
			return fmt.Errorf("hitlservice: resolve approval %s: %w", approvalID, err)
		}
		row, getErr := s.approvals.GetHITLApproval(ctx, approvalID)
		if getErr != nil {
			if errors.Is(getErr, libdb.ErrNotFound) {
				return ErrApprovalNotFound
			}
			return fmt.Errorf("hitlservice: look up approval %s: %w", approvalID, getErr)
		}
		if row.State == runtimetypes.HITLApprovalExpired {
			return ErrApprovalExpired
		}
		return ErrApprovalAlreadyResolved
	}

	s.mu.Lock()
	ch, ok := s.pending[approvalID]
	s.mu.Unlock()
	if ok {
		select {
		case ch <- answer{approved: approved}:
		default:
		}
	}
	return nil
}

const sweepBatchLimit = 200

func (s *service) SweepExpired(ctx context.Context) (int, error) {
	if s.approvals == nil {
		return 0, nil
	}
	now := time.Now().UTC()
	rows, err := s.approvals.ListExpiredHITLApprovals(ctx, now, sweepBatchLimit)
	if err != nil {
		return 0, fmt.Errorf("hitlservice: list expired approvals: %w", err)
	}
	expired := 0
	for _, row := range rows {
		approved := onTimeoutOutcome(Action(row.OnTimeout))
		err := s.approvals.ResolveHITLApproval(ctx, row.ID, runtimetypes.HITLApprovalExpired, marshalApprovalResolution(approved), now)
		if err != nil {
			if errors.Is(err, libdb.ErrNotFound) {
				continue
			}
			return expired, fmt.Errorf("hitlservice: resolve expired approval %s: %w", row.ID, err)
		}
		expired++
		s.mu.Lock()
		ch, ok := s.pending[row.ID]
		s.mu.Unlock()
		if ok {
			select {
			case ch <- answer{approved: approved}:
			default:
			}
		}
	}
	return expired, nil
}

func (s *service) ListPending(ctx context.Context, limit int) ([]*runtimetypes.HITLApproval, error) {
	if s.approvals == nil {
		return nil, fmt.Errorf("hitlservice: durable approval store not configured; pass a runtimetypes.Store-backed store to New/NewWithDefaultPolicy")
	}
	rows, err := s.approvals.ListHITLApprovals(ctx, runtimetypes.HITLApprovalPending, nil, limit)
	if err != nil {
		return nil, fmt.Errorf("hitlservice: list pending approvals: %w", err)
	}
	if rows == nil {
		rows = []*runtimetypes.HITLApproval{}
	}
	return rows, nil
}

func onTimeoutOutcome(onTimeout Action) bool {
	return onTimeout == ActionAllow
}

type approvalResolution struct {
	Approved   *bool   `json:"approved,omitempty"`
	Answer     *string `json:"answer,omitempty"`
	AnsweredBy *string `json:"answeredBy,omitempty"`
}

type answer struct {
	approved bool
	text     string
}

func marshalAttentionResolution(text, by string) json.RawMessage {
	res := approvalResolution{Answer: &text}
	if by != "" {
		res.AnsweredBy = &by
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func marshalApprovalResolution(approved bool) json.RawMessage {
	raw, err := json.Marshal(approvalResolution{Approved: &approved})
	if err != nil {
		return json.RawMessage(fmt.Sprintf(`{"approved":%t}`, approved))
	}
	return raw
}

func summarizeApprovalArgs(args map[string]any) string {
	for _, key := range []string{"path", "command", "url", "pattern"} {
		v, ok := args[key].(string)
		if !ok || strings.TrimSpace(v) == "" {
			continue
		}
		s := strings.TrimSpace(strings.ReplaceAll(v, "\n", " "))
		if len([]rune(s)) > 96 {
			return string([]rune(s)[:95]) + "..."
		}
		return s
	}
	return ""
}
