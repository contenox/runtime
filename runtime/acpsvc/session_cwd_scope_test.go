package acpsvc

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	libacp "github.com/contenox/runtime/libacp"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/enginesvc"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_SessionListing_ScopedToTheRequestedCwd(t *testing.T) {
	here := t.TempDir()
	elsewhere := t.TempDir()
	nested := filepath.Join(here, "pkg")

	require.True(t, sessionUnderRequestedCwd(here, here), "a session rooted here is listed")
	require.True(t, sessionUnderRequestedCwd(nested, here), "so is one rooted below it")
	require.False(t, sessionUnderRequestedCwd(elsewhere, here), "a session from another directory must not be listed")

	require.True(t, sessionUnderRequestedCwd(elsewhere, ""), "an unscoped request still sees everything")
	require.True(t, sessionUnderRequestedCwd(elsewhere, "/"), "and so does the sentinel")
	require.True(t, sessionUnderRequestedCwd("", ""), "untagged sessions stay in an unscoped listing")

	require.False(t, sessionUnderRequestedCwd("", here), "an untagged session is attributable to no directory, so a scoped listing omits it")
}

func TestE2E_Wire_SessionListCwdFilterAppliesBeforePaging(t *testing.T) {
	prev := listSessionsPageSize
	listSessionsPageSize = 2
	t.Cleanup(func() { listSessionsPageSize = prev })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "wire-cwd.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	defer db.Close()

	agentR, clientW := io.Pipe()
	clientR, agentW := io.Pipe()
	agentSide := &wirePipe{r: agentR, w: agentW}
	clientSide := &wirePipe{r: clientR, w: clientW}
	factory := New(Deps{Engine: &enginesvc.Engine{}, DB: db, WorkspaceID: "cwd-ws"})
	conn := libacp.NewAgentSideConnection(agentSide, func(c *libacp.AgentSideConnection) libacp.Agent { return factory(c) })
	go func() { _ = conn.Run(ctx) }()
	defer clientSide.Close()

	client := &wireClient{t: t, rw: clientSide}
	resp, _ := client.call(libacp.MethodInitialize, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.Nil(t, resp.Error)

	here := t.TempDir()
	nested := filepath.Join(here, "pkg")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	newSession := func(cwd string) libacp.SessionID {
		resp, _ := client.call(libacp.MethodSessionNew, libacp.NewSessionRequest{Cwd: cwd, McpServers: []libacp.McpServer{}})
		require.Nil(t, resp.Error)
		var nr libacp.NewSessionResponse
		require.NoError(t, json.Unmarshal(resp.Result, &nr))
		client.drainNotifications(1)
		return nr.SessionID
	}
	want := map[libacp.SessionID]bool{}
	want[newSession(here)] = true
	for i := 0; i < 3; i++ {
		newSession(t.TempDir())
	}
	want[newSession(nested)] = true
	want[newSession(here)] = true
	newSession(t.TempDir())

	seen := map[libacp.SessionID]int{}
	cursor := ""
	var pageSizes []int
	for {
		resp, _ := client.call(libacp.MethodSessionList, libacp.ListSessionsRequest{Cwd: here, Cursor: cursor})
		require.Nil(t, resp.Error)
		var list libacp.ListSessionsResponse
		require.NoError(t, json.Unmarshal(resp.Result, &list))
		pageSizes = append(pageSizes, len(list.Sessions))
		for _, s := range list.Sessions {
			seen[s.SessionID]++
		}
		require.LessOrEqual(t, len(pageSizes), 10, "paging must terminate")
		if list.NextCursor == "" {
			break
		}
		cursor = list.NextCursor
	}
	require.Equal(t, []int{2, 1}, pageSizes, "pages are filled from in-scope sessions only")
	require.Len(t, seen, len(want))
	for sid, n := range seen {
		require.Equal(t, 1, n)
		require.True(t, want[sid], "session %s is outside the requested cwd", sid)
	}
}
