package hitlpolicyapi

import (
	"encoding/json"
	"fmt"
	"github.com/contenox/runtime/runtime/project"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/localfileservice"
)

func AddRoutes(mux *http.ServeMux, files localfileservice.Service, policies hitlservice.PolicySource) {
	h := &handler{files: files, policies: policies}
	mux.HandleFunc("GET /hitl-policies/list", h.listPolicies)
	mux.HandleFunc("GET /hitl-policies", h.getPolicy)
	mux.HandleFunc("POST /hitl-policies", h.createPolicy)
	mux.HandleFunc("PUT /hitl-policies", h.updatePolicy)
	mux.HandleFunc("DELETE /hitl-policies", h.deletePolicy)
}

type handler struct {
	files    localfileservice.Service
	policies hitlservice.PolicySource
}

func normalizePolicyName(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("%w: query parameter name is required", apiframework.ErrBadRequest)
	}
	name, err := localfileservice.NormalizeRelPath(raw, false)
	if err != nil {
		return "", fmt.Errorf("%w: %s", apiframework.ErrBadRequest, err.Error())
	}
	base := filepath.Base(name)
	if !strings.HasPrefix(base, "hitl-policy") || !strings.HasSuffix(base, ".json") {
		return "", fmt.Errorf("%w: policy name must match hitl-policy*.json", apiframework.ErrBadRequest)
	}
	return name, nil
}

func (h *handler) listPolicies(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.policies.(hitlservice.PolicyLister); ok {
		_ = apiframework.Encode(w, r, http.StatusOK, hitlservice.ChoiceFiles(hitlservice.ChoicesFor(h.policies, workspaceIDFromRequest(r))))
		return
	}
	files, err := h.files.List(r.Context(), ".")
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	names := []string{}
	for _, f := range files {
		if f.IsDirectory {
			continue
		}
		base := filepath.Base(f.Path)
		if strings.HasPrefix(base, "hitl-policy") && strings.HasSuffix(base, ".json") {
			names = append(names, f.Path)
		}
	}
	_ = apiframework.Encode(w, r, http.StatusOK, names)
}

func (h *handler) getPolicy(w http.ResponseWriter, r *http.Request) {
	name, err := normalizePolicyName(apiframework.GetQueryParam(r, "name", "", "Policy filename, e.g. hitl-policy-strict.json."))
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}
	data, err := h.readPolicy(r, name)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}
	var policy hitlservice.Policy
	if err := json.Unmarshal(data, &policy); err != nil {
		_ = apiframework.Error(w, r, fmt.Errorf("invalid policy JSON: %w", err), apiframework.GetOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, policy)
}

func (h *handler) createPolicy(w http.ResponseWriter, r *http.Request) {
	name, err := normalizePolicyName(apiframework.GetQueryParam(r, "name", "", "Policy filename, e.g. hitl-policy-custom.json."))
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	policy, data, err := decodePolicy(r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	if _, err := h.files.Write(r.Context(), name, data, true); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusCreated, policy)
}

func (h *handler) updatePolicy(w http.ResponseWriter, r *http.Request) {
	name, err := normalizePolicyName(apiframework.GetQueryParam(r, "name", "", "Policy filename, e.g. hitl-policy-strict.json."))
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	if _, err := h.files.Stat(r.Context(), name); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	policy, data, err := decodePolicy(r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	if _, err := h.files.Write(r.Context(), name, data, false); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, policy)
}

func (h *handler) deletePolicy(w http.ResponseWriter, r *http.Request) {
	name, err := normalizePolicyName(apiframework.GetQueryParam(r, "name", "", "Policy filename, e.g. hitl-policy-custom.json."))
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.DeleteOperation)
		return
	}
	if err := h.files.Delete(r.Context(), name); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.DeleteOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, fmt.Sprintf("policy %s deleted", name))
}

func decodePolicy(r *http.Request) (hitlservice.Policy, []byte, error) {
	policy, err := apiframework.Decode[hitlservice.Policy](r)
	if err != nil {
		return hitlservice.Policy{}, nil, err
	}
	data, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return hitlservice.Policy{}, nil, err
	}
	return policy, data, nil
}

func (h *handler) readPolicy(r *http.Request, name string) ([]byte, error) {
	if h.policies != nil {
		if data, err := h.policies.ReadPolicy(r.Context(), "", hitlservice.ResolveForWorkspace(h.policies, workspaceIDFromRequest(r), filepath.ToSlash(name))); err == nil {
			return data, nil
		}
	}
	data, _, err := h.files.Read(r.Context(), name)
	return data, err
}

func workspaceIDFromRequest(r *http.Request) string {
	path := strings.TrimSpace(r.URL.Query().Get("workspace"))
	if path == "" {
		return ""
	}
	return project.LayerKeyOf(path)
}
