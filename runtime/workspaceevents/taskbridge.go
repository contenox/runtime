package workspaceevents

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/contenox/runtime/runtime/taskengine"
)

const maxCommandSubject = 200

type SessionResolver func(ctx context.Context) (session, cwd string)

type TaskEventBridge struct {
	inner   taskengine.TaskEventSink
	events  Service
	session SessionResolver
}

func NewTaskEventBridge(inner taskengine.TaskEventSink, events Service, session SessionResolver) *TaskEventBridge {
	return &TaskEventBridge{inner: inner, events: events, session: session}
}

func (b *TaskEventBridge) Wants(kind taskengine.TaskEventKind) bool {
	if kind == taskengine.TaskEventToolCall && b.events != nil {
		return true
	}
	return b.inner != nil && b.inner.Wants(kind)
}

func (b *TaskEventBridge) PublishTaskEvent(ctx context.Context, event taskengine.TaskEvent) error {
	var err error
	if b.inner != nil && b.inner.Wants(event.Kind) {
		err = b.inner.PublishTaskEvent(ctx, event)
	}
	if event.Kind == taskengine.TaskEventToolCall {
		b.observe(ctx, event)
	}
	return err
}

func toolLeaf(name string) (provider, leaf string) {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[:i], name[i+1:]
	}
	return "", name
}

func (b *TaskEventBridge) observe(ctx context.Context, event taskengine.TaskEvent) {
	if b.events == nil || b.session == nil || event.Error != "" {
		return
	}
	provider, leaf := toolLeaf(event.ToolName)
	wrote := leaf == "write_file" || leaf == "edit_file" || leaf == "sed"
	ran := provider == "local_shell" || leaf == "local_shell"
	if !wrote && !ran {
		return
	}
	session, cwd := b.session(ctx)
	if cwd == "" {
		return
	}
	root, err := b.events.GrantedRoot(cwd)
	if err != nil {
		return
	}
	data := map[string]any{"toolCallId": event.ApprovalID, "tool": event.ToolName}
	if session != "" {
		data["sessionId"] = session
	}
	in := Input{Source: SourceTool}
	switch {
	case wrote:
		path := strings.TrimSpace(event.ToolDiffPath)
		if path == "" {
			path, _ = event.ApprovalArgs["path"].(string)
		}
		if strings.TrimSpace(path) == "" {
			return
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		in.Type = TypeToolWrote
		in.Subject = path
	case ran:
		in.Type = TypeToolRan
		in.Subject = commandSubject(event.ApprovalArgs)
		if code, ok := exitCode(event.Content); ok {
			data["exitCode"] = code
		}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	in.Data = raw
	appendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_, _ = b.events.Append(appendCtx, root, in)
}

func commandSubject(args map[string]any) string {
	command, _ := args["command"].(string)
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	name := strings.ReplaceAll(fields[0], "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if len(name) > maxCommandSubject {
		name = name[:maxCommandSubject]
	}
	return name
}

func exitCode(content string) (int, bool) {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "{") {
		return 0, false
	}
	var result struct {
		ExitCode *int `json:"exit_code"`
	}
	if err := json.Unmarshal([]byte(trimmed), &result); err != nil || result.ExitCode == nil {
		return 0, false
	}
	return *result.ExitCode, true
}
