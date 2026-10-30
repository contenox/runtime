//go:build windows

package terminalservice

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

type pipeConn struct {
	r   *io.PipeReader
	w   *io.PipeWriter
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *pipeConn) Read(p []byte) (int, error) { return c.r.Read(p) }
func (c *pipeConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}
func (c *pipeConn) Close() error { _ = c.r.Close(); return nil }
func (c *pipeConn) output() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func TestWindowsConPTY_ProducesOutputAndCwd(t *testing.T) {
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "terminal.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	svc, err := New(Config{Enabled: true, AllowedRoot: root, DefaultShell: "cmd.exe", MaxSessions: 2}, db, "node-test", "ws-test")
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.CloseAll(context.Background()) })

	resp, err := svc.Create(ctx, "u", CreateRequest{CWD: root, Shell: "cmd.exe", Cols: 100, Rows: 30})
	require.NoError(t, err)

	pr, pw := io.Pipe()
	conn := &pipeConn{r: pr, w: pw}
	attachCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); _ = svc.Attach(attachCtx, "u", resp.ID, conn, nil) }()
	t.Cleanup(func() { cancel(); _ = pw.Close(); <-done })

	_, err = pw.Write([]byte("echo contenox-ok\r"))
	require.NoError(t, err)
	_, err = pw.Write([]byte("cd\r"))
	require.NoError(t, err)

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		out := conn.output()
		if strings.Contains(out, "contenox-ok") && strings.Count(out, root) >= 1 && strings.Contains(out[strings.LastIndex(out, "contenox-ok"):], root) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no expected output; got %q", conn.output())
}
