//go:build linux

package libsandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"
)

const (
	shimSelfExe = "/proc/self/exe"

	envShimSentinel = "CONTENOX_SANDBOX_SHIM"

	envShimSpec = "CONTENOX_SANDBOX_SPEC"
)

func ShimMain() (handled bool, err error) {
	if os.Getenv(envShimSentinel) == "" {
		return false, nil
	}
	var plan isolationPlan
	if e := json.Unmarshal([]byte(os.Getenv(envShimSpec)), &plan); e != nil {
		return true, fmt.Errorf("%w: shim: decode plan: %v", ErrIsolation, e)
	}

	if plan.Loopback {
		if e := bringLoopbackUp(); e != nil {
			return true, fmt.Errorf("%w: shim: %w", ErrIsolation, e)
		}
	}

	if plan.Egress {
		if e := establishEgress(plan.EgressSockFD); e != nil {
			return true, fmt.Errorf("%w: shim: %w", ErrIsolation, e)
		}
	}

	runtime.LockOSThread()

	if plan.Tap {
		if e := installSyscallTap(plan.TapSockFD); e != nil {
			return true, fmt.Errorf("%w: shim: %w", ErrIsolation, e)
		}
	}

	if e := applyLandlock(plan); e != nil {
		return true, fmt.Errorf("%w: shim: %w", ErrIsolation, e)
	}

	childEnv := stripTransportEnv(os.Environ())
	if e := syscall.Exec(plan.Exec, plan.Args, childEnv); e != nil {
		return true, fmt.Errorf("%w: shim: exec %q: %w", ErrIsolation, plan.Exec, e)
	}
	return true, nil
}

func stripTransportEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, envShimSentinel+"=") || strings.HasPrefix(kv, envShimSpec+"=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
