package indexapi

import (
	"net/http"

	"github.com/js402/cate/core/serverops"
	"github.com/js402/cate/core/services/indexservice"
)

func AddIndexRoutes(mux *http.ServeMux, config *serverops.Config, indexService *indexservice.Service) {
	f := &indexManager{
		service: indexService,
	}
	_ = f
	// mux.HandleFunc("POST /files", f.create)
	// mux.HandleFunc("GET /files/{id}", f.getMetadata)
	// mux.HandleFunc("PUT /files/{id}", f.update)
	// mux.HandleFunc("DELETE /files/{id}", f.delete)
	// mux.HandleFunc("GET /files/{id}/download", f.download)
	// mux.HandleFunc("GET /files/paths", f.listPaths)
}

type indexManager struct {
	service *indexservice.Service
}
