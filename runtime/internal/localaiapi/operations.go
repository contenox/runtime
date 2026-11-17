package localaiapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/autosetup"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/modelacquisition"
	"github.com/contenox/runtime/runtime/modelhardware"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/modelworkers"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/google/uuid"
)

type operation struct {
	ID        string `json:"id"`
	Model     string `json:"model"`
	Action    string `json:"action"`
	Phase     string `json:"phase"`
	Current   int64  `json:"current"`
	Total     int64  `json:"total"`
	Error     string `json:"error,omitempty"`
	Failure   string `json:"failure,omitempty"`
	AccessURL string `json:"accessUrl,omitempty"`
}

func (h *handler) progress(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	reply(w, 200, h.current)
}
func (h *handler) stop(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.current.ID != r.PathValue("id") {
		fail(w, 404, errors.New("Operation no longer current."))
		return
	}
	if h.cancel != nil {
		h.cancel()
	}
	reply(w, 200, h.current)
}

func (h *handler) start(w http.ResponseWriter, r *http.Request) {
	var req struct{ Model, Action, Token string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		fail(w, 400, err)
		return
	}
	if req.Action != "use" && req.Action != "download" {
		fail(w, 400, errors.New("Unknown model action."))
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cancel != nil {
		fail(w, 409, errors.New("A model operation is already running."))
		return
	}
	models, err := h.library(r.Context())
	if err != nil {
		fail(w, 500, err)
		return
	}
	idx := slices.IndexFunc(models, func(m model) bool { return m.Name == req.Model })
	if idx < 0 {
		fail(w, 404, errors.New("Unknown local model."))
		return
	}
	m := models[idx]
	if req.Action == "use" && !m.Chat {
		fail(w, 400, errors.New("Choose a chat model."))
		return
	}
	ctx, cancel := context.WithCancel(h.ctx)
	if req.Token != "" {
		ctx = modelstore.WithDownloadToken(ctx, req.Token)
	}
	h.cancel = cancel
	h.current = operation{ID: uuid.NewString(), Model: m.Name, Action: req.Action, Phase: "prepare"}
	go h.run(ctx, cancel, m, req.Action)
	reply(w, 202, h.current)
}

func (h *handler) run(ctx context.Context, cancel context.CancelFunc, m model, action string) {
	update := func(phase string, current, total int64) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.current.Phase, h.current.Current, h.current.Total = phase, current, total
		return ctx.Err()
	}
	execute := func() error {
		opts := autosetup.Options{DataRoot: h.root, Progress: io.Discard, Report: func(autosetup.Progress) error { return ctx.Err() }}
		provider, err := h.prepare.PrepareLocal(ctx, autosetup.NativePrerequisite{Backend: m.Engine}, opts, h.setup)
		if err != nil {
			return err
		}
		if !m.Installed {
			if err := update("download", 0, m.Bytes); err != nil {
				return err
			}
			var currentFile string
			var completed, previous int64
			err = h.acquisition.Pull(ctx, m.Name, func(p modelacquisition.DownloadProgress) error {
				if p.Artifact != currentFile {
					completed += previous
					currentFile = p.Artifact
				}
				previous = p.Downloaded
				return update("download", completed+p.Downloaded, max(m.Bytes, completed+max(p.Total, p.Downloaded)))
			})
			if err != nil {
				return err
			}
		} else if provider.DiscoveryError != "" {
			return fmt.Errorf("Local model discovery: %s", provider.DiscoveryError)
		}
		if action == "use" {
			if err := update("verify", 0, 0); err != nil {
				return err
			}
			return h.useForChat(ctx, m, provider.ID)
		}
		return nil
	}
	err := execute()
	h.mu.Lock()
	defer h.mu.Unlock()
	defer cancel()
	h.cancel = nil
	switch {
	case err == nil:
		h.current.Phase = "done"
	case errors.Is(ctx.Err(), context.Canceled):
		h.current.Phase = "cancelled"
	default:
		h.current.Failure = h.current.Phase
		if errors.Is(err, admission.ErrCapacity) || errors.Is(err, transport.ErrInsufficientMemory) {
			h.current.Failure = "capacity"
		}
		if errors.Is(err, transport.ErrDeviceBusy) {
			h.current.Failure = "busy"
		}
		h.current.Phase = "failed"
		h.current.Error = err.Error()
		var denied *modelstore.AccessDenied
		if errors.As(err, &denied) {
			h.current.AccessURL = denied.Page
		}
	}
}

func (h *handler) useForChat(ctx context.Context, m model, connectionID string) error {
	const role = inferencesetup.RoleChat
	settings, err := modelworkers.ReadAssignedOptions(ctx, runtimetypes.New(h.db.WithoutTransaction()), role)
	if err != nil {
		return err
	}
	// Give a new chat allocation a resident window. Reserving a second, cold
	// window can exhaust host RAM even when the weights fit on the accelerator.
	proposal := inferencesetup.RouteProposal{
		Assignment:   inferencesetup.Assignment{Role: role, Model: m.Name, Provider: runtimestate.ConnectionSelector(connectionID)},
		RoleRevision: m.primaryRevision, SettingsChange: inferencesetup.PreserveSettings,
	}
	if settings == (modeldconn.WorkerOptions{}) {
		settings.ColdMemory = "0%"
		resident, _ := modeldconn.Status(ctx)
		settings.Context = residentChatContext(resident, m.Name)
		if desc, err := modelregistry.NewCurated().Resolve(ctx, m.Name); settings.Context == 0 && err == nil {
			hw := modelhardware.Detect(ctx)
			settings.Context = min(desc.AutoHotContext(hw.MemoryBytes), modelregistry.MinAgenticHotContext)
		}
		proposal.SettingsChange, proposal.Settings = inferencesetup.ReplaceSettings, settings
	}
	reviewed, err := h.setup.Review(ctx, []inferencesetup.RouteProposal{proposal})
	if err != nil {
		return err
	}
	_, err = h.setup.Apply(ctx, reviewed, inferencesetup.SourceUser)
	return err
}

// Free memory excludes the already-resident model. Reusing its window avoids
// asking the owner to reserve a second copy with a different context size.
func residentChatContext(status transport.DaemonStatus, modelName string) int {
	for _, worker := range status.Workers {
		if worker.State == "ready" && worker.Spec.Role == inferencesetup.RoleChat && worker.Spec.Model == modelName && worker.Spec.Config.NumCtx > 0 {
			return worker.Spec.Config.NumCtx
		}
	}
	return 0
}
