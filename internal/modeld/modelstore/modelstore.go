// Package modelstore resolves model names to on-disk paths within a modeld
// node's models directory. It exists so a session request can omit Path: a
// remote node has no idea what filesystem layout the runtime that sent the
// request uses, so the node must resolve ModelName+Type against its own
// storage instead of trusting a caller-supplied path.
//
// Layout mirrors what the runtime's local catalog providers already scan
// (runtime/modelrepo/llama, runtime/modelrepo/openvino), so an existing local
// models directory works unchanged as a node's models dir:
//
//	<dir>/<name>/model.gguf              (llama)
//	<dir>/<name>/openvino_model.xml       (openvino, or openvino_language_model.xml)
package modelstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/contenox/runtime/internal/archiveutil"
	"github.com/contenox/runtime/internal/modeld/llama"
	"github.com/contenox/runtime/internal/transport"
)

// DefaultSubdir is the models directory name under a data root when no
// explicit override is configured.
const DefaultSubdir = "models"

var openvinoEntrypoints = []string{"openvino_model.xml", "openvino_language_model.xml"}

// These alias the transport package's canonical sentinels rather than
// defining new error values: model resolution errors surface both in-process
// (slot.Service callers checking modelstore.ErrModelNotFound directly) and
// across the gRPC wire (Admin.ReceiveModel's errors, encoded/decoded via
// transport/grpc's sentinel table). A local, unrelated error value here would
// lose its identity crossing the wire.
var (
	// ErrModelNotFound is returned when no model matching the requested
	// name+type exists in the models directory.
	ErrModelNotFound = transport.ErrModelNotFound
	// ErrDigestMismatch is returned when a caller-supplied digest does not
	// match the on-disk content. Only enforced when the caller supplies a
	// non-empty digest to verify against.
	ErrDigestMismatch = transport.ErrDigestMismatch
	// ErrUnsupportedType is returned for a backend type this package does not
	// know how to resolve.
	ErrUnsupportedType = transport.ErrUnsupportedModelType
)

// Dir resolves the models directory: an explicit override if non-empty,
// otherwise <dataRoot>/<DefaultSubdir>.
func Dir(dataRoot, override string) string {
	if override != "" {
		return override
	}
	return filepath.Join(dataRoot, DefaultSubdir)
}

// Resolve finds the on-disk path for a model by name and backend type within
// dir. When wantDigest is non-empty, the resolved artifact's content digest is compared and a
// mismatch is rejected — this guards against a stale or wrong file answering
// under a name the caller expects to be a specific model.
// OpenVINO identity covers regular files and their relative names throughout the bundle.
func Resolve(dir, name, backendType, wantDigest string) (path string, err error) {
	if err := validateModelName(name); err != nil {
		return "", err
	}
	modelDir := filepath.Join(dir, name)

	switch backendType {
	case "llama":
		path = filepath.Join(modelDir, "model.gguf")
		if info, statErr := os.Stat(path); os.IsNotExist(statErr) {
			return "", fmt.Errorf("%w: %s (llama)", ErrModelNotFound, name)
		} else if statErr != nil {
			return "", statErr
		} else if !info.Mode().IsRegular() {
			return "", fmt.Errorf("model %s has no regular llama artifact", name)
		}
		if wantDigest != "" {
			got, digestErr := ArtifactDigest(path, backendType)
			if digestErr != nil {
				return "", digestErr
			}
			if got != wantDigest {
				return "", fmt.Errorf("%w: model %s want=%s got=%s", ErrDigestMismatch, name, wantDigest, got)
			}
		}
		return path, nil

	case "openvino":
		for _, entry := range openvinoEntrypoints {
			if _, statErr := os.Stat(filepath.Join(modelDir, entry)); statErr == nil {
				if wantDigest != "" {
					got, err := ArtifactDigest(modelDir, backendType)
					if err != nil {
						return "", err
					}
					if got != wantDigest {
						return "", fmt.Errorf("%w: model %s want=%s got=%s", ErrDigestMismatch, name, wantDigest, got)
					}
				}
				return modelDir, nil
			} else if !os.IsNotExist(statErr) {
				return "", statErr
			}
		}
		return "", fmt.Errorf("%w: %s (openvino)", ErrModelNotFound, name)

	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedType, backendType)
	}
}

// ResolveMMProj returns the optional multimodal projector installed next to a
// resolved llama model path (see llama.MMProjFilename), or "" when the model
// has none. ArtifactDigest includes an installed projector in bundle identity;
// vision execution capability is certified separately by the backend's Describe.
func ResolveMMProj(modelPath string) string {
	return llama.MMProjPathFor(modelPath)
}

// ArtifactDigest identifies the complete native artifact. Single-file llama models
// retain their file SHA256; vision models and OpenVINO use the bundle tree digest.
func ArtifactDigest(path, backend string) (string, error) {
	switch backend {
	case "openvino":
		return DirectoryDigest(path)
	case "llama":
		dir := filepath.Dir(path)
		if _, err := os.Lstat(filepath.Join(dir, llama.MMProjFilename)); err == nil {
			return DirectoryDigest(dir)
		} else if !os.IsNotExist(err) {
			return "", err
		}
		return FileDigest(path)
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedType, backend)
	}
}

// FileDigest returns the hex-encoded sha256 digest of a file's content.
func FileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("modelstore: open %s: %w", path, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("modelstore: hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// DirectoryDigest hashes relative file names and content in deterministic order.
// Bundle inputs must be regular files so symbolic links cannot escape the recorded artifact.
func DirectoryDigest(dir string) (string, error) {
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("model bundle contains a nonregular input: %s", path)
		}
		relative, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		digest, err := FileDigest(path)
		if err != nil {
			return err
		}
		return encoder.Encode([2]string{filepath.ToSlash(relative), digest})
	})
	if err != nil {
		return "", fmt.Errorf("modelstore: hash bundle: %w", err)
	}
	return "tree-sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func validateModelName(name string) error {
	if name == "" || name == "." || name == ".." || strings.HasPrefix(name, archiveutil.PreviousDirectoryPrefix) || strings.ContainsAny(name, "/\\:\x00") || !filepath.IsLocal(name) {
		return fmt.Errorf("%w: invalid model name %q", ErrModelNotFound, name)
	}
	return nil
}
