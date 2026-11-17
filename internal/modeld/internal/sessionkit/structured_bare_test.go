package sessionkit_test

import (
	"testing"

	"github.com/contenox/runtime/internal/modeld/internal/sessionkit"
)

func TestUnit_StructuredToolCallChunk_BareNativeSchemaPayload(t *testing.T) {
	for _, raw := range []string{
		`{"name":"contenox_ready","arguments":{"status":"ok"}}`,
		`{"tool_calls":[{"function":{"name":"contenox_ready","arguments":{"status":"ok"}}}]}`,
	} {
		t.Run(raw, func(t *testing.T) {
			chunk, err := sessionkit.StructuredToolCallChunk(raw)
			if err != nil {
				t.Fatal(err)
			}
			if chunk.Text != "" || len(chunk.ToolCalls) != 1 {
				t.Fatalf("chunk = %+v, want one call without leaked JSON", chunk)
			}
			call := chunk.ToolCalls[0]
			if call.ID == "" || call.Type != "function" || call.Function.Name != "contenox_ready" || call.Function.Arguments != `{"status":"ok"}` {
				t.Fatalf("call = %+v", call)
			}
		})
	}
}

func TestUnit_StructuredToolCallChunk_InvalidBarePayloadFails(t *testing.T) {
	for _, raw := range []string{
		`{}`,
		`{"tool_calls":[]}`,
		`{"arguments":{"status":"ok"}}`,
		`{"name":"","arguments":{}}`,
		`{"name":3,"arguments":{}}`,
		`{"function":{"name":"contenox_ready","arguments":{"status":"ok"}}}`,
		`{"name":"contenox_ready"}`,
		`{"name":"contenox_ready",`,
	} {
		t.Run(raw, func(t *testing.T) {
			if chunk, err := sessionkit.StructuredToolCallChunk(raw); err == nil {
				t.Fatalf("invalid payload returned success: %+v", chunk)
			}
		})
	}
}

func TestUnit_StructuredToolCallChunk_ContentEnvelopeStaysContent(t *testing.T) {
	chunk, err := sessionkit.StructuredToolCallChunk(`{"content":"confirmation","name":"not_a_call"}`)
	if err != nil {
		t.Fatal(err)
	}
	if chunk.Text != "confirmation" || len(chunk.ToolCalls) != 0 {
		t.Fatalf("chunk = %+v", chunk)
	}
}
