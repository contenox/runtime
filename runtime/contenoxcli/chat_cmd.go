package contenoxcli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/agentservice"
	"github.com/contenox/runtime/runtime/localtools"
	"github.com/contenox/runtime/runtime/taskengine"
)

type chatOpts struct {
	EffectiveDB                  string
	EffectiveChain               string
	EffectiveDefaultModel        string
	EffectiveDefaultProvider     string
	EffectiveConfiguredModel     string
	EffectiveConfiguredProvider  string
	EffectiveAltDefaultModel     string
	EffectiveAltDefaultProvider  string
	EffectiveMaxTokens           string
	EffectiveContext             int
	EffectiveNoDeleteModels      bool
	EffectiveEnableLocalExec     bool
	EffectiveLocalExecAllowedDir string
	EffectiveTracing             bool
	EffectiveSteps               bool
	EffectiveHITL                bool
	EffectiveRaw                 bool
	EffectiveThink               string
	HistoryTrim                  int
	LastN                        int
	InputValue                   string
	InputFlagPassed              bool
	ContenoxDir                  string
	EffectiveSkipBackendCycle    bool
	EffectiveAskApproval         localtools.AskApproval
	EffectiveTaskEventSink       taskengine.TaskEventSink
}

func execChat(ctx context.Context, db libdb.DBManager, opts chatOpts, out, errW io.Writer) error {
	engine, err := BuildEngine(ctx, db, opts)
	if err != nil {
		return fmt.Errorf("failed to build engine: %w", err)
	}
	defer engine.Stop()

	if err := PreflightLLMSetup(errW, engine.SetupCheck); err != nil {
		return err
	}

	chainPathAbs, err := filepath.Abs(opts.EffectiveChain)
	if err != nil {
		return fmt.Errorf("invalid chain path: %w", err)
	}
	chain, err := loadChainFromFile(chainPathAbs)
	if err != nil {
		return err
	}

	in := opts.InputValue
	if !opts.InputFlagPassed {
		stdinData, ok, err := readStdinIfAvailable(maxCLIStdinBytes)
		if err != nil {
			return err
		}
		stdinStr := strings.TrimSpace(stdinData)
		if ok && stdinStr != "" {
			if in != "" {
				in = in + "\n\n" + stdinStr
			} else {
				in = stdinStr
			}
		}
	}
	if in == "" {
		return fmt.Errorf("no input for chain: pass input as args, --input, or pipe via stdin")
	}

	workspaceID := ResolveWorkspaceID(opts.ContenoxDir)

	sessionReportErr, _, sessionEnd := engine.Tracker.Start(ctx, "resolve", "active_session")
	sessionID, err := ensureDefaultSession(ctx, db, workspaceID)
	if err != nil {
		sessionReportErr(err)
		fmt.Fprintf(errW, "warning: failed to resolve active session — history will not be persisted: %v\n", err)
		sessionID = ""
	}
	sessionEnd()

	templateVars := buildTemplateVars(opts)

	ag := agentservice.New(agentservice.Deps{
		Engine:      engine,
		DB:          db,
		WorkspaceID: workspaceID,
	})

	if opts.EffectiveTracing {
		slog.Info("Executing chain", "chain", chainPathAbs)
	} else {
		fmt.Fprintln(errW, "Thinking...")
	}

	stopTrace := startTraceStream(ctx, opts, engine, errW)
	defer stopTrace()
	stopThoughts := startThoughtStream(ctx, engine, errW, opts.EffectiveThink)
	defer stopThoughts()

	agentsMD, agentsMDSource := loadAgentsMDFromCwd()

	resp, err := ag.Prompt(ctx, agentservice.PromptRequest{
		SessionID:      sessionID,
		Input:          in,
		Chain:          chain,
		TemplateVars:   templateVars,
		ContextLength:  opts.EffectiveContext,
		HistoryTrim:    opts.HistoryTrim,
		AgentsMD:       agentsMD,
		AgentsMDSource: agentsMDSource,
	})
	if err != nil {
		if isModelResolverFailure(err) {
			PrintSetupIssues(errW, engine.SetupCheck)
		}
		return fmt.Errorf("chain execution failed: %w", err)
	}

	if shouldPrintThinking(opts.EffectiveThink) {
		if hist, ok := resp.Output.(taskengine.ChatHistory); ok {
			for _, msg := range hist.Messages {
				if msg.Role == "assistant" && msg.Thinking != "" {
					fmt.Fprintln(errW, "\nReasoning:")
					fmt.Fprintln(errW, msg.Thinking)
				}
			}
		}
	}
	printRelevantOutput(out, resp.Output, resp.OutputType, opts.EffectiveRaw)

	if opts.LastN > 0 {
		if hist, ok := resp.Output.(taskengine.ChatHistory); ok {
			var visible []taskengine.Message
			for _, m := range hist.Messages {
				if m.Role != "system" && m.Role != "tool" && len(m.CallTools) == 0 {
					visible = append(visible, m)
				}
			}
			if opts.LastN < len(visible) {
				visible = visible[len(visible)-opts.LastN:]
			}
			if len(visible) > 0 {
				fmt.Fprintln(errW, "\n── last", opts.LastN, "turns ──────────────────────")
				for _, m := range visible {
					fmt.Fprintf(errW, "[%s] %s:\n  %s\n\n", m.Timestamp.Format("15:04:05"), m.Role, m.Content)
				}
				fmt.Fprintln(errW, "────────────────────────────────────")
			}
		}
	}
	if opts.EffectiveSteps && len(resp.Steps) > 0 {
		fmt.Fprintln(errW, "\n📋 Steps:")
		for i, u := range resp.Steps {
			fmt.Fprintf(errW, "  %d. %s (%s) %s %s\n", i+1, u.TaskID, u.TaskHandler, formatDuration(u.Duration), u.Transition)
		}
	}
	return nil
}

func buildTemplateVars(opts chatOpts) map[string]string {
	templateVars := map[string]string{
		"model":    opts.EffectiveDefaultModel,
		"provider": opts.EffectiveDefaultProvider,
		"think":    opts.EffectiveThink,
	}

	defaultModel := opts.EffectiveConfiguredModel
	if defaultModel == "" {
		defaultModel = opts.EffectiveDefaultModel
	}
	if defaultModel != "" {
		templateVars["default_model"] = defaultModel
	}

	defaultProvider := opts.EffectiveConfiguredProvider
	if defaultProvider == "" {
		defaultProvider = opts.EffectiveDefaultProvider
	}
	if defaultProvider != "" {
		templateVars["default_provider"] = defaultProvider
	}

	if opts.EffectiveAltDefaultModel != "" {
		templateVars["alt_model"] = opts.EffectiveAltDefaultModel
	}
	if opts.EffectiveAltDefaultProvider != "" {
		templateVars["alt_provider"] = opts.EffectiveAltDefaultProvider
	}
	if opts.EffectiveMaxTokens != "" {
		templateVars["max_tokens"] = opts.EffectiveMaxTokens
	}
	return templateVars
}
