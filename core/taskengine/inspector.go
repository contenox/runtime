package taskengine

import "time"

type Inspector interface {
	Start() StackTrace
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
}

type ErrorResponse struct {
	ErrorInternal error  `json:"-"`
	Error         string `json:"error"`
}

type MockInspector struct{}

func (m MockInspector) Start() StackTrace {
	return &MockStackTrace{
		history:     make([]CapturedStateUnit, 0),
		breakpoints: make(map[string]bool),
	}
}

type MockStackTrace struct {
	history     []CapturedStateUnit
	breakpoints map[string]bool
	vars        map[string]interface{}
	dataTypes   map[string]DataType
	currentTask *ChainTask
}

func (s *MockStackTrace) RecordStep(step CapturedStateUnit) {
	s.history = append(s.history, step)
}

func (s *MockStackTrace) GetExecutionHistory() []CapturedStateUnit {
	return s.history
}

func (s *MockStackTrace) SetBreakpoint(taskID string) {
	s.breakpoints[taskID] = true
}

func (s *MockStackTrace) ClearBreakpoints() {
	s.breakpoints = make(map[string]bool)
}

func (s *MockStackTrace) HasBreakpoint(taskID string) bool {
	return s.breakpoints[taskID]
}

func (s *MockStackTrace) GetCurrentState() ExecutionState {
	return ExecutionState{
		Variables:   s.vars,
		DataTypes:   s.dataTypes,
		CurrentTask: s.currentTask,
	}
}
