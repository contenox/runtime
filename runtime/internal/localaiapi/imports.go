package localaiapi

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/contenox/runtime/internal/archiveutil"
	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/autosetup"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/google/uuid"
)

var importName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,95}$`)

func (h *handler) importFiles(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	engine := r.URL.Query().Get("engine")
	if !importName.MatchString(name) || !filepath.IsLocal(name) || strings.HasSuffix(name, ".") || (engine != "llama" && engine != "openvino") {
		fail(w, 400, errors.New("Choose a model name and a supported engine."))
		return
	}
	if r.Header.Get("X-Contenox-Import") != "1" {
		fail(w, 400, errors.New("Missing model upload header."))
		return
	}
	parts, err := r.MultipartReader()
	if err != nil {
		fail(w, 400, errors.New("Select the model files to import."))
		return
	}
	h.mu.Lock()
	if h.cancel != nil {
		h.mu.Unlock()
		fail(w, 409, errors.New("A model operation is already running."))
		return
	}
	models, err := h.library(r.Context())
	if err != nil {
		h.mu.Unlock()
		fail(w, 500, err)
		return
	}
	if slices.ContainsFunc(models, func(m model) bool { return strings.EqualFold(m.Name, name) }) {
		h.mu.Unlock()
		fail(w, 409, errors.New("A model with this name already exists."))
		return
	}
	ctx, cancel := context.WithCancel(modeldconn.WithDataRoot(r.Context(), h.root))
	h.cancel = cancel
	h.current = operation{ID: uuid.NewString(), Model: name, Action: "import", Phase: "download", Total: r.ContentLength}
	h.mu.Unlock()
	stop := context.AfterFunc(ctx, func() { _ = r.Body.Close() })
	defer stop()
	err = h.installUpload(ctx, name, engine, parts)
	reply(w, 200, h.finish(ctx, cancel, err))
}

func (h *handler) installUpload(ctx context.Context, name, engine string, parts *multipart.Reader) error {
	dir, err := os.MkdirTemp(h.root, ".model-import-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := stageModelFiles(dir, engine, parts, func(n int64) {
		h.mu.Lock()
		h.current.Current += n
		h.mu.Unlock()
	}); err != nil {
		return err
	}
	h.mu.Lock()
	h.current.Phase = "prepare"
	h.mu.Unlock()
	provider, err := h.prepare.PrepareLocal(ctx, autosetup.NativePrerequisite{Backend: engine}, autosetup.Options{DataRoot: h.root, Progress: io.Discard, Report: func(autosetup.Progress) error { return ctx.Err() }}, h.setup)
	if err != nil {
		return err
	}
	in, out := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := archiveutil.WriteTar(ctx, dir, out)
		_ = out.CloseWithError(err)
		done <- err
	}()
	_, err = modeldconn.PushModelTarget(ctx, modeldconn.ModeldTarget{}, transport.PushManifest{Name: name, Type: engine, Format: transport.PushFormatTar}, in)
	_ = in.CloseWithError(err)
	if err := errors.Join(err, <-done); err != nil {
		return err
	}
	return h.setup.RefreshProvider(ctx, runtimestate.ConnectionSelector(provider.ID))
}

type uploadCounter func(int64)

func (count uploadCounter) Write(p []byte) (int, error) { count(int64(len(p))); return len(p), nil }

func stageModelFiles(dir, engine string, parts *multipart.Reader, count uploadCounter) error {
	for {
		part, err := parts.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name := part.FormName()
		segments := strings.Split(name, "/")
		if len(segments) > 2 || (engine != "openvino" && len(segments) != 1) {
			return errors.New("Invalid model bundle filename.")
		}
		for _, segment := range segments {
			if !importName.MatchString(segment) || !filepath.IsLocal(segment) || strings.HasSuffix(segment, ".") {
				return errors.New("Invalid model bundle filename.")
			}
		}
		if engine == "llama" && name != "model.gguf" && name != "mmproj.gguf" && name != "chat_template.jinja" && name != modelstore.LoRAFile && name != modelstore.LoRASettingsFile {
			return errors.New("Unexpected file in the GGUF model bundle.")
		}
		reader := bufio.NewReader(part)
		if strings.HasSuffix(name, ".gguf") {
			header, err := reader.Peek(8)
			if err != nil || !bytes.Equal(header[:4], []byte("GGUF")) {
				return errors.New("The selected file has no GGUF header.")
			}
		}
		destination := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return err
		}
		file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(file, io.TeeReader(reader, count))
		if err := errors.Join(copyErr, file.Close()); err != nil {
			return err
		}
	}
	nonempty := func(name string) bool {
		stat, err := os.Stat(filepath.Join(dir, name))
		return err == nil && stat.Size() > 0
	}
	if engine == "llama" && !nonempty("model.gguf") {
		return errors.New("The bundle needs model.gguf.")
	}
	if engine == "llama" {
		adapter, err := modelstore.BundledLoRA(filepath.Join(dir, "model.gguf"))
		if err != nil {
			return err
		}
		if adapter == nil && nonempty(modelstore.LoRASettingsFile) {
			return errors.New("LoRA strength requires an adapter.gguf file")
		}
	}
	if engine == "openvino" && !(nonempty("openvino_model.xml") && nonempty("openvino_model.bin") || nonempty("openvino_language_model.xml") && nonempty("openvino_language_model.bin")) {
		if nonempty("model_index.json") {
			_, err := modelstore.Resolve(filepath.Dir(dir), filepath.Base(dir), engine, "")
			return err
		}
		return errors.New("The OpenVINO bundle needs matching model XML and BIN files.")
	}
	return nil
}
