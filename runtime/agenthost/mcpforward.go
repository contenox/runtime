package agenthost

import (
	"context"
	"fmt"
	"sort"

	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

type McpServerResolver interface {
	GetByName(ctx context.Context, name string) (*runtimetypes.MCPServer, error)
}

func ResolveForwardedMcpServers(ctx context.Context, resolver McpServerResolver, names []string) ([]libacp.McpServer, error) {
	if len(names) == 0 {
		return nil, nil
	}
	out := make([]libacp.McpServer, 0, len(names))
	for _, name := range names {
		row, err := resolver.GetByName(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("agenthost: mcp server %q from the agent's mcp_servers allowlist: %w", name, err)
		}
		srv, err := McpServerForACP(row)
		if err != nil {
			return nil, err
		}
		out = append(out, srv)
	}
	return out, nil
}

func McpServerForACP(row *runtimetypes.MCPServer) (libacp.McpServer, error) {
	srv := libacp.McpServer{Name: row.Name}
	switch row.Transport {
	case "stdio":
		if row.Command == "" {
			return libacp.McpServer{}, fmt.Errorf("agenthost: mcp server %q: stdio transport without a command", row.Name)
		}
		srv.Command = row.Command
		srv.Args = append([]string{}, row.Args...)
		srv.Env = []libacp.EnvVariable{}
	case "http", "sse":
		if row.URL == "" {
			return libacp.McpServer{}, fmt.Errorf("agenthost: mcp server %q: %s transport without a url", row.Name, row.Transport)
		}
		srv.Type = row.Transport
		srv.URL = row.URL
		srv.Headers = make([]libacp.HttpHeader, 0, len(row.Headers))
		for name, value := range row.Headers {
			srv.Headers = append(srv.Headers, libacp.HttpHeader{Name: name, Value: value})
		}
		sort.Slice(srv.Headers, func(i, j int) bool { return srv.Headers[i].Name < srv.Headers[j].Name })
	default:
		return libacp.McpServer{}, fmt.Errorf("agenthost: mcp server %q: unsupported transport %q for ACP forwarding", row.Name, row.Transport)
	}
	return srv, nil
}

func filterMcpServersByCapabilities(servers []libacp.McpServer, caps libacp.McpCapabilities) (kept []libacp.McpServer, dropped []string) {
	for _, srv := range servers {
		switch srv.Kind() {
		case libacp.McpServerKindHTTP:
			if !caps.HTTP {
				dropped = append(dropped, srv.Name)
				continue
			}
		case libacp.McpServerKindSSE:
			if !caps.SSE {
				dropped = append(dropped, srv.Name)
				continue
			}
		}
		kept = append(kept, srv)
	}
	return kept, dropped
}
