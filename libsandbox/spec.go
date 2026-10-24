package libsandbox

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/contenox/runtime/libtracker"
)

const (
	ModeRO = "ro"
	ModeRW = "rw"
)

var ErrInvalidSpec = errors.New("libsandbox: invalid spec")

var ErrInvalidCarveout = errors.New("libsandbox: invalid carve-out")

var ErrIsolation = errors.New("libsandbox: isolation failure")

type Spec struct {
	WorkspaceRoot string

	Home string

	EnvAllow []string

	EnvSet map[string]string

	FS []FSCarveout

	Net []NetCarveout

	AllowPrivateEgress bool

	NetworkWall bool

	Tracker libtracker.ActivityTracker

	SyscallTap bool
}

type FSCarveout struct {
	Path  string `json:"path"`
	Mode  string `json:"mode"`
	Needs string `json:"needs"`
}

type NetCarveout struct {
	Host  string `json:"host"`
	Ports []int  `json:"ports,omitempty"`
	Needs string `json:"needs"`
}

func (s Spec) validate() error {
	if strings.TrimSpace(s.WorkspaceRoot) == "" {
		return fmt.Errorf("%w: WorkspaceRoot is required (the one writable work dir the agent is confined to)", ErrInvalidSpec)
	}
	if strings.TrimSpace(s.Home) == "" {
		return fmt.Errorf("%w: Home is required (the scoped HOME that keeps the real home out of reach)", ErrInvalidSpec)
	}
	if !s.NetworkWall {
		if len(s.Net) > 0 {
			return fmt.Errorf("%w: Net carve-outs require NetworkWall — the network wall that makes them reachable is namespaced and off by default; enable NetworkWall to confine the network, or drop the carve-outs", ErrInvalidSpec)
		}
		if s.AllowPrivateEgress {
			return fmt.Errorf("%w: AllowPrivateEgress requires NetworkWall — it only governs how the namespaced network wall resolves carve-outs", ErrInvalidSpec)
		}
	}
	for i, c := range s.FS {
		if err := validateFSCarveout(c); err != nil {
			return fmt.Errorf("%w: FS[%d]: %w", ErrInvalidCarveout, i, err)
		}
	}
	for i, c := range s.Net {
		if err := validateNetCarveout(c); err != nil {
			return fmt.Errorf("%w: Net[%d]: %w", ErrInvalidCarveout, i, err)
		}
	}
	return nil
}

func validateFSCarveout(c FSCarveout) error {
	if strings.TrimSpace(c.Path) == "" {
		return errors.New("path is required")
	}
	if hasTraversal(c.Path) {
		return fmt.Errorf("path %q must not contain a %q traversal segment", c.Path, "..")
	}
	if c.Mode != ModeRO && c.Mode != ModeRW {
		return fmt.Errorf("mode %q must be %q or %q", c.Mode, ModeRO, ModeRW)
	}
	if strings.TrimSpace(c.Needs) == "" {
		return errors.New("needs is required: every carve-out must justify why the agent breaks without it")
	}
	return nil
}

func validateNetCarveout(c NetCarveout) error {
	if strings.TrimSpace(c.Host) == "" {
		return errors.New("host is required")
	}
	if strings.TrimSpace(c.Needs) == "" {
		return errors.New("needs is required: every carve-out must justify why the agent breaks without it")
	}
	for _, port := range c.Ports {
		if port < 1 || port > 65535 {
			return fmt.Errorf("port %d is out of range (a TCP port must be 1-65535)", port)
		}
	}
	return nil
}

func hasTraversal(p string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(p), "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}
