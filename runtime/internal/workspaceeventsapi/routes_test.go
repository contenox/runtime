package workspaceeventsapi

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	libbus "github.com/contenox/runtime/libbus"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/eventlog"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/vfs"
	"github.com/contenox/runtime/runtime/workspaceevents"
	"github.com/stretchr/testify/require"
)

type apiFixture struct {
	server  *httptest.Server
	service workspaceevents.Service
	rootA   string
	rootB   string
}

func newAPIFixture(t *testing.T) apiFixture {
	t.Helper()
	return newAPIFixtureAt(t, time.Now)
}

func newAPIFixtureAt(t *testing.T, now func() time.Time) apiFixture {
	t.Helper()
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "events.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	rootA := t.TempDir()
	rootB := t.TempDir()
	factory, err := vfs.NewFactory(rootA, rootB)
	require.NoError(t, err)
	bus := libbus.NewInMem()
	t.Cleanup(func() { _ = bus.Close() })
	service := workspaceevents.New("ws-api", eventlog.NewService(db, nil, libtracker.NoopTracker{}), bus, factory, libtracker.NoopTracker{})
	mux := http.NewServeMux()
	addRoutes(mux, service, now)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return apiFixture{server: server, service: service, rootA: rootA, rootB: rootB}
}

func (f apiFixture) url(path, root string, query url.Values) string {
	if query == nil {
		query = url.Values{}
	}
	if root != "" {
		query.Set("root", root)
	}
	return f.server.URL + path + "?" + query.Encode()
}

func (f apiFixture) post(t *testing.T, root, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(f.url("/workspace/events", root, nil), "application/json", strings.NewReader(body))
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func (f apiFixture) list(t *testing.T, root string, query url.Values) listResponse {
	t.Helper()
	resp, err := http.Get(f.url("/workspace/events", root, query))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out listResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

func TestUnit_WorkspaceEventsAPI_PostThenListByCursor(t *testing.T) {
	f := newAPIFixture(t)
	resp := f.post(t, f.rootA, `{"type":"editor.opened","subject":"src/main.go"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var first workspaceevents.Event
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&first))
	require.Equal(t, workspaceevents.SourceEditor, first.Source)

	resp = f.post(t, f.rootA, `{"type":"editor.saved","subject":"src/main.go","data":{"etag":"e1"}}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	all := f.list(t, f.rootA, nil)
	require.Len(t, all.Events, 2)
	require.Equal(t, all.Events[1].NID, all.Cursor)

	tail := f.list(t, f.rootA, url.Values{"since": {jsonNumber(first.NID)}})
	require.Len(t, tail.Events, 1)
	require.Equal(t, "editor.saved", tail.Events[0].Type)

	none := f.list(t, f.rootA, url.Values{"since": {jsonNumber(all.Cursor)}})
	require.Empty(t, none.Events)
	require.Equal(t, all.Cursor, none.Cursor)

	other := f.list(t, f.rootB, nil)
	require.Empty(t, other.Events)

	byDefault := f.list(t, "", nil)
	require.Len(t, byDefault.Events, 2)
}

func jsonNumber(n int64) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}

func TestUnit_WorkspaceEventsAPI_RefusesNonEditorTypesAndBadInput(t *testing.T) {
	f := newAPIFixture(t)
	cases := map[string]string{
		"watcher type":   `{"type":"fs.modified","subject":"a.txt"}`,
		"tool type":      `{"type":"tool.wrote","subject":"a.txt"}`,
		"unknown type":   `{"type":"editor.exploded","subject":"a.txt"}`,
		"escaping path":  `{"type":"editor.opened","subject":"../../etc/passwd"}`,
		"missing path":   `{"type":"editor.opened"}`,
		"non-object":     `{"type":"editor.saved","subject":"a.txt","data":[1,2]}`,
		"oversized data": `{"type":"editor.saved","subject":"a.txt","data":{"blob":"` + strings.Repeat("x", workspaceevents.MaxDataBytes) + `"}}`,
	}
	for name, body := range cases {
		resp := f.post(t, f.rootA, body)
		require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, name)
	}

	resp := f.post(t, filepath.Dir(f.rootA), `{"type":"editor.opened","subject":"a.txt"}`)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	resp, err := http.Get(f.url("/workspace/events", f.rootA, url.Values{"since": {"-4"}}))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	require.Empty(t, f.list(t, f.rootA, nil).Events)
}

func TestUnit_WorkspaceEventsAPI_RateLimitsABurst(t *testing.T) {
	var clock atomic.Int64
	start := time.Now()
	f := newAPIFixtureAt(t, func() time.Time { return start.Add(time.Duration(clock.Load())) })
	for i := 0; i < bucketCapacity; i++ {
		resp := f.post(t, f.rootA, `{"type":"editor.dirty","subject":"a.txt"}`)
		require.Equal(t, http.StatusCreated, resp.StatusCode, "post %d", i)
	}
	resp := f.post(t, f.rootA, `{"type":"editor.dirty","subject":"a.txt"}`)
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	require.Equal(t, "1", resp.Header.Get("Retry-After"))

	resp = f.post(t, f.rootB, `{"type":"editor.dirty","subject":"b.txt"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	clock.Store(int64(time.Second))
	resp = f.post(t, f.rootA, `{"type":"editor.dirty","subject":"a.txt"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
}

type sseReader struct {
	scanner *bufio.Scanner
	cancel  context.CancelFunc
}

func (f apiFixture) openStream(t *testing.T, root string, query url.Values, lastEventID string) *sseReader {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url("/workspace/events/stream", root, query), nil)
	require.NoError(t, err)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	t.Cleanup(func() {
		cancel()
		_ = resp.Body.Close()
	})
	return &sseReader{scanner: bufio.NewScanner(resp.Body), cancel: cancel}
}

func (s *sseReader) next(t *testing.T) workspaceevents.Event {
	t.Helper()
	for s.scanner.Scan() {
		line := s.scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event workspaceevents.Event
		require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event))
		return event
	}
	t.Fatalf("stream ended before an event arrived: %v", s.scanner.Err())
	return workspaceevents.Event{}
}

func TestUnit_WorkspaceEventsAPI_StreamReplaysThenFollowsLive(t *testing.T) {
	f := newAPIFixture(t)
	ctx := context.Background()
	first, err := f.service.Append(ctx, f.rootA, workspaceevents.Input{Type: workspaceevents.TypeEditorOpened, Source: workspaceevents.SourceEditor, Subject: "a.txt"})
	require.NoError(t, err)
	second, err := f.service.Append(ctx, f.rootA, workspaceevents.Input{Type: workspaceevents.TypeEditorSaved, Source: workspaceevents.SourceEditor, Subject: "a.txt"})
	require.NoError(t, err)

	replay := f.openStream(t, f.rootA, url.Values{"since": {"0"}}, "")
	require.Equal(t, first.NID, replay.next(t).NID)
	require.Equal(t, second.NID, replay.next(t).NID)

	resumed := f.openStream(t, f.rootA, nil, jsonNumber(first.NID))
	require.Equal(t, second.NID, resumed.next(t).NID)

	live := f.openStream(t, f.rootA, nil, "")
	_, err = f.service.Append(ctx, f.rootB, workspaceevents.Input{Type: workspaceevents.TypeEditorOpened, Source: workspaceevents.SourceEditor, Subject: "b.txt"})
	require.NoError(t, err)
	_, err = f.service.Append(ctx, f.rootA, workspaceevents.Input{Type: workspaceevents.TypeToolWrote, Source: workspaceevents.SourceTool, Subject: "c.txt"})
	require.NoError(t, err)
	got := live.next(t)
	require.Equal(t, workspaceevents.TypeToolWrote, got.Type)
	require.Equal(t, "c.txt", got.Subject)
	require.Equal(t, got.NID, replay.next(t).NID)
}
