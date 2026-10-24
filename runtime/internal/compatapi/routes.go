package compatapi

import (
	"net/http"

	"github.com/contenox/runtime/apiframework/middleware"
	"github.com/contenox/runtime/runtime/agentservice"
	"github.com/contenox/runtime/runtime/stateservice"
	"github.com/contenox/runtime/runtime/taskchainservice"
)

type CompatDeps struct {
	Agent        agentservice.Agent
	Chains       taskchainservice.Service
	StateService stateservice.Service
	Defaults     stateservice.RuntimeDefaults
	Auth         middleware.AuthZReader
	Token        string
}

func AddOpenAIRoutes(mux *http.ServeMux, deps CompatDeps) {
	chat := &chatHandler{deps: deps}
	fim := &fimHandler{deps: deps}

	mux.HandleFunc("POST /openai/v1/chat/completions", chat.handle)
	mux.HandleFunc("POST /openai/{chainID}/v1/chat/completions", chat.handle)
	mux.HandleFunc("POST /openai/v1/fim/completions", fim.handle)
	mux.HandleFunc("POST /openai/{chainID}/v1/fim/completions", fim.handle)
	mux.HandleFunc("POST /openai/v1/completions", fim.handle)
}

func AddRootRoutes(mux *http.ServeMux, deps CompatDeps) {
	chat := &chatHandler{deps: deps}
	fim := &fimHandler{deps: deps}

	mux.HandleFunc("GET /v1/models", rootModels(deps))
	mux.HandleFunc("POST /v1/chat/completions", chat.handle)
	mux.HandleFunc("POST /v1/fim/completions", fim.handle)
	mux.HandleFunc("POST /v1/completions", fim.handle)
}
