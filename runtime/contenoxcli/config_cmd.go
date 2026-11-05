package contenoxcli

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/locale"
	"github.com/contenox/runtime/runtime/reasoning"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/stateservice"
	"github.com/spf13/cobra"
)

var validConfigKeys = map[string]string{
	"default-model":                 "Default LLM model name (e.g. qwen3:8b)",
	"default-provider":              "Default LLM provider type (e.g. ollama, openai, anthropic, openrouter, gemini)",
	"default-alt-model":             "Optional second model name, for agents that use one.",
	"default-alt-provider":          "Optional provider type for the second model.",
	"default-autocomplete-model":    "Optional code-completion model name, independent from default-model.",
	"default-autocomplete-provider": "Optional code-completion provider type, independent from default-provider.",
	"default-max-tokens":            "Optional default response token cap.",
	"default-think":                 "Default reasoning level: auto, off, minimal, low, medium, high, xhigh.",
	"default-chain":                 "Default chain file path (relative to .contenox/ or absolute)",
	"hitl-policy-name":              "Active HITL policy file name (e.g. hitl-policy-strict.json). Empty = use hitl-policy-default.json.",
	"telemetry-enabled":             "Enable writing telemetry logs to <data-dir>/telemetry.log (true/false)",
	"update-check":                  "Enable automatic update availability checks (true/false). Set false for zero-trust/air-gapped environments.",
	"default-mission-agent":         "Default declared agent fired by '/mission <intent>' and 'contenox-runtime mission fire' with no --agent.",
	"default-mission-policy":        "Default mission envelope (HITL policy) used when '/mission' or 'contenox-runtime mission fire' names none.",
	"unofficial-sign-ins":           "Allow sign-ins the vendor does not support for third-party apps, such as a Claude subscription or GitHub Copilot (true/false). Off by default; may break or put the account at risk.",
	"locale":                        "Language tag agents see as {{var:locale}} (e.g. de-DE). Empty = detected from the operating system.",
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage persistent CLI settings (default model, provider, agent, approval policy).",
	Long: `Store and retrieve persistent CLI defaults backed by SQLite.

Global keys (shared across all projects): default-model, default-provider, default-alt-model, default-alt-provider, default-autocomplete-model, default-autocomplete-provider, default-max-tokens, default-think, telemetry-enabled, update-check, default-mission-agent, default-mission-policy, locale
Workspace keys (scoped to current project): default-chain, hitl-policy-name

Supported keys:
  default-model                  Default LLM model name (e.g. qwen3:8b)
  default-provider               Default LLM provider type (e.g. ollama, openai, anthropic, openrouter, gemini)
  default-alt-model              Optional second model, for agents that use one
  default-alt-provider           Optional provider for the second model
  default-autocomplete-model     Optional code-completion model, separate from chat
  default-autocomplete-provider  Optional code-completion provider, separate from chat
  default-max-tokens             Optional response token cap
  default-think                  Default reasoning level: auto, off, minimal, low, medium, high, xhigh
  telemetry-enabled              Enable local telemetry logs (true/false)
  update-check                   Enable automatic update checks (true/false)
  default-chain                  Default chain file path
  hitl-policy-name               Active HITL policy file name (e.g. hitl-policy-strict.json)
  default-mission-agent          Default agent fired by /mission and 'mission fire' with no --agent
  default-mission-policy         Default mission envelope (HITL policy) when none is named
  locale                         Language tag agents see as {{var:locale}}, e.g. de-DE (empty = detected from the OS)`,
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a persistent config value.",
	Long: `Set a persistent CLI default stored in the SQLite database.

Global keys (default-model, default-provider, default-alt-model, default-alt-provider, default-autocomplete-model, default-autocomplete-provider, default-max-tokens, default-think, telemetry-enabled, update-check) are shared across all projects.
Workspace keys (default-chain, hitl-policy-name) are scoped to the current project
workspace and fall back to the global value when not set locally.

Examples:
  contenox-runtime config set default-model    qwen3:8b
  contenox-runtime config set default-provider ollama

  # Keep chat on a hosted provider, but run code completion on a local Ollama model:
  contenox-runtime config set default-provider openai
  contenox-runtime config set default-model    gpt-5.6
  contenox-runtime config set default-autocomplete-provider ollama
  contenox-runtime config set default-autocomplete-model    qwen3-coder:30b


  contenox-runtime config set default-autocomplete-provider mistral
  contenox-runtime config set default-autocomplete-model    mistral-code-fim-latest
  contenox-runtime config set default-max-tokens 8192
  contenox-runtime config set default-think    high
  contenox-runtime config set default-chain    .contenox/my-chain.json
  contenox-runtime config set hitl-policy-name hitl-policy-strict.json
  contenox-runtime config set locale           de-DE`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		key, value := args[0], args[1]
		if _, ok := validConfigKeys[key]; !ok {
			return fmt.Errorf("unknown key %q — valid keys: %s", key, validConfigKeyList())
		}
		if key == locale.Key {
			normalized, err := stateservice.NormalizeLocale(value)
			if err != nil {
				return err
			}
			value = normalized
		}
		if key == "default-max-tokens" {
			normalized, err := normalizeMaxTokensConfig(value)
			if err != nil {
				return err
			}
			value = normalized
		}
		if key == "default-think" {
			normalized, err := reasoning.Normalize(value)
			if err != nil {
				return err
			}
			value = normalized
		}
		db, store, workspaceID, err := openConfigDBWithWorkspace(cmd)
		if err != nil {
			return err
		}
		defer db.Close()

		ctx := libtracker.WithNewRequestID(context.Background())
		if err := clikv.WriteConfig(ctx, store, workspaceID, key, value); err != nil {
			return fmt.Errorf("failed to set %q: %w", key, err)
		}
		_, scope := clikv.ReadConfig(ctx, store, workspaceID, key)
		fmt.Fprintf(cmd.OutOrStdout(), "✓  %s = %s  (%s)\n", key, value, scope)
		return nil
	},
}

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Get a persistent config value.",
	Long: `Print the current value of a persistent CLI setting.

Examples:
  contenox-runtime config get default-model
  contenox-runtime config get default-provider
  contenox-runtime config get default-think
  contenox-runtime config get default-chain
  contenox-runtime config get hitl-policy-name
  contenox-runtime config get locale`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		key := args[0]
		if _, ok := validConfigKeys[key]; !ok {
			return fmt.Errorf("unknown key %q", key)
		}
		db, store, workspaceID, err := openConfigDBWithWorkspace(cmd)
		if err != nil {
			return err
		}
		defer db.Close()

		ctx := libtracker.WithNewRequestID(context.Background())
		val, scope := readConfigForDisplay(ctx, store, workspaceID, key)
		fmt.Fprintf(cmd.OutOrStdout(), "%s  (%s)\n", val, scope)
		return nil
	},
}

func readConfigForDisplay(ctx context.Context, store runtimetypes.Store, workspaceID, key string) (string, string) {
	if key == locale.Key {
		if v := locale.Override(ctx, store); v != "" {
			return v, "global"
		}
		return locale.Detect(), "detected"
	}
	return clikv.ReadConfig(ctx, store, workspaceID, key)
}

var configListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all persistent config values.",
	Long: `Print all known CLI config keys, their current values, and their scope.

Example:
  contenox-runtime config list`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		db, store, workspaceID, err := openConfigDBWithWorkspace(cmd)
		if err != nil {
			return err
		}
		defer db.Close()

		ctx := libtracker.WithNewRequestID(context.Background())
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "KEY\tVALUE\tSCOPE")
		for _, key := range validConfigKeyNames() {
			val, scope := readConfigForDisplay(ctx, store, workspaceID, key)
			fmt.Fprintf(w, "%s\t%s\t%s\n", key, val, scope)
		}
		return w.Flush()
	},
}

func normalizeMaxTokensConfig(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return "", fmt.Errorf("default-max-tokens must be a non-negative integer, got %q", value)
	}
	if n < 0 {
		return "", fmt.Errorf("default-max-tokens must be non-negative, got %d", n)
	}
	return strconv.Itoa(n), nil
}

func validConfigKeyList() string {
	return strings.Join(validConfigKeyNames(), ", ")
}

func validConfigKeyNames() []string {
	keys := make([]string, 0, len(validConfigKeys))
	for key := range validConfigKeys {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func getConfigKV(ctx context.Context, store runtimetypes.Store, key string) (string, error) {
	return clikv.Read(ctx, store, key), nil
}

func openConfigDB(cmd *cobra.Command) (libdb.DBManager, runtimetypes.Store, error) {
	dbPath, err := resolveDBPath(cmd)
	if err != nil {
		return nil, nil, err
	}
	ctx := libtracker.WithNewRequestID(context.Background())
	db, err := OpenDBAt(ctx, dbPath)
	if err != nil {
		return nil, nil, err
	}
	return db, runtimetypes.New(db.WithoutTransaction()), nil
}

func openConfigDBWithWorkspace(cmd *cobra.Command) (libdb.DBManager, runtimetypes.Store, string, error) {
	db, store, err := openConfigDB(cmd)
	if err != nil {
		return nil, nil, "", err
	}
	contenoxDir, _ := ResolveContenoxDir(cmd)
	workspaceID := ResolveWorkspaceID(contenoxDir)
	return db, store, workspaceID, nil
}

func init() {
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configGetCmd)
	configCmd.AddCommand(configListCmd)
}
