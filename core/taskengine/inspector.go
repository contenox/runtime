package taskengine

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/contenox/activitytracker"
	"github.com/contenox/runtime-mvp/libs/libkv"
)

type Inspector interface {
	Start(ctx context.Context) StackTrace
}

type StackTrace interface {
	// Observation
	RecordStep(step CapturedStateUnit)
	GetExecutionHistory() []CapturedStateUnit

	// // Control
	SetBreakpoint(taskID string)
	ClearBreakpoints()

	HasBreakpoint(taskID string) bool

	// Debugging
	GetCurrentState() ExecutionState
}

type ExecutionState struct {
	Variables   map[string]any
	DataTypes   map[string]DataType
	CurrentTask *ChainTask
}

type CapturedStateUnit struct {
	TaskID     string        `json:"taskID"`
	TaskType   string        `json:"taskType"`
	InputType  DataType      `json:"inputType"`
	OutputType DataType      `json:"outputType"`
	Transition string        `json:"transition"`
	Duration   time.Duration `json:"duration"`
	Error      ErrorResponse `json:"error"`
	Input      string        `json:"Input"`
	Output     string        `json:"Output"`
}

type ErrorResponse struct {
	ErrorInternal error  `json:"-"`
	Error         string `json:"error"`
}

func NewSimpleInspector(kvManager libkv.KVManager) *SimpleInspector {
	return &SimpleInspector{
		kvManager: kvManager,
	}
}

type SimpleInspector struct {
	kvManager libkv.KVManager
}

func (m SimpleInspector) Start(ctx context.Context) StackTrace {
	// Extract requestID from context
	reqID, ok := ctx.Value(activitytracker.ContextKeyRequestID).(string)
	if !ok {
		log.Printf("SERVERBUG: Missing requestID in context during Start")
		// Proceed to return the StackTrace even without a requestID
	}

	// Store requestID in KV set if kvManager exists and requestID is valid
	if m.kvManager != nil && ok {
		kvOp, err := m.kvManager.Executor(ctx)
		if err != nil {
			log.Printf("SERVERBUG: Failed to get KV operation during Start: %v", err)
		} else {
			setStateKey := "state:requests" // Set key for request IDs with state
			reqIDBytes := []byte(reqID)

			// Add requestID to the set
			if err := kvOp.SetAdd(ctx, setStateKey, reqIDBytes); err != nil {
				log.Printf("SERVERBUG: Failed to add requestID to state set: %v", err)
			}
		}
	}

	// Return the new StackTrace instance
	return &SimpleStackTrace{
		history:     make([]CapturedStateUnit, 0),
		breakpoints: make(map[string]bool),
		ctx:         ctx,
		kvManager:   m.kvManager,
	}
}

type SimpleStackTrace struct {
	history     []CapturedStateUnit
	breakpoints map[string]bool
	vars        map[string]interface{}
	dataTypes   map[string]DataType
	currentTask *ChainTask
	ctx         context.Context
	kvManager   libkv.KVManager
}

func (s *SimpleStackTrace) RecordStep(step CapturedStateUnit) {
	if s.kvManager != nil {
		// Extract request ID from context
		reqID, ok := s.ctx.Value(activitytracker.ContextKeyRequestID).(string)
		if !ok {
			log.Printf("SERVERBUG: Missing requestID in context")
			return
		}

		// Define key with prefix and requestID
		key := "state:" + reqID

		// Marshal the step to JSON
		data, err := json.Marshal(step)
		if err != nil {
			log.Printf("SERVERBUG: Failed to marshal CapturedStateUnit: %v", err)
			return
		}

		// Get KV operation handle
		opCtx, timeout := context.WithTimeout(context.Background(), time.Second*10)
		defer timeout()
		kvOp, err := s.kvManager.Executor(opCtx)
		if err != nil {
			log.Printf("SERVERBUG: Failed to get KV operation: %v", err)
			return
		}

		// Push step to KV list
		if err := kvOp.ListPush(opCtx, key, data); err != nil {
			log.Printf("SERVERBUG: Failed to store step in KV: %v", err)
			return
		}

		listLen, err := kvOp.ListLength(opCtx, key)
		if err != nil {
			log.Printf("SERVERBUG: Failed to get list length: %v", err)
		} else if listLen > 1000 {
			if err := kvOp.ListTrim(opCtx, key, 0, 999); err != nil {
				log.Printf("SERVERBUG: Failed to trim KV list: %v", err)
			}
		}
	}

	// Append to in-memory history (for local debugging)
	s.history = append(s.history, step)
}

func (s *SimpleStackTrace) GetExecutionHistory() []CapturedStateUnit {
	return s.history
}

func (s *SimpleStackTrace) SetBreakpoint(taskID string) {
	s.breakpoints[taskID] = true
}

func (s *SimpleStackTrace) ClearBreakpoints() {
	s.breakpoints = make(map[string]bool)
}

func (s *SimpleStackTrace) HasBreakpoint(taskID string) bool {
	return s.breakpoints[taskID]
}

func (s *SimpleStackTrace) GetCurrentState() ExecutionState {
	return ExecutionState{
		Variables:   s.vars,
		DataTypes:   s.dataTypes,
		CurrentTask: s.currentTask,
	}
}
