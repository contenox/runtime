package localfileapi

import (
	"fmt"
	"net/http"
	"sync"

	apiframework "github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/runtime/localfileservice"
	"github.com/contenox/runtime/runtime/vfs"
)

func AddWorkspaceRoutes(mux *http.ServeMux, factory *vfs.Factory, hitlFor PolicyEvaluatorFactory) error {
	if factory == nil {
		return fmt.Errorf("localfileapi: workspace factory is nil")
	}
	wh := &workspaceHandler{
		factory:  factory,
		services: map[string]localfileservice.Service{},
		filters:  defaultFilters(),
		hitlFor:  hitlFor,
	}
	for _, root := range factory.Roots() {
		if _, err := wh.serviceFor(root); err != nil {
			return fmt.Errorf("localfileapi: workspace root %q: %w", root, err)
		}
	}

	mux.HandleFunc("GET /files", wh.wrap((*handler).list))
	mux.HandleFunc("GET /files/stat", wh.wrap((*handler).stat))
	mux.HandleFunc("GET /files/content", wh.wrap((*handler).content))
	mux.HandleFunc("GET /files/download", wh.wrap((*handler).download))
	mux.HandleFunc("POST /files", wh.wrap((*handler).createFile))
	mux.HandleFunc("PUT /files", wh.wrap((*handler).updateFile))
	mux.HandleFunc("DELETE /files", wh.wrap((*handler).deleteFile))
	mux.HandleFunc("PUT /files/move", wh.wrap((*handler).movePath))
	mux.HandleFunc("POST /folders", wh.wrap((*handler).createFolder))
	mux.HandleFunc("DELETE /folders", wh.wrap((*handler).deleteFolder))
	return nil
}

type workspaceHandler struct {
	factory *vfs.Factory

	filters map[string]FileFilter
	hitlFor PolicyEvaluatorFactory

	mu       sync.Mutex
	services map[string]localfileservice.Service
}

func (wh *workspaceHandler) serviceFor(resolvedRoot string) (localfileservice.Service, error) {
	wh.mu.Lock()
	defer wh.mu.Unlock()
	if svc, ok := wh.services[resolvedRoot]; ok {
		return svc, nil
	}
	svc, err := localfileservice.New(resolvedRoot)
	if err != nil {
		return nil, err
	}
	wh.services[resolvedRoot] = svc
	return svc, nil
}

func (wh *workspaceHandler) wrap(fn func(*handler, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		root := apiframework.GetQueryParam(r, "root", "", "Workspace root the request operates in: a granted root (or a directory under one); empty or \"/\" resolves to the default (first-configured) root.")
		resolved, ok := wh.factory.Allows(root)
		if !ok {
			_ = apiframework.Error(w, r,
				fmt.Errorf("%w: workspace root %q is not under any configured workspace root; roots: %s",
					apiframework.ErrUnprocessableEntity, root, wh.factory.DescribeRoots()),
				apiframework.ListOperation)
			return
		}
		svc, err := wh.serviceFor(resolved)
		if err != nil {
			_ = apiframework.Error(w, r, err, apiframework.ListOperation)
			return
		}
		view, _ := vfs.OpenView(svc.Root())
		fn(&handler{service: svc, view: view, filters: wh.filters, hitlFor: wh.hitlFor}, w, r)
	}
}
