package agenthost_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/libacp/acpexec"
	"github.com/contenox/runtime/runtime/agenthost"
	"github.com/stretchr/testify/require"
)

const hostTestyBinEnv = "ACP_TESTY_BIN"

func testyBinFromEnv(t *testing.T) string {
	t.Helper()
	bin := os.Getenv(hostTestyBinEnv)
	if bin == "" {
		t.Skipf("skipping: set %s to a built testy binary to run (see `make acp-client-e2e`)", hostTestyBinEnv)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("%s=%q is not accessible: %v", hostTestyBinEnv, bin, err)
	}
	return bin
}

func testyCommandPrompt(t *testing.T, v any) []libacp.ContentBlock {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return []libacp.ContentBlock{libacp.NewTextContent(string(raw))}
}

func TestHostE2E_Testy_EchoRoundTrip(t *testing.T) {
	requireSandboxable(t)
	testyBin := testyBinFromEnv(t)
	ctx, agent := registerAgent(t, "testy-echo", testyBin)

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	const message = "the quick brown fox jumps over the lazy dog"
	var stderr acpexec.LockedBuffer
	harness := &agenthost.RecordingHarness{}
	res, err := agenthost.DriveTurn(ctx, agent, harness, agenthost.TurnRequest{
		Cwd:       t.TempDir(),
		Prompt:    testyCommandPrompt(t, map[string]any{"command": "echo", "message": message}),
		Stderr:    &stderr,
		KillGrace: 500 * time.Millisecond,
	})
	require.NoError(t, err, "testy stderr:\n%s", stderr.String())

	require.Equal(t, libacp.ProtocolVersion, res.Initialize.ProtocolVersion, "testy stderr:\n%s", stderr.String())
	require.NotEmpty(t, res.SessionID, "testy stderr:\n%s", stderr.String())
	require.Equal(t, libacp.StopReasonEndTurn, res.StopReason, "testy stderr:\n%s", stderr.String())

	require.Equal(t, message, harness.MessageText(), "testy stderr:\n%s", stderr.String())
	updates := harness.Updates()
	require.Len(t, updates, 1, "testy stderr:\n%s", stderr.String())
	require.Equal(t, res.SessionID, updates[0].SessionID)
	require.Equal(t, libacp.SessionUpdateAgentMessageChunk, updates[0].Update.SessionUpdate)

	tracker := &libacp.TurnTracker{}
	for _, n := range updates {
		tracker.Observe(n)
	}
	require.NoError(t, tracker.Err(res.StopReason), "testy stderr:\n%s", stderr.String())
}

func TestHostE2E_Testy_Greet(t *testing.T) {
	requireSandboxable(t)
	testyBin := testyBinFromEnv(t)
	ctx, agent := registerAgent(t, "testy-greet", testyBin)

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var stderr acpexec.LockedBuffer
	harness := &agenthost.RecordingHarness{}
	res, err := agenthost.DriveTurn(ctx, agent, harness, agenthost.TurnRequest{
		Cwd:       t.TempDir(),
		Prompt:    testyCommandPrompt(t, map[string]any{"command": "greet"}),
		Stderr:    &stderr,
		KillGrace: 500 * time.Millisecond,
	})
	require.NoError(t, err, "testy stderr:\n%s", stderr.String())

	require.Equal(t, libacp.StopReasonEndTurn, res.StopReason, "testy stderr:\n%s", stderr.String())
	require.Equal(t, "Hello, world!", harness.MessageText(), "testy stderr:\n%s", stderr.String())
}
