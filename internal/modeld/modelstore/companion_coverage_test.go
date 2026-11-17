package modelstore_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/runtime/modelregistry"
)

func curatedCatalog(t *testing.T) []modelregistry.ModelDescriptor {
	t.Helper()
	entries, err := modelregistry.NewCurated().List(context.Background())
	if err != nil {
		t.Fatalf("list the curated catalog: %v", err)
	}
	return entries
}

func publishedCompanions(t *testing.T) (map[string]modelstore.CompanionAsset, func(string) ([]byte, error)) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("assets", modelstore.CompanionIndexFilename))
	if err != nil {
		t.Fatalf("read the published companion index: %v", err)
	}
	index, err := modelstore.ReadCompanionIndex(data)
	if err != nil {
		t.Fatalf("decode the published companion index: %v", err)
	}
	return index, func(id string) ([]byte, error) { return os.ReadFile(filepath.Join("assets", id+".tar.gz")) }
}

func curatedRequirements(t *testing.T) []modelstore.CompanionRequirement {
	t.Helper()
	requirements, err := modelstore.CompanionRequirements(curatedCatalog(t))
	if err != nil {
		t.Fatalf("resolve the curated companion requirements: %v", err)
	}
	if len(requirements) == 0 {
		t.Fatal("the curated catalog declares no tokenizer companion, so these checks would prove nothing")
	}
	return requirements
}

func TestUnit_EveryCuratedNativeOperationPublishesItsTokenizerCompanion(t *testing.T) {
	index, archive := publishedCompanions(t)
	curatedRequirements(t)
	if err := modelstore.VerifyCompanionCoverage(curatedCatalog(t), index, archive); err != nil {
		t.Fatal(err)
	}
}

func TestUnit_AModelWithoutADeclaredCompanionNamesItsRebuildCommand(t *testing.T) {
	index, archive := publishedCompanions(t)
	for _, requirement := range curatedRequirements(t) {
		stripped := requirement.Model
		stripped.TokenizerCompanionID = ""
		err := modelstore.VerifyCompanionCoverage([]modelregistry.ModelDescriptor{stripped}, index, archive)
		if err == nil {
			t.Fatalf("model %s passed coverage without a declared companion", stripped.Name)
		}
		if !strings.Contains(err.Error(), stripped.Name) || !strings.Contains(err.Error(), "modeld:companion") {
			t.Fatalf("model %s failure names neither the model nor its rebuild command: %v", stripped.Name, err)
		}
	}
}

func TestUnit_AMissingCompanionArchiveNamesTheModelAndItsRebuildCommand(t *testing.T) {
	index, _ := publishedCompanions(t)
	for _, requirement := range curatedRequirements(t) {
		err := modelstore.VerifyCompanionCoverage([]modelregistry.ModelDescriptor{requirement.Model}, index, func(string) ([]byte, error) {
			return nil, os.ErrNotExist
		})
		if err == nil || !strings.Contains(err.Error(), requirement.Model.Name) || !strings.Contains(err.Error(), "modeld:companion") {
			t.Fatalf("a missing companion archive for %s reported %v", requirement.Model.Name, err)
		}
	}
}

func TestUnit_AStaleOrUnpinnedCompanionIsRejected(t *testing.T) {
	index, archive := publishedCompanions(t)
	for id := range index {
		data, err := archive(id)
		if err != nil {
			t.Fatalf("read companion %s: %v", id, err)
		}
		if _, err := modelstore.ValidateCompanionArchive("some-other-asset", data); err == nil {
			t.Fatalf("companion %s validated under a different asset ID", id)
		}
		if _, err := modelstore.ValidateCompanionArchive(id, data[:len(data)/2]); err == nil {
			t.Fatalf("companion %s validated after truncation", id)
		}
		stale := index[id]
		stale.ArchiveSHA256 = strings.Repeat("0", 64)
		staleIndex := map[string]modelstore.CompanionAsset{id: stale}
		if err := modelstore.VerifyCompanionCoverage(curatedCatalog(t), staleIndex, archive); err == nil {
			t.Fatalf("companion %s passed coverage with a stale digest", id)
		}
	}
}

func TestUnit_AnUnrequiredCompanionIsRejected(t *testing.T) {
	index, archive := publishedCompanions(t)
	orphan := map[string]modelstore.CompanionAsset{}
	for id, asset := range index {
		orphan[id] = asset
	}
	orphan["not-required-by-any-model"] = modelstore.CompanionAsset{Archive: "not-required-by-any-model.tar.gz"}
	if err := modelstore.VerifyCompanionCoverage(curatedCatalog(t), orphan, archive); err == nil ||
		!strings.Contains(err.Error(), "not-required-by-any-model") {
		t.Fatalf("an unrequired published companion reported %v", err)
	}
}
