package contenoxcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/contenox/runtime/runtime/homedir"

	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/libbus"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libkvstore"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/acpsvc"
	"github.com/contenox/runtime/runtime/agentdecl"
	"github.com/contenox/runtime/runtime/agentinstance"
	"github.com/contenox/runtime/runtime/agentregistryservice"
	"github.com/contenox/runtime/runtime/agentservice"
	"github.com/contenox/runtime/runtime/enginesvc"
	"github.com/contenox/runtime/runtime/fleetservice"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/internal/updatecheck"
	"github.com/contenox/runtime/runtime/localtools"
	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/contenox/runtime/runtime/missiontools"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/operatorinbox"
	"github.com/contenox/runtime/runtime/oracletools"
	"github.com/contenox/runtime/runtime/presence"
	"github.com/contenox/runtime/runtime/reasoning"
	"github.com/contenox/runtime/runtime/reportrouter"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/contenox/runtime/runtime/vfs"
	"github.com/spf13/cobra"
)

var acpCmd = &cobra.Command{
	Use:   "acp",
	Short: "Run the Contenox ACP server over stdio.",
	Long: `Speak Agent Client Protocol over stdio so editors like Zed can run local Contenox chains.

The chain executed for each session/prompt is loaded from ~/.contenox-harness/default-acp-chain.json
(override with the CONTENOX_ACP_CHAIN_PATH environment variable). Populate it like any other
contenox chain.

The default model is read from the global 'default-model' / 'default-provider' configuration
(set via 'contenox-runtime config set default-model …'). Logging goes to stderr; stdin/stdout are
reserved for the JSON-RPC stream.

HITL is on by default — gated tool calls route through the ACP session/request_permission
flow so the editor's UI controls approval. Pass --auto to disable (unattended/testing).

The /mission slash command dispatches a mission in-process: the fired unit is a child
subprocess of this editor session and its reports arrive live back in the firing session.
No 'contenox-runtime serve' is needed. Setting CONTENOX_SERVER_URL opts into forwarding the
dispatch to that serve instead (reports then land in its operator inbox).`,
	RunE: runACP,
}

var acpxCmd = &cobra.Command{
	Use:   "acpx",
	Short: "Run as an ACP agent under the headless / untrusted-driver profile (OpenClaw).",
	Long: `Same Agent Client Protocol server as 'acp', for drivers that are not the
device owner — OpenClaw and other non-editor clients. It loads the hardened
hitl-policy-acpx.json (local_shell denied, web mutations denied, web reads
gated) and the chain at ~/.contenox-harness/headless-acp-chain.json (override with
CONTENOX_ACPX_CHAIN_PATH).

Containment for the untrusted driver is the HITL policy, not an in-chain
step. IDE clients (Zed, GoLand, AionUi) should keep using 'acp'. Selection
is per-spawn: each ACP client launches its own contenox process, so the two
profiles never share state.`,
	RunE: runACPX,
}

func init() {
	for _, c := range []*cobra.Command{acpCmd, acpxCmd} {
		registerOracleFlags(c)
		c.Flags().Bool("auto", false, "Non-interactive mode: disable HITL permission prompts (gated tools run unattended)")
		c.Flags().Bool("setup", false, "Run interactive setup wizard to configure provider and model, then exit.")
		c.Flags().String("workspace-id", "", "Workspace ID for new ACP sessions (default: the stable workspace from ~/.contenox-harness/workspace.id, same as the CLI). Existing sessions are always located by their session ID regardless of workspace.")
	}
	acpCmd.Flags().Bool("experimental-acp", false, "Accepted for compatibility with ACP clients that hardcode this launch flag (e.g. AionUi); no effect.")
	_ = acpCmd.Flags().MarkHidden("experimental-acp")
}

type acpStdio struct{}

func (acpStdio) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (acpStdio) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (acpStdio) Close() error                { return os.Stdin.Close() }

// acpDefaultPolicyName is the envelope file the app's chat runs under.
const acpDefaultPolicyName = "hitl-policy-default.json"

type acpProfile struct {
	hitlPolicy string
	chainFile  string
	chainEnv   string
	// compiledChain names the chain the declaration catalog compiles to, when
	// this profile serves one. Empty means the profile only knows chainFile.
	compiledChain string
	seedChain     func(contenoxDir string) error
	embedFleet    bool
}

var acpProfileACP = acpProfile{
	hitlPolicy:    "hitl-policy-default.json",
	chainFile:     "default-acp-chain.json",
	chainEnv:      "CONTENOX_ACP_CHAIN_PATH",
	compiledChain: "chain-agent-acp.json",
	embedFleet:    true,
}

var acpProfileACPX = acpProfile{
	hitlPolicy: "hitl-policy-acpx.json",
	chainFile:  headlessACPChainFilename,
	chainEnv:   "CONTENOX_ACPX_CHAIN_PATH",
	seedChain:  seedHeadlessACPChainIfMissing,
}

func runACP(cmd *cobra.Command, _ []string) error  { return runACPProfile(cmd, acpProfileACP) }
func runACPX(cmd *cobra.Command, _ []string) error { return runACPProfile(cmd, acpProfileACPX) }

// seedAgentDeclarations preseed the home before reading its config, so a shipped
// default this release changed applies on this run rather than the next one.
func seedAgentDeclarations(ctx context.Context, contenoxDir string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	// One compiler per home: several project runtimes share it.
	release, lockErr := agentdecl.CompileLock(contenoxDir)
	if lockErr != nil {
		return lockErr
	}
	defer release()

	cfg, err := agentdecl.Shipped()
	if err != nil {
		return err
	}
	configRoots := []agentdecl.Root{}
	if homeRoot, rootErr := agentdecl.LocalRoot(contenoxDir); rootErr == nil {
		preseeded, preseedErr := agentdecl.Preseed(ctx, homeRoot)
		if preseedErr != nil {
			return preseedErr
		}
		if len(preseeded.Created)+len(preseeded.Updated)+len(preseeded.Edited) > 0 {
			slog.Info("agent declarations: shipped files refreshed",
				"created", len(preseeded.Created), "updated", len(preseeded.Updated), "edited", len(preseeded.Edited))
		}
		configRoots = append(configRoots, homeRoot)
	}
	project, _ := os.Getwd()
	if projectRoot, pErr := agentdecl.LocalRoot(project); pErr == nil {
		if projectContenox, cErr := projectRoot.Child(".contenox"); cErr == nil {
			configRoots = append(configRoots, projectContenox)
		}
	}
	if len(configRoots) > 0 {
		if layered, loadErr := agentdecl.Load(ctx, configRoots...); loadErr == nil {
			cfg = layered
		} else {
			slog.Warn("agent declarations: a user agents.toml was ignored", "error", loadErr)
		}
	}
	if stale, readErr := os.ReadFile(filepath.Join(contenoxDir, "default-acp-chain.json")); readErr == nil && !bytes.Equal(stale, []byte(initACPChain)) {
		slog.Warn("contenox-runtime: default-acp-chain.json is no longer used; the default chain is compiled from agents/acp/ — edit that declaration instead",
			"ignored", filepath.Join(contenoxDir, "default-acp-chain.json"))
	}
	preseeded, results, err := agentdecl.BootstrapFor(ctx, contenoxDir, project, cfg)
	if err != nil {
		slog.Error("agent declarations: bootstrap reported a problem; using what compiled", "error", err)
		return nil
	}
	slog.Info("contenox-runtime serve: agent declarations synced",
		"created", len(preseeded.Created), "updated", len(preseeded.Updated),
		"edited", len(preseeded.Edited), "declarations", len(results))
	return nil
}

func defaultChainFor(contenoxDir string, profile acpProfile) (*acpsvc.ChainRegistry, error) {
	if env := os.Getenv(profile.chainEnv); env != "" {
		return acpsvc.LoadChainRegistryPath(env)
	}
	if profile.compiledChain == "" {
		return acpsvc.LoadChainRegistryFrom(profile.chainFile, profile.chainEnv)
	}
	compiled := filepath.Join(contenoxDir, agentdecl.GeneratedDirName, profile.compiledChain)
	if _, err := os.Stat(compiled); err != nil {
		if cfg, cfgErr := agentdecl.Shipped(); cfgErr == nil {
			if chain, cErr := agentdecl.CompileShippedTree(cfg); cErr == nil {
				slog.Warn("agent declarations: serving the shipped tree; the home's copy is refused")
				return acpsvc.NewChainRegistryFor(chain, "embedded:agentdecl/preseed/agents/acp"), nil
			}
		}
	}
	return acpsvc.LoadChainRegistryPath(compiled)
}

func runACPProfile(cmd *cobra.Command, profile acpProfile) error {
	if setup, _ := cmd.Flags().GetBool("setup"); setup {
		return runSetup(cmd, cmd.OutOrStdout())
	}

	parentCtx := cmd.Context()
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, stop := signal.NotifyContext(parentCtx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	defer func() { _ = modelrepo.Shutdown() }()

	autoMode, _ := cmd.Flags().GetBool("auto")
	enableHITL := !autoMode

	workspaceFlag, _ := cmd.Flags().GetString("workspace-id")

	var tracker libtracker.ActivityTracker = libtracker.NewTextActivityTracker(os.Stderr)

	reportErr, reportChange, endStartup := tracker.Start(ctx, "startup", "acp")
	defer endStartup()
	reportChange("phase", "flags_parsed")

	dbPath, err := resolveDBPath(cmd)
	if err != nil {
		reportErr(err)
		return err
	}
	reportChange("phase", "resolve_db_path")
	dbCtx := libtracker.WithNewRequestID(ctx)
	db, err := OpenDBAt(dbCtx, dbPath)
	if err != nil {
		reportErr(err)
		return fmt.Errorf("open database %q: %w", dbPath, err)
	}
	defer db.Close()
	reportChange("phase", "open_db")

	contenoxDir, err := globalContenoxDir()
	if err != nil {
		reportErr(err)
		return fmt.Errorf("resolve contenox dir: %w", err)
	}
	reportChange("phase", "resolve_contenox_dir")
	if err := vfs.SetControlPlaneDenied(controlPlaneDirs(contenoxDir)...); err != nil {
		reportErr(err)
		return fmt.Errorf("register control-plane isolation: %w", err)
	}
	workspaceID := workspaceFlag
	if workspaceID == "" {
		workspaceID = ResolveWorkspaceID(contenoxDir)
	}
	reportChange("phase", "seed_hitl")
	if profile.seedChain != nil {
		if err := profile.seedChain(contenoxDir); err != nil {
			reportErr(err)
			return fmt.Errorf("seed ACP chain preset: %w", err)
		}
	}
	if err := seedAgentDeclarations(cmd.Context(), contenoxDir); err != nil {
		reportErr(err)
		return fmt.Errorf("seed agent declarations: %w", err)
	}
	reportChange("phase", "seed_agents")
	reportChange("phase", "seed_chain")

	closeLogs, err := setupTelemetryLogging(ctx, runtimetypes.New(db.WithoutTransaction()), contenoxDir)
	if err != nil {
		reportErr(err)
		return fmt.Errorf("setup telemetry logging: %w", err)
	}
	defer closeLogs()
	reportChange("phase", "setup_telemetry")

	var transport *acpsvc.Transport

	if acpsvc.ReadConfigValue(ctx, db, "default-model") == "" &&
		(os.Getenv(envDefaultProvider) != "" || os.Getenv(envDefaultModel) != "") {
		if err := completeEnvSetup(ctx, db); err != nil {
			fmt.Fprintf(os.Stderr, "contenox-runtime acp: environment-based setup incomplete: %v\n", err)
		} else {
			fmt.Fprintln(os.Stderr, "contenox-runtime acp: configured provider/model from environment.")
		}
	}

	defaultModel := configValueWithEnv(ctx, db, "default-model", envDefaultModel)
	defaultProvider := configValueWithEnv(ctx, db, "default-provider", envDefaultProvider)
	defaultAltModel := configValueWithEnv(ctx, db, "default-alt-model", envDefaultAltModel)
	defaultAltProvider := configValueWithEnv(ctx, db, "default-alt-provider", envDefaultAltProvider)
	defaultMaxTokens, err := normalizeMaxTokensConfig(configValueWithEnv(ctx, db, "default-max-tokens", envDefaultMaxTokens))
	if err != nil {
		return err
	}
	defaultThink := reasoning.Default
	if configuredThink := configValueWithEnv(ctx, db, "default-think", envDefaultThink); configuredThink != "" {
		level, err := reasoning.Normalize(configuredThink)
		if err != nil {
			return fmt.Errorf("config default-think: %w", err)
		}
		defaultThink = level
	}

	chains, err := defaultChainFor(contenoxDir, profile)
	if err != nil {
		return err
	}
	_, _, end := tracker.Start(ctx, "load", "acp_chain", "source", chains.Source(), "id", chains.Default().ID)
	end()

	bus := libbus.NewSQLite(db.WithoutTransaction())
	defer bus.Close()
	missions := missionservice.New(db, missionservice.WithEventPublisher(bus))

	acpHITL := hitlservice.NewWithDefaultPolicy(acpPolicySource(), runtimetypes.LocalTenantID, runtimetypes.New(db.WithoutTransaction()), tracker, profile.hitlPolicy)

	oracleStore := runtimetypes.New(db.WithoutTransaction())
	oracleCfg := resolveOracleConfig(ctx, oracleStore, cmd)
	if strings.TrimSpace(os.Getenv(profile.chainEnv)) != "" {
		oracleCfg.chain = ""
	}

	fleetRef := &fleetHolder{}
	tools := map[string]taskengine.ToolsRepo{
		"echo":     localtools.NewEchoTools(),
		"print":    localtools.NewPrint(tracker),
		"webtools": localtools.NewWebCaller(tracker),
		"local_fs": localtools.NewLocalFSToolsWith(
			"",
			db,
			acpsvc.NewACPFileIO(func() *acpsvc.Transport { return transport }),
			"local_fs",
			acpsvc.NewACPCwdResolver(func() *acpsvc.Transport { return transport }),
		),
		"local_shell": localtools.NewLocalExecToolsWith(
			acpsvc.NewACPCommandRunner(func() *acpsvc.Transport { return transport }),
		),
		missiontools.ToolsProviderName: missiontools.New(missions, missionAttentionAsker{
			hitl:     acpHITL,
			missions: missions,
			bus:      bus,
		}, missiontools.WithSupervision(
			missionSupervision{missions: missions, hitl: acpHITL},
			missionSupervision{missions: missions, hitl: acpHITL},
		), missiontools.WithSpawner(fleetSpawner{fleet: fleetRef.get}, missions, subagentDefaults(db))),
	}
	if oracleCfg.enabled() {
		tools[oracletools.ToolsProviderName] = oracletools.New(oracleResolver{
			hitl: acpHITL, missions: missions, store: oracleStore, out: cmd.ErrOrStderr(), approves: oracleCfg.approves,
		})
	}

	var askApproval localtools.AskApproval
	if enableHITL {
		askApproval = func(ctx context.Context, req hitlservice.ApprovalRequest) (bool, error) {
			if transport == nil {
				return false, fmt.Errorf("acpsvc: HITL approval requested before transport initialization")
			}
			return transport.AskApproval(ctx, req)
		}
	}

	var engine *enginesvc.Engine
	if err := acpsvc.CleanupStaleACPManagedMCPServers(ctx, db); err != nil {
		return fmt.Errorf("cleanup stale ACP MCP servers: %w", err)
	}
	if defaultModel == "" {
		fmt.Fprintln(os.Stderr, "contenox-runtime acp: no default-model configured; serving setup-only. Run the \"Setup Contenox\" auth method or `contenox-runtime acp --setup` to configure a provider and model.")
	} else {
		cfg := enginesvc.Config{
			DefaultModel:       defaultModel,
			DefaultProvider:    defaultProvider,
			AltDefaultModel:    defaultAltModel,
			AltDefaultProvider: defaultAltProvider,
			LocalTools:         tools,
			Tracker:            tracker,
			WorkspaceID:        workspaceID,
			Bus:                bus,
		}
		if enableHITL {
			cfg.EnableHITL = true
			cfg.AskApproval = askApproval
			cfg.HITLPolicySource = acpPolicySource()
			cfg.HITLDefaultPolicyName = profile.hitlPolicy
		}

		engine, err = enginesvc.Build(ctx, db, cfg)
		if err != nil {
			return fmt.Errorf("build engine: %w", err)
		}
		defer engine.Stop()
	}

	if engine != nil && oracleCfg.enabled() {
		oracleChain, oracleChainRef, oracleErr := loadOracleChain(ctx, contenoxDir, oracleCfg)
		if oracleErr != nil {
			return oracleErr
		}
		contextLength, _ := cmd.Flags().GetInt("context")
		templateVars := map[string]string{
			"model":    defaultModel,
			"provider": defaultProvider,
			"think":    string(defaultThink),
		}
		if defaultAltModel != "" {
			templateVars["alt_model"] = defaultAltModel
		}
		if defaultAltProvider != "" {
			templateVars["alt_provider"] = defaultAltProvider
		}
		if defaultMaxTokens != "" {
			templateVars["max_tokens"] = defaultMaxTokens
		}
		hitlservice.SetAdjudicator(acpHITL, &oracleDriver{
			agent:         agentservice.New(agentservice.Deps{Engine: engine, DB: db, WorkspaceID: workspaceID}),
			chain:         oracleChain,
			chainRef:      oracleChainRef,
			policy:        oracleCfg.policy,
			templateVars:  templateVars,
			contextLength: contextLength,
			approves:      oracleCfg.approves,
			missions:      missions,
			out:           cmd.ErrOrStderr(),
		})
		fmt.Fprintln(cmd.ErrOrStderr(), oracleMountedLine(oracleCfg))
	}

	updateBanner := acpUpdateBanner(dbCtx, db, contenoxDir)

	presenceStore := presence.NewStore(libkvstore.NewSQLiteManager(db))

	var (
		missionFleet      acpsvc.MissionDispatcher
		missionAgents     acpsvc.MissionAgentResolver
		missionForward    *acpsvc.MissionForwardConfig
		stopFleetTeardown func()
	)
	isDispatchedUnit := strings.TrimSpace(os.Getenv(profile.chainEnv)) != ""
	switch {
	case !profile.embedFleet || isDispatchedUnit:
	case strings.TrimSpace(os.Getenv(envServeURL)) != "":
		fwd := newMissionForwarder()
		missionFleet, missionAgents, missionForward = fwd, fwd, fwd.forwardConfig()
	case engine != nil:
		fleet, agents, stop, buildErr := buildInProcessFleet(ctx, inProcessFleetDeps{
			db:          db,
			bus:         bus,
			missions:    missions,
			contenoxDir: contenoxDir,
			tracker:     tracker,
			transport:   func() *acpsvc.Transport { return transport },
			hitl:        acpHITL,
		})
		if buildErr != nil {
			return buildErr
		}
		missionFleet, missionAgents, stopFleetTeardown = fleet, agents, stop
	}
	fleetRef.set(missionFleet)
	if stopFleetTeardown != nil {
		defer stopFleetTeardown()
	}

	transportFactory := acpsvc.New(acpsvc.Deps{
		Engine:                engine,
		DB:                    db,
		ChainRegistry:         chains,
		DefaultModel:          defaultModel,
		DefaultProvider:       defaultProvider,
		DefaultAltModel:       defaultAltModel,
		DefaultAltProvider:    defaultAltProvider,
		DefaultMaxTokens:      defaultMaxTokens,
		DefaultThink:          defaultThink,
		LiveDefaults:          liveDefaultsResolver(db, true, acpsvc.LiveDefaults{}),
		WorkspaceID:           workspaceID,
		ContenoxDir:           contenoxDir,
		KnownPolicies:         embeddedPolicyNames(),
		HITLDefaultPolicyName: profile.hitlPolicy,
		UpdateBanner:          updateBanner,
		DecisionRecorder:      decisionRecorderOf(acpHITL),
		Fleet:                 missionFleet,
		Agents:                missionAgents,
		MissionForwarded:      missionForward,
		EnvSetup: &acpsvc.EnvSetupSpec{
			Vars: acpEnvSetupVars(),
			Complete: func(cctx context.Context) error {
				return completeEnvSetup(cctx, db)
			},
		},
	})

	acpCwd, _ := os.Getwd()
	presenceReporter := presence.StartReporter(ctx, presenceStore, presence.Record{
		Kind: presence.KindACP,
		Cwd:  acpCwd,
	})
	defer presenceReporter.Stop()

	conn := libacp.NewAgentSideConnection(acpStdio{}, func(c *libacp.AgentSideConnection) libacp.Agent {
		agent := transportFactory(c)
		transport = agent.(*acpsvc.Transport)
		return newPresenceAgent(agent, presenceReporter)
	})

	runErr := conn.Run(ctx)
	if transport != nil {
		_ = transport.Close(context.Background())
	}
	if runErr != nil && !errors.Is(runErr, io.EOF) && !errors.Is(runErr, context.Canceled) {
		return fmt.Errorf("acp run: %w", runErr)
	}
	return nil
}

type inProcessFleetDeps struct {
	db          libdb.DBManager
	bus         libbus.Messenger
	missions    missionservice.Service
	contenoxDir string
	tracker     libtracker.ActivityTracker
	transport   func() *acpsvc.Transport
	hitl        hitlservice.Service
}

func buildInProcessFleet(ctx context.Context, deps inProcessFleetDeps) (fleetservice.Service, agentregistryservice.Service, func(), error) {
	agents := agentregistryservice.New(deps.db)

	discoverChainAgents(ctx, agents, deps.contenoxDir)

	kernel := agentinstance.New(agents, agentinstance.WithStderr(os.Stderr))

	operatorInbox := operatorinbox.New(deps.db)

	router, err := reportrouter.New(reportrouter.Deps{
		Bus: deps.bus,
		Sessions: missionReportDeliverer{
			chat:   func() contenoxSessionDeliverer { return chatDeliverer(deps.transport()) },
			kernel: kernel,
		},
		Inbox:   operatorInbox,
		Tracker: deps.tracker,
		AgentSupervisor: agentAnswerOffer{
			hitl:     deps.hitl,
			missions: deps.missions,
			prompter: transportPrompter{transport: deps.transport},
			tracker:  deps.tracker,
		},
	})
	if err != nil {
		_ = kernel.Close()
		return nil, nil, nil, fmt.Errorf("build report router: %w", err)
	}
	stopRouter, err := router.Start(ctx)
	if err != nil {
		_ = kernel.Close()
		return nil, nil, nil, fmt.Errorf("start report router: %w", err)
	}

	projectRoot, _ := os.Getwd()
	fleet := fleetservice.New(kernel, agents, deps.missions, nil, projectRoot, deps.tracker,
		fleetservice.WithPolicyValidator(hitlservice.NewPolicyValidator(hitlPolicySource(deps.contenoxDir), runtimetypes.LocalTenantID, "")))

	stop := func() {
		stopRouter()
		_ = kernel.Close()
	}
	return fleet, agents, stop, nil
}

type missionReportDeliverer struct {
	chat   func() contenoxSessionDeliverer
	kernel agentinstance.Manager
}

type contenoxSessionDeliverer interface {
	DeliverToContenoxSession(ctx context.Context, contenoxSessionID string, n libacp.SessionNotification) error
}

func chatDeliverer(t *acpsvc.Transport) contenoxSessionDeliverer {
	if t == nil {
		return nil
	}
	return t
}

var _ reportrouter.SessionDeliverer = missionReportDeliverer{}

func (d missionReportDeliverer) DeliverToSession(ctx context.Context, sessionID libacp.SessionID, n libacp.SessionNotification) error {
	if d.chat != nil {
		if chat := d.chat(); chat != nil {
			if err := chat.DeliverToContenoxSession(ctx, string(sessionID), n); err == nil {
				return nil
			}
		}
	}
	if d.kernel != nil {
		return d.kernel.DeliverToSession(ctx, sessionID, n)
	}
	return acpsvc.ErrSessionNotLive
}

type missionAttentionAsker struct {
	hitl     hitlservice.Service
	missions missionservice.Service
	bus      libbus.Messenger
}

var _ missiontools.AttentionAsker = missionAttentionAsker{}

func (a missionAttentionAsker) RaiseAttention(ctx context.Context, missionID, summary, detail string) (string, error) {
	var parentSessionID, agentName, intent string
	if a.missions != nil {
		if m, err := a.missions.Get(ctx, missionID); err == nil && m != nil {
			parentSessionID, agentName, intent = m.ParentSessionID, m.AgentName, m.Intent
		}
	}
	return a.hitl.RequestAttention(ctx, hitlservice.AttentionRequest{
		Summary:   summary,
		Detail:    detail,
		MissionID: missionID,
		AgentName: agentName,
		OnRaised: func(askID string) {
			a.publishAsked(ctx, missionservice.AttentionAskedEvent{
				MissionID:       missionID,
				AskID:           askID,
				ParentSessionID: parentSessionID,
				AgentName:       agentName,
				Intent:          intent,
				Summary:         summary,
				Detail:          detail,
			})
		},
	}, taskengine.NoopTaskEventSink{})
}

func (a missionAttentionAsker) publishAsked(ctx context.Context, ev missionservice.AttentionAskedEvent) {
	if a.bus == nil {
		return
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		return
	}
	_ = a.bus.Publish(ctx, missionservice.AttentionAskedSubject, raw)
}

type transportPrompter struct {
	transport func() *acpsvc.Transport
}

func (p transportPrompter) PromptContenoxSession(ctx context.Context, contenoxSessionID, text string) error {
	t := p.transport()
	if t == nil {
		return acpsvc.ErrSessionNotLive
	}
	return t.PromptContenoxSession(ctx, contenoxSessionID, text)
}

func acpPolicySource() hitlservice.PolicySource {
	dir, err := homedir.Dir()
	if err != nil {
		return hitlservice.NewFSPolicySource()
	}
	return hitlservice.NewFSPolicySource(filepath.Join(dir, agentdecl.GeneratedDirName), dir)
}

func acpUpdateBanner(ctx context.Context, db libdb.DBManager, contenoxDir string) string {
	if v, ok := os.LookupEnv("CONTENOX_UPDATE_CHECK"); ok && (v == "false" || v == "0") {
		return ""
	}
	if acpsvc.ReadConfigValue(ctx, db, "update-check") == "false" {
		return ""
	}

	type result struct {
		tag       string
		available bool
	}
	ch := make(chan result, 1)
	go func() {
		tag, avail, err := updatecheck.IsAvailable(ctx, CLIVersion(), contenoxDir)
		if err != nil {
			ch <- result{}
			return
		}
		ch <- result{tag, avail}
	}()

	select {
	case r := <-ch:
		if !r.available {
			return ""
		}
		return fmt.Sprintf("contenox-runtime %s is available (current: %s) — run `contenox-runtime update` to upgrade.", r.tag, CLIVersion())
	case <-time.After(500 * time.Millisecond):
		return ""
	}
}
