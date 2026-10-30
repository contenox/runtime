package libsandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/contenox/runtime/libtracker"
)

func Command(ctx context.Context, spec Spec, name string, args ...string) (*exec.Cmd, error) {
	tracker := spec.Tracker
	if tracker == nil {
		tracker = libtracker.NoopTracker{}
	}
	reportErr, reportChange, end := tracker.Start(ctx, "confine", "sandbox",
		"command", name, "workspace", spec.WorkspaceRoot)
	defer end()

	if strings.TrimSpace(name) == "" {
		err := fmt.Errorf("%w: name (the executable to confine) is required", ErrInvalidSpec)
		reportErr(err)
		return nil, err
	}
	if err := spec.validate(); err != nil {
		reportErr(err)
		return nil, err
	}
	// A caller-supplied PATH is part of the spec, so a bad one is diagnosed as
	// such on every host, before confinement availability is considered.
	if explicit, ok := spec.EnvSet["PATH"]; ok && strings.TrimSpace(explicit) != "" {
		if err := validatePATH(explicit, spec.Home, spec.FS); err != nil {
			reportErr(err)
			return nil, err
		}
	}
	// Confinement is the point of this package: where the wall cannot be
	// built, report that instead of running a command that looks confined.
	// This comes after spec validation so a malformed request keeps its own
	// diagnosis on every host.
	if err := Preflight(); err != nil {
		reportErr(err)
		return nil, err
	}

	cmd := exec.Command(name, args...)
	cmd.Dir = spec.WorkspaceRoot
	cmd.Env = scrubEnv(os.Environ(), spec.EnvAllow, spec.EnvSet, spec.Home)

	if _, set := spec.EnvSet["PATH"]; !set {
		cmd.Env = OverlayEnv(cmd.Env, map[string]string{
			"PATH": confinedPATH(os.Getenv("PATH"), spec.Home, spec.FS),
		})
	}

	if err := validatePATH(lookupEnv(cmd.Env, "PATH"), spec.Home, spec.FS); err != nil {
		reportErr(err)
		return nil, err
	}

	if err := applyIsolation(ctx, cmd, spec, tracker); err != nil {
		err = fmt.Errorf("libsandbox: apply isolation for %q: %w", name, err)
		reportErr(err)
		return nil, err
	}

	reportChange(name, map[string]any{
		"dir":  cmd.Dir,
		"args": len(args),
		"fs":   len(spec.FS),
		"net":  len(spec.Net),
		"env":  len(cmd.Env),
	})
	return cmd, nil
}
