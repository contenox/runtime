package acpsvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	libacp "github.com/contenox/runtime/libacp"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/agentservice"
	"github.com/contenox/runtime/runtime/chatservice"
	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/contenox/runtime/runtime/vfs"
)

const sessionListTitleMaxLen = 60

func truncateSessionListTitle(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) <= sessionListTitleMaxLen {
		return s
	}
	if sessionListTitleMaxLen <= 3 {
		return string(runes[:sessionListTitleMaxLen])
	}
	return string(runes[:sessionListTitleMaxLen-3]) + "..."
}

const mcpNamePrefix = runtimetypes.ACPMCPServerNamePrefix

func mcpNameFor(connectionID string, sessionID libacp.SessionID, original string) string {
	sum := sha256.Sum256([]byte(connectionID + "\x00" + string(sessionID) + "\x00" + original))
	hash := hex.EncodeToString(sum[:])[:12]
	return mcpNamePrefix + hash + "-" + sanitizeMCPNameComponent(original)
}

func sanitizeMCPNameComponent(name string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z':
			sb.WriteRune(r)
		case r >= '0' && r <= '9':
			sb.WriteRune(r)
		case r == '_' || r == '-':
			sb.WriteRune(r)
		default:
			sb.WriteByte('_')
		}
		if sb.Len() >= 48 {
			break
		}
	}
	out := strings.Trim(sb.String(), "_-")
	if out == "" {
		return "mcp"
	}
	return out
}

func (t *Transport) LoadSession(ctx context.Context, req libacp.LoadSessionRequest) (libacp.LoadSessionResponse, error) {
	reportErr, reportChange, end := t.tracker().Start(ctx, "load", "acp_session", "session_id", string(req.SessionID))
	defer end()

	if req.SessionID == "" {
		err := libacp.NewError(libacp.ErrInvalidParams, "sessionId is required")
		reportErr(err)
		return libacp.LoadSessionResponse{}, err
	}
	if !cwdIsUsable(req.Cwd) {
		err := libacp.NewErrorf(libacp.ErrInvalidParams, "cwd must be an absolute path, got %q", req.Cwd)
		reportErr(err)
		return libacp.LoadSessionResponse{}, err
	}
	if t.deps.Engine == nil {
		err := errSetupRequired()
		reportErr(err)
		return libacp.LoadSessionResponse{}, err
	}
	store := runtimetypes.New(t.deps.DB.WithoutTransaction())
	sessionCwd, err := t.resolveExistingSessionCwd(ctx, store, req.SessionID, req.Cwd)
	if err != nil {
		reportErr(err)
		return libacp.LoadSessionResponse{}, err
	}
	workspaceID := t.workspaceID()
	if resolved, ok := t.resolveSessionWorkspace(ctx, string(req.SessionID)); ok {
		workspaceID = resolved
	}

	registered, err := t.registerMcpServers(ctx, store, req.SessionID, req.McpServers)
	if err != nil {
		reportErr(err)
		return libacp.LoadSessionResponse{}, err
	}

	ag := agentservice.New(agentservice.Deps{
		Engine:      t.deps.Engine,
		DB:          t.deps.DB,
		WorkspaceID: workspaceID,
		Identity:    "acp-client",
	})

	contenoxSessionID, messages, err := ag.SessionLoad(ctx, string(req.SessionID))
	if err != nil {
		t.cleanupMcpServers(ctx, store, registered)
		wrapped := libacp.NewErrorf(libacp.ErrInvalidParams, "load session %q: %v", req.SessionID, err)
		reportErr(wrapped)
		return libacp.LoadSessionResponse{}, wrapped
	}

	entry := &sessionEntry{
		WorkspaceID:       workspaceID,
		Cwd:               sessionCwd,
		InternalSessionID: contenoxSessionID,
		McpServerNames:    registered,
		driver:            &nativeDriver{t: t, agent: ag},
		Provider:          t.provider(),
		Model:             t.model(),
		Think:             t.thinkDefault(),
		HITLPolicy:        hitlPolicyDefaultValue,
	}
	t.sessionMu.Lock()
	t.sessions[req.SessionID] = entry
	t.bindContenoxSession(contenoxSessionID, req.SessionID)
	t.sessionMu.Unlock()
	t.persistSessionCwd(ctx, store, req.SessionID, sessionCwd)
	t.markExternalIfPersisted(ctx, store, req.SessionID, entry)
	entry.FiredMissions = t.readSessionFiredMission(ctx, store, req.SessionID)

	t.clearToolCallState(req.SessionID)
	t.subscribeTerminal(req.SessionID, contenoxSessionID)
	_, isExternal := entry.driver.(*externalDriver)
	t.replayMessages(ctx, req.SessionID, messages, isExternal)
	t.reattachNativeTurn(ctx, req.SessionID)
	if _, isExternal := entry.driver.(*externalDriver); isExternal {
		t.reemitExternalCommandMenu(ctx, store, req.SessionID)
	} else if entry.driver.AvailableCommands() != nil {
		libacp.AfterResponse(ctx, func() {
			t.sendAvailableCommands(ctx, req.SessionID)
			if banner := t.takeBanner(); banner != "" {
				t.sendUpdate(ctx, libacp.SessionNotification{
					SessionID: req.SessionID,
					Update:    libacp.NewAgentMessageChunk(banner),
				})
			}
		})
	}

	reportChange(string(req.SessionID), map[string]any{
		"contenox_session_id": contenoxSessionID,
		"message_count":       len(messages),
	})
	return libacp.LoadSessionResponse{ConfigOptions: t.reloadedConfigOptions(ctx, store, req.SessionID, entry)}, nil
}

func (t *Transport) replayMessages(ctx context.Context, sessionID libacp.SessionID, messages []taskengine.Message, external bool) {
	_, reportChange, end := t.tracker().Start(ctx, "replay", "acp_session", "session_id", string(sessionID), "message_count", len(messages))
	defer end()

	var users, assistantText, toolCalls, toolResults int
	for i, m := range messages {
		messageID := fmt.Sprintf("replay-%d", i)
		switch m.Role {
		case "user":
			if m.Content == "" {
				continue
			}
			update := libacp.NewUserMessageChunk(m.Content)
			update.MessageID = messageID
			t.sendUpdate(ctx, libacp.SessionNotification{
				SessionID: sessionID,
				Update:    update,
			})
			users++
		case "assistant":
			if m.Thinking != "" {
				update := libacp.NewAgentThoughtChunk(m.Thinking)
				update.MessageID = messageID
				t.sendUpdate(ctx, libacp.SessionNotification{
					SessionID: sessionID,
					Update:    update,
				})
			}
			if m.Content != "" {
				update := libacp.NewAgentMessageChunk(m.Content)
				update.MessageID = messageID
				t.sendUpdate(ctx, libacp.SessionNotification{
					SessionID: sessionID,
					Update:    update,
				})
				assistantText++
			}
			for _, tc := range m.CallTools {
				t.sendUpdate(ctx, libacp.SessionNotification{
					SessionID: sessionID,
					Update:    toolCallUpdateFromCall(tc),
				})
				toolCalls++
			}
		case "tool":
			update := toolCallUpdateFromResult(m)
			if external {
				if u, ok := externalToolReplayUpdate(m); ok {
					update = u
				}
			}
			t.sendUpdate(ctx, libacp.SessionNotification{
				SessionID: sessionID,
				Update:    update,
			})
			toolResults++
		}
	}
	reportChange(string(sessionID), map[string]any{
		"user":         users,
		"assistant":    assistantText,
		"tool_calls":   toolCalls,
		"tool_results": toolResults,
	})

	t.sendInitialUsageUpdate(ctx, sessionID)
}

func toolCallUpdateFromCall(tc taskengine.ToolCall) libacp.SessionUpdate {
	title := tc.Function.Name
	var argsMap map[string]any
	if tc.Function.Arguments != "" && json.Valid([]byte(tc.Function.Arguments)) {
		_ = json.Unmarshal([]byte(tc.Function.Arguments), &argsMap)
	}
	if summary := summarizeToolCallArgs(tc.Function.Name, argsMap); summary != "" {
		title = tc.Function.Name + ": " + summary
	}
	update := libacp.SessionUpdate{
		SessionUpdate: libacp.SessionUpdateToolCall,
		ToolCallID:    tc.ID,
		Title:         title,
		Kind:          toolKindFor(tc.Function.Name),
		Status:        libacp.ToolCallStatusCompleted,
	}
	if tc.Function.Arguments != "" && json.Valid([]byte(tc.Function.Arguments)) {
		update.RawInput = json.RawMessage(tc.Function.Arguments)
	}
	return update
}

func externalToolReplayUpdate(m taskengine.Message) (libacp.SessionUpdate, bool) {
	var rec externalToolRecord
	if err := json.Unmarshal([]byte(m.Content), &rec); err != nil {
		return libacp.SessionUpdate{}, false
	}
	toolCallID := rec.ToolCallID
	if toolCallID == "" {
		toolCallID = m.ToolCallID
	}
	if toolCallID == "" {
		return libacp.SessionUpdate{}, false
	}
	status := rec.Status
	if status == "" {
		status = libacp.ToolCallStatusCompleted
	}
	title := rec.Title
	if title == "" {
		title = toolCallID
	}
	return libacp.SessionUpdate{
		SessionUpdate: libacp.SessionUpdateToolCall,
		ToolCallID:    toolCallID,
		Title:         title,
		Kind:          rec.Kind,
		Status:        status,
		RawInput:      rec.RawInput,
		RawOutput:     rec.RawOutput,
		ToolContent:   rec.ToolContent,
		Locations:     rec.Locations,
	}, true
}

func toolCallUpdateFromResult(m taskengine.Message) libacp.SessionUpdate {
	update := libacp.SessionUpdate{
		SessionUpdate: libacp.SessionUpdateToolCallUpdate,
		ToolCallID:    m.ToolCallID,
		Status:        libacp.ToolCallStatusCompleted,
	}
	if m.Content != "" {
		update.RawOutput = json.RawMessage(jsonString(m.Content))
		if diff := diffContentFromResult(m.Content); diff != nil {
			update.ToolContent = []libacp.ToolCallContent{*diff}
		}
	}
	if text, failed := toolResultFailure(m); failed {
		update.Status = libacp.ToolCallStatusFailed
		block := libacp.NewTextContent(text)
		update.ToolContent = append([]libacp.ToolCallContent{{
			Type:    libacp.ToolCallContentRegular,
			Content: &block,
		}}, update.ToolContent...)
		update.Meta = json.RawMessage(`{"error":` + jsonString(text) + `}`)
	}
	return update
}

func toolResultFailure(m taskengine.Message) (string, bool) {
	if m.ToolResultError {
		return toolResultErrorText(m.Content), true
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(m.Content), &object); err == nil && len(object) == 1 {
		if raw, ok := object["error"]; ok {
			var text string
			if err := json.Unmarshal(raw, &text); err == nil && text != "" {
				return text, true
			}
		}
	}
	if strings.HasPrefix(m.Content, "tool ") && strings.Contains(m.Content, " execution failed") {
		return m.Content, true
	}
	if strings.HasPrefix(m.Content, "the arguments for tool ") && strings.Contains(m.Content, "never reached the runtime") {
		return m.Content, true
	}
	return "", false
}

func toolResultErrorText(content string) string {
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &object); err == nil && len(object) == 1 {
		if raw, ok := object["error"]; ok {
			var text string
			if err := json.Unmarshal(raw, &text); err == nil && text != "" {
				return text
			}
		}
	}
	return content
}

func cwdIsUsable(cwd string) bool {
	// "/" is the ACP sentinel for "the default workspace root", not a real
	// path: on Windows it is not absolute, but it must still mean the same.
	return cwd == "/" || filepath.IsAbs(cwd)
}

func errSetupRequired() error {
	return libacp.NewError(libacp.ErrAuthRequired, "contenox is not configured yet: no default-model is set. Run the \"Setup Contenox\" auth method, set the CONTENOX_DEFAULT_* environment variables (or run `contenox acp --setup`), then reconnect.")
}

func (t *Transport) resolveWorkspaceCwd(cwd string) (string, error) {
	resolved, err := vfs.ResolveSessionCwd(t.deps.WorkspaceRoots, cwd, "")
	if err != nil {
		return "", libacp.NewError(libacp.ErrInvalidParams, err.Error())
	}
	return resolved, nil
}

func (t *Transport) resolveExistingSessionCwd(ctx context.Context, store runtimetypes.Store, sid libacp.SessionID, cwd string) (string, error) {
	if f := t.deps.WorkspaceRoots; f != nil && (cwd == "" || cwd == "/") {
		if existing := t.sessionCwd(ctx, store, sid); existing != "" {
			if resolved, ok := f.Allows(existing); ok {
				return resolved, nil
			}
		}
	}
	return t.resolveWorkspaceCwd(cwd)
}

func (t *Transport) NewSession(ctx context.Context, req libacp.NewSessionRequest) (libacp.NewSessionResponse, error) {
	internalID := newSessionID(sessionNamespace(t))
	sessionID := libacp.SessionID(internalID)

	reportErr, reportChange, end := t.tracker().Start(ctx, "new", "acp_session", "session_id", string(sessionID), "cwd", req.Cwd, "mcp_servers", len(req.McpServers))
	defer end()

	// "/" is the ACP sentinel for "the default workspace root", not a real
	// path: on Windows it is not absolute, but it must still mean the same.
	if !cwdIsUsable(req.Cwd) {
		err := libacp.NewErrorf(libacp.ErrInvalidParams, "cwd must be an absolute path, got %q", req.Cwd)
		reportErr(err)
		return libacp.NewSessionResponse{}, err
	}
	if t.deps.Engine == nil {
		err := errSetupRequired()
		reportErr(err)
		return libacp.NewSessionResponse{}, err
	}
	sessionCwd, err := t.resolveWorkspaceCwd(req.Cwd)
	if err != nil {
		reportErr(err)
		return libacp.NewSessionResponse{}, err
	}

	workspaceID := t.workspaceID()

	store := runtimetypes.New(t.deps.DB.WithoutTransaction())

	if ref, ok := parseAdoptMeta(req.Meta); ok {
		resp, adoptErr := t.newAdoptedSession(ctx, internalID, sessionID, sessionCwd, workspaceID, store, ref, reportChange)
		if adoptErr != nil {
			reportErr(adoptErr)
			return libacp.NewSessionResponse{}, adoptErr
		}
		return resp, nil
	}

	if agentName := parseAgentMeta(req.Meta); agentName != "" {
		att, spawnErr := t.bringUpExternal(ctx, sessionID, sessionCwd, agentName, false)
		if spawnErr != nil {
			reportErr(spawnErr)
			return libacp.NewSessionResponse{}, spawnErr
		}
		bridge := att.bridge
		ag := agentservice.New(agentservice.Deps{
			Engine:      t.deps.Engine,
			DB:          t.deps.DB,
			WorkspaceID: workspaceID,
			Identity:    "acp-client",
		})
		contenoxSessionID, sessErr := ag.SessionNew(ctx, internalID)
		if sessErr != nil {
			att.teardown(t)
			wrapped := fmt.Errorf("acpsvc: agent.SessionNew: %w", sessErr)
			reportErr(wrapped)
			return libacp.NewSessionResponse{}, wrapped
		}
		entry := &sessionEntry{
			WorkspaceID:       workspaceID,
			Cwd:               sessionCwd,
			InternalSessionID: contenoxSessionID,
			HITLPolicy:        hitlPolicyDefaultValue,
			driver: &externalDriver{
				t:            t,
				agentName:    agentName,
				upstreamID:   sessionID,
				conn:         att.conn,
				handle:       att.handle,
				instanceID:   att.instanceID,
				downstreamID: att.downstreamID,
				bridge:       bridge,
			},
		}
		t.sessionMu.Lock()
		t.sessions[sessionID] = entry
		t.bindContenoxSession(contenoxSessionID, sessionID)
		t.sessionMu.Unlock()
		t.persistSessionCwd(ctx, store, sessionID, sessionCwd)
		t.persistSessionAgent(ctx, store, sessionID, agentName)
		t.persistSessionInstance(ctx, sessionID, att.instanceID)
		t.persistSessionDownstream(ctx, sessionID, att.downstreamID)
		t.clearToolCallState(sessionID)

		libacp.AfterResponse(ctx, func() {
			bridge.markBound(ctx)
		})

		reportChange(string(sessionID), map[string]any{
			"contenox_session_id": contenoxSessionID,
			"workspace_id":        workspaceID,
			"external_agent":      agentName,
		})
		return libacp.NewSessionResponse{
			SessionID:     sessionID,
			ConfigOptions: t.sessionConfigOptions(ctx, entry),
			Meta:          agentMetaJSON(entry.driver.AgentName()),
		}, nil
	}

	registered, err := t.registerMcpServers(ctx, store, sessionID, req.McpServers)
	if err != nil {
		reportErr(err)
		return libacp.NewSessionResponse{}, err
	}

	ag := agentservice.New(agentservice.Deps{
		Engine:      t.deps.Engine,
		DB:          t.deps.DB,
		WorkspaceID: workspaceID,
		Identity:    "acp-client",
	})

	contenoxSessionID, err := ag.SessionNew(ctx, internalID)
	if err != nil {
		t.cleanupMcpServers(ctx, store, registered)
		wrapped := fmt.Errorf("acpsvc: agent.SessionNew: %w", err)
		reportErr(wrapped)
		return libacp.NewSessionResponse{}, wrapped
	}

	missionID, _ := missionservice.ParseMissionMeta(req.Meta)

	entry := &sessionEntry{
		WorkspaceID:       workspaceID,
		Cwd:               sessionCwd,
		InternalSessionID: contenoxSessionID,
		McpServerNames:    registered,
		MissionID:         missionID,
		driver:            &nativeDriver{t: t, agent: ag},
		Provider:          t.provider(),
		Model:             t.model(),
		Think:             t.thinkDefault(),
		HITLPolicy:        hitlPolicyDefaultValue,
	}
	t.sessionMu.Lock()
	t.sessions[sessionID] = entry
	t.bindContenoxSession(contenoxSessionID, sessionID)
	t.sessionMu.Unlock()
	t.persistSessionCwd(ctx, store, sessionID, sessionCwd)
	t.persistSessionMission(ctx, store, sessionID, missionID)
	t.clearToolCallState(sessionID)
	t.subscribeTerminal(sessionID, contenoxSessionID)

	libacp.AfterResponse(ctx, func() {
		t.sendAvailableCommands(ctx, sessionID)
		if banner := t.takeBanner(); banner != "" {
			t.sendUpdate(ctx, libacp.SessionNotification{
				SessionID: sessionID,
				Update:    libacp.NewAgentMessageChunk(banner),
			})
		}
		t.sendInitialUsageUpdate(ctx, sessionID)
	})

	reportChange(string(sessionID), map[string]any{
		"contenox_session_id": contenoxSessionID,
		"workspace_id":        workspaceID,
	})
	return libacp.NewSessionResponse{
		SessionID:     sessionID,
		ConfigOptions: t.sessionConfigOptions(ctx, entry),
	}, nil
}

func (t *Transport) ResumeSession(ctx context.Context, req libacp.ResumeSessionRequest) (libacp.ResumeSessionResponse, error) {
	reportErr, reportChange, end := t.tracker().Start(ctx, "resume", "acp_session", "session_id", string(req.SessionID))
	defer end()

	if req.SessionID == "" {
		err := libacp.NewError(libacp.ErrInvalidParams, "sessionId is required")
		reportErr(err)
		return libacp.ResumeSessionResponse{}, err
	}
	if !cwdIsUsable(req.Cwd) {
		err := libacp.NewErrorf(libacp.ErrInvalidParams, "cwd must be an absolute path, got %q", req.Cwd)
		reportErr(err)
		return libacp.ResumeSessionResponse{}, err
	}
	if t.deps.Engine == nil {
		err := errSetupRequired()
		reportErr(err)
		return libacp.ResumeSessionResponse{}, err
	}
	store := runtimetypes.New(t.deps.DB.WithoutTransaction())
	sessionCwd, err := t.resolveExistingSessionCwd(ctx, store, req.SessionID, req.Cwd)
	if err != nil {
		reportErr(err)
		return libacp.ResumeSessionResponse{}, err
	}
	workspaceID := t.workspaceID()
	if resolved, ok := t.resolveSessionWorkspace(ctx, string(req.SessionID)); ok {
		workspaceID = resolved
	}

	registered, err := t.registerMcpServers(ctx, store, req.SessionID, req.McpServers)
	if err != nil {
		reportErr(err)
		return libacp.ResumeSessionResponse{}, err
	}

	ag := agentservice.New(agentservice.Deps{
		Engine:      t.deps.Engine,
		DB:          t.deps.DB,
		WorkspaceID: workspaceID,
		Identity:    "acp-client",
	})

	contenoxSessionID, err := ag.SessionResume(ctx, string(req.SessionID))
	if err != nil {
		t.cleanupMcpServers(ctx, store, registered)
		wrapped := libacp.NewErrorf(libacp.ErrInvalidParams, "resume session %q: %v", req.SessionID, err)
		reportErr(wrapped)
		return libacp.ResumeSessionResponse{}, wrapped
	}

	entry := &sessionEntry{
		WorkspaceID:       workspaceID,
		Cwd:               sessionCwd,
		InternalSessionID: contenoxSessionID,
		McpServerNames:    registered,
		driver:            &nativeDriver{t: t, agent: ag},
		Provider:          t.provider(),
		Model:             t.model(),
		Think:             t.thinkDefault(),
		HITLPolicy:        hitlPolicyDefaultValue,
	}
	t.sessionMu.Lock()
	t.sessions[req.SessionID] = entry
	t.bindContenoxSession(contenoxSessionID, req.SessionID)
	t.sessionMu.Unlock()
	t.persistSessionCwd(ctx, store, req.SessionID, sessionCwd)
	t.markExternalIfPersisted(ctx, store, req.SessionID, entry)
	t.clearToolCallState(req.SessionID)
	t.subscribeTerminal(req.SessionID, contenoxSessionID)
	t.reattachNativeTurn(ctx, req.SessionID)

	if _, isExternal := entry.driver.(*externalDriver); isExternal {
		t.reemitExternalCommandMenu(ctx, store, req.SessionID)
	} else if entry.driver.AvailableCommands() != nil {
		libacp.AfterResponse(ctx, func() {
			t.sendAvailableCommands(ctx, req.SessionID)
		})
	}

	reportChange(string(req.SessionID), map[string]any{
		"contenox_session_id": contenoxSessionID,
	})
	return libacp.ResumeSessionResponse{ConfigOptions: t.reloadedConfigOptions(ctx, store, req.SessionID, entry)}, nil
}

func (t *Transport) SetSessionMode(_ context.Context, _ libacp.SetSessionModeRequest) (libacp.SetSessionModeResponse, error) {
	return libacp.SetSessionModeResponse{}, libacp.MethodNotFound(libacp.MethodSessionSetMode)
}

func (t *Transport) SetSessionModel(_ context.Context, _ libacp.SetSessionModelRequest) (libacp.SetSessionModelResponse, error) {
	return libacp.SetSessionModelResponse{}, libacp.MethodNotFound(libacp.MethodSessionSetModel)
}

func (t *Transport) CloseSession(ctx context.Context, req libacp.CloseSessionRequest) (libacp.CloseSessionResponse, error) {
	_, reportChange, end := t.tracker().Start(ctx, "close", "acp_session", "session_id", string(req.SessionID))
	defer end()

	if req.SessionID == "" {
		return libacp.CloseSessionResponse{}, libacp.NewError(libacp.ErrInvalidParams, "sessionId is required")
	}
	entry := t.dropSessionEntry(req.SessionID)
	if entry != nil && t.deps.DB != nil {
		store := runtimetypes.New(t.deps.DB.WithoutTransaction())
		t.cleanupMcpServers(ctx, store, entry.McpServerNames)
	}
	if entry != nil {
		_ = entry.driver.Close()
	}
	t.clearToolCallState(req.SessionID)
	t.closeTerminal(req.SessionID, entry)
	reportChange(string(req.SessionID), map[string]any{"was_open": entry != nil})
	return libacp.CloseSessionResponse{}, nil
}

func (t *Transport) DeleteSession(ctx context.Context, req libacp.DeleteSessionRequest) (libacp.DeleteSessionResponse, error) {
	reportErr, reportChange, end := t.tracker().Start(ctx, "delete", "acp_session", "session_id", string(req.SessionID))
	defer end()

	if req.SessionID == "" {
		err := libacp.NewError(libacp.ErrInvalidParams, "sessionId is required")
		reportErr(err)
		return libacp.DeleteSessionResponse{}, err
	}
	if t.deps.Engine == nil {
		err := errSetupRequired()
		reportErr(err)
		return libacp.DeleteSessionResponse{}, err
	}

	workspaceID := t.workspaceID()
	if resolved, ok := t.resolveSessionWorkspace(ctx, string(req.SessionID)); ok {
		workspaceID = resolved
	}

	entry := t.dropSessionEntry(req.SessionID)
	store := runtimetypes.New(t.deps.DB.WithoutTransaction())
	if entry != nil {
		t.cleanupMcpServers(ctx, store, entry.McpServerNames)
	}
	if entry != nil {
		_ = entry.driver.Close()
	}
	if t.deps.Instances != nil {
		if instanceID := t.readSessionInstance(ctx, store, req.SessionID); instanceID != "" {
			_ = t.deps.Instances.Stop(instanceID)
		}
	}
	if t.deps.NativeTurns != nil {
		t.deps.NativeTurns.Cancel(req.SessionID)
	}
	t.clearToolCallState(req.SessionID)
	t.closeTerminal(req.SessionID, entry)

	ag := agentservice.New(agentservice.Deps{
		Engine:      t.deps.Engine,
		DB:          t.deps.DB,
		WorkspaceID: workspaceID,
		Identity:    "acp-client",
	})
	if err := ag.SessionDelete(ctx, string(req.SessionID)); err != nil {
		reportErr(err)
		return libacp.DeleteSessionResponse{}, libacp.InternalError(err.Error())
	}
	_ = store.DeleteKV(ctx, acpSessionCwdKVPrefix+string(req.SessionID))
	_ = store.DeleteKV(ctx, acpSessionAgentKVPrefix+string(req.SessionID))
	_ = store.DeleteKV(ctx, acpSessionAgentCommandsKVPrefix+string(req.SessionID))
	_ = store.DeleteKV(ctx, acpSessionAgentConfigOptionsKVPrefix+string(req.SessionID))
	_ = store.DeleteKV(ctx, acpSessionHITLPolicyKVPrefix+string(req.SessionID))
	_ = store.DeleteKV(ctx, acpSessionInstanceKVPrefix+string(req.SessionID))
	_ = store.DeleteKV(ctx, acpSessionDownstreamKVPrefix+string(req.SessionID))
	_ = store.DeleteKV(ctx, acpSessionMissionKVPrefix+string(req.SessionID))

	reportChange(string(req.SessionID), map[string]any{"was_open": entry != nil})
	return libacp.DeleteSessionResponse{}, nil
}

func (t *Transport) dropSessionEntry(sid libacp.SessionID) *sessionEntry {
	t.cancelInflightPrompt(sid)
	t.sessionMu.Lock()
	defer t.sessionMu.Unlock()
	entry, ok := t.sessions[sid]
	if !ok {
		return nil
	}
	delete(t.sessions, sid)
	t.unbindContenoxSession(entry.InternalSessionID)
	return entry
}

func (t *Transport) registerMcpServers(ctx context.Context, store runtimetypes.Store, sessionID libacp.SessionID, servers []libacp.McpServer) ([]string, error) {
	var registered []string
	for _, srv := range servers {
		if err := srv.Validate(); err != nil {
			t.cleanupMcpServers(ctx, store, registered)
			return nil, fmt.Errorf("acpsvc: invalid mcp server %q: %w", srv.Name, err)
		}
		name := mcpNameFor(t.mcpOwnerID(), sessionID, srv.Name)
		row := mcpRowFromLibacp(name, srv)
		if err := store.UpsertMCPServerByName(ctx, row); err != nil {
			t.cleanupMcpServers(ctx, store, registered)
			return nil, fmt.Errorf("acpsvc: register mcp server %q: %w", srv.Name, err)
		}
		if t.deps.Engine != nil && t.deps.Engine.MCPManager != nil {
			if err := t.deps.Engine.MCPManager.StartWorker(ctx, row); err != nil {
				registered = append(registered, name)
				t.cleanupMcpServers(ctx, store, registered)
				return nil, fmt.Errorf("acpsvc: start mcp worker %q: %w", srv.Name, err)
			}
		}
		registered = append(registered, name)
	}
	return registered, nil
}

func (t *Transport) cleanupMcpServers(ctx context.Context, store runtimetypes.Store, names []string) {
	for _, name := range names {
		if t.deps.Engine != nil && t.deps.Engine.MCPManager != nil {
			t.deps.Engine.MCPManager.StopWorker(ctx, name)
		}
		cleanupMCPSessionIDs(ctx, store, name)
		row, err := store.GetMCPServerByName(ctx, name)
		if err != nil {
			if errors.Is(err, libdb.ErrNotFound) {
				continue
			}
			continue
		}
		_ = store.DeleteMCPServer(ctx, row.ID)
	}
}

func cleanupMCPSessionIDs(ctx context.Context, store runtimetypes.Store, serverName string) {
	prefix := "mcp_session:" + serverName + ":"
	for {
		page, err := store.ListKVPrefix(ctx, prefix, nil, 100)
		if err != nil {
			return
		}
		for _, kv := range page {
			_ = store.DeleteKV(ctx, kv.Key)
		}
		if len(page) < 100 {
			return
		}
	}
}

func (t *Transport) runtimeToolsAllowlist(ctx context.Context, store runtimetypes.Store, sessionNames []string) ([]string, error) {
	allowlist := []string{"*"}
	current := make(map[string]struct{}, len(sessionNames))
	for _, name := range sessionNames {
		current[name] = struct{}{}
	}
	var cursor *time.Time
	var cursorID string
	for {
		page, err := store.ListMCPServers(ctx, cursor, 100, cursorID)
		if err != nil {
			return nil, fmt.Errorf("acpsvc: list mcp servers for runtime allowlist: %w", err)
		}
		for _, srv := range page {
			if !runtimetypes.IsACPManagedMCPServerName(srv.Name) {
				continue
			}
			if _, ok := current[srv.Name]; ok {
				continue
			}
			allowlist = append(allowlist, "!"+srv.Name)
		}
		if len(page) < 100 {
			return allowlist, nil
		}
		cursor = &page[len(page)-1].CreatedAt
		cursorID = page[len(page)-1].ID
	}
}

func CleanupStaleACPManagedMCPServers(ctx context.Context, db libdb.DBManager) error {
	if db == nil {
		return nil
	}
	store := runtimetypes.New(db.WithoutTransaction())
	var stale []*runtimetypes.MCPServer
	var cursor *time.Time
	var cursorID string
	for {
		page, err := store.ListMCPServers(ctx, cursor, 100, cursorID)
		if err != nil {
			return err
		}
		for _, srv := range page {
			if runtimetypes.IsACPManagedMCPServerName(srv.Name) {
				stale = append(stale, srv)
			}
		}
		if len(page) < 100 {
			break
		}
		cursor = &page[len(page)-1].CreatedAt
		cursorID = page[len(page)-1].ID
	}
	for _, srv := range stale {
		cleanupMCPSessionIDs(ctx, store, srv.Name)
		if err := store.DeleteMCPServer(ctx, srv.ID); err != nil && !errors.Is(err, libdb.ErrNotFound) {
			return err
		}
	}
	return nil
}

func (t *Transport) mcpOwnerID() string {
	if t.connectionID != "" {
		return t.connectionID
	}
	return "conn-unknown"
}

func mcpRowFromLibacp(name string, srv libacp.McpServer) *runtimetypes.MCPServer {
	row := &runtimetypes.MCPServer{
		Name:                  name,
		Transport:             "stdio",
		Command:               srv.Command,
		Args:                  srv.Args,
		URL:                   srv.URL,
		ConnectTimeoutSeconds: 30,
	}
	switch srv.Kind() {
	case libacp.McpServerKindHTTP:
		row.Transport = "http"
	case libacp.McpServerKindSSE:
		row.Transport = "sse"
	default:
		row.Transport = "stdio"
	}
	if len(srv.Headers) > 0 {
		row.Headers = make(map[string]string, len(srv.Headers))
		for _, h := range srv.Headers {
			row.Headers[h.Name] = h.Value
		}
	}
	return row
}

func newSessionID(namespace string) string {
	return namespace + "-" + uuid.NewString()
}

func sessionNamespace(t *Transport) string {
	id := t.clientIdentity()
	if id == nil {
		return "acp"
	}
	var sb strings.Builder
	for _, r := range strings.ToLower(id.Name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		}
		if sb.Len() >= 16 {
			break
		}
	}
	if sb.Len() == 0 {
		return "acp"
	}
	return sb.String()
}

func (t *Transport) Close(ctx context.Context) error {
	store := runtimetypes.New(t.deps.DB.WithoutTransaction())

	t.unsubscribeAllTerminals()

	t.sessionMu.Lock()
	entries := make([]*sessionEntry, 0, len(t.sessions))
	sids := make([]libacp.SessionID, 0, len(t.sessions))
	for sid, e := range t.sessions {
		entries = append(entries, e)
		sids = append(sids, sid)
		t.deps.SessionRouter.unbind(e.InternalSessionID, t)
	}
	t.sessions = make(map[libacp.SessionID]*sessionEntry)
	t.contenoxToACPID = make(map[string]libacp.SessionID)
	t.sessionMu.Unlock()

	for _, sid := range sids {
		t.cancelInflightPrompt(sid)
	}

	for _, e := range entries {
		t.cleanupMcpServers(ctx, store, e.McpServerNames)
		_ = e.driver.Close()
	}
	return nil
}

func (t *Transport) sessionFor(id libacp.SessionID) (*sessionEntry, bool) {
	t.sessionMu.Lock()
	defer t.sessionMu.Unlock()
	e, ok := t.sessions[id]
	return e, ok
}

func (t *Transport) resolveSessionWorkspace(ctx context.Context, name string) (string, bool) {
	if name == "" {
		return "", false
	}
	row := t.deps.DB.WithoutTransaction().QueryRowContext(ctx, `
		SELECT mi.workspace_id
		FROM message_indices mi
		WHERE mi.name = $1 AND mi.identity = 'acp-client'
		ORDER BY (SELECT COUNT(*) FROM messages m WHERE m.idx_id = mi.id) DESC, mi.id DESC
		LIMIT 1`, name)
	var workspaceID string
	if err := row.Scan(&workspaceID); err != nil || workspaceID == "" {
		return "", false
	}
	return workspaceID, true
}

var listSessionsPageSize = 100

type sessionListRow struct {
	internalID string
	name       string
	updatedAt  time.Time
	hasTime    bool
}

func sessionListRowLess(a, b sessionListRow) bool {
	if a.hasTime != b.hasTime {
		return a.hasTime
	}
	if a.hasTime && !a.updatedAt.Equal(b.updatedAt) {
		return a.updatedAt.After(b.updatedAt)
	}
	return a.internalID > b.internalID
}

func listSessionsCursor(r sessionListRow) string {
	ts := ""
	if r.hasTime {
		ts = strconv.FormatInt(r.updatedAt.UnixNano(), 10)
	}
	return ts + "|" + r.internalID
}

func listSessionsResume(rows []sessionListRow, cursor string) int {
	tsPart, id, found := strings.Cut(cursor, "|")
	boundary := sessionListRow{internalID: id}
	if !found {
		boundary.internalID = cursor
	} else if tsPart != "" {
		if ns, err := strconv.ParseInt(tsPart, 10, 64); err == nil {
			boundary.updatedAt = time.Unix(0, ns)
			boundary.hasTime = true
		}
	}
	for i, r := range rows {
		if sessionListRowLess(boundary, r) {
			return i
		}
	}
	return len(rows)
}

func (t *Transport) ListSessions(ctx context.Context, req libacp.ListSessionsRequest) (libacp.ListSessionsResponse, error) {
	exec := t.deps.DB.WithoutTransaction()

	rows, err := exec.QueryContext(ctx, `
		SELECT mi.id, mi.name,
		       (SELECT MAX(m.added_at) FROM messages m WHERE m.idx_id = mi.id)
		FROM message_indices mi
		WHERE mi.workspace_id = $1
		  AND mi.identity = 'acp-client'
		  AND mi.name IS NOT NULL AND mi.name != ''`, t.workspaceID())
	if err != nil {
		return libacp.ListSessionsResponse{}, fmt.Errorf("acpsvc: list sessions: %w", err)
	}
	defer rows.Close()

	var scanned []sessionListRow
	for rows.Next() {
		var row sessionListRow
		var updatedAt any
		if err := rows.Scan(&row.internalID, &row.name, &updatedAt); err != nil {
			return libacp.ListSessionsResponse{}, fmt.Errorf("acpsvc: scan session: %w", err)
		}
		row.updatedAt, row.hasTime = parseDBTime(updatedAt)
		scanned = append(scanned, row)
	}
	if err := rows.Err(); err != nil {
		return libacp.ListSessionsResponse{}, fmt.Errorf("acpsvc: rows: %w", err)
	}
	rows.Close()

	store := runtimetypes.New(exec)
	var all []sessionListRow
	for _, row := range scanned {
		if !sessionUnderRequestedCwd(t.sessionCwd(ctx, store, libacp.SessionID(row.name)), req.Cwd) {
			continue
		}
		all = append(all, row)
	}
	sort.Slice(all, func(i, j int) bool { return sessionListRowLess(all[i], all[j]) })

	start := 0
	if req.Cursor != "" {
		start = listSessionsResume(all, req.Cursor)
	}
	end := min(start+listSessionsPageSize, len(all))

	chatMgr := chatservice.NewManager(t.workspaceID())
	var sessions []libacp.SessionInfo
	for _, row := range all[start:end] {
		info := libacp.SessionInfo{
			SessionID: libacp.SessionID(row.name),
			Title:     t.sessionListTitle(ctx, chatMgr, exec, row.internalID, row.name),
			Cwd:       t.sessionCwd(ctx, store, libacp.SessionID(row.name)),
		}
		info.Meta = sessionListMeta(
			t.readSessionAgent(ctx, store, libacp.SessionID(row.name)),
			t.readSessionMission(ctx, store, libacp.SessionID(row.name)),
		)
		if row.hasTime {
			info.UpdatedAt = row.updatedAt.UTC().Format(time.RFC3339)
		}
		sessions = append(sessions, info)
	}

	resp := libacp.ListSessionsResponse{Sessions: sessions}
	if end < len(all) {
		resp.NextCursor = listSessionsCursor(all[end-1])
	}
	return resp, nil
}

func sessionUnderRequestedCwd(storedRoot, requested string) bool {
	requested = strings.TrimSpace(requested)
	if requested == "" || requested == "/" {
		return true
	}
	if storedRoot == "" {
		return false
	}
	resolved, err := vfs.Contain(requested, storedRoot)
	return err == nil && resolved != ""
}

func (t *Transport) sessionListTitle(ctx context.Context, mgr *chatservice.Manager, exec libdb.Exec, internalSessionID, fallback string) string {
	if title := firstUserMessageTitle(ctx, mgr, exec, internalSessionID); title != "" {
		return title
	}
	if title := readProvisionalTitle(ctx, exec, internalSessionID); title != "" {
		return truncateSessionListTitle(title)
	}
	return fallback
}

func firstUserMessageTitle(ctx context.Context, mgr *chatservice.Manager, exec libdb.Exec, internalSessionID string) string {
	msgs, err := mgr.ListMessages(ctx, exec, internalSessionID)
	if err != nil {
		return ""
	}
	for _, m := range msgs {
		if m.Role == "user" && strings.TrimSpace(m.Content) != "" {
			return truncateSessionListTitle(strings.TrimSpace(m.Content))
		}
	}
	return ""
}

func (t *Transport) sessionInfoTitle(ctx context.Context, internalSessionID string) string {
	if t.deps.DB == nil || internalSessionID == "" {
		return ""
	}
	mgr := chatservice.NewManager(t.workspaceID())
	exec := t.deps.DB.WithoutTransaction()
	if title := firstUserMessageTitle(ctx, mgr, exec, internalSessionID); title != "" {
		return title
	}
	return readProvisionalTitle(ctx, exec, internalSessionID)
}

const acpSessionCwdKVPrefix = "acp:session_cwd:"

type sessionCwdRecord struct {
	Cwd string `json:"cwd"`
}

func (t *Transport) persistSessionCwd(ctx context.Context, store runtimetypes.Store, sid libacp.SessionID, cwd string) {
	if cwd == "" {
		return
	}
	raw, err := json.Marshal(sessionCwdRecord{Cwd: cwd})
	if err != nil {
		return
	}
	if err := store.SetKV(ctx, acpSessionCwdKVPrefix+string(sid), raw); err != nil {
		reportErr, _, end := t.tracker().Start(ctx, "persist_cwd", "acp_session", "session_id", string(sid))
		reportErr(err)
		end()
	}
}

func (t *Transport) sessionCwd(ctx context.Context, store runtimetypes.Store, sid libacp.SessionID) string {
	t.sessionMu.Lock()
	entry, ok := t.sessions[sid]
	t.sessionMu.Unlock()
	if ok && entry.Cwd != "" {
		return entry.Cwd
	}
	var rec sessionCwdRecord
	if err := store.GetKV(ctx, acpSessionCwdKVPrefix+string(sid), &rec); err != nil {
		return ""
	}
	return rec.Cwd
}

func parseDBTime(v any) (time.Time, bool) {
	switch tv := v.(type) {
	case nil:
		return time.Time{}, false
	case time.Time:
		return tv, true
	case []byte:
		return parseDBTimeString(string(tv))
	case string:
		return parseDBTimeString(tv)
	}
	return time.Time{}, false
}

func parseDBTimeString(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05.999999999 -0700",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	} {
		if ts, err := time.Parse(layout, s); err == nil {
			return ts, true
		}
	}
	return time.Time{}, false
}
