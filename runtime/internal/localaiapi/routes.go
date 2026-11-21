// Package localaiapi exposes the desktop's local chat setup and model library.
package localaiapi

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sync"

	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/autosetup"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/modelacquisition"
	"github.com/contenox/runtime/runtime/modeldprobe"
	"github.com/contenox/runtime/runtime/modelhardware"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/modelworkers"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

type handler struct {
	ctx         context.Context
	root        string
	setup       *inferencesetup.Service
	prepare     *autosetup.Service
	acquisition *modelacquisition.Service
	store       *modelstore.Admin
	db          libdbexec.DBManager
	mu          sync.Mutex
	current     operation
	cancel      context.CancelFunc
}

func AddRoutes(ctx context.Context, mux *http.ServeMux, db libdbexec.DBManager, setup *inferencesetup.Service, prepare *autosetup.Service, acquisition *modelacquisition.Service) {
	root := modeldconn.DataRootFromContext(ctx)
	h := &handler{ctx: modeldconn.WithDataRoot(ctx, root), root: root, db: db, setup: setup, prepare: prepare, acquisition: acquisition, store: modelstore.NewAdmin(modelstore.Dir(root, ""))}
	mux.HandleFunc("GET /local-ai/models", h.models)
	mux.HandleFunc("DELETE /local-ai/models/{name}", h.remove)
	mux.HandleFunc("POST /local-ai/imports/{name}", h.importFiles)
	mux.HandleFunc("GET /local-ai/hardware", h.hardware)
	mux.HandleFunc("GET /local-ai/media-devices", h.mediaDevices)
	mux.HandleFunc("GET /local-ai/status", h.status)
	mux.HandleFunc("POST /local-ai/workers/unload", h.unload)
	mux.HandleFunc("GET /local-ai/operations/current", h.progress)
	mux.HandleFunc("POST /local-ai/operations", h.start)
	mux.HandleFunc("DELETE /local-ai/operations/{id}", h.stop)
}

func reply(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, code int, err error) {
	reply(w, code, map[string]string{"message": err.Error()})
}

type model struct {
	LoRA            bool   `json:"lora,omitempty"`
	MediaError      string `json:"mediaError,omitempty"`
	sourceURL       string
	primaryRevision string
	mediaRevision   string
	MediaEnabled    bool                      `json:"mediaEnabled,omitempty"`
	MediaDevice     transport.ExecutionTarget `json:"mediaDevice,omitzero"`
	Name            string                    `json:"name"`
	Label           string                    `json:"label"`
	Engine          string                    `json:"engine"`
	Pipeline        string                    `json:"pipeline,omitempty"`
	Bytes           int64                     `json:"bytes"`
	Installed       bool                      `json:"installed"`
	Chat            bool                      `json:"chat"`
	Assigned        bool                      `json:"assigned"`
	Primary         bool                      `json:"primary"`
}

func (h *handler) library(ctx context.Context) ([]model, error) {
	ctx = modeldconn.WithDataRoot(ctx, h.root)
	entries, err := h.acquisition.Catalog(ctx)
	if err != nil {
		return nil, err
	}
	installed, err := h.store.Inventory()
	if err != nil {
		return nil, err
	}
	config, err := h.setup.Configuration(ctx)
	if err != nil {
		return nil, err
	}
	local := map[string]bool{}
	selectors := map[string]bool{}
	for _, p := range config.Providers {
		if p.URL == "local" && (p.Type == "modeld" || p.Type == "local") {
			local[p.ID] = true
			selectors[p.Selector] = true
		}
	}
	items := map[string]model{}
	for _, entry := range entries {
		items[entry.Name] = model{sourceURL: entry.SourceURL, Name: entry.Name, Label: entry.DisplayLabel, Bytes: entry.SizeBytes + entry.MMProjSizeBytes, Engine: entry.BackendType(), Pipeline: entry.Pipeline, Chat: entry.Pipeline == "" && (!entry.Curated || slices.Contains(inferencesetup.RolesServed(entry), inferencesetup.RoleChat))}
	}
	for _, file := range installed {
		item, known := items[file.Name]
		item.MediaError = file.MediaError
		item.LoRA = file.LoRA
		if !known {
			item.Chat = file.Pipeline == ""
		} // An imported model can be tested before it is selected.
		item.Name, item.Bytes, item.Engine, item.Installed, item.Pipeline = file.Name, file.SizeBytes, file.Type, true, file.Pipeline
		items[file.Name] = item
	}
	var primaryRevision string
	mediaRevisions := make(map[string]string)
	for _, role := range config.Roles {
		mediaRevisions[role.Role] = role.Revision
		if role.Role == inferencesetup.RoleChat {
			primaryRevision = role.Revision
		}
		if item, ok := items[role.Model]; ok && (local[role.ConnectionID] || role.Provider == "modeld" || role.Provider == "local") {
			item.Assigned = true
			if role.Role == "primary" {
				item.Primary = true
			}
			if role.Role == transport.MediaRoleForPipeline(item.Pipeline) {
				item.MediaEnabled = role.Enabled && role.State == "available"
				opts, err := modelworkers.ReadAssignedOptions(ctx, runtimetypes.New(h.db.WithoutTransaction()), role.Role)
				if err != nil {
					return nil, err
				}
				item.MediaDevice = opts.Target.Normalize()
			}
			items[item.Name] = item
		}
	}
	for _, observed := range config.Models {
		item, ok := items[observed.Name]
		if ok && item.Pipeline == "" && observed.Chat && selectors[observed.Provider] {
			item.Chat = true
			items[item.Name] = item
		}
	}
	models := make([]model, 0, len(items))
	for _, m := range items {
		m.primaryRevision = primaryRevision
		m.mediaRevision = mediaRevisions[transport.MediaRoleForPipeline(m.Pipeline)]
		if m.Label == "" {
			m.Label = m.Name
		}
		models = append(models, m)
	}
	slices.SortFunc(models, func(a, b model) int {
		return cmp.Compare(a.Label, b.Label)
	})
	return models, nil
}

func (h *handler) models(w http.ResponseWriter, r *http.Request) {
	models, err := h.library(r.Context())
	if err != nil {
		fail(w, 500, err)
		return
	}
	reply(w, 200, models)
}

func (h *handler) hardware(w http.ResponseWriter, r *http.Request) {
	hw := modelhardware.Detect(r.Context())
	catalog, err := h.acquisition.Catalog(r.Context())
	if err != nil {
		fail(w, 500, err)
		return
	}
	files, err := h.store.Inventory()
	if err != nil {
		fail(w, 500, err)
		return
	}
	onDisk := map[string]bool{}
	for _, file := range files {
		onDisk[file.Name] = true
	}
	h.mu.Lock()
	last := h.current
	h.mu.Unlock()
	skip := ""
	if last.Phase == "failed" && last.Failure == "verify" {
		skip = last.Model
	}
	selected := recommendChat(catalog, hw, onDisk, skip)
	if live, err := modeldconn.Status(modeldconn.WithDataRoot(r.Context(), h.root)); err == nil {
		for _, m := range catalog {
			if m.Name != skip && onDisk[m.Name] && m.SupportsAgenticTools() && residentChatContext(live, m.Name) >= modelregistry.MinAgenticHotContext {
				selected = m.Name
				break
			}
		}
	}
	reply(w, 200, map[string]any{"device": hw.Label, "memory": hw.MemoryBytes, "systemMemory": hw.SystemTotalBytes, "recommendation": selected})
}

// First-run setup only budgets for chat. Prefer an installed model, then the
// smallest complete download that meets the catalog's agent and context bar.
func recommendChat(catalog []modelregistry.ModelDescriptor, hw modelhardware.Hardware, installed map[string]bool, skip string) string {
	var candidates []modelregistry.ModelDescriptor
	for _, m := range catalog {
		if m.Name != skip && m.Curated && m.AutoEligible && m.AutoAcquirable() && m.SupportsAgenticTools() && m.BackendType() == hw.PreferredBackend && m.AutoHotContext(hw.MemoryBytes) >= modelregistry.MinAgenticHotContext {
			candidates = append(candidates, m)
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	slices.SortFunc(candidates, func(a, b modelregistry.ModelDescriptor) int {
		if installed[a.Name] != installed[b.Name] {
			if installed[a.Name] {
				return -1
			}
			return 1
		}
		if bySize := cmp.Compare(a.SizeBytes+a.MMProjSizeBytes, b.SizeBytes+b.MMProjSizeBytes); bySize != 0 {
			return bySize
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return candidates[0].Name
}

func (h *handler) status(w http.ResponseWriter, r *http.Request) {
	ctx := modeldconn.WithDataRoot(r.Context(), h.root)
	st := modeldprobe.New(h.root).Probe(ctx)
	result := map[string]any{"state": st.State.String()}
	if st.State == modeldprobe.StateRunning {
		daemon, err := modeldconn.Status(ctx)
		if err != nil {
			fail(w, 503, err)
			return
		}
		result["workers"] = daemon.Workers
		result["error"] = daemon.LastError
	} else if st.State == modeldprobe.StateUnreachable || st.State == modeldprobe.StateStale {
		result["error"] = st.Err().Error()
	}
	reply(w, 200, result)
}

func (h *handler) unload(w http.ResponseWriter, r *http.Request) {
	var request struct{ Instance string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&request); err != nil || request.Instance == "" {
		fail(w, 400, errors.New("Select a resident worker."))
		return
	}
	ctx := modeldconn.WithDataRoot(r.Context(), h.root)
	workers, err := modelworkers.NewConfigured(ctx, h.db)
	if err != nil {
		fail(w, 500, err)
		return
	}
	defer workers.Close()
	if err = workers.StopIdle(ctx, request.Instance); err != nil {
		fail(w, 409, err)
		return
	}
	reply(w, 200, true)
}

func (h *handler) remove(w http.ResponseWriter, r *http.Request) {
	ctx := modeldconn.WithDataRoot(r.Context(), h.root)
	name := r.PathValue("name")
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cancel != nil {
		fail(w, 409, errors.New("Wait for the current model operation to finish."))
		return
	}
	library, err := h.library(ctx)
	if err != nil {
		fail(w, 500, err)
		return
	}
	idx := slices.IndexFunc(library, func(m model) bool { return m.Name == name && m.Installed })
	if idx < 0 {
		fail(w, 404, errors.New("Model is not installed."))
		return
	}
	if library[idx].Assigned {
		fail(w, 409, errors.New("Choose another model for the assigned role before removing this model."))
		return
	}
	address, err := modeldconn.LocalEndpointAddr(ctx)
	if err != nil {
		fail(w, 503, err)
		return
	}
	client, err := modeldconn.Endpoint(ctx, "local", address)
	if err != nil {
		fail(w, 503, err)
		return
	}
	if err = client.RemoveModel(ctx, name); err != nil {
		fail(w, 409, err)
		return
	}
	config, err := h.setup.Configuration(ctx)
	if err == nil {
		for _, p := range config.Providers {
			if p.URL == "local" && (p.Type == "local" || p.Type == "modeld") {
				if err = h.setup.RefreshProvider(ctx, p.Selector); err != nil {
					break
				}
			}
		}
	}
	if err != nil {
		fail(w, 500, err)
		return
	}
	reply(w, 200, true)
}
