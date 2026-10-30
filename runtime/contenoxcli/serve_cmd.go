package contenoxcli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"os/signal"

	"github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/apiframework/middleware"
	"github.com/contenox/runtime/libacp"
	libbus "github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libkvstore"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/acpsvc"
	"github.com/contenox/runtime/runtime/agentdecl"
	"github.com/contenox/runtime/runtime/agentinstance"
	"github.com/contenox/runtime/runtime/agentregistryservice"
	"github.com/contenox/runtime/runtime/agentservice"
	"github.com/contenox/runtime/runtime/chainagents"
	"github.com/contenox/runtime/runtime/enginesvc"
	"github.com/contenox/runtime/runtime/fleetservice"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/internal/compatapi"
	"github.com/contenox/runtime/runtime/internal/fleetapi"
	internaltools "github.com/contenox/runtime/runtime/internal/tools"
	"github.com/contenox/runtime/runtime/localfileservice"
	"github.com/contenox/runtime/runtime/localtools"
	"github.com/contenox/runtime/runtime/missionchanges"
	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/contenox/runtime/runtime/missiontools"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/nativeturn"
	"github.com/contenox/runtime/runtime/operatorinbox"
	"github.com/contenox/runtime/runtime/oracletools"
	"github.com/contenox/runtime/runtime/presence"
	"github.com/contenox/runtime/runtime/reportrouter"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/serverapi"
	"github.com/contenox/runtime/runtime/shellenvservice"
	"github.com/contenox/runtime/runtime/shellsession"
	"github.com/contenox/runtime/runtime/stateservice"
	"github.com/contenox/runtime/runtime/taskchainservice"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/contenox/runtime/runtime/terminalservice"
	"github.com/contenox/runtime/runtime/toolsproviderservice"
	"github.com/contenox/runtime/runtime/version"
	"github.com/contenox/runtime/runtime/vfs"
	"github.com/contenox/runtime/runtime/workspacegrants"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const (
	defaultServeAddr = "127.0.0.1"
	defaultServePort = "32123"
	remoteBindAddr   = "0.0.0.0"
)

// serveBackendCycleBudget bounds startup discovery so a provider that answers slowly or not
// at all cannot keep the listener from binding; the state records the timeout and the next
// refresh probes again.
const serveBackendCycleBudget = 15 * time.Second

// catalogRefreshInterval is how often a serving runtime re-probes its providers. A provider
// that was down or slow at startup otherwise stays out of the model catalog until a restart,
// because nothing else asks the state to reconcile.
const catalogRefreshInterval = 60 * time.Second

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the Contenox HTTP server.",
	Long: `Start the Contenox HTTP API server.

Foundation routes:
  GET /health
  GET /version

The product API is served under /api (state, models, model-registry, backends,
setup-status, providers, tools, mcp-servers, task-chains, hitl-policies, task
execution, task events, terminal sessions). Chat, its HITL approvals, and
execution-state/event replay run over the /acp WebSocket instead (see
'contenox-runtime acp').

OpenAI- and Ollama-compatible endpoints are served for external clients:
point an OpenAI client's base URL at the server root (POST /v1/chat/completions,
GET /v1/models; also under /api/openai) or an Ollama client at the same host
(GET /api/tags, POST /api/chat, POST /api/generate). Requests execute through
the configured default chain and runtime defaults; a requested model is honored
when the default provider serves it, otherwise the default model is used.

Terminal routes are enabled by default on local serve under /api/terminal/sessions.
Set TERMINAL_ENABLED=false to disable them. TERMINAL_ALLOWED_ROOT defaults to the
workspace root. TERMINAL_MAX_SESSIONS defaults to 8 (0 = unlimited).

The server binds to 127.0.0.1:32123 by default (local-only). Pass --remote to
bind all interfaces (0.0.0.0) so other machines on your LAN can reach it, or set
ADDR/PORT to bind a specific address. TOKEN gates every API request and is
MANDATORY for any non-loopback bind (--remote or a LAN ADDR). The
token is resolved as: TOKEN env, else ~/.contenox-harness/serve-token.txt, else (with
--remote) a freshly generated token saved there (0600); programmatic clients
(contenox approvals/mission/fleet) discover the same file automatically. Serving
is plain HTTP — put a TLS-terminating reverse proxy in front for untrusted
networks. Chat needs a configured model; without one, serve still starts and
reports what is missing through /api/setup-status so a client can configure it.`,
	RunE: runServe,
}

func init() {
	serveCmd.Flags().StringArray("workspace-root", nil,
		"Directory a browser client may choose as a session workspace (repeatable). The serve directory is always the default; these extend the allowlist. Also settable via WORKSPACE_ROOTS (OS path-list separated) or as `contenox-runtime serve [dir]...` positional arguments.")
	registerOracleFlags(serveCmd)
	serveCmd.Flags().Bool("remote", false,
		"Serve on all network interfaces (0.0.0.0) so other machines on your LAN can reach the API. Auto-provisions a TOKEN (env, else ~/.contenox-harness/serve-token.txt, else generated and saved there) since a token is mandatory off loopback. A shorthand for ADDR=0.0.0.0; an explicit ADDR still wins. Plain HTTP — front with a TLS reverse proxy for untrusted networks.")
}

func resolveServeAddr(remote bool, envAddr string) string {
	if a := strings.TrimSpace(envAddr); a != "" {
		return a
	}
	if remote {
		return remoteBindAddr
	}
	return defaultServeAddr
}

func lanURLs(port string) []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var urls []string
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		if ip4 := ipNet.IP.To4(); ip4 != nil {
			urls = append(urls, "http://"+net.JoinHostPort(ip4.String(), port))
		}
	}
	return urls
}

func buildWorkspaceBaseRoots(cmd *cobra.Command, args []string, config *serverapi.Config, defaultRoot string) []string {
	roots := []string{defaultRoot}
	if len(args) > 1 {
		roots = append(roots, args[1:]...)
	}
	if flags, _ := cmd.Flags().GetStringArray("workspace-root"); len(flags) > 0 {
		roots = append(roots, flags...)
	}
	if config != nil {
		for _, r := range filepath.SplitList(config.WorkspaceRoots) {
			if strings.TrimSpace(r) != "" {
				roots = append(roots, r)
			}
		}
	}
	return roots
}

func runServe(cmd *cobra.Command, args []string) error {
	baseCtx := cmd.Context()
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	ctx, stop := signal.NotifyContext(baseCtx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	defer func() { _ = modelrepo.Shutdown() }()

	config := &serverapi.Config{}
	if err := serverapi.LoadConfig(config); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	remote, _ := cmd.Flags().GetBool("remote")
	config.Addr = resolveServeAddr(remote, config.Addr)
	if config.Port == "" {
		config.Port = defaultServePort
	}
	config.Token = strings.TrimSpace(config.Token)
	if config.Token == "" {
		if fileTok := readServeTokenFile(); fileTok != "" {
			config.Token = fileTok
			fmt.Fprintf(cmd.OutOrStdout(), "serve token: loaded from %s\n", serveTokenPathHint())
		} else if remote {
			gen, err := generateServeToken()
			if err != nil {
				return fmt.Errorf("generate serve token: %w", err)
			}
			if err := writeServeTokenFile(gen); err != nil {
				return fmt.Errorf("persist serve token to %s: %w", serveTokenPathHint(), err)
			}
			config.Token = gen
			fmt.Fprintf(cmd.OutOrStdout(),
				"serve token: generated and saved to %s (0600) — clients auto-discover it there; `cat` it to log in, or set TOKEN to override\n",
				serveTokenPathHint())
		}
	}
	if remote && config.Token == "" {
		return fmt.Errorf("--remote binds all network interfaces (%s) and requires a TOKEN; set one, or ensure %s is writable so serve can provision it",
			remoteBindAddr, serveTokenPathHint())
	}
	if err := serverapi.ValidateLocalServeSecurity(config.Addr, config.Token); err != nil {
		return err
	}

	dbPath, err := resolveDBPath(cmd)
	if err != nil {
		return err
	}
	dbCtx := libtracker.WithNewRequestID(ctx)
	db, err := OpenDBAt(dbCtx, dbPath)
	if err != nil {
		return fmt.Errorf("open database %q: %w", dbPath, err)
	}
	defer db.Close()

	if removed, pruneErr := pruneLegacyLocalBackends(dbCtx, db); pruneErr != nil {
		slog.Warn("could not prune legacy local backends", "error", pruneErr)
	} else if removed > 0 {
		slog.Info("pruned legacy local backends", "count", removed)
	}

	injectGlobalShellEnv := newLiveGlobalShellEnv(shellenvservice.New(db), 3*time.Second)
	sandboxShellScrub, sandboxTerminalScrub := resolveSandboxScrubs(config, injectGlobalShellEnv)

	contenoxDir, err := ResolveContenoxDir(cmd)
	if err != nil {
		return fmt.Errorf("resolve .contenox dir: %w", err)
	}
	workspaceID := ResolveWorkspaceID(contenoxDir)
	workspaceRoot := filepath.Dir(contenoxDir)
	if len(args) > 0 {
		servedDir, err := filepath.Abs(args[0])
		if err != nil {
			return fmt.Errorf("resolve serve directory %q: %w", args[0], err)
		}
		workspaceRoot = servedDir
	}
	nodeID := uuid.NewString()[:8]

	store := runtimetypes.New(db.WithoutTransaction())
	closeLogs, err := setupTelemetryLogging(ctx, store, contenoxDir)
	if err != nil {
		return fmt.Errorf("setup telemetry logging: %w", err)
	}
	defer closeLogs()

	opts, err := buildRunOpts(cmd, db, contenoxDir)
	if err != nil {
		return err
	}
	opts.EffectiveEnableLocalExec = true
	if opts.EffectiveLocalExecAllowedDir == "" {
		opts.EffectiveLocalExecAllowedDir = workspaceRoot
	}

	workspaceBaseRoots := buildWorkspaceBaseRoots(cmd, args, config, workspaceRoot)
	effectiveWorkspaceRoots := append(append([]string{}, workspaceBaseRoots...),
		workspacegrants.ReadGrants(dbCtx, store)...)
	workspaceFactory, err := vfs.NewFactory(effectiveWorkspaceRoots...)
	if err != nil {
		return fmt.Errorf("resolve workspace roots: %w", err)
	}
	if err := vfs.SetControlPlaneDenied(controlPlaneDirs(contenoxDir)...); err != nil {
		return fmt.Errorf("register control-plane isolation: %w", err)
	}
	workspaceReloader := newWorkspaceRootReloader(workspaceFactory, workspaceBaseRoots, store)

	var tracker libtracker.ActivityTracker = libtracker.NoopTracker{}
	if opts.EffectiveTracing {
		tracker = libtracker.NewLogActivityTracker(slog.Default())
	}

	bus := libbus.NewSQLite(db.WithoutTransaction())
	defer bus.Close()
	kvMgr := libkvstore.NewSQLiteManager(db)

	presenceStore := presence.NewStore(kvMgr)
	presenceReporter := presence.StartReporter(ctx, presenceStore, presence.Record{
		Kind:    presence.KindServe,
		Cwd:     workspaceRoot,
		Address: net.JoinHostPort(config.Addr, config.Port),
	})
	defer presenceReporter.Stop()

	stopWorkspaceReload, err := startWorkspaceRootReloader(ctx, bus, workspaceReloader)
	if err != nil {
		return fmt.Errorf("start workspace-root reloader: %w", err)
	}
	defer stopWorkspaceReload()

	hitlSource := hitlPolicySource(contenoxDir)
	hitlSvc := hitlservice.NewWithDefaultPolicy(hitlSource, runtimetypes.LocalTenantID, store, tracker, "")
	missions := missionservice.New(db, missionservice.WithEventPublisher(bus))

	fleetRef := &fleetHolder{}
	oracleStore := runtimetypes.New(db.WithoutTransaction())
	oracleCfg := resolveOracleConfig(ctx, oracleStore, cmd)
	localTools := map[string]taskengine.ToolsRepo{
		"echo":     localtools.NewEchoTools(),
		"print":    localtools.NewPrint(tracker),
		"webtools": localtools.NewWebCaller(tracker),
		"local_fs": localtools.NewLocalFSToolsWith(
			"", db, nil, "local_fs",
			acpsvc.NewServeCwdResolver(db, workspaceFactory),
		),
		missiontools.ToolsProviderName: missiontools.New(missions, nil, missiontools.WithSupervision(
			missionSupervision{missions: missions, hitl: hitlSvc},
			missionSupervision{missions: missions, hitl: hitlSvc},
		), missiontools.WithSpawner(fleetSpawner{fleet: fleetRef.get}, missions, subagentDefaults(db))),
	}
	if oracleCfg.enabled() {
		localTools[oracletools.ToolsProviderName] = oracletools.New(oracleResolver{
			hitl: hitlSvc, missions: missions, store: oracleStore, out: os.Stderr, approves: oracleCfg.approves,
		})
	}
	if opts.EffectiveEnableLocalExec {
		execOpts := []localtools.LocalExecOption{}
		if opts.EffectiveLocalExecAllowedDir != "" {
			execOpts = append(execOpts, localtools.WithLocalExecAllowedDir(opts.EffectiveLocalExecAllowedDir))
		}
		if sandboxShellScrub != nil {
			execOpts = append(execOpts, localtools.WithLocalExecScrubEnv(sandboxShellScrub))
		}
		localTools["local_shell"] = localtools.NewLocalExecTools(execOpts...)
	}

	var shellSessions shellsession.Manager
	if opts.EffectiveEnableLocalExec {
		shellSessions = shellsession.NewManager(shellsession.Config{
			CwdResolver: acpsvc.NewServeCwdResolver(db, workspaceFactory),
			Workspace:   workspaceFactory,
			ScrubEnv:    sandboxShellScrub,
		})
		defer shellSessions.Shutdown()
		localTools[shellsession.ToolsProviderName] = shellsession.NewTools(shellSessions)
	}

	toolsRepo := internaltools.NewPersistentRepo(localTools, db, http.DefaultClient, bus, tracker)
	toolsProviderSvc := toolsproviderservice.New(db, toolsRepo, tracker)

	taskEventSink := taskengine.NewKVJournalTaskEventSink(
		taskengine.NewBusTaskEventSink(bus), kvMgr, tracker)
	approvalCeiling, err := parseHITLApprovalCeiling(config.HITLApprovalTimeout)
	if err != nil {
		return err
	}
	hitlservice.SetApprovalCeiling(hitlSvc, approvalCeiling)
	stopHITLApprovalSweeper := startHITLApprovalSweeper(ctx, hitlSvc, hitlApprovalSweepInterval)
	defer stopHITLApprovalSweeper()

	sessionRouter := acpsvc.NewSessionRouter()

	engine, err := enginesvc.Build(ctx, db, enginesvc.Config{
		DefaultModel:       opts.EffectiveDefaultModel,
		DefaultProvider:    opts.EffectiveDefaultProvider,
		AltDefaultModel:    opts.EffectiveAltDefaultModel,
		AltDefaultProvider: opts.EffectiveAltDefaultProvider,
		ContextLength:      opts.EffectiveContext,
		NoDeleteModels:     opts.EffectiveNoDeleteModels,
		BackendCycleBudget: serveBackendCycleBudget,
		LocalTools:         localTools,
		EnableHITL:         true,
		AskApproval: func(ctx context.Context, req hitlservice.ApprovalRequest) (bool, error) {
			if allowed, err := sessionRouter.AskApproval(ctx, req); !errors.Is(err, acpsvc.ErrNoBoundSession) {
				return allowed, err
			}
			return hitlSvc.RequestApproval(ctx, req, taskEventSink)
		},
		HITLService:      hitlSvc,
		Bus:              bus,
		KVStore:          kvMgr,
		Tracker:          tracker,
		Tracing:          opts.EffectiveTracing,
		TaskEventSink:    taskEventSink,
		WorkspaceID:      workspaceID,
		HITLPolicySource: hitlSource,
	})
	if err != nil {
		return fmt.Errorf("build engine (run `contenox-runtime setup` to configure a model): %w", err)
	}
	if engine.State == nil {
		return fmt.Errorf("build engine: runtime state is not configured")
	}
	defer engine.Stop()
	if opts.EffectiveDefaultModel == "" {
		fmt.Fprintln(os.Stderr, "contenox-runtime serve: no default-model configured; serving setup-only — configure a provider and model in the app, or run `contenox-runtime setup`.")
	}
	stopCatalogRefresh := startCatalogRefresh(ctx, engine.State, catalogRefreshInterval)
	defer stopCatalogRefresh()

	if oracleCfg.enabled() {
		oracleChain, oracleChainRef, oracleErr := loadOracleChain(ctx, contenoxDir, oracleCfg)
		if oracleErr != nil {
			return oracleErr
		}
		hitlservice.SetAdjudicator(hitlSvc, &oracleDriver{
			agent:         agentservice.New(agentservice.Deps{Engine: engine, DB: db, WorkspaceID: workspaceID}),
			chain:         oracleChain,
			chainRef:      oracleChainRef,
			policy:        oracleCfg.policy,
			templateVars:  buildTemplateVars(opts),
			contextLength: opts.EffectiveContext,
			approves:      oracleCfg.approves,
			missions:      missions,
			out:           os.Stderr,
		})
		fmt.Fprintln(os.Stderr, oracleMountedLine(oracleCfg))
	}

	chainFiles, err := localfileservice.NewPrivileged(contenoxDir)
	if err != nil {
		return fmt.Errorf("chain files: %w", err)
	}
	chains := taskchainservice.NewLocal(chainFiles)
	agent := agentservice.New(agentservice.Deps{
		Engine:      engine,
		DB:          db,
		WorkspaceID: workspaceID,
	})
	terminalCfg, err := resolveTerminalConfig(config, workspaceRoot)
	if err != nil {
		return err
	}
	terminalCfg.ScrubEnv = sandboxTerminalScrub
	terminalSvc, err := terminalservice.New(terminalCfg, db, nodeID, workspaceID)
	if err != nil {
		return fmt.Errorf("create terminal service: %w", err)
	}
	if terminalCfg.Enabled {
		terminalSvc = terminalservice.WithActivityTracker(terminalSvc, tracker)
		defer func() { _ = terminalSvc.CloseAll(context.Background()) }()
		stopTerminalReaper := startTerminalReaper(ctx, terminalSvc, terminalReapInterval(terminalCfg.IdleTimeout))
		defer stopTerminalReaper()
	}

	agentRegistry := agentregistryservice.New(db)

	instances := agentinstance.New(
		agentRegistry,
		agentinstance.WithEventSink(newInstanceEventSink(tracker)),
		agentinstance.WithPermissionFallback(fleetservice.NewUnattendedPermissionAnswerer(
			fleetservice.UnattendedPermissionDeps{
				HITL:              hitlSvc,
				Missions:          missions,
				Sink:              taskEventSink,
				DefaultPolicyName: "",
				Tracker:           tracker,
			})),
	)
	defer func() { _ = instances.Close() }()

	nativeTurnCfg, err := nativeturn.ParseEnv(config.TurnMax, config.TurnGrace)
	if err != nil {
		return err
	}
	nativeTurns := nativeturn.New(nativeTurnCfg)
	defer func() { _ = nativeTurns.Close() }()
	stopNativeTurnReaper := startNativeTurnReaper(ctx, nativeTurns, nativeTurnReapInterval(nativeTurnCfg.GraceWindow))
	defer stopNativeTurnReaper()

	// The app's runtime compiles the declared agents before anything discovers
	// them: a fresh home has no chain to serve otherwise.
	if err := seedAgentDeclarations(ctx, contenoxDir); err != nil {
		return fmt.Errorf("seed agent declarations: %w", err)
	}
	stopAgentWatch := watchAgentDeclarations(ctx, contenoxDir)
	defer stopAgentWatch()
	discoverChainAgents(ctx, agentRegistry, contenoxDir)

	fleet := fleetservice.New(instances, agentRegistry, missions, workspaceFactory, workspaceRoot, tracker,
		fleetservice.WithPolicyValidator(hitlservice.NewPolicyValidator(hitlSource, runtimetypes.LocalTenantID, "")))
	fleetRef.set(fleet)

	operatorInbox := operatorinbox.New(db)
	reportRouter, err := reportrouter.New(reportrouter.Deps{
		Bus: bus,
		Sessions: missionReportDeliverer{
			chat:   func() contenoxSessionDeliverer { return sessionRouter },
			kernel: instances,
		},
		Inbox:   operatorInbox,
		Tracker: tracker,
		AgentSupervisor: agentAnswerOffer{
			hitl:     hitlSvc,
			missions: missions,
			prompter: sessionRouter,
			tracker:  tracker,
		},
	})
	if err != nil {
		return fmt.Errorf("build report router: %w", err)
	}
	stopReportRouter, err := reportRouter.Start(ctx)
	if err != nil {
		return fmt.Errorf("start report router: %w", err)
	}
	defer stopReportRouter()

	var acpAgentFactory libacp.AgentFactory
	if acpChains, err := defaultChainFor(contenoxDir, acpProfileACP); err != nil {
		slog.Warn("contenox-runtime serve: /acp transport disabled: ACP chain registry unavailable", "error", err)
	} else {
		acpAgentFactory = acpsvc.New(acpsvc.Deps{
			Engine:                engine,
			DB:                    db,
			ChainRegistry:         acpChains,
			DefaultModel:          opts.EffectiveDefaultModel,
			DefaultProvider:       opts.EffectiveDefaultProvider,
			DefaultAltModel:       opts.EffectiveAltDefaultModel,
			DefaultAltProvider:    opts.EffectiveAltDefaultProvider,
			DefaultMaxTokens:      opts.EffectiveMaxTokens,
			DefaultThink:          opts.EffectiveThink,
			LiveDefaults:          liveDefaultsResolver(db, false, servePinnedDefaults(cmd, opts)),
			WorkspaceID:           workspaceID,
			ContenoxDir:           contenoxDir,
			WorkspaceRoots:        workspaceFactory,
			ShellSessions:         shellSessions,
			SessionRouter:         sessionRouter,
			DecisionRecorder:      decisionRecorderOf(hitlSvc),
			Instances:             instances,
			NativeTurns:           nativeTurns,
			Fleet:                 fleet,
			Agents:                agentRegistry,
			KnownPolicies:         embeddedPolicyNames(),
			HITLDefaultPolicyName: acpDefaultPolicyName,
		})
	}

	runtimeDefaults := stateservice.RuntimeDefaults{
		ChainRef:    firstNonEmptyStr(opts.EffectiveChain, "default-chain.json"),
		Model:       opts.EffectiveDefaultModel,
		Provider:    opts.EffectiveDefaultProvider,
		AltModel:    opts.EffectiveAltDefaultModel,
		AltProvider: opts.EffectiveAltDefaultProvider,
		MaxTokens:   opts.EffectiveMaxTokens,
		Think:       opts.EffectiveThink,
	}

	apiMux := http.NewServeMux()
	cleanupAPI, err := serverapi.New(ctx, apiMux, nodeID, "local", config, serverapi.Dependencies{
		DB:                    db,
		PubSub:                bus,
		State:                 engine.State,
		ToolsProviderService:  toolsProviderSvc,
		Agent:                 agent,
		Chains:                chains,
		Fleet:                 fleet,
		Missions:              missions,
		MissionChanges:        missionchanges.New(missions, instances.(missionchanges.SessionJournalReader)),
		OperatorInbox:         operatorInbox,
		HITL:                  hitlSvc,
		Tracker:               tracker,
		TerminalService:       terminalSvc,
		TerminalEnabled:       terminalCfg.Enabled,
		WorkspaceID:           workspaceID,
		ProjectRoot:           workspaceRoot,
		ContenoxDir:           contenoxDir,
		WorkspaceRoots:        workspaceFactory,
		WorkspaceRootMutators: workspaceReloader.mutators(bus),
		HITLPolicySource:      hitlSource,
		HITLDefaultPolicyName: acpDefaultPolicyName,
		Defaults:              runtimeDefaults,
	})
	if err != nil {
		return fmt.Errorf("build server: %w", err)
	}
	defer func() { _ = cleanupAPI() }()

	fleetapi.AddPresenceRoutes(apiMux, presenceStore)

	rootMux := http.NewServeMux()
	serverapi.AddHealthRoutes(rootMux)
	serverapi.AddVersionRoutes(rootMux, version.Get(), nodeID, "local")
	rootMux.Handle("/api/", http.StripPrefix("/api", serverapi.ProtectAPI(config.Token, config.AllowedAPIOrigins, apiMux)))
	serverapi.AddUIAuthRoutes(rootMux, config.Token)
	compatDeps := compatapi.CompatDeps{
		Agent:        agent,
		Chains:       chains,
		StateService: stateservice.New(engine.State, db, workspaceID),
		Defaults:     runtimeDefaults,
		Token:        config.Token,
	}
	compatapi.AddRootRoutes(rootMux, compatDeps)
	compatapi.AddOllamaRoutes(rootMux, compatDeps)
	if acpAgentFactory != nil {
		rootMux.Handle("/acp", acpWebSocketHandler(acpAgentFactory, config.Token))
	}

	handler := middleware.EnableCORS(&middleware.CORSConfig{
		AllowedAPIOrigins: firstNonEmptyStr(config.AllowedAPIOrigins, middleware.DefaultAllowedAPIOrigins),
		AllowedMethods:    middleware.DefaultAllowedMethods,
		AllowedHeaders:    middleware.DefaultAllowedHeaders,
		ProxyOrigin:       config.ProxyOrigin,
	}, apiframework.RequestIDMiddleware(rootMux))

	srv := &http.Server{
		Addr:              net.JoinHostPort(config.Addr, config.Port),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		fmt.Fprintf(cmd.OutOrStdout(), "contenox-runtime serve %s ready: http://%s\n", version.Get(), srv.Addr)
		if !serverapi.IsLoopbackAddress(config.Addr) {
			for _, u := range lanURLs(config.Port) {
				fmt.Fprintf(cmd.OutOrStdout(), "  reachable on LAN: %s\n", u)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  note: plain HTTP — the TOKEN travels in cleartext; front with a TLS reverse proxy on untrusted networks\n")
		}
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return fmt.Errorf("serve: %w", err)
	}
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func resolveTerminalConfig(config *serverapi.Config, workspaceRoot string) (terminalservice.Config, error) {
	if config == nil {
		config = &serverapi.Config{}
	}
	terminalEnabled := strings.TrimSpace(config.TerminalEnabled)
	if terminalEnabled == "" {
		terminalEnabled = "true"
	}
	allowedRoot := strings.TrimSpace(config.TerminalAllowedRoot)
	if terminalservice.IsEnabled(terminalEnabled) && allowedRoot == "" {
		allowedRoot = workspaceRoot
	}
	cfg, err := terminalservice.ParseEnv(
		terminalEnabled,
		allowedRoot,
		config.TerminalShell,
		config.TerminalIdleTimeout,
		config.TerminalMaxSessions,
	)
	if err != nil {
		return terminalservice.Config{}, err
	}
	return cfg, nil
}

func terminalReapInterval(idleTimeout time.Duration) time.Duration {
	if idleTimeout <= 0 {
		return 0
	}
	interval := time.Minute
	if half := idleTimeout / 2; half > 0 && half < interval {
		interval = half
	}
	if interval < time.Second {
		return time.Second
	}
	return interval
}

func startTerminalReaper(ctx context.Context, svc terminalservice.Service, interval time.Duration) func() {
	if svc == nil || interval <= 0 {
		return func() {}
	}
	reaperCtx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-reaperCtx.Done():
				return
			case <-ticker.C:
				_ = svc.ReapIdle(reaperCtx)
			}
		}
	}()
	return cancel
}

func nativeTurnReapInterval(grace time.Duration) time.Duration {
	if grace <= 0 {
		return time.Minute
	}
	interval := grace / 2
	if interval > time.Minute {
		interval = time.Minute
	}
	if interval < time.Second {
		return time.Second
	}
	return interval
}

func startNativeTurnReaper(ctx context.Context, reg *nativeturn.Registry, interval time.Duration) func() {
	if reg == nil || interval <= 0 {
		return func() {}
	}
	reaperCtx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-reaperCtx.Done():
				return
			case <-ticker.C:
				_ = reg.ReapIdle(reaperCtx)
			}
		}
	}()
	return cancel
}

func startCatalogRefresh(ctx context.Context, state *runtimestate.State, interval time.Duration) func() {
	if state == nil || interval <= 0 {
		return func() {}
	}
	refreshCtx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-refreshCtx.Done():
				return
			case <-ticker.C:
				if err := state.ReconcileIfStale(refreshCtx); err != nil {
					slog.Warn("contenox-runtime serve: backend catalog refresh failed", "error", err)
				}
			}
		}
	}()
	return cancel
}

const hitlApprovalSweepInterval = 30 * time.Second

func parseHITLApprovalCeiling(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid HITL_APPROVAL_TIMEOUT %q: must be a positive Go duration (e.g. 1h)", raw)
	}
	return d, nil
}

func startHITLApprovalSweeper(ctx context.Context, svc hitlservice.Service, interval time.Duration) func() {
	if svc == nil || interval <= 0 {
		return func() {}
	}
	sweepCtx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-sweepCtx.Done():
				return
			case <-ticker.C:
				_, _ = svc.SweepExpired(sweepCtx)
			}
		}
	}()
	return cancel
}

func discoverChainAgents(ctx context.Context, agents agentregistryservice.Service, contenoxDir string) {
	// Declarations are the source: the generated directory holds every chain
	// sessions actually run, so its copy owns the id.
	roots := []string{filepath.Join(contenoxDir, agentdecl.GeneratedDirName), contenoxDir}
	if homeDir, err := globalContenoxDir(); err == nil {
		roots = append(roots, homeDir)
	}
	res, err := chainagents.Discover(ctx, agents, roots...)
	if err != nil {
		slog.Warn("contenox-runtime serve: chain-agent discovery failed; the fleet keeps the agents already declared",
			"error", err, "roots", roots)
		return
	}
	if len(res.Created) > 0 || len(res.Updated) > 0 || len(res.Disabled) > 0 || len(res.Skipped) > 0 {
		slog.Info("contenox-runtime serve: chain agents discovered",
			"created", res.Created, "updated", res.Updated,
			"disabled", res.Disabled, "skipped_name_taken", res.Skipped,
			"unchanged", len(res.Unchanged))
	}
}

func servePinnedDefaults(cmd *cobra.Command, opts chatOpts) acpsvc.LiveDefaults {
	var p acpsvc.LiveDefaults
	changed := cmd.Flags().Changed
	if changed("model") {
		p.Model = opts.EffectiveDefaultModel
	}
	if changed("provider") {
		p.Provider = opts.EffectiveDefaultProvider
	}
	if changed("alt-model") {
		p.AltModel = opts.EffectiveAltDefaultModel
	}
	if changed("alt-provider") {
		p.AltProvider = opts.EffectiveAltDefaultProvider
	}
	if changed("max-tokens") {
		p.MaxTokens = opts.EffectiveMaxTokens
	}
	if changed("think") {
		p.Think = opts.EffectiveThink
	}
	return p
}

// decisionRecorderOf returns the approval service's audit recorder, or nil when
// the service cannot record (it then simply does not write audit rows).
func decisionRecorderOf(svc hitlservice.Service) hitlservice.DecisionRecorder {
	rec, _ := svc.(hitlservice.DecisionRecorder)
	return rec
}

// watchAgentDeclarations recompiles the catalog when a declaration or
// agents.toml changes, and tells connected clients so the Agents view can
// re-read the problems. It polls a cheap signature: a tick over a folder holding
// a few dozen files costs less than a dependency on a platform watcher, and it
// behaves the same on Windows and Linux.
func watchAgentDeclarations(ctx context.Context, contenoxDir string) func() {
	const interval = 500 * time.Millisecond
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		signature := agentdecl.TreeSignature(contenoxDir)
		var pending <-chan time.Time
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				current := agentdecl.TreeSignature(contenoxDir)
				if current == signature {
					continue
				}
				signature = current
				// Debounce a burst of saves: one compile per quiet period.
				pending = time.After(300 * time.Millisecond)
			case <-pending:
				pending = nil
				if err := seedAgentDeclarations(ctx, contenoxDir); err != nil {
					slog.Error("contenox-runtime serve: agent declarations failed to recompile", "error", err)
					continue
				}
				acpsvc.NotifyAgentDeclarationsChanged()
			}
		}
	}()
	return cancel
}
