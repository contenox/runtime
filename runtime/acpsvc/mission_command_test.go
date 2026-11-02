package acpsvc

import (
	"context"
	"encoding/json"
	"github.com/contenox/runtime/runtime/hitlservice"
	"os"
	"path/filepath"
	"strings"
	"testing"

	libacp "github.com/contenox/runtime/libacp"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/fleetservice"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func registerChainAgent(t *testing.T, db libdb.DBManager, name string, enabled bool, tools string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".json")
	body := `{"id":"` + name + `","tasks":[{"id":"loop","handler":"chat_completion","execute_config":{"tools":` + tools + `}}]}`
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	agent := &runtimetypes.Agent{Name: name, Enabled: enabled}
	require.NoError(t, agent.SetChainConfig(runtimetypes.ChainConfig{Path: path}))
	require.NoError(t, runtimetypes.New(db.WithoutTransaction()).CreateAgent(context.Background(), agent))
}

func missionPolicyDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		require.NoError(t, os.WriteFile(filepath.Join(dir, n), []byte(`{}`), 0o600))
	}
	return dir
}

func missionCommandTransport(t *testing.T, disp *fakeDispatcher) (*Transport, libdb.DBManager) {
	t.Helper()
	tr, db := newMissionTestTransport(t, disp, &fakeResolver{known: map[string]bool{"agent-reviewer": true}})
	registerChainAgent(t, db, "agent-reviewer", true, `["mission","local_fs","native-git"]`)
	registerChainAgent(t, db, "agent-planner", true, `["mission"]`)
	registerChainAgent(t, db, "agent-shell", true, `["local_shell"]`)
	registerChainAgent(t, db, "agent-off", false, `["mission"]`)
	policies := hitlservice.NewFSPolicySource(
		missionPolicyDir(t, "hitl-policy-read_only.json", "hitl-policy-default.json", "notes.json"),
		missionPolicyDir(t, "hitl-policy-read_only.json", "hitl-policy-strict.json"),
	)
	tr.deps.PolicyCatalog = func(string) []string { return hitlservice.ChoiceFiles(hitlservice.Choices(policies)) }
	return tr, db
}

func commandMeta(t *testing.T, cmds []libacp.AvailableCommand, name string) map[string]map[string]any {
	t.Helper()
	for _, c := range cmds {
		if c.Name != name {
			continue
		}
		var meta map[string]map[string]map[string]any
		require.NoError(t, json.Unmarshal(c.Meta, &meta), "command %s meta %s", name, string(c.Meta))
		got, ok := meta["contenox.argumentCompletions"]
		require.True(t, ok, "command %s meta lacks contenox.argumentCompletions: %s", name, string(c.Meta))
		return got
	}
	t.Fatalf("command %s not advertised", name)
	return nil
}

func values(t *testing.T, completion map[string]any) []string {
	t.Helper()
	raw, ok := completion["values"].([]any)
	require.True(t, ok, "values missing: %v", completion)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		out = append(out, v.(string))
	}
	return out
}

func TestUnit_MissionCommands_AdvertiseArgumentCompletions(t *testing.T) {
	tr, _ := missionCommandTransport(t, &fakeDispatcher{})
	cmds := tr.acpCommands()

	agents := []string{"agent-planner", "agent-reviewer"}
	envelopes := []string{"hitl-policy-default.json", "hitl-policy-read_only.json", "hitl-policy-strict.json"}

	mission := commandMeta(t, cmds, "mission")
	require.Len(t, mission, 2)
	require.Equal(t, agents, values(t, mission[""]))
	require.NotEmpty(t, mission[""]["hint"])
	require.Equal(t, envelopes, values(t, mission["--policy"]))
	require.NotEmpty(t, mission["--policy"]["hint"])

	agent := commandMeta(t, cmds, "mission-agent")
	require.Len(t, agent, 1)
	require.Equal(t, agents, values(t, agent[""]))
	require.NotEmpty(t, agent[""]["hint"])

	envelope := commandMeta(t, cmds, "mission-envelope")
	require.Len(t, envelope, 1)
	require.Equal(t, envelopes, values(t, envelope[""]))
}

func TestUnit_ParseMissionFlags(t *testing.T) {
	f, rest, err := parseMissionFlags("--policy hitl-policy-strict.json agent-reviewer look at it")
	require.NoError(t, err)
	require.Equal(t, "hitl-policy-strict.json", f.policy)
	require.Equal(t, "agent-reviewer look at it", rest)

	f, rest, err = parseMissionFlags("--policy=strict review")
	require.NoError(t, err)
	require.Equal(t, "strict", f.policy)
	require.Equal(t, "review", rest)

	f, rest, err = parseMissionFlags("review the diff")
	require.NoError(t, err)
	require.Equal(t, "", f.policy)
	require.Equal(t, "review the diff", rest)

	_, _, err = parseMissionFlags("--policy")
	require.ErrorContains(t, err, "--policy needs an envelope name")

	_, _, err = parseMissionFlags("--force do it")
	require.ErrorContains(t, err, "unknown /mission flag")
}

func TestUnit_HandleMission_NamedAgentWithoutIntentIsRefused(t *testing.T) {
	disp := &fakeDispatcher{}
	tr, db := missionCommandTransport(t, disp)
	setMissionConfig(t, db, "default-mission-agent", "agent-reviewer")
	setMissionConfig(t, db, "default-mission-policy", "hitl-policy-read_only.json")

	_, err := tr.handleMission(context.Background(), &sessionEntry{InternalSessionID: "s"}, "agent-reviewer")
	require.ErrorContains(t, err, "describe what agent-reviewer should do")
	require.ErrorContains(t, err, "No mission was dispatched")
	require.Empty(t, disp.got.AgentName)
}

func TestUnit_HandleMission_PolicyFlagAndSessionCwd(t *testing.T) {
	disp := &fakeDispatcher{result: fleetservice.DispatchResult{MissionID: "m"}}
	tr, db := missionCommandTransport(t, disp)
	setMissionConfig(t, db, "default-mission-agent", "agent-reviewer")
	setMissionConfig(t, db, "default-mission-policy", "hitl-policy-default.json")

	cwd := t.TempDir()
	out, err := tr.handleMission(context.Background(), &sessionEntry{InternalSessionID: "s", Cwd: cwd}, "--policy read_only agent-reviewer review the diff")
	require.NoError(t, err)
	require.Equal(t, "hitl-policy-read_only.json", disp.got.HITLPolicyName)
	require.Equal(t, "agent-reviewer", disp.got.AgentName)
	require.Equal(t, "review the diff", disp.got.Intent)
	require.Equal(t, cwd, disp.got.Cwd)
	require.Contains(t, out, "hitl-policy-read_only.json")

	disp.got = fleetservice.DispatchRequest{}
	_, err = tr.handleMission(context.Background(), &sessionEntry{InternalSessionID: "s"}, "--policy nope review")
	require.ErrorContains(t, err, `unknown mission envelope "nope"`)
	require.ErrorContains(t, err, "hitl-policy-read_only.json")
	require.Empty(t, disp.got.AgentName)
}

func requireOrdered(t *testing.T, msg string, parts ...string) {
	t.Helper()
	last := -1
	for _, p := range parts {
		i := strings.Index(msg, p)
		require.Greaterf(t, i, last, "%q must appear after the previous hint in %q", p, msg)
		last = i
	}
}

func TestUnit_HandleMission_NoDefaultAgentTeachesInOrder(t *testing.T) {
	tr, db := missionCommandTransport(t, &fakeDispatcher{})
	setMissionConfig(t, db, "default-mission-policy", "hitl-policy-read_only.json")
	_, err := tr.handleMission(context.Background(), &sessionEntry{InternalSessionID: "s"}, "review the diff")
	require.Error(t, err)
	requireOrdered(t, err.Error(), "/mission-agent agent-planner", "Available: agent-planner, agent-reviewer", "Settings → Missions", "contenox-runtime config set default-mission-agent")
}

func TestUnit_HandleMission_NoEnvelopeTeachesInOrder(t *testing.T) {
	tr, db := missionCommandTransport(t, &fakeDispatcher{})
	setMissionConfig(t, db, "default-mission-agent", "agent-reviewer")
	_, err := tr.handleMission(context.Background(), &sessionEntry{InternalSessionID: "s"}, "review the diff")
	require.Error(t, err)
	requireOrdered(t, err.Error(), "/mission-envelope hitl-policy-default.json", "Available: hitl-policy-default.json", "Settings → Missions", "contenox-runtime config set default-mission-policy")
}

func TestUnit_HandleMissionAgent_ShowSetAndRefuse(t *testing.T) {
	tr, db := missionCommandTransport(t, &fakeDispatcher{})
	store := runtimetypes.New(db.WithoutTransaction())

	out, err := tr.handleMissionAgent(context.Background(), "")
	require.NoError(t, err)
	require.Contains(t, out, "Default mission agent: (none)")
	require.Contains(t, out, "agent-planner, agent-reviewer")

	out, err = tr.handleMissionAgent(context.Background(), "agent-planner")
	require.NoError(t, err)
	require.Contains(t, out, "agent-planner")
	require.Equal(t, "agent-planner", clikv.Read(context.Background(), store, "default-mission-agent"))

	for _, bad := range []string{"agent-shell", "agent-off", "ghost"} {
		_, err = tr.handleMissionAgent(context.Background(), bad)
		require.Error(t, err, bad)
		requireOrdered(t, err.Error(), "/mission-agent agent-planner", "Available:", "Settings → Missions", "contenox-runtime config set default-mission-agent")
	}
	require.Equal(t, "agent-planner", clikv.Read(context.Background(), store, "default-mission-agent"))
}

func TestUnit_HandleMissionEnvelope_ShowSetAndRefuse(t *testing.T) {
	tr, db := missionCommandTransport(t, &fakeDispatcher{})
	store := runtimetypes.New(db.WithoutTransaction())

	out, err := tr.handleMissionEnvelope(context.Background(), nil, "")
	require.NoError(t, err)
	require.Contains(t, out, "Default mission envelope: (none)")

	_, err = tr.handleMissionEnvelope(context.Background(), nil, "read_only")
	require.NoError(t, err)
	require.Equal(t, "hitl-policy-read_only.json", clikv.Read(context.Background(), store, "default-mission-policy"))

	_, err = tr.handleMissionEnvelope(context.Background(), nil, "notes.json")
	require.Error(t, err)
	requireOrdered(t, err.Error(), "/mission-envelope hitl-policy-default.json", "Available:", "Settings → Missions", "contenox-runtime config set default-mission-policy")
	require.Equal(t, "hitl-policy-read_only.json", clikv.Read(context.Background(), store, "default-mission-policy"))
}

func TestUnit_MissionDefaultCommandsUnavailableWithoutFleet(t *testing.T) {
	tr, _ := newMissionTestTransport(t, nil, nil)
	_, err := tr.handleMissionAgent(context.Background(), "agent-reviewer")
	require.ErrorContains(t, err, "unavailable")
	_, err = tr.handleMissionEnvelope(context.Background(), nil, "")
	require.ErrorContains(t, err, "unavailable")

	name, args, ok := parseCommand("/mission-agent agent-reviewer")
	require.True(t, ok)
	require.Equal(t, "mission-agent", name)
	require.Equal(t, "agent-reviewer", args)
}
