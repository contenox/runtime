package fleetservice

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/contenox/runtime/libacp"
	libbus "github.com/contenox/runtime/libbus"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/agentinstance"
	"github.com/contenox/runtime/runtime/agentregistryservice"
	"github.com/contenox/runtime/runtime/chainagents"
	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/contenox/runtime/runtime/operatorinbox"
	"github.com/contenox/runtime/runtime/reportrouter"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestFleetE2E_ReportRouting_ParentSessionAndInbox(t *testing.T) {
	requireFleetSandbox(t)
	if testing.Short() {
		t.Skip("skipping report-routing e2e: builds and boots the full contenox binary")
	}

	bin := buildContenoxBinary(t)
	home := t.TempDir()

	t.Setenv("HOME", home)
	for _, k := range []string{
		"CONTENOX_DEFAULT_MODEL", "CONTENOX_DEFAULT_PROVIDER",
		"CONTENOX_DEFAULT_ALT_MODEL", "CONTENOX_DEFAULT_ALT_PROVIDER",
		"CONTENOX_DEFAULT_MAX_TOKENS", "CONTENOX_DEFAULT_THINK",
		"CONTENOX_ACP_CHAIN_PATH",
	} {
		t.Setenv(k, "")
	}
	runContenoxCLI(t, bin, home, "config", "set", "default-model", "chain-unit-fixture-model")
	runContenoxCLI(t, bin, home, "config", "set", "update-check", "false")

	contenoxDir := filepath.Join(home, ".contenox-harness")
	require.DirExists(t, contenoxDir)
	writeChainAgentFixture(t, contenoxDir)

	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "report-routing-e2e.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	agents := agentregistryservice.New(db)
	bus := libbus.NewInMem()
	t.Cleanup(func() { _ = bus.Close() })
	missions := missionservice.New(db, missionservice.WithEventPublisher(bus))
	inbox := operatorinbox.New(db)

	res, err := chainagents.Discover(ctx, agents, contenoxDir)
	require.NoError(t, err)
	require.Equal(t, []string{"agent-fleet-fixture"}, res.Created)

	stderr := &lockedBuffer{}
	instances := agentinstance.New(agents,
		agentinstance.WithSelfExecutable(bin),
		agentinstance.WithStderr(stderr),
	)
	t.Cleanup(func() { _ = instances.Close() })

	router, err := reportrouter.New(reportrouter.Deps{
		Bus:      bus,
		Sessions: instances,
		Inbox:    inbox,
		Tracker:  libtracker.NoopTracker{},
	})
	require.NoError(t, err)
	stopRouter, err := router.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(stopRouter)

	workDir := t.TempDir()
	svc := New(instances, agents, missions, nil, workDir, libtracker.NoopTracker{})

	parent, err := svc.Dispatch(ctx, DispatchRequest{
		AgentName:      "agent-fleet-fixture",
		Intent:         "be the supervising session",
		HITLPolicyName: "default",
	})
	require.NoError(t, err, "parent dispatch stderr:\n%s", stderr.String())

	viewer := &recordingViewer{id: "supervisor-observer"}
	_, err = instances.Attach(ctx, parent.InstanceID, libacp.SessionID(parent.SessionID), viewer)
	require.NoError(t, err)

	childMission := &missionservice.Mission{
		Intent:          "be the sub-unit that reports back",
		AgentName:       "agent-fleet-fixture",
		HITLPolicyName:  "default",
		ParentSessionID: parent.SessionID,
	}
	require.NoError(t, missions.Create(ctx, childMission))

	const deliveredSummary = "sub-unit result routed to the parent session"
	require.NoError(t, missions.AddReport(ctx, childMission.ID, &missionservice.Report{
		Kind:    missionservice.ReportKindResult,
		Summary: deliveredSummary,
	}))

	require.Eventually(t, func() bool {
		return strings.Contains(viewer.messageText(), deliveredSummary)
	}, 30*time.Second, 50*time.Millisecond,
		"the report never reached the supervising session; transcript=%q\nstderr:\n%s", viewer.messageText(), stderr.String())

	inboxItems, err := inbox.List(ctx, 100)
	require.NoError(t, err)
	for _, it := range inboxItems {
		require.NotEqual(t, deliveredSummary, it.Report.Summary,
			"a report delivered to its supervisor must not also fall into the operator inbox")
	}

	operatorMission := &missionservice.Mission{
		Intent:         "operator fired this directly",
		AgentName:      "agent-fleet-fixture",
		HITLPolicyName: "default",
	}
	require.NoError(t, missions.Create(ctx, operatorMission))

	const inboxSummary = "operator-fired result awaiting the inbox"
	require.NoError(t, missions.AddReport(ctx, operatorMission.ID, &missionservice.Report{
		Kind:    missionservice.ReportKindResult,
		Summary: inboxSummary,
	}))

	require.Eventually(t, func() bool {
		items, lerr := inbox.List(ctx, 100)
		if lerr != nil {
			return false
		}
		for _, it := range items {
			if it.Report.Summary == inboxSummary {
				require.Equal(t, operatorinbox.ReasonOperatorFired, it.Reason)
				require.Equal(t, operatorMission.ID, it.MissionID)
				return true
			}
		}
		return false
	}, 15*time.Second, 50*time.Millisecond, "the operator-fired report never landed in the inbox")

	require.NoError(t, svc.Stop(ctx, parent.InstanceID))
}
