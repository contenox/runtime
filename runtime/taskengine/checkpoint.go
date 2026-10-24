package taskengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

const CheckpointSchemaVersion = 1

var ErrCheckpointVersion = errors.New("taskengine: unsupported checkpoint schema version")

var checkpointMigrations = map[int]func(raw []byte) ([]byte, error){}

type ApprovalPendingError struct {
	ApprovalID string
	ToolName   string
}

func (e *ApprovalPendingError) Error() string {
	return fmt.Sprintf("tool call %q is awaiting human approval (approval %s); the run suspends until the approval is answered", e.ToolName, e.ApprovalID)
}

type ChainSuspendedError struct {
	ApprovalID string
	Scope      EventScope
}

func (e *ChainSuspendedError) Error() string {
	return fmt.Sprintf("chain %s suspended at task %s awaiting approval %s; answer the approval to resume the run", e.Scope.Chain, e.Scope.Task, e.ApprovalID)
}

type PendingToolCall struct {
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Checkpoint struct {
	ApprovalID        string
	PendingCalls      []PendingToolCall
	Chain             *TaskChainDefinition
	TaskID            string
	RetryIndex        int
	Scope             EventScope
	Vars              map[string]any
	VarTypes          map[string]DataType
	EdgeCounts        map[string]int
	History           ChatHistory
	TemplateVars      map[string]string
	ToolsAllowlist    []string
	HasToolsAllowlist bool
	ContextLength     int
	SessionID         string
	MissionID         string
	RequestID         string
	ChainRef          string
	CreatedAt         time.Time
}

type checkpointVarWire struct {
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value"`
}

type checkpointWireV1 struct {
	SchemaVersion     int                          `json:"schema_version"`
	ApprovalID        string                       `json:"approval_id"`
	PendingCalls      []PendingToolCall            `json:"pending_calls,omitempty"`
	Chain             *TaskChainDefinition         `json:"chain"`
	TaskID            string                       `json:"task_id"`
	RetryIndex        int                          `json:"retry_index"`
	Scope             EventScope                   `json:"scope"`
	Vars              map[string]checkpointVarWire `json:"vars,omitempty"`
	EdgeCounts        map[string]int               `json:"edge_counts,omitempty"`
	History           ChatHistory                  `json:"history"`
	TemplateVars      map[string]string            `json:"template_vars,omitempty"`
	ToolsAllowlist    []string                     `json:"tools_allowlist,omitempty"`
	HasToolsAllowlist bool                         `json:"has_tools_allowlist,omitempty"`
	ContextLength     int                          `json:"context_length,omitempty"`
	SessionID         string                       `json:"session_id,omitempty"`
	MissionID         string                       `json:"mission_id,omitempty"`
	RequestID         string                       `json:"request_id,omitempty"`
	ChainRef          string                       `json:"chain_ref,omitempty"`
	CreatedAt         time.Time                    `json:"created_at"`
}

func MarshalCheckpoint(cp *Checkpoint) ([]byte, error) {
	if cp == nil {
		return nil, fmt.Errorf("taskengine: cannot marshal a nil checkpoint")
	}
	wire := checkpointWireV1{
		SchemaVersion:     CheckpointSchemaVersion,
		ApprovalID:        cp.ApprovalID,
		PendingCalls:      cp.PendingCalls,
		Chain:             cp.Chain,
		TaskID:            cp.TaskID,
		RetryIndex:        cp.RetryIndex,
		Scope:             cp.Scope,
		EdgeCounts:        cp.EdgeCounts,
		History:           cp.History,
		TemplateVars:      cp.TemplateVars,
		ToolsAllowlist:    cp.ToolsAllowlist,
		HasToolsAllowlist: cp.HasToolsAllowlist,
		ContextLength:     cp.ContextLength,
		SessionID:         cp.SessionID,
		MissionID:         cp.MissionID,
		RequestID:         cp.RequestID,
		ChainRef:          cp.ChainRef,
		CreatedAt:         cp.CreatedAt,
	}
	if len(cp.Vars) > 0 {
		wire.Vars = make(map[string]checkpointVarWire, len(cp.Vars))
		for name, value := range cp.Vars {
			dt, ok := cp.VarTypes[name]
			if !ok {
				dt = InferDataType(value)
			}
			raw, err := json.Marshal(value)
			if err != nil {
				return nil, fmt.Errorf("taskengine: checkpoint var %q (%s) is not JSON-serializable: %w", name, dt.String(), err)
			}
			wire.Vars[name] = checkpointVarWire{Type: dt.String(), Value: raw}
		}
	}
	return json.Marshal(wire)
}

func UnmarshalCheckpoint(raw []byte) (*Checkpoint, error) {
	var probe struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("taskengine: checkpoint payload is not valid JSON: %w", err)
	}
	version := probe.SchemaVersion
	if version <= 0 {
		return nil, fmt.Errorf("%w: payload declares no schema_version", ErrCheckpointVersion)
	}
	raw, version, err := migrateCheckpointPayload(raw, version, CheckpointSchemaVersion)
	if err != nil {
		return nil, err
	}

	var wire checkpointWireV1
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("taskengine: decode checkpoint v%d: %w", version, err)
	}
	cp := &Checkpoint{
		ApprovalID:        wire.ApprovalID,
		PendingCalls:      wire.PendingCalls,
		Chain:             wire.Chain,
		TaskID:            wire.TaskID,
		RetryIndex:        wire.RetryIndex,
		Scope:             wire.Scope,
		EdgeCounts:        wire.EdgeCounts,
		History:           wire.History,
		TemplateVars:      wire.TemplateVars,
		ToolsAllowlist:    wire.ToolsAllowlist,
		HasToolsAllowlist: wire.HasToolsAllowlist,
		ContextLength:     wire.ContextLength,
		SessionID:         wire.SessionID,
		MissionID:         wire.MissionID,
		RequestID:         wire.RequestID,
		ChainRef:          wire.ChainRef,
		CreatedAt:         wire.CreatedAt,
	}
	if len(wire.Vars) > 0 {
		cp.Vars = make(map[string]any, len(wire.Vars))
		cp.VarTypes = make(map[string]DataType, len(wire.Vars))
		for name, v := range wire.Vars {
			dt, err := DataTypeFromString(v.Type)
			if err != nil {
				return nil, fmt.Errorf("taskengine: checkpoint var %q: %w", name, err)
			}
			value, err := decodeCheckpointVar(dt, v.Value)
			if err != nil {
				return nil, fmt.Errorf("taskengine: checkpoint var %q: %w", name, err)
			}
			cp.Vars[name] = value
			cp.VarTypes[name] = dt
		}
	}
	return cp, nil
}

func migrateCheckpointPayload(raw []byte, version, target int) ([]byte, int, error) {
	for version < target {
		migrate, ok := checkpointMigrations[version]
		if !ok {
			return nil, version, fmt.Errorf("%w: no migration from version %d toward %d", ErrCheckpointVersion, version, target)
		}
		migrated, err := migrate(raw)
		if err != nil {
			return nil, version, fmt.Errorf("taskengine: checkpoint migration from version %d failed: %w", version, err)
		}
		raw = migrated
		version++
	}
	if version > target {
		return nil, version, fmt.Errorf("%w: payload is version %d but this binary knows only %d — update the binary to resume this run", ErrCheckpointVersion, version, target)
	}
	return raw, version, nil
}

func decodeCheckpointVar(dt DataType, raw json.RawMessage) (any, error) {
	switch dt {
	case DataTypeNil:
		return nil, nil
	case DataTypeString:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("declared string but does not decode as one: %w", err)
		}
		return s, nil
	case DataTypeInt:
		var n int
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, fmt.Errorf("declared int but does not decode as one: %w", err)
		}
		return n, nil
	case DataTypeChatHistory:
		var h ChatHistory
		if err := json.Unmarshal(raw, &h); err != nil {
			return nil, fmt.Errorf("declared chat_history but does not decode as one: %w", err)
		}
		return h, nil
	case DataTypeJSON, DataTypeAny:
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("does not decode as JSON: %w", err)
		}
		return v, nil
	default:
		return nil, fmt.Errorf("unknown data type %d", int(dt))
	}
}

type CheckpointSaver interface {
	SaveCheckpoint(ctx context.Context, cp *Checkpoint) error
}

type checkpointSaverContextKey struct{}

func WithCheckpointSaver(ctx context.Context, saver CheckpointSaver) context.Context {
	if saver == nil {
		return ctx
	}
	return context.WithValue(ctx, checkpointSaverContextKey{}, saver)
}

func checkpointSaverFromContext(ctx context.Context) CheckpointSaver {
	saver, _ := ctx.Value(checkpointSaverContextKey{}).(CheckpointSaver)
	return saver
}

func HasCheckpointSaver(ctx context.Context) bool {
	return checkpointSaverFromContext(ctx) != nil
}

type approvalVerdictsContextKey struct{}

func WithApprovalVerdicts(ctx context.Context, verdicts map[string]bool) context.Context {
	if len(verdicts) == 0 {
		return ctx
	}
	copied := make(map[string]bool, len(verdicts))
	for k, v := range verdicts {
		copied[k] = v
	}
	return context.WithValue(ctx, approvalVerdictsContextKey{}, copied)
}

func ApprovalVerdictFromContext(ctx context.Context, approvalID string) (approved bool, ok bool) {
	if approvalID == "" {
		return false, false
	}
	verdicts, _ := ctx.Value(approvalVerdictsContextKey{}).(map[string]bool)
	if verdicts == nil {
		return false, false
	}
	approved, ok = verdicts[approvalID]
	return approved, ok
}

type GateResult struct {
	Value any
	Type  DataType
}

func MarshalGateResult(r GateResult) ([]byte, error) {
	raw, err := json.Marshal(r.Value)
	if err != nil {
		return nil, fmt.Errorf("taskengine: gate result (%s) is not JSON-serializable: %w", r.Type.String(), err)
	}
	return json.Marshal(checkpointVarWire{Type: r.Type.String(), Value: raw})
}

func UnmarshalGateResult(raw []byte) (GateResult, error) {
	var wire checkpointVarWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return GateResult{}, fmt.Errorf("taskengine: gate result payload is not valid JSON: %w", err)
	}
	dt, err := DataTypeFromString(wire.Type)
	if err != nil {
		return GateResult{}, fmt.Errorf("taskengine: gate result: %w", err)
	}
	value, err := decodeCheckpointVar(dt, wire.Value)
	if err != nil {
		return GateResult{}, fmt.Errorf("taskengine: gate result: %w", err)
	}
	return GateResult{Value: value, Type: dt}, nil
}

type gateResultRecorderContextKey struct{}

type GateResultRecorder func(ctx context.Context, approvalID string, result GateResult) error

func WithGateResultRecorder(ctx context.Context, rec GateResultRecorder) context.Context {
	if rec == nil {
		return ctx
	}
	return context.WithValue(ctx, gateResultRecorderContextKey{}, rec)
}

func GateResultRecorderFromContext(ctx context.Context) GateResultRecorder {
	rec, _ := ctx.Value(gateResultRecorderContextKey{}).(GateResultRecorder)
	return rec
}

var ErrGateRecordFailed = errors.New("taskengine: gated call executed but recording its result failed")

type recordedGateResultsContextKey struct{}

type GateResultStore struct {
	mu sync.Mutex
	m  map[string]GateResult
}

func NewGateResultStore() *GateResultStore {
	return &GateResultStore{m: map[string]GateResult{}}
}

func (s *GateResultStore) Set(approvalID string, r GateResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[approvalID] = r
}

func (s *GateResultStore) Get(approvalID string) (GateResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.m[approvalID]
	return r, ok
}

func WithGateResultStore(ctx context.Context, store *GateResultStore) context.Context {
	if store == nil {
		return ctx
	}
	return context.WithValue(ctx, recordedGateResultsContextKey{}, store)
}

func RecordedGateResultFromContext(ctx context.Context, approvalID string) (GateResult, bool) {
	if approvalID == "" {
		return GateResult{}, false
	}
	store, _ := ctx.Value(recordedGateResultsContextKey{}).(*GateResultStore)
	if store == nil {
		return GateResult{}, false
	}
	return store.Get(approvalID)
}

type attentionAnswersContextKey struct{}

type AttentionAnswer struct {
	Answered bool
	Text     string
}

func WithAttentionAnswers(ctx context.Context, answers map[string]AttentionAnswer) context.Context {
	if len(answers) == 0 {
		return ctx
	}
	copied := make(map[string]AttentionAnswer, len(answers))
	for k, v := range answers {
		copied[k] = v
	}
	return context.WithValue(ctx, attentionAnswersContextKey{}, copied)
}

func AttentionAnswerFromContext(ctx context.Context, askID string) (ans AttentionAnswer, ok bool) {
	if askID == "" {
		return AttentionAnswer{}, false
	}
	answers, _ := ctx.Value(attentionAnswersContextKey{}).(map[string]AttentionAnswer)
	if answers == nil {
		return AttentionAnswer{}, false
	}
	ans, ok = answers[askID]
	return ans, ok
}

type suspendableToolCallContextKey struct{}

func WithSuspendableToolCall(ctx context.Context) context.Context {
	return context.WithValue(ctx, suspendableToolCallContextKey{}, true)
}

func ToolCallSuspendable(ctx context.Context) bool {
	ok, _ := ctx.Value(suspendableToolCallContextKey{}).(bool)
	return ok
}

type detachedAsksContextKey struct{}

func WithDetachedAsks(ctx context.Context) context.Context {
	return context.WithValue(ctx, detachedAsksContextKey{}, true)
}

func AsksDetached(ctx context.Context) bool {
	ok, _ := ctx.Value(detachedAsksContextKey{}).(bool)
	return ok
}

type resumeCheckpointContextKey struct{}

func WithResumeCheckpoint(ctx context.Context, cp *Checkpoint) context.Context {
	if cp == nil {
		return ctx
	}
	return context.WithValue(ctx, resumeCheckpointContextKey{}, cp)
}

func resumeCheckpointFromContext(ctx context.Context) *Checkpoint {
	cp, _ := ctx.Value(resumeCheckpointContextKey{}).(*Checkpoint)
	return cp
}

func unansweredToolCalls(msgs []Message) []PendingToolCall {
	idx, answered := resumableToolCallBatch(msgs)
	if idx < 0 {
		return nil
	}
	var out []PendingToolCall
	for _, tc := range msgs[idx].CallTools {
		if tc.ID == "" || answered[tc.ID] {
			continue
		}
		out = append(out, PendingToolCall{
			CallID:    tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	return out
}
