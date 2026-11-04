package contenoxcli

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/contenox/runtime/runtime/acpsvc"
	"github.com/contenox/runtime/runtime/agentdecl"
	"github.com/contenox/runtime/runtime/project"
)

func renderWorkspaceEnvelopes(ctx context.Context, contenoxDir string, roots []string) {
	for _, root := range roots {
		names, err := agentdecl.RenderWorkspaceEnvelopes(ctx, contenoxDir, root)
		if err != nil {
			slog.Warn("contenox-runtime serve: this workspace's envelopes could not be applied; the global ones stay in effect",
				"workspace", root, "error", err)
			continue
		}
		if len(names) > 0 {
			slog.Info("contenox-runtime serve: workspace envelopes applied", "workspace", root, "envelopes", names)
		}
	}
}

func workspaceEnvelopeSignature(roots []string) string {
	parts := make([]string, 0, len(roots))
	for _, root := range roots {
		file := filepath.Join(root, project.ContenoxDirName, agentdecl.ConfigFilename)
		part := root + "|"
		if info, err := os.Stat(file); err == nil {
			part += strconv.FormatInt(info.ModTime().UnixNano(), 10) + ":" + strconv.FormatInt(info.Size(), 10)
		}
		parts = append(parts, part)
	}
	sort.Strings(parts)
	return strings.Join(parts, "\n")
}

func watchWorkspaceEnvelopes(ctx context.Context, contenoxDir string, roots func() []string) func() {
	ctx, cancel := context.WithCancel(ctx)
	current := roots()
	renderWorkspaceEnvelopes(ctx, contenoxDir, current)
	signature := workspaceEnvelopeSignature(current)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				current := roots()
				next := workspaceEnvelopeSignature(current)
				if next == signature {
					continue
				}
				signature = next
				renderWorkspaceEnvelopes(ctx, contenoxDir, current)
			}
		}
	}()
	return cancel
}

func agentsConfigApplied(contenoxDir string) func(ctx context.Context, scope, workspaceRoot string) {
	return func(ctx context.Context, scope, workspaceRoot string) {
		if scope == "workspace" && workspaceRoot != "" {
			renderWorkspaceEnvelopes(ctx, contenoxDir, []string{workspaceRoot})
			acpsvc.NotifyAgentDeclarationsChanged()
			return
		}
		if err := seedAgentDeclarations(ctx, contenoxDir); err != nil {
			slog.Error("contenox-runtime serve: agent declarations failed to recompile after an agents.toml edit", "error", err)
			return
		}
		acpsvc.NotifyAgentDeclarationsChanged()
	}
}
