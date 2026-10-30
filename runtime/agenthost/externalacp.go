package agenthost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/libacp/acpexec"
	"github.com/contenox/runtime/libsandbox"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/internal/winproc"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

const closeTimeout = 5 * time.Second

type ExternalACPAgent struct {
	Config runtimetypes.ExternalACPConfig

	Stderr io.Writer

	KillGrace time.Duration

	Tracker libtracker.ActivityTracker

	SelfSpawn bool
}

func NewExternalACPAgent(cfg runtimetypes.ExternalACPConfig) *ExternalACPAgent {
	return &ExternalACPAgent{Config: cfg}
}

var _ Agent = (*ExternalACPAgent)(nil)

func (a *ExternalACPAgent) Connect(ctx context.Context, harness libacp.Client) (*Handle, error) {
	if harness == nil {
		return nil, fmt.Errorf("agenthost: harness is required")
	}
	if err := a.Config.Validate(); err != nil {
		return nil, fmt.Errorf("agenthost: invalid external_acp config: %w", err)
	}

	switch a.Config.Transport {
	case runtimetypes.ExternalACPTransportStdio:
		return a.connectStdio(ctx, harness)
	case runtimetypes.ExternalACPTransportEndpoint:
		return nil, fmt.Errorf("agenthost: endpoint transport is not implemented yet (agent %q)", a.Config.URL)
	default:
		return nil, fmt.Errorf("agenthost: unknown transport %q", a.Config.Transport)
	}
}

func (a *ExternalACPAgent) connectStdio(ctx context.Context, harness libacp.Client) (*Handle, error) {
	cmd, err := buildAgentCmd(ctx, a)
	if err != nil {
		return nil, fmt.Errorf("agenthost: sandbox external ACP agent %q: %w", a.Config.Command, err)
	}

	var opts []acpexec.Option
	if a.Stderr != nil {
		opts = append(opts, acpexec.WithStderr(a.Stderr))
	}
	if a.KillGrace > 0 {
		opts = append(opts, acpexec.WithKillGrace(a.KillGrace))
	}

	proc, err := acpexec.Spawn(ctx, cmd, opts...)
	if err != nil {
		return nil, fmt.Errorf("agenthost: spawn external ACP agent %q: %w", a.Config.Command, err)
	}

	conn := libacp.NewClientSideConnection(proc, func(*libacp.ClientSideConnection) libacp.Client {
		return harness
	})

	runDone := make(chan error, 1)
	go func() { runDone <- conn.Run(ctx) }()

	closeFn := func() error {
		procErr := proc.Close()
		select {
		case runErr := <-runDone:
			if procErr != nil {
				return procErr
			}
			return runErr
		case <-time.After(closeTimeout):
			if procErr != nil {
				return procErr
			}
			return fmt.Errorf("agenthost: ClientSideConnection.Run did not exit within %s of Close", closeTimeout)
		}
	}

	return &Handle{Conn: conn, closeFn: closeFn}, nil
}

const sandboxDocsURL = "https://contenox.com/docs/guide/agent-sandbox/"

const sandboxCarveoutFile = ".contenox-harness/sandbox-carveouts.json"

func buildAgentCmd(ctx context.Context, a *ExternalACPAgent) (*exec.Cmd, error) {
	if a.SelfSpawn || selfInvocation(a.Config.Command) {
		return selfSpawnCmd(a), nil
	}
	if err := libsandbox.Preflight(); err != nil {
		return nil, fmt.Errorf("external agents run only inside the sandbox, which cannot be built on this host: %w — see %s", err, sandboxDocsURL)
	}
	if a.Config.Cwd == "" {
		return nil, errors.New("cwd is required to confine the agent (the wall needs a workspace; it will not default to the whole filesystem)")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve operator home for the sandbox: %w", err)
	}

	tracker := a.Tracker
	if tracker == nil {
		tracker = libtracker.NoopTracker{}
	}

	spec := libsandbox.Spec{
		WorkspaceRoot:      a.Config.Cwd,
		Home:               home,
		EnvAllow:           libsandbox.DefaultEnvPolicy().Resolve(os.Environ()),
		EnvSet:             a.Config.Env,
		FS:                 defaultAgentCarveouts(),
		AllowPrivateEgress: false,
		SyscallTap:         false,
		Tracker:            tracker,
	}

	if err := applyCarveoutFile(filepath.Join(home, sandboxCarveoutFile), &spec); err != nil {
		return nil, err
	}

	spec.NetworkWall = len(spec.Net) > 0 || networkWallOptIn()

	return libsandbox.Command(ctx, spec, a.Config.Command, a.Config.Args...)
}

func selfInvocation(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	self, err := os.Executable()
	if err != nil {
		return false
	}
	if !strings.ContainsRune(command, filepath.Separator) {
		resolved, lerr := exec.LookPath(command)
		if lerr != nil {
			return false
		}
		command = resolved
	}
	selfInfo, err := os.Stat(self)
	if err != nil {
		return false
	}
	cmdInfo, err := os.Stat(command)
	if err != nil {
		return false
	}
	return os.SameFile(selfInfo, cmdInfo)
}

func selfSpawnCmd(a *ExternalACPAgent) *exec.Cmd {
	cmd := exec.Command(a.Config.Command, a.Config.Args...)
	winproc.Hide(cmd)
	cmd.Dir = a.Config.Cwd
	env := os.Environ()
	for k, v := range a.Config.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env
	return cmd
}

func networkWallOptIn() bool {
	switch os.Getenv("CONTENOX_SANDBOX_NETWORK_WALL") {
	case "1", "true", "TRUE", "yes", "on":
		return true
	default:
		return false
	}
}

func defaultAgentCarveouts() []libsandbox.FSCarveout {
	return []libsandbox.FSCarveout{
		{Path: "~/.claude", Mode: libsandbox.ModeRO, Needs: "agent auth/config"},
		{Path: "~/.codex", Mode: libsandbox.ModeRO, Needs: "agent auth/config"},
		{Path: "~/.config/goose", Mode: libsandbox.ModeRO, Needs: "agent auth/config"},
	}
}

func applyCarveoutFile(path string, spec *libsandbox.Spec) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open sandbox carve-out file %q: %w", path, err)
	}
	defer f.Close()

	fs, net, err := libsandbox.LoadCarveouts(f)
	if err != nil {
		return fmt.Errorf("load sandbox carve-outs from %q: %w", path, err)
	}
	spec.FS = append(spec.FS, fs...)
	spec.Net = net
	return nil
}
