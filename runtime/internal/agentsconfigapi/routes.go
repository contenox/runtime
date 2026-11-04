package agentsconfigapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/runtime/agentdecl"
	"github.com/contenox/runtime/runtime/project"
)

const (
	ScopeHome      = "home"
	ScopeWorkspace = "workspace"
	maxBodyBytes   = 1 << 20
)

type Deps struct {
	Home    string
	Roots   func() []string
	Applied func(ctx context.Context, scope, workspaceRoot string)
}

type Document struct {
	Scope     string         `json:"scope"`
	Workspace string         `json:"workspace,omitempty"`
	Path      string         `json:"path"`
	Exists    bool           `json:"exists"`
	Revision  string         `json:"revision"`
	Raw       string         `json:"raw"`
	Layer     map[string]any `json:"layer"`
	Problems  []string       `json:"problems"`
	Sections  []SectionInfo  `json:"sections"`
}

type SectionInfo struct {
	Name string                `json:"name"`
	Kind agentdecl.SectionKind `json:"kind"`
}

type Item struct {
	Section   string `json:"section"`
	Name      string `json:"name"`
	Scope     string `json:"scope"`
	Workspace string `json:"workspace,omitempty"`
	InLayer   bool   `json:"inLayer"`
	Layer     any    `json:"layer"`
	Effective any    `json:"effective"`
	Revision  string `json:"revision"`
}

type SectionView struct {
	Section   string                `json:"section"`
	Kind      agentdecl.SectionKind `json:"kind"`
	Scope     string                `json:"scope"`
	Workspace string                `json:"workspace,omitempty"`
	InLayer   bool                  `json:"inLayer"`
	Layer     any                   `json:"layer"`
	Effective any                   `json:"effective"`
	Items     []Item                `json:"items,omitempty"`
	Revision  string                `json:"revision"`
}

type Deleted struct {
	Section  string `json:"section"`
	Name     string `json:"name,omitempty"`
	Revision string `json:"revision"`
}

type rawRequest struct {
	Raw      string `json:"raw"`
	Revision string `json:"revision"`
}

type createRequest struct {
	Name     string `json:"name"`
	Value    any    `json:"value"`
	Revision string `json:"revision"`
}

type valueRequest struct {
	Value    any    `json:"value"`
	Revision string `json:"revision"`
}

type handler struct {
	deps Deps
}

func AddRoutes(mux *http.ServeMux, deps Deps) {
	h := &handler{deps: deps}
	mux.HandleFunc("GET /agents-config", h.getDocument)
	mux.HandleFunc("PUT /agents-config", h.putDocument)
	mux.HandleFunc("GET /agents-config/{section}", h.getSection)
	mux.HandleFunc("POST /agents-config/{section}", h.createItem)
	mux.HandleFunc("PUT /agents-config/{section}", h.putSection)
	mux.HandleFunc("PATCH /agents-config/{section}", h.patchSection)
	mux.HandleFunc("DELETE /agents-config/{section}", h.deleteSection)
	mux.HandleFunc("GET /agents-config/{section}/{name}", h.getItem)
	mux.HandleFunc("PUT /agents-config/{section}/{name}", h.putItem)
	mux.HandleFunc("PATCH /agents-config/{section}/{name}", h.patchItem)
	mux.HandleFunc("DELETE /agents-config/{section}/{name}", h.deleteItem)
}

type target struct {
	scope     string
	workspace string
	path      string
	home      string
}

func (t target) texts(homeRaw, candidate string) []string {
	if t.scope == ScopeHome {
		return []string{candidate}
	}
	return []string{homeRaw, candidate}
}

func sameFolder(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func (h *handler) resolveTarget(r *http.Request) (target, error) {
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	if scope == "" {
		scope = ScopeHome
	}
	home := filepath.Join(h.deps.Home, agentdecl.ConfigFilename)
	switch scope {
	case ScopeHome:
		return target{scope: ScopeHome, path: home, home: home}, nil
	case ScopeWorkspace:
		workspace := strings.TrimSpace(r.URL.Query().Get("workspace"))
		if workspace == "" {
			return target{}, fmt.Errorf("%w: scope=workspace needs workspace=<folder>", apiframework.ErrBadRequest)
		}
		if h.deps.Roots != nil {
			for _, root := range h.deps.Roots() {
				if sameFolder(root, workspace) {
					return target{scope: ScopeWorkspace, workspace: root, path: project.LayerConfigPath(root), home: home}, nil
				}
			}
		}
		return target{}, fmt.Errorf("%w: %q is not a granted workspace folder", apiframework.ErrUnprocessableEntity, workspace)
	}
	return target{}, fmt.Errorf("%w: scope must be %q or %q", apiframework.ErrBadRequest, ScopeHome, ScopeWorkspace)
}

func fail(w http.ResponseWriter, r *http.Request, op apiframework.Operation, base error, format string, args ...any) {
	_ = apiframework.Error(w, r, fmt.Errorf("%w: %s", base, fmt.Sprintf(format, args...)), op)
}

func decodeBody(w http.ResponseWriter, r *http.Request, into any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.UseNumber()
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("%w: %v", apiframework.ErrBadRequest, err)
	}
	return nil
}

func expectedRevision(r *http.Request, inBody string) string {
	if v := strings.Trim(strings.TrimSpace(r.Header.Get("If-Match")), `"`); v != "" {
		return v
	}
	return strings.TrimSpace(inBody)
}

func (h *handler) readHome(t target) (agentdecl.ConfigLayer, error) {
	return agentdecl.ReadConfigLayer(t.home)
}

func (h *handler) effective(t target, layerRaw string) (agentdecl.Config, error) {
	homeRaw := ""
	if t.scope == ScopeWorkspace {
		home, err := h.readHome(t)
		if err != nil {
			return agentdecl.Config{}, err
		}
		homeRaw = home.Raw
	}
	return agentdecl.ConfigFromTexts(t.texts(homeRaw, layerRaw)...)
}

func (h *handler) problemsFor(t target, layerRaw string) []string {
	homeRaw := ""
	if t.scope == ScopeWorkspace {
		if home, err := h.readHome(t); err == nil {
			homeRaw = home.Raw
		}
	}
	return agentdecl.ConfigProblems(t.texts(homeRaw, layerRaw)...)
}

func (h *handler) document(t target, layer agentdecl.ConfigLayer) (Document, error) {
	parsed := map[string]any{}
	if layer.Raw != "" {
		var err error
		if parsed, err = agentdecl.ParseConfigLayer(layer.Raw); err != nil {
			parsed = map[string]any{}
		}
	}
	sections := make([]SectionInfo, 0)
	for _, name := range agentdecl.ConfigSectionNames() {
		kind, _ := agentdecl.SectionKindOf(name)
		sections = append(sections, SectionInfo{Name: name, Kind: kind})
	}
	problems := h.problemsFor(t, layer.Raw)
	if problems == nil {
		problems = []string{}
	}
	return Document{
		Scope: t.scope, Workspace: t.workspace, Path: t.path, Exists: layer.Exists,
		Revision: layer.Revision, Raw: layer.Raw, Layer: parsed, Problems: problems, Sections: sections,
	}, nil
}

func (h *handler) getDocument(w http.ResponseWriter, r *http.Request) {
	t, err := h.resolveTarget(r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}
	layer, err := agentdecl.ReadConfigLayer(t.path)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}
	doc, err := h.document(t, layer)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, doc)
}

func (h *handler) commit(w http.ResponseWriter, r *http.Request, t target, op apiframework.Operation, want string, edit func(raw string) (string, error)) (agentdecl.ConfigLayer, bool) {
	unlock := agentdecl.LockLayerWrites()
	defer unlock()
	layer, err := agentdecl.ReadConfigLayer(t.path)
	if err != nil {
		_ = apiframework.Error(w, r, err, op)
		return agentdecl.ConfigLayer{}, false
	}
	if want != "" && want != layer.Revision {
		fail(w, r, op, apiframework.ErrConflict, "%s changed since you read it (revision %s); read it again and redo the change", t.path, layer.Revision)
		return agentdecl.ConfigLayer{}, false
	}
	next, err := edit(layer.Raw)
	if err != nil {
		var missing *notFoundError
		if errors.As(err, &missing) {
			fail(w, r, op, apiframework.ErrNotFound, "%s", missing.Error())
			return agentdecl.ConfigLayer{}, false
		}
		var exists *existsError
		if errors.As(err, &exists) {
			fail(w, r, op, apiframework.ErrConflict, "%s", exists.Error())
			return agentdecl.ConfigLayer{}, false
		}
		fail(w, r, op, apiframework.ErrUnprocessableEntity, "%v", err)
		return agentdecl.ConfigLayer{}, false
	}
	if fresh := agentdecl.NewConfigProblems(h.problemsFor(t, layer.Raw), h.problemsFor(t, next)); len(fresh) > 0 {
		fail(w, r, op, apiframework.ErrUnprocessableEntity, "this change would break the configuration, so nothing was written: %s", strings.Join(fresh, "; "))
		return agentdecl.ConfigLayer{}, false
	}
	if next != layer.Raw {
		if err := agentdecl.WriteConfigLayer(t.path, next); err != nil {
			_ = apiframework.Error(w, r, err, op)
			return agentdecl.ConfigLayer{}, false
		}
	}
	written, err := agentdecl.ReadConfigLayer(t.path)
	if err != nil {
		_ = apiframework.Error(w, r, err, op)
		return agentdecl.ConfigLayer{}, false
	}
	if next != layer.Raw && h.deps.Applied != nil {
		h.deps.Applied(r.Context(), t.scope, t.workspace)
	}
	return written, true
}

type notFoundError struct{ msg string }

func (e *notFoundError) Error() string { return e.msg }

type existsError struct{ msg string }

func (e *existsError) Error() string { return e.msg }

func (h *handler) putDocument(w http.ResponseWriter, r *http.Request) {
	t, err := h.resolveTarget(r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	var req rawRequest
	if err := decodeBody(w, r, &req); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	if _, err := agentdecl.ParseConfigLayer(req.Raw); err != nil {
		fail(w, r, apiframework.UpdateOperation, apiframework.ErrUnprocessableEntity, "the file is not valid TOML: %v", err)
		return
	}
	layer, ok := h.commit(w, r, t, apiframework.UpdateOperation, expectedRevision(r, req.Revision), func(string) (string, error) {
		return req.Raw, nil
	})
	if !ok {
		return
	}
	doc, err := h.document(t, layer)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, doc)
}

func (h *handler) sectionFromRequest(w http.ResponseWriter, r *http.Request, op apiframework.Operation) (string, agentdecl.SectionKind, bool) {
	section := r.PathValue("section")
	kind, ok := agentdecl.SectionKindOf(section)
	if !ok {
		fail(w, r, op, apiframework.ErrNotFound, "unknown section %q; sections are %s", section, strings.Join(agentdecl.ConfigSectionNames(), ", "))
		return "", "", false
	}
	return section, kind, true
}

func genericMap(v any) (map[string]any, error) {
	encoded, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func (h *handler) itemView(t target, section, name string, layer agentdecl.ConfigLayer, effective map[string]any) (Item, bool) {
	layerValue, inLayer, _ := agentdecl.LayerItem(layer.Raw, section, name)
	effectiveValue, inEffective := effective[name]
	if !inLayer && !inEffective {
		return Item{}, false
	}
	return Item{
		Section: section, Name: name, Scope: t.scope, Workspace: t.workspace,
		InLayer: inLayer, Layer: layerValue, Effective: effectiveValue, Revision: layer.Revision,
	}, true
}

func (h *handler) effectiveSection(t target, layerRaw, section string) (any, error) {
	cfg, err := h.effective(t, layerRaw)
	if err != nil {
		return nil, err
	}
	return agentdecl.EffectiveSection(cfg, section)
}

func (h *handler) getSection(w http.ResponseWriter, r *http.Request) {
	section, kind, ok := h.sectionFromRequest(w, r, apiframework.GetOperation)
	if !ok {
		return
	}
	t, err := h.resolveTarget(r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}
	layer, err := agentdecl.ReadConfigLayer(t.path)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}
	effective, err := h.effectiveSection(t, layer.Raw, section)
	if err != nil {
		fail(w, r, apiframework.GetOperation, apiframework.ErrUnprocessableEntity, "%v", err)
		return
	}
	layerValue, inLayer, err := agentdecl.LayerSection(layer.Raw, section)
	if err != nil {
		fail(w, r, apiframework.GetOperation, apiframework.ErrUnprocessableEntity, "%v", err)
		return
	}
	view := SectionView{
		Section: section, Kind: kind, Scope: t.scope, Workspace: t.workspace,
		InLayer: inLayer, Layer: layerValue, Effective: effective, Revision: layer.Revision,
	}
	if kind != agentdecl.SectionSingleton {
		byName, err := genericMap(effective)
		if err != nil {
			_ = apiframework.Error(w, r, err, apiframework.GetOperation)
			return
		}
		names := make([]string, 0, len(byName))
		for name := range byName {
			names = append(names, name)
		}
		sort.Strings(names)
		view.Items = make([]Item, 0, len(names))
		for _, name := range names {
			if item, found := h.itemView(t, section, name, layer, byName); found {
				view.Items = append(view.Items, item)
			}
		}
		view.Effective = nil
	}
	_ = apiframework.Encode(w, r, http.StatusOK, view)
}

func (h *handler) getItem(w http.ResponseWriter, r *http.Request) {
	section, kind, ok := h.sectionFromRequest(w, r, apiframework.GetOperation)
	if !ok {
		return
	}
	if kind == agentdecl.SectionSingleton {
		fail(w, r, apiframework.GetOperation, apiframework.ErrNotFound, "%s is one settings section, not a list of named items; use /agents-config/%s", section, section)
		return
	}
	t, err := h.resolveTarget(r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}
	layer, err := agentdecl.ReadConfigLayer(t.path)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}
	effective, err := h.effectiveSection(t, layer.Raw, section)
	if err != nil {
		fail(w, r, apiframework.GetOperation, apiframework.ErrUnprocessableEntity, "%v", err)
		return
	}
	byName, err := genericMap(effective)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.GetOperation)
		return
	}
	item, found := h.itemView(t, section, r.PathValue("name"), layer, byName)
	if !found {
		fail(w, r, apiframework.GetOperation, apiframework.ErrNotFound, "%s has no %q", section, r.PathValue("name"))
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, item)
}

func (h *handler) respondItem(w http.ResponseWriter, r *http.Request, t target, section, name string, layer agentdecl.ConfigLayer, status int, op apiframework.Operation) {
	effective, err := h.effectiveSection(t, layer.Raw, section)
	if err != nil {
		_ = apiframework.Error(w, r, err, op)
		return
	}
	byName, err := genericMap(effective)
	if err != nil {
		_ = apiframework.Error(w, r, err, op)
		return
	}
	item, _ := h.itemView(t, section, name, layer, byName)
	item.Section, item.Name, item.Scope, item.Workspace, item.Revision = section, name, t.scope, t.workspace, layer.Revision
	_ = apiframework.Encode(w, r, status, item)
}

func (h *handler) createItem(w http.ResponseWriter, r *http.Request) {
	section, kind, ok := h.sectionFromRequest(w, r, apiframework.CreateOperation)
	if !ok {
		return
	}
	if kind == agentdecl.SectionSingleton {
		fail(w, r, apiframework.CreateOperation, apiframework.ErrBadRequest, "%s is one settings section; replace it with PUT /agents-config/%s", section, section)
		return
	}
	t, err := h.resolveTarget(r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	var req createRequest
	if err := decodeBody(w, r, &req); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.CreateOperation)
		return
	}
	name := strings.TrimSpace(req.Name)
	layer, ok := h.commit(w, r, t, apiframework.CreateOperation, expectedRevision(r, req.Revision), func(raw string) (string, error) {
		if _, exists, _ := agentdecl.LayerItem(raw, section, name); exists {
			return "", &existsError{msg: fmt.Sprintf("%s.%s already exists in %s; change it with PUT", section, name, t.path)}
		}
		return agentdecl.SetLayerItem(raw, section, name, req.Value)
	})
	if !ok {
		return
	}
	h.respondItem(w, r, t, section, name, layer, http.StatusCreated, apiframework.CreateOperation)
}

func (h *handler) putItem(w http.ResponseWriter, r *http.Request) {
	section, kind, ok := h.sectionFromRequest(w, r, apiframework.UpdateOperation)
	if !ok {
		return
	}
	if kind == agentdecl.SectionSingleton {
		fail(w, r, apiframework.UpdateOperation, apiframework.ErrNotFound, "%s is one settings section, not a list of named items; use /agents-config/%s", section, section)
		return
	}
	t, err := h.resolveTarget(r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	var req valueRequest
	if err := decodeBody(w, r, &req); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	name := r.PathValue("name")
	layer, ok := h.commit(w, r, t, apiframework.UpdateOperation, expectedRevision(r, req.Revision), func(raw string) (string, error) {
		if _, exists, _ := agentdecl.LayerItem(raw, section, name); !exists {
			return "", &notFoundError{msg: fmt.Sprintf("%s.%s is not in %s; create it with POST first", section, name, t.path)}
		}
		return agentdecl.SetLayerItem(raw, section, name, req.Value)
	})
	if !ok {
		return
	}
	h.respondItem(w, r, t, section, name, layer, http.StatusOK, apiframework.UpdateOperation)
}

func (h *handler) deleteItem(w http.ResponseWriter, r *http.Request) {
	section, kind, ok := h.sectionFromRequest(w, r, apiframework.DeleteOperation)
	if !ok {
		return
	}
	if kind == agentdecl.SectionSingleton {
		fail(w, r, apiframework.DeleteOperation, apiframework.ErrNotFound, "%s is one settings section, not a list of named items; use /agents-config/%s", section, section)
		return
	}
	t, err := h.resolveTarget(r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.DeleteOperation)
		return
	}
	name := r.PathValue("name")
	layer, ok := h.commit(w, r, t, apiframework.DeleteOperation, expectedRevision(r, ""), func(raw string) (string, error) {
		out, removed, err := agentdecl.DeleteLayerItem(raw, section, name)
		if err != nil {
			return "", err
		}
		if !removed {
			return "", &notFoundError{msg: fmt.Sprintf("%s.%s is not in %s", section, name, t.path)}
		}
		return out, nil
	})
	if !ok {
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, Deleted{Section: section, Name: name, Revision: layer.Revision})
}

func (h *handler) putSection(w http.ResponseWriter, r *http.Request) {
	section, kind, ok := h.sectionFromRequest(w, r, apiframework.UpdateOperation)
	if !ok {
		return
	}
	if kind != agentdecl.SectionSingleton {
		fail(w, r, apiframework.UpdateOperation, apiframework.ErrBadRequest, "%s holds named items; change one with PUT /agents-config/%s/{name}", section, section)
		return
	}
	t, err := h.resolveTarget(r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	var req valueRequest
	if err := decodeBody(w, r, &req); err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	layer, ok := h.commit(w, r, t, apiframework.UpdateOperation, expectedRevision(r, req.Revision), func(raw string) (string, error) {
		return agentdecl.SetLayerSection(raw, section, req.Value)
	})
	if !ok {
		return
	}
	h.respondSection(w, r, t, section, kind, layer, apiframework.UpdateOperation)
}

func (h *handler) respondSection(w http.ResponseWriter, r *http.Request, t target, section string, kind agentdecl.SectionKind, layer agentdecl.ConfigLayer, op apiframework.Operation) {
	effective, err := h.effectiveSection(t, layer.Raw, section)
	if err != nil {
		_ = apiframework.Error(w, r, err, op)
		return
	}
	layerValue, inLayer, _ := agentdecl.LayerSection(layer.Raw, section)
	_ = apiframework.Encode(w, r, http.StatusOK, SectionView{
		Section: section, Kind: kind, Scope: t.scope, Workspace: t.workspace,
		InLayer: inLayer, Layer: layerValue, Effective: effective, Revision: layer.Revision,
	})
}

func (h *handler) deleteSection(w http.ResponseWriter, r *http.Request) {
	section, kind, ok := h.sectionFromRequest(w, r, apiframework.DeleteOperation)
	if !ok {
		return
	}
	if kind != agentdecl.SectionSingleton {
		fail(w, r, apiframework.DeleteOperation, apiframework.ErrBadRequest, "%s holds named items; remove one with DELETE /agents-config/%s/{name}", section, section)
		return
	}
	t, err := h.resolveTarget(r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.DeleteOperation)
		return
	}
	layer, ok := h.commit(w, r, t, apiframework.DeleteOperation, expectedRevision(r, ""), func(raw string) (string, error) {
		out, removed, err := agentdecl.DeleteLayerSection(raw, section)
		if err != nil {
			return "", err
		}
		if !removed {
			return "", &notFoundError{msg: fmt.Sprintf("%s is not in %s", section, t.path)}
		}
		return out, nil
	})
	if !ok {
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, Deleted{Section: section, Revision: layer.Revision})
}

func decodePatch(w http.ResponseWriter, r *http.Request) (any, string, error) {
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/merge-patch+json") {
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		dec.UseNumber()
		var patch any
		if err := dec.Decode(&patch); err != nil {
			return nil, "", fmt.Errorf("%w: %v", apiframework.ErrBadRequest, err)
		}
		return patch, "", nil
	}
	var req valueRequest
	if err := decodeBody(w, r, &req); err != nil {
		return nil, "", err
	}
	return req.Value, req.Revision, nil
}

func (h *handler) patchItem(w http.ResponseWriter, r *http.Request) {
	section, kind, ok := h.sectionFromRequest(w, r, apiframework.UpdateOperation)
	if !ok {
		return
	}
	if kind == agentdecl.SectionSingleton {
		fail(w, r, apiframework.UpdateOperation, apiframework.ErrNotFound, "%s is one settings section, not a list of named items; use PATCH /agents-config/%s", section, section)
		return
	}
	t, err := h.resolveTarget(r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	patch, revision, err := decodePatch(w, r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	name := r.PathValue("name")
	layer, ok := h.commit(w, r, t, apiframework.UpdateOperation, expectedRevision(r, revision), func(raw string) (string, error) {
		if _, exists, _ := agentdecl.LayerItem(raw, section, name); !exists {
			return "", &notFoundError{msg: fmt.Sprintf("%s.%s is not in %s; create it with POST first", section, name, t.path)}
		}
		return agentdecl.PatchLayerItem(raw, section, name, patch)
	})
	if !ok {
		return
	}
	h.respondItem(w, r, t, section, name, layer, http.StatusOK, apiframework.UpdateOperation)
}

func (h *handler) patchSection(w http.ResponseWriter, r *http.Request) {
	section, kind, ok := h.sectionFromRequest(w, r, apiframework.UpdateOperation)
	if !ok {
		return
	}
	if kind != agentdecl.SectionSingleton {
		fail(w, r, apiframework.UpdateOperation, apiframework.ErrBadRequest, "%s holds named items; patch one with PATCH /agents-config/%s/{name}", section, section)
		return
	}
	t, err := h.resolveTarget(r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	patch, revision, err := decodePatch(w, r)
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.UpdateOperation)
		return
	}
	layer, ok := h.commit(w, r, t, apiframework.UpdateOperation, expectedRevision(r, revision), func(raw string) (string, error) {
		return agentdecl.PatchLayerSection(raw, section, patch)
	})
	if !ok {
		return
	}
	h.respondSection(w, r, t, section, kind, layer, apiframework.UpdateOperation)
}
