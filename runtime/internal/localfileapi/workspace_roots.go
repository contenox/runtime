package localfileapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	apiframework "github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/runtime/project"
	"github.com/contenox/runtime/runtime/vfs"
	"github.com/contenox/runtime/runtime/workspacegrants"
)

type workspaceRoot struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Default bool   `json:"default"`
	Managed bool   `json:"managed"`
}

type workspaceRootsResponse struct {
	Roots []workspaceRoot `json:"roots"`
}

type addWorkspaceRootRequest struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

type RootsMutators struct {
	Add    func(ctx context.Context, path, name string) error
	Remove func(ctx context.Context, path string) error
	Grants func(ctx context.Context) []string
}

func AddWorkspaceRootsRoutes(mux *http.ServeMux, factory *vfs.Factory, mutators *RootsMutators) {
	if factory == nil {
		return
	}
	mux.HandleFunc("GET /workspace/roots", func(w http.ResponseWriter, r *http.Request) {
		writeRootsResponse(w, r, factory, mutators)
	})
	if mutators == nil {
		return
	}
	if mutators.Add != nil {
		mux.HandleFunc("POST /workspace/roots", func(w http.ResponseWriter, r *http.Request) {
			req, err := apiframework.Decode[addWorkspaceRootRequest](r)
			if err != nil {
				_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
				return
			}
			if strings.TrimSpace(req.Path) == "" {
				_ = apiframework.Error(w, r,
					apiframework.MissingParameter("path", "path is required: name the directory to grant as a workspace root"),
					apiframework.CreateOperation)
				return
			}
			if err := mutators.Add(r.Context(), req.Path, req.Name); err != nil {
				_ = apiframework.Error(w, r, grantError(err), apiframework.CreateOperation)
				return
			}
			writeRootsResponse(w, r, factory, mutators)
		})
	}
	if mutators.Remove != nil {
		mux.HandleFunc("DELETE /workspace/roots", func(w http.ResponseWriter, r *http.Request) {
			path := apiframework.GetQueryParam(r, "path", "", "Workspace root directory to revoke.")
			if strings.TrimSpace(path) == "" {
				_ = apiframework.Error(w, r,
					apiframework.MissingParameter("path", "path is required: name the workspace root to revoke"),
					apiframework.DeleteOperation)
				return
			}
			if err := mutators.Remove(r.Context(), path); err != nil {
				_ = apiframework.Error(w, r, grantError(err), apiframework.DeleteOperation)
				return
			}
			writeRootsResponse(w, r, factory, mutators)
		})
	}
}

func writeRootsResponse(w http.ResponseWriter, r *http.Request, factory *vfs.Factory, mutators *RootsMutators) {
	def := factory.Default()
	roots := factory.Roots()
	managed := managedRootSet(r.Context(), mutators)
	resp := workspaceRootsResponse{Roots: make([]workspaceRoot, len(roots))}
	for i, root := range roots {
		resp.Roots[i] = workspaceRoot{
			Path:    root,
			Name:    project.MarkerName(root),
			Default: root == def,
			Managed: managed[root],
		}
	}
	_ = apiframework.Encode(w, r, http.StatusOK, resp)
}

func managedRootSet(ctx context.Context, mutators *RootsMutators) map[string]bool {
	set := map[string]bool{}
	if mutators == nil || mutators.Grants == nil {
		return set
	}
	for _, g := range mutators.Grants(ctx) {
		if resolved, err := vfs.ResolveRoot(g); err == nil {
			set[resolved] = true
		}
	}
	return set
}

func grantError(err error) error {
	if errors.Is(err, workspacegrants.ErrInvalidGrant) {
		return fmt.Errorf("%w: %s", apiframework.ErrUnprocessableEntity, err.Error())
	}
	return err
}
