package workspacetriggersapi

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strconv"

	api "github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/runtime/workflows"
)

type workflowRequest struct {
	Revision string          `json:"revision"`
	Source   string          `json:"source"`
	Event    json.RawMessage `json:"event"`
	Decision string          `json:"decision"`
}

func (h *handler) workflowRoutes(mux *http.ServeMux) {
	for _, pattern := range []string{"GET /workspace/workflows", "GET /workspace/workflows/{name}", "PUT /workspace/workflows/{name}", "POST /workspace/workflows/{name}/run", "GET /workspace/workflow-runs", "GET /workspace/workflow-runs/{id}", "POST /workspace/workflow-runs/{id}/decision", "GET /workspace/workflow-runs/{id}/pdf"} {
		mux.HandleFunc(pattern, h.workflow)
	}
}
func (h *handler) workflow(w http.ResponseWriter, r *http.Request) {
	root, _, err := h.scope(r)
	if err != nil {
		_ = api.Error(w, r, err, api.ListOperation)
		return
	}
	service := h.deps.Workflows
	ctx := r.Context()
	name, id := r.PathValue("name"), r.PathValue("id")
	var body workflowRequest
	if r.Method != http.MethodGet {
		r.Body = http.MaxBytesReader(w, r.Body, 512<<10)
		body, err = api.Decode[workflowRequest](r)
		if err != nil {
			_ = api.Error(w, r, err, api.UpdateOperation)
			return
		}
	}
	var out any
	switch r.Pattern {
	case "GET /workspace/workflows":
		out, err = workflows.Definitions(root)
	case "GET /workspace/workflows/{name}":
		out, err = workflows.Load(root, name)
	case "PUT /workspace/workflows/{name}":
		out, err = service.Save(root, workflows.Definition{Name: name, Source: body.Source, Revision: body.Revision})
	case "POST /workspace/workflows/{name}/run":
		out, err = service.Run(ctx, root, workflows.Invocation{Name: name, Revision: body.Revision, Origin: "manual", Payload: body.Event})
	case "GET /workspace/workflow-runs":
		out, err = service.Recent(ctx, root)
	case "GET /workspace/workflow-runs/{id}":
		out, err = service.Get(ctx, root, id)
	case "POST /workspace/workflow-runs/{id}/decision":
		out, err = service.Decide(ctx, root, id, body.Decision)
	case "GET /workspace/workflow-runs/{id}/pdf":
		var run workflows.Run
		run, err = service.Get(ctx, root, id)
		if err == nil {
			err = fs.ErrNotExist
			for _, c := range run.Changes {
				if c.Path == r.URL.Query().Get("file") && len(c.Binary) > 0 {
					w.Header().Set("Content-Type", "application/pdf")
					w.Header().Set("Content-Disposition", "inline; filename="+strconv.Quote(path.Base(c.Path)))
					w.Header().Set("Cache-Control", "no-store")
					w.Write(c.Binary)
					return
				}
			}
		}
	}
	if err != nil {
		switch {
		case errors.Is(err, workflows.ErrConflict):
			err = api.Conflict(err.Error())
		case errors.Is(err, fs.ErrNotExist):
			err = api.NotFound(err.Error())
		default:
			err = api.UnprocessableEntity(err.Error())
		}
		_ = api.Error(w, r, err, api.UpdateOperation)
		return
	}
	if run, ok := out.(workflows.Run); ok {
		for i := range run.Changes {
			run.Changes[i].Binary = nil
		}
		out = run
	}
	_ = api.Encode(w, r, http.StatusOK, out)
}
