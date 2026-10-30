package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnit_OpenAIStreamClient_StreamsThinkingDeltas(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/chat/completions", r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-test\",\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"think-1\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	client := &OpenAIStreamClient{
		openAIClient: openAIClient{
			baseURL:    srv.URL,
			apiKey:     "test-key",
			httpClient: srv.Client(),
			modelName:  "gpt-test",
			tracker:    libtracker.NoopTracker{},
		},
	}

	stream, err := client.Stream(context.Background(), []modelrepo.Message{{Role: "user", Content: "hello"}})
	require.NoError(t, err)

	asm := modelrepo.NewStreamAssembler("openai", "gpt-test")
	for parcel := range stream {
		require.NoError(t, asm.Consume(parcel))
	}
	res, err := asm.Result()
	require.NoError(t, err)
	assert.Equal(t, "think-1", res.Thinking)
	assert.Equal(t, "hello", res.Content)
}

func TestUnit_OpenAIStreamClient_GoldenFixture_ToolCallFragments(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/chat/completions", r.URL.Path)
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, true, body["stream"])
		w.Header().Set("Content-Type", "text/event-stream")
		lines := []string{
			`{"choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"hmm"}}]}`,
			`{"choices":[{"index":0,"delta":{"content":"let me "}}]}`,
			`{"choices":[{"index":0,"delta":{"content":"check"}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"get_weather","arguments":"{\"ci"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ty\":\"Berlin\"}"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"get_time","arguments":"{\"tz\":"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"\"CET\"}"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":40,"completion_tokens":22,"total_tokens":62}}`,
		}
		for _, l := range lines {
			fmt.Fprintf(w, "data: %s\n\n", l)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	client := &OpenAIStreamClient{
		openAIClient: openAIClient{
			baseURL:    srv.URL,
			apiKey:     "test-key",
			httpClient: srv.Client(),
			modelName:  "gpt-test",
			tracker:    libtracker.NoopTracker{},
		},
	}

	stream, err := client.Stream(context.Background(), []modelrepo.Message{{Role: "user", Content: "weather?"}})
	require.NoError(t, err)

	asm := modelrepo.NewStreamAssembler("openai", "gpt-test")
	for parcel := range stream {
		require.NoError(t, asm.Consume(parcel))
	}
	res, err := asm.Result()
	require.NoError(t, err)

	assert.Equal(t, "let me check", res.Content)
	assert.Equal(t, "hmm", res.Thinking)
	require.Len(t, res.ToolCalls, 2)
	assert.Equal(t, "call_a", res.ToolCalls[0].ID)
	assert.Equal(t, "get_weather", res.ToolCalls[0].Function.Name)
	assert.Equal(t, `{"city":"Berlin"}`, res.ToolCalls[0].Function.Arguments)
	assert.Equal(t, "call_b", res.ToolCalls[1].ID)
	assert.Equal(t, "get_time", res.ToolCalls[1].Function.Name)
	assert.Equal(t, `{"tz":"CET"}`, res.ToolCalls[1].Function.Arguments)
	assert.Equal(t, "tool_calls", res.FinishReason)
	require.NotNil(t, res.Usage)
	assert.Equal(t, 40, res.Usage.PromptTokens)
	assert.Equal(t, 22, res.Usage.CompletionTokens)
	assert.Equal(t, 62, res.Usage.TotalTokens)
}

func TestUnit_OpenAIStreamClient_ResponsesGoldenFixture_FunctionCallDeltas(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/responses", r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		lines := []string{
			`{"type":"response.reasoning_summary_text.delta","delta":"thinking hard"}`,
			`{"type":"response.output_text.delta","delta":"calling "}`,
			`{"type":"response.output_text.delta","delta":"a tool"}`,
			`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_r1","name":"get_weather","arguments":""}}`,
			`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"city\":"}`,
			`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"\"Berlin\"}"}`,
			`{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_r1","name":"get_weather","arguments":"{\"city\":\"Berlin\"}"}}`,
			`{"type":"response.completed","response":{"output":[],"reasoning":{"summary":""},"usage":{"input_tokens":30,"output_tokens":11,"total_tokens":41}}}`,
		}
		for _, l := range lines {
			fmt.Fprintf(w, "data: %s\n\n", l)
		}
	}))
	defer srv.Close()

	client := &OpenAIStreamClient{
		openAIClient: openAIClient{
			baseURL:    srv.URL,
			apiKey:     "key",
			httpClient: srv.Client(),
			modelName:  "gpt-5",
			tracker:    libtracker.NoopTracker{},
		},
	}

	stream, err := client.Stream(context.Background(), []modelrepo.Message{{Role: "user", Content: "weather?"}})
	require.NoError(t, err)

	asm := modelrepo.NewStreamAssembler("openai", "gpt-5")
	for parcel := range stream {
		require.NoError(t, asm.Consume(parcel))
	}
	res, err := asm.Result()
	require.NoError(t, err)

	assert.Equal(t, "calling a tool", res.Content)
	assert.Equal(t, "thinking hard", res.Thinking)
	require.Len(t, res.ToolCalls, 1)
	assert.Equal(t, "call_r1", res.ToolCalls[0].ID)
	assert.Equal(t, "get_weather", res.ToolCalls[0].Function.Name)
	assert.Equal(t, `{"city":"Berlin"}`, res.ToolCalls[0].Function.Arguments)
	assert.Equal(t, "stop", res.FinishReason)
	require.NotNil(t, res.Usage)
	assert.Equal(t, 30, res.Usage.PromptTokens)
	assert.Equal(t, 11, res.Usage.CompletionTokens)
}

func TestUnit_OpenAIStreamClient_ErrorEventSurfaces(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"response.output_text.delta","delta":"par"}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"error","code":"server_error","message":"boom"}`+"\n\n")
	}))
	defer srv.Close()

	client := &OpenAIStreamClient{
		openAIClient: openAIClient{
			baseURL:    srv.URL,
			apiKey:     "key",
			httpClient: srv.Client(),
			modelName:  "gpt-5",
			tracker:    libtracker.NoopTracker{},
		},
	}

	stream, err := client.Stream(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)

	asm := modelrepo.NewStreamAssembler("openai", "gpt-5")
	var sawErr bool
	for parcel := range stream {
		if asm.Consume(parcel) != nil {
			sawErr = true
		}
	}
	require.True(t, sawErr, "the SSE error event must surface as an Error parcel")
	_, err = asm.Result()
	require.ErrorContains(t, err, "boom")
}

func TestUnit_OpenAIStreamClient_ResponsesAPIStreamsTextDeltas(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/responses", r.URL.Path)
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, true, body["stream"], "Responses API must be called with stream:true")
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.output_text.delta\n")
		fmt.Fprint(w, `data: {"type":"response.output_text.delta","delta":"Hello"}`+"\n\n")
		fmt.Fprint(w, "event: response.output_text.delta\n")
		fmt.Fprint(w, `data: {"type":"response.output_text.delta","delta":", world"}`+"\n\n")
		fmt.Fprint(w, "event: response.completed\n")
		fmt.Fprint(w, `data: {"type":"response.completed","response":{"output":[],"reasoning":{"summary":""}}}`+"\n\n")
	}))
	defer srv.Close()

	client := &OpenAIStreamClient{
		openAIClient: openAIClient{
			baseURL:    srv.URL,
			apiKey:     "key",
			httpClient: srv.Client(),
			modelName:  "gpt-5",
			tracker:    libtracker.NoopTracker{},
		},
	}

	stream, err := client.Stream(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)

	var got []string
	for p := range stream {
		require.NoError(t, p.Error)
		if p.Data != "" {
			got = append(got, p.Data)
		}
	}
	require.Equal(t, []string{"Hello", ", world"}, got)
}

func TestUnit_OpenAIStreamClient_ResponsesAPIEmitsReasoningSummary(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.output_text.delta\n")
		fmt.Fprint(w, `data: {"type":"response.output_text.delta","delta":"ans"}`+"\n\n")
		fmt.Fprint(w, "event: response.completed\n")
		fmt.Fprint(w, `data: {"type":"response.completed","response":{"output":[{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"I reasoned this"}]}],"reasoning":{"effort":"medium","summary":"auto"}}}`+"\n\n")
	}))
	defer srv.Close()

	client := &OpenAIStreamClient{
		openAIClient: openAIClient{
			baseURL:    srv.URL,
			apiKey:     "key",
			httpClient: srv.Client(),
			modelName:  "gpt-5",
			tracker:    libtracker.NoopTracker{},
		},
	}

	stream, err := client.Stream(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)

	var data, thinking string
	for p := range stream {
		require.NoError(t, p.Error)
		data += p.Data
		thinking += p.Thinking
	}
	require.Equal(t, "ans", data)
	require.Equal(t, "I reasoned this", thinking)
}

type responsesTestCall struct {
	index int
	id    string
	name  string
	args  string
}

func responsesTestCalls(n int) []responsesTestCall {
	calls := make([]responsesTestCall, 0, n)
	for i := 1; i <= n; i++ {
		calls = append(calls, responsesTestCall{
			index: i,
			id:    fmt.Sprintf("call_%d", i),
			name:  fmt.Sprintf("tool_%d", i),
			args:  fmt.Sprintf(`{"path": "dir-%d", "recursive": true}`, i),
		})
	}
	return calls
}

func responsesAddedEvents(calls []responsesTestCall) []string {
	events := make([]string, 0, len(calls))
	for _, c := range calls {
		events = append(events, fmt.Sprintf(
			`{"type":"response.output_item.added","output_index":%d,"item":{"type":"function_call","id":"fc_%d","call_id":%q,"name":%q,"arguments":""}}`,
			c.index, c.index, c.id, c.name))
	}
	return events
}

func responsesArgumentsDoneEvents(calls []responsesTestCall) []string {
	events := make([]string, 0, len(calls))
	for _, c := range calls {
		events = append(events, fmt.Sprintf(
			`{"type":"response.function_call_arguments.done","item_id":"fc_%d","output_index":%d,"arguments":%q}`,
			c.index, c.index, c.args))
	}
	return events
}

func responsesItemDoneEvents(calls []responsesTestCall) []string {
	events := make([]string, 0, len(calls))
	for _, c := range calls {
		events = append(events, fmt.Sprintf(
			`{"type":"response.output_item.done","output_index":%d,"item":{"type":"function_call","id":"fc_%d","call_id":%q,"name":%q,"arguments":%q}}`,
			c.index, c.index, c.id, c.name, c.args))
	}
	return events
}

func responsesCompletedEvent(calls []responsesTestCall) string {
	output := make([]map[string]any, 0, len(calls))
	for _, c := range calls {
		output = append(output, map[string]any{
			"type":      "function_call",
			"id":        fmt.Sprintf("fc_%d", c.index),
			"call_id":   c.id,
			"name":      c.name,
			"arguments": c.args,
		})
	}
	payload, err := json.Marshal(map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"output": output,
			"usage":  map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15},
		},
	})
	if err != nil {
		panic(err)
	}
	return string(payload)
}

func runResponsesStream(t *testing.T, events []string) modelrepo.StreamResult {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/responses", r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range events {
			fmt.Fprintf(w, "data: %s\n\n", event)
		}
	}))
	t.Cleanup(srv.Close)

	client := &OpenAIStreamClient{
		openAIClient: openAIClient{
			baseURL:    srv.URL,
			apiKey:     "key",
			httpClient: srv.Client(),
			modelName:  "gpt-5",
			tracker:    libtracker.NoopTracker{},
		},
	}
	stream, err := client.Stream(context.Background(), []modelrepo.Message{{Role: "user", Content: "list things"}})
	require.NoError(t, err)

	asm := modelrepo.NewStreamAssembler("openai", "gpt-5")
	for parcel := range stream {
		require.NoError(t, asm.Consume(parcel))
	}
	res, err := asm.Result()
	require.NoError(t, err)
	return res
}

func assertResponsesCallArguments(t *testing.T, res modelrepo.StreamResult, calls []responsesTestCall) {
	t.Helper()
	require.Len(t, res.ToolCalls, len(calls))
	assert.Empty(t, res.Violations, "no provider stream violation is expected once the final arguments are used")
	for i, want := range calls {
		got := res.ToolCalls[i]
		assert.Equal(t, want.id, got.ID)
		assert.Equal(t, want.name, got.Function.Name)
		assert.Equal(t, want.args, got.Function.Arguments, "call %d must keep the arguments the provider sent", i)
		assert.False(t, got.ArgsMissing, "call %d has arguments", i)
	}
}

func TestUnit_OpenAIStreamClient_ResponsesParallelCalls_ArgumentsOnlyInDoneEvents(t *testing.T) {
	t.Parallel()

	calls := responsesTestCalls(6)
	events := responsesAddedEvents(calls)
	events = append(events, responsesArgumentsDoneEvents(calls)...)
	events = append(events, responsesCompletedEvent(calls))

	assertResponsesCallArguments(t, runResponsesStream(t, events), calls)
}

func TestUnit_OpenAIStreamClient_ResponsesParallelCalls_ArgumentsOnlyInOutputItemDone(t *testing.T) {
	t.Parallel()

	calls := responsesTestCalls(6)
	events := responsesAddedEvents(calls)
	events = append(events, responsesItemDoneEvents(calls)...)
	events = append(events, responsesCompletedEvent(calls))

	assertResponsesCallArguments(t, runResponsesStream(t, events), calls)
}

func TestUnit_OpenAIStreamClient_ResponsesParallelCalls_ArgumentsOnlyInCompleted(t *testing.T) {
	t.Parallel()

	calls := responsesTestCalls(6)
	events := responsesAddedEvents(calls)
	events = append(events, responsesCompletedEvent(calls))

	assertResponsesCallArguments(t, runResponsesStream(t, events), calls)
}

func TestUnit_OpenAIStreamClient_ResponsesToolCalls_ReconstructedFromCompletedOnly(t *testing.T) {
	t.Parallel()

	calls := responsesTestCalls(2)
	assertResponsesCallArguments(t, runResponsesStream(t, []string{responsesCompletedEvent(calls)}), calls)
}

func TestUnit_OpenAIStreamClient_ResponsesToolArguments_DeltasAndDoneNotDuplicated(t *testing.T) {
	t.Parallel()

	events := []string{
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_r1","name":"get_weather","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"city\":"}`,
		`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"\"Berlin\"}"}`,
		`{"type":"response.function_call_arguments.done","item_id":"fc_1","output_index":1,"arguments":"{\"city\":\"Berlin\"}"}`,
		`{"type":"response.completed","response":{"output":[{"type":"function_call","id":"fc_1","call_id":"call_r1","name":"get_weather","arguments":"{\"city\":\"Berlin\"}"}]}}`,
	}
	res := runResponsesStream(t, events)
	require.Len(t, res.ToolCalls, 1)
	assert.Equal(t, `{"city":"Berlin"}`, res.ToolCalls[0].Function.Arguments)
	assert.Empty(t, res.Violations)
}

func TestUnit_OpenAIStreamClient_ResponsesToolArguments_TruncatedDeltasOverriddenByDone(t *testing.T) {
	t.Parallel()

	events := []string{
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_r1","name":"read_file","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"path\": \"REA"}`,
		`{"type":"response.function_call_arguments.done","item_id":"fc_1","output_index":1,"arguments":"{\"path\": \"README.md\"}"}`,
		`{"type":"response.completed","response":{"output":[{"type":"function_call","id":"fc_1","call_id":"call_r1","name":"read_file","arguments":"{\"path\": \"README.md\"}"}]}}`,
	}
	res := runResponsesStream(t, events)
	require.Len(t, res.ToolCalls, 1)
	assert.Equal(t, `{"path": "README.md"}`, res.ToolCalls[0].Function.Arguments)
	assert.Empty(t, res.Violations)
}

func TestUnit_OpenAIStreamClient_ResponsesToolArguments_ExplicitEmptyObjectIsNotMissing(t *testing.T) {
	t.Parallel()

	events := []string{
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_r1","name":"list_sessions","arguments":""}}`,
		`{"type":"response.function_call_arguments.done","item_id":"fc_1","output_index":1,"arguments":"{}"}`,
		`{"type":"response.completed","response":{"output":[{"type":"function_call","id":"fc_1","call_id":"call_r1","name":"list_sessions","arguments":"{}"}]}}`,
	}
	res := runResponsesStream(t, events)
	require.Len(t, res.ToolCalls, 1)
	assert.Equal(t, "{}", res.ToolCalls[0].Function.Arguments)
	assert.False(t, res.ToolCalls[0].ArgsMissing, "an explicit {} is what the model sent")
	assert.Empty(t, res.Violations)
}

func TestUnit_OpenAIStreamClient_ResponsesToolArguments_NoArgumentsAtAllIsFlagged(t *testing.T) {
	t.Parallel()

	events := []string{
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_r1","name":"read_file","arguments":""}}`,
		`{"type":"response.completed","response":{"output":[{"type":"function_call","id":"fc_1","call_id":"call_r1","name":"read_file","arguments":""}]}}`,
	}
	res := runResponsesStream(t, events)
	require.Len(t, res.ToolCalls, 1)
	assert.Equal(t, "{}", res.ToolCalls[0].Function.Arguments)
	assert.True(t, res.ToolCalls[0].ArgsMissing, "the model sent no arguments; the engine must know that")
	require.Len(t, res.Violations, 1)
	assert.Contains(t, res.Violations[0], "without arguments")
}
