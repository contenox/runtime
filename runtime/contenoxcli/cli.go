package contenoxcli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/contenox/runtime/runtime/homedir"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/locale"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/project"
	"github.com/contenox/runtime/runtime/reasoning"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/version"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func CLIVersion() string {
	return version.Get()
}

func cliVersion() string {
	return version.Get()
}

const DefaultWorkspaceID = "00000000-0000-0000-0000-000000000002"

const (
	defaultOllama  = "http://127.0.0.1:11434"
	defaultContext = 0
	defaultTimeout = 5 * time.Minute
)

var reservedSubcommands = map[string]bool{"init": true, "chat": true, "help": true, "completion": true, "session": true, "run": true, "tools": true, "mcp": true, "backend": true, "agent": true, "config": true, "model": true, "models": true, "doctor": true, "version": true, "state": true, "acp": true, "acpx": true, "setup": true, "cache": true, "serve": true, "update": true, "approvals": true, "fleet": true, "mission": true, "workspace": true, "sandbox": true, "shell-env": true}

func Main() {
	args := os.Args[1:]
	onlyHelp := len(args) == 0
	if !onlyHelp {
		allRootFlags := true
		for _, a := range args {
			if a != "--help" && a != "-h" && a != "--version" && a != "-v" {
				allRootFlags = false
				break
			}
		}
		onlyHelp = allRootFlags
	}
	if sub := dispatchSubcommand(args, onlyHelp); sub != "" {
		rootCmd.SetArgs(append([]string{sub}, args...))
	}
	err := rootCmd.Execute()
	_ = modelrepo.Shutdown()
	if err != nil {
		recordStartupFailure(err)
		var ee *exitError
		if errors.As(err, &ee) {
			os.Exit(ee.code)
		}
		fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
		os.Exit(1)
	}
}

func containsExperimentalACPFlag(args []string) bool {
	return slices.Contains(args, "--experimental-acp")
}

func dispatchSubcommand(args []string, onlyHelp bool) string {
	switch {
	case containsExperimentalACPFlag(args) && !firstNonFlagIsReserved(args):
		return "acp"
	case !onlyHelp && !firstNonFlagIsReserved(args):
		return "chat"
	}
	return ""
}

func recordStartupFailure(execErr error) {
	defer func() { _ = recover() }()
	dir, err := homedir.Dir()
	if err != nil {
		return
	}
	f, openErr := os.OpenFile(filepath.Join(dir, "telemetry.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if openErr != nil {
		return
	}
	defer f.Close()
	logger := slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelInfo}))
	tr := libtracker.NewLogActivityTracker(logger)
	reportErr, _, end := tr.Start(context.Background(), "exec", "cli",
		"argv", strings.Join(os.Args[1:], " "),
		"version", CLIVersion(),
	)
	reportErr(execErr)
	end()
}

func firstNonFlagIsReserved(args []string) bool {
	boolFlags := map[string]bool{
		"--shell": true, "--trace": true, "--steps": true, "--raw": true,
		"--no-delete-models": true, "--editor": true,
		"-e": true, "-h": true, "--help": true, "-v": true, "--version": true,
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			if i+1 < len(args) {
				return reservedSubcommands[args[i+1]]
			}
			return false
		}
		if strings.HasPrefix(a, "--") {
			if strings.Contains(a, "=") || boolFlags[a] {
				continue
			}
			i++
			continue
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			if len(a) == 2 {
				i++
			}
			continue
		}
		return reservedSubcommands[a]
	}
	return false
}

var rootCmd = &cobra.Command{
	Use:   "contenox-runtime",
	Short: "Contenox: an AI agent you run in a project folder.",
	Long: `Contenox runs an AI agent in your project folder. It reads your files, runs
tools and shell commands, and asks before it changes anything. Sessions, settings,
and approvals are stored in SQLite on your machine. Use a cloud provider, a
ChatGPT subscription, or a local Ollama server.

  Quickstart:
    contenox-runtime setup                         # pick a provider, model, and API key
    contenox-runtime init                          # set up .contenox/ in this project
    contenox-runtime "list files in this folder"   # chat in the current session

  Inspect models:
    contenox-runtime model list                    # models exposed by registered live backends

  Or register an LLM backend manually:
    # Local Ollama daemon
    ollama serve && ollama pull qwen3:8b
    contenox-runtime backend add ollama --type ollama
    contenox-runtime config set default-provider ollama
    contenox-runtime config set default-model qwen3:8b

    # Hosted Ollama Cloud
    contenox-runtime backend add ollama-cloud --type ollama --url https://ollama.com/api --api-key-env OLLAMA_API_KEY
    contenox-runtime config set default-provider ollama

    # Google Gemini (no GPU required)
    contenox-runtime backend add gemini --type gemini --api-key-env GEMINI_API_KEY
    contenox-runtime config set default-model  gemini-flash-latest
    contenox-runtime config set default-provider gemini

    # OpenAI
    contenox-runtime backend add openai --type openai --api-key-env OPENAI_API_KEY
    contenox-runtime config set default-model    gpt-5.6
    contenox-runtime config set default-provider openai

    # Anthropic
    contenox-runtime backend add anthropic --type anthropic --api-key-env ANTHROPIC_API_KEY
    contenox-runtime config set default-model    claude-sonnet-5-5
    contenox-runtime config set default-provider anthropic

    # OpenRouter
    contenox-runtime backend add openrouter --type openrouter --api-key-env OPENROUTER_API_KEY
    contenox-runtime config set default-model    deepseek/deepseek-chat-v3-5
    contenox-runtime config set default-provider openrouter

  See 'contenox-runtime backend add --help' for every provider type.

  Scope note:
    Backends and config are GLOBAL (stored in .contenox-harness/local.db in your
    home folder; the desktop app uses its own database, see --db).
    Agent files (.contenox/) are LOCAL to each project directory, like .git/.
    Run 'contenox-runtime init' once per project to create them.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Run a stateful chat session (default when no subcommand is given).",
	Long: `Send a message to the active chat session and get a response.
Input is passed as positional args, --input, or piped via stdin.

  contenox-runtime "what can you do?"
  echo "summarise README.md" | contenox-runtime
  contenox-runtime chat --shell "list files in the current dir"

Sessions persist conversation history across invocations (stored in SQLite).
Each session remembers previous messages so the model has context.
The first run auto-creates a "default" session. Manage sessions with:

  contenox-runtime session list              list active-scope sessions (* = active)
  contenox-runtime session list --all        list every session across the whole DB
  contenox-runtime session new <name>        create a new named session (becomes active)
  contenox-runtime session switch <name>     switch to a different session
  contenox-runtime session show [name|id]    print a session (active, by name, or by id)
  contenox-runtime session delete <name>     delete a session and all its messages
  contenox-runtime session workspaces        list workspaces and namespaces (whole DB)
  contenox-runtime session fork --summary    compact older history into a summary and continue
                                     in a new session (useful when context fills up)

Giving the model tools (file system and shell access):

  --local-exec-allowed-dir <dir>     allow local_fs tools inside <dir>
  --shell                            enable local_shell (the agent's tool policy decides which commands run)

Human-in-the-loop is on by default. The workflow pauses for terminal approval before
write_file, sed, and local_shell calls. The active policy is defined in
~/.contenox-harness/hitl-policy-default.json (override per workspace via
.contenox/hitl-policy-*.json or via 'contenox-runtime config set hitl-policy-name').

  --auto                             non-interactive mode: skip approval prompts
                                     entirely. Use only in trusted environments
                                     or for scripted workflows.

Examples:
  # Chat with file system access to the current project:
  contenox-runtime chat --local-exec-allowed-dir . "summarise the README"

  # Shell access (the agent's tool policy decides which commands run; the shipped agents allow common dev tools):
  contenox-runtime chat --shell "suggest a commit message from git diff"

  # Non-interactive shell run — no approvals, runs everything allowed by policy (USE WITH CARE):
  contenox-runtime chat --shell --local-exec-allowed-dir . --auto "refactor main.go to use slog"

  # Trim context: only send last 10 messages from session history to the model:
  contenox-runtime chat --trim 10 "let's continue where we left off"

  # Show last 6 turns of the conversation after the reply:
  contenox-runtime chat --last 6 "hello"`,
	Args: cobra.ArbitraryArgs,
	RunE: runChat,
}

var initCmd = &cobra.Command{
	Use:   "init [provider]",
	Short: "Set up .contenox/ in this project with the default agents.",
	Long: `Create the .contenox/ directory and populate it with the default agent files.

This writes agents.toml and the shipped agent declarations under agents/, and
compiles them into .generated/.

After init, register a backend, make sure the runtime can see a model, then set your defaults:

  # Local Ollama daemon:
  ollama pull qwen3:8b
  contenox-runtime backend add ollama --type ollama
  contenox-runtime config set default-provider ollama
  contenox-runtime config set default-model qwen3:8b

  # Hosted Ollama Cloud:
  contenox-runtime backend add ollama-cloud --type ollama --url https://ollama.com/api --api-key-env OLLAMA_API_KEY
  contenox-runtime config set default-provider ollama
  contenox-runtime config set default-model gpt-oss:20b

  # OpenAI:
  contenox-runtime backend add openai --type openai --api-key-env OPENAI_API_KEY
  contenox-runtime config set default-provider openai
  contenox-runtime config set default-model gpt-5.6

  # Anthropic:
  contenox-runtime backend add anthropic --type anthropic --api-key-env ANTHROPIC_API_KEY
  contenox-runtime config set default-provider anthropic
  contenox-runtime config set default-model claude-sonnet-5-5

  # Google Gemini:
  contenox-runtime backend add gemini --type gemini --api-key-env GEMINI_API_KEY
  contenox-runtime config set default-provider gemini
  contenox-runtime config set default-model gemini-3.1-pro-preview

  # OpenRouter:
  contenox-runtime backend add openrouter --type openrouter --api-key-env OPENROUTER_API_KEY
  contenox-runtime config set default-provider openrouter
  contenox-runtime config set default-model deepseek/deepseek-chat-v3-5

  # Optional code-completion model, independent from chat:
  contenox-runtime config set default-autocomplete-provider ollama
  contenox-runtime config set default-autocomplete-model qwen3-coder:30b

Use --force to overwrite existing files, or --update to refresh unchanged default files to the latest version.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInitCmd,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the contenox-runtime version",
	Long: `Print the contenox-runtime version string.

This is the subcommand form of 'contenox-runtime --version' and exists so that typing
'contenox-runtime version' is not mistaken for chat input by the default run command.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("%s version %s\n", cmd.Root().Name(), cmd.Root().Version)
	},
}

func init() {
	v := cliVersion()
	rootCmd.Version = v
	rootCmd.Short = fmt.Sprintf("Contenox v%s: an AI agent you run in a project folder.", v)
	rootCmd.Long = fmt.Sprintf("Version: %s\n\n%s", v, rootCmd.Long)

	f := rootCmd.PersistentFlags()
	f.String("db", "", "SQLite database path (default: the .contenox-harness/local.db file in your home folder; the desktop app keeps its own database under its app-data folder, on Windows %APPDATA%\\ContenoxHarness\\local.db, and passes it with this flag)")
	f.String("data-dir", "", "Override the .contenox data directory path")
	f.String("ollama", defaultOllama, "Ollama base URL")
	f.String("model", "", "Model name (task/chat/embed). Overrides config default-model; no built-in default.")
	f.String("provider", "", "Provider type override. See 'contenox-runtime backend add --help' for supported backend types.")
	f.String("alt-model", "", "Second model for agents that use one. Overrides config default-alt-model.")
	f.String("alt-provider", "", "Provider type for the second model. Overrides config default-alt-provider.")
	f.Int("max-tokens", 0, "Response token cap. Overrides config default-max-tokens when set.")
	f.Int("context", defaultContext, "Context length")
	f.Bool("no-delete-models", true, "Legacy compatibility flag; OSS runtime model deletion is disabled.")
	_ = f.MarkHidden("no-delete-models")
	f.String("chain", "", "Path to a chain JSON file to run instead of the default agent. Falls back to default-chain in config, then the agent in agents/contenox/")
	f.String("input", "", "Input text (default: positional args or stdin if piped)")
	f.Bool("shell", false, "Enable the local_shell tools (use only in trusted environments)")
	f.String("local-exec-allowed-dir", "", "If set, local_shell may only run scripts/binaries under this directory")
	f.Duration("timeout", defaultTimeout, "Maximum execution time (e.g., 5m, 1h)")
	f.Bool("trace", false, "Stream task-step events to stderr in real time")

	f.Bool("steps", false, "Print execution steps after the result")
	f.Bool("raw", false, "Print full output (e.g. entire chat JSON)")
	f.String("think", "", "Set reasoning level for supported models: auto, off, minimal, low, medium, high, xhigh (default: config default-think, then high)")
	f.BoolP("editor", "e", false, "Open $EDITOR (or $VISUAL, fallback nano) to compose the prompt; piped stdin is preloaded as reference")

	rootCmd.AddCommand(initCmd, chatCmd, sessionCmd, runCmd, toolsCmd, doctorCmd, versionCmd)
	rootCmd.AddCommand(mcpCmd)
	rootCmd.AddCommand(backendCmd)
	rootCmd.AddCommand(agentCmd)
	rootCmd.AddCommand(cacheCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(modelCmd)
	rootCmd.AddCommand(stateCmd)
	rootCmd.AddCommand(acpCmd)
	rootCmd.AddCommand(acpxCmd)
	rootCmd.AddCommand(setupCmd)
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(approvalsCmd)
	rootCmd.AddCommand(fleetCmd)
	rootCmd.AddCommand(missionCmd)
	rootCmd.AddCommand(workspaceCmd)
	rootCmd.AddCommand(sandboxCmd)
	rootCmd.AddCommand(shellEnvCmd)

	rootCmd.InitDefaultHelpCmd()
	initCmd.Flags().BoolP("force", "f", false, "Overwrite existing files")
	initCmd.Flags().Bool("update", false, "Update unchanged default files to the latest version")
	initCmd.Flags().Bool("project", false, "Create a project marker in the CURRENT directory (a fresh workspace id), instead of reusing an ancestor's .contenox")
	initCmd.Flags().String("name", "", "Friendly project name for the marker (default: the directory name)")

	chatCmd.Flags().Int("trim", 0, "Only send the last N messages from session history to the model (0 = send all)")
	chatCmd.Flags().Int("last", 0, "Print last N user/assistant turns after the reply (0 = only print new reply)")
	chatCmd.Flags().Bool("auto", false, "Non-interactive mode: disable HITL approval prompts. Default is HITL on; tools route through the active hitl-policy. Use --auto only in trusted/scripted contexts.")

}

func setupTelemetryLogging(ctx context.Context, store runtimetypes.Store, contenoxDir string) (func(), error) {
	enabledStr := clikv.Read(ctx, store, "telemetry-enabled")
	if enabledStr != "true" {
		return func() {}, nil
	}

	logPath := filepath.Join(contenoxDir, "telemetry.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return func() {}, fmt.Errorf("failed to open telemetry log: %w", err)
	}

	mw := io.MultiWriter(os.Stderr, f)
	slog.SetDefault(slog.New(slog.NewTextHandler(mw, &slog.HandlerOptions{Level: slog.LevelInfo})))
	return func() { f.Close() }, nil
}

func ResolveContenoxDir(cmd *cobra.Command) (string, error) {
	if cmd != nil {
		dataDir, _ := cmd.Root().PersistentFlags().GetString("data-dir")
		if dataDir != "" {
			return filepath.Abs(dataDir)
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	home, _ := os.UserHomeDir()
	dir := cwd
	for {
		if home != "" && dir == home {
			return filepath.Join(cwd, ".contenox"), nil
		}
		candidate := filepath.Join(dir, ".contenox")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			if _, werr := os.Stat(filepath.Join(candidate, "workspace.id")); werr == nil {
				return candidate, nil
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return filepath.Join(cwd, ".contenox"), nil
		}
		dir = parent
	}
}

func controlPlaneDirs(contenoxDir string) []string {
	dirs := []string{contenoxDir}
	if home, err := globalContenoxDir(); err == nil {
		dirs = append(dirs, home)
	}
	return dirs
}

func ResolveWorkspaceID(contenoxDir string) string {
	if m, ok := project.ReadFromContenoxDir(contenoxDir); ok && m.ID != "" {
		return m.ID
	}
	return DefaultWorkspaceID
}

func runInitCmd(cmd *cobra.Command, args []string) error {
	force, _ := cmd.Flags().GetBool("force")
	update, _ := cmd.Flags().GetBool("update")
	projectMode, _ := cmd.Flags().GetBool("project")
	rawName, _ := cmd.Flags().GetString("name")
	projectName, err := project.NormalizeName(rawName)
	if err != nil {
		return err
	}
	provider := ""
	if len(args) > 0 {
		provider = args[0]
	}

	var contenoxDir string
	if projectMode {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to resolve current directory: %w", err)
		}
		contenoxDir, projectName = resolveProjectInit(cwd, projectName)
		if err := RunInit(cmd.OutOrStdout(), cmd.ErrOrStderr(), force, update, provider, contenoxDir, projectName); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(),
			"To let sessions open it from the app: contenox-runtime workspace add %s\n", cwd)

		return nil
	}
	contenoxDir, err = ResolveContenoxDir(cmd)
	if err != nil {
		return fmt.Errorf("failed to resolve .contenox dir: %w", err)
	}
	return RunInit(cmd.OutOrStdout(), cmd.ErrOrStderr(), force, update, provider, contenoxDir, projectName)
}

func resolveProjectInit(cwd, name string) (contenoxDir, projectName string) {
	contenoxDir = filepath.Join(cwd, project.ContenoxDirName)
	projectName = name
	if strings.TrimSpace(projectName) == "" {
		projectName = filepath.Base(filepath.Dir(contenoxDir))
	}
	return contenoxDir, projectName
}

func runChat(cmd *cobra.Command, args []string) error {
	flags := cmd.Root().Flags()
	useEditor, _ := flags.GetBool("editor")

	if len(args) == 1 && args[0] == "help" && !flags.Changed("input") && !useEditor {
		_ = cmd.Help()
		return nil
	}

	if len(args) == 0 && !flags.Changed("input") && !useEditor {
		if stat, err := os.Stdin.Stat(); err != nil || (stat.Mode()&os.ModeCharDevice) != 0 {
			_ = cmd.Usage()
			return nil
		}
	}

	contenoxDir, err := ResolveContenoxDir(cmd)
	if err != nil {
		return fmt.Errorf("failed to resolve .contenox dir: %w", err)
	}

	dbPath, err := resolveDBPath(cmd)
	if err != nil {
		return err
	}
	dbCtx := libtracker.WithNewRequestID(context.Background())
	db, err := OpenDBAt(dbCtx, dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	closeLogs, err := setupTelemetryLogging(dbCtx, runtimetypes.New(db.WithoutTransaction()), contenoxDir)
	if err != nil {
		slog.Warn("Failed to setup telemetry logging", "error", err)
	}
	defer closeLogs()

	store := runtimetypes.New(db.WithoutTransaction())

	changed := func(name string) bool { return flags.Changed(name) }

	configuredDefaultModel := ""
	if kv, _ := getConfigKV(dbCtx, store, "default-model"); kv != "" {
		configuredDefaultModel = kv
	}
	configuredDefaultProvider := ""
	if kv, _ := getConfigKV(dbCtx, store, "default-provider"); kv != "" {
		configuredDefaultProvider = kv
	}

	effectiveModel, _ := flags.GetString("model")
	if !changed("model") {
		if configuredDefaultModel != "" {
			effectiveModel = configuredDefaultModel
		}
	}

	effectiveDefaultProvider := configuredDefaultProvider
	if changed("provider") {
		effectiveDefaultProvider, _ = flags.GetString("provider")
	}

	effectiveAltModel := ""
	if kv, _ := getConfigKV(dbCtx, store, "default-alt-model"); kv != "" {
		effectiveAltModel = kv
	}
	if changed("alt-model") {
		effectiveAltModel, _ = flags.GetString("alt-model")
	}

	effectiveAltProvider := ""
	if kv, _ := getConfigKV(dbCtx, store, "default-alt-provider"); kv != "" {
		effectiveAltProvider = kv
	}
	if changed("alt-provider") {
		effectiveAltProvider, _ = flags.GetString("alt-provider")
	}

	effectiveMaxTokens, err := resolveEffectiveMaxTokens(dbCtx, store, flags)
	if err != nil {
		return err
	}

	effectiveContext, _ := flags.GetInt("context")
	effectiveNoDeleteModels, _ := flags.GetBool("no-delete-models")

	effectiveChain, _ := flags.GetString("chain")
	if effectiveChain == "" && !changed("chain") {
		if kv, _ := getConfigKV(dbCtx, store, "default-chain"); kv != "" && !retiredChainFile(kv) {
			effectiveChain = kv
			if !filepath.IsAbs(effectiveChain) {
				if resolved, rerr := lookupSystemFile(contenoxDir, effectiveChain); rerr == nil {
					effectiveChain = resolved
				} else {
					effectiveChain = filepath.Join(contenoxDir, effectiveChain)
				}
			}
		}
	}
	if effectiveChain == "" && !changed("chain") {
		resolved, rerr := resolveCompiledAgentChain(dbCtx, contenoxDir, chatAgentChainID)
		if rerr != nil {
			fmt.Fprintln(os.Stderr, rerr.Error())
			fmt.Fprintln(os.Stderr, "Pass --chain explicitly, or set one with 'contenox-runtime config set default-chain <file>'.")
			return errChainRequired
		}
		effectiveChain = resolved
	}

	effectiveEnableLocalExec, _ := flags.GetBool("shell")
	effectiveLocalExecAllowedDir, _ := flags.GetString("local-exec-allowed-dir")

	effectiveTracing, _ := flags.GetBool("trace")
	effectiveSteps, _ := flags.GetBool("steps")
	effectiveRaw, _ := flags.GetBool("raw")

	var inputValue string
	var inputPassed bool
	if useEditor {
		var seed []byte
		if data, ok, err := readStdinIfAvailable(maxCLIStdinBytes); err != nil {
			return err
		} else if ok {
			seed = []byte(data)
		}
		prompt, err := captureFromEditor(seed, effectiveModel)
		if err != nil {
			if errors.Is(err, errEmptyPrompt) {
				fmt.Fprintln(cmd.ErrOrStderr(), "aborted due to empty prompt")
				return errPromptAborted
			}
			return err
		}
		inputValue = prompt
		inputPassed = true
	} else if changed("input") {
		rawInput, _ := flags.GetString("input")
		inputValue, err = resolveInputFlagValue("--input", rawInput)
		if err != nil {
			return err
		}
		inputPassed = true
	} else if len(args) > 0 {
		inputValue = strings.Join(args, " ")
	}

	timeout, _ := flags.GetDuration("timeout")
	timeoutCtx, timeoutCancel := context.WithTimeout(libtracker.WithNewRequestID(context.Background()), timeout)
	defer timeoutCancel()

	ctx, stop := signal.NotifyContext(timeoutCtx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	effectiveThink, err := resolveEffectiveThink(dbCtx, store, flags)
	if err != nil {
		return err
	}
	autoMode, _ := cmd.Flags().GetBool("auto")
	effectiveHITL := !autoMode
	historyTrim, _ := cmd.Flags().GetInt("trim")
	lastN, _ := cmd.Flags().GetInt("last")

	opts := chatOpts{
		EffectiveDB:                  dbPath,
		EffectiveChain:               effectiveChain,
		EffectiveDefaultModel:        effectiveModel,
		EffectiveDefaultProvider:     effectiveDefaultProvider,
		EffectiveConfiguredModel:     configuredDefaultModel,
		EffectiveConfiguredProvider:  configuredDefaultProvider,
		EffectiveAltDefaultModel:     effectiveAltModel,
		EffectiveAltDefaultProvider:  effectiveAltProvider,
		EffectiveMaxTokens:           effectiveMaxTokens,
		EffectiveContext:             effectiveContext,
		EffectiveNoDeleteModels:      effectiveNoDeleteModels,
		EffectiveEnableLocalExec:     effectiveEnableLocalExec,
		EffectiveLocalExecAllowedDir: effectiveLocalExecAllowedDir,
		EffectiveTracing:             effectiveTracing,
		EffectiveSteps:               effectiveSteps,
		EffectiveHITL:                effectiveHITL,
		EffectiveRaw:                 effectiveRaw,
		EffectiveThink:               effectiveThink,
		EffectiveLocale:              locale.Resolve(dbCtx, store),
		HistoryTrim:                  historyTrim,
		LastN:                        lastN,
		InputValue:                   inputValue,
		InputFlagPassed:              inputPassed,
		ContenoxDir:                  contenoxDir,
	}
	return execChat(ctx, db, opts, cmd.OutOrStdout(), cmd.ErrOrStderr())
}

func resolveEffectiveThink(ctx context.Context, store runtimetypes.Store, flags *pflag.FlagSet) (string, error) {
	if flags != nil && flags.Changed("think") {
		v, _ := flags.GetString("think")
		level, err := reasoning.Normalize(v)
		if err != nil {
			return "", fmt.Errorf("--think: %w", err)
		}
		return level, nil
	}
	if store != nil {
		if kv, _ := getConfigKV(ctx, store, "default-think"); kv != "" {
			level, err := reasoning.Normalize(kv)
			if err != nil {
				return "", fmt.Errorf("config default-think: %w", err)
			}
			return level, nil
		}
	}
	return reasoning.Default, nil
}

func resolveEffectiveMaxTokens(ctx context.Context, store runtimetypes.Store, flags *pflag.FlagSet) (string, error) {
	if flags != nil && flags.Changed("max-tokens") {
		n, _ := flags.GetInt("max-tokens")
		if n < 0 {
			return "", fmt.Errorf("--max-tokens must be non-negative, got %d", n)
		}
		return strconv.Itoa(n), nil
	}
	if store != nil {
		if kv, _ := getConfigKV(ctx, store, "default-max-tokens"); kv != "" {
			return normalizeMaxTokensConfig(kv)
		}
	}
	return "", nil
}

func shouldPrintThinking(level string) bool {
	return reasoning.DisplayEnabled(level)
}

var (
	errChainRequired = &exitError{1}
	errPromptAborted = &exitError{1}
)

type exitError struct{ code int }

func (e *exitError) Error() string { return "exit" }
