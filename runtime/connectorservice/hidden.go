package connectorservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/errdefs"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

const (
	hiddenToolsKey           = "connector-hidden-tools"
	sessionHiddenToolsPrefix = "session-connectors-hidden:"
)

type HiddenTools struct {
	Everywhere []string            `json:"everywhere"`
	Workspaces map[string][]string `json:"workspaces"`
}

func scopeKey(workspace string) string {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return ""
	}
	workspace = filepath.Clean(workspace)
	if runtime.GOOS == "windows" {
		workspace = strings.ToLower(workspace)
	}
	return workspace
}

func qualify(connector, tool string) string {
	return connector + "." + tool
}

func readHidden(ctx context.Context, store runtimetypes.Store) map[string][]string {
	stored := map[string][]string{}
	if err := store.GetKV(ctx, hiddenToolsKey, &stored); err != nil {
		return map[string][]string{}
	}
	return stored
}

func writeHidden(ctx context.Context, store runtimetypes.Store, stored map[string][]string) error {
	for k, v := range stored {
		if len(v) == 0 {
			delete(stored, k)
		}
	}
	if len(stored) == 0 {
		err := store.DeleteKV(ctx, hiddenToolsKey)
		if errors.Is(err, libdb.ErrNotFound) {
			return nil
		}
		return err
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	return store.SetKV(ctx, hiddenToolsKey, raw)
}

func ListHidden(ctx context.Context, store runtimetypes.Store) HiddenTools {
	out := HiddenTools{Everywhere: []string{}, Workspaces: map[string][]string{}}
	for k, v := range readHidden(ctx, store) {
		if k == "" {
			out.Everywhere = v
		} else {
			out.Workspaces[k] = v
		}
	}
	return out
}

func replaceConnectorTools(list []string, connector string, tools []string) []string {
	prefix := connector + "."
	next := make([]string, 0, len(list)+len(tools))
	for _, n := range list {
		if !strings.HasPrefix(n, prefix) {
			next = append(next, n)
		}
	}
	seen := map[string]struct{}{}
	for _, tool := range tools {
		tool = strings.TrimSpace(strings.TrimPrefix(tool, prefix))
		if tool == "" {
			continue
		}
		if _, dup := seen[tool]; dup {
			continue
		}
		seen[tool] = struct{}{}
		next = append(next, qualify(connector, tool))
	}
	sort.Strings(next)
	return next
}

func validConnectorName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, ". ") {
		return fmt.Errorf("%w: connector name %q is not valid", errdefs.ErrUnprocessableEntity, name)
	}
	return nil
}

func SetHidden(ctx context.Context, store runtimetypes.Store, workspace, connector string, tools []string) error {
	if err := validConnectorName(connector); err != nil {
		return err
	}
	if strings.TrimSpace(workspace) != "" {
		if _, err := runtimetypes.CleanWorkspacePath(workspace); err != nil {
			return fmt.Errorf("%w: %w", errdefs.ErrUnprocessableEntity, err)
		}
	}
	stored := readHidden(ctx, store)
	key := scopeKey(workspace)
	stored[key] = replaceConnectorTools(stored[key], connector, tools)
	return writeHidden(ctx, store, stored)
}

func hiddenFor(ctx context.Context, store runtimetypes.Store, cwd string) []string {
	var out []string
	for k, v := range readHidden(ctx, store) {
		if k == "" || runtimetypes.WorkspaceCovers(k, cwd) {
			out = append(out, v...)
		}
	}
	return out
}

func sessionHiddenKey(sessionID string) string {
	return sessionHiddenToolsPrefix + sessionID
}

func SessionHidden(ctx context.Context, store runtimetypes.Store, sessionID string) []string {
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}
	var names []string
	if err := store.GetKV(ctx, sessionHiddenKey(sessionID), &names); err != nil {
		return nil
	}
	return names
}

func SetSessionToolHidden(ctx context.Context, store runtimetypes.Store, sessionID, connector, tool string, hidden bool) error {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(tool) == "" {
		return fmt.Errorf("%w: session and tool are required", errdefs.ErrUnprocessableEntity)
	}
	if err := validConnectorName(connector); err != nil {
		return err
	}
	full := qualify(connector, strings.TrimPrefix(strings.TrimSpace(tool), connector+"."))
	current := SessionHidden(ctx, store, sessionID)
	next := make([]string, 0, len(current)+1)
	for _, n := range current {
		if n != full {
			next = append(next, n)
		}
	}
	if hidden {
		next = append(next, full)
	}
	if len(next) == 0 {
		err := store.DeleteKV(ctx, sessionHiddenKey(sessionID))
		if errors.Is(err, libdb.ErrNotFound) {
			return nil
		}
		return err
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return err
	}
	return store.SetKV(ctx, sessionHiddenKey(sessionID), raw)
}

func leafNames(full []string, connector string) []string {
	prefix := connector + "."
	var out []string
	for _, n := range full {
		if leaf, ok := strings.CutPrefix(n, prefix); ok {
			out = append(out, leaf)
		}
	}
	sort.Strings(out)
	return out
}

func (s *service) markHidden(ctx context.Context, picked []Connector, f Filter) {
	store := s.store()
	stored := readHidden(ctx, store)
	var settings, chat []string
	if f.Session {
		settings = hiddenFor(ctx, store, f.Cwd)
		chat = SessionHidden(ctx, store, f.SessionID)
	}
	for i := range picked {
		c := &picked[i]
		if f.Session {
			c.HiddenBySettings = leafNames(settings, c.Name)
			c.HiddenInChat = leafNames(chat, c.Name)
			continue
		}
		for scope, names := range stored {
			if leaves := leafNames(names, c.Name); len(leaves) > 0 {
				if c.Hidden == nil {
					c.Hidden = map[string][]string{}
				}
				c.Hidden[scope] = leaves
			}
		}
	}
}
