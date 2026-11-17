package modelstore

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/contenox/runtime/internal/archiveutil"
	"github.com/contenox/runtime/internal/modeld/openvino/ovsession"
	"github.com/contenox/runtime/runtime/modelregistry"
)

//go:embed assets
var tokenizerCompanionAssets embed.FS

type tokenizerCompanionDescriptor = CompanionAsset

func stageTokenizerCompanion(ctx context.Context, modelDir string, desc modelregistry.ModelDescriptor) error {
	kind, format, required := literalSourceOperation(desc)
	if !required && desc.TokenizerCompanionID == "" {
		return nil
	}
	if !required || desc.TokenizerCompanionID == "" {
		return errors.New("model tokenizer companion requires an explicit OpenVINO FIM or Next Edit contract and asset ID")
	}
	asset, err := readTokenizerCompanionDescriptor(desc.TokenizerCompanionID)
	if err != nil {
		return err
	}
	if asset.ModelRepo != desc.Repo || asset.ModelRevision != desc.Revision || asset.Kind != kind || asset.Format != format {
		return fmt.Errorf("tokenizer companion %q does not match model repository revision or %s format %q", desc.TokenizerCompanionID, kind, format)
	}
	archivePath := filepath.ToSlash(filepath.Join("assets", asset.Archive))
	if !fs.ValidPath(archivePath) || filepath.Base(asset.Archive) != asset.Archive || !strings.HasSuffix(asset.Archive, ".tar.gz") {
		return fmt.Errorf("tokenizer companion %q has an invalid archive path", desc.TokenizerCompanionID)
	}
	archive, err := tokenizerCompanionAssets.ReadFile(archivePath)
	if err != nil {
		return fmt.Errorf("read tokenizer companion %q: %w", desc.TokenizerCompanionID, err)
	}
	digest := sha256.Sum256(archive)
	if hex.EncodeToString(digest[:]) != asset.ArchiveSHA256 {
		return fmt.Errorf("tokenizer companion %q archive digest mismatch", desc.TokenizerCompanionID)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	stageDir, err := os.MkdirTemp(modelDir, ".tokenizer-companion-*")
	if err != nil {
		return fmt.Errorf("create tokenizer companion staging directory: %w", err)
	}
	defer os.RemoveAll(stageDir)
	compressed, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return fmt.Errorf("open tokenizer companion %q archive: %w", desc.TokenizerCompanionID, err)
	}
	extractErr := archiveutil.ExtractTar(compressed, stageDir)
	closeErr := compressed.Close()
	if extractErr != nil {
		return fmt.Errorf("extract tokenizer companion %q: %w", desc.TokenizerCompanionID, extractErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close tokenizer companion %q archive: %w", desc.TokenizerCompanionID, closeErr)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateTokenizerCompanionTree(stageDir); err != nil {
		return fmt.Errorf("tokenizer companion %q contents are invalid: %w", desc.TokenizerCompanionID, err)
	}
	manifestName := ovsession.LiteralSourceTokenizerManifestFilename
	companionName := ovsession.LiteralSourceTokenizerDirectoryName
	manifestTarget := filepath.Join(modelDir, manifestName)
	companionTarget := filepath.Join(modelDir, companionName)
	for _, target := range []string{manifestTarget, companionTarget} {
		if _, err := os.Lstat(target); err == nil {
			return fmt.Errorf("tokenizer companion target %q already exists in staged model", filepath.Base(target))
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(filepath.Join(stageDir, manifestName), manifestTarget); err != nil {
		return fmt.Errorf("stage tokenizer companion manifest: %w", err)
	}
	if err := os.Rename(filepath.Join(stageDir, companionName), companionTarget); err != nil {
		return fmt.Errorf("stage tokenizer companion files: %w", err)
	}
	if err := ovsession.ValidateLiteralSourceTokenizerForOperation(modelDir, companionTarget, kind, format); err != nil {
		return fmt.Errorf("validate tokenizer companion %q: %w", desc.TokenizerCompanionID, err)
	}
	return nil
}

func readTokenizerCompanionDescriptor(id string) (tokenizerCompanionDescriptor, error) {
	if id == "" || strings.ContainsAny(id, `/\\:`) || id == "." || id == ".." {
		return tokenizerCompanionDescriptor{}, errors.New("tokenizer companion asset ID is invalid")
	}
	indexData, err := tokenizerCompanionAssets.ReadFile("assets/index.json")
	if err != nil {
		return tokenizerCompanionDescriptor{}, fmt.Errorf("read embedded tokenizer companion index: %w", err)
	}
	var index map[string]tokenizerCompanionDescriptor
	if err := json.Unmarshal(indexData, &index); err != nil {
		return tokenizerCompanionDescriptor{}, fmt.Errorf("decode embedded tokenizer companion index: %w", err)
	}
	asset, found := index[id]
	if !found {
		return tokenizerCompanionDescriptor{}, fmt.Errorf("tokenizer companion %q is not bundled; repair the model registry entry", id)
	}
	if asset.Archive == "" || asset.ArchiveSHA256 == "" || asset.ModelRepo == "" || asset.ModelRevision == "" || asset.Kind == "" || asset.Format == "" {
		return tokenizerCompanionDescriptor{}, fmt.Errorf("tokenizer companion %q embedded descriptor is incomplete", id)
	}
	return asset, nil
}

func validateTokenizerCompanionTree(root string) error {
	allowedFiles := map[string]bool{
		ovsession.LiteralSourceTokenizerManifestFilename:                                       true,
		filepath.Join(ovsession.LiteralSourceTokenizerDirectoryName, "tokenizer.json"):         true,
		filepath.Join(ovsession.LiteralSourceTokenizerDirectoryName, "tokenizer_config.json"):  true,
		filepath.Join(ovsession.LiteralSourceTokenizerDirectoryName, "openvino_tokenizer.xml"): true,
		filepath.Join(ovsession.LiteralSourceTokenizerDirectoryName, "openvino_tokenizer.bin"): true,
		filepath.Join(ovsession.LiteralSourceTokenizerDirectoryName, "LICENSE"):                true,
		filepath.Join(ovsession.LiteralSourceTokenizerDirectoryName, "PROVENANCE.json"):        true,
	}
	allowedDirectories := map[string]bool{ovsession.LiteralSourceTokenizerDirectoryName: true}
	found := map[string]bool{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if !allowedDirectories[relative] {
				return fmt.Errorf("unexpected directory %q", relative)
			}
			return nil
		}
		if !entry.Type().IsRegular() || !allowedFiles[relative] {
			return fmt.Errorf("unexpected or non-regular companion file %q", relative)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() == 0 {
			return fmt.Errorf("companion file %q is empty", relative)
		}
		found[relative] = true
		return nil
	}); err != nil {
		return err
	}
	for name := range allowedFiles {
		if !found[name] {
			return fmt.Errorf("required file %q is missing", name)
		}
	}
	return nil
}
