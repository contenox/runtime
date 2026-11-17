package transport_test

import (
	"encoding/json"
	"testing"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/transport"
)

// configWireKeys is the spelling of every launch-configuration member on the
// wire. It is a second copy of the JSON tags by construction — that is what
// makes it a check rather than a restatement of the code: a rename on either
// side fails here, and a reader of the worker status payload cannot tell which
// keys it may rely on from a struct that carries none.
//
// The IDE reads these names off the worker status payload
// (contenox-inference-status.tsx and contenox-workers-contribution.ts), so a
// rename that lands without moving them breaks a shipped view silently.
var configWireKeys = []string{
	"AdmissionPermit",
	"DisableBOS",
	"EmbeddingPooling",
	"Execution",
	"FlashAttn",
	"HotContextTokens",
	"KVCacheType",
	"NumBatch",
	"NumCtx",
	"NumGpuLayers",
	"NumThreads",
	"Operation",
	"PlannerEffectiveContext",
	"PromptFormat",
	"PromptTemplateDigest",
	"ReasoningFormat",
	"SessionNamespace",
	"Target",
	"TensorSplit",
}

// permitWireKeys is the spelling of one owner permit on the wire.
var permitWireKeys = []string{"ID", "OwnerEpoch", "WorkerID"}

// TestUnit_LaunchConfigWireKeys pins the launch configuration's wire spelling.
// The type carries no tags for the runtime's own use, so the names a client
// reads are the only declaration of the shape; this holds them.
func TestUnit_LaunchConfigWireKeys(t *testing.T) {
	raw, err := json.Marshal(transport.Config{})
	if err != nil {
		t.Fatalf("marshal a launch configuration: %v", err)
	}
	assertKeys(t, raw, configWireKeys, "launch configuration")
}

// TestUnit_LaunchConfigKeysAreStableUnderContent pins that the keys do not
// depend on what the configuration holds: an empty configuration, one with
// every scalar set and one with nested values all spell the same names, so a
// reader never has to handle two spellings of one field.
func TestUnit_LaunchConfigKeysAreStableUnderContent(t *testing.T) {
	populated := transport.Config{
		Operation: "generate", SessionNamespace: "session", NumCtx: 4096,
		TensorSplit: []float32{0.5, 0.5}, FlashAttn: true, KVCacheType: "q8_0",
		Target:    transport.ExecutionTarget{Mode: transport.PlacementCPU},
		Execution: &transport.Execution{Context: 4096, Threads: 8},
	}
	raw, err := json.Marshal(populated)
	if err != nil {
		t.Fatalf("marshal a populated launch configuration: %v", err)
	}
	assertKeys(t, raw, configWireKeys, "populated launch configuration")
}

// TestUnit_PermitWireKeys pins the owner permit's wire spelling, which reaches a
// client nested in the launch configuration.
func TestUnit_PermitWireKeys(t *testing.T) {
	raw, err := json.Marshal(admission.Permit{})
	if err != nil {
		t.Fatalf("marshal a permit: %v", err)
	}
	assertKeys(t, raw, permitWireKeys, "owner permit")
}

func assertKeys(t *testing.T, raw json.RawMessage, want []string, what string) {
	t.Helper()
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("%s is not a JSON object: %v", what, err)
	}
	if len(decoded) != len(want) {
		t.Fatalf("%s carries %d keys and %d are declared: %s", what, len(decoded), len(want), raw)
	}
	for _, key := range want {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("%s does not carry the declared wire key %q: %s", what, key, raw)
		}
	}
}
