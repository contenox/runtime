package runtimetypes

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/google/uuid"
)

const DesktopBrowserSelector = "@desktop-browser"

// DesktopBrowserLauncher is supplied by the parent desktop process. An MCP
// server's name or self-reported tool annotations alone cannot opt into its grants.
type DesktopBrowserLauncher struct {
	command, helper, endpoint string
}

func ParseDesktopBrowserLauncher(raw string) *DesktopBrowserLauncher {
	var paths []string
	if json.Unmarshal([]byte(raw), &paths) != nil || len(paths) != 3 {
		return nil
	}
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			return nil
		}
	}
	return &DesktopBrowserLauncher{paths[0], paths[1], paths[2]}
}

func (l *DesktopBrowserLauncher) Matches(s *MCPServer) bool {
	if l == nil || s == nil || s.Name != "browser" || s.Transport != "stdio" || s.Command != l.command ||
		len(s.Env) != 1 || s.Env["ELECTRON_RUN_AS_NODE"] != "1" || len(s.InjectParams) != 0 ||
		len(s.Args) != 3 || s.Args[0] != l.helper || s.Args[1] != l.endpoint {
		return false
	}
	id, err := uuid.Parse(s.Args[2])
	return err == nil && id.Version() == 4 && id.Variant() == uuid.RFC4122 && id.String() == s.Args[2]
}

type desktopBrowserKey struct{}

func WithDesktopBrowser(ctx context.Context, server *MCPServer) context.Context {
	return context.WithValue(ctx, desktopBrowserKey{}, server)
}

func DesktopBrowserFromContext(ctx context.Context) *MCPServer {
	server, _ := ctx.Value(desktopBrowserKey{}).(*MCPServer)
	return server
}

// Called on a verified registration to bind dispatch to the same installation and scope.
func (s *MCPServer) DesktopBrowserKey() string {
	return s.ID + "/" + s.Args[2] + "/" + s.Workspace
}
