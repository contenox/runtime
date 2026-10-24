package acpsvc

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	libacp "github.com/contenox/runtime/libacp"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/fleetservice"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

type fakeDispatcher struct {
	got    fleetservice.DispatchRequest
	result fleetservice.DispatchResult
	err    error
}

func (f *fakeDispatcher) Dispatch(_ context.Context, req fleetservice.DispatchRequest) (fleetservice.DispatchResult, error) {
	f.got = req
	if f.err != nil {
		return fleetservice.DispatchResult{}, f.err
	}
	return f.result, nil
}

type fakeResolver struct {
	known map[string]bool
}

func (f *fakeResolver) GetByName(_ context.Context, name string) (*runtimetypes.Agent, error) {
	if f.known[name] {
		return &runtimetypes.Agent{Name: name}, nil
	}
	return nil, libdb.ErrNotFound
}

func newMissionTestTransport(t *testing.T, disp *fakeDispatcher, res *fakeResolver) (*Transport, libdb.DBManager) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mission-acp.db")
	db, err := libdb.NewSQLiteDBManager(ctx, path, runtimetypes.SchemaSQLite)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tr := &Transport{deps: Deps{DB: db}}
	if disp != nil {
		tr.deps.Fleet = disp
	}
	if res != nil {
		tr.deps.Agents = res
	}
	return tr, db
}

func setMissionConfig(t *testing.T, db libdb.DBManager, key, value string) {
	t.Helper()
	store := runtimetypes.New(db.WithoutTransaction())
	if err := clikv.WriteConfig(context.Background(), store, "", key, value); err != nil {
		t.Fatalf("seed config %s: %v", key, err)
	}
}

func TestUnit_HandleMission_DefaultAgentForm(t *testing.T) {
	disp := &fakeDispatcher{result: fleetservice.DispatchResult{InstanceID: "inst-1", SessionID: "sess-1", MissionID: "m-1"}}
	tr, db := newMissionTestTransport(t, disp, &fakeResolver{})
	setMissionConfig(t, db, "default-mission-agent", "reviewer")
	setMissionConfig(t, db, "default-mission-policy", "hitl-policy-strict.json")

	sess := &sessionEntry{InternalSessionID: "cnx-parent-1"}
	out, err := tr.handleMission(context.Background(), sess, "triage the failing CI run")
	if err != nil {
		t.Fatalf("handleMission: %v", err)
	}

	if disp.got.AgentName != "reviewer" {
		t.Fatalf("agent = %q, want reviewer", disp.got.AgentName)
	}
	if disp.got.Intent != "triage the failing CI run" {
		t.Fatalf("intent = %q", disp.got.Intent)
	}
	if disp.got.HITLPolicyName != "hitl-policy-strict.json" {
		t.Fatalf("policy = %q", disp.got.HITLPolicyName)
	}
	if disp.got.ParentSessionID != "cnx-parent-1" {
		t.Fatalf("parent session = %q, want cnx-parent-1", disp.got.ParentSessionID)
	}
	for _, want := range []string{"default mission agent", "reviewer", "triage the failing CI run", "m-1", "inst-1", "sess-1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("confirmation %q missing %q", out, want)
		}
	}
}

func TestUnit_HandleMission_NamedAgentForm(t *testing.T) {
	disp := &fakeDispatcher{result: fleetservice.DispatchResult{MissionID: "m-2"}}
	tr, db := newMissionTestTransport(t, disp, &fakeResolver{known: map[string]bool{"planner": true}})
	setMissionConfig(t, db, "default-mission-agent", "reviewer")
	setMissionConfig(t, db, "default-mission-policy", "envelope.json")

	sess := &sessionEntry{InternalSessionID: "cnx-parent-2"}
	out, err := tr.handleMission(context.Background(), sess, "planner draft the release notes")
	if err != nil {
		t.Fatalf("handleMission: %v", err)
	}

	if disp.got.AgentName != "planner" {
		t.Fatalf("agent = %q, want planner", disp.got.AgentName)
	}
	if disp.got.Intent != "draft the release notes" {
		t.Fatalf("intent = %q, want 'draft the release notes'", disp.got.Intent)
	}
	if !strings.Contains(out, "named agent") || !strings.Contains(out, "planner") {
		t.Fatalf("confirmation must name the chosen agent: %q", out)
	}
}

func TestUnit_HandleMission_UnknownFirstTokenFallsBackToDefault(t *testing.T) {
	disp := &fakeDispatcher{result: fleetservice.DispatchResult{MissionID: "m-3"}}
	tr, db := newMissionTestTransport(t, disp, &fakeResolver{known: map[string]bool{"planner": true}})
	setMissionConfig(t, db, "default-mission-agent", "reviewer")
	setMissionConfig(t, db, "default-mission-policy", "envelope.json")

	sess := &sessionEntry{InternalSessionID: "cnx-3"}
	if _, err := tr.handleMission(context.Background(), sess, "summarise today's commits"); err != nil {
		t.Fatalf("handleMission: %v", err)
	}
	if disp.got.AgentName != "reviewer" {
		t.Fatalf("agent = %q, want reviewer (default)", disp.got.AgentName)
	}
	if disp.got.Intent != "summarise today's commits" {
		t.Fatalf("intent = %q", disp.got.Intent)
	}
}

func TestUnit_HandleMission_InProcessConfirmationNamesThisSession(t *testing.T) {
	disp := &fakeDispatcher{result: fleetservice.DispatchResult{InstanceID: "i", SessionID: "s", MissionID: "m"}}
	tr, db := newMissionTestTransport(t, disp, &fakeResolver{})
	setMissionConfig(t, db, "default-mission-agent", "reviewer")
	setMissionConfig(t, db, "default-mission-policy", "envelope.json")

	out, err := tr.handleMission(context.Background(), &sessionEntry{InternalSessionID: "cnx-1"}, "go")
	if err != nil {
		t.Fatalf("handleMission: %v", err)
	}
	if !strings.Contains(out, "live in this session") {
		t.Fatalf("in-process confirmation must say reports arrive live in this session: %q", out)
	}
}

func TestUnit_HandleMission_ForwardedUnreachableTeaches(t *testing.T) {
	disp := &fakeDispatcher{}
	tr, db := newMissionTestTransport(t, disp, &fakeResolver{})
	setMissionConfig(t, db, "default-mission-agent", "reviewer")
	setMissionConfig(t, db, "default-mission-policy", "envelope.json")
	tr.deps.MissionForwarded = &MissionForwardConfig{
		Reachable: func() bool { return false },
		TargetURL: func() string { return "http://serve.example:8080" },
	}

	_, err := tr.handleMission(context.Background(), &sessionEntry{InternalSessionID: "cnx"}, "go")
	if err == nil {
		t.Fatal("an unreachable forwarded serve must teach, not fire")
	}
	for _, want := range []string{"unavailable", "stopped answering", "http://serve.example:8080"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("forwarded teaching error missing %q: %q", want, err.Error())
		}
	}
	if disp.got.AgentName != "" {
		t.Fatal("no dispatch may be attempted when the forwarded serve is unreachable")
	}
}

func TestUnit_HandleMission_ForwardedConfirmationNamesInbox(t *testing.T) {
	disp := &fakeDispatcher{result: fleetservice.DispatchResult{MissionID: "m-remote"}}
	tr, db := newMissionTestTransport(t, disp, &fakeResolver{})
	setMissionConfig(t, db, "default-mission-agent", "reviewer")
	setMissionConfig(t, db, "default-mission-policy", "envelope.json")
	tr.deps.MissionForwarded = &MissionForwardConfig{Reachable: func() bool { return true }}

	out, err := tr.handleMission(context.Background(), &sessionEntry{InternalSessionID: "cnx"}, "go")
	if err != nil {
		t.Fatalf("handleMission: %v", err)
	}
	if !strings.Contains(out, "operator inbox") {
		t.Fatalf("forwarded confirmation must name the operator inbox: %q", out)
	}
}

func TestUnit_HandleMission_NoDefaultAgentErrors(t *testing.T) {
	tr, db := newMissionTestTransport(t, &fakeDispatcher{}, &fakeResolver{})
	setMissionConfig(t, db, "default-mission-policy", "envelope.json")
	_, err := tr.handleMission(context.Background(), &sessionEntry{InternalSessionID: "s"}, "do something")
	if err == nil || !strings.Contains(err.Error(), "no mission agent") {
		t.Fatalf("want no-agent error, got %v", err)
	}
}

func TestUnit_HandleMission_NoEnvelopeErrors(t *testing.T) {
	tr, db := newMissionTestTransport(t, &fakeDispatcher{}, &fakeResolver{})
	setMissionConfig(t, db, "default-mission-agent", "reviewer")
	_, err := tr.handleMission(context.Background(), &sessionEntry{InternalSessionID: "s"}, "do something")
	if err == nil || !strings.Contains(err.Error(), "envelope") {
		t.Fatalf("want no-envelope error, got %v", err)
	}
}

func TestUnit_HandleMission_UnavailableWithoutFleet(t *testing.T) {
	tr, _ := newMissionTestTransport(t, nil, nil)
	_, err := tr.handleMission(context.Background(), &sessionEntry{}, "do something")
	if err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("want unavailable error, got %v", err)
	}
}

func TestUnit_HandleMission_EmptyArgsShowsUsage(t *testing.T) {
	tr, _ := newMissionTestTransport(t, &fakeDispatcher{}, &fakeResolver{})
	_, err := tr.handleMission(context.Background(), &sessionEntry{}, "   ")
	if err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("want usage error, got %v", err)
	}
}

func TestUnit_HandleMission_DispatchErrorSurfaces(t *testing.T) {
	disp := &fakeDispatcher{err: context.Canceled}
	tr, db := newMissionTestTransport(t, disp, &fakeResolver{})
	setMissionConfig(t, db, "default-mission-agent", "reviewer")
	setMissionConfig(t, db, "default-mission-policy", "envelope.json")
	_, err := tr.handleMission(context.Background(), &sessionEntry{InternalSessionID: "s"}, "go")
	if err == nil {
		t.Fatal("dispatch error must surface to the caller")
	}
}

func TestUnit_MissionCommandIsParsedRegardlessOfCapability(t *testing.T) {
	name, args, ok := parseCommand("/mission reviewer do the thing")
	if !ok || name != "mission" || args != "reviewer do the thing" {
		t.Fatalf("parseCommand(/mission ...) = %q,%q,%v", name, args, ok)
	}
}

func TestUnit_AcpCommands_WithMissionCapability_IncludesMission(t *testing.T) {
	tr, _ := newMissionTestTransport(t, &fakeDispatcher{}, &fakeResolver{})
	cmds := tr.acpCommands()

	if !containsCommand(cmds, "mission") {
		t.Fatalf("mission missing from advertised commands with capability wired: %v", commandNames(cmds))
	}
	for _, c := range allACPCommands() {
		if !containsCommand(cmds, c.Name) {
			t.Fatalf("advertised commands missing %q: %v", c.Name, commandNames(cmds))
		}
	}
	if len(cmds) != len(allACPCommands()) {
		t.Fatalf("advertised %d commands, want %d (full set): %v", len(cmds), len(allACPCommands()), commandNames(cmds))
	}
}

func TestUnit_AcpCommands_WithoutMissionCapability_ExcludesMission(t *testing.T) {
	tr, _ := newMissionTestTransport(t, nil, nil)
	cmds := tr.acpCommands()

	if containsCommand(cmds, "mission") {
		t.Fatalf("mission advertised without capability: %v", commandNames(cmds))
	}
	for _, c := range allACPCommands() {
		if c.Name == "mission" {
			continue
		}
		if !containsCommand(cmds, c.Name) {
			t.Fatalf("advertised commands missing %q: %v", c.Name, commandNames(cmds))
		}
	}
	if want := len(allACPCommands()) - 1; len(cmds) != want {
		t.Fatalf("advertised %d commands, want %d (full set minus mission): %v", len(cmds), want, commandNames(cmds))
	}
}

func TestUnit_HandleMission_TeachingErrorWithoutCapability(t *testing.T) {
	tr, _ := newMissionTestTransport(t, nil, nil)
	_, err := tr.handleMission(context.Background(), &sessionEntry{}, "do something")
	if err == nil {
		t.Fatal("want a teaching error, got nil")
	}
	for _, want := range []string{"unavailable", "default-model", "in-process fleet", "CONTENOX_SERVER_URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("teaching error missing %q: %q", want, err.Error())
		}
	}
	for _, forbidden := range []string{"Beam", "contenox serve", "serve-hosted"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("in-process teaching error must not teach serve-as-center, but contains %q: %q", forbidden, err.Error())
		}
	}
}

func TestUnit_DeliverToContenoxSession_MapsAndErrors(t *testing.T) {
	tr, _ := newMissionTestTransport(t, nil, nil)

	err := tr.DeliverToContenoxSession(context.Background(), "nope",
		libacp.SessionNotification{Update: libacp.NewAgentMessageChunk("hi")})
	if !errors.Is(err, ErrSessionNotLive) {
		t.Fatalf("unknown contenox id must yield ErrSessionNotLive (the inbox-fallback signal), got %v", err)
	}

	tr.contenoxToACPID = map[string]libacp.SessionID{"cnx-1": "acp-1"}
	if err := tr.DeliverToContenoxSession(context.Background(), "cnx-1",
		libacp.SessionNotification{Update: libacp.NewAgentMessageChunk("hi")}); err != nil {
		t.Fatalf("a bound session must deliver (nil), got %v", err)
	}
}

func containsCommand(cmds []libacp.AvailableCommand, name string) bool {
	for _, c := range cmds {
		if c.Name == name {
			return true
		}
	}
	return false
}

func commandNames(cmds []libacp.AvailableCommand) []string {
	names := make([]string, len(cmds))
	for i, c := range cmds {
		names[i] = c.Name
	}
	return names
}
