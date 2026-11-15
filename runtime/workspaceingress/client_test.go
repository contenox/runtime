package workspaceingress

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/eventlog"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/vfs"
	"github.com/contenox/runtime/runtime/workspaceevents"
	"github.com/stretchr/testify/require"
)

func TestLostAcknowledgementAcrossRuntimeRestart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "state.db")
	factory, err := vfs.NewFactory(root)
	require.NoError(t, err)
	var ack atomic.Int32
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer machine-token" {
			http.Error(w, "invalid token", 401)
			return
		}
		if r.URL.Path == "/v1/deliveries/ack" {
			if ack.Add(1) == 1 {
				http.Error(w, "disconnected", 503)
				return
			}
			w.WriteHeader(204)
			return
		}
		p := page{Destination: "machine", Through: 1, Receipt: "receipt", Events: []delivery{}}
		if ack.Load() < 2 {
			p.Events = append(p.Events, delivery{ID: "delivery-42", Sequence: 1, Topic: "github.pull_request.opened", Payload: json.RawMessage(`{"number":42}`), Received: time.Now()})
		}
		json.NewEncoder(w).Encode(p)
	}))
	defer host.Close()
	scope := workspaceevents.Scope("test", root)
	open := func() (*Client, libdbexec.DBManager) {
		db, err := libdbexec.NewSQLiteDBManager(ctx, path, runtimetypes.SchemaSQLite)
		require.NoError(t, err)
		bus := libbus.NewInMem()
		tracker := libtracker.NoopTracker{}
		log := eventlog.NewService(db, nil, tracker)
		return New(db, "test", workspaceevents.New("test", log, bus, factory, tracker), factory, host.URL), db
	}
	c, db := open()
	_, err = db.WithoutTransaction().ExecContext(ctx, "INSERT INTO ingress_bindings(scope,destination,origin,token) VALUES($1,'machine',$2,'machine-token')", scope, host.URL)
	require.NoError(t, err)
	b, err := c.binding(ctx, scope)
	require.NoError(t, err)
	_, err = c.sync(ctx, root, scope, b, false)
	require.Error(t, err)
	require.NoError(t, db.Close())
	c, db = open()
	defer db.Close()
	b, err = c.binding(ctx, scope)
	require.NoError(t, err)
	require.EqualValues(t, 1, b.Cursor)
	more, err := c.sync(ctx, root, scope, b, false)
	require.NoError(t, err)
	require.True(t, more)
	more, err = c.sync(ctx, root, scope, b, false)
	require.NoError(t, err)
	require.False(t, more)
	events, err := c.Events.Since(ctx, root, 0, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "github.pull_request.opened", events[0].Subject)
	require.Equal(t, "github", events[0].Source)
}
