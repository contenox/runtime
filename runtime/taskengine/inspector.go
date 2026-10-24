package taskengine

import (
	"context"
	"time"
)

type Inspector interface {
	Start(ctx context.Context) StackTrace
}

type StackTrace interface {
	RecordStep(step CapturedStateUnit)
	GetExecutionHistory() []CapturedStateUnit
}

type CapturedStateUnit struct {
	Scope       EventScope    `json:"scope,omitzero"`
	TaskID      string        `json:"taskID" example:"validate_input"`
	TaskHandler string        `json:"taskHandler" example:"chat_completion"`
	InputType   DataType      `json:"inputType" example:"string"`
	OutputType  DataType      `json:"outputType" example:"string"`
	Transition  string        `json:"transition" example:"valid_input"`
	Duration    time.Duration `json:"duration" example:"452000000"`
	Error       ErrorResponse `json:"error"`
	Input       any           `json:"input,omitempty"`
	Output      any           `json:"output,omitempty"`
	InputVar    string        `json:"inputVar" example:"input"`

	RetryIndex   int         `json:"retryIndex"`
	Cancelled    bool        `json:"cancelled,omitempty"`
	TimedOut     bool        `json:"timedOut,omitempty"`
	ProviderType string      `json:"providerType,omitempty"`
	ModelName    string      `json:"modelName,omitempty"`
	ToolNames    []string    `json:"toolNames,omitempty"`
	TokenUsage   *TokenUsage `json:"tokenUsage,omitempty"`
	FinishReason string      `json:"finishReason,omitempty"`
}

type TokenUsage struct {
	Prompt     int `json:"prompt"`
	Completion int `json:"completion"`
	Thinking   int `json:"thinking,omitempty"`
	Total      int `json:"total"`
	CacheRead  int `json:"cacheRead,omitempty"`
	CacheWrite int `json:"cacheWrite,omitempty"`
}

type ErrorResponse struct {
	ErrorInternal error  `json:"-"`
	Error         string `json:"error" example:"validation failed: input contains prohibited content"`
}

func NewSimpleInspector() Inspector {
	return &simpleInspector{}
}

type simpleInspector struct{}

func (simpleInspector) Start(ctx context.Context) StackTrace {
	return &SimpleStackTrace{
		history: make([]CapturedStateUnit, 0),
		ctx:     ctx,
	}
}

type SimpleStackTrace struct {
	history []CapturedStateUnit
	ctx     context.Context
}

func (s *SimpleStackTrace) RecordStep(step CapturedStateUnit) {
	s.history = append(s.history, step)
}

func (s *SimpleStackTrace) GetExecutionHistory() []CapturedStateUnit {
	return s.history
}
