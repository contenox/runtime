package chatapi

import (
	"fmt"
	"net/http"

	"github.com/contenox/contenox/core/runtimestate"
	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/services/chatservice"
	"github.com/contenox/contenox/core/taskengine"
	"github.com/google/uuid"
)

func AddChatRoutes(mux *http.ServeMux, _ *serverops.Config, chatManager chatservice.Service, stateService *runtimestate.State) {
	h := &chatManagerHandler{service: chatManager, stateService: stateService}

	mux.HandleFunc("POST /chats", h.createChat)
	mux.HandleFunc("POST /chats/{id}/chat", h.chat)
	mux.HandleFunc("POST /v1/chat/completions", h.openAIChatCompletions)
	mux.HandleFunc("POST /chats/{id}/instruction", h.addInstruction)
	mux.HandleFunc("GET /chats/{id}", h.history)
	mux.HandleFunc("GET /chats", h.listChats)
}

type chatManagerHandler struct {
	service      chatservice.Service
	stateService *runtimestate.State
}

type newChatInstanceRequest struct {
	Model   string `json:"model"`
	Subject string `json:"subject"`
}

func (h *chatManagerHandler) createChat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	req, err := serverops.Decode[newChatInstanceRequest](r)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}
	chatID, err := h.service.NewInstance(ctx, req.Subject, req.Model)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}

	resp := map[string]string{
		"id": chatID,
	}
	_ = serverops.Encode(w, r, http.StatusCreated, resp)
}

type instructionRequest struct {
	Instruction string `json:"instruction"`
}

func (h *chatManagerHandler) addInstruction(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := r.PathValue("id")
	chatID, err := uuid.Parse(idStr)
	if err != nil {
		_ = serverops.Error(w, r, fmt.Errorf("id parsing error: %w: %w", err, serverops.ErrBadPathValue), serverops.CreateOperation)
		return
	}

	req, err := serverops.Decode[instructionRequest](r)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}

	err = h.service.AddInstruction(ctx, chatID.String(), req.Instruction)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}

	resp := map[string]string{}
	_ = serverops.Encode(w, r, http.StatusOK, resp)
}

type chatRequest struct {
	Message string   `json:"message"`
	Models  []string `json:"models"`
}

func (h *chatManagerHandler) chat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := r.PathValue("id")
	chatID, err := uuid.Parse(idStr)
	if err != nil {
		_ = serverops.Error(w, r, fmt.Errorf("id parsing error: %w: %w", err, serverops.ErrBadPathValue), serverops.ServerOperation)
		return
	}

	req, err := serverops.Decode[chatRequest](r)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}
	if req.Message == "" {
		_ = serverops.Error(w, r, fmt.Errorf("message is required"), serverops.CreateOperation)
		return
	}
	if len(req.Models) == 0 {
		req.Models = []string{}
	}
	reply, _, _, err := h.service.Chat(ctx, chatID.String(), req.Message, req.Models...)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}

	resp := map[string]string{
		"response": reply,
	}
	_ = serverops.Encode(w, r, http.StatusOK, resp)
}

func (h *chatManagerHandler) openAIChatCompletions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	oaiReq, err := serverops.Decode[taskengine.OpenAIChatRequest](r)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}

	// Validate at least one message exists
	if len(oaiReq.Messages) == 0 {
		_ = serverops.Error(w, r, fmt.Errorf("at least one message required"), serverops.CreateOperation)
		return
	}

	resp, err := h.service.OpenAIChatCompletions(ctx, oaiReq)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}

	_ = serverops.Encode(w, r, http.StatusOK, resp)
}

func (h *chatManagerHandler) history(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := r.PathValue("id")
	chatID, err := uuid.Parse(idStr)
	if err != nil {
		_ = serverops.Error(w, r, fmt.Errorf("id parsing error: %w: %w", err, serverops.ErrBadPathValue), serverops.ListOperation)
		return
	}

	history, err := h.service.GetChatHistory(ctx, chatID.String())
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.GetOperation)
		return
	}

	_ = serverops.Encode(w, r, http.StatusOK, history)
}

func (h *chatManagerHandler) listChats(w http.ResponseWriter, r *http.Request) {
	chats, err := h.service.ListChats(r.Context())
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.ListOperation)
		return
	}

	_ = serverops.Encode(w, r, http.StatusOK, chats)
}
