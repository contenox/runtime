package modelregistry_test

import (
	"context"
	"strings"
	"testing"

	"github.com/contenox/runtime/internal/modeld/capacity"
	"github.com/contenox/runtime/runtime/modelregistry"
)

func completeDescriptor(name string) modelregistry.ModelDescriptor {
	return modelregistry.ModelDescriptor{
		Name: name, SourceURL: "https://huggingface.co/example/model.gguf", SizeBytes: 1 << 30,
		Curated: true, AutoContextLimit: 32768, AutoKVProfile: capacity.LayerKVProfile{GlobalLayers: 32, PerLayerKVBytes: 2048},
	}
}

func TestUnit_TheEmbeddedCatalogIsLoadedAndValidated(t *testing.T) {
	descriptors, err := modelregistry.NewCurated().List(context.Background())
	if err != nil {
		t.Fatalf("list the embedded catalog: %v", err)
	}
	if len(descriptors) == 0 {
		t.Fatal("the embedded catalog declares no model")
	}
	for _, descriptor := range descriptors {
		if err := modelregistry.ValidateDescriptor(descriptor.Name, descriptor); err != nil {
			t.Fatalf("embedded entry %s: %v", descriptor.Name, err)
		}
	}
}

func TestUnit_AnEntryIncompleteForItsClaimedRoleIsRejectedByName(t *testing.T) {
	fim := completeDescriptor("fim-entry")
	fim.Completion = modelregistry.CompletionProfile{Mode: modelregistry.CompletionFIM, FIMFormat: "not-a-declared-format", ContextTokens: 8192, Evidence: "declared"}
	embedding := completeDescriptor("embedding-entry")
	embedding.Embedding = modelregistry.EmbeddingProfile{Pooling: "last", ContextTokens: 4096, Normalize: true, Evidence: "declared"}
	editor := completeDescriptor("edit-entry")
	editor.NextEdit = modelregistry.NextEditProfile{Format: "not-a-declared-format", ContextTokens: 8192, Evidence: "declared"}
	sourceless := completeDescriptor("sourceless")
	sourceless.SourceURL = ""
	unnamed := completeDescriptor("declared-name")
	unnamed.Name = "other-name"
	prompted := completeDescriptor("prompted")
	prompted.Completion = modelregistry.CompletionProfile{Mode: modelregistry.CompletionPrompt, FIMFormat: "starcoder", ContextTokens: 8192, Evidence: "declared"}

	for _, testCase := range []struct {
		key        string
		descriptor modelregistry.ModelDescriptor
	}{
		{"fim-entry", fim},
		{"embedding-entry", embedding},
		{"edit-entry", editor},
		{"sourceless", sourceless},
		{"declared-name", unnamed},
		{"prompted", prompted},
	} {
		err := modelregistry.ValidateDescriptor(testCase.key, testCase.descriptor)
		if err == nil {
			t.Fatalf("%s was accepted while incomplete for its claimed role", testCase.key)
		}
		if !strings.Contains(err.Error(), testCase.key) {
			t.Fatalf("%s failure does not name the model: %v", testCase.key, err)
		}
	}
}

func TestUnit_AnEntryCompleteForItsClaimedRoleIsAccepted(t *testing.T) {
	complete := completeDescriptor("complete")
	complete.Completion = modelregistry.CompletionProfile{Mode: modelregistry.CompletionFIM, FIMFormat: "starcoder", ContextTokens: 8192, Evidence: "declared"}
	complete.NextEdit = modelregistry.NextEditProfile{Format: "sweep-v1", ContextTokens: 8192, Evidence: "declared"}
	complete.Embedding = modelregistry.EmbeddingProfile{Pooling: "last", ContextTokens: 4096, Dimension: 1024, Normalize: true, Evidence: "declared"}
	if err := modelregistry.ValidateDescriptor(complete.Name, complete); err != nil {
		t.Fatal(err)
	}
	if !complete.SupportsCompletion() || !complete.SupportsNextEdit() || !complete.SupportsRetrieval() {
		t.Fatal("a validated role contract does not report its support")
	}
}
