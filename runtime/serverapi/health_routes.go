package serverapi

import (
	"net/http"

	"github.com/contenox/runtime/apiframework"
)

type HealthResponse struct {
	Status string `json:"status"`
}

func AddHealthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		_ = apiframework.Encode(w, r, http.StatusOK, HealthResponse{Status: "ok"})
	})
}
