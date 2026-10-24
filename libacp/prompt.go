package libacp

import "encoding/json"

type StopReason string

const (
	StopReasonEndTurn         StopReason = "end_turn"
	StopReasonMaxTokens       StopReason = "max_tokens"
	StopReasonMaxTurnRequests StopReason = "max_turn_requests"
	StopReasonRefusal         StopReason = "refusal"
	StopReasonCancelled       StopReason = "cancelled"
)

type PromptRequest struct {
	SessionID SessionID       `json:"sessionId"`
	Prompt    []ContentBlock  `json:"prompt"`
	Meta      json.RawMessage `json:"_meta,omitempty"`
}

type PromptResponse struct {
	StopReason StopReason      `json:"stopReason"`
	Meta       json.RawMessage `json:"_meta,omitempty"`
}

type SessionUpdateKind string

const (
	SessionUpdateUserMessageChunk  SessionUpdateKind = "user_message_chunk"
	SessionUpdateAgentMessageChunk SessionUpdateKind = "agent_message_chunk"
	SessionUpdateAgentThoughtChunk SessionUpdateKind = "agent_thought_chunk"
	SessionUpdateToolCall          SessionUpdateKind = "tool_call"
	SessionUpdateToolCallUpdate    SessionUpdateKind = "tool_call_update"
	SessionUpdatePlan              SessionUpdateKind = "plan"
	SessionUpdateAvailableCommands SessionUpdateKind = "available_commands_update"
	SessionUpdateCurrentMode       SessionUpdateKind = "current_mode_update"
	SessionUpdateConfigOption      SessionUpdateKind = "config_option_update"
	SessionUpdateUsageUpdate       SessionUpdateKind = "usage_update"
	SessionUpdateUsageStats        SessionUpdateKind = "usage_stats"
	SessionUpdateSessionInfo       SessionUpdateKind = "session_info_update"
)

func AllSessionUpdateKinds() []SessionUpdateKind {
	return []SessionUpdateKind{
		SessionUpdateUserMessageChunk,
		SessionUpdateAgentMessageChunk,
		SessionUpdateAgentThoughtChunk,
		SessionUpdateToolCall,
		SessionUpdateToolCallUpdate,
		SessionUpdatePlan,
		SessionUpdateAvailableCommands,
		SessionUpdateCurrentMode,
		SessionUpdateConfigOption,
		SessionUpdateUsageUpdate,
		SessionUpdateUsageStats,
		SessionUpdateSessionInfo,
	}
}

type SessionUpdate struct {
	SessionUpdate SessionUpdateKind `json:"sessionUpdate"`

	Content *ContentBlock `json:"-"`

	ToolCallID  string             `json:"toolCallId,omitempty"`
	Title       string             `json:"title,omitempty"`
	Kind        ToolKind           `json:"kind,omitempty"`
	Status      ToolCallStatus     `json:"status,omitempty"`
	ToolContent []ToolCallContent  `json:"-"`
	Locations   []ToolCallLocation `json:"locations,omitempty"`
	RawInput    json.RawMessage    `json:"rawInput,omitempty"`
	RawOutput   json.RawMessage    `json:"rawOutput,omitempty"`

	Entries []PlanEntry `json:"entries,omitempty"`

	AvailableCommands []AvailableCommand `json:"availableCommands,omitempty"`

	CurrentModeID string `json:"currentModeId,omitempty"`

	ConfigOptions []SessionConfigOption `json:"configOptions,omitempty"`

	Used int        `json:"used,omitempty"`
	Size int        `json:"size,omitempty"`
	Cost *UsageCost `json:"cost,omitempty"`

	Stats *SessionStats `json:"stats,omitempty"`

	MessageID string `json:"messageId,omitempty"`

	UpdatedAt string `json:"updatedAt,omitempty"`

	Meta json.RawMessage `json:"_meta,omitempty"`
}

type sessionUpdateWire struct {
	SessionUpdate SessionUpdateKind `json:"sessionUpdate"`

	Content json.RawMessage `json:"content,omitempty"`

	ToolCallID string             `json:"toolCallId,omitempty"`
	Title      string             `json:"title,omitempty"`
	Kind       ToolKind           `json:"kind,omitempty"`
	Status     ToolCallStatus     `json:"status,omitempty"`
	Locations  []ToolCallLocation `json:"locations,omitempty"`
	RawInput   json.RawMessage    `json:"rawInput,omitempty"`
	RawOutput  json.RawMessage    `json:"rawOutput,omitempty"`

	Entries []PlanEntry `json:"entries,omitempty"`

	AvailableCommands *[]AvailableCommand `json:"availableCommands,omitempty"`

	CurrentModeID string `json:"currentModeId,omitempty"`

	ConfigOptions *[]SessionConfigOption `json:"configOptions,omitempty"`

	Used *int       `json:"used,omitempty"`
	Size *int       `json:"size,omitempty"`
	Cost *UsageCost `json:"cost,omitempty"`

	Stats *SessionStats `json:"stats,omitempty"`

	MessageID string `json:"messageId,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`

	Meta json.RawMessage `json:"_meta,omitempty"`
}

type UsageCost struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

type SessionModelStats struct {
	Model    string `json:"model"`
	Provider string `json:"provider,omitempty"`
	Calls    int64  `json:"calls"`

	InputTokens      int64 `json:"inputTokens"`
	OutputTokens     int64 `json:"outputTokens"`
	ThinkingTokens   int64 `json:"thinkingTokens"`
	CacheReadTokens  int64 `json:"cacheReadTokens"`
	CacheWriteTokens int64 `json:"cacheWriteTokens"`
}

type SessionStats struct {
	SessionID string `json:"sessionId"`

	Turns      int64 `json:"turns"`
	Steps      int64 `json:"steps"`
	ModelCalls int64 `json:"modelCalls"`
	LlmMs      int64 `json:"llmMs"`
	ToolMs     int64 `json:"toolMs"`

	TtftSamples int64 `json:"ttftSamples"`
	TtftMs      int64 `json:"ttftMs"`
	StreamMs    int64 `json:"streamMs"`

	InputTokens      int64 `json:"inputTokens"`
	OutputTokens     int64 `json:"outputTokens"`
	ThinkingTokens   int64 `json:"thinkingTokens"`
	CacheReadTokens  int64 `json:"cacheReadTokens"`
	CacheWriteTokens int64 `json:"cacheWriteTokens"`

	ByModel []SessionModelStats `json:"byModel,omitempty"`
}

func (u SessionUpdate) MarshalJSON() ([]byte, error) {
	w := sessionUpdateWire{
		SessionUpdate: u.SessionUpdate,
		ToolCallID:    u.ToolCallID,
		Title:         u.Title,
		Kind:          u.Kind,
		Status:        u.Status,
		Locations:     u.Locations,
		RawInput:      u.RawInput,
		RawOutput:     u.RawOutput,
		Entries:       u.Entries,
		CurrentModeID: u.CurrentModeID,
		Cost:          u.Cost,
		Stats:         u.Stats,
		MessageID:     u.MessageID,
		UpdatedAt:     u.UpdatedAt,
		Meta:          u.Meta,
	}
	if u.SessionUpdate == SessionUpdateUsageUpdate {
		used, size := u.Used, u.Size
		w.Used, w.Size = &used, &size
	}
	if u.SessionUpdate == SessionUpdateAvailableCommands {
		commands := u.AvailableCommands
		if commands == nil {
			commands = []AvailableCommand{}
		}
		w.AvailableCommands = &commands
	}
	if u.SessionUpdate == SessionUpdateConfigOption {
		options := u.ConfigOptions
		if options == nil {
			options = []SessionConfigOption{}
		}
		w.ConfigOptions = &options
	}
	switch u.SessionUpdate {
	case SessionUpdateToolCall, SessionUpdateToolCallUpdate:
		if len(u.ToolContent) > 0 {
			raw, err := json.Marshal(u.ToolContent)
			if err != nil {
				return nil, err
			}
			w.Content = raw
		}
	default:
		if u.Content != nil {
			raw, err := json.Marshal(u.Content)
			if err != nil {
				return nil, err
			}
			w.Content = raw
		}
	}
	return json.Marshal(w)
}

func (u *SessionUpdate) UnmarshalJSON(data []byte) error {
	var w sessionUpdateWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	*u = SessionUpdate{
		SessionUpdate: w.SessionUpdate,
		ToolCallID:    w.ToolCallID,
		Title:         w.Title,
		Kind:          w.Kind,
		Status:        w.Status,
		Locations:     w.Locations,
		RawInput:      w.RawInput,
		RawOutput:     w.RawOutput,
		Entries:       w.Entries,
		CurrentModeID: w.CurrentModeID,
		Cost:          w.Cost,
		Stats:         w.Stats,
		MessageID:     w.MessageID,
		UpdatedAt:     w.UpdatedAt,
		Meta:          w.Meta,
	}
	if w.Used != nil {
		u.Used = *w.Used
	}
	if w.Size != nil {
		u.Size = *w.Size
	}
	if w.AvailableCommands != nil {
		u.AvailableCommands = *w.AvailableCommands
	}
	if w.ConfigOptions != nil {
		u.ConfigOptions = *w.ConfigOptions
	}
	if len(w.Content) == 0 {
		return nil
	}
	switch w.SessionUpdate {
	case SessionUpdateToolCall, SessionUpdateToolCallUpdate:
		return json.Unmarshal(w.Content, &u.ToolContent)
	default:
		var cb ContentBlock
		if err := json.Unmarshal(w.Content, &cb); err != nil {
			return err
		}
		u.Content = &cb
	}
	return nil
}

type SessionNotification struct {
	SessionID SessionID       `json:"sessionId"`
	Update    SessionUpdate   `json:"update"`
	Meta      json.RawMessage `json:"_meta,omitempty"`
}

const EventSeqMetaKey = "contenox.seq"

func (n SessionNotification) WithSeq(seq uint64) SessionNotification {
	meta := make(map[string]any)
	if len(n.Meta) > 0 {
		_ = json.Unmarshal(n.Meta, &meta)
	}
	if meta == nil {
		meta = make(map[string]any)
	}
	meta[EventSeqMetaKey] = seq
	encoded, _ := json.Marshal(meta)
	n.Meta = encoded
	return n
}

func NewAgentMessageChunk(text string) SessionUpdate {
	c := NewTextContent(text)
	return SessionUpdate{SessionUpdate: SessionUpdateAgentMessageChunk, Content: &c}
}

func NewAgentThoughtChunk(text string) SessionUpdate {
	c := NewTextContent(text)
	return SessionUpdate{SessionUpdate: SessionUpdateAgentThoughtChunk, Content: &c}
}

func NewUserMessageChunk(text string) SessionUpdate {
	c := NewTextContent(text)
	return SessionUpdate{SessionUpdate: SessionUpdateUserMessageChunk, Content: &c}
}
