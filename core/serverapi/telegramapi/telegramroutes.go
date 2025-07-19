package telegramapi

import (
	"fmt"
	"net/http"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/core/services/telegramservice"
)

func AddTelegramRoutes(mux *http.ServeMux, telegramService telegramservice.Service) {
	s := &telegramHandler{service: telegramService}

	mux.HandleFunc("POST /telegram-frontends", s.create)
	mux.HandleFunc("PUT /telegram-frontends/{id}", s.update)
	mux.HandleFunc("GET /telegram-frontends/{id}", s.get)
	mux.HandleFunc("DELETE /telegram-frontends/{id}", s.delete)
	mux.HandleFunc("GET /telegram-frontends", s.list)
	mux.HandleFunc("GET /telegram-frontends/users/{userId}", s.listByUser)
}

type telegramHandler struct {
	service telegramservice.Service
}

type TelegramFrontendDAO struct {
	Description  string `json:"description"`
	BotToken     string `json:"botToken"`
	SyncInterval int    `json:"syncInterval"`
	LastOffset   int    `json:"lastOffset"`
}

func (h *telegramHandler) create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	frontend, err := serverops.Decode[TelegramFrontendDAO](r)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}

	id, err := serverops.GetIdentity(ctx)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}
	f := store.TelegramFrontend{
		UserID:       id,
		BotToken:     frontend.BotToken,
		Description:  frontend.Description,
		LastOffset:   frontend.LastOffset,
		SyncInterval: frontend.SyncInterval,
	}

	if err := h.service.Create(ctx, &f); err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}

	_ = serverops.Encode(w, r, http.StatusCreated, frontend)
}

func (h *telegramHandler) update(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if id == "" {
		serverops.Error(w, r, fmt.Errorf("ID is required: %w", serverops.ErrBadPathValue), serverops.UpdateOperation)
		return
	}

	frontend, err := serverops.Decode[store.TelegramFrontend](r)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.UpdateOperation)
		return
	}
	frontend.ID = id

	if err := h.service.Update(ctx, &frontend); err != nil {
		_ = serverops.Error(w, r, err, serverops.UpdateOperation)
		return
	}

	_ = serverops.Encode(w, r, http.StatusOK, frontend)
}

func (h *telegramHandler) get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if id == "" {
		serverops.Error(w, r, fmt.Errorf("ID is required: %w", serverops.ErrBadPathValue), serverops.GetOperation)
		return
	}

	frontend, err := h.service.Get(ctx, id)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.GetOperation)
		return
	}

	frontend.BotToken = "***-***"

	_ = serverops.Encode(w, r, http.StatusOK, frontend)
}

func (h *telegramHandler) delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if id == "" {
		serverops.Error(w, r, fmt.Errorf("ID is required: %w", serverops.ErrBadPathValue), serverops.DeleteOperation)
		return
	}

	if err := h.service.Delete(ctx, id); err != nil {
		_ = serverops.Error(w, r, err, serverops.DeleteOperation)
		return
	}

	_ = serverops.Encode(w, r, http.StatusOK, "Telegram frontend deleted")
}

func (h *telegramHandler) list(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	frontends, err := h.service.List(ctx)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.ListOperation)
		return
	}

	for _, tf := range frontends {
		tf.BotToken = "***-***"
	}

	_ = serverops.Encode(w, r, http.StatusOK, frontends)
}

func (h *telegramHandler) listByUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := r.PathValue("userId")
	if userID == "" {
		serverops.Error(w, r, fmt.Errorf("user ID is required: %w", serverops.ErrBadPathValue), serverops.ListOperation)
		return
	}

	frontends, err := h.service.ListByUser(ctx, userID)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.ListOperation)
		return
	}
	for _, tf := range frontends {
		tf.BotToken = "***-***"
	}
	_ = serverops.Encode(w, r, http.StatusOK, frontends)
}
