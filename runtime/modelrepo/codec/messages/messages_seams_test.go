package messages_test

import (
	"encoding/json"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	msgcodec "github.com/contenox/runtime/runtime/modelrepo/codec/messages"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func buildOne(t *testing.T, cfg *modelrepo.ChatConfig) msgcodec.Request {
	t.Helper()
	req, _, err := msgcodec.Build([]modelrepo.Message{{Role: "user", Content: "hi"}}, cfg)
	require.NoError(t, err)
	return req
}

func TestUnit_Build_StopSequencesReachTheRequest(t *testing.T) {
	req := buildOne(t, &modelrepo.ChatConfig{StopSequences: []string{"\n\nHuman:", "###"}})
	assert.Equal(t, []string{"\n\nHuman:", "###"}, req.StopSequences)

	absent := buildOne(t, &modelrepo.ChatConfig{})
	assert.Empty(t, absent.StopSequences)
}

func TestUnit_Build_ToolChoiceModesReachTheRequest(t *testing.T) {
	cases := map[modelrepo.ToolChoiceMode]string{
		modelrepo.ToolChoiceAuto:     "auto",
		modelrepo.ToolChoiceNone:     "none",
		modelrepo.ToolChoiceRequired: "any",
	}
	for mode, want := range cases {
		req := buildOne(t, &modelrepo.ChatConfig{
			Tools:      []modelrepo.Tool{{Type: "function", Function: &modelrepo.FunctionTool{Name: "fs.list"}}},
			ToolChoice: &modelrepo.ToolChoice{Mode: mode},
		})
		require.NotNil(t, req.ToolChoice, "mode %s must reach the request", mode)
		assert.Equal(t, want, req.ToolChoice.Type)
		assert.Empty(t, req.ToolChoice.Name)
	}
}

func TestUnit_Build_ToolChoiceWithoutToolsIsOmitted(t *testing.T) {
	req := buildOne(t, &modelrepo.ChatConfig{ToolChoice: &modelrepo.ToolChoice{Mode: modelrepo.ToolChoiceNone}})
	assert.Nil(t, req.ToolChoice, "a tool_choice without tool definitions is rejected by providers")
}

func TestUnit_Build_NamedToolChoiceUsesTheDeclaredName(t *testing.T) {
	req, _, err := msgcodec.Build(
		[]modelrepo.Message{{Role: "user", Content: "hi"}},
		&modelrepo.ChatConfig{
			Tools:      []modelrepo.Tool{{Type: "function", Function: &modelrepo.FunctionTool{Name: "fs.list"}}},
			ToolChoice: &modelrepo.ToolChoice{Mode: modelrepo.ToolChoiceRequired, Tool: "fs.list"},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, req.ToolChoice)
	assert.Equal(t, "tool", req.ToolChoice.Type)
	require.Len(t, req.Tools, 1)
	assert.Equal(t, req.Tools[0].Name, req.ToolChoice.Name)
	assert.Equal(t, "fs_list", req.ToolChoice.Name)
}

func TestUnit_Build_DisablingParallelCallsReachesTheRequest(t *testing.T) {
	tool := []modelrepo.Tool{{Type: "function", Function: &modelrepo.FunctionTool{Name: "fs.list"}}}
	disabled := false
	req := buildOne(t, &modelrepo.ChatConfig{Tools: tool, ParallelToolCalls: &disabled})
	require.NotNil(t, req.ToolChoice)
	assert.True(t, req.ToolChoice.DisableParallelToolUse)

	enabled := true
	on := buildOne(t, &modelrepo.ChatConfig{Tools: tool, ParallelToolCalls: &enabled})
	assert.Nil(t, on.ToolChoice, "enabling parallel calls is the dialect default and says nothing")
}

func TestUnit_Build_ParameterlessToolCarriesAnEmptyObjectSchema(t *testing.T) {
	req, _, err := msgcodec.Build(
		[]modelrepo.Message{{Role: "user", Content: "hi"}},
		&modelrepo.ChatConfig{Tools: []modelrepo.Tool{{Type: "function", Function: &modelrepo.FunctionTool{Name: "now"}}}},
	)
	require.Len(t, req.Tools, 1)
	raw, err := json.Marshal(req.Tools[0])
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"input_schema":{"type":"object"}`)
}

func TestUnit_Build_DeclaredSchemaIsCarriedUnchanged(t *testing.T) {
	schema := map[string]any{"type": "object", "required": []string{"city"}}
	req, _, err := msgcodec.Build(
		[]modelrepo.Message{{Role: "user", Content: "hi"}},
		&modelrepo.ChatConfig{Tools: []modelrepo.Tool{{Type: "function", Function: &modelrepo.FunctionTool{Name: "weather", Parameters: schema}}}},
	)
	require.Len(t, req.Tools, 1)
	raw, err := json.Marshal(req.Tools[0])
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"required":["city"]`)
}

func decodeAll(t *testing.T, lines ...string) (*msgcodec.StreamDecoder, *modelrepo.StreamAssembler) {
	t.Helper()
	dec := msgcodec.NewStreamDecoder(nil)
	asm := modelrepo.NewStreamAssembler("anthropic", "claude")
	for _, line := range lines {
		parcels, err := dec.DecodeLine([]byte(line))
		require.NoError(t, err)
		for _, p := range parcels {
			require.NoError(t, asm.Consume(p))
		}
	}
	require.NoError(t, asm.Consume(dec.Finish()))
	return dec, asm
}

func TestUnit_StreamDecoder_MessageStopTerminatesTheStream(t *testing.T) {
	_, asm := decodeAll(t,
		`{"type":"message_start","message":{"usage":{"input_tokens":9}}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`,
		`{"type":"message_stop"}`,
	)
	res, err := asm.Result()
	require.NoError(t, err)
	assert.Equal(t, "hi", res.Content)
	assert.Equal(t, "end_turn", res.FinishReason)
}

func TestUnit_StreamDecoder_MissingMessageStopAndStopReasonIsTruncated(t *testing.T) {
	_, asm := decodeAll(t,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"half"}}`,
	)
	_, err := asm.Result()
	assert.ErrorIs(t, err, modelrepo.ErrStreamTruncated)
}

func TestUnit_StreamDecoder_StopReasonWithoutMessageStopCompletes(t *testing.T) {
	_, asm := decodeAll(t,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"done"}}`,
		`{"type":"message_delta","delta":{"stop_reason":"refusal"}}`,
	)
	res, err := asm.Result()
	require.NoError(t, err)
	assert.Equal(t, "refusal", res.FinishReason)
	assert.Equal(t, "done", res.Content)
}

func TestUnit_Decode_MatchedStopSequenceIsReported(t *testing.T) {
	raw := []byte(`{
		"role": "assistant",
		"stop_reason": "stop_sequence",
		"stop_sequence": "###",
		"content": [{"type": "text", "text": "done"}]
	}`)

	var resp msgcodec.Response
	require.NoError(t, json.Unmarshal(raw, &resp))
	assert.Equal(t, "stop_sequence", resp.StopReason)
	assert.Equal(t, "###", resp.StopSequence)

	res, err := msgcodec.DecodeResponse(raw, nil)
	require.NoError(t, err)
	assert.Equal(t, "stop_sequence", res.FinishReason)
}

func TestUnit_StreamDecoder_MatchedStopSequenceReachesTheTerminal(t *testing.T) {
	d := msgcodec.NewStreamDecoder(nil)
	for _, ev := range []string{
		`{"type":"message_start","message":{"role":"assistant"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"done"}}`,
		`{"type":"message_delta","delta":{"stop_reason":"stop_sequence","stop_sequence":"###"}}`,
		`{"type":"message_stop"}`,
	} {
		_, err := d.DecodeLine([]byte(ev))
		require.NoError(t, err)
	}

	term := d.Finish().Terminal
	require.NotNil(t, term)
	assert.Equal(t, "stop_sequence", term.FinishReason)
	assert.Equal(t, "###", term.StopSequence, "the matched stop sequence reaches the caller")
}
