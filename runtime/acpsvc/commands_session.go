package acpsvc

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/contenox/runtime/runtime/homedir"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	libacp "github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/chatservice"
	"github.com/contenox/runtime/runtime/taskengine"
)

const compactDefaultKeep = 8

func (t *Transport) handleClear(ctx context.Context, _ libacp.SessionID, sess *sessionEntry) (string, error) {
	mgr := chatservice.NewManager(sess.WorkspaceID)

	exec, commit, release, err := t.deps.DB.WithTransaction(ctx)
	if err != nil {
		return "", fmt.Errorf("start transaction: %w", err)
	}
	defer release()
	if err := mgr.ClearSession(ctx, exec, sess.InternalSessionID); err != nil {
		return "", err
	}
	if err := commit(ctx); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return "Conversation history cleared.", nil
}

func (t *Transport) handleCompact(ctx context.Context, _ libacp.SessionID, sess *sessionEntry, args string) (string, error) {
	keep := compactDefaultKeep
	if s := strings.TrimSpace(args); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return "", fmt.Errorf("invalid keep count %q: expected a non-negative integer", s)
		}
		keep = n
	}

	mgr := chatservice.NewManager(sess.WorkspaceID)
	history, err := mgr.ListMessages(ctx, t.deps.DB.WithoutTransaction(), sess.InternalSessionID)
	if err != nil {
		return "", fmt.Errorf("load history: %w", err)
	}
	if len(history) == 0 {
		return "", fmt.Errorf("no history to compact")
	}

	chain, err := t.loadCompactChain()
	if err != nil {
		return "", err
	}

	templateVars := t.chainTemplateVars(sess)
	templateVars["chain"] = chain.ID
	execCtx := taskengine.WithTemplateVars(ctx, templateVars)

	compacted, err := chatservice.CompactHistory(execCtx, t.deps.Engine.TaskService, chain, history, keep)
	if err != nil {
		return "", err
	}

	exec, commit, release, err := t.deps.DB.WithTransaction(ctx)
	if err != nil {
		return "", fmt.Errorf("start transaction: %w", err)
	}
	defer release()
	if err := mgr.ClearSession(ctx, exec, sess.InternalSessionID); err != nil {
		return "", err
	}
	if err := mgr.PersistDiff(ctx, exec, sess.InternalSessionID, compacted); err != nil {
		return "", fmt.Errorf("persist compacted history: %w", err)
	}
	if err := commit(ctx); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}

	return fmt.Sprintf("Compacted %d messages to %d (kept last %d).", len(history), len(compacted), keep), nil
}

func (t *Transport) loadCompactChain() (*taskengine.TaskChainDefinition, error) {
	const name = "chain-compact.json"
	var candidates []string
	if t.deps.ContenoxDir != "" {
		candidates = append(candidates, filepath.Join(t.deps.ContenoxDir, name))
	}
	if hd, err := homedir.Dir(); err == nil {
		candidates = append(candidates, filepath.Join(hd, name))
	}
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var chain taskengine.TaskChainDefinition
		if err := json.Unmarshal(data, &chain); err != nil {
			return nil, fmt.Errorf("invalid chain JSON at %q: %w", p, err)
		}
		if chain.ID == "" {
			return nil, fmt.Errorf("chain at %q has empty ID", p)
		}
		return &chain, nil
	}
	return nil, fmt.Errorf("%s not found in %q or ~/.contenox-harness; run 'contenox init' to populate it", name, t.deps.ContenoxDir)
}
