package mediaapi

import (
	"errors"
	"net/http"
	"path/filepath"

	api "github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/mediageneration"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/vfs"
	"github.com/contenox/runtime/runtime/workspaceevents"
	"github.com/google/uuid"
)

func AddRoutes(mux *http.ServeMux, workspaces workspaceevents.Service, dataRoot string, setup *inferencesetup.Service) {
	mux.HandleFunc("POST /workspace/media/render", func(w http.ResponseWriter, r *http.Request) {
		root, err := workspaces.GrantedRoot(r.URL.Query().Get("root"))
		if err != nil {
			_ = api.Error(w, r, api.Forbidden(err.Error()), api.CreateOperation)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16384)
		req, err := api.Decode[struct {
			mediageneration.OutputOptions
			Kind   string  `json:"kind"`
			Prompt string  `json:"prompt"`
			Model  string  `json:"model"`
			Seed   *uint32 `json:"seed"`
		}](r)
		if err != nil || req.Kind != "image" && req.Kind != "video" {
			if err == nil {
				err = errors.New("choose image or video")
			}
			_ = api.Error(w, r, api.BadRequest(err.Error()), api.CreateOperation)
			return
		}
		options := mediageneration.Options{OutputOptions: req.OutputOptions, Model: req.Model, Prompt: req.Prompt, Video: req.Kind == "video", Seed: 42}
		if req.Seed != nil {
			options.Seed = *req.Seed
		}
		extension := ".png"
		if options.Video {
			extension = ".mp4"
		}
		path := "Media/" + uuid.NewString() + extension
		if _, err := vfs.Contain(root, filepath.FromSlash(path)); err != nil {
			_ = api.Error(w, r, api.Forbidden(err.Error()), api.CreateOperation)
			return
		}
		ctx := modeldconn.WithDataRoot(r.Context(), dataRoot)
		result, err := setup.RenderMedia(ctx, options)
		if err == nil {
			err = mediageneration.WriteNew(ctx, root, path, result.Data)
		}
		if err != nil {
			_ = api.Error(w, r, api.UnprocessableEntity(err.Error()), api.CreateOperation)
			return
		}
		_ = api.Encode(w, r, http.StatusCreated, map[string]any{"file": path, "mime": result.MIME, "execution": result.Execution})
	})
}
