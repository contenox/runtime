package openvino_test

import (
	"testing"

	"github.com/contenox/runtime/internal/modeld/openvino"
	"github.com/contenox/runtime/internal/modeld/openvino/ovsession"
)

// TestUnit_NativeStreamChunkPreservesMeasuredTokens pins that a decode whose
// token counts the backend reported carries them, with the finish reason it
// finished under.
func TestUnit_NativeStreamChunkPreservesMeasuredTokens(t *testing.T) {
	chunk := openvino.NativeStreamChunk(ovsession.StreamChunk{Text: "answer", Metrics: &ovsession.PipelineMetrics{UsageKnown: true, PromptTokens: 71, CompletionTokens: 13, FinishReason: "length"}})
	if chunk.Usage == nil || chunk.Usage.PromptTokens != 71 || chunk.Usage.CompletionTokens != 13 || chunk.FinishReason != "length" || chunk.Text != "answer" {
		t.Fatalf("chunk = %+v", chunk)
	}
	unknown := openvino.NativeStreamChunk(ovsession.StreamChunk{Metrics: &ovsession.PipelineMetrics{}})
	if unknown.Usage != nil {
		t.Fatalf("unreported usage = %+v", unknown.Usage)
	}
}

// TestUnit_NativeStreamChunkCarriesTheFinishReasonWithoutUsage pins that
// completion and usage accounting are separate facts. An OpenVINO decode that
// finishes while its token counts are unknown must still say it finished:
// a stream that ends without the marker reads as truncated, which would report
// a correct answer as a failure, and the unknown usage must stay unset rather
// than becoming a zero a meter would read as a real count.
func TestUnit_NativeStreamChunkCarriesTheFinishReasonWithoutUsage(t *testing.T) {
	finished := openvino.NativeStreamChunk(ovsession.StreamChunk{
		Text:    "answer",
		Metrics: &ovsession.PipelineMetrics{FinishReason: "stop"},
	})
	if finished.FinishReason != "stop" {
		t.Fatalf("a decode that finished with unknown usage carries its finish reason, got %+v", finished)
	}
	if finished.Usage != nil {
		t.Fatalf("an unknown usage stays unset, got %+v", finished.Usage)
	}
	if finished.Text != "answer" {
		t.Fatalf("the text of the final chunk is kept, got %+v", finished)
	}

	length := openvino.NativeStreamChunk(ovsession.StreamChunk{Metrics: &ovsession.PipelineMetrics{FinishReason: "length"}})
	if length.FinishReason != "length" || length.Usage != nil {
		t.Fatalf("a length stop with unknown usage = %+v", length)
	}

	partial := openvino.NativeStreamChunk(ovsession.StreamChunk{Text: "half"})
	if partial.FinishReason != "" || partial.Usage != nil || partial.Text != "half" {
		t.Fatalf("a chunk with no metrics carries no marker and no usage: %+v", partial)
	}
}
