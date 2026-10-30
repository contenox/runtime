package contenoxcli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/contenox/runtime/runtime/homedir"

	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/agentdecl"
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
	}
	if opts.EffectiveEnableLocalExec {
		execOpts := []localtools.LocalExecOption{}
		if opts.EffectiveLocalExecAllowedDir != "" {
			execOpts = append(execOpts, localtools.WithLocalExecAllowedDir(opts.EffectiveLocalExecAllowedDir))
		}
		tools["local_shell"] = localtools.NewLocalExecTools(execOpts...)

		if !opts.EffectiveHITL && opts.EffectiveLocalExecAllowedDir == "" {
			slog.Warn("local_shell is enabled with no HITL and no allowed-dir; chain-level tools_policies is the only safety gate")
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
	dirs := []string{filepath.Join(primaryDir, agentdecl.GeneratedDirName), primaryDir}
	if dir, err := homedir.Dir(); err == nil {
		dirs = append(dirs, dir)
	}
	return hitlservice.NewFSPolicySource(dirs...)
}
