package accessapi

import (
	"fmt"
	"net/http"
	"strings"

	apiframework "github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/runtime/accessview"
	"github.com/contenox/runtime/runtime/internal/localfileapi"
	"github.com/contenox/runtime/runtime/vfs"
)

const maxBatchPaths = 2000

type EvaluateRequest struct {
	Paths []string `json:"paths"`
}

type EvaluateResponse struct {
	PolicyName string                   `json:"policyName"`
	Verdicts   []accessview.PathVerdict `json:"verdicts"`
}

func AddRoutes(mux *http.ServeMux, factory *vfs.Factory, hitlFor localfileapi.PolicyEvaluatorFactory) {
	if factory == nil {
		return
	}
	h := &handler{factory: factory, hitlFor: hitlFor}
	mux.HandleFunc("POST /workspace/access", h.evaluate)
}

type handler struct {
	factory *vfs.Factory
	hitlFor localfileapi.PolicyEvaluatorFactory
}

func (h *handler) evaluate(w http.ResponseWriter, r *http.Request) {
	req, err := apiframework.Decode[EvaluateRequest](r)
	if err != nil {
		_ = apiframework.Error(w, r, fmt.Errorf("%w: %v", apiframework.ErrBadRequest, err), apiframework.CreateOperation)
		return
	}
	if len(req.Paths) > maxBatchPaths {
		_ = apiframework.Error(w, r,
			fmt.Errorf("%w: batch of %d paths exceeds the %d-path limit", apiframework.ErrUnprocessableEntity, len(req.Paths), maxBatchPaths),
			apiframework.CreateOperation)
		return
	}

	root := apiframework.GetQueryParam(r, "root", "", "Workspace root the request operates in: a granted root (or a directory under one); empty or \"/\" resolves to the default (first-configured) root.")
	resolved, ok := h.factory.Allows(root)
	if !ok {
		_ = apiframework.Error(w, r,
			fmt.Errorf("%w: workspace root %q is not under any configured workspace root; roots: %s",
				apiframework.ErrUnprocessableEntity, root, h.factory.DescribeRoots()),
			apiframework.CreateOperation)
		return
	}
	if h.hitlFor == nil {
		_ = apiframework.Error(w, r,
			fmt.Errorf("%w: workspace access evaluation is not available on this deployment", apiframework.ErrUnprocessableEntity),
			apiframework.CreateOperation)
		return
	}

	view, err := vfs.OpenView(resolved)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ServerOperation)
		return
	}

	policyName := strings.TrimSpace(apiframework.GetQueryParam(r, "policy", "", "HITL policy name to evaluate against; omitted uses the runtime's default policy resolution (the same resolution the live agent uses)."))

	ev := accessview.NewEvaluator(view, h.hitlFor(policyName))
	resolvedPolicy, verdicts := ev.Evaluate(r.Context(), req.Paths)

	_ = apiframework.Encode(w, r, http.StatusOK, EvaluateResponse{
		PolicyName: resolvedPolicy,
		Verdicts:   verdicts,
	})
}
