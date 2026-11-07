package contenoxcli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/agentservice"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/oracletools"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

type countingAgent struct {
	calls int
}

func (a *countingAgent) Capabilities(context.Context) (*agentservice.AgentCapabilities, error) {
	return nil, nil
}
func (a *countingAgent) SessionNew(context.Context, string) (string, error) { return "", nil }
func (a *countingAgent) SessionList(context.Context) ([]*agentservice.SessionInfo, error) {
	return nil, nil
}
func (a *countingAgent) SessionLoad(context.Context, string) (string, []taskengine.Message, error) {
	return "", nil, nil
}
func (a *countingAgent) SessionResume(context.Context, string) (string, error) { return "", nil }
func (a *countingAgent) SessionDelete(context.Context, string) error           { return nil }
func (a *countingAgent) SessionEnsureDefault(context.Context) (string, error)  { return "", nil }
func (a *countingAgent) Prompt(context.Context, agentservice.PromptRequest) (*agentservice.PromptResponse, error) {
	a.calls++
	return &agentservice.PromptResponse{}, nil
}

var _ agentservice.Agent = (*countingAgent)(nil)

const (
	chatOraclePolicyAllowing  = `{"default_action":"approve","rules":[],"attention":{"allowAgentApprovals":true,"maxAgentApprovals":1}}`
	chatOraclePolicyHumanOnly = `{"default_action":"approve","rules":[]}`
)

func chatOracleService(t *testing.T, policy string) (context.Context, runtimetypes.Store, hitlservice.Service) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hitl-policy-chat.json"), []byte(policy), 0o644))
	ctx, _, store := setupSQLiteStore(t)
	svc := hitlservice.NewWithDefaultPolicy(hitlservice.NewFSPolicySource(dir), runtimetypes.LocalTenantID, store, libtracker.NoopTracker{}, "")
	return ctx, store, svc
}

func openChatAsk(t *testing.T, ctx context.Context, svc hitlservice.Service) *hitlservice.OpenAsk {
	t.Helper()
	ask, err := svc.OpenApproval(ctx, hitlservice.ApprovalRequest{
		ToolCallID:  "call-npm-install",
		ToolsName:   "local_shell",
		ToolName:    "local_shell",
		Args:        map[string]any{"command": "npm install"},
		PolicyName:  "hitl-policy-chat.json",
		SessionID:   "session-1",
		Intent:      "Check git status and read package.json, then run npm install",
		AgentReview: true,
	}, taskengine.NoopTaskEventSink{})
	require.NoError(t, err)
	t.Cleanup(ask.Close)
	return ask
}

func TestUnit_OracleDecidesAChatAskWithinTheSessionBound(t *testing.T) {
	ctx, store, svc := chatOracleService(t, chatOraclePolicyAllowing)
	var out bytes.Buffer
	r := oracleResolver{hitl: svc, store: store, out: &out}

	first := openChatAsk(t, ctx, svc)
	require.NoError(t, r.Decide(ctx, first.ID, true, ""))
	verdict, err := first.Wait(ctx)
	require.NoError(t, err)
	require.True(t, verdict.Approved)
	require.Contains(t, verdict.DecidedBy, oracleAgentName)
	row, err := store.GetHITLApproval(ctx, first.ID)
	require.NoError(t, err)
	require.Equal(t, runtimetypes.HITLApprovalApproved, row.State)
	require.Contains(t, string(row.Resolution), oracleAgentName)

	second := openChatAsk(t, ctx, svc)
	err = r.Decide(ctx, second.ID, true, "")
	var refused *oracletools.RefusedError
	require.True(t, errors.As(err, &refused), "past the session's bound the verdict is refused, not recorded: %v", err)
	require.Contains(t, refused.Reason, "bound (1) is spent")
	row, err = store.GetHITLApproval(ctx, second.ID)
	require.NoError(t, err)
	require.Equal(t, runtimetypes.HITLApprovalPending, row.State, "the ask stays with a human")
}

func TestUnit_OracleRefusesAChatAskUnderAHumanOnlyEnvelope(t *testing.T) {
	ctx, store, svc := chatOracleService(t, chatOraclePolicyHumanOnly)
	r := oracleResolver{hitl: svc, store: store}

	ask := openChatAsk(t, ctx, svc)
	err := r.Decide(ctx, ask.ID, true, "")
	var refused *oracletools.RefusedError
	require.True(t, errors.As(err, &refused))
	require.Contains(t, refused.Reason, "does not allow agent-decided tool calls")
}

func TestUnit_OracleAdjudicatesChatAsksOnlyWhenReviewIsOn(t *testing.T) {
	agent := &countingAgent{}
	d := &oracleDriver{agent: agent, chain: &taskengine.TaskChainDefinition{}}
	base := hitlservice.Adjudication{AskID: "ask-1", Kind: hitlservice.AskKindPermission, SessionID: "session-1", ToolsName: "local_shell", ToolName: "local_shell"}

	d.Adjudicate(context.Background(), base)
	require.Zero(t, agent.calls, "a chat ask without review on never reaches the chain")

	reviewed := base
	reviewed.AgentReview = true
	reviewed.Intent = "run npm install"
	d.Adjudicate(context.Background(), reviewed)
	require.Equal(t, 1, agent.calls, "a chat ask with review on runs the chain even though mission approvals are off")

	mission := base
	mission.MissionID = "mission-1"
	d.Adjudicate(context.Background(), mission)
	require.Equal(t, 1, agent.calls, "a mission's tool call still needs oracle-approves-tool-calls")
}
