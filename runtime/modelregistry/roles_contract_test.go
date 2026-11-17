package modelregistry_test

import (
	"errors"
	"testing"

	"github.com/contenox/runtime/internal/modeld/capacity"
	"github.com/contenox/runtime/runtime/modelregistry"
)

func roleCandidate(name string) modelregistry.ModelDescriptor {
	return modelregistry.ModelDescriptor{Name: name, Curated: true, AutoEligible: true, License: "apache-2.0", Origin: "china",
		SizeBytes: 1 << 28, AutoContextLimit: 32768, AutoKVProfile: capacity.LayerKVProfile{GlobalLayers: 2, PerLayerKVBytes: 1024}}
}

func TestUnit_RoleSelectionRequiresExecutionContractNotNameOrUseCase(t *testing.T) {
	unqualified := roleCandidate("coder-fim-embedding")
	unqualified.UseCase, unqualified.Notes = "coding", "supports FIM and embeddings"
	for _, role := range []string{"autocomplete", "embedding"} {
		_, err := modelregistry.SelectRole([]modelregistry.ModelDescriptor{unqualified}, role, "", "llama", 2<<30, "f16")
		if !errors.Is(err, modelregistry.ErrNoAutoModel) {
			t.Fatalf("%s accepted workflow labels as evidence: %v", role, err)
		}
	}
}

func TestUnit_RoleSelectionIsIndependentAndResourceBounded(t *testing.T) {
	completion, embedding := roleCandidate("completion"), roleCandidate("retrieval")
	completion.Completion = modelregistry.CompletionProfile{Mode: modelregistry.CompletionInstruct, ContextTokens: 8192, Evidence: "instruction-following code-generation evidence"}
	embedding.Embedding = modelregistry.EmbeddingProfile{Pooling: "last", ContextTokens: 4096, Dimension: 1024, Normalize: true, Evidence: "dedicated retrieval evidence"}
	candidates := []modelregistry.ModelDescriptor{completion, embedding}
	for role, expected := range map[string]string{"autocomplete": "completion", "embedding": "retrieval"} {
		got, err := modelregistry.SelectRole(candidates, role, "china", "llama", 2<<30, "f16")
		if err != nil || got.Name != expected {
			t.Fatalf("%s got %s: %v", role, got.Name, err)
		}
		_, err = modelregistry.SelectRole(candidates, role, "china", "llama", got.RoleMemoryBytes(role, "f16")-1, "f16")
		if !errors.Is(err, modelregistry.ErrNoAutoModel) {
			t.Fatalf("%s exceeded resource budget: %v", role, err)
		}
		_, err = modelregistry.SelectRole(candidates, role, "eu", "llama", 2<<30, "f16")
		if !errors.Is(err, modelregistry.ErrNoAutoModel) {
			t.Fatalf("%s ignored origin: %v", role, err)
		}
		_, err = modelregistry.SelectRole(candidates, role, "china", "openvino", 2<<30, "f16")
		if !errors.Is(err, modelregistry.ErrNoAutoModel) {
			t.Fatalf("%s ignored backend: %v", role, err)
		}
	}
}

func TestUnit_RoleSelectionTakesTheCheapestSufficientCandidate(t *testing.T) {
	cheap, large := roleCandidate("fim-1.5b-q4"), roleCandidate("fim-7b")
	cheap.RecommendedVRAMGB, large.RecommendedVRAMGB = 3, 8
	cheap.SizeBytes, large.SizeBytes = 986<<20, 4683<<20
	for _, candidate := range []*modelregistry.ModelDescriptor{&cheap, &large} {
		candidate.Completion = modelregistry.CompletionProfile{Mode: modelregistry.CompletionFIM, FIMFormat: "qwen2.5-coder", ContextTokens: 8192, Evidence: "native FIM contract"}
	}
	got, err := modelregistry.SelectRole([]modelregistry.ModelDescriptor{large, cheap}, "autocomplete", "china", "llama", 32<<30, "f16")
	if err != nil || got.Name != "fim-1.5b-q4" {
		t.Fatalf("completion selected %q: %v", got.Name, err)
	}
}

func TestUnit_NativeFIMDoesNotQualifyForAnInstructedCompletionChain(t *testing.T) {
	candidate := roleCandidate("native-fim")
	candidate.Completion = modelregistry.CompletionProfile{Mode: "native-fim", ContextTokens: 8192, Evidence: "native FIM training"}
	if candidate.SupportsCompletion() {
		t.Fatal("native FIM was confused with instructed completion")
	}
}
