package contenoxcli

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/agentdecl"
	"github.com/contenox/runtime/runtime/connectorservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

type mcpWorkers interface {
	StartWorker(ctx context.Context, srv *runtimetypes.MCPServer) error
	StopWorker(ctx context.Context, name string)
}

var (
	declaredToolsMu   sync.Mutex
	declaredToolsSink func(context.Context, []agentdecl.SyncResult)
)

func setDeclaredToolsSink(sink func(context.Context, []agentdecl.SyncResult)) {
	declaredToolsMu.Lock()
	defer declaredToolsMu.Unlock()
	declaredToolsSink = sink
}

func publishDeclaredTools(ctx context.Context, results []agentdecl.SyncResult) {
	declaredToolsMu.Lock()
	sink := declaredToolsSink
	declaredToolsMu.Unlock()
	if sink != nil {
		sink(ctx, results)
	}
}

func registerDeclaredTools(db libdb.DBManager, workers mcpWorkers) func(context.Context, []agentdecl.SyncResult) {
	return func(ctx context.Context, results []agentdecl.SyncResult) {
		if err := syncDeclaredTools(ctx, runtimetypes.New(db.WithoutTransaction()), workers, results); err != nil {
			slog.Warn("agent declarations: declared tools were not registered", "error", err)
		}
	}
}

func declaredMCPRow(agent string, d agentdecl.DeclaredMCPServer) *runtimetypes.MCPServer {
	return &runtimetypes.MCPServer{
		Name:                  runtimetypes.DeclaredToolName(agent, d.Declared),
		Transport:             d.Transport,
		Command:               d.Command,
		Args:                  d.Args,
		URL:                   d.URL,
		Headers:               d.Headers,
		AuthType:              d.AuthType,
		AuthEnvKey:            d.AuthEnvKey,
		ConnectTimeoutSeconds: 30,
	}
}

func declaredRemoteRow(agent string, d agentdecl.DeclaredRemoteTool) *runtimetypes.RemoteTools {
	timeout := d.TimeoutMs
	if timeout <= 0 {
		timeout = 10000
	}
	return &runtimetypes.RemoteTools{
		Name:        runtimetypes.DeclaredToolName(agent, d.Declared),
		EndpointURL: d.EndpointURL,
		SpecURL:     d.SpecURL,
		TimeoutMs:   timeout,
		Headers:     d.Headers,
	}
}

func nonEmptyMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return m
}

func nonEmptySlice(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

func sameMCP(a, b *runtimetypes.MCPServer) bool {
	key := func(s *runtimetypes.MCPServer) string {
		raw, _ := json.Marshal([]any{s.Transport, s.Command, nonEmptySlice(s.Args), s.URL, nonEmptyMap(s.Headers), s.AuthType, s.AuthEnvKey})
		return string(raw)
	}
	return key(a) == key(b)
}

func sameRemote(a, b *runtimetypes.RemoteTools) bool {
	key := func(s *runtimetypes.RemoteTools) string {
		raw, _ := json.Marshal([]any{s.EndpointURL, s.SpecURL, s.TimeoutMs, nonEmptyMap(s.Headers)})
		return string(raw)
	}
	return key(a) == key(b)
}

func syncDeclaredTools(ctx context.Context, store runtimetypes.Store, workers mcpWorkers, results []agentdecl.SyncResult) error {
	wantMCP := map[string]*runtimetypes.MCPServer{}
	wantRemote := map[string]*runtimetypes.RemoteTools{}
	for _, res := range results {
		if res.Action == agentdecl.ActionRefused || res.Name == "" {
			continue
		}
		for _, d := range res.MCP {
			row := declaredMCPRow(res.Name, d)
			wantMCP[row.Name] = row
		}
		for _, d := range res.Remote {
			row := declaredRemoteRow(res.Name, d)
			wantRemote[row.Name] = row
		}
	}

	servers, err := connectorservice.AllMCPServers(ctx, store)
	if err != nil {
		return err
	}
	have := map[string]*runtimetypes.MCPServer{}
	for _, srv := range servers {
		if runtimetypes.IsDeclaredToolName(srv.Name) {
			have[srv.Name] = srv
		}
	}
	for name, srv := range have {
		if _, keep := wantMCP[name]; keep {
			continue
		}
		if workers != nil {
			workers.StopWorker(ctx, name)
		}
		if err := store.DeleteMCPServer(ctx, srv.ID); err != nil {
			return err
		}
	}
	for name, row := range wantMCP {
		if cur, ok := have[name]; ok {
			if sameMCP(cur, row) {
				continue
			}
			row.ID = cur.ID
			if err := store.UpdateMCPServer(ctx, row); err != nil {
				return err
			}
		} else if err := store.CreateMCPServer(ctx, row); err != nil {
			return err
		}
		if workers != nil {
			if err := workers.StartWorker(ctx, row); err != nil {
				slog.Warn("agent declarations: declared MCP server did not start", "name", name, "error", err)
			}
		}
	}

	remotes, err := connectorservice.AllRemoteTools(ctx, store)
	if err != nil {
		return err
	}
	haveRemote := map[string]*runtimetypes.RemoteTools{}
	for _, r := range remotes {
		if runtimetypes.IsDeclaredToolName(r.Name) {
			haveRemote[r.Name] = r
		}
	}
	for name, r := range haveRemote {
		if _, keep := wantRemote[name]; keep {
			continue
		}
		if err := store.DeleteRemoteTools(ctx, r.ID); err != nil {
			return err
		}
	}
	for name, row := range wantRemote {
		if cur, ok := haveRemote[name]; ok {
			if sameRemote(cur, row) {
				continue
			}
			row.ID = cur.ID
			if err := store.UpdateRemoteTools(ctx, row); err != nil {
				return err
			}
		} else if err := store.CreateRemoteTools(ctx, row); err != nil {
			return err
		}
	}
	return nil
}
