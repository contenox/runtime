package acpsvc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/contenox/runtime/libacp"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

const (
	oracleTestPolicyAllowing  = `{"default_action":"approve","rules":[],"attention":{"allowAgentApprovals":true,"maxAgentApprovals":10}}`
	oracleTestPolicyHumanOnly = `{"default_action":"approve","rules":[]}`
)

type agentBoundedResponder interface {
	RespondAsAgentBounded(ctx context.Context, askID, agentName string, approved bool, guidance string, max int) error
}

type fakeOracle struct {
	svc     hitlservice.Service
	respond bool
	approve bool
	max     int

	mu   sync.Mutex
	asks []hitlservice.Adjudication
	errs []error
}

func (f *fakeOracle) Adjudicate(ctx context.Context, ask hitlservice.Adjudication) {
	f.mu.Lock()
	f.asks = append(f.asks, ask)
	f.mu.Unlock()
	if !f.respond || !ask.AgentReview {
		return
	}
	err := f.svc.(agentBoundedResponder).RespondAsAgentBounded(ctx, ask.AskID, decidedByOracle, f.approve, "", f.max)
	f.mu.Lock()
	f.errs = append(f.errs, err)
	f.mu.Unlock()
}

func (f *fakeOracle) seen() []hitlservice.Adjudication {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]hitlservice.Adjudication(nil), f.asks...)
}

func permissionRequestCount(c *loopbackClient) int {
	c.permMu.Lock()
	defer c.permMu.Unlock()
	return len(c.permReqs)
}

func configOptionByID(opts []libacp.SessionConfigOption, id string) (libacp.SessionConfigOption, bool) {
	for _, o := range opts {
		if o.ID == id {
			return o, true
		}
	}
	return libacp.SessionConfigOption{}, false
}

type oracleHarness struct {
	*loopbackHarness
	svc     hitlservice.Service
	oracle  *fakeOracle
	store   runtimetypes.Store
	session libacp.SessionID
	options []libacp.SessionConfigOption
	askCtx  context.Context
}

func newOracleHarness(t *testing.T, policy string, oracle *fakeOracle) *oracleHarness {
	t.Helper()
	h := newLoopbackHarness(t)
	ctx := context.Background()

	policyDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(policyDir, "hitl-policy-default.json"), []byte(policy), 0o644))
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "approvals.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	store := runtimetypes.New(db.WithoutTransaction())
	svc := hitlservice.NewWithDefaultPolicy(hitlservice.NewFSPolicySource(policyDir), runtimetypes.LocalTenantID, store, libtracker.NoopTracker{}, "")
	oracle.svc = svc
	hitlservice.SetAdjudicator(svc, oracle)
	h.tr.deps.Approvals = svc
	h.tr.deps.OracleMounted = true

	_, err = h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)
	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []libacp.McpServer{}})
	require.NoError(t, err)
	h.lc.drain(t, 1)

	h.tr.sessionMu.Lock()
	internalID := h.tr.sessions[newResp.SessionID].InternalSessionID
	h.tr.sessionMu.Unlock()

	return &oracleHarness{
		loopbackHarness: h,
		svc:             svc,
		oracle:          oracle,
		store:           store,
		session:         newResp.SessionID,
		options:         newResp.ConfigOptions,
		askCtx:          context.WithValue(ctx, runtimetypes.SessionIDContextKey, internalID),
	}
}

func npmInstallAsk() hitlservice.ApprovalRequest {
	return hitlservice.ApprovalRequest{
		ToolCallID: "npm-install",
		ToolsName:  "local_shell",
		ToolName:   "local_shell",
		Args:       map[string]any{"command": "npm install"},
		PolicyName: "hitl-policy-default.json",
	}
}

func (h *oracleHarness) approvedRows(t *testing.T) []*runtimetypes.HITLApproval {
	t.Helper()
	rows, err := h.store.ListHITLApprovals(context.Background(), runtimetypes.HITLApprovalApproved, nil, 20)
	require.NoError(t, err)
	return rows
}

func TestLoopback_OracleAnswersFirstAndTheCardIsWithdrawn(t *testing.T) {
	h := newOracleHarness(t, oracleTestPolicyAllowing, &fakeOracle{respond: true, approve: true, max: 1})

	option, ok := configOptionByID(h.options, configIDOracle)
	require.True(t, ok, "a session whose envelope allows agent approvals offers the Oracle switch")
	require.Equal(t, libacp.SessionConfigOptionTypeBoolean, option.Type)
	require.Equal(t, "true", option.CurrentValue, "on by default")

	hold := make(chan struct{})
	h.lc.holdPermissions(hold)
	allowed, err := h.router.AskApproval(h.askCtx, npmInstallAsk())
	close(hold)
	require.NoError(t, err)
	require.True(t, allowed)
	require.Equal(t, 1, permissionRequestCount(h.lc), "the card went out at once and was withdrawn")

	seen := h.oracle.seen()
	require.Len(t, seen, 1)
	require.True(t, seen[0].AgentReview)
	require.Equal(t, "local_shell", seen[0].ToolsName)

	notes := h.lc.drain(t, 1)
	require.Equal(t, "npm-install", notes[0].Update.ToolCallID)
	require.Len(t, notes[0].Update.ToolContent, 1)
	require.Equal(t, oracleApprovedNote, notes[0].Update.ToolContent[0].Content.Text)
	var meta map[string]approvalMeta
	require.NoError(t, json.Unmarshal(notes[0].Update.Meta, &meta))
	require.True(t, meta[approvalMetaKey].Approved)
	require.Contains(t, meta[approvalMetaKey].DecidedBy, decidedByOracle)
	require.Equal(t, oracleApprovedNote, meta[approvalMetaKey].Note)

	rows := h.approvedRows(t)
	require.Len(t, rows, 1)
	require.Contains(t, string(rows[0].Resolution), decidedByOracle)

	h.lc.holdPermissions(nil)
	allowed, err = h.router.AskApproval(h.askCtx, npmInstallAsk())
	require.NoError(t, err)
	require.True(t, allowed, "the loopback client answers Allow")
	require.Equal(t, 2, permissionRequestCount(h.lc))
	rows = h.approvedRows(t)
	require.Len(t, rows, 2)
	byOracle := 0
	for _, row := range rows {
		if string(row.Resolution) != "" && containsDecidedBy(row, decidedByOracle) {
			byOracle++
		}
	}
	require.Equal(t, 1, byOracle, "past the session's bound Oracle can no longer approve; the human did")
}

func containsDecidedBy(row *runtimetypes.HITLApproval, by string) bool {
	return len(row.Resolution) > 0 && (string(row.Resolution) != "") && containsFold(string(row.Resolution), by)
}

func containsFold(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexFold(s, sub) >= 0)
}

func indexFold(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			a, b := s[i+j], sub[j]
			if 'A' <= a && a <= 'Z' {
				a += 'a' - 'A'
			}
			if 'A' <= b && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func TestLoopback_HumanAnswersFirstWhileOracleIsOffered(t *testing.T) {
	h := newOracleHarness(t, oracleTestPolicyAllowing, &fakeOracle{respond: false})

	allowed, err := h.router.AskApproval(h.askCtx, npmInstallAsk())
	require.NoError(t, err)
	require.True(t, allowed)
	require.Equal(t, 1, permissionRequestCount(h.lc))

	seen := h.oracle.seen()
	require.Len(t, seen, 1)
	require.True(t, seen[0].AgentReview, "Oracle was offered the ask")

	rows := h.approvedRows(t)
	require.Len(t, rows, 1)
	require.Contains(t, string(rows[0].Resolution), decidedByACPClient)
}

func TestLoopback_OracleSwitchedOffIsNeverOfferedTheAsk(t *testing.T) {
	h := newOracleHarness(t, oracleTestPolicyAllowing, &fakeOracle{respond: true, approve: true, max: 10})

	setResp, err := h.client.SetSessionConfigOption(context.Background(), libacp.SetSessionConfigOptionRequest{
		SessionID: h.session,
		ConfigID:  configIDOracle,
		Value:     libacp.BoolConfigValue(false),
	})
	require.NoError(t, err)
	option, ok := configOptionByID(setResp.ConfigOptions, configIDOracle)
	require.True(t, ok)
	require.Equal(t, "false", option.CurrentValue)

	allowed, err := h.router.AskApproval(h.askCtx, npmInstallAsk())
	require.NoError(t, err)
	require.True(t, allowed)
	require.Equal(t, 1, permissionRequestCount(h.lc))

	seen := h.oracle.seen()
	require.Len(t, seen, 1)
	require.False(t, seen[0].AgentReview, "a switched-off session opens the ask for humans only")

	rows := h.approvedRows(t)
	require.Len(t, rows, 1)
	require.Contains(t, string(rows[0].Resolution), decidedByACPClient)
}

func TestLoopback_OracleIsNotOfferedUnderAHumanOnlyEnvelope(t *testing.T) {
	h := newOracleHarness(t, oracleTestPolicyHumanOnly, &fakeOracle{respond: true, approve: true, max: 10})

	_, ok := configOptionByID(h.options, configIDOracle)
	require.False(t, ok, "no switch when the envelope does not allow agent approvals")

	allowed, err := h.router.AskApproval(h.askCtx, npmInstallAsk())
	require.NoError(t, err)
	require.True(t, allowed)
	require.Equal(t, 1, permissionRequestCount(h.lc))

	seen := h.oracle.seen()
	require.Len(t, seen, 1)
	require.False(t, seen[0].AgentReview)
}
