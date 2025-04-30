package indexapi

import (
	"net/http"

	"github.com/js402/cate/core/serverops"
	"github.com/js402/cate/core/services/indexservice"
)

func AddIndexRoutes(mux *http.ServeMux, _ *serverops.Config, indexService *indexservice.Service) {
	f := &indexManager{
		service: indexService,
	}
	mux.HandleFunc("POST /index", f.index)
	mux.HandleFunc("POST /search", f.search)
}

type indexManager struct {
	service *indexservice.Service
}

func (im *indexManager) index(w http.ResponseWriter, r *http.Request) {
	req, err := serverops.Decode[indexservice.IndexRequest](r)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}

	resp, err := im.service.Index(r.Context(), &req)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}
	_ = serverops.Encode(w, r, http.StatusOK, resp)
}

func (im *indexManager) search(w http.ResponseWriter, r *http.Request) {
	req, err := serverops.Decode[indexservice.SearchRequest](r)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.GetOperation)
		return
	}

	resp, err := im.service.Search(r.Context(), &req)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.GetOperation)
		return
	}
	_ = serverops.Encode(w, r, http.StatusOK, resp)
}
