package modelstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/contenox/runtime/internal/archiveutil"
	"github.com/contenox/runtime/internal/modeld/llama"
	"github.com/contenox/runtime/internal/modeld/openvino"
	"github.com/contenox/runtime/internal/modeld/openvino/ovsession"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelregistry"
)

var (
	pullMu       sync.Mutex
	pullInFlight = map[string]*modelPull{}
)

type modelPull struct {
	done chan struct{}
	err  error
}

// ModelReceiver installs a complete artifact through its owner's mutation guard.
type ModelReceiver func(context.Context, transport.PushManifest, io.Reader) (transport.PushResult, error)

// AutoPullOptions carries options for EnsureModelAvailable.
type AutoPullOptions struct {
	DataRoot    string
	Receive     ModelReceiver
	ProgressOut io.Writer
	Progress    func(DownloadProgress) error
	Tracker     libtracker.ActivityTracker
}

// DownloadProgress is the byte position of one model artifact download.
type DownloadProgress struct {
	Artifact   string `json:"-"`
	Downloaded int64
	Total      int64
}

// EnsureModelAvailable stages every required asset before submitting the complete
// bundle to its owner. Existing assignments and artifacts survive download failure.
func EnsureModelAvailable(ctx context.Context, reg modelregistry.Registry, modelName string, opts AutoPullOptions) (err error) {
	if err := validateModelName(modelName); err != nil {
		return err
	}
	if ctx == nil || reg == nil || opts.Receive == nil || opts.DataRoot == "" {
		return errors.New("model acquisition requires context, registry, data root and owner receiver")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	modelsDir, err := filepath.Abs(Dir(opts.DataRoot, ""))
	if err != nil {
		return err
	}
	key := filepath.Join(modelsDir, modelName)
	pullMu.Lock()
	if pull, busy := pullInFlight[key]; busy {
		pullMu.Unlock()
		select {
		case <-pull.done:
			return pull.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	pull := &modelPull{done: make(chan struct{})}
	pullInFlight[key] = pull
	pullMu.Unlock()
	defer func() {
		pullMu.Lock()
		pull.err = err
		delete(pullInFlight, key)
		close(pull.done)
		pullMu.Unlock()
	}()
	desc, err := reg.Resolve(ctx, modelName)
	if errors.Is(err, modelregistry.ErrNotFound) {
		for _, backend := range []string{"llama", "openvino"} {
			if _, resolveErr := Resolve(modelsDir, modelName, backend, ""); resolveErr == nil {
				return nil
			} else if !errors.Is(resolveErr, ErrModelNotFound) {
				return resolveErr
			}
		}
		return fmt.Errorf("%w: %s (not found in curated or local registry)", ErrModelNotFound, modelName)
	}
	if err != nil {
		return err
	}
	if desc == nil || desc.Name != modelName {
		return errors.New("model registry returned a different or missing artifact; select the intended model")
	}
	if err := requiredAssets(modelsDir, *desc); err == nil {
		return nil
	} else if !errors.Is(err, ErrModelNotFound) && !errors.Is(err, ErrDigestMismatch) {
		return err
	}
	admin := NewAdmin(modelsDir)
	stats, err := admin.DiskStats(ctx)
	if err != nil {
		return err
	}
	required := 2 * (desc.SizeBytes + desc.MMProjSizeBytes)
	if required > 0 && stats.FreeBytes < required+(500*1024*1024) {
		return fmt.Errorf("insufficient disk space for download and owner staging: need %d bytes plus 500 MiB, have %d free bytes", required, stats.FreeBytes)
	}
	out := opts.ProgressOut
	if out == nil {
		out = io.Discard
	}
	destDir, err := os.MkdirTemp(modelsDir, stagingPrefix+modelName+"-download-*")
	if err != nil {
		return fmt.Errorf("create download staging directory: %w", err)
	}
	defer os.RemoveAll(destDir)
	modelBackend := desc.BackendType()
	if modelBackend == "openvino" {
		if desc.Repo == "" {
			return fmt.Errorf("openvino model %q has no source repo in registry", modelName)
		}
		if _, err := fmt.Fprintf(out, "Downloading OpenVINO model %q...\n", modelName); err != nil {
			return err
		}
		if err := DownloadOpenVINOIR(ctx, desc.Repo, desc.Revision, destDir, out, opts.Progress); err != nil {
			return fmt.Errorf("auto-pull openvino model %q: %w", modelName, err)
		}
		if err := stageTokenizerCompanion(ctx, destDir, *desc); err != nil {
			return fmt.Errorf("acquire model %q tokenizer companion: %w", modelName, err)
		}
	} else if modelBackend == "llama" {
		if desc.SourceURL == "" || (desc.Vision && desc.MMProjURL == "") {
			return fmt.Errorf("model %q is missing a required model or vision-projector source; repair its registry entry", modelName)
		}
		destFile := filepath.Join(destDir, "model.gguf")
		if _, err := fmt.Fprintf(out, "Downloading model %q...\n", modelName); err != nil {
			return err
		}
		if err := downloadFile(ctx, desc.SourceURL, destFile, out, opts.Progress); err != nil {
			return fmt.Errorf("auto-pull model %q: %w", modelName, err)
		}

		if desc.Vision && desc.MMProjURL != "" {
			destMMProj := filepath.Join(destDir, "mmproj.gguf")
			if err := downloadFile(ctx, desc.MMProjURL, destMMProj, out, opts.Progress); err != nil {
				return fmt.Errorf("acquire model %q: required vision projector: %w", modelName, err)
			}
		}
		if desc.ChatTemplateURL != "" {
			destTemplate := filepath.Join(destDir, llama.ChatTemplateFilename)
			if err := downloadChatTemplate(ctx, desc.ChatTemplateURL, destTemplate, out, opts.Progress); err != nil {
				return fmt.Errorf("auto-pull model %q: chat template: %w", modelName, err)
			}
		}
	} else {
		return fmt.Errorf("%w: %s", ErrUnsupportedType, modelBackend)
	}
	if desc.ToolProtocol != "" || desc.ReasoningProtocol != "" {
		if err := WriteModelProfile(modelBackend, destDir, desc.ToolProtocol, desc.ReasoningProtocol, desc.ReasoningFormat); err != nil {
			return fmt.Errorf("write required model profile: %w", err)
		}
	}
	staged := *desc
	staged.Name = filepath.Base(destDir)
	if err := requiredAssets(modelsDir, staged); err != nil {
		return err
	}
	if err := receiveStaged(ctx, modelName, modelBackend, destDir, opts.Receive); err != nil {
		return err
	}
	return nil
}

func requiredAssets(modelsDir string, desc modelregistry.ModelDescriptor) error {
	path, err := Resolve(modelsDir, desc.Name, desc.BackendType(), "")
	if err != nil {
		return err
	}
	dir := filepath.Join(modelsDir, desc.Name)
	var required []string
	switch desc.BackendType() {
	case "llama":
		required = append(required, filepath.Base(path))
		if desc.SourceSHA256 != "" {
			digest, err := FileDigest(path)
			if err != nil {
				return err
			}
			if digest != desc.SourceSHA256 {
				return fmt.Errorf("%w: %s does not match its reviewed download; acquire its complete bundle", ErrDigestMismatch, desc.Name)
			}
		}
		if desc.Vision {
			required = append(required, llama.MMProjFilename)
		}
		if desc.ChatTemplateURL != "" {
			required = append(required, llama.ChatTemplateFilename)
		}
	case "openvino":
		kind, format, companionRequired := literalSourceOperation(desc)
		if companionRequired {
			if desc.TokenizerCompanionID == "" {
				return fmt.Errorf("%w: %s requires a bundled tokenizer companion for %s format %q; repair its registry entry", ErrModelNotFound, desc.Name, kind, format)
			}
			companionDir := filepath.Join(dir, ovsession.LiteralSourceTokenizerDirectoryName)
			if err := ovsession.ValidateLiteralSourceTokenizerForOperation(dir, companionDir, kind, format); err != nil {
				return fmt.Errorf("%w: %s tokenizer companion %q is missing or incompatible: %w", ErrModelNotFound, desc.Name, desc.TokenizerCompanionID, err)
			}
			if err := requireRegularFile(filepath.Join(companionDir, "LICENSE")); err != nil {
				return fmt.Errorf("%w: %s tokenizer companion %q license is missing: %w", ErrModelNotFound, desc.Name, desc.TokenizerCompanionID, err)
			}
			provenancePath := filepath.Join(companionDir, "PROVENANCE.json")
			if err := requireRegularFile(provenancePath); err != nil {
				return fmt.Errorf("%w: %s tokenizer companion %q provenance is missing: %w", ErrModelNotFound, desc.Name, desc.TokenizerCompanionID, err)
			}
			if err := validateTokenizerCompanionProvenance(provenancePath, desc.TokenizerCompanionID); err != nil {
				return fmt.Errorf("%w: %s tokenizer companion %q provenance is incompatible: %w", ErrModelNotFound, desc.Name, desc.TokenizerCompanionID, err)
			}
		} else if desc.TokenizerCompanionID != "" {
			return fmt.Errorf("%w: %s declares tokenizer companion %q without a native FIM or Next Edit contract", ErrModelNotFound, desc.Name, desc.TokenizerCompanionID)
		}
		if desc.Vision {
			if err := openvino.ValidateVisionBundle(dir); err != nil {
				return fmt.Errorf("%w: %s vision bundle is incomplete: %w", ErrModelNotFound, desc.Name, err)
			}
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".xml") {
				required = append(required, entry.Name(), strings.TrimSuffix(entry.Name(), ".xml")+".bin")
			}
		}
	}
	if desc.ToolProtocol != "" || desc.ReasoningProtocol != "" {
		required = append(required, "contenox-"+desc.BackendType()+".json")
	}
	for _, name := range required {
		info, err := os.Lstat(filepath.Join(dir, name))
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s requires %s; acquire its complete bundle", ErrModelNotFound, desc.Name, name)
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("%w: %s requires a nonempty regular %s; acquire its complete bundle", ErrModelNotFound, desc.Name, name)
		}
	}
	return nil
}

func literalSourceOperation(desc modelregistry.ModelDescriptor) (string, string, bool) {
	if desc.BackendType() != "openvino" {
		return "", "", false
	}
	if desc.Completion.Mode == modelregistry.CompletionFIM {
		return "fim", desc.Completion.FIMFormat, true
	}
	if desc.SupportsNextEdit() {
		return "next-edit", desc.NextEdit.Format, true
	}
	return "", "", false
}

func requireRegularFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("%s is not a nonempty regular file", filepath.Base(path))
	}
	return nil
}

func validateTokenizerCompanionProvenance(path, id string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var provenance struct {
		AssetID string `json:"asset_id"`
	}
	if err := json.Unmarshal(data, &provenance); err != nil {
		return err
	}
	if provenance.AssetID != id {
		return fmt.Errorf("asset ID is %q", provenance.AssetID)
	}
	return nil
}

func receiveStaged(ctx context.Context, name, backend, dir string, receive ModelReceiver) error {
	hash := sha256.New()
	if err := archiveutil.WriteTar(ctx, dir, hash); err != nil {
		return err
	}
	manifest := transport.PushManifest{Name: name, Type: backend, Format: transport.PushFormatTar, Digest: hex.EncodeToString(hash.Sum(nil))}
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := archiveutil.WriteTar(ctx, dir, writer)
		_ = writer.CloseWithError(err)
		done <- err
	}()
	_, receiveErr := receive(ctx, manifest, reader)
	_ = reader.CloseWithError(receiveErr)
	return errors.Join(receiveErr, <-done)
}

// downloadChatTemplate fetches a model's chat template. Publishers ship it
// either as a template file or inside tokenizer_config.json, and both have to
// land as the template llama.cpp reads: a JSON body written verbatim would be
// handed to the Jinja engine as its program.
func downloadChatTemplate(ctx context.Context, srcURL, destPath string, progressW io.Writer, progress func(DownloadProgress) error) error {
	staged := destPath + ".part"
	if err := downloadFile(ctx, srcURL, staged, progressW, progress); err != nil {
		return err
	}
	defer func() { _ = os.Remove(staged) }()
	raw, err := os.ReadFile(staged)
	if err != nil {
		return err
	}
	template := extractChatTemplate(raw)
	if strings.TrimSpace(template) == "" {
		return fmt.Errorf("no chat template in the fetched %s", srcURL)
	}
	return os.WriteFile(destPath, []byte(template), 0o644)
}

// extractChatTemplate returns the template a fetched body carries: the body
// itself when it is a template, or the chat_template field of a tokenizer
// config. A config may also key templates by name, whose first entry is the one
// the model renders with.
func extractChatTemplate(raw []byte) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return ""
	}
	// A template may itself open with a brace (`{{ bos_token }}`), so this is a
	// JSON question only when the body parses as a tokenizer config object; a
	// body that does not is the template.
	var probe map[string]json.RawMessage
	if strings.HasPrefix(trimmed, "{") && json.Unmarshal(raw, &probe) == nil {
		return templateFromTokenizerConfig(raw)
	}
	return trimmed
}

func templateFromTokenizerConfig(raw []byte) string {
	var config struct {
		ChatTemplate json.RawMessage `json:"chat_template"`
	}
	if err := json.Unmarshal(raw, &config); err != nil || len(config.ChatTemplate) == 0 {
		return ""
	}
	var one string
	if err := json.Unmarshal(config.ChatTemplate, &one); err == nil {
		return one
	}
	var many map[string]string
	if err := json.Unmarshal(config.ChatTemplate, &many); err == nil {
		names := make([]string, 0, len(many))
		for name := range many {
			names = append(names, name)
		}
		sort.Strings(names)
		if len(names) > 0 {
			return many[names[0]]
		}
	}
	return ""
}

// DownloadFile downloads a file from srcURL to destPath using a temporary staging file.
func DownloadFile(ctx context.Context, srcURL, destPath string, progressW io.Writer) error {
	return downloadFile(ctx, srcURL, destPath, progressW, nil)
}

func downloadFile(ctx context.Context, srcURL, destPath string, progressW io.Writer, progress func(DownloadProgress) error) error {
	resp, err := modelDownload(ctx, srcURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	partPath := destPath + ".part"
	f, err := os.OpenFile(partPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(partPath)
	}()

	pw := &DownloadProgressWriter{
		Artifact: destPath,
		Total:    resp.ContentLength,
		Out:      progressW,
		Progress: progress,
	}

	tr := io.TeeReader(resp.Body, pw)
	if _, err := io.Copy(f, tr); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if progress != nil && pw.Downloaded != pw.Total {
		if err := progress(DownloadProgress{Artifact: destPath, Downloaded: pw.Downloaded, Total: pw.Downloaded}); err != nil {
			return err
		}
	}
	return os.Rename(partPath, destPath)
}

// DownloadProgressWriter writes download progress to Out.
type DownloadProgressWriter struct {
	Artifact   string
	Total      int64
	Downloaded int64
	LastPrint  time.Time
	Out        io.Writer
	Progress   func(DownloadProgress) error
}

func (pw *DownloadProgressWriter) Write(p []byte) (int, error) {
	n := len(p)
	pw.Downloaded += int64(n)
	now := time.Now()
	if now.Sub(pw.LastPrint) >= 250*time.Millisecond || pw.Downloaded == pw.Total {
		pw.LastPrint = now
		if pw.Progress != nil {
			if err := pw.Progress(DownloadProgress{Artifact: pw.Artifact, Downloaded: pw.Downloaded, Total: pw.Total}); err != nil {
				return n, err
			}
		}
		if pw.Out != nil {
			if pw.Total > 0 {
				pct := float64(pw.Downloaded) / float64(pw.Total) * 100
				if _, err := fmt.Fprintf(pw.Out, "\rDownloading: %.1f%% (%s / %s)...", pct, FormatSize(pw.Downloaded), FormatSize(pw.Total)); err != nil {
					return n, err
				}
			} else {
				if _, err := fmt.Fprintf(pw.Out, "\rDownloading: %s...", FormatSize(pw.Downloaded)); err != nil {
					return n, err
				}
			}
		}
	}
	return n, nil
}

// FormatSize formats a byte count into a human-readable string.
func FormatSize(n int64) string {
	if n <= 0 {
		return "0 B"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n >= div*unit && exp < 3 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}

// WriteModelProfile writes local parsing protocols to a fresh profile JSON.
func WriteModelProfile(modelBackend, modelDir, toolProtocol, reasoningProtocol, reasoningFormat string) error {
	var fileName string
	switch strings.ToLower(strings.TrimSpace(modelBackend)) {
	case "llama":
		fileName = "contenox-llama.json"
	case "openvino":
		fileName = "contenox-openvino.json"
	default:
		return nil
	}

	path := filepath.Join(modelDir, fileName)
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	profile := map[string]any{}
	if toolProtocol != "" {
		profile["tool_calls"] = map[string]any{"protocol": toolProtocol}
	}
	if reasoningProtocol != "" {
		reasoning := map[string]any{"protocol": reasoningProtocol}
		if reasoningFormat != "" {
			reasoning["format"] = reasoningFormat
		}
		profile["reasoning"] = reasoning
	}
	body, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	return os.WriteFile(path, body, 0o644)
}

type hfModelInfo struct {
	Siblings []struct {
		RFilename string `json:"rfilename"`
	} `json:"siblings"`
}

// DownloadOpenVINOIR downloads one repository snapshot into a staging directory; an empty revision selects the repository default.
func DownloadOpenVINOIR(ctx context.Context, repo, revision, destDir string, out io.Writer, progress func(DownloadProgress) error) error {
	metadataURL := huggingFaceOrigin + "/api/models/" + repo
	if revision != "" {
		metadataURL += "/revision/" + revision
	} else {
		revision = "main"
	}
	resp, err := modelDownload(ctx, metadataURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var info hfModelInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return fmt.Errorf("decode HF model info: %w", err)
	}
	if len(info.Siblings) == 0 {
		return fmt.Errorf("no files listed for repo %s", repo)
	}
	for _, s := range info.Siblings {
		if s.RFilename == "" {
			continue
		}
		dest, err := archiveutil.SafeJoin(destDir, s.RFilename)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if out != nil {
			if _, err := fmt.Fprintf(out, "  %s\n", s.RFilename); err != nil {
				return err
			}
		}
		if err := downloadFile(ctx, "https://huggingface.co/"+repo+"/resolve/"+revision+"/"+s.RFilename, dest, out, progress); err != nil {
			return fmt.Errorf("download %s: %w", s.RFilename, err)
		}
	}
	return nil
}
