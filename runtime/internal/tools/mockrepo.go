package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/getkin/kin-openapi/openapi3"
)

type MockToolsRepo struct {
	Calls           []ToolsCallRecord
	ResponseMap     map[string]ToolsResponse
	DefaultResponse ToolsResponse
	ErrorSequence   []error
	callCount       int
}

type ToolsCallRecord struct {
	Args  taskengine.ToolsCall
	Input any
}

type ToolsResponse struct {
	Output any
	Schema *openapi3.T
}

func NewMockToolsRegistry() *MockToolsRepo {
	return &MockToolsRepo{
		ResponseMap: make(map[string]ToolsResponse),
		DefaultResponse: ToolsResponse{
			Output: "default mock response",
			Schema: nil,
		},
	}
}

func (m *MockToolsRepo) Exec(
	ctx context.Context,
	startingTime time.Time,
	input any,
	debug bool,
	args *taskengine.ToolsCall,
) (any, taskengine.DataType, error) {
	m.callCount++

	call := ToolsCallRecord{
		Args:  *args,
		Input: input,
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

	return resp.Output, taskengine.DataTypeAny, err
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

func (m *MockToolsRepo) GetSchemasForSupportedTools(ctx context.Context) (map[string]*openapi3.T, error) {
	schemas := make(map[string]*openapi3.T)
	for toolsType, response := range m.ResponseMap {
		schemas[toolsType] = response.Schema
	}
	return schemas, nil
}

func (m *MockToolsRepo) GetToolsForToolsByName(ctx context.Context, name string) ([]taskengine.Tool, error) {
	if _, ok := m.ResponseMap[name]; ok {
		return nil, fmt.Errorf("not implemented")
	}
	return nil, fmt.Errorf("%w: %q", taskengine.ErrToolsNotFound, name)
}

var _ taskengine.ToolsRepo = (*MockToolsRepo)(nil)
