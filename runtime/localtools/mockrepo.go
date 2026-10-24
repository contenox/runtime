package localtools

import (
	"context"
	"time"

	"github.com/contenox/runtime/runtime/taskengine"
)

type MockToolsRepo struct {
	Calls           []ToolsCallRecord
	ResponseMap     map[string]ToolsResponse
	DefaultResponse ToolsResponse
	ErrorSequence   []error
	callCount       int
}

type ToolsCallRecord struct {
	Args       taskengine.ToolsCall
	Input      any
	InputType  taskengine.DataType
	Transition string
}

type ToolsResponse struct {
	Status     int
	Output     any
	OutputType taskengine.DataType
	Transition string
}

func NewMockToolsRegistry() *MockToolsRepo {
	return &MockToolsRepo{
		ResponseMap: make(map[string]ToolsResponse),
		DefaultResponse: ToolsResponse{
			Output:     "default mock response",
			OutputType: taskengine.DataTypeString,
			Transition: "",
		},
	}
}

func (m *MockToolsRepo) Exec(
	ctx context.Context,
	startingTime time.Time,
	input any,
	inputType taskengine.DataType,
	transition string,
	args *taskengine.ToolsCall,
) (int, any, taskengine.DataType, string, error) {
	m.callCount++

	call := ToolsCallRecord{
		Args:       *args,
		Input:      input,
		InputType:  inputType,
		Transition: transition,
	}
	m.Calls = append(m.Calls, call)

	var err error
	if len(m.ErrorSequence) > 0 {
		err = m.ErrorSequence[0]
		if len(m.ErrorSequence) > 1 {
			m.ErrorSequence = m.ErrorSequence[1:]
		} else {
			m.ErrorSequence = nil
		}
	}

	var resp ToolsResponse
	if specificResp, ok := m.ResponseMap[args.Name]; ok {
		resp = specificResp
	} else {
		resp = m.DefaultResponse
	}

	return resp.Status, resp.Output, resp.OutputType, resp.Transition, err
}

func (m *MockToolsRepo) Reset() {
	m.Calls = nil
	m.callCount = 0
	m.ErrorSequence = nil
}

func (m *MockToolsRepo) CallCount() int {
	return m.callCount
}

func (m *MockToolsRepo) LastCall() *ToolsCallRecord {
	if len(m.Calls) == 0 {
		return nil
	}
	return &m.Calls[len(m.Calls)-1]
}

func (m *MockToolsRepo) WithResponse(toolsType string, response ToolsResponse) *MockToolsRepo {
	m.ResponseMap[toolsType] = response
	return m
}

func (m *MockToolsRepo) WithErrorSequence(errors ...error) *MockToolsRepo {
	m.ErrorSequence = errors
	return m
}

func (m *MockToolsRepo) Supports(ctx context.Context) ([]string, error) {
	supported := make([]string, 0, len(m.ResponseMap))
	for k := range m.ResponseMap {
		supported = append(supported, k)
	}
	return supported, nil
}

var _ taskengine.ToolsRegistry = (*MockToolsRepo)(nil)
