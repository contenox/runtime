package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

const injectSpec = `{
  "openapi": "3.0.0",
  "info": {"title": "t", "version": "1"},
  "paths": {
    "/orders": {
      "post": {
        "operationId": "create_order",
        "parameters": [{"name": "tenant", "in": "query", "required": true, "schema": {"type": "string"}}],
        "requestBody": {"content": {"application/json": {"schema": {
          "type": "object",
          "required": ["item", "account"],
          "properties": {"item": {"type": "string"}, "account": {"type": "string"}}
        }}}},
        "responses": {"200": {"description": "ok"}}
      }
    }
  }
}`

type seenRequest struct {
	query  string
	header string
	body   map[string]any
}

func TestUnit_RemoteToolsInjectParamsPropertiesAndHeaders(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	var seen seenRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/openapi.json" {
			_, _ = io.WriteString(w, injectSpec)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen.query = r.URL.Query().Get("tenant")
		seen.header = r.Header.Get("X-Region")
		_ = json.Unmarshal(raw, &seen.body)
		mu.Unlock()
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(srv.Close)

	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "tools.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	store := runtimetypes.New(db.WithoutTransaction())
	require.NoError(t, store.CreateRemoteTools(ctx, &runtimetypes.RemoteTools{
		Name:         "shop",
		EndpointURL:  srv.URL,
		TimeoutMs:    5000,
		InjectParams: map[string]string{"account": "acme", "tenant": "t-1"},
		Properties:   runtimetypes.InjectionArg{Name: "X-Region", Value: "eu", In: runtimetypes.LocationHeader},
		CreatedAt:    time.Now(),
	}))

	repo := NewPersistentRepo(nil, db, srv.Client(), nil, nil)
	tools, err := repo.GetToolsForToolsByName(ctx, "shop")
	require.NoError(t, err)
	require.Len(t, tools, 1)
	params, _ := json.Marshal(tools[0].Function.Parameters)
	var schema struct {
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
	}
	require.NoError(t, json.Unmarshal(params, &schema))
	require.Contains(t, schema.Properties, "item")
	require.NotContains(t, schema.Properties, "account", "injected body values are not shown to the model")
	require.NotContains(t, schema.Properties, "tenant", "injected query values are not shown to the model")
	require.Equal(t, []string{"item"}, schema.Required)

	_, _, err = repo.Exec(ctx, time.Now(), map[string]any{"item": "book", "account": "evil"}, false, &taskengine.ToolsCall{Name: "shop", ToolName: "create_order"})
	require.NoError(t, err)
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, "t-1", seen.query)
	require.Equal(t, "eu", seen.header)
	require.Equal(t, "book", seen.body["item"])
	require.Equal(t, "acme", seen.body["account"], "an injected value wins over the model")
	require.NotContains(t, seen.body, "X-Region")
}
