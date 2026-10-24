package acpsvc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	libacp "github.com/contenox/runtime/libacp"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/localtools"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/vfs"
)

var osFallback = localtools.NewOSFileIO()

type acpFileIO struct {
	transport func() *Transport
}

func NewACPFileIO(transport func() *Transport) localtools.FileIO {
	return &acpFileIO{transport: transport}
}

func (a *acpFileIO) ReadFile(ctx context.Context, path string) ([]byte, error) {
	t := a.transport()
	if t == nil {
		return nil, errors.New("acpsvc: no active ACP transport")
	}
	if !t.getClientCaps().FS.ReadTextFile {
		return osFallback.ReadFile(ctx, path)
	}
	req := libacp.ReadTextFileRequest{Path: path}
	if sid := resolveACPSessionID(ctx, t); sid != "" {
		req.SessionID = sid
	}
	resp, err := t.conn.ReadTextFile(ctx, req)
	if err != nil {
		return nil, mapACPNotExist(err)
	}
	return []byte(resp.Content), nil
}

func (a *acpFileIO) WriteFile(ctx context.Context, path string, data []byte) error {
	t := a.transport()
	if t == nil {
		return errors.New("acpsvc: no active ACP transport")
	}
	if !t.getClientCaps().FS.WriteTextFile {
		return osFallback.WriteFile(ctx, path, data)
	}
	req := libacp.WriteTextFileRequest{Path: path, Content: string(data)}
	if sid := resolveACPSessionID(ctx, t); sid != "" {
		req.SessionID = sid
	}
	if _, err := t.conn.WriteTextFile(ctx, req); err != nil {
		return mapACPNotExist(err)
	}
	return nil
}

func mapACPNotExist(err error) error {
	if mapped := libacp.AsNotExist(err); errors.Is(mapped, os.ErrNotExist) {
		return mapped
	}
	var typed *libacp.Error
	if err != nil && !errors.As(err, &typed) && strings.Contains(strings.ToLower(err.Error()), "not found") {
		return fmt.Errorf("%w: %v", os.ErrNotExist, err)
	}
	return err
}

func NewACPCwdResolver(transport func() *Transport) func(context.Context) string {
	return func(ctx context.Context) string {
		t := transport()
		if t == nil {
			return ""
		}
		internalID := sessionIDFromCtx(ctx)
		if internalID == "" {
			return ""
		}
		t.sessionMu.Lock()
		defer t.sessionMu.Unlock()
		for _, entry := range t.sessions {
			if entry.InternalSessionID == internalID && entry.Cwd != "" {
				return entry.Cwd
			}
		}
		return ""
	}
}

func NewServeCwdResolver(db libdb.DBManager, roots *vfs.Factory) func(context.Context) string {
	defaultRoot := func() string {
		if roots == nil {
			return ""
		}
		return roots.Default()
	}
	return func(ctx context.Context) string {
		stored := ""
		if db != nil {
			if internalID := sessionIDFromCtx(ctx); internalID != "" {
				stored = serveSessionCwd(ctx, db, internalID)
			}
		}
		resolved, err := vfs.ResolveSessionCwd(roots, stored, defaultRoot())
		if err != nil {
			slog.Warn("acpsvc: session workspace is outside the configured workspace roots; using the default root",
				"stored_cwd", stored, "default_root", defaultRoot(), "error", err)
			return defaultRoot()
		}
		return resolved
	}
}

func serveSessionCwd(ctx context.Context, db libdb.DBManager, internalID string) string {
	exec := db.WithoutTransaction()
	var name string
	if err := exec.QueryRowContext(ctx,
		`SELECT name FROM message_indices WHERE id = $1`, internalID,
	).Scan(&name); err != nil || name == "" {
		return ""
	}
	var rec sessionCwdRecord
	if err := runtimetypes.New(exec).GetKV(ctx, acpSessionCwdKVPrefix+name, &rec); err != nil {
		return ""
	}
	return rec.Cwd
}
