package acpexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

const defaultKillGrace = 5 * time.Second

type Option func(*config)

type config struct {
	stderr    io.Writer
	killGrace time.Duration
}

func WithStderr(w io.Writer) Option {
	return func(c *config) { c.stderr = w }
}

func WithKillGrace(d time.Duration) Option {
	return func(c *config) { c.killGrace = d }
}

type Process struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	grace  time.Duration

	waitDone chan struct{}
	waitErr  error

	stdoutClosed atomic.Bool
	closeOnce    sync.Once
	closeErr     error
}

var _ io.ReadWriteCloser = (*Process)(nil)

func Spawn(ctx context.Context, cmd *exec.Cmd, opts ...Option) (*Process, error) {
	cfg := config{stderr: io.Discard, killGrace: defaultKillGrace}
	for _, opt := range opts {
		opt(&cfg)
	}

	if cmd.Stdout != nil {
		return nil, fmt.Errorf("acpexec: stdout pipe: exec: Stdout already set")
	}
	stdout, childStdout, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("acpexec: stdout pipe: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = stdout.Close()
		_ = childStdout.Close()
		return nil, fmt.Errorf("acpexec: stdin pipe: %w", err)
	}
	cmd.Stdout = childStdout
	cmd.Stderr = cfg.stderr

	setProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = childStdout.Close()
		return nil, fmt.Errorf("acpexec: start %s: %w", cmd.Path, err)
	}

	p := &Process{
		cmd:      cmd,
		stdin:    stdin,
		stdout:   stdout,
		grace:    cfg.killGrace,
		waitDone: make(chan struct{}),
	}

	go func() {
		p.waitErr = cmd.Wait()
		close(p.waitDone)
	}()

	if err := childStdout.Close(); err != nil {
		return nil, errors.Join(fmt.Errorf("acpexec: close parent stdout writer: %w", err), p.Close())
	}

	if ctx != nil {
		go func() {
			select {
			case <-ctx.Done():
				_ = p.Close()
			case <-p.waitDone:
			}
		}()
	}

	return p, nil
}

func (p *Process) Read(b []byte) (int, error) {
	n, err := p.stdout.Read(b)
	if errors.Is(err, os.ErrClosed) && p.stdoutClosed.Load() {
		return n, io.EOF
	}
	return n, err
}

func (p *Process) Write(b []byte) (int, error) { return p.stdin.Write(b) }

const killReapTimeout = 5 * time.Second

func (p *Process) Close() error {
	p.closeOnce.Do(func() {
		_ = p.stdin.Close()

		killed := false
		select {
		case <-p.waitDone:
		case <-time.After(p.grace):
			killed = true
			killProcessTree(p.cmd)
			select {
			case <-p.waitDone:
			case <-time.After(killReapTimeout):
				p.closeStdout()
				p.closeErr = fmt.Errorf("acpexec: subprocess %s not reaped %s after kill (a descendant outside its process group may be holding its pipes)", p.cmd.Path, killReapTimeout)
				return
			}
		}

		p.closeStdout()
		p.closeErr = p.waitErr

		if killed && exitFromKill(p.waitErr) {
			p.closeErr = nil
		}
	})
	return p.closeErr
}

func (p *Process) closeStdout() {
	p.stdoutClosed.Store(true)
	_ = p.stdout.Close()
}

type LockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *LockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *LockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
