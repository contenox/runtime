package chatcompletions_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/codec/chatcompletions"
	"github.com/stretchr/testify/assert"
)

func TestUnit_Build_SerializesImageAsContentParts(t *testing.T) {
	pngBytes := []byte{0x89, 0x50, 0x4e, 0x47}
	msgs := []modelrepo.Message{
		{Role: "user", Content: "just text"},
		{Role: "user", Content: "describe this", Images: []modelrepo.ImagePart{
			{Data: pngBytes, MimeType: "image/png"},
		}},
	}

	req, _ := chatcompletions.Build("gpt-5-mini", msgs, nil)
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, raw)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got.Messages))
	}
	if b := got.Messages[0].Content; len(b) == 0 || b[0] != '"' {
		t.Errorf("text-only content should be a JSON string, got %s", b)
	}

	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL *struct {
			URL string `json:"url"`
		} `json:"image_url"`
	}
	if err := json.Unmarshal(got.Messages[1].Content, &parts); err != nil {
		t.Fatalf("image message content is not a parts array: %v\n%s", err, got.Messages[1].Content)
	}
	if len(parts) != 2 {
		t.Fatalf("expected [text, image_url] parts, got %d: %s", len(parts), got.Messages[1].Content)
	}
	if parts[0].Type != "text" || parts[0].Text != "describe this" {
		t.Errorf("text part wrong: %+v", parts[0])
	}
	if parts[1].Type != "image_url" || parts[1].ImageURL == nil {
		t.Fatalf("image part wrong: %+v", parts[1])
	}
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)
	if parts[1].ImageURL.URL != want {
		t.Errorf("data URI mismatch:\n want %s\n  got %s", want, parts[1].ImageURL.URL)
	}
}

func TestUnit_Build_SerializesAudioAsInputAudioParts(t *testing.T) {
	wavBytes := []byte{0x52, 0x49, 0x46, 0x46}
	msgs := []modelrepo.Message{
		{Role: "user", Content: "transcribe this", Audio: []modelrepo.AudioPart{
			{Data: wavBytes, MimeType: "audio/wav"},
		}},
	}

	req, _ := chatcompletions.Build("gpt-audio", msgs, nil)
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, raw)
	}
	var parts []struct {
		Type       string `json:"type"`
		Text       string `json:"text"`
		InputAudio *struct {
			Data   string `json:"data"`
			Format string `json:"format"`
		} `json:"input_audio"`
	}
	if err := json.Unmarshal(got.Messages[0].Content, &parts); err != nil {
		t.Fatalf("audio message content is not a parts array: %v\n%s", err, got.Messages[0].Content)
	}
	if len(parts) != 2 {
		t.Fatalf("expected [text, input_audio] parts, got %d: %s", len(parts), got.Messages[0].Content)
	}
	if parts[1].Type != "input_audio" || parts[1].InputAudio == nil {
		t.Fatalf("audio part wrong: %+v", parts[1])
	}
	if parts[1].InputAudio.Format != "wav" {
		t.Errorf("format: want wav, got %q", parts[1].InputAudio.Format)
	}
	if want := base64.StdEncoding.EncodeToString(wavBytes); parts[1].InputAudio.Data != want {
		t.Errorf("audio data mismatch: want %s, got %s", want, parts[1].InputAudio.Data)
	}
}

func TestUnit_Build_PlacesModelAndSanitizesToolNames(t *testing.T) {
	maxTok := 256
	cfg := &modelrepo.ChatConfig{
		MaxTokens: &maxTok,
		Tools: []modelrepo.Tool{{
			Type: "function",
			Function: &modelrepo.FunctionTool{
				Name:        "filesystem.list_directory",
				Description: "list a dir",
				Parameters:  map[string]any{"type": "object"},
			},
		}},
	}
	msgs := []modelrepo.Message{{Role: "user", Content: "hi"}}

	req, nameMap := chatcompletions.Build("meta/llama-3.3-70b-instruct-maas", msgs, cfg)

	if req.Model != "meta/llama-3.3-70b-instruct-maas" {
		t.Fatalf("model not placed in body: %q", req.Model)
	}
	if req.MaxTokens == nil || *req.MaxTokens != 256 {
		t.Fatalf("max_tokens not set")
	}
	if len(req.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(req.Tools))
	}
	san := req.Tools[0].Function.Name
	if san == "filesystem.list_directory" {
		t.Fatalf("tool name not sanitized (dot must be removed): %q", san)
	}
	if nameMap[san] != "filesystem.list_directory" {
		t.Fatalf("nameMap missing reverse mapping for %q: %v", san, nameMap)
	}
}

func TestUnit_Build_ToolOnlyAssistantHasNullContent(t *testing.T) {
	msgs := []modelrepo.Message{{
		Role:      "assistant",
		Content:   "",
		ToolCalls: []modelrepo.ToolCall{toolCall("call_1", "do_thing", "{}")},
	}}
	req, _ := chatcompletions.Build("m", msgs, nil)
	b, err := json.Marshal(req.Messages[0])
	if err != nil {
		t.Fatal(err)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(b, &probe); err != nil {
		t.Fatal(err)
	}
	if string(probe["content"]) != "null" {
		t.Fatalf("expected null content for tool-only assistant msg, got %s", probe["content"])
	}
}

func TestUnit_DecodeResponse_TextAndToolCalls(t *testing.T) {
	nameMap := map[string]string{"fs_list": "fs.list"}
	raw := []byte(`{"choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":"working","tool_calls":[{"id":"call_9","type":"function","function":{"name":"fs_list","arguments":"{\"path\":\"/tmp\"}"}}]}}]}`)

	res, err := chatcompletions.DecodeResponse(raw, nameMap)
	if err != nil {
		t.Fatal(err)
	}
	if res.Message.Content != "working" {
		t.Fatalf("content: %q", res.Message.Content)
	}
	if len(res.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(res.ToolCalls))
	}
	if res.ToolCalls[0].Function.Name != "fs.list" {
		t.Fatalf("tool name not un-sanitized: %q", res.ToolCalls[0].Function.Name)
	}
	if res.ToolCalls[0].Function.Arguments != `{"path":"/tmp"}` {
		t.Fatalf("args: %q", res.ToolCalls[0].Function.Arguments)
	}
}

func TestUnit_StreamDecoder_EmitsRawDeltasForAssembler(t *testing.T) {
	nameMap := map[string]string{"fs_list": "fs.list"}
	d := chatcompletions.NewStreamDecoder(nameMap)
	asm := modelrepo.NewStreamAssembler("openai", "test-model")

	lines := []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"lo"}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"fs_list","arguments":"{\"pa"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":\"/x\"}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18,"completion_tokens_details":{"reasoning_tokens":5}}}`,
	}
	for _, l := range lines {
		parcels, err := d.DecodeLine([]byte(l))
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range parcels {
			if err := asm.Consume(p); err != nil {
				t.Fatal(err)
			}
		}
	}
	d.MarkTerminated()
	if err := asm.Consume(d.Finish()); err != nil {
		t.Fatal(err)
	}
	res, err := asm.Result()
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "Hello" {
		t.Fatalf("assembled text: %q", res.Content)
	}
	if len(res.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(res.ToolCalls))
	}
	if res.ToolCalls[0].Function.Name != "fs.list" {
		t.Fatalf("stream tool name not un-sanitized: %q", res.ToolCalls[0].Function.Name)
	}
	if res.ToolCalls[0].Function.Arguments != `{"path":"/x"}` {
		t.Fatalf("assembled args: %q", res.ToolCalls[0].Function.Arguments)
	}
	if res.FinishReason != "tool_calls" {
		t.Fatalf("finish reason: %q", res.FinishReason)
	}
	if res.Usage == nil || res.Usage.PromptTokens != 11 || res.Usage.CompletionTokens != 7 || res.Usage.ThinkingTokens != 5 || res.Usage.TotalTokens != 18 {
		t.Fatalf("usage: %+v", res.Usage)
	}
}

func TestUnit_StreamDecoder_UnterminatedStreamIsTruncated(t *testing.T) {
	d := chatcompletions.NewStreamDecoder(nil)
	asm := modelrepo.NewStreamAssembler("openai", "test-model")
	parcels, err := d.DecodeLine([]byte(`{"choices":[{"index":0,"delta":{"content":"half"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range parcels {
		if err := asm.Consume(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := asm.Consume(d.Finish()); err != nil {
		t.Fatal(err)
	}
	if _, err := asm.Result(); !errors.Is(err, modelrepo.ErrStreamTruncated) {
		t.Fatalf("expected truncation, got %v", err)
	}
}

func TestUnit_StreamDecoder_FinishReasonIsPreserved(t *testing.T) {
	d := chatcompletions.NewStreamDecoder(nil)
	asm := modelrepo.NewStreamAssembler("openai", "test-model")
	if _, err := d.DecodeLine([]byte(`{"choices":[{"index":0,"delta":{"content":"cut"}}]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.DecodeLine([]byte(`{"choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`)); err != nil {
		t.Fatal(err)
	}
	d.MarkTerminated()
	if err := asm.Consume(d.Finish()); err != nil {
		t.Fatal(err)
	}
	res, err := asm.Result()
	if err != nil {
		t.Fatal(err)
	}
	if res.FinishReason != "length" {
		t.Fatalf("finish reason: %q", res.FinishReason)
	}
}

func TestUnit_ReasoningEffort_MapsThinkOntoTheWire(t *testing.T) {
	none := chatcompletions.ReasoningEffort(nil)
	assert.Equal(t, "", none)

	auto := "auto"
	assert.Equal(t, "", chatcompletions.ReasoningEffort(&auto))

	on := "on"
	assert.Equal(t, "high", chatcompletions.ReasoningEffort(&on))

	off := "off"
	assert.Equal(t, "", chatcompletions.ReasoningEffort(&off))

	highest := "max"
	assert.Equal(t, "max", chatcompletions.ReasoningEffort(&highest))

	low := "LOW"
	assert.Equal(t, "low", chatcompletions.ReasoningEffort(&low))

	invalid := "very-much"
	assert.Equal(t, "", chatcompletions.ReasoningEffort(&invalid))
}

func wireRequest(t *testing.T, cfg *modelrepo.ChatConfig) map[string]json.RawMessage {
	t.Helper()
	req, _ := chatcompletions.Build("m", []modelrepo.Message{{Role: "user", Content: "hi"}}, cfg)
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, raw)
	}
	return probe
}

func TestUnit_Build_StopSequencesReachTheStopMember(t *testing.T) {
	probe := wireRequest(t, &modelrepo.ChatConfig{StopSequences: []string{"\n\nUser:", "###"}})

	var stops []string
	if err := json.Unmarshal(probe["stop"], &stops); err != nil {
		t.Fatalf("stop is not a string array: %v (%s)", err, probe["stop"])
	}
	assert.Equal(t, []string{"\n\nUser:", "###"}, stops)
}

func TestUnit_Build_NoStopSequencesOmitsTheStopMember(t *testing.T) {
	probe := wireRequest(t, &modelrepo.ChatConfig{})
	if _, ok := probe["stop"]; ok {
		t.Fatalf("stop must be absent when no stop sequence is set, got %s", probe["stop"])
	}
}

func TestUnit_Build_ToolChoiceModeReachesTheWire(t *testing.T) {
	tool := []modelrepo.Tool{{Type: "function", Function: &modelrepo.FunctionTool{Name: "fs.list"}}}
	for _, mode := range []modelrepo.ToolChoiceMode{modelrepo.ToolChoiceAuto, modelrepo.ToolChoiceNone, modelrepo.ToolChoiceRequired} {
		probe := wireRequest(t, &modelrepo.ChatConfig{Tools: tool, ToolChoice: &modelrepo.ToolChoice{Mode: mode}})
		var got string
		if err := json.Unmarshal(probe["tool_choice"], &got); err != nil {
			t.Fatalf("tool_choice is not a mode string: %v (%s)", err, probe["tool_choice"])
		}
		assert.Equal(t, string(mode), got)
	}
}

func TestUnit_Build_ToolChoiceWithoutToolsIsOmitted(t *testing.T) {
	probe := wireRequest(t, &modelrepo.ChatConfig{ToolChoice: &modelrepo.ToolChoice{Mode: modelrepo.ToolChoiceNone}})
	if _, ok := probe["tool_choice"]; ok {
		t.Fatalf("tool_choice must not be sent without tool definitions, got %s", probe["tool_choice"])
	}
}

func TestUnit_Build_NamedToolChoiceReachesTheObjectForm(t *testing.T) {
	cfg := &modelrepo.ChatConfig{
		Tools:      []modelrepo.Tool{{Type: "function", Function: &modelrepo.FunctionTool{Name: "fs.list"}}},
		ToolChoice: &modelrepo.ToolChoice{Mode: modelrepo.ToolChoiceRequired, Tool: "fs.list"},
	}
	probe := wireRequest(t, cfg)

	var named struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(probe["tool_choice"], &named); err != nil {
		t.Fatalf("tool_choice is not the object form: %v (%s)", err, probe["tool_choice"])
	}
	if named.Type != "function" {
		t.Fatalf("object tool_choice type: %q", named.Type)
	}
	if named.Function.Name != "fs_list" {
		t.Fatalf("named tool_choice must carry the sanitized declared name, got %q", named.Function.Name)
	}
}

func TestUnit_Build_ParallelToolCallsReachesTheWire(t *testing.T) {
	disabled := false
	probe := wireRequest(t, &modelrepo.ChatConfig{ParallelToolCalls: &disabled})
	assert.Equal(t, "false", string(probe["parallel_tool_calls"]))
}

func TestUnit_Build_ResponseFormatReachesTheWire(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}}
	probe := wireRequest(t, &modelrepo.ChatConfig{Format: &modelrepo.ResponseFormat{
		Mode:   modelrepo.ResponseFormatJSONSchema,
		Name:   "weather",
		Schema: schema,
		Strict: true,
	}})

	var format struct {
		Type       string `json:"type"`
		JSONSchema *struct {
			Name   string         `json:"name"`
			Schema map[string]any `json:"schema"`
			Strict bool           `json:"strict"`
		} `json:"json_schema"`
	}
	if err := json.Unmarshal(probe["response_format"], &format); err != nil {
		t.Fatalf("response_format not encoded: %v (%s)", err, probe["response_format"])
	}
	if format.Type != "json_schema" || format.JSONSchema == nil {
		t.Fatalf("response_format shape: %s", probe["response_format"])
	}
	if format.JSONSchema.Name != "weather" || !format.JSONSchema.Strict {
		t.Fatalf("json_schema members: %s", probe["response_format"])
	}
	if format.JSONSchema.Schema["type"] != "object" {
		t.Fatalf("schema not carried: %s", probe["response_format"])
	}
}

func TestUnit_Build_JSONObjectFormatCarriesNoSchema(t *testing.T) {
	probe := wireRequest(t, &modelrepo.ChatConfig{Format: &modelrepo.ResponseFormat{Mode: modelrepo.ResponseFormatJSONObject}})
	assert.Equal(t, `{"type":"json_object"}`, string(probe["response_format"]))
}

func TestUnit_Build_TextFormatIsAbsentFromTheWire(t *testing.T) {
	probe := wireRequest(t, &modelrepo.ChatConfig{Format: &modelrepo.ResponseFormat{Mode: modelrepo.ResponseFormatText}})
	if _, ok := probe["response_format"]; ok {
		t.Fatalf("a text format constrains nothing and must not be sent, got %s", probe["response_format"])
	}
}

func TestUnit_DecodeRequest_AcceptsBothToolChoiceForms(t *testing.T) {
	var named chatcompletions.Request
	if err := json.Unmarshal([]byte(`{"model":"m","tool_choice":{"type":"function","function":{"name":"fs_list"}}}`), &named); err != nil {
		t.Fatalf("object tool_choice rejected: %v", err)
	}
	marshalled, err := json.Marshal(named)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	assert.Contains(t, string(marshalled), `"function":{"name":"fs_list"}`)

	var mode chatcompletions.Request
	if err := json.Unmarshal([]byte(`{"model":"m","tool_choice":"required"}`), &mode); err != nil {
		t.Fatalf("string tool_choice rejected: %v", err)
	}
	assert.Equal(t, "required", mode.ToolChoice)
}

func TestUnit_ChatConfig_RefusesWhatNoDialectAccepts(t *testing.T) {
	tooMany := []string{"a", "b", "c", "d", "e"}
	if err := (&modelrepo.ChatConfig{StopSequences: tooMany}).Validate(); err == nil {
		t.Fatal("more stop sequences than the shared maximum must be refused")
	}
	if err := (&modelrepo.ChatConfig{StopSequences: []string{"ok", ""}}).Validate(); err == nil {
		t.Fatal("an empty stop sequence must be refused")
	}
	if err := (&modelrepo.ChatConfig{ToolChoice: &modelrepo.ToolChoice{Mode: "sometimes"}}).Validate(); err == nil {
		t.Fatal("an unknown tool choice mode must be refused")
	}
	if err := (&modelrepo.ChatConfig{ToolChoice: &modelrepo.ToolChoice{Mode: modelrepo.ToolChoiceNone, Tool: "fs.list"}}).Validate(); err == nil {
		t.Fatal("a named tool under the none mode must be refused")
	}
	if err := (&modelrepo.ChatConfig{Format: &modelrepo.ResponseFormat{Mode: modelrepo.ResponseFormatJSONSchema}}).Validate(); err == nil {
		t.Fatal("a json_schema format without a schema must be refused")
	}
	if err := (&modelrepo.ChatConfig{
		StopSequences: []string{"x"},
		Tools:         []modelrepo.Tool{{Type: "function", Function: &modelrepo.FunctionTool{Name: "fs.list"}}},
		ToolChoice:    &modelrepo.ToolChoice{Mode: modelrepo.ToolChoiceRequired, Tool: "fs.list"},
		Format:        &modelrepo.ResponseFormat{Mode: modelrepo.ResponseFormatJSONObject},
	}).Validate(); err != nil {
		t.Fatalf("a complete config must validate, got %v", err)
	}
}

func toolCall(id, name, args string) modelrepo.ToolCall {
	call := modelrepo.ToolCall{ID: id, Type: "function"}
	call.Function.Name = name
	call.Function.Arguments = args
	return call
}

func TestUnit_DecodeResponse_RefusalIsTheTypedRefusal(t *testing.T) {
	raw := []byte(`{"choices":[{"index":0,"finish_reason":"refusal","message":{"role":"assistant","content":"","refusal":"I can't help with that."}}]}`)
	_, err := chatcompletions.DecodeResponse(raw, nil)
	if !errors.Is(err, modelrepo.ErrRefused) {
		t.Fatalf("expected the typed refusal, got %v", err)
	}
	if !strings.Contains(err.Error(), "I can't help with that.") {
		t.Fatalf("the vendor's refusal text must be readable: %v", err)
	}
}

func TestUnit_DecodeResponse_RefusalFinishReasonIsTheTypedRefusal(t *testing.T) {
	raw := []byte(`{"choices":[{"index":0,"finish_reason":"refusal","message":{"role":"assistant","content":""}}]}`)
	_, err := chatcompletions.DecodeResponse(raw, nil)
	if !errors.Is(err, modelrepo.ErrRefused) {
		t.Fatalf("expected the typed refusal, got %v", err)
	}
}

func TestUnit_DecodeResponse_EmptyAnswerStaysAnError(t *testing.T) {
	raw := []byte(`{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":""}}]}`)
	_, err := chatcompletions.DecodeResponse(raw, nil)
	if err == nil {
		t.Fatal("an empty answer must not decode as a result")
	}
	if errors.Is(err, modelrepo.ErrRefused) {
		t.Fatalf("an empty answer is not a refusal: %v", err)
	}
}

func TestUnit_StreamDecoder_RefusalIsTheTypedRefusal(t *testing.T) {
	d := chatcompletions.NewStreamDecoder(nil)
	parcels, err := d.DecodeLine([]byte(`{"choices":[{"index":0,"delta":{"refusal":"I won't."}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(parcels) != 1 || parcels[0].Error == nil {
		t.Fatalf("expected one error parcel, got %+v", parcels)
	}
	if !errors.Is(parcels[0].Error, modelrepo.ErrRefused) {
		t.Fatalf("expected the typed refusal, got %v", parcels[0].Error)
	}

	asm := modelrepo.NewStreamAssembler("openai", "test-model")
	if err := asm.Consume(parcels[0]); !errors.Is(err, modelrepo.ErrRefused) {
		t.Fatalf("assembly must report the refusal, got %v", err)
	}
	if _, err := asm.Result(); !errors.Is(err, modelrepo.ErrRefused) {
		t.Fatalf("result must report the refusal, got %v", err)
	}
}

func TestUnit_DecodeResponse_UsageDetailIsCarried(t *testing.T) {
	raw := []byte(`{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],
		"usage":{"prompt_tokens":100,"completion_tokens":40,"total_tokens":140,
		"prompt_tokens_details":{"cached_tokens":64},
		"completion_tokens_details":{"reasoning_tokens":25}}}`)
	res, err := chatcompletions.DecodeResponse(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Usage == nil {
		t.Fatal("usage must be decoded")
	}
	if res.Usage.CacheReadTokens != 64 {
		t.Fatalf("cache reads: %+v", res.Usage)
	}
	if res.Usage.ThinkingTokens != 25 {
		t.Fatalf("thinking tokens: %+v", res.Usage)
	}
	if res.Usage.PromptTokens != 100 || res.Usage.CompletionTokens != 40 || res.Usage.TotalTokens != 140 {
		t.Fatalf("token counts: %+v", res.Usage)
	}
}

func TestUnit_StreamDecoder_ErrorFrameFailsTheTurn(t *testing.T) {
	d := chatcompletions.NewStreamDecoder(nil)
	asm := modelrepo.NewStreamAssembler("openai", "test-model")

	parcels, err := d.DecodeLine([]byte(`{"choices":[{"index":0,"delta":{"content":"par"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range parcels {
		if err := asm.Consume(p); err != nil {
			t.Fatal(err)
		}
	}

	parcels, err = d.DecodeLine([]byte(`{"error":{"message":"injected mid-stream error","type":"server_error","code":500}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(parcels) != 1 || parcels[0].Error == nil {
		t.Fatalf("expected one error parcel, got %+v", parcels)
	}
	if !strings.Contains(parcels[0].Error.Error(), "injected mid-stream error") {
		t.Fatalf("the error must carry the vendor message, got %v", parcels[0].Error)
	}
	var providerErr *modelrepo.ProviderError
	if !errors.As(parcels[0].Error, &providerErr) || providerErr.StatusCode != 500 {
		t.Fatalf("expected a provider error with status 500, got %v", parcels[0].Error)
	}

	_ = asm.Consume(parcels[0])
	d.MarkTerminated()
	_ = asm.Consume(d.Finish())
	if _, err := asm.Result(); err == nil || !strings.Contains(err.Error(), "injected mid-stream error") {
		t.Fatalf("assembly must report the streamed error, got %v", err)
	}
}

func TestUnit_StreamDecoder_ErrorFrameWithVendorCodeIsClassified(t *testing.T) {
	d := chatcompletions.NewStreamDecoder(nil)
	parcels, err := d.DecodeLine([]byte(`{"error":{"message":"This request is over the limit","type":"invalid_request_error","code":"context_length_exceeded"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(parcels) != 1 || !errors.Is(parcels[0].Error, modelrepo.ErrContextLengthExceeded) {
		t.Fatalf("expected the typed context limit, got %+v", parcels)
	}
}
