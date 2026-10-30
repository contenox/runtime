package contenoxcli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/contenox/runtime/libacp"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/acpsvc"
	"github.com/contenox/runtime/runtime/backendservice"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/reasoning"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

const (
	envDefaultModel       = "CONTENOX_DEFAULT_MODEL"
	envDefaultProvider    = "CONTENOX_DEFAULT_PROVIDER"
	envDefaultAltModel    = "CONTENOX_DEFAULT_ALT_MODEL"
	envDefaultAltProvider = "CONTENOX_DEFAULT_ALT_PROVIDER"
	envDefaultMaxTokens   = "CONTENOX_DEFAULT_MAX_TOKENS"
	envDefaultThink       = "CONTENOX_DEFAULT_THINK"
	envBaseURL            = "CONTENOX_BASE_URL"
)

func configValueWithEnv(ctx context.Context, db libdb.DBManager, key, envKey string) string {
	if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
		return v
	}
	return acpsvc.ReadConfigValue(ctx, db, key)
}

// liveDefaultsResolver builds the acpsvc.Deps.LiveDefaults callback: it re-reads
// the persisted default-* config on demand (the DB is shared by every runtime
// process, so edits made via the HTTP API or another process are seen).
// withEnv lets CONTENOX_DEFAULT_* env vars win (acp); pinned holds non-empty
// values that must never be overridden (explicit CLI flags).
func liveDefaultsResolver(db libdb.DBManager, withEnv bool, pinned acpsvc.LiveDefaults) func(context.Context) acpsvc.LiveDefaults {
	return func(ctx context.Context) acpsvc.LiveDefaults {
		get := func(key, envKey string) string {
			if withEnv {
				return configValueWithEnv(ctx, db, key, envKey)
			}
			return acpsvc.ReadConfigValue(ctx, db, key)
		}
		first := func(a, b string) string {
			if a != "" {
				return a
			}
			return b
		}
		d := acpsvc.LiveDefaults{
			Model:       first(pinned.Model, get("default-model", envDefaultModel)),
			Provider:    first(pinned.Provider, get("default-provider", envDefaultProvider)),
			AltModel:    first(pinned.AltModel, get("default-alt-model", envDefaultAltModel)),
			AltProvider: first(pinned.AltProvider, get("default-alt-provider", envDefaultAltProvider)),
		}
		if pinned.MaxTokens != "" {
			d.MaxTokens = pinned.MaxTokens
		} else if n, err := normalizeMaxTokensConfig(get("default-max-tokens", envDefaultMaxTokens)); err == nil {
			d.MaxTokens = n
		}
		if pinned.Think != "" {
			d.Think = pinned.Think
		} else if v := get("default-think", envDefaultThink); v != "" {
			if level, err := reasoning.Normalize(v); err == nil {
				d.Think = level
			}
		}
		return d
	}
}

func acpEnvSetupVars() []libacp.AuthEnvVar {
	notSecret := false
	providerKeys := make([]string, 0, len(setupProviders))
	for _, sp := range setupProviders {
		providerKeys = append(providerKeys, sp.key)
	}
	vars := []libacp.AuthEnvVar{
		{Name: envDefaultProvider, Label: "Provider (" + strings.Join(providerKeys, ", ") + ")", Secret: &notSecret},
		{Name: envDefaultModel, Label: "Model (defaults to the provider's default model)", Secret: &notSecret, Optional: true},
	}
	for _, sp := range setupProviders {
		if sp.needsAPIKey {
			vars = append(vars, libacp.AuthEnvVar{Name: sp.envKey, Label: sp.label + " API key", Optional: true})
		}
	}
	return vars
}

func completeEnvSetup(ctx context.Context, db libdb.DBManager) error {
	provider := strings.ToLower(strings.TrimSpace(os.Getenv(envDefaultProvider)))
	model := strings.TrimSpace(os.Getenv(envDefaultModel))
	if provider == "" {
		if model == "" {
			return fmt.Errorf("set %s (and optionally %s)", envDefaultProvider, envDefaultModel)
		}
		return fmt.Errorf("%s is set but %s is missing", envDefaultModel, envDefaultProvider)
	}

	var sp *setupProvider
	for i := range setupProviders {
		if setupProviders[i].key == provider {
			sp = &setupProviders[i]
			break
		}
	}
	if sp == nil {
		keys := make([]string, 0, len(setupProviders))
		for _, p := range setupProviders {
			keys = append(keys, p.key)
		}
		return fmt.Errorf("unknown provider %q in %s (choose one of: %s)", provider, envDefaultProvider, strings.Join(keys, ", "))
	}

	if model == "" {
		model = sp.defaultModel
	}
	if model == "" {
		return fmt.Errorf("set %s: provider %q has no default model", envDefaultModel, sp.key)
	}

	apiKey := ""
	if sp.needsAPIKey {
		apiKey = strings.TrimSpace(os.Getenv(sp.envKey))
		if apiKey == "" && !backendExists(ctx, db, sp.key) {
			return fmt.Errorf("set %s: provider %q needs an API key", sp.envKey, sp.key)
		}
	}

	baseURL := ""
	if sp.needsBaseURL {
		baseURL = strings.TrimSpace(os.Getenv(envBaseURL))
		if baseURL == "" && !backendExists(ctx, db, sp.key) {
			return fmt.Errorf("set %s: provider %q needs an endpoint URL", envBaseURL, sp.key)
		}
	}

	if err := registerSetupBackend(ctx, db, sp.key, apiKey, baseURL); err != nil {
		return err
	}

	store := runtimetypes.New(db.WithoutTransaction())
	if err := clikv.WriteConfig(ctx, store, "global", "default-provider", sp.key); err != nil {
		return fmt.Errorf("persist default-provider: %w", err)
	}
	if model != "" {
		if err := clikv.WriteConfig(ctx, store, "global", "default-model", model); err != nil {
			return fmt.Errorf("persist default-model: %w", err)
		}
	}
	return nil
}

func backendExists(ctx context.Context, db libdb.DBManager, providerType string) bool {
	svc := backendservice.New(db)
	backends, err := svc.List(ctx, nil, 100)
	if err != nil {
		return false
	}
	for _, b := range backends {
		if strings.EqualFold(b.Type, providerType) {
			return true
		}
	}
	return false
}
