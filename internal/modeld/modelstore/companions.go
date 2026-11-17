package modelstore

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/contenox/runtime/internal/archiveutil"
	"github.com/contenox/runtime/internal/modeld/openvino/ovsession"
	"github.com/contenox/runtime/runtime/modelcontract"
	"github.com/contenox/runtime/runtime/modelregistry"
)

// CompanionIndexFilename is the embedded companion index beside the companion archives.
const CompanionIndexFilename = "index.json"

// CompanionAsset publishes one companion archive for one model repository revision.
type CompanionAsset struct {
	Archive       string `json:"archive"`
	ArchiveSHA256 string `json:"archiveSha256"`
	ModelRepo     string `json:"modelRepo"`
	ModelRevision string `json:"modelRevision"`
	Kind          string `json:"kind"`
	Format        string `json:"format"`
}

// CompanionManifest is the format identity a companion archive records for its model.
type CompanionManifest struct {
	Version                 int      `json:"version"`
	Kind                    string   `json:"kind"`
	Format                  string   `json:"format"`
	FormatVersion           int      `json:"format_version"`
	SourceTokenizerSHA256   string   `json:"source_tokenizer_sha256"`
	ControlTokens           []string `json:"control_tokens"`
	PreparationScriptSHA256 string   `json:"preparation_script_sha256"`
}

// CompanionRequirement is the tokenizer companion one curated model declares.
type CompanionRequirement struct {
	Model    modelregistry.ModelDescriptor
	AssetID  string
	Kind     string
	Format   string
	Version  int
	Controls []string
}

// CompanionRebuildCommand is the command that rebuilds one model's companion from its pinned snapshot.
func CompanionRebuildCommand(model modelregistry.ModelDescriptor) string {
	source := model.Repo
	if model.Revision != "" {
		source = model.Repo + "@" + model.Revision
	}
	return fmt.Sprintf("task -d runtime modeld:companion MODEL=%s MODEL_DIR=<pinned %s snapshot>", model.Name, source)
}

// CompanionRequirements returns the companion every curated OpenVINO model with a
// completion or edit contract declares, sourced from the curated catalog rather
// than from a second list.
func CompanionRequirements(entries []modelregistry.ModelDescriptor) ([]CompanionRequirement, error) {
	ordered := append([]modelregistry.ModelDescriptor{}, entries...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Name < ordered[j].Name })
	var requirements []CompanionRequirement
	for _, entry := range ordered {
		kind, format, required := literalSourceOperation(entry)
		if !required {
			if entry.TokenizerCompanionID != "" {
				return nil, fmt.Errorf("model %s declares tokenizer companion %q without a native FIM or Next Edit contract; remove the declaration", entry.Name, entry.TokenizerCompanionID)
			}
			continue
		}
		version, controls, err := modelcontract.TokenizerCompanionControls(kind, format)
		if err != nil {
			return nil, fmt.Errorf("model %s: %w", entry.Name, err)
		}
		if entry.TokenizerCompanionID == "" {
			return nil, fmt.Errorf("model %s has a native %s format %q and no tokenizer companion; declare its TokenizerCompanionID, then rebuild with: %s", entry.Name, kind, format, CompanionRebuildCommand(entry))
		}
		requirements = append(requirements, CompanionRequirement{Model: entry, AssetID: entry.TokenizerCompanionID, Kind: kind, Format: format, Version: version, Controls: controls})
	}
	return requirements, nil
}

// CompanionIndex derives the published index for every companion the curated catalog requires.
func CompanionIndex(entries []modelregistry.ModelDescriptor, archive func(id string) ([]byte, error)) (map[string]CompanionAsset, error) {
	requirements, err := CompanionRequirements(entries)
	if err != nil {
		return nil, err
	}
	index := make(map[string]CompanionAsset, len(requirements))
	for _, requirement := range requirements {
		data, err := archive(requirement.AssetID)
		if err != nil {
			return nil, fmt.Errorf("model %s companion %q: %w; rebuild it with: %s", requirement.Model.Name, requirement.AssetID, err, CompanionRebuildCommand(requirement.Model))
		}
		manifest, err := ValidateCompanionArchive(requirement.AssetID, data)
		if err != nil {
			return nil, fmt.Errorf("model %s companion %q: %w; rebuild it with: %s", requirement.Model.Name, requirement.AssetID, err, CompanionRebuildCommand(requirement.Model))
		}
		if manifest.Kind != requirement.Kind || manifest.Format != requirement.Format {
			return nil, fmt.Errorf("model %s companion %q declares %s format %q instead of %s format %q; rebuild it with: %s", requirement.Model.Name, requirement.AssetID, manifest.Kind, manifest.Format, requirement.Kind, requirement.Format, CompanionRebuildCommand(requirement.Model))
		}
		sum := sha256.Sum256(data)
		index[requirement.AssetID] = CompanionAsset{
			Archive: requirement.AssetID + ".tar.gz", ArchiveSHA256: hex.EncodeToString(sum[:]),
			ModelRepo: requirement.Model.Repo, ModelRevision: requirement.Model.Revision,
			Kind: requirement.Kind, Format: requirement.Format,
		}
	}
	return index, nil
}

// VerifyCompanionCoverage fails when a published index does not match the catalog's companions.
func VerifyCompanionCoverage(entries []modelregistry.ModelDescriptor, index map[string]CompanionAsset, archive func(id string) ([]byte, error)) error {
	expected, err := CompanionIndex(entries, archive)
	if err != nil {
		return err
	}
	for id, asset := range expected {
		published, found := index[id]
		if !found {
			return fmt.Errorf("companion %q is not published in the embedded %s", id, CompanionIndexFilename)
		}
		if !reflect.DeepEqual(published, asset) {
			return fmt.Errorf("companion %q is published as %+v but the catalog resolves %+v", id, published, asset)
		}
	}
	for id := range index {
		if _, found := expected[id]; !found {
			return fmt.Errorf("companion %q is published but no curated model requires it", id)
		}
	}
	return nil
}

// ReadCompanionIndex decodes a companion index.
func ReadCompanionIndex(data []byte) (map[string]CompanionAsset, error) {
	var index map[string]CompanionAsset
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("decode companion index: %w", err)
	}
	return index, nil
}

// RenderCompanionIndex renders a companion index in its published form.
func RenderCompanionIndex(index map[string]CompanionAsset) ([]byte, error) {
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode companion index: %w", err)
	}
	return append(data, '\n'), nil
}

// ValidateCompanionArchive verifies one companion archive against its asset ID and the format schema.
func ValidateCompanionArchive(id string, archive []byte) (CompanionManifest, error) {
	if id == "" || strings.ContainsAny(id, `/\\:`) || id == "." || id == ".." {
		return CompanionManifest{}, errors.New("companion asset ID is invalid")
	}
	stage, err := os.MkdirTemp("", "contenox-companion-verify-*")
	if err != nil {
		return CompanionManifest{}, err
	}
	defer os.RemoveAll(stage)
	compressed, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return CompanionManifest{}, fmt.Errorf("open archive: %w", err)
	}
	extractErr := archiveutil.ExtractTar(compressed, stage)
	closeErr := compressed.Close()
	if extractErr != nil {
		return CompanionManifest{}, fmt.Errorf("extract archive: %w", extractErr)
	}
	if closeErr != nil {
		return CompanionManifest{}, fmt.Errorf("close archive: %w", closeErr)
	}
	if err := validateTokenizerCompanionTree(stage); err != nil {
		return CompanionManifest{}, fmt.Errorf("archive contents are invalid: %w", err)
	}
	manifestData, err := os.ReadFile(filepath.Join(stage, ovsession.LiteralSourceTokenizerManifestFilename))
	if err != nil {
		return CompanionManifest{}, err
	}
	var manifest CompanionManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return CompanionManifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	version, controls, err := modelcontract.TokenizerCompanionControls(manifest.Kind, manifest.Format)
	if err != nil {
		return CompanionManifest{}, err
	}
	if manifest.Version != 1 || manifest.FormatVersion != version || !equalCompanionStrings(manifest.ControlTokens, controls) {
		return CompanionManifest{}, fmt.Errorf("manifest declares %s format %q version %d with controls %v; the format schema declares version %d with controls %v", manifest.Kind, manifest.Format, manifest.FormatVersion, manifest.ControlTokens, version, controls)
	}
	if !pinnedDigest(manifest.SourceTokenizerSHA256) {
		return CompanionManifest{}, errors.New("manifest pins no source tokenizer digest")
	}
	if !pinnedDigest(manifest.PreparationScriptSHA256) {
		return CompanionManifest{}, errors.New("manifest pins no preparation script digest")
	}
	provenanceData, err := os.ReadFile(filepath.Join(stage, ovsession.LiteralSourceTokenizerDirectoryName, "PROVENANCE.json"))
	if err != nil {
		return CompanionManifest{}, err
	}
	var provenance struct {
		AssetID string `json:"asset_id"`
	}
	if err := json.Unmarshal(provenanceData, &provenance); err != nil {
		return CompanionManifest{}, fmt.Errorf("decode provenance: %w", err)
	}
	if provenance.AssetID != id {
		return CompanionManifest{}, fmt.Errorf("provenance asset ID is %q instead of %q", provenance.AssetID, id)
	}
	return manifest, nil
}

// ReadCompanionArchiveManifest decodes the manifest of an archive whose contents are already validated.
func ReadCompanionArchiveManifest(archive []byte) (CompanionManifest, error) {
	compressed, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return CompanionManifest{}, fmt.Errorf("open archive: %w", err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return CompanionManifest{}, errors.New("archive carries no companion manifest")
		}
		if err != nil {
			return CompanionManifest{}, fmt.Errorf("read archive: %w", err)
		}
		if header.Name != ovsession.LiteralSourceTokenizerManifestFilename {
			continue
		}
		var manifest CompanionManifest
		if err := json.NewDecoder(reader).Decode(&manifest); err != nil {
			return CompanionManifest{}, fmt.Errorf("decode manifest: %w", err)
		}
		return manifest, nil
	}
}

func pinnedDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func equalCompanionStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
