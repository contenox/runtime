package fleetapi

import (
	"context"
	"net/http"

	apiframework "github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/runtime/presence"
)

type PresenceReader interface {
	List(ctx context.Context) ([]presence.Entry, error)
}

func AddPresenceRoutes(mux *http.ServeMux, reader PresenceReader) {
	h := &presenceHandler{reader: reader}
	mux.HandleFunc("GET /fleet/presence", h.list)
}

type presenceHandler struct {
	reader PresenceReader
}

func (h *presenceHandler) list(w http.ResponseWriter, r *http.Request) {
	entries, err := h.reader.List(r.Context())
	if err != nil {
		_ = apiframework.Error(w, r, err, apiframework.ListOperation)
		return
	}
	_ = apiframework.Encode(w, r, http.StatusOK, entries)
}
