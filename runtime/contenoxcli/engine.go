package contenoxcli

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/enginesvc"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/internal/setupcheck"
	"github.com/contenox/runtime/runtime/localtools"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/contenox/runtime/runtime/vfs"
)

func ComputeReadiness(ctx context.Context, db libdbexec.DBManager, opts chatOpts) (setupcheck.Result, error) {
	engine, err := BuildEngine(ctx, db, opts)
	if err != nil {
		return setupcheck.Result{}, err
	}
	defer engine.Stop()
	return setupcheck.EnrichResultWithOllamaProbe(ctx, engine.SetupCheck), nil
}

type Engine = enginesvc.Engine

func BuildEngine(ctx context.Context, db libdbexec.DBManager, opts chatOpts) (*Engine, error) {
	var tracker libtracker.ActivityTracker = libtracker.NoopTracker{}
	if opts.EffectiveTracing {
		tracker = libtracker.NewLogActivityTracker(slog.Default())
	}

	reportErr, reportChange, end := tracker.Start(ctx, "build", "engine")
	defer end()

	if err := vfs.SetControlPlaneDenied(controlPlaneDirs(opts.ContenoxDir)...); err != nil {
		err = fmt.Errorf("register control-plane isolation: %w", err)
		reportErr(err)
		return nil, err
	}

	tools := map[string]taskengine.ToolsRepo{
		"echo":     localtools.NewEchoTools(),
		"print":    localtools.NewPrint(tracker),
		"webtools": localtools.NewWebCaller(tracker),
		"local_fs": localtools.NewLocalFSTools(opts.EffectiveLocalExecAllowedDir, db),
		localtools.GitToolsName: localtools.NewGitToolsWith(
			opts.EffectiveLocalExecAllowedDir, localtools.GitToolsName, nil,
		),
	}
	if opts.EffectiveEnableLocalExec {
		gate := localtools.GateRequireAllowlist
		if opts.EffectiveHITL {
			gate = localtools.GatePolicy
		}
		execOpts := []localtools.LocalExecOption{
			localtools.WithLocalExecTracker(tracker),
			localtools.WithLocalExecGate(gate),
		}
		if opts.EffectiveLocalExecAllowedDir != "" {
			execOpts = append(execOpts, localtools.WithLocalExecAllowedDir(opts.EffectiveLocalExecAllowedDir))
		}
		tools["local_shell"] = localtools.NewLocalExecTools(execOpts...)

		if !opts.EffectiveHITL {
			slog.Warn("local_shell runs without HITL; it refuses every command unless the chain's tools_policies names _allowed_commands")
		}
	}

	askApproval := opts.EffectiveAskApproval
	if askApproval == nil {
		askApproval = NewCLIAskApproval(os.Stderr)
	}

	readinessModel, readinessProvider := readinessDefaults(opts)

	reportChange("phase", "tools_prepared")
	engine, err := enginesvc.Build(ctx, db, enginesvc.Config{
		DefaultModel:             opts.EffectiveDefaultModel,
		DefaultProvider:          opts.EffectiveDefaultProvider,
		AltDefaultModel:          opts.EffectiveAltDefaultModel,
		AltDefaultProvider:       opts.EffectiveAltDefaultProvider,
		ReadinessDefaultModel:    readinessModel,
		ReadinessDefaultProvider: readinessProvider,
		ContextLength:            opts.EffectiveContext,
		NoDeleteModels:           opts.EffectiveNoDeleteModels,
		LocalTools:               tools,
		EnableHITL:               opts.EffectiveHITL,
		AskApproval:              askApproval,
		Tracker:                  tracker,
		Tracing:                  opts.EffectiveTracing,
		SkipBackendCycle:         opts.EffectiveSkipBackendCycle,
		WorkspaceID:              ResolveWorkspaceID(opts.ContenoxDir),
		HITLPolicySource:         hitlPolicySource(opts.ContenoxDir),
		TaskEventSink:            opts.EffectiveTaskEventSink,
	})
	if err != nil {
		reportErr(err)
		return nil, err
	}
	reportChange("phase", "enginesvc_built")
	return engine, nil
}

func readinessDefaults(opts chatOpts) (model, provider string) {
	if opts.EffectiveDefaultModel != opts.EffectiveConfiguredModel &&
		opts.EffectiveDefaultModel != "" {
		model = opts.EffectiveDefaultModel
	}
	if opts.EffectiveDefaultProvider != opts.EffectiveConfiguredProvider &&
		opts.EffectiveDefaultProvider != "" {
		provider = opts.EffectiveDefaultProvider
	}
	return model, provider
}

func hitlPolicySource(primaryDir string) hitlservice.PolicySource {
	return hitlservice.NewFSPolicySource(hitlPolicyDirs(primaryDir)...)
}

func hitlPolicyDirs(primaryDir string) []string {
	return acpPolicyDirs(primaryDir)
}
