package taskengine

import (
	"encoding/json"
	"testing"

	libmodelprovider "github.com/contenox/runtime/runtime/modelrepo"
	"github.com/stretchr/testify/assert"
)

func TestUnit_ReconcileToolCallArguments_PrefersTheContinuation(t *testing.T) {
	continuation := &libmodelprovider.Continuation{
		Provider: "openai",
		Model:    "gpt-5",
		Items: json.RawMessage(`{"output":[
			{"type":"reasoning","id":"rs_1"},
			{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read_file","arguments":"{\"path\": \"README.md\"}"},
			{"type":"function_call","id":"fc_2","call_id":"call_2","name":"read_file","arguments":"{\"path\":\"go.mod\"}"}
		]}`),
	}
	calls := []ToolCall{
		{ID: "call_1", Function: FunctionCall{Name: "read_file", Arguments: "{}"}, ArgsMissing: true},
		{ID: "call_2", Function: FunctionCall{Name: "read_file", Arguments: `{"path":"go.mod"}`}},
	}

	repaired := reconcileToolCallArguments(calls, continuation)

	assert.Equal(t, 1, repaired)
	assert.Equal(t, `{"path": "README.md"}`, calls[0].Function.Arguments,
		"the continuation is the model's own record, so it must win over lost arguments")
	assert.False(t, calls[0].ArgsMissing)
	assert.Equal(t, `{"path":"go.mod"}`, calls[1].Function.Arguments,
		"a call that already agrees with the continuation is left alone")
}

func TestUnit_ReconcileToolCallArguments_FallsBackToCallOrder(t *testing.T) {
	continuation := &libmodelprovider.Continuation{
		Provider: "openai",
		Items: json.RawMessage(`{"output":[
			{"type":"function_call","call_id":"call_1","name":"a","arguments":"{\"x\":1}"},
			{"type":"function_call","call_id":"call_2","name":"b","arguments":"{\"y\":2}"}
		]}`),
	}
	calls := []ToolCall{
		{ID: "rewritten-by-the-engine", Function: FunctionCall{Name: "a", Arguments: "{}"}},
		{ID: "call_2", Function: FunctionCall{Name: "b", Arguments: "{}"}},
	}

	repaired := reconcileToolCallArguments(calls, continuation)

	assert.Equal(t, 2, repaired)
	assert.Equal(t, `{"x":1}`, calls[0].Function.Arguments)
	assert.Equal(t, `{"y":2}`, calls[1].Function.Arguments)
}

func TestUnit_ReconcileToolCallArguments_IgnoresForeignContinuations(t *testing.T) {
	calls := []ToolCall{{ID: "call_1", Function: FunctionCall{Name: "a", Arguments: "{}"}}}

	assert.Zero(t, reconcileToolCallArguments(calls, nil))
	assert.Zero(t, reconcileToolCallArguments(calls, &libmodelprovider.Continuation{Items: json.RawMessage(`[]`)}))
	assert.Zero(t, reconcileToolCallArguments(calls, &libmodelprovider.Continuation{Items: json.RawMessage(`{"parts":[1]}`)}))
	assert.Equal(t, "{}", calls[0].Function.Arguments)
}

func TestUnit_ReconcileToolCallArguments_DoesNotInventEmptyArguments(t *testing.T) {
	continuation := &libmodelprovider.Continuation{
		Items: json.RawMessage(`{"output":[{"type":"function_call","call_id":"call_1","name":"a","arguments":""}]}`),
	}
	calls := []ToolCall{{ID: "call_1", Function: FunctionCall{Name: "a", Arguments: "{}"}, ArgsMissing: true}}

	assert.Zero(t, reconcileToolCallArguments(calls, continuation))
	assert.Equal(t, "{}", calls[0].Function.Arguments)
	assert.True(t, calls[0].ArgsMissing)
}

func TestUnit_ReconcileToolCallArguments_DoesNotCrossArgumentsBetweenTools(t *testing.T) {
	continuation := &libmodelprovider.Continuation{
		Items: json.RawMessage(`{"output":[
			{"type":"function_call","call_id":"call_1","name":"write_file","arguments":"{\"path\":\"a\"}"},
			{"type":"function_call","call_id":"call_2","name":"read_file","arguments":"{\"path\":\"b\"}"}
		]}`),
	}
	calls := []ToolCall{
		{ID: "rewritten-1", Function: FunctionCall{Name: "read_file", Arguments: "{}"}},
		{ID: "rewritten-2", Function: FunctionCall{Name: "write_file", Arguments: "{}"}},
	}

	repaired := reconcileToolCallArguments(calls, continuation)

	assert.Equal(t, 2, repaired)
	assert.Equal(t, `{"path":"b"}`, calls[0].Function.Arguments, "read_file must get the read_file call's arguments")
	assert.Equal(t, `{"path":"a"}`, calls[1].Function.Arguments, "write_file must get the write_file call's arguments")
}

func TestUnit_ReconcileToolCallArguments_UsesEachContinuationEntryOnce(t *testing.T) {
	continuation := &libmodelprovider.Continuation{
		Items: json.RawMessage(`{"output":[
			{"type":"function_call","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"a\"}"}
		]}`),
	}
	calls := []ToolCall{
		{ID: "rewritten-1", Function: FunctionCall{Name: "read_file", Arguments: "{}"}},
		{ID: "rewritten-2", Function: FunctionCall{Name: "read_file", Arguments: "{}"}},
	}

	repaired := reconcileToolCallArguments(calls, continuation)

	assert.Equal(t, 1, repaired)
	assert.Equal(t, `{"path":"a"}`, calls[0].Function.Arguments)
	assert.Equal(t, "{}", calls[1].Function.Arguments, "one continuation entry cannot answer two calls")
}

func TestUnit_ReconcileToolCallArguments_DoesNotMatchADifferentTool(t *testing.T) {
	continuation := &libmodelprovider.Continuation{
		Items: json.RawMessage(`{"output":[
			{"type":"function_call","call_id":"call_1","name":"write_file","arguments":"{\"path\":\"a\"}"}
		]}`),
	}
	calls := []ToolCall{{ID: "rewritten", Function: FunctionCall{Name: "read_file", Arguments: "{}"}}}

	assert.Zero(t, reconcileToolCallArguments(calls, continuation))
	assert.Equal(t, "{}", calls[0].Function.Arguments)
}
