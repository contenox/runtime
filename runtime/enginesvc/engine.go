package enginesvc

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	libbus "github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libkvstore"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/execservice"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/internal/setupcheck"
	"github.com/contenox/runtime/runtime/internal/tools"
	"github.com/contenox/runtime/runtime/llmrepo"
	"github.com/contenox/runtime/runtime/localtools"
	"github.com/contenox/runtime/runtime/mcpworker"
	"github.com/contenox/runtime/runtime/missiontools"
	"github.com/contenox/runtime/runtime/ollamatokenizer"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/stateservice"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/contenox/runtime/runtime/toolguidance"
)

const LocalTenantID = runtimetypes.LocalTenantID

func Build(ctx context.Context, db libdbexec.DBManager, cfg Config) (*Engine, error) {
	engineCtx, engineCancel := context.WithCancel(ctx)

	bus := cfg.Bus
	ownsBus := false
	if bus == nil {
		bus = libbus.NewSQLite(db.WithoutTransaction())
		ownsBus = true
	}

	closeBus := func() {
		if ownsBus {
			bus.Close()
		}
	}

	success := false
	defer func() {
		if !success {
			engineCancel()
			closeBus()
		}
	}()

	kvMgr := cfg.KVStore
	if kvMgr == nil {
		kvMgr = libkvstore.NewSQLiteManager(db)
	}

	state := cfg.State
	if state == nil {
		stateOpts := []runtimestate.Option{
			runtimestate.WithKVStore(kvMgr),
			runtimestate.WithAutoDiscoverModels(),
		}
		if cfg.NoDeleteModels {
			stateOpts = append(stateOpts, runtimestate.WithSkipDeleteUndeclaredModels())
		}
		var err error
		state, err = runtimestate.New(engineCtx, db, bus, stateOpts...)
		if err != nil {
			return nil, fmt.Errorf("failed to create runtime state: %w", err)
		}
	}
	engine := &Engine{Stop: func() {
		engineCancel()
		closeBus()
	}, Bus: bus, State: state}

	tenantID := cfg.TenantID
	if tenantID == "" {
		tenantID = runtimetypes.LocalTenantID
	}
	if cfg.DefaultModel != "" {
		config := &runtimestate.Config{
			TenantID:  tenantID,
			TaskModel: cfg.DefaultModel,
			ChatModel: cfg.DefaultModel,
		}
		if err := runtimestate.InitPromptExec(ctx, config, db, state, cfg.ContextLength); err != nil {
			return nil, fmt.Errorf("failed to init prompt executor: %w", err)
		}
		if err := runtimestate.InitChatExec(ctx, config, db, state, cfg.ContextLength); err != nil {
			return nil, fmt.Errorf("failed to init chat executor: %w", err)
		}

		specs := []runtimestate.ExtraModelSpec{
			{
				Name:          cfg.DefaultModel,
				ContextLength: cfg.ContextLength,
				CanChat:       true,
				CanPrompt:     true,
				CanEmbed:      false,
			},
		}
		if err := runtimestate.EnsureModels(ctx, db, tenantID, specs); err != nil {
			return nil, fmt.Errorf("failed to ensure models: %w", err)
		}
	}

	tracker := cfg.Tracker
	if tracker == nil {
		if cfg.Tracing {
			tracker = libtracker.NewLogActivityTracker(slog.Default())
		} else {
			tracker = libtracker.NoopTracker{}
		}
	}

	if !cfg.SkipBackendCycle {
		cycleCtx := ctx
		cancelCycle := func() {}
		if cfg.BackendCycleBudget > 0 {
			cycleCtx, cancelCycle = context.WithTimeout(ctx, cfg.BackendCycleBudget)
		}
		cycleReportErr, _, cycleEnd := tracker.Start(ctx, "sync", "backend_cycle")
		if err := state.RunBackendCycle(cycleCtx); err != nil {
			cycleReportErr(err)
		}
		cycleEnd()
		cancelCycle()

		rt := state.Get(ctx)
		anyReachable := false
		_, reportReachable, reachableEnd := tracker.Start(ctx, "check", "backend_reachability")
		for id, bs := range rt {
			if bs.Error != "" {
				reportReachable(id, map[string]any{"url": bs.Backend.BaseURL, "error": bs.Error})
			} else {
				anyReachable = true
			}
		}
		if !anyReachable {
			reportReachable("", "no reachable backends; subsequent model operations may fail")
		}
		reachableEnd()
	}

	ss := stateservice.New(state, db, cfg.WorkspaceID)
	setupStatus := func(ctx context.Context) (setupcheck.Result, error) {
		r, err := ss.SetupStatus(ctx)
		if err != nil {
			return setupcheck.Result{}, err
		}
		return setupcheck.OverlayEffectiveDefaults(r, cfg.ReadinessDefaultModel, cfg.ReadinessDefaultProvider), nil
	}
	res, err := setupStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("setup status failed: %w", err)
	}
	engine.SetupCheck = res
	engine.SetupStatus = setupStatus

	tokenizer := ollamatokenizer.NewEstimateTokenizer()

	repo, err := llmrepo.NewModelManager(state, tokenizer, llmrepo.ModelManagerConfig{
		DefaultPromptModel:    llmrepo.ModelConfig{Name: cfg.DefaultModel, Provider: cfg.DefaultProvider},
		DefaultEmbeddingModel: llmrepo.ModelConfig{Name: cfg.DefaultModel, Provider: cfg.DefaultProvider},
		DefaultChatModel:      llmrepo.ModelConfig{Name: cfg.DefaultModel, Provider: cfg.DefaultProvider},
	}, tracker)
	if err != nil {
		return nil, fmt.Errorf("failed to create model manager: %w", err)
	}

	eventSink := cfg.TaskEventSink
	if eventSink == nil {
		eventSink = taskengine.NewBusTaskEventSink(bus)
	}
	cfg.TaskEventSink = eventSink

	mgr, localToolNames, toolsRepo, err := buildTools(engineCtx, cfg, db, tracker, bus)
	if err != nil {
		return nil, err
	}

	execCtx := taskengine.WithTaskEventSink(engineCtx, eventSink)

	exec, err := taskengine.NewExec(execCtx, repo, toolsRepo, tracker)
	if err != nil {
		return nil, fmt.Errorf("failed to create task executor: %w", err)
	}
	var inspector taskengine.Inspector = taskengine.NewSimpleInspector()
	inspector = taskengine.NewKVInspector(inspector, kvMgr, tracker)
	inspector = taskengine.NewBusInspector(inspector, bus, tracker)
	for _, wrap := range cfg.ExtraInspectors {
		inspector = wrap(inspector)
	}
	envExec, err := taskengine.NewEnv(execCtx, tracker, exec, inspector, toolsRepo)
	if err != nil {
		return nil, fmt.Errorf("failed to create environment executor: %w", err)
	}
	envExec, err = taskengine.NewMacroEnv(envExec, toolsRepo, taskengine.WithPlatformShell(func() string {
		return localtools.DetectPlatformShell().Summary()
	}))
	if err != nil {
		return nil, fmt.Errorf("failed to create macro environment: %w", err)
	}
	taskService := execservice.NewTasksEnv(engineCtx, envExec, toolsRepo)

	engine.TaskService = taskService
	engine.Tracker = tracker
	engine.TaskEventSink = eventSink
	engine.MCPManager = mgr
	engine.LocalTools = localToolNames

	oldStop := engine.Stop
	engine.Stop = func() {
		mgr.StopAll()
		oldStop()
	}
	success = true
	return engine, nil
}

func buildTools(engineCtx context.Context, cfg Config, db libdbexec.DBManager, tracker libtracker.ActivityTracker, bus libbus.Messenger) (*mcpworker.Manager, []string, taskengine.ToolsRepo, error) {
	store := runtimetypes.New(db.WithoutTransaction())
	mgr, err := mcpworker.New(engineCtx, store, bus, tracker)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create mcp worker manager: %w", err)
	}
	if err := mgr.WatchEvents(engineCtx); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to start mcp event watcher: %w", err)
	}

	localToolNames := make([]string, 0, len(cfg.LocalTools))
	for name := range cfg.LocalTools {
		localToolNames = append(localToolNames, name)
	}
	toolsRepo := tools.NewPersistentRepo(cfg.LocalTools, db, http.DefaultClient, bus, tracker)

	if cfg.EnableHITL {
		if cfg.AskApproval == nil {
			return nil, nil, nil, fmt.Errorf("enginesvc: EnableHITL is true but AskApproval callback is nil")
		}
		hitlSvc := cfg.HITLService
		if hitlSvc == nil {
			hitlTenant := cfg.TenantID
			if hitlTenant == "" {
				hitlTenant = runtimetypes.LocalTenantID
			}
			hitlSvc = hitlservice.NewWithDefaultPolicy(cfg.HITLPolicySource, hitlTenant, store, tracker, cfg.HITLDefaultPolicyName)
		}
		raw := toolsRepo
		toolsRepo = hitlExemptProviders(
			localtools.NewHITLWrapper(toolsRepo, cfg.AskApproval, hitlSvc, tracker, cfg.TaskEventSink),
			raw,
			missiontools.ToolsProviderName,
		)
	}

	toolsRepo = toolguidance.WrapFromEnv(toolsRepo)

	return mgr, localToolNames, toolsRepo, nil
}

type hitlExemptRepo struct {
	taskengine.ToolsRepo
	raw    taskengine.ToolsRepo
	exempt map[string]bool
}

func hitlExemptProviders(gated, raw taskengine.ToolsRepo, providers ...string) taskengine.ToolsRepo {
	exempt := make(map[string]bool, len(providers))
	for _, p := range providers {
		exempt[p] = true
	}
	return &hitlExemptRepo{ToolsRepo: gated, raw: raw, exempt: exempt}
}

func (h *hitlExemptRepo) Exec(ctx context.Context, startingTime time.Time, input any, debug bool, args *taskengine.ToolsCall) (any, taskengine.DataType, error) {
	if args != nil && h.exempt[args.Name] {
		return h.raw.Exec(ctx, startingTime, input, debug, args)
	}
	return h.ToolsRepo.Exec(ctx, startingTime, input, debug, args)
}
