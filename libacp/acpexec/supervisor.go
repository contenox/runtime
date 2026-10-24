package acpexec

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/contenox/runtime/libacp"
)

type Supervisor struct {
	Command func(ctx context.Context) *exec.Cmd

	MaxRestarts int

	Backoff func(attempt int) time.Duration

	OnRestart func(attempt int, cause error)

	SpawnOptions []Option
}

func (s *Supervisor) Serve(ctx context.Context, session func(ctx context.Context, proc *Process, attempt int) error) error {
	if s.Command == nil {
		return fmt.Errorf("acpexec: Supervisor.Command is required")
	}
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		proc, err := Spawn(ctx, s.Command(ctx), s.SpawnOptions...)
		if err != nil {
			return fmt.Errorf("%w: %w", libacp.ErrAgentStartFailed, err)
		}

		runErr := session(ctx, proc, attempt)
		_ = proc.Close()

		if runErr == nil {
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if libacp.IsStartupError(runErr) || !libacp.IsRetryableError(runErr) {
			return runErr
		}
		if attempt >= s.MaxRestarts {
			return runErr
		}

		if s.OnRestart != nil {
			s.OnRestart(attempt+1, runErr)
		}
		if s.Backoff != nil {
			if d := s.Backoff(attempt + 1); d > 0 {
				timer := time.NewTimer(d)
				select {
				case <-timer.C:
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				}
			}
		}
	}
}
