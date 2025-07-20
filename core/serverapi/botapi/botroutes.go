package botapi

import (
	"fmt"
	"net/http"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/core/services/botservice"
	"github.com/google/uuid"
)

func AddBotRoutes(mux *http.ServeMux, botService botservice.Service) {
	s := &botAPIService{service: botService}

	mux.HandleFunc("POST /bots", s.create)
	mux.HandleFunc("GET /bots", s.list)
	mux.HandleFunc("GET /bots/{id}", s.get)
	mux.HandleFunc("PUT /bots/{id}", s.update)
	mux.HandleFunc("DELETE /bots/{id}", s.delete)
}

type botAPIService struct {
	service botservice.Service
}

func (s *botAPIService) create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	bot, err := serverops.Decode[store.Bot](r)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}

	// Generate ID
	bot.ID = uuid.NewString()

	if err := s.service.CreateBot(ctx, &bot); err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}

	_ = serverops.Encode(w, r, http.StatusCreated, bot)
}

func (s *botAPIService) get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if id == "" {
		serverops.Error(w, r, fmt.Errorf("bot ID is required: %w", serverops.ErrBadPathValue), serverops.GetOperation)
		return
	}

	bot, err := s.service.GetBot(ctx, id)
	if err != nil {
		serverops.Error(w, r, err, serverops.GetOperation)
		return
	}

	serverops.Encode(w, r, http.StatusOK, bot)
}

func (s *botAPIService) list(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	bots, err := s.service.ListBots(ctx)
	if err != nil {
		serverops.Error(w, r, err, serverops.ListOperation)
		return
	}

	type ListResponse struct {
		Object string       `json:"object"`
		Data   []*store.Bot `json:"data"`
	}

	response := ListResponse{
		Object: "list",
		Data:   bots,
	}

	serverops.Encode(w, r, http.StatusOK, response)
}

func (s *botAPIService) update(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if id == "" {
		serverops.Error(w, r, fmt.Errorf("bot ID is required: %w", serverops.ErrBadPathValue), serverops.UpdateOperation)
		return
	}

	bot, err := serverops.Decode[store.Bot](r)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.UpdateOperation)
		return
	}

	// Ensure the ID in the path matches the bot's ID
	if bot.ID != id {
		serverops.Error(w, r, fmt.Errorf("bot ID mismatch"), serverops.UpdateOperation)
		return
	}

	if err := s.service.UpdateBot(ctx, &bot); err != nil {
		_ = serverops.Error(w, r, err, serverops.UpdateOperation)
		return
	}

	_ = serverops.Encode(w, r, http.StatusOK, bot)
}

func (s *botAPIService) delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if id == "" {
		serverops.Error(w, r, fmt.Errorf("bot ID is required: %w", serverops.ErrBadPathValue), serverops.DeleteOperation)
		return
	}

	if err := s.service.DeleteBot(ctx, id); err != nil {
		_ = serverops.Error(w, r, err, serverops.DeleteOperation)
		return
	}

	_ = serverops.Encode(w, r, http.StatusOK, "bot removed")
}
