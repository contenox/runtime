package contenoxcli

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/contenox/runtime/libsandbox"
	"github.com/contenox/runtime/runtime/serverapi"
	"github.com/contenox/runtime/runtime/shellenvservice"
)

func resolveSandboxScrubs(config *serverapi.Config, injectGlobal func() map[string]string) (shell, terminal func([]string) []string) {
	extraAllow := libsandbox.ParseEnvList(config.SandboxEnvAllow)
	extraDeny := libsandbox.ParseEnvList(config.SandboxEnvDeny)
	shellFilter := libsandbox.EnvScrub(resolveScrubMode(config.SandboxShellScrub, libsandbox.ScrubDenySecrets), extraAllow, extraDeny)
	terminalFilter := libsandbox.EnvScrub(resolveScrubMode(config.SandboxTerminalScrub, libsandbox.ScrubOff), extraAllow, extraDeny)
	return composeShellEnv(shellFilter, injectGlobal), composeShellEnv(terminalFilter, injectGlobal)
}

func composeShellEnv(filter func([]string) []string, injectGlobal func() map[string]string) func([]string) []string {
	if filter == nil && injectGlobal == nil {
		return nil
	}
	return func(parent []string) []string {
		env := parent
		if filter != nil {
			env = filter(env)
		}
		if injectGlobal != nil {
			env = libsandbox.OverlayEnv(env, injectGlobal())
		}
		return env
	}
}

func newLiveGlobalShellEnv(svc shellenvservice.Service, ttl time.Duration) func() map[string]string {
	var (
		mu     sync.Mutex
		at     time.Time
		val    map[string]string
		loaded bool
	)
	return func() map[string]string {
		mu.Lock()
		defer mu.Unlock()
		if loaded && time.Since(at) < ttl {
			return val
		}
		vars, err := svc.Get(context.Background())
		if err != nil {
			slog.Warn("contenox-runtime serve: could not read global shell-env; keeping last known", "error", err)
			return val
		}
		val, at, loaded = vars, time.Now(), true
		return val
	}
}

func resolveScrubMode(raw, def string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def
	}
	if !libsandbox.ScrubModeValid(raw) {
		slog.Warn("contenox-runtime serve: unrecognized sandbox scrub mode; using default",
			"value", raw, "default", def)
		return def
	}
	return raw
}
